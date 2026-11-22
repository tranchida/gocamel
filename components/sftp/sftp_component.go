package sftp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path"
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
	if err := gocamel.ValidateRemotePath(u.Path); err != nil {
		return nil, fmt.Errorf("invalid SFTP path: %w", err)
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
	connectTimeout := gocamel.ParseConnectTimeout(u)
	if connectTimeout <= 0 {
		connectTimeout = 10 * time.Second
	}
	return &SFTPEndpoint{
		uri:            uri,
		url:            u,
		comp:           c,
		connectTimeout: connectTimeout,
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

func (e *SFTPEndpoint) targetAddr() string {
	if e.url.Port() == "" {
		return net.JoinHostPort(e.url.Hostname(), "22")
	}
	return e.url.Host
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
	if err := gocamel.ValidatePath(knownHostsFile); err != nil {
		return nil, fmt.Errorf("invalid knownHostsFile path %s: %w", knownHostsFile, err)
	}
	return knownhosts.New(knownHostsFile)
}

// connect establishes an SSH + SFTP connection
func (e *SFTPEndpoint) connect(ctx context.Context) (*ssh.Client, *sftp.Client, net.Conn, error) {
	return e.connectWithDial(ctx, nil)
}

// connectWithDial is connect but calls onDial as soon as the TCP transport is
// established, before the SSH/SFTP handshake, so a consumer can force-close it
// during shutdown. Context cancellation and connectTimeout bound both dialing
// and the SSH/SFTP handshake.
func (e *SFTPEndpoint) connectWithDial(ctx context.Context, onDial func(net.Conn)) (*ssh.Client, *sftp.Client, net.Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	host := e.targetAddr()

	user := gocamel.GetConfigValue(e.url, "username")
	if user == "" {
		user = "root"
	}

	hostKeyCallback, err := e.getHostKeyCallback(e.url)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("host key validation configuration error: %w", err)
	}

	timeout := e.connectTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	var auth []ssh.AuthMethod
	// Private key authentication (takes priority over password if present).
	if privateKeyFile := gocamel.GetConfigValue(e.url, "privateKeyFile"); privateKeyFile != "" {
		// The key path comes from the trusted endpoint URI config; validate it
		// before touching the filesystem (gosec G304).
		if err := gocamel.ValidatePath(privateKeyFile); err != nil {
			return nil, nil, nil, fmt.Errorf("invalid private key path %s: %w", privateKeyFile, err)
		}
		key, err := os.ReadFile(privateKeyFile) // #nosec G304
		if err != nil {
			return nil, nil, nil, fmt.Errorf("cannot read private key %s: %w", privateKeyFile, err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to parse private key: %w", err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if password := gocamel.GetConfigValue(e.url, "password"); password != "" || len(auth) == 0 {
		auth = append(auth, ssh.Password(password))
	}

	config := &ssh.ClientConfig{
		User:            user,
		Auth:            auth,
		HostKeyCallback: hostKeyCallback,
		Timeout:         timeout,
	}

	handshakeCtx, cancelHandshake := context.WithTimeout(ctx, timeout)
	defer cancelHandshake()

	dialer := &net.Dialer{Timeout: timeout}
	rawConn, err := dialer.DialContext(handshakeCtx, "tcp", host)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("SSH connection error: %w", err)
	}
	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := handshakeCtx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := rawConn.SetDeadline(deadline); err != nil {
		_ = rawConn.Close()
		return nil, nil, nil, fmt.Errorf("SSH connection error: %w", err)
	}
	stopHandshakeWatch := context.AfterFunc(handshakeCtx, func() {
		_ = rawConn.Close()
	})
	if onDial != nil {
		onDial(rawConn)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(rawConn, host, config)
	if err != nil {
		stopHandshakeWatch()
		_ = rawConn.Close()
		return nil, nil, nil, fmt.Errorf("SSH connection error: %w", err)
	}
	sshClient := ssh.NewClient(sshConn, chans, reqs)

	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		stopHandshakeWatch()
		_ = sshClient.Close()
		_ = rawConn.Close()
		return nil, nil, nil, fmt.Errorf("SFTP client creation error: %w", err)
	}
	if !stopHandshakeWatch() {
		_ = sftpClient.Close()
		_ = sshClient.Close()
		_ = rawConn.Close()
		return nil, nil, nil, fmt.Errorf("SSH connection error: %w", handshakeCtx.Err())
	}
	if err := rawConn.SetDeadline(time.Time{}); err != nil {
		_ = sftpClient.Close()
		_ = sshClient.Close()
		_ = rawConn.Close()
		return nil, nil, nil, fmt.Errorf("SSH connection error: %w", err)
	}
	return sshClient, sftpClient, rawConn, nil
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
	rawConn    net.Conn
	stopped    bool // guarded by operation lease
}

func (p *SFTPProducer) Start(ctx context.Context) error {
	if err := p.acquire(ctx); err != nil {
		return err
	}
	defer p.release()
	p.stopped = false
	return nil
}

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
	p.stopped = true
	closeSFTPBundle(p.sshClient, p.sftpClient, p.rawConn)
	p.sftpClient = nil
	p.sshClient = nil
	p.rawConn = nil
	return nil
}

func (p *SFTPProducer) getClients(ctx context.Context) (*ssh.Client, *sftp.Client, net.Conn, error) {
	if p.endpoint.disconnect {
		return p.endpoint.connect(ctx)
	}
	if p.sftpClient != nil {
		if _, err := p.sftpClient.Getwd(); err == nil {
			return p.sshClient, p.sftpClient, p.rawConn, nil
		}
		closeSFTPBundle(p.sshClient, p.sftpClient, p.rawConn)
		p.sftpClient = nil
		p.sshClient = nil
		p.rawConn = nil
	}
	sshClient, sftpClient, rawConn, err := p.endpoint.connect(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	p.sshClient = sshClient
	p.sftpClient = sftpClient
	p.rawConn = rawConn
	return sshClient, sftpClient, rawConn, nil
}

func (p *SFTPProducer) releaseClients(sshClient *ssh.Client, sftpClient *sftp.Client, rawConn net.Conn) {
	if p.endpoint.disconnect {
		closeSFTPBundle(sshClient, sftpClient, rawConn)
	}
}

// isSFTPAlreadyExists checks whether an OpenFile(O_EXCL) error indicates that
// the target path already exists on the SFTP server. Because SFTPv3 maps EEXIST
// to generic SSH_FX_FAILURE (4), we confirm existence via Lstat before
// classifying SSH_FX_FAILURE as "already exists" so unrelated server failures
// (disk full, read-only filesystem, etc.) still surface as errors.
func isSFTPAlreadyExists(client *sftp.Client, remotePath string, err error) bool {
	if errors.Is(err, os.ErrExist) {
		return true
	}
	var statusErr *sftp.StatusError
	if errors.As(err, &statusErr) && statusErr.FxCode() == sftp.ErrSSHFxFailure {
		if _, statErr := client.Lstat(remotePath); statErr == nil {
			return true
		}
	}
	return false
}

// isSFTPOpUnsupported reports whether err indicates the SFTP server does not
// support an extended operation (such as posix-rename@openssh.com).
func isSFTPOpUnsupported(err error) bool {
	if errors.Is(err, sftp.ErrSSHFxOpUnsupported) {
		return true
	}
	var statusErr *sftp.StatusError
	return errors.As(err, &statusErr) && statusErr.FxCode() == sftp.ErrSSHFxOpUnsupported
}

// Send sends the exchange content to the SFTP server.
func (p *SFTPProducer) Send(exchange *gocamel.Exchange) error {
	if exchange == nil {
		return fmt.Errorf("sftp: exchange must not be nil")
	}
	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if err := p.acquire(ctx); err != nil {
		return err
	}
	defer p.release()
	if p.stopped {
		return fmt.Errorf("sftp producer is stopped")
	}

	remotePath := p.endpoint.url.Path
	if remotePath == "" || remotePath == "/" {
		if name, ok := exchange.GetIn().GetHeader(gocamel.CamelFileName); ok && name != nil {
			joined, err := gocamel.JoinRemotePath("", fmt.Sprintf("%v", name))
			if err != nil {
				return fmt.Errorf("sftp: invalid path: %w", err)
			}
			remotePath = "/" + joined
		} else {
			return fmt.Errorf("no path or CamelFileName specified for SFTP")
		}
	} else if strings.HasSuffix(remotePath, "/") {
		if name, ok := exchange.GetIn().GetHeader(gocamel.CamelFileName); ok && name != nil {
			joined, err := gocamel.JoinRemotePath(remotePath, fmt.Sprintf("%v", name))
			if err != nil {
				return fmt.Errorf("sftp: invalid path: %w", err)
			}
			remotePath = joined
		} else {
			return fmt.Errorf("path is a directory but CamelFileName is not specified")
		}
	}

	if err := gocamel.ValidateRemotePath(remotePath); err != nil {
		return fmt.Errorf("sftp: invalid path: %w", err)
	}

	var body []byte
	switch b := exchange.GetIn().GetBody().(type) {
	case []byte:
		body = b
	case string:
		body = []byte(b)
	case io.Reader:
		var err error
		if body, err = gocamel.ReadLimited(b, p.endpoint.maxMessageSize); err != nil {
			return fmt.Errorf("error reading body: %w", err)
		}
	default:
		return fmt.Errorf("unsupported body type for SFTP: %T", exchange.GetIn().GetBody())
	}

	sshClient, sftpClient, rawConn, err := p.getClients(ctx)
	if err != nil {
		return err
	}
	defer p.releaseClients(sshClient, sftpClient, rawConn)

	if dir := path.Dir(remotePath); dir != "." && dir != "/" {
		_ = sftpClient.MkdirAll(dir) // ignore the error if the directory exists
	}

	var openFlags int
	switch p.fileExist {
	case gocamel.FileExistAppend:
		openFlags = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	case gocamel.FileExistFail, gocamel.FileExistIgnore:
		openFlags = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	default:
		openFlags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}

	file, err := sftpClient.OpenFile(remotePath, openFlags)
	if err != nil {
		if p.fileExist == gocamel.FileExistFail && isSFTPAlreadyExists(sftpClient, remotePath, err) {
			return fmt.Errorf("file already exists on SFTP: %s", remotePath)
		}
		if p.fileExist == gocamel.FileExistIgnore && isSFTPAlreadyExists(sftpClient, remotePath, err) {
			return nil
		}
		return fmt.Errorf("error creating SFTP file: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()

	if p.fileExist == gocamel.FileExistAppend {
		// SFTPv3 servers commonly ignore SSH_FXF_APPEND and rely on client-sent
		// write offsets, so seek to EOF explicitly after opening without O_TRUNC.
		if _, err := file.Seek(0, io.SeekEnd); err != nil {
			return fmt.Errorf("error seeking SFTP file for append: %w", err)
		}
	}

	if _, err = io.Copy(file, bytes.NewReader(body)); err != nil {
		return fmt.Errorf("error writing SFTP file: %w", err)
	}
	closed = true
	if err := file.Close(); err != nil {
		return fmt.Errorf("error closing SFTP file: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Consumer
// ---------------------------------------------------------------------------

// SFTPConsumer represents an SFTP consumer
type SFTPConsumer struct {
	endpoint  *SFTPEndpoint
	processor gocamel.Processor
	opts      gocamel.PollingOptions
	mu        sync.Mutex
	// transition is non-nil while a Start or Stop is in progress. It serializes
	// the two operations so a Stop racing a Start cannot observe started=false
	// and return before the consumer is fully up (and vice versa). Guarded by mu.
	transition chan struct{}
	started    bool
	stopping   bool
	cancel     context.CancelFunc
	sshClient  *ssh.Client  // persistent connection (disconnect=false)
	sftpClient *sftp.Client // persistent connection (disconnect=false)
	rawConn    net.Conn     // underlying socket for persistent connection
	// activeSSH/activeSFTP reference the connection used by the current poll,
	// including disconnect=true mode, so Stop can force-close it to unblock a
	// stalled network operation. Guarded by mu.
	activeSSH  *ssh.Client
	activeSFTP *sftp.Client
	activeRaw  net.Conn
	wg         sync.WaitGroup
}

// acquireTransition waits for an in-progress Start/Stop and claims the next
// transition. It never holds c.mu while waiting.
func (c *SFTPConsumer) acquireTransition() {
	for {
		c.mu.Lock()
		if c.transition != nil {
			done := c.transition
			c.mu.Unlock()
			<-done
			continue
		}
		c.transition = make(chan struct{})
		c.mu.Unlock()
		return
	}
}

// releaseTransition publishes the final state and wakes Start/Stop waiters.
func (c *SFTPConsumer) releaseTransition() {
	c.mu.Lock()
	close(c.transition)
	c.transition = nil
	c.mu.Unlock()
}

func closeSFTPBundle(sshClient *ssh.Client, sftpClient *sftp.Client, rawConn net.Conn) {
	if sftpClient != nil {
		_ = sftpClient.Close()
	}
	if sshClient != nil {
		_ = sshClient.Close()
	}
	if rawConn != nil {
		_ = rawConn.Close()
	}
}

func (c *SFTPConsumer) closeActiveConns() {
	c.detachAndCloseConns(false)
}

func (c *SFTPConsumer) detachAndCloseConns(markStopping bool) {
	c.mu.Lock()
	var cancel context.CancelFunc
	if markStopping {
		c.stopping = true
		cancel = c.cancel
		c.cancel = nil
	}
	sshClient := c.sshClient
	sftpClient := c.sftpClient
	rawConn := c.rawConn
	activeSSH := c.activeSSH
	activeSFTP := c.activeSFTP
	activeRaw := c.activeRaw
	c.sshClient = nil
	c.sftpClient = nil
	c.rawConn = nil
	c.activeSSH = nil
	c.activeSFTP = nil
	c.activeRaw = nil
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	// Close the transports before waiting so a poll blocked in a network read
	// is unblocked instead of keeping Stop (and the route shutdown) stuck.
	if activeRaw != nil {
		_ = activeRaw.Close()
	}
	if rawConn != nil && rawConn != activeRaw {
		_ = rawConn.Close()
	}
	if activeSFTP != nil {
		_ = activeSFTP.Close()
	}
	if sftpClient != nil && sftpClient != activeSFTP {
		_ = sftpClient.Close()
	}
	if activeSSH != nil {
		_ = activeSSH.Close()
	}
	if sshClient != nil && sshClient != activeSSH {
		_ = sshClient.Close()
	}
}

func (c *SFTPConsumer) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	c.acquireTransition()
	defer c.releaseTransition()

	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return nil
	}
	c.stopping = false
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.started = true
	c.wg.Add(1)
	c.mu.Unlock()

	go func() {
		defer c.wg.Done()
		initialDelay := c.opts.InitialDelay
		if initialDelay < 0 {
			initialDelay = 0
		}
		if initialDelay > 0 {
			timer := time.NewTimer(initialDelay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return
			}
		} else if ctx.Err() != nil {
			return
		}
		c.poll(ctx)
	}()
	return nil
}

func (c *SFTPConsumer) poll(ctx context.Context) {
	stopWatch := context.AfterFunc(ctx, c.closeActiveConns)
	defer stopWatch()

	delay := c.opts.Delay
	if delay <= 0 {
		delay = 5 * time.Second
	}
	ticker := time.NewTicker(delay)
	defer ticker.Stop()

	c.doPoll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			c.doPoll(ctx)
		}
	}
}

func (c *SFTPConsumer) dialAndTrack(ctx context.Context, persistent bool) (*ssh.Client, *sftp.Client, net.Conn, error) {
	c.mu.Lock()
	if c.stopping || !c.started || ctx.Err() != nil {
		c.mu.Unlock()
		return nil, nil, nil, fmt.Errorf("sftp consumer is stopped")
	}
	c.mu.Unlock()

	var dialedRaw net.Conn
	onDial := func(raw net.Conn) {
		c.mu.Lock()
		if c.stopping || !c.started || ctx.Err() != nil {
			c.mu.Unlock()
			_ = raw.Close()
			return
		}
		dialedRaw = raw
		c.activeRaw = raw
		c.mu.Unlock()
	}

	sshClient, sftpClient, rawConn, err := c.endpoint.connectWithDial(ctx, onDial)
	if err != nil {
		c.mu.Lock()
		if c.activeRaw == dialedRaw {
			c.activeRaw = nil
		}
		c.mu.Unlock()
		return nil, nil, nil, err
	}

	c.mu.Lock()
	if c.stopping || !c.started || ctx.Err() != nil {
		if c.activeRaw == rawConn {
			c.activeRaw = nil
		}
		c.mu.Unlock()
		closeSFTPBundle(sshClient, sftpClient, rawConn)
		return nil, nil, nil, fmt.Errorf("sftp consumer is stopped")
	}
	if persistent {
		c.sshClient = sshClient
		c.sftpClient = sftpClient
		c.rawConn = rawConn
	}
	c.activeSSH = sshClient
	c.activeSFTP = sftpClient
	c.activeRaw = rawConn
	c.mu.Unlock()
	return sshClient, sftpClient, rawConn, nil
}

func (c *SFTPConsumer) getClients(ctx context.Context) (*ssh.Client, *sftp.Client, net.Conn, error) {
	if c.endpoint.disconnect {
		return c.dialAndTrack(ctx, false)
	}

	c.mu.Lock()
	if c.stopping || !c.started || ctx.Err() != nil {
		c.mu.Unlock()
		return nil, nil, nil, fmt.Errorf("sftp consumer is stopped")
	}
	sshClient := c.sshClient
	sftpClient := c.sftpClient
	rawConn := c.rawConn
	if sftpClient != nil {
		c.activeSSH = sshClient
		c.activeSFTP = sftpClient
		c.activeRaw = rawConn
	}
	c.mu.Unlock()

	if sftpClient != nil {
		if _, err := sftpClient.Getwd(); err == nil {
			return sshClient, sftpClient, rawConn, nil
		}
		closeSFTPBundle(sshClient, sftpClient, rawConn)
		c.mu.Lock()
		if c.sftpClient == sftpClient {
			c.sftpClient = nil
			c.sshClient = nil
			c.rawConn = nil
		}
		if c.activeSFTP == sftpClient {
			c.activeSFTP = nil
			c.activeSSH = nil
			c.activeRaw = nil
		}
		c.mu.Unlock()
	}

	return c.dialAndTrack(ctx, true)
}

func (c *SFTPConsumer) releaseClients(sshClient *ssh.Client, sftpClient *sftp.Client, rawConn net.Conn) {
	if c.endpoint.disconnect {
		closeSFTPBundle(sshClient, sftpClient, rawConn)
	}
	c.mu.Lock()
	if c.activeSFTP == sftpClient {
		c.activeSFTP = nil
		c.activeSSH = nil
		c.activeRaw = nil
	}
	c.mu.Unlock()
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
			_ = s.client.Rename(s.lockPath, s.path)
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
		_ = s.client.Rename(s.lockPath, s.path)
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
		_ = s.client.Rename(s.lockPath, s.path)
	}
}

func isExcludedMoveDir(fullPath, moveDir string) bool {
	if moveDir == "" {
		return false
	}
	normFull := path.Clean(strings.TrimPrefix(strings.ReplaceAll(fullPath, "\\", "/"), "/"))
	normMove := path.Clean(strings.TrimPrefix(strings.ReplaceAll(moveDir, "\\", "/"), "/"))
	if normMove == "" || normMove == "." {
		return false
	}
	return normFull == normMove || strings.HasPrefix(normFull, normMove+"/")
}

func (c *SFTPConsumer) doPoll(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	sshClient, sftpClient, rawConn, err := c.getClients(ctx)
	if err != nil {
		gocamel.LogError("SFTP polling connection error", "error", err)
		return
	}
	defer c.releaseClients(sshClient, sftpClient, rawConn)

	rootPath := c.endpoint.url.Path
	if rootPath == "" {
		rootPath = "."
	} else if err := gocamel.ValidateRemotePath(rootPath); err != nil {
		gocamel.LogError("invalid SFTP polling path", "error", err)
		return
	}

	type sftpFile struct {
		name string
		path string
	}
	var files []sftpFile

	if c.opts.Recursive {
		walker := sftpClient.Walk(rootPath)
		for walker.Step() {
			if ctx.Err() != nil {
				return
			}
			if walker.Err() != nil {
				continue
			}
			entry := walker.Stat()
			if entry.Mode()&os.ModeSymlink != 0 {
				if entry.IsDir() {
					walker.SkipDir()
				}
				continue
			}
			if entry.IsDir() {
				if isExcludedMoveDir(walker.Path(), c.opts.Move) || isExcludedMoveDir(walker.Path(), c.opts.MoveFailed) {
					walker.SkipDir()
				}
				continue
			}
			if err := gocamel.ValidateRemotePath(walker.Path()); err != nil {
				gocamel.LogWarn("skipping unsafe SFTP path", "path", walker.Path(), "error", err)
				continue
			}
			name := path.Base(walker.Path())
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
			if ctx.Err() != nil {
				return
			}
			if entry.Mode()&os.ModeSymlink != 0 {
				continue
			}
			if entry.IsDir() {
				continue
			}
			entryName := entry.Name()
			if entryName == "" || entryName == "." || entryName == ".." || strings.ContainsAny(entryName, "/\\\x00") {
				gocamel.LogWarn("skipping unsafe SFTP entry name", "directory", rootPath, "name", entryName)
				continue
			}
			if strings.HasSuffix(entryName, ".camelLock") || strings.HasSuffix(entryName, ".camelExclusiveReadLock") {
				continue
			}
			if !gocamel.MatchFileName(entryName, c.opts.Include, c.opts.Exclude) {
				continue
			}
			files = append(files, sftpFile{
				name: entryName,
				path: path.Join(rootPath, entryName),
			})
		}
	}

	count := 0
	for _, f := range files {
		if ctx.Err() != nil {
			return
		}
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
				if ctx.Err() != nil {
					return
				}
				gocamel.LogWarn("SFTP file did not stabilise before read lock timeout; skipping",
					"file", f.path, "timeout", c.opts.ReadLockTimeout)
				continue
			}

		case gocamel.ReadLockRename:
			targetLockPath := f.path + ".camelExclusiveReadLock"
			if err := sftpClient.Rename(f.path, targetLockPath); err != nil {
				gocamel.LogWarn("could not acquire SFTP rename read lock; skipping", "file", f.path, "error", err)
				continue
			}
			lockPath = targetLockPath

		case gocamel.ReadLockMarkerFile:
			targetMarkerPath := f.path + ".camelLock"
			mf, err := sftpClient.OpenFile(targetMarkerPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
			if err != nil {
				if !isSFTPAlreadyExists(sftpClient, targetMarkerPath, err) {
					gocamel.LogWarn("could not create marker file for SFTP read lock; skipping", "file", f.path, "error", err)
				}
				continue
			}
			if err := mf.Close(); err != nil {
				_ = sftpClient.Remove(targetMarkerPath)
				gocamel.LogWarn("could not close marker file for SFTP read lock; skipping", "file", f.path, "error", err)
				continue
			}
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
				_ = sftpClient.Rename(lockPath, f.path)
			}
			continue
		}
		content, err := gocamel.ReadLimited(file, c.endpoint.maxMessageSize)
		_ = file.Close()
		if err != nil {
			gocamel.LogError("SFTP file read error", "file", readPath, "error", err)
			if markerPath != "" {
				_ = sftpClient.Remove(markerPath)
			}
			if lockPath != "" {
				_ = sftpClient.Rename(lockPath, f.path)
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

		_ = gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange))
		count++
	}
}

