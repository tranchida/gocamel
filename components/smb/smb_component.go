package smb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hirochachacha/go-smb2"

	gocamel "gitlab.com/tranchida/gocamel"
)

// SMBComponent represents the SMB component
type SMBComponent struct{}

// NewSMBComponent creates a new SMBComponent
func NewSMBComponent() *SMBComponent {
	return &SMBComponent{}
}

// CreateEndpoint creates a new SMB endpoint
func (c *SMBComponent) CreateEndpoint(uri string) (gocamel.Endpoint, error) {
	u, err := gocamel.ParseURI(uri)
	if err != nil {
		return nil, err
	}
	return &SMBEndpoint{
		uri:            uri,
		url:            u,
		comp:           c,
		connectTimeout: gocamel.ParseConnectTimeout(u),
		disconnect:     strings.EqualFold(gocamel.GetConfigValue(u, "disconnect"), "true"),
		// maxMessageSize bounds how many bytes a single polled file may hold
		// (0 = unlimited).
		maxMessageSize: gocamel.ParseInt64Option(gocamel.GetConfigValue(u, "maxMessageSize")),
	}, nil
}

// SMBEndpoint represents an SMB endpoint
type SMBEndpoint struct {
	uri            string
	url            *url.URL
	comp           *SMBComponent
	connectTimeout time.Duration
	disconnect     bool
	maxMessageSize int64
}

// URI returns the endpoint URI
func (e *SMBEndpoint) URI() string {
	return e.uri
}

type smbConn struct {
	tcp     net.Conn
	session *smb2.Session
	share   *smb2.Share
}

func (sc *smbConn) close() {
	if sc.share != nil {
		sc.share.Umount()
	}
	if sc.session != nil {
		sc.session.Logoff()
	}
	if sc.tcp != nil {
		sc.tcp.Close()
	}
}

// connect establishes an SMB connection
func (e *SMBEndpoint) connect() (*smbConn, error) {
	host := e.url.Host
	if !strings.Contains(host, ":") {
		host += ":445"
	}

	tcp, err := net.DialTimeout("tcp", host, e.connectTimeout)
	if err != nil {
		return nil, fmt.Errorf("erreur de connexion TCP (SMB): %w", err)
	}

	d := &smb2.Dialer{
		Initiator: &smb2.NTLMInitiator{
			User:     gocamel.GetConfigValue(e.url, "username"),
			Password: gocamel.GetConfigValue(e.url, "password"),
			Domain:   gocamel.GetConfigValue(e.url, "domain"),
		},
	}

	session, err := d.Dial(tcp)
	if err != nil {
		tcp.Close()
		return nil, fmt.Errorf("erreur de session SMB: %w", err)
	}

	shareName, _ := e.shareName()
	if shareName == "" {
		session.Logoff()
		tcp.Close()
		return nil, fmt.Errorf("aucun nom de partage spécifié dans l'URI SMB")
	}

	share, err := session.Mount(shareName)
	if err != nil {
		session.Logoff()
		tcp.Close()
		return nil, fmt.Errorf("erreur de montage du partage SMB %s: %w", shareName, err)
	}

	return &smbConn{tcp: tcp, session: session, share: share}, nil
}

// shareName returns the share name and the relative path within that share.
func (e *SMBEndpoint) shareName() (string, string) {
	parts := strings.SplitN(strings.TrimPrefix(e.url.Path, "/"), "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return "", ""
	}
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], parts[1]
}

// getFilePath returns the file/directory path relative to the share.
func (e *SMBEndpoint) getFilePath() string {
	_, p := e.shareName()
	return strings.ReplaceAll(p, "/", "\\")
}

// CreateProducer creates an SMB producer
func (e *SMBEndpoint) CreateProducer() (gocamel.Producer, error) {
	return &SMBProducer{endpoint: e, fileExist: gocamel.ParseFileExist(e.url)}, nil
}

// CreateConsumer creates an SMB consumer
func (e *SMBEndpoint) CreateConsumer(processor gocamel.Processor) (gocamel.Consumer, error) {
	return &SMBConsumer{
		endpoint:  e,
		processor: processor,
		opts:      gocamel.ParsePollingOptions(e.url),
	}, nil
}

// ---------------------------------------------------------------------------
// Producer
// ---------------------------------------------------------------------------

// SMBProducer represents an SMB producer
type SMBProducer struct {
	endpoint  *SMBEndpoint
	fileExist gocamel.FileExistBehavior
	mu        sync.Mutex
	sc        *smbConn // connexion persistante (disconnect=false)
}

func (p *SMBProducer) Start(ctx context.Context) error { return nil }

func (p *SMBProducer) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sc != nil {
		p.sc.close()
		p.sc = nil
	}
	return nil
}

