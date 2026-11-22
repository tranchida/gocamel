package ftp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/jlaffaye/ftp"

	gocamel "github.com/tranchida/gocamel"
)

// containsControlChar reports whether s contains any ASCII control character
// (< 0x20, including CR/LF/NUL, or 0x7f).
func containsControlChar(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}

// validateFTPRemotePath rejects ASCII control characters (including CR/LF and
// NUL) even when built standalone against gocamel v0.20.0, alongside traversal
// validation from gocamel.ValidateRemotePath.
func validateFTPRemotePath(p string) error {
	if containsControlChar(p) {
		return fmt.Errorf("path contains control character")
	}
	return gocamel.ValidateRemotePath(p)
}

// FTPComponent represents the FTP component
type FTPComponent struct{}

// NewFTPComponent creates a new FTPComponent
func NewFTPComponent() *FTPComponent {
	return &FTPComponent{}
}

// CreateEndpoint creates a new FTP endpoint
func (c *FTPComponent) CreateEndpoint(uri string) (gocamel.Endpoint, error) {
	u, err := gocamel.ParseURI(uri)
	if err != nil {
		return nil, err
	}
	if err := validateFTPRemotePath(u.Path); err != nil {
		return nil, fmt.Errorf("invalid FTP path: %w", err)
	}
	if u.User != nil {
		if containsControlChar(u.User.Username()) {
			return nil, fmt.Errorf("invalid FTP username: contains control character")
		}
		if pass, ok := u.User.Password(); ok && containsControlChar(pass) {
			return nil, fmt.Errorf("invalid FTP password: contains control character")
		}
	}
	if user := gocamel.GetConfigValue(u, "username"); containsControlChar(user) {
		return nil, fmt.Errorf("invalid FTP username: contains control character")
	}
	if pass := gocamel.GetConfigValue(u, "password"); containsControlChar(pass) {
		return nil, fmt.Errorf("invalid FTP password: contains control character")
	}
	if move := gocamel.GetConfigValue(u, "move"); move != "" {
		if err := validateFTPRemotePath(move); err != nil {
			return nil, fmt.Errorf("invalid move path: %w", err)
		}
	}
	if moveFailed := gocamel.GetConfigValue(u, "moveFailed"); moveFailed != "" {
		if err := validateFTPRemotePath(moveFailed); err != nil {
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
	return &FTPEndpoint{
		uri:            uri,
		url:            u,
		comp:           c,
		connectTimeout: connectTimeout,
		// passiveMode=true (default): the jlaffaye/ftp library uses EPSV/PASV.
		// passiveMode=false: disables EPSV and falls back to basic PASV.
		passiveMode: !strings.EqualFold(gocamel.GetConfigValue(u, "passiveMode"), "false"),
		// disconnect=true: disconnect after each operation.
		// disconnect=false (default): maintain the connection between polls.
		disconnect: strings.EqualFold(gocamel.GetConfigValue(u, "disconnect"), "true"),
		// maxMessageSize bounds how many bytes a single polled or produced file
		// may hold (defaults to DefaultMaxBodySize = 32 MiB).
		maxMessageSize: maxMessageSize,
	}, nil
}

// FTPEndpoint represents an FTP endpoint
type FTPEndpoint struct {
	uri            string
	url            *url.URL
	comp           *FTPComponent
	connectTimeout time.Duration
	passiveMode    bool
	disconnect     bool
	maxMessageSize int64
}

// URI returns the endpoint URI
func (e *FTPEndpoint) URI() string {
	return gocamel.RedactURI(e.uri)
}

func (e *FTPEndpoint) targetAddr() string {
	if e.url.Port() == "" {
		return net.JoinHostPort(e.url.Hostname(), "21")
	}
	return e.url.Host
}

// connect establishes an FTP connection
func (e *FTPEndpoint) connect() (*ftp.ServerConn, error) {
	conn, _, err := e.connectWithRaw(context.Background(), nil, nil)
	return conn, err
}

// connectWithRaw establishes an FTP connection, bounding both TCP dialing and
// the greeting + Login handshake with e.connectTimeout and ctx. When trackConn
// is non-nil, every dialed control and data net.Conn passes through it so the
// caller can force-close active sockets during shutdown.
func (e *FTPEndpoint) connectWithRaw(
	ctx context.Context,
	dialCtxFn func() context.Context,
	trackConn func(net.Conn) (net.Conn, error),
) (*ftp.ServerConn, net.Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	user := gocamel.GetConfigValue(e.url, "username")
	if user == "" {
		user = "anonymous"
	}
	password := gocamel.GetConfigValue(e.url, "password")
	if containsControlChar(user) {
		return nil, nil, fmt.Errorf("invalid FTP username: contains control character")
	}
	if containsControlChar(password) {
		return nil, nil, fmt.Errorf("invalid FTP password: contains control character")
	}

	timeout := e.connectTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	handshakeCtx, cancelHandshake := context.WithTimeout(ctx, timeout)
	defer cancelHandshake()

	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := handshakeCtx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}

	host := e.targetAddr()
	var rawConn net.Conn
	var stopHandshakeWatch func() bool
	dialer := &net.Dialer{Timeout: timeout}
	opts := []ftp.DialOption{
		ftp.DialWithTimeout(timeout),
		ftp.DialWithDialFunc(func(network, address string) (net.Conn, error) {
			isControl := rawConn == nil
			dctx := handshakeCtx
			if !isControl {
				if dialCtxFn != nil {
					dctx = dialCtxFn()
				} else {
					dctx = ctx
				}
				if dctx == nil {
					dctx = context.Background()
				}
			}
			c, err := dialer.DialContext(dctx, network, address)
			if err != nil {
				return nil, err
			}
			if isControl {
				if err := c.SetDeadline(deadline); err != nil {
					_ = c.Close()
					return nil, err
				}
			}
			if trackConn != nil {
				tracked, err := trackConn(c)
				if err != nil {
					_ = c.Close()
					return nil, err
				}
				c = tracked
			}
			if isControl {
				rawConn = c
				stopHandshakeWatch = context.AfterFunc(handshakeCtx, func() {
					_ = c.Close()
				})
			}
			return c, nil
		}),
	}
	if !e.passiveMode {
		opts = append(opts, ftp.DialWithDisabledEPSV(true))
	}

	conn, err := ftp.Dial(host, opts...)
	if err != nil {
		if stopHandshakeWatch != nil {
			stopHandshakeWatch()
		}
		if rawConn != nil {
			_ = rawConn.Close()
		}
		return nil, nil, fmt.Errorf("FTP connection error: %w", err)
	}

	loginErr := conn.Login(user, password)
	watchStopped := true
	if stopHandshakeWatch != nil {
		watchStopped = stopHandshakeWatch()
	}
	if loginErr != nil || !watchStopped {
		_ = conn.Quit()
		if rawConn != nil {
			_ = rawConn.Close()
		}
		if loginErr != nil {
			return nil, nil, fmt.Errorf("FTP authentication error: %w", loginErr)
		}
		return nil, nil, fmt.Errorf("FTP authentication error: %w", handshakeCtx.Err())
	}

	if rawConn != nil {
		if err := rawConn.SetDeadline(time.Time{}); err != nil {
			_ = conn.Quit()
			_ = rawConn.Close()
			return nil, nil, fmt.Errorf("FTP connection error: %w", err)
		}
	}
	return conn, rawConn, nil
}

// CreateProducer creates an FTP producer
func (e *FTPEndpoint) CreateProducer() (gocamel.Producer, error) {
	return &FTPProducer{endpoint: e, fileExist: gocamel.ParseFileExist(e.url)}, nil
}

// CreateConsumer creates an FTP consumer
func (e *FTPEndpoint) CreateConsumer(processor gocamel.Processor) (gocamel.Consumer, error) {
	opts := gocamel.ParsePollingOptions(e.url)
	if err := validateFTPRemotePath(opts.Move); err != nil {
		return nil, fmt.Errorf("invalid move path: %w", err)
	}
	if err := validateFTPRemotePath(opts.MoveFailed); err != nil {
		return nil, fmt.Errorf("invalid moveFailed path: %w", err)
	}
	return &FTPConsumer{
		endpoint:  e,
		processor: processor,
		opts:      opts,
	}, nil
}

// trackedConn wraps a net.Conn so closing it invokes onClose exactly once,
// removing closed control/data sockets from the consumer's active set.
type trackedConn struct {
	net.Conn
	onClose func()
	once    sync.Once
}

func (tc *trackedConn) Close() error {
	err := tc.Conn.Close()
	tc.once.Do(tc.onClose)
	return err
}

// ftpFileExists checks whether path exists on the FTP server via SIZE.
// A 550 (StatusFileUnavailable) response indicates absence; any other protocol
// or network error is returned so callers do not silently overwrite existing files.
func ftpFileExists(conn *ftp.ServerConn, remotePath string) (bool, error) {
	size, err := conn.FileSize(remotePath)
	if err == nil {
		return size >= 0, nil
	}
	var tpErr *textproto.Error
	if errors.As(err, &tpErr) && tpErr.Code == ftp.StatusFileUnavailable {
		return false, nil
	}
	return false, fmt.Errorf("error checking FTP file existence for %s: %w", remotePath, err)
}

// ---------------------------------------------------------------------------
// Producer
// ---------------------------------------------------------------------------

// FTPProducer represents an FTP producer
type FTPProducer struct {
	endpoint  *FTPEndpoint
	fileExist gocamel.FileExistBehavior
	mu        sync.Mutex      // protects operation initialization and dialCtx
	operation chan struct{}   // exclusive connection lease for the entire transfer
	conn      *ftp.ServerConn // persistent connection (disconnect=false)
	dialCtx   context.Context // current Send context for data-connection dials
	stopped   bool            // guarded by operation lease
}

func (p *FTPProducer) Start(ctx context.Context) error {
	if err := p.acquire(ctx); err != nil {
		return err
	}
	defer p.release()
	p.stopped = false
	return nil
}

// acquire leases the connection for one operation without holding a mutex
// across network I/O. A blocked sender can still cancel while waiting.
func (p *FTPProducer) acquire(ctx context.Context) error {
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

func (p *FTPProducer) release() { <-p.operation }

func (p *FTPProducer) setDialCtx(ctx context.Context) {
	p.mu.Lock()
	p.dialCtx = ctx
	p.mu.Unlock()
}

func (p *FTPProducer) currentDialCtx() context.Context {
	p.mu.Lock()
	ctx := p.dialCtx
	p.mu.Unlock()
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func (p *FTPProducer) Stop() error {
	if err := p.acquire(context.Background()); err != nil {
		return err
	}
	defer p.release()
	p.stopped = true
	if p.conn != nil {
		conn := p.conn
		p.conn = nil
		return conn.Quit()
	}
	return nil
}

func (p *FTPProducer) getConn(ctx context.Context) (*ftp.ServerConn, error) {
	if p.endpoint.disconnect {
		conn, _, err := p.endpoint.connectWithRaw(ctx, p.currentDialCtx, nil)
		return conn, err
	}
	if p.conn != nil {
		if err := p.conn.NoOp(); err == nil {
			return p.conn, nil
		}
		_ = p.conn.Quit()
		p.conn = nil
	}
	conn, _, err := p.endpoint.connectWithRaw(ctx, p.currentDialCtx, nil)
	if err != nil {
		return nil, err
	}
	p.conn = conn
	return conn, nil
}

func (p *FTPProducer) releaseConn(conn *ftp.ServerConn) {
	if p.endpoint.disconnect {
		_ = conn.Quit()
	}
}

func (p *FTPProducer) discardConnOnError(conn *ftp.ServerConn) {
	if !p.endpoint.disconnect && p.conn == conn {
		_ = conn.Quit()
		p.conn = nil
	}
}

// Send sends the exchange content to the FTP server.
func (p *FTPProducer) Send(exchange *gocamel.Exchange) error {
	if err := p.acquire(exchange.Context); err != nil {
		return err
	}
	defer p.release()
	if p.stopped {
		return fmt.Errorf("ftp producer is stopped")
	}

	remotePath := p.endpoint.url.Path
	if remotePath == "" || remotePath == "/" || strings.HasSuffix(remotePath, "/") {
		name, ok := exchange.GetIn().GetHeader(gocamel.CamelFileName)
		if !ok || name == nil {
			if remotePath == "" || remotePath == "/" {
				return fmt.Errorf("no path or CamelFileName specified for FTP")
			}
			return fmt.Errorf("path is a directory but CamelFileName is not specified")
		}
		nameStr := fmt.Sprintf("%v", name)
		if containsControlChar(nameStr) {
			return fmt.Errorf("ftp: invalid CamelFileName: contains control character")
		}
		if cleanName := path.Clean(strings.ReplaceAll(nameStr, "\\", "/")); cleanName == "." || cleanName == "/" || cleanName == "" {
			return fmt.Errorf("ftp: invalid CamelFileName: %q", nameStr)
		}
		baseDir := remotePath
		if baseDir == "/" {
			baseDir = ""
		}
		joined, err := gocamel.JoinRemotePath(baseDir, nameStr)
		if err != nil {
			return fmt.Errorf("ftp: invalid path: %w", err)
		}
		remotePath = joined
	}

	if err := validateFTPRemotePath(remotePath); err != nil {
		return fmt.Errorf("ftp: invalid path: %w", err)
	}

	remotePath = strings.TrimPrefix(remotePath, "/")
	if remotePath == "" {
		return fmt.Errorf("no path or CamelFileName specified for FTP")
	}

	var body []byte
	var err error
	switch b := exchange.GetIn().GetBody().(type) {
	case []byte:
		body = b
	case string:
		body = []byte(b)
	case io.Reader:
		body, err = gocamel.ReadLimited(b, p.endpoint.maxMessageSize)
		if err != nil {
			return fmt.Errorf("error reading body: %w", err)
		}
	default:
		return fmt.Errorf("unsupported body type for FTP: %T", exchange.GetIn().GetBody())
	}

	p.setDialCtx(exchange.Context)
	defer p.setDialCtx(context.Background())

	conn, err := p.getConn(exchange.Context)
	if err != nil {
		return err
	}
	defer p.releaseConn(conn)

	switch p.fileExist {
	case gocamel.FileExistFail:
		exists, err := ftpFileExists(conn, remotePath)
		if err != nil {
			p.discardConnOnError(conn)
			return err
		}
		if exists {
			return fmt.Errorf("file already exists on FTP: %s", remotePath)
		}
	case gocamel.FileExistIgnore:
		exists, err := ftpFileExists(conn, remotePath)
		if err != nil {
			p.discardConnOnError(conn)
			return err
		}
		if exists {
			return nil
		}
	case gocamel.FileExistAppend:
		if err := conn.Append(remotePath, bytes.NewReader(body)); err != nil {
			p.discardConnOnError(conn)
			return fmt.Errorf("error appending FTP file: %w", err)
		}
		return nil
	}

	// STOR (like RETR, APPE, and SIZE above) accepts a full pathname resolved from
	// the login directory, so no ChangeDir is needed. Keeping the session in
	// the login directory matters: with a persistent connection
	// (disconnect=false), a ChangeDir would leak into the next Send and make
	// a relative path resolve against the previous file's directory.
	if err := conn.Stor(remotePath, bytes.NewReader(body)); err != nil {
		p.discardConnOnError(conn)
		return fmt.Errorf("error sending FTP file: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Consumer
// ---------------------------------------------------------------------------

// FTPConsumer represents an FTP consumer
type FTPConsumer struct {
	endpoint  *FTPEndpoint
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
	conn       *ftp.ServerConn // persistent connection (disconnect=false)
	rawConn    net.Conn        // underlying control connection for conn
	// activeConn/activeRaw reference the connection used by the current poll,
	// including disconnect=true mode, so Stop can force-close it to unblock a
	// stalled network operation. Guarded by mu.
	activeConn *ftp.ServerConn
	activeRaw  net.Conn
	// conns tracks every live control and data net.Conn opened by this consumer
	// so Stop and context cancellation can abort stalled LIST/RETR transfers.
	// Guarded by mu.
	conns map[net.Conn]struct{}
	wg    sync.WaitGroup
}

// acquireTransition waits for an in-progress Start/Stop and claims the next
// transition. It never holds c.mu while waiting.
func (c *FTPConsumer) acquireTransition() {
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
func (c *FTPConsumer) releaseTransition() {
	c.mu.Lock()
	close(c.transition)
	c.transition = nil
	c.mu.Unlock()
}

func (c *FTPConsumer) trackConn(raw net.Conn) (net.Conn, error) {
	c.mu.Lock()
	if c.stopping || !c.started {
		c.mu.Unlock()
		_ = raw.Close()
		return nil, fmt.Errorf("ftp consumer is stopped")
	}
	if c.conns == nil {
		c.conns = make(map[net.Conn]struct{})
	}
	var tc *trackedConn
	tc = &trackedConn{
		Conn: raw,
		onClose: func() {
			c.mu.Lock()
			delete(c.conns, tc)
			c.mu.Unlock()
		},
	}
	c.conns[tc] = struct{}{}
	c.mu.Unlock()
	return tc, nil
}

func (c *FTPConsumer) closeTrackedConns() {
	c.mu.Lock()
	conns := make([]net.Conn, 0, len(c.conns))
	for conn := range c.conns {
		conns = append(conns, conn)
	}
	c.conns = nil
	c.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

func (c *FTPConsumer) Start(ctx context.Context) error {
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

func (c *FTPConsumer) poll(ctx context.Context) {
	stopWatch := context.AfterFunc(ctx, c.closeTrackedConns)
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

func (c *FTPConsumer) getConn(ctx context.Context) (*ftp.ServerConn, error) {
	c.mu.Lock()
	if c.stopping || !c.started || ctx.Err() != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("ftp consumer is stopped")
	}
	if c.endpoint.disconnect {
		c.mu.Unlock()
		conn, raw, err := c.endpoint.connectWithRaw(ctx, nil, c.trackConn)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		if c.stopping || !c.started || ctx.Err() != nil {
			c.mu.Unlock()
			_ = conn.Quit()
			if raw != nil {
				_ = raw.Close()
			}
			return nil, fmt.Errorf("ftp consumer is stopped")
		}
		c.activeConn = conn
		c.activeRaw = raw
		c.mu.Unlock()
		return conn, nil
	}

	conn := c.conn
	raw := c.rawConn
	if conn != nil {
		c.activeConn = conn
		c.activeRaw = raw
	}
	c.mu.Unlock()
	if conn != nil {
		if err := conn.NoOp(); err == nil {
			return conn, nil
		}
		_ = conn.Quit()
		if raw != nil {
			_ = raw.Close()
		}
		c.mu.Lock()
		if c.conn == conn {
			c.conn = nil
			c.rawConn = nil
		}
		if c.activeConn == conn {
			c.activeConn = nil
			c.activeRaw = nil
		}
		c.mu.Unlock()
	}

	c.mu.Lock()
	if c.stopping || !c.started || ctx.Err() != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("ftp consumer is stopped")
	}
	c.mu.Unlock()

	newConn, newRaw, err := c.endpoint.connectWithRaw(ctx, nil, c.trackConn)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.stopping || !c.started || ctx.Err() != nil {
		c.mu.Unlock()
		_ = newConn.Quit()
		if newRaw != nil {
			_ = newRaw.Close()
		}
		return nil, fmt.Errorf("ftp consumer is stopped")
	}
	c.conn = newConn
	c.rawConn = newRaw
	c.activeConn = newConn
	c.activeRaw = newRaw
	c.mu.Unlock()
	return newConn, nil
}

func (c *FTPConsumer) releaseConn(conn *ftp.ServerConn) {
	if !c.endpoint.disconnect {
		return
	}
	_ = conn.Quit()
	c.mu.Lock()
	if c.activeConn == conn {
		c.activeConn = nil
		c.activeRaw = nil
	}
	c.mu.Unlock()
}

type ftpSynchronization struct {
	conn       *ftp.ServerConn
	path       string
	lockPath   string
	markerPath string
	noop       bool
	delete     bool
	move       string
	moveFailed string
}

func (s *ftpSynchronization) cleanupLock() {
	if s.markerPath != "" {
		_ = s.conn.Delete(s.markerPath)
	}
}

func (s *ftpSynchronization) OnComplete(exchange *gocamel.Exchange) {
	defer s.cleanupLock()
	effectivePath := s.path
	if s.lockPath != "" {
		effectivePath = s.lockPath
	}

	if s.noop {
		if s.lockPath != "" {
			_ = s.conn.Rename(s.lockPath, s.path)
		}
		return
	}
	if s.move != "" {
		moveFTPFile(s.conn, effectivePath, s.move)
	} else if s.delete {
		if err := s.conn.Delete(effectivePath); err != nil {
			gocamel.LogError("FTP file deletion error", "file", effectivePath, "error", err)
		}
	} else if s.lockPath != "" {
		_ = s.conn.Rename(s.lockPath, s.path)
	}
}

func (s *ftpSynchronization) OnFailure(exchange *gocamel.Exchange) {
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
		moveFTPFile(s.conn, effectivePath, s.moveFailed)
	} else if s.lockPath != "" {
		_ = s.conn.Rename(s.lockPath, s.path)
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

func (c *FTPConsumer) doPoll(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	conn, err := c.getConn(ctx)
	if err != nil {
		gocamel.LogError("FTP polling connection error", "error", err)
		return
	}
	defer c.releaseConn(conn)

	rootPath := strings.Trim(c.endpoint.url.Path, "/")
	if rootPath == "" {
		rootPath = "."
	} else if err := validateFTPRemotePath(rootPath); err != nil {
		gocamel.LogError("invalid FTP polling path", "error", err)
		return
	}

	type ftpFile struct {
		name string
		path string
	}
	var files []ftpFile

	var listDir func(dir string)
	listDir = func(dir string) {
		if ctx.Err() != nil {
			return
		}
		entries, err := conn.List(dir)
		if err != nil {
			gocamel.LogError("FTP listing error", "directory", dir, "error", err)
			return
		}
		for _, entry := range entries {
			if ctx.Err() != nil {
				return
			}
			if entry.Name == "" || entry.Name == "." || entry.Name == ".." {
				continue
			}
			if containsControlChar(entry.Name) || strings.ContainsAny(entry.Name, "/\\") {
				gocamel.LogWarn("skipping unsafe FTP entry name", "directory", dir, "name", entry.Name)
				continue
			}
			fullPath := dir + "/" + entry.Name
			if dir == "." {
				fullPath = entry.Name
			}
			switch entry.Type {
			case ftp.EntryTypeFolder:
				if c.opts.Recursive {
					if isExcludedMoveDir(fullPath, c.opts.Move) || isExcludedMoveDir(fullPath, c.opts.MoveFailed) {
						continue
					}
					listDir(fullPath)
				}
			case ftp.EntryTypeFile:
				if strings.HasSuffix(entry.Name, ".camelLock") || strings.HasSuffix(entry.Name, ".camelExclusiveReadLock") {
					continue
				}
				if gocamel.MatchFileName(entry.Name, c.opts.Include, c.opts.Exclude) {
					files = append(files, ftpFile{name: entry.Name, path: fullPath})
				}
			}
		}
	}
	listDir(rootPath)

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
				size, err := conn.FileSize(f.path)
				if err != nil {
					return 0, time.Time{}, err
				}
				modTime, _ := conn.GetTime(f.path)
				return size, modTime, nil
			}
			if !gocamel.WaitForStable(ctx, statFn, c.opts.ReadLockTimeout, c.opts.ReadLockCheckInterval, c.opts.ReadLockMinAge, c.opts.ReadLockMinLength) {
				if ctx.Err() != nil {
					return
				}
				gocamel.LogWarn("FTP file did not stabilise before read lock timeout; skipping",
					"file", f.path, "timeout", c.opts.ReadLockTimeout)
				continue
			}

		case gocamel.ReadLockRename:
			targetLockPath := f.path + ".camelExclusiveReadLock"
			if err := conn.Rename(f.path, targetLockPath); err != nil {
				gocamel.LogWarn("could not acquire FTP rename read lock; skipping", "file", f.path, "error", err)
				continue
			}
			lockPath = targetLockPath

		case gocamel.ReadLockMarkerFile:
			targetMarkerPath := f.path + ".camelLock"
			exists, err := ftpFileExists(conn, targetMarkerPath)
			if err != nil {
				gocamel.LogWarn("could not check FTP marker file; skipping", "file", f.path, "error", err)
				continue
			}
			if exists {
				// Marker exists: file is locked
				continue
			}
			if err := conn.Stor(targetMarkerPath, bytes.NewReader([]byte{})); err != nil {
				gocamel.LogWarn("could not create marker file for FTP read lock; skipping", "file", f.path, "error", err)
				continue
			}
			markerPath = targetMarkerPath
		}

		readPath := f.path
		if lockPath != "" {
			readPath = lockPath
		}

		resp, err := conn.Retr(readPath)
		if err != nil {
			gocamel.LogError("FTP file retrieval error", "file", readPath, "error", err)
			if markerPath != "" {
				_ = conn.Delete(markerPath)
			}
			if lockPath != "" {
				_ = conn.Rename(lockPath, f.path)
			}
			continue
		}
		content, err := gocamel.ReadLimited(resp, c.endpoint.maxMessageSize)
		_ = resp.Close()
		if err != nil {
			gocamel.LogError("FTP file read error", "file", readPath, "error", err)
			if markerPath != "" {
				_ = conn.Delete(markerPath)
			}
			if lockPath != "" {
				_ = conn.Rename(lockPath, f.path)
			}
			continue
		}

		exchange := gocamel.NewExchange(ctx)
		exchange.SetBody(content)
		exchange.SetHeader(gocamel.CamelFileName, f.name)
		exchange.SetHeader(gocamel.CamelFilePath, f.path)

		exchange.AddSynchronization(&ftpSynchronization{
			conn:       conn,
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

func (c *FTPConsumer) Stop() error {
	c.acquireTransition()
	defer c.releaseTransition()

	c.mu.Lock()
	c.stopping = true
	cancel := c.cancel
	c.cancel = nil
	conn := c.conn
	raw := c.rawConn
	activeConn := c.activeConn
	activeRaw := c.activeRaw
	c.conn = nil
	c.rawConn = nil
	c.activeConn = nil
	c.activeRaw = nil
	conns := make([]net.Conn, 0, len(c.conns))
	for tracked := range c.conns {
		conns = append(conns, tracked)
	}
	c.conns = nil
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	// Close all control and data sockets before waiting so a poll blocked in
	// handshake, LIST, or RETR unblocks instead of stalling Stop.
	for _, tracked := range conns {
		_ = tracked.Close()
	}
	if raw != nil {
		_ = raw.Close()
	}
	if activeRaw != nil && activeRaw != raw {
		_ = activeRaw.Close()
	}

	c.wg.Wait()

	if conn != nil {
		_ = conn.Quit()
	}
	if activeConn != nil && activeConn != conn {
		_ = activeConn.Quit()
	}

	c.mu.Lock()
	c.started = false
	c.stopping = false
	c.mu.Unlock()
	return nil
}

// ftpMkdirAll creates each segment of dir on the FTP server, ignoring errors for
// segments that already exist.
func ftpMkdirAll(conn *ftp.ServerConn, dir string) {
	abs := strings.HasPrefix(dir, "/")
	parts := strings.Split(strings.TrimPrefix(dir, "/"), "/")
	current := ""
	if abs {
		current = "/"
	}
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if current == "" || current == "/" {
			current += part
		} else {
			current += "/" + part
		}
		_ = conn.MakeDir(current)
	}
}

// moveFTPFile renames/moves a file on the FTP server, creating destDir if needed.
func moveFTPFile(conn *ftp.ServerConn, srcPath, destDir string) {
	filename := path.Base(strings.ReplaceAll(srcPath, "\\", "/"))
	filename = strings.TrimSuffix(filename, ".camelExclusiveReadLock")
	cleanDir := path.Clean(strings.ReplaceAll(destDir, "\\", "/"))
	if cleanDir == "" || cleanDir == "." {
		return
	}
	var destPath string
	if cleanDir == "/" {
		destPath = "/" + filename
	} else {
		cleanDir = strings.TrimRight(cleanDir, "/")
		ftpMkdirAll(conn, cleanDir)
		destPath = cleanDir + "/" + filename
	}
	if err := conn.Rename(srcPath, destPath); err != nil {
		gocamel.LogError("FTP move error", "src", srcPath, "dest", destPath, "error", err)
	}
}