func (c *SFTPConsumer) Stop() error {
	c.acquireTransition()
	defer c.releaseTransition()

	c.detachAndCloseConns(true)

	c.wg.Wait()

	c.mu.Lock()
	c.started = false
	c.stopping = false
	c.mu.Unlock()
	return nil
}

// moveSFTPFile renames/moves a file on the SFTP server.
func moveSFTPFile(client *sftp.Client, srcPath, destDir string) {
	filename := path.Base(strings.ReplaceAll(srcPath, "\\", "/"))
	filename = strings.TrimSuffix(filename, ".camelExclusiveReadLock")
	cleanDir := path.Clean(strings.ReplaceAll(destDir, "\\", "/"))
	destPath := path.Join(cleanDir, filename)
	// MkdirAll stats cleanDir first and returns nil when it is already a
	// directory, so any error here means there is no usable destination.
	if err := client.MkdirAll(cleanDir); err != nil {
		gocamel.LogError("SFTP directory creation error", "directory", cleanDir, "error", err)
		return
	}
	if err := client.PosixRename(srcPath, destPath); err != nil {
		if isSFTPOpUnsupported(err) {
			if renameErr := client.Rename(srcPath, destPath); renameErr != nil {
				gocamel.LogError("SFTP move error", "src", srcPath, "dest", destPath, "error", renameErr)
			}
			return
		}
		gocamel.LogError("SFTP move error", "src", srcPath, "dest", destPath, "error", err)
	}
}
