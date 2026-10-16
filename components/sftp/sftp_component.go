package sftp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	gocamel "github.com/tranchida/gocamel"
)

// SFTPComponent represents the SFTP component
type SFTPComponent struct{}

// NewSFTPComponent creates a new SFTPComponent
func NewSFTPComponent() *SFTPComponent {
	return &SFTPComponent{}
}

// CreateEndpoint creates a new SFTP endpoint
func (c *SFTPComponent) CreateEndpoint(uri string) (gocamel.Endpoint, error) {
	u, err := gocamel.ParseURI(uri)
	if err != nil {
		return nil, err
	}
	if move := gocamel.GetConfigValue(u, "move"); move != "" {
		if err := gocamel.ValidateRemotePath(move); err != nil {
			return nil, fmt.Errorf("invalid move path: %w", err)
		}
	}
	if moveFailed := gocamel.GetConfigValue(u, "moveFailed"); moveFailed != "" {
		if err := gocamel.ValidateRemotePath(moveFailed); err != nil {
			return nil, fmt.Errorf("invalid moveFailed path: %w", err)
		}
	}
	maxMessageSize := gocamel.ParseInt64Option(gocamel.GetConfigValue(u, "maxMessageSize"))
	if maxMessageSize <= 0 {
		maxMessageSize = gocamel.DefaultMaxBodySize
	}
	return &SFTPEndpoint{
		uri:            uri,
		url:            u,
		comp:           c,
		connectTimeout: gocamel.ParseConnectTimeout(u),
		disconnect:     strings.EqualFold(gocamel.GetConfigValue(u, "disconnect"), "true"),
		// maxMessageSize bounds how many bytes a single polled or produced file
		// may hold (defaults to DefaultMaxBodySize = 32 MiB).
		maxMessageSize: maxMessageSize,
	}, nil
}

// SFTPEndpoint represents an SFTP endpoint
type SFTPEndpoint struct {
	uri            string
	url            *url.URL
	comp           *SFTPComponent
	connectTimeout time.Duration
	disconnect     bool
	maxMessageSize int64
}

// URI returns the endpoint URI
func (e *SFTPEndpoint) URI() string {
	return gocamel.RedactURI(e.uri)
}

func (e *SFTPEndpoint) getHostKeyCallback(u *url.URL) (ssh.HostKeyCallback, error) {
	if strings.EqualFold(gocamel.GetConfigValue(u, "strictHostKeyChecking"), "false") {
		// SECURITY: this disables SSH host key verification entirely and
		// exposes the connection to active man-in-the-middle attacks. We
		// log a loud warning on every connect so the choice is visible
		// in operational logs, not buried in a URI parameter.
		gocamel.LogError("SFTP host key verification DISABLED (strictHostKeyChecking=false). "+
			"Connection is vulnerable to MITM — do not use against untrusted networks.",
			"host", u.Host)
		// Explicit user opt-in via the URI; the loud warning above is logged
		// on every connect (gosec G106).
		return ssh.InsecureIgnoreHostKey(), nil // #nosec G106
	}
	knownHostsFile := gocamel.GetConfigValue(u, "knownHostsFile")
	if knownHostsFile == "" {
		return nil, fmt.Errorf("strictHostKeyChecking is true but knownHostsFile is not specified")
	}
	return knownhosts.New(knownHostsFile)
}

// connect establishes an SSH + SFTP connection
func (e *SFTPEndpoint) connect() (*ssh.Client, *sftp.Client, error) {
	host := e.url.Host
	if !strings.Contains(host, ":") {
		host += ":22"
	}

	user := gocamel.GetConfigValue(e.url, "username")
	if user == "" {
		user = "root"
	}

	hostKeyCallback, err := e.getHostKeyCallback(e.url)
	if err != nil {
		return nil, nil, fmt.Errorf("host key validation configuration error: %w", err)
	}

	config := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.Password(gocamel.GetConfigValue(e.url, "password"))},
		HostKeyCallback: hostKeyCallback,
		Timeout:         e.connectTimeout,
	}

	// Private key authentication (takes priority over password if present).
	if privateKeyFile := gocamel.GetConfigValue(e.url, "privateKeyFile"); privateKeyFile != "" {
		// The key path comes from the trusted endpoint URI config; validate it
		// before touching the filesystem (gosec G304).
		if err := gocamel.ValidatePath(privateKeyFile); err != nil {
			return nil, nil, fmt.Errorf("invalid private key path %s: %w", privateKeyFile, err)
		}
		key, err := os.ReadFile(privateKeyFile) // #nosec G304
		if err != nil {
			return nil, nil, fmt.Errorf("cannot read private key %s: %w", privateKeyFile, err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse private key: %w", err)
		}
		config.Auth = append([]ssh.AuthMethod{ssh.PublicKeys(signer)}, config.Auth...)
	}

	sshClient, err := ssh.Dial("tcp", host, config)
	if err != nil {
		return nil, nil, fmt.Errorf("SSH connection error: %w", err)
	}

	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		sshClient.Close()
		return nil, nil, fmt.Errorf("SFTP client creation error: %w", err)
	}
	return sshClient, sftpClient, nil
}

