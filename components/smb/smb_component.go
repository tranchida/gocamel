package smb

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

	"github.com/hirochachacha/go-smb2"

	gocamel "github.com/tranchida/gocamel"
)

// smbFileHandle abstracts an open remote SMB file handle for testing.
type smbFileHandle interface {
	io.ReadWriteCloser
}

// smbShare abstracts *smb2.Share operations used by SMBProducer and SMBConsumer.
type smbShare interface {
	Stat(name string) (os.FileInfo, error)
	Open(name string) (smbFileHandle, error)
	OpenFile(name string, flag int, perm os.FileMode) (smbFileHandle, error)
	ReadDir(dirname string) ([]os.FileInfo, error)
	Mkdir(name string, perm os.FileMode) error
	Rename(oldpath, newpath string) error
	Remove(name string) error
	Umount() error
}

type realSMBShare struct {
	*smb2.Share
}

func (s realSMBShare) Open(name string) (smbFileHandle, error) {
	f, err := s.Share.Open(name)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, nil
	}
	return f, nil
}

func (s realSMBShare) OpenFile(name string, flag int, perm os.FileMode) (smbFileHandle, error) {
	f, err := s.Share.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, nil
	}
	return f, nil
}

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

// containsTraversalSegment reports whether s contains any ".." path component.
func containsTraversalSegment(s string) bool {
	norm := strings.ReplaceAll(s, "\\", "/")
	for _, part := range strings.Split(norm, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

// validateSMBRemotePath rejects ASCII control characters alongside traversal
// validation from gocamel.ValidateRemotePath, even when built standalone
// against gocamel v0.20.0.
func validateSMBRemotePath(p string) error {
	if containsControlChar(p) {
		return fmt.Errorf("path contains control character")
	}
	return gocamel.ValidateRemotePath(p)
}

func splitSharePath(rawPath string) (string, string) {
	parts := strings.SplitN(strings.TrimLeft(rawPath, "/"), "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return "", ""
	}
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], parts[1]
}

// SMBComponent represents the SMB component.
type SMBComponent struct{}

// NewSMBComponent creates a new SMBComponent.
func NewSMBComponent() *SMBComponent {
	return &SMBComponent{}
}

// CreateEndpoint creates a new SMB endpoint.
func (c *SMBComponent) CreateEndpoint(uri string) (gocamel.Endpoint, error) {
	u, err := gocamel.ParseURI(uri)
	if err != nil {
		return nil, err
	}
	if err := validateSMBRemotePath(u.Path); err != nil {
		return nil, fmt.Errorf("invalid SMB path: %w", err)
	}
	if _, relPath := splitSharePath(u.Path); relPath != "" {
		if err := validateSMBRemotePath(relPath); err != nil {
			return nil, fmt.Errorf("invalid SMB path: %w", err)
		}
	}
	if move := gocamel.GetConfigValue(u, "move"); move != "" {
		if err := validateSMBRemotePath(move); err != nil {
			return nil, fmt.Errorf("invalid move path: %w", err)
		}
	}
	if moveFailed := gocamel.GetConfigValue(u, "moveFailed"); moveFailed != "" {
		if err := validateSMBRemotePath(moveFailed); err != nil {
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
	return &SMBEndpoint{
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

// SMBEndpoint represents an SMB endpoint.
type SMBEndpoint struct {
	uri            string
	url            *url.URL
	comp           *SMBComponent
	connectTimeout time.Duration
	disconnect     bool
	maxMessageSize int64
	connectFn      func(ctx context.Context, onDial func(net.Conn)) (*smbConn, error)
}

// URI returns the endpoint URI.
func (e *SMBEndpoint) URI() string {
	return gocamel.RedactURI(e.uri)
}

func (e *SMBEndpoint) targetAddr() string {
	if e.url.Port() == "" {
		return net.JoinHostPort(e.url.Hostname(), "445")
	}
	return e.url.Host
}

type smbConn struct {
	tcp     net.Conn
	session *smb2.Session
	share   smbShare
}

// abort force-closes the transport. It is used by Stop to unblock a poll that
// is stuck in a network operation; Umount/Logoff are skipped for live SMB
// sessions because they would try to use the same broken transport.
func (sc *smbConn) abort() {
	if sc.tcp != nil {
		_ = sc.tcp.Close()
	}
	if sc.session == nil && sc.share != nil {
		_ = sc.share.Umount()
	}
}

func (sc *smbConn) close() {
	if sc.share != nil {
		_ = sc.share.Umount()
	}
	if sc.session != nil {
		_ = sc.session.Logoff()
	}
	if sc.tcp != nil {
		_ = sc.tcp.Close()
	}
}

// connect establishes an SMB connection.
func (e *SMBEndpoint) connect(ctx context.Context) (*smbConn, error) {
	return e.connectWithDial(ctx, nil)
}

// connectWithDial is connect but calls onDial as soon as the TCP transport is
// established, before the SMB handshake, so a consumer can force-close it
// during shutdown. Context cancellation and connectTimeout bound both dialing
// and the SMB session/mount handshake.
func (e *SMBEndpoint) connectWithDial(ctx context.Context, onDial func(net.Conn)) (*smbConn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if e.connectFn != nil {
		return e.connectFn(ctx, onDial)
	}

	shareName, _ := e.shareName()
	if shareName == "" {
		return nil, fmt.Errorf("no share name specified in the SMB URI")
	}

	host := e.targetAddr()
	timeout := e.connectTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	handshakeCtx, cancelHandshake := context.WithTimeout(ctx, timeout)
	defer cancelHandshake()

	dialer := &net.Dialer{Timeout: timeout}
	tcp, err := dialer.DialContext(handshakeCtx, "tcp", host)
	if err != nil {
		return nil, fmt.Errorf("TCP connection error (SMB): %w", err)
	}
	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := handshakeCtx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := tcp.SetDeadline(deadline); err != nil {
		_ = tcp.Close()
		return nil, fmt.Errorf("TCP connection error (SMB): %w", err)
	}
	stopHandshakeWatch := context.AfterFunc(handshakeCtx, func() {
		_ = tcp.Close()
	})
	if onDial != nil {
		onDial(tcp)
	}

	d := &smb2.Dialer{
		Initiator: &smb2.NTLMInitiator{
			User:     gocamel.GetConfigValue(e.url, "username"),
			Password: gocamel.GetConfigValue(e.url, "password"),
			Domain:   gocamel.GetConfigValue(e.url, "domain"),
		},
	}

	session, err := d.DialContext(handshakeCtx, tcp)
	if err != nil {
		stopHandshakeWatch()
		_ = tcp.Close()
		return nil, fmt.Errorf("SMB session error: %w", err)
	}

	share, err := session.WithContext(handshakeCtx).Mount(shareName)
	if err != nil {
		stopHandshakeWatch()
		_ = session.Logoff()
		_ = tcp.Close()
		return nil, fmt.Errorf("error mounting SMB share %s: %w", shareName, err)
	}
	if !stopHandshakeWatch() {
		_ = share.Umount()
		_ = session.Logoff()
		_ = tcp.Close()
		return nil, fmt.Errorf("SMB connection error: %w", handshakeCtx.Err())
	}
	if err := tcp.SetDeadline(time.Time{}); err != nil {
		_ = share.Umount()
		_ = session.Logoff()
		_ = tcp.Close()
		return nil, fmt.Errorf("TCP connection error (SMB): %w", err)
	}

	return &smbConn{tcp: tcp, session: session, share: realSMBShare{share}}, nil
}

// shareName returns the share name and the relative path within that share.
func (e *SMBEndpoint) shareName() (string, string) {
	return splitSharePath(e.url.Path)
}

// getFilePath returns the file/directory path relative to the share using SMB backslashes.
func (e *SMBEndpoint) getFilePath() string {
	_, p := e.shareName()
	return strings.ReplaceAll(p, "/", "\\")
}

// CreateProducer creates an SMB producer.
func (e *SMBEndpoint) CreateProducer() (gocamel.Producer, error) {
	return &SMBProducer{endpoint: e, fileExist: gocamel.ParseFileExist(e.url)}, nil
}

// CreateConsumer creates an SMB consumer.
func (e *SMBEndpoint) CreateConsumer(processor gocamel.Processor) (gocamel.Consumer, error) {
	opts := gocamel.ParsePollingOptions(e.url)
	if err := validateSMBRemotePath(opts.Move); err != nil {
		return nil, fmt.Errorf("invalid move path: %w", err)
	}
	if err := validateSMBRemotePath(opts.MoveFailed); err != nil {
		return nil, fmt.Errorf("invalid moveFailed path: %w", err)
	}
	return &SMBConsumer{
		endpoint:  e,
		processor: processor,
		opts:      opts,
	}, nil
}

// ---------------------------------------------------------------------------
// Producer
// ---------------------------------------------------------------------------

// SMBProducer represents an SMB producer.
type SMBProducer struct {
	endpoint  *SMBEndpoint
	fileExist gocamel.FileExistBehavior
	mu        sync.Mutex    // protects operation initialization
	operation chan struct{} // exclusive connection lease for the entire transfer
	sc        *smbConn      // persistent connection (disconnect=false)
	stopped   bool          // guarded by operation lease
}

func (p *SMBProducer) Start(ctx context.Context) error {
	if err := p.acquire(ctx); err != nil {
		return err
	}
	defer p.release()
	p.stopped = false
	return nil
}

// acquire leases the connection for one operation without holding a mutex
// across network I/O. A blocked sender can still cancel while waiting.
func (p *SMBProducer) acquire(ctx context.Context) error {
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

func (p *SMBProducer) release() { <-p.operation }

func (p *SMBProducer) Stop() error {
	if err := p.acquire(context.Background()); err != nil {
		return err
	}
	defer p.release()
	p.stopped = true
	if p.sc != nil {
		p.sc.close()
		p.sc = nil
	}
	return nil
}

func (p *SMBProducer) getConn(ctx context.Context) (*smbConn, error) {
	if p.endpoint.disconnect {
		return p.endpoint.connect(ctx)
	}
	if p.sc != nil {
		if _, err := p.sc.share.Stat("."); err == nil {
			return p.sc, nil
		}
		p.sc.close()
		p.sc = nil
	}
	sc, err := p.endpoint.connect(ctx)
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
	if exchange == nil {
		return fmt.Errorf("smb: exchange must not be nil")
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
		return fmt.Errorf("smb producer is stopped")
	}

	_, posixPath := p.endpoint.shareName()
	posixPath = strings.ReplaceAll(posixPath, "\\", "/")
	if posixPath == "" || strings.HasSuffix(posixPath, "/") {
		name, ok := exchange.GetIn().GetHeader(gocamel.CamelFileName)
		if !ok || name == nil {
			return fmt.Errorf("no file name (CamelFileName) specified for SMB")
		}
		nameStr := fmt.Sprintf("%v", name)
		if containsControlChar(nameStr) {
			return fmt.Errorf("smb: invalid CamelFileName: contains control character")
		}
		if containsTraversalSegment(nameStr) {
			return fmt.Errorf("smb: invalid CamelFileName: contains traversal")
		}
		if cleanName := path.Clean(strings.ReplaceAll(nameStr, "\\", "/")); cleanName == "." || cleanName == "/" || cleanName == "" {
			return fmt.Errorf("smb: invalid CamelFileName: %q", nameStr)
		}
		joined, err := gocamel.JoinRemotePath(posixPath, nameStr)
		if err != nil {
			return fmt.Errorf("smb: invalid path: %w", err)
		}
		posixPath = joined
	}

	if err := validateSMBRemotePath(posixPath); err != nil {
		return fmt.Errorf("smb: invalid path: %w", err)
	}
	posixPath = strings.TrimLeft(path.Clean(posixPath), "/")
	if posixPath == "" || posixPath == "." || containsTraversalSegment(posixPath) {
		return fmt.Errorf("no file name (CamelFileName) specified for SMB")
	}

	// Compute POSIX parent dir before converting '/' -> '\' so path.Dir works on Unix too.
	posixDir := path.Dir(posixPath)
	smbPath := strings.ReplaceAll(posixPath, "/", "\\")

	var body []byte
	var err error
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
		return fmt.Errorf("unsupported body type for SMB: %T", exchange.GetIn().GetBody())
	}

	sc, err := p.getConn(ctx)
	if err != nil {
		return err
	}
	defer p.releaseConn(sc)

	if posixDir != "." && posixDir != "/" && posixDir != "" {
		smbDir := strings.ReplaceAll(posixDir, "/", "\\")
		if err := smbMkdirAll(sc.share, smbDir); err != nil {
			return fmt.Errorf("error creating SMB directory: %w", err)
		}
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

	file, err := sc.share.OpenFile(smbPath, openFlags, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			if p.fileExist == gocamel.FileExistFail {
				return fmt.Errorf("file already exists on SMB: %s", smbPath)
			}
			if p.fileExist == gocamel.FileExistIgnore {
				return nil
			}
		}
		return fmt.Errorf("error creating SMB file: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()

	if _, err = io.Copy(file, bytes.NewReader(body)); err != nil {
		return fmt.Errorf("error writing SMB file: %w", err)
	}
	closed = true
	if err := file.Close(); err != nil {
		return fmt.Errorf("error closing SMB file: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Consumer
// ---------------------------------------------------------------------------

// SMBConsumer represents an SMB consumer.
type SMBConsumer struct {
	endpoint  *SMBEndpoint
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
	sc         *smbConn // persistent connection (disconnect=false)
	// active references the connection used by the current poll, including
	// disconnect=true mode, so Stop can abort it to unblock a stalled network
	// operation. Guarded by mu.
	active    *smbConn
	activeRaw net.Conn
	wg        sync.WaitGroup
}

// acquireTransition waits for an in-progress Start/Stop and claims the next
// transition. It never holds c.mu while waiting.
func (c *SMBConsumer) acquireTransition() {
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
func (c *SMBConsumer) releaseTransition() {
	c.mu.Lock()
	close(c.transition)
	c.transition = nil
	c.mu.Unlock()
}

func (c *SMBConsumer) closeActiveConns() {
	c.detachAndCloseConns(false)
}

func (c *SMBConsumer) detachAndCloseConns(markStopping bool) {
	c.mu.Lock()
	var cancel context.CancelFunc
	if markStopping {
		c.stopping = true
		cancel = c.cancel
		c.cancel = nil
	}
	sc := c.sc
	active := c.active
	activeRaw := c.activeRaw
	c.sc = nil
	c.active = nil
	c.activeRaw = nil
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	// Abort transports before waiting so a poll blocked in a network read
	// is unblocked instead of keeping Stop (and route shutdown) stuck.
	if activeRaw != nil {
		_ = activeRaw.Close()
	}
	if active != nil {
		active.abort()
	}
	if sc != nil && sc != active {
		sc.abort()
	}
}

func (c *SMBConsumer) Start(ctx context.Context) error {
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

func (c *SMBConsumer) poll(ctx context.Context) {
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

func (c *SMBConsumer) dialAndTrack(ctx context.Context, persistent bool) (*smbConn, error) {
	c.mu.Lock()
	if c.stopping || !c.started || ctx.Err() != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("smb consumer is stopped")
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

	sc, err := c.endpoint.connectWithDial(ctx, onDial)
	if err != nil {
		c.mu.Lock()
		if c.activeRaw == dialedRaw {
			c.activeRaw = nil
		}
		c.mu.Unlock()
		return nil, err
	}

	c.mu.Lock()
	if c.stopping || !c.started || ctx.Err() != nil {
		if c.activeRaw == sc.tcp {
			c.activeRaw = nil
		}
		c.mu.Unlock()
		sc.close()
		return nil, fmt.Errorf("smb consumer is stopped")
	}
	if persistent {
		c.sc = sc
	}
	c.active = sc
	if sc.tcp != nil {
		c.activeRaw = sc.tcp
	}
	c.mu.Unlock()
	return sc, nil
}

func (c *SMBConsumer) getConn(ctx context.Context) (*smbConn, error) {
	if c.endpoint.disconnect {
		return c.dialAndTrack(ctx, false)
	}

	c.mu.Lock()
	if c.stopping || !c.started || ctx.Err() != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("smb consumer is stopped")
	}
	sc := c.sc
	if sc != nil {
		c.active = sc
		c.activeRaw = sc.tcp
	}
	c.mu.Unlock()

	if sc != nil {
		if _, err := sc.share.Stat("."); err == nil {
			return sc, nil
		}
		sc.close()
		c.mu.Lock()
		if c.sc == sc {
			c.sc = nil
		}
		if c.active == sc {
			c.active = nil
			c.activeRaw = nil
		}
		c.mu.Unlock()
	}

	return c.dialAndTrack(ctx, true)
}

func (c *SMBConsumer) releaseConn(sc *smbConn) {
	if c.endpoint.disconnect {
		sc.close()
	}
	c.mu.Lock()
	if c.active == sc {
		c.active = nil
		c.activeRaw = nil
	}
	c.mu.Unlock()
}

type smbFile struct {
	name string
	path string
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

// listSMBFiles lists files (recursively if opts.Recursive) applying include/exclude filters.
func (c *SMBConsumer) listSMBFiles(ctx context.Context, share smbShare, dirPath string) []smbFile {
	if ctx.Err() != nil {
		return nil
	}
	entries, err := share.ReadDir(dirPath)
	if err != nil {
		gocamel.LogError("SMB listing error", "directory", dirPath, "error", err)
		return nil
	}

	posixDir := strings.ReplaceAll(dirPath, "\\", "/")
	var result []smbFile
	for _, entry := range entries {
		if ctx.Err() != nil {
			return result
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			continue
		}
		entryName := entry.Name()
		if entryName == "" || entryName == "." || entryName == ".." {
			continue
		}
		if containsControlChar(entryName) || strings.ContainsAny(entryName, "/\\") {
			gocamel.LogWarn("skipping unsafe SMB entry name", "directory", dirPath, "name", entryName)
			continue
		}
		entryPath := strings.ReplaceAll(path.Join(posixDir, entryName), "/", "\\")

		if entry.IsDir() {
			if c.opts.Recursive {
				if isExcludedMoveDir(entryPath, c.opts.Move) || isExcludedMoveDir(entryPath, c.opts.MoveFailed) {
					continue
				}
				result = append(result, c.listSMBFiles(ctx, share, entryPath)...)
			}
			continue
		}
		if strings.HasSuffix(entryName, ".camelLock") || strings.HasSuffix(entryName, ".camelExclusiveReadLock") {
			continue
		}
		if gocamel.MatchFileName(entryName, c.opts.Include, c.opts.Exclude) {
			result = append(result, smbFile{name: entryName, path: entryPath})
		}
	}
	return result
}

type smbSynchronization struct {
	share      smbShare
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
			gocamel.LogError("SMB file deletion error", "file", effectivePath, "error", err)
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
	if ctx.Err() != nil {
		return
	}
	sc, err := c.getConn(ctx)
	if err != nil {
		gocamel.LogError("SMB polling connection error", "error", err)
		return
	}
	defer c.releaseConn(sc)

	rootPath := strings.TrimRight(c.endpoint.getFilePath(), "\\")
	if rootPath == "" {
		rootPath = "."
	} else if err := validateSMBRemotePath(rootPath); err != nil {
		gocamel.LogError("invalid SMB polling path", "error", err)
		return
	}

	files := c.listSMBFiles(ctx, sc.share, rootPath)

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
				info, err := sc.share.Stat(f.path)
				if err != nil {
					return 0, time.Time{}, err
				}
				return info.Size(), info.ModTime(), nil
			}
			if !gocamel.WaitForStable(ctx, statFn, c.opts.ReadLockTimeout, c.opts.ReadLockCheckInterval, c.opts.ReadLockMinAge, c.opts.ReadLockMinLength) {
				if ctx.Err() != nil {
					return
				}
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
			mf, err := sc.share.OpenFile(targetMarkerPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if err != nil {
				if !errors.Is(err, os.ErrExist) {
					gocamel.LogWarn("could not create marker file for SMB read lock; skipping", "file", f.path, "error", err)
				}
				continue
			}
			if err := mf.Close(); err != nil {
				_ = sc.share.Remove(targetMarkerPath)
				gocamel.LogWarn("could not close marker file for SMB read lock; skipping", "file", f.path, "error", err)
				continue
			}
			markerPath = targetMarkerPath
		}

		readPath := f.path
		if lockPath != "" {
			readPath = lockPath
		}

		file, err := sc.share.Open(readPath)
		if err != nil {
			gocamel.LogError("SMB file open error", "file", readPath, "error", err)
			if markerPath != "" {
				_ = sc.share.Remove(markerPath)
			}
			if lockPath != "" {
				_ = sc.share.Rename(lockPath, f.path)
			}
			continue
		}
		content, err := gocamel.ReadLimited(file, c.endpoint.maxMessageSize)
		_ = file.Close()
		if err != nil {
			gocamel.LogError("SMB file read error", "file", readPath, "error", err)
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

		_ = gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange))
		count++
	}
}

func (c *SMBConsumer) Stop() error {
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

// moveSMBFile renames/moves a file on the SMB share.
func moveSMBFile(share smbShare, srcPath, destDir string) {
	filename := path.Base(strings.ReplaceAll(srcPath, "\\", "/"))
	filename = strings.TrimSuffix(filename, ".camelExclusiveReadLock")
	if filename == "" || filename == "." || filename == ".." {
		gocamel.LogError("SMB move error: invalid source filename", "src", srcPath)
		return
	}

	posixDestDir := strings.ReplaceAll(destDir, "\\", "/")
	posixDestDir = strings.TrimLeft(posixDestDir, "/")
	posixDestDir = path.Clean(posixDestDir)
	posixDestDir = strings.TrimLeft(posixDestDir, "/")

	var destPath string
	if posixDestDir == "" || posixDestDir == "." {
		destPath = filename
	} else {
		smbDestDir := strings.ReplaceAll(posixDestDir, "/", "\\")
		if err := smbMkdirAll(share, smbDestDir); err != nil {
			gocamel.LogError("SMB directory creation error", "directory", smbDestDir, "error", err)
			return
		}
		destPath = strings.ReplaceAll(path.Join(posixDestDir, filename), "/", "\\")
		destPath = strings.TrimLeft(destPath, "\\")
	}

	if err := share.Rename(srcPath, destPath); err != nil {
		gocamel.LogError("SMB move error", "src", srcPath, "dest", destPath, "error", err)
	}
}

// smbMkdirAll recursively creates directories on an SMB share.
func smbMkdirAll(share smbShare, dirPath string) error {
	dirPath = strings.ReplaceAll(dirPath, "/", "\\")
	dirPath = strings.TrimLeft(dirPath, "\\")
	parts := strings.Split(dirPath, "\\")
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
		if err := share.Mkdir(current, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
			if info, statErr := share.Stat(current); statErr == nil && info.IsDir() {
				continue
			}
			return err
		}
	}
	return nil
}