func (p *SMBProducer) getConn() (*smbConn, error) {
	if p.endpoint.disconnect {
		return p.endpoint.connect()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sc != nil {
		if _, err := p.sc.share.Stat("."); err == nil {
			return p.sc, nil
		}
		p.sc.close()
		p.sc = nil
	}
	sc, err := p.endpoint.connect()
	if err != nil {
		return nil, err
	}
	p.sc = sc
	return sc, nil
}

func (p *SMBProducer) releaseConn(sc *smbConn) {
	if p.endpoint.disconnect {
		sc.close()
	}
}

// Send sends the exchange content to the SMB share.
func (p *SMBProducer) Send(exchange *gocamel.Exchange) error {
	sc, err := p.getConn()
	if err != nil {
		return err
	}
	defer p.releaseConn(sc)

	path := p.endpoint.getFilePath()
	if path == "" || strings.HasSuffix(path, "\\") {
		if name, ok := exchange.GetIn().GetHeader(gocamel.CamelFileName); ok {
			path = filepath.Join(path, fmt.Sprintf("%v", name))
		} else {
			return fmt.Errorf("aucun nom de fichier (CamelFileName) spécifié pour SMB")
		}
	}
	path = strings.ReplaceAll(path, "/", "\\")

	// Security: validate path for directory traversal
	if err := gocamel.ValidateRemotePath(path); err != nil {
		return fmt.Errorf("smb: invalid path: %w", err)
	}

	switch p.fileExist {
	case gocamel.FileExistFail:
		if _, err := sc.share.Stat(path); err == nil {
			return fmt.Errorf("le fichier existe déjà sur SMB: %s", path)
		}
	case gocamel.FileExistIgnore:
		if _, err := sc.share.Stat(path); err == nil {
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
		if body, err = io.ReadAll(b); err != nil {
			return fmt.Errorf("erreur lecture du corps: %w", err)
		}
	default:
		return fmt.Errorf("unsupported body type for SMB: %T", exchange.GetIn().GetBody())
	}

	if p.fileExist == gocamel.FileExistAppend {
		if f, err := sc.share.Open(path); err == nil {
			existing, _ := io.ReadAll(f)
			f.Close()
			body = append(existing, body...)
		}
	}

	// Create parent directories if necessary
	smbMkdirAll(sc.share, filepath.Dir(path))

	file, err := sc.share.Create(path)
	if err != nil {
		return fmt.Errorf("erreur lors de la création du fichier SMB: %w", err)
	}
	defer file.Close()

	if _, err = io.Copy(file, bytes.NewReader(body)); err != nil {
		return fmt.Errorf("erreur lors de l'écriture SMB: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Consumer
// ---------------------------------------------------------------------------

// SMBConsumer represents an SMB consumer
type SMBConsumer struct {
	endpoint  *SMBEndpoint
	processor gocamel.Processor
	opts      gocamel.PollingOptions
	cancel    context.CancelFunc
	sc        *smbConn // connexion persistante (disconnect=false)
	wg        sync.WaitGroup
}

func (c *SMBConsumer) Start(ctx context.Context) error {
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

func (c *SMBConsumer) poll(ctx context.Context) {
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

func (c *SMBConsumer) getConn() (*smbConn, error) {
	if c.endpoint.disconnect {
		return c.endpoint.connect()
	}
	if c.sc != nil {
		if _, err := c.sc.share.Stat("."); err == nil {
			return c.sc, nil
		}
		c.sc.close()
		c.sc = nil
	}
	sc, err := c.endpoint.connect()
	if err != nil {
		return nil, err
	}
	c.sc = sc
	return sc, nil
}

func (c *SMBConsumer) releaseConn(sc *smbConn) {
	if c.endpoint.disconnect {
		sc.close()
	}
}

type smbFile struct {
	name string
	path string
}

// listSMBFiles lists files (recursively if opts.Recursive) applying include/exclude filters.
func (c *SMBConsumer) listSMBFiles(share *smb2.Share, dirPath string) []smbFile {
	entries, err := share.ReadDir(dirPath)
	if err != nil {
		gocamel.LogError("Erreur lors du listage SMB", "directory", dirPath, "error", err)
		return nil
	}

	var result []smbFile
	for _, entry := range entries {
		entryPath := filepath.Join(dirPath, entry.Name())
		entryPath = strings.ReplaceAll(entryPath, "/", "\\")

		if entry.IsDir() {
			if c.opts.Recursive && entry.Name() != "." && entry.Name() != ".." {
				result = append(result, c.listSMBFiles(share, entryPath)...)
			}
			continue
		}
		if strings.HasSuffix(entry.Name(), ".camelLock") || strings.HasSuffix(entry.Name(), ".camelExclusiveReadLock") {
			continue
		}
		if gocamel.MatchFileName(entry.Name(), c.opts.Include, c.opts.Exclude) {
			result = append(result, smbFile{name: entry.Name(), path: entryPath})
		}
	}
	return result
}

type smbSynchronization struct {
	share      *smb2.Share
	path       string
	lockPath   string
	markerPath string
	noop       bool
	delete     bool
	move       string
	moveFailed string
}

func (s *smbSynchronization) cleanupLock() {
	if s.markerPath != "" {
		_ = s.share.Remove(s.markerPath)
	}
}

func (s *smbSynchronization) OnComplete(exchange *gocamel.Exchange) {
	defer s.cleanupLock()
	effectivePath := s.path
	if s.lockPath != "" {
		effectivePath = s.lockPath
	}

	if s.noop {
		if s.lockPath != "" {
			_ = s.share.Rename(s.lockPath, s.path)
		}
		return
	}
	if s.move != "" {
		moveSMBFile(s.share, effectivePath, s.move)
	} else if s.delete {
		if err := s.share.Remove(effectivePath); err != nil {
			gocamel.LogError("Erreur lors de la suppression du fichier SMB", "file", effectivePath, "error", err)
		}
	} else if s.lockPath != "" {
		_ = s.share.Rename(s.lockPath, s.path)
	}
}

func (s *smbSynchronization) OnFailure(exchange *gocamel.Exchange) {
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
		moveSMBFile(s.share, effectivePath, s.moveFailed)
	} else if s.lockPath != "" {
		_ = s.share.Rename(s.lockPath, s.path)
	}
}

func (c *SMBConsumer) doPoll(ctx context.Context) {
	sc, err := c.getConn()
	if err != nil {
		gocamel.LogError("Erreur de connexion SMB pendant le polling", "error", err)
		return
	}
	defer c.releaseConn(sc)

	rootPath := c.endpoint.getFilePath()
	if rootPath == "" {
		rootPath = "."
	}

	files := c.listSMBFiles(sc.share, rootPath)

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
				info, err := sc.share.Stat(f.path)
				if err != nil {
					return 0, time.Time{}, err
				}
				return info.Size(), info.ModTime(), nil
			}
			if !gocamel.WaitForStable(ctx, statFn, c.opts.ReadLockTimeout, c.opts.ReadLockCheckInterval, c.opts.ReadLockMinAge, c.opts.ReadLockMinLength) {
				gocamel.LogWarn("SMB file did not stabilise before read lock timeout; skipping",
					"file", f.path, "timeout", c.opts.ReadLockTimeout)
				continue
			}

		case gocamel.ReadLockRename:
			targetLockPath := f.path + ".camelExclusiveReadLock"
			if err := sc.share.Rename(f.path, targetLockPath); err != nil {
				gocamel.LogWarn("could not acquire SMB rename read lock; skipping", "file", f.path, "error", err)
				continue
			}
			lockPath = targetLockPath

		case gocamel.ReadLockMarkerFile:
			targetMarkerPath := f.path + ".camelLock"
			if _, err := sc.share.Stat(targetMarkerPath); err == nil {
				// Marker exists: file is locked
				continue
			}
			mf, err := sc.share.Create(targetMarkerPath)
			if err != nil {
				gocamel.LogWarn("could not create marker file for SMB read lock; skipping", "file", f.path, "error", err)
				continue
			}
			mf.Close()
			markerPath = targetMarkerPath
		}

		readPath := f.path
		if lockPath != "" {
			readPath = lockPath
		}

		file, err := sc.share.Open(readPath)
		if err != nil {
			gocamel.LogError("Erreur lors de l'ouverture du fichier SMB", "file", readPath, "error", err)
			if markerPath != "" {
				_ = sc.share.Remove(markerPath)
			}
			if lockPath != "" {
				_ = sc.share.Rename(lockPath, f.path)
			}
			continue
		}
		content, err := gocamel.ReadLimited(file, c.endpoint.maxMessageSize)
		file.Close()
		if err != nil {
			gocamel.LogError("Erreur lors de la lecture du fichier SMB", "file", readPath, "error", err)
			if markerPath != "" {
				_ = sc.share.Remove(markerPath)
			}
			if lockPath != "" {
				_ = sc.share.Rename(lockPath, f.path)
			}
			continue
		}

		exchange := gocamel.NewExchange(ctx)
		exchange.SetBody(content)
		exchange.SetHeader(gocamel.CamelFileName, f.name)
		exchange.SetHeader(gocamel.CamelFilePath, f.path)

		exchange.AddSynchronization(&smbSynchronization{
			share:      sc.share,
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

func (c *SMBConsumer) Stop() error {
	if c.cancel != nil {
		c.cancel()
	}
	c.wg.Wait()
	if c.sc != nil {
		c.sc.close()
		c.sc = nil
	}
	return nil
}

// moveSMBFile renames/moves a file on the SMB share.
func moveSMBFile(share *smb2.Share, srcPath, destDir string) {
	destDir = strings.ReplaceAll(destDir, "/", "\\")
	smbMkdirAll(share, destDir)
	filename := filepath.Base(srcPath)
	filename = strings.TrimSuffix(filename, ".camelExclusiveReadLock")
	destPath := strings.ReplaceAll(filepath.Join(destDir, filename), "/", "\\")
	if err := share.Rename(srcPath, destPath); err != nil {
		gocamel.LogError("Erreur déplacement SMB", "src", srcPath, "dest", destPath, "error", err)
	}
}

// smbMkdirAll recursively creates directories on an SMB share.
func smbMkdirAll(share *smb2.Share, path string) {
	path = strings.ReplaceAll(path, "/", "\\")
	parts := strings.Split(path, "\\")
	current := ""
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if current == "" {
			current = part
		} else {
			current = current + "\\" + part
		}
		share.Mkdir(current, 0755) // ignore error if directory already exists
	}
}