// CreateProducer creates an SFTP producer
func (e *SFTPEndpoint) CreateProducer() (gocamel.Producer, error) {
	return &SFTPProducer{endpoint: e, fileExist: gocamel.ParseFileExist(e.url)}, nil
}

// CreateConsumer creates an SFTP consumer
func (e *SFTPEndpoint) CreateConsumer(processor gocamel.Processor) (gocamel.Consumer, error) {
	opts := gocamel.ParsePollingOptions(e.url)
	if err := gocamel.ValidateRemotePath(opts.Move); err != nil {
		return nil, fmt.Errorf("invalid move path: %w", err)
	}
	if err := gocamel.ValidateRemotePath(opts.MoveFailed); err != nil {
		return nil, fmt.Errorf("invalid moveFailed path: %w", err)
	}
	return &SFTPConsumer{
		endpoint:  e,
		processor: processor,
		opts:      opts,
	}, nil
}

// ---------------------------------------------------------------------------
// Producer
// ---------------------------------------------------------------------------

// SFTPProducer represents an SFTP producer
type SFTPProducer struct {
	endpoint   *SFTPEndpoint
	fileExist  gocamel.FileExistBehavior
	mu         sync.Mutex    // protects operation initialization
	operation  chan struct{} // exclusive connection lease for the entire transfer
	sshClient  *ssh.Client
	sftpClient *sftp.Client
}

func (p *SFTPProducer) Start(ctx context.Context) error { return nil }

// acquire leases the connection for one operation without holding a mutex
// across network I/O. A blocked sender can still cancel while waiting.
func (p *SFTPProducer) acquire(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.Lock()
	if p.operation == nil {
		p.operation = make(chan struct{}, 1)
	}
	operation := p.operation
	p.mu.Unlock()
	select {
	case operation <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *SFTPProducer) release() { <-p.operation }

func (p *SFTPProducer) Stop() error {
	if err := p.acquire(context.Background()); err != nil {
		return err
	}
	defer p.release()
	if p.sftpClient != nil {
		p.sftpClient.Close()
		p.sftpClient = nil
	}
	if p.sshClient != nil {
		p.sshClient.Close()
		p.sshClient = nil
	}
	return nil
}

func (p *SFTPProducer) getClients() (*ssh.Client, *sftp.Client, error) {
	if p.endpoint.disconnect {
		return p.endpoint.connect()
	}
	if p.sftpClient != nil {
		if _, err := p.sftpClient.Getwd(); err == nil {
			return p.sshClient, p.sftpClient, nil
		}
		p.sftpClient.Close()
		if p.sshClient != nil {
			p.sshClient.Close()
		}
		p.sftpClient = nil
		p.sshClient = nil
	}
	ssh, sftp, err := p.endpoint.connect()
	if err != nil {
		return nil, nil, err
	}
	p.sshClient = ssh
	p.sftpClient = sftp
	return ssh, sftp, nil
}

func (p *SFTPProducer) releaseClients(sshClient *ssh.Client, sftpClient *sftp.Client) {
	if p.endpoint.disconnect {
		if sftpClient != nil {
			sftpClient.Close()
		}
		if sshClient != nil {
			sshClient.Close()
		}
	}
}

// Send sends the exchange content to the SFTP server.
func (p *SFTPProducer) Send(exchange *gocamel.Exchange) error {
	if err := p.acquire(exchange.Context); err != nil {
		return err
	}
	defer p.release()
	sshClient, sftpClient, err := p.getClients()
	if err != nil {
		return err
	}
	defer p.releaseClients(sshClient, sftpClient)

	path := p.endpoint.url.Path
	if path == "" || path == "/" {
		if name, ok := exchange.GetIn().GetHeader(gocamel.CamelFileName); ok {
			joined, err := gocamel.JoinRemotePath("", fmt.Sprintf("%v", name))
			if err != nil {
				return fmt.Errorf("sftp: invalid path: %w", err)
			}
			path = "/" + joined
		} else {
			return fmt.Errorf("no path or CamelFileName specified for SFTP")
		}
	} else if strings.HasSuffix(path, "/") {
		if name, ok := exchange.GetIn().GetHeader(gocamel.CamelFileName); ok {
			joined, err := gocamel.JoinRemotePath(path, fmt.Sprintf("%v", name))
			if err != nil {
				return fmt.Errorf("sftp: invalid path: %w", err)
			}
			path = joined
		} else {
			return fmt.Errorf("path is a directory but CamelFileName is not specified")
		}
	}

	if err := gocamel.ValidateRemotePath(path); err != nil {
		return fmt.Errorf("sftp: invalid path: %w", err)
	}

	switch p.fileExist {
	case gocamel.FileExistFail:
		if _, err := sftpClient.Stat(path); err == nil {
			return fmt.Errorf("file already exists on SFTP: %s", path)
		}
	case gocamel.FileExistIgnore:
		if _, err := sftpClient.Stat(path); err == nil {
			return nil
		}
	}

	var body []byte
	switch b := exchange.GetIn().GetBody().(type) {
	case []byte:
		body = b
	case string:
		body = []byte(b)
	case io.Reader:
		if body, err = gocamel.ReadLimited(b, p.endpoint.maxMessageSize); err != nil {
			return fmt.Errorf("error reading body: %w", err)
		}
	default:
		return fmt.Errorf("unsupported body type for SFTP: %T", exchange.GetIn().GetBody())
	}

	if p.fileExist == gocamel.FileExistAppend {
		// A failed open of a missing file is expected (create-if-absent
		// semantics); a partial read, however, must abort the send:
		// concatenating a truncated copy of the existing content would
		// silently corrupt the stored file.
		if f, err := sftpClient.Open(path); err == nil {
			existing, readErr := gocamel.ReadLimited(f, p.endpoint.maxMessageSize)
			f.Close()
			if readErr != nil {
				return fmt.Errorf("sftp: failed to read existing file %s for append: %w", path, readErr)
			}
			body = append(existing, body...)
		}
	}

	if dir := filepath.Dir(path); dir != "." && dir != "/" {
		sftpClient.MkdirAll(dir) // ignore the error if the directory exists
	}

	file, err := sftpClient.Create(path)
	if err != nil {
		return fmt.Errorf("error creating SFTP file: %w", err)
	}
	defer file.Close()

	if _, err = io.Copy(file, bytes.NewReader(body)); err != nil {
		return fmt.Errorf("error writing SFTP file: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Consumer
// ---------------------------------------------------------------------------

// SFTPConsumer represents an SFTP consumer
type SFTPConsumer struct {
	endpoint   *SFTPEndpoint
	processor  gocamel.Processor
	opts       gocamel.PollingOptions
	cancel     context.CancelFunc
	sshClient  *ssh.Client  // persistent connection (disconnect=false)
	sftpClient *sftp.Client // persistent connection (disconnect=false)
	wg         sync.WaitGroup
}

func (c *SFTPConsumer) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		select {
		case <-time.After(c.opts.InitialDelay):
		case <-ctx.Done():
			return
		}
		c.poll(ctx)
	}()
	return nil
}

func (c *SFTPConsumer) poll(ctx context.Context) {
	ticker := time.NewTicker(c.opts.Delay)
	defer ticker.Stop()
	c.doPoll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.doPoll(ctx)
		}
	}
}

func (c *SFTPConsumer) getClients() (*ssh.Client, *sftp.Client, error) {
	if c.endpoint.disconnect {
		return c.endpoint.connect()
	}
	if c.sftpClient != nil {
		if _, err := c.sftpClient.Getwd(); err == nil {
			return c.sshClient, c.sftpClient, nil
		}
		c.sftpClient.Close()
		c.sshClient.Close()
		c.sftpClient = nil
		c.sshClient = nil
	}
	ssh, sftp, err := c.endpoint.connect()
	if err != nil {
		return nil, nil, err
	}
	c.sshClient = ssh
	c.sftpClient = sftp
	return ssh, sftp, nil
}

func (c *SFTPConsumer) releaseClients(sshClient *ssh.Client, sftpClient *sftp.Client) {
	if c.endpoint.disconnect {
		sftpClient.Close()
		sshClient.Close()
	}
}

type sftpSynchronization struct {
	client     *sftp.Client
	path       string
	lockPath   string
	markerPath string
	noop       bool
	delete     bool
	move       string
	moveFailed string
}

func (s *sftpSynchronization) cleanupLock() {
	if s.markerPath != "" {
		_ = s.client.Remove(s.markerPath)
	}
}

func (s *sftpSynchronization) OnComplete(exchange *gocamel.Exchange) {
	defer s.cleanupLock()
	effectivePath := s.path
	if s.lockPath != "" {
		effectivePath = s.lockPath
	}

	if s.noop {
		if s.lockPath != "" {
			_ = s.client.PosixRename(s.lockPath, s.path)
		}
		return
	}
	if s.move != "" {
		moveSFTPFile(s.client, effectivePath, s.move)
	} else if s.delete {
		if err := s.client.Remove(effectivePath); err != nil {
			gocamel.LogError("SFTP file deletion error", "file", effectivePath, "error", err)
		}
	} else if s.lockPath != "" {
		_ = s.client.PosixRename(s.lockPath, s.path)
	}
}

func (s *sftpSynchronization) OnFailure(exchange *gocamel.Exchange) {
	if errors.Is(exchange.Error, gocamel.ErrStopRouting) {
		s.OnComplete(exchange)
		return
	}
	defer s.cleanupLock()

	effectivePath := s.path
	if s.lockPath != "" {
		effectivePath = s.lockPath
	}

	if !s.noop && s.moveFailed != "" {
		moveSFTPFile(s.client, effectivePath, s.moveFailed)
	} else if s.lockPath != "" {
		_ = s.client.PosixRename(s.lockPath, s.path)
	}
}

func (c *SFTPConsumer) doPoll(ctx context.Context) {
	sshClient, sftpClient, err := c.getClients()
	if err != nil {
		gocamel.LogError("SFTP polling connection error", "error", err)
		return
	}
	defer c.releaseClients(sshClient, sftpClient)

	rootPath := c.endpoint.url.Path
	if rootPath == "" {
		rootPath = "."
	}

	type sftpFile struct {
		name string
		path string
	}
	var files []sftpFile

	if c.opts.Recursive {
		walker := sftpClient.Walk(rootPath)
		for walker.Step() {
			if walker.Err() != nil {
				continue
			}
			entry := walker.Stat()
			if entry.Mode()&os.ModeSymlink != 0 {
				continue
			}
			if entry.IsDir() {
				continue
			}
			name := filepath.Base(walker.Path())
			if strings.HasSuffix(name, ".camelLock") || strings.HasSuffix(name, ".camelExclusiveReadLock") {
				continue
			}
			if gocamel.MatchFileName(name, c.opts.Include, c.opts.Exclude) {
				files = append(files, sftpFile{name: name, path: walker.Path()})
			}
		}
	} else {
		entries, err := sftpClient.ReadDir(rootPath)
		if err != nil {
			gocamel.LogError("SFTP directory listing error", "error", err)
			return
		}
		for _, entry := range entries {
			if entry.Mode()&os.ModeSymlink != 0 {
				continue
			}
			if entry.IsDir() {
				continue
			}
			if strings.HasSuffix(entry.Name(), ".camelLock") || strings.HasSuffix(entry.Name(), ".camelExclusiveReadLock") {
				continue
			}
			if !gocamel.MatchFileName(entry.Name(), c.opts.Include, c.opts.Exclude) {
				continue
			}
			sep := "/"
			if rootPath == "/" {
				sep = ""
			}
			files = append(files, sftpFile{
				name: entry.Name(),
				path: rootPath + sep + entry.Name(),
			})
		}
	}

	count := 0
	for _, f := range files {
		if c.opts.MaxMessagesPerPoll > 0 && count >= c.opts.MaxMessagesPerPoll {
			break
		}

		var lockPath string
		var markerPath string

		switch c.opts.ReadLock {
		case gocamel.ReadLockChanged:
			statFn := func() (int64, time.Time, error) {
				info, err := sftpClient.Stat(f.path)
				if err != nil {
					return 0, time.Time{}, err
				}
				return info.Size(), info.ModTime(), nil
			}
			if !gocamel.WaitForStable(ctx, statFn, c.opts.ReadLockTimeout, c.opts.ReadLockCheckInterval, c.opts.ReadLockMinAge, c.opts.ReadLockMinLength) {
				gocamel.LogWarn("SFTP file did not stabilise before read lock timeout; skipping",
					"file", f.path, "timeout", c.opts.ReadLockTimeout)
				continue
			}

		case gocamel.ReadLockRename:
			targetLockPath := f.path + ".camelExclusiveReadLock"
			if err := sftpClient.PosixRename(f.path, targetLockPath); err != nil {
				gocamel.LogWarn("could not acquire SFTP rename read lock; skipping", "file", f.path, "error", err)
				continue
			}
			lockPath = targetLockPath

		case gocamel.ReadLockMarkerFile:
			targetMarkerPath := f.path + ".camelLock"
			if _, err := sftpClient.Stat(targetMarkerPath); err == nil {
				// Marker exists: file is locked by another process
				continue
			}
			mf, err := sftpClient.Create(targetMarkerPath)
			if err != nil {
				gocamel.LogWarn("could not create marker file for SFTP read lock; skipping", "file", f.path, "error", err)
				continue
			}
			mf.Close()
			markerPath = targetMarkerPath
		}

		readPath := f.path
		if lockPath != "" {
			readPath = lockPath
		}

		file, err := sftpClient.Open(readPath)
		if err != nil {
			gocamel.LogError("SFTP file open error", "file", readPath, "error", err)
			if markerPath != "" {
				_ = sftpClient.Remove(markerPath)
			}
			if lockPath != "" {
				_ = sftpClient.PosixRename(lockPath, f.path)
			}
			continue
		}
		content, err := gocamel.ReadLimited(file, c.endpoint.maxMessageSize)
		file.Close()
		if err != nil {
			gocamel.LogError("SFTP file read error", "file", readPath, "error", err)
			if markerPath != "" {
				_ = sftpClient.Remove(markerPath)
			}
			if lockPath != "" {
				_ = sftpClient.PosixRename(lockPath, f.path)
			}
			continue
		}

		exchange := gocamel.NewExchange(ctx)
		exchange.SetBody(content)
		exchange.SetHeader(gocamel.CamelFileName, f.name)
		exchange.SetHeader(gocamel.CamelFilePath, f.path)

		exchange.AddSynchronization(&sftpSynchronization{
			client:     sftpClient,
			path:       f.path,
			lockPath:   lockPath,
			markerPath: markerPath,
			noop:       c.opts.Noop,
			delete:     c.opts.Delete,
			move:       c.opts.Move,
			moveFailed: c.opts.MoveFailed,
		})

		procErr := gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange))

		if procErr == nil {
			count++
		}
	}
}

func (c *SFTPConsumer) Stop() error {
	if c.cancel != nil {
		c.cancel()
	}
	c.wg.Wait()
	if c.sftpClient != nil {
		c.sftpClient.Close()
		c.sftpClient = nil
	}
	if c.sshClient != nil {
		c.sshClient.Close()
		c.sshClient = nil
	}
	return nil
}

// moveSFTPFile renames/moves a file on the SFTP server.
func moveSFTPFile(client *sftp.Client, srcPath, destDir string) {
	filename := filepath.Base(srcPath)
	filename = strings.TrimSuffix(filename, ".camelExclusiveReadLock")
	destPath := destDir + "/" + filename
	// MkdirAll stats destDir first and returns nil when it is already a
	// directory, so any error here means there is no usable destination.
	if err := client.MkdirAll(destDir); err != nil {
		gocamel.LogError("SFTP directory creation error", "directory", destDir, "error", err)
		return
	}
	if err := client.PosixRename(srcPath, destPath); err != nil {
		gocamel.LogError("SFTP move error", "src", srcPath, "dest", destPath, "error", err)
	}
}
