package mail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"strconv"
	"sync"
	"time"

	pop3 "github.com/knadh/go-pop3"

	gocamel "github.com/tranchida/gocamel"
)

var errPOP3MessageTooLarge = errors.New("pop3 message exceeds maxMessageSize")

// pop3BoundedConn wraps a net.Conn so an active RETR command cannot read
// unbounded data into memory even if the server lies in LIST.
type pop3BoundedConn struct {
	net.Conn
	mu        sync.Mutex
	limit     int64
	readBytes int64
}

func newPop3BoundedConn(conn net.Conn) *pop3BoundedConn {
	return &pop3BoundedConn{Conn: conn}
}

func (c *pop3BoundedConn) setReadLimit(limit int64) {
	c.mu.Lock()
	c.limit = limit
	c.readBytes = 0
	c.mu.Unlock()
}

func (c *pop3BoundedConn) clearReadLimit() {
	c.mu.Lock()
	c.limit = 0
	c.readBytes = 0
	c.mu.Unlock()
}

func (c *pop3BoundedConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	limit := c.limit
	readSoFar := c.readBytes
	c.mu.Unlock()

	if limit > 0 && readSoFar >= limit {
		return 0, errPOP3MessageTooLarge
	}

	n, err := c.Conn.Read(p)
	if n > 0 {
		c.mu.Lock()
		if c.limit > 0 {
			c.readBytes += int64(n)
			over := c.readBytes > c.limit
			c.mu.Unlock()
			if over {
				return n, errPOP3MessageTooLarge
			}
		} else {
			c.mu.Unlock()
		}
	}
	return n, err
}

// Pop3Consumer implements email reception via POP3/POP3S.
type Pop3Consumer struct {
	endpoint    *MailEndpoint
	processor   gocamel.Processor
	stopChan    chan struct{}
	stopClosed  bool
	wg          sync.WaitGroup
	client      *pop3.Conn
	conn        net.Conn // underlying transport, closed on Stop to unblock I/O
	boundedConn *pop3BoundedConn
	mu          sync.Mutex
	started     bool
	stopping    bool
	stopped     bool
	cancel      context.CancelFunc
}

// pop3CapturingDialer dials with DialContext, applies an initial handshake
// deadline, wraps the connection in a pop3BoundedConn, and records it so Stop
// and context cancellation can unblock stalled servers.
type pop3CapturingDialer struct {
	ctx     context.Context
	timeout time.Duration
	address string
	onDial  func(*pop3BoundedConn)
	mu      sync.Mutex
	conn    *pop3BoundedConn
}

func (d *pop3CapturingDialer) Dial(network, address string) (net.Conn, error) {
	dialCtx := d.ctx
	if dialCtx == nil {
		dialCtx = context.Background()
	}
	target := address
	if d.address != "" {
		target = d.address
	}
	rawConn, err := (&net.Dialer{Timeout: d.timeout}).DialContext(dialCtx, network, target)
	if err != nil {
		return nil, err
	}
	if d.timeout > 0 {
		_ = rawConn.SetDeadline(time.Now().Add(d.timeout))
	}
	bc := newPop3BoundedConn(rawConn)
	d.mu.Lock()
	d.conn = bc
	onDial := d.onDial
	d.mu.Unlock()
	if onDial != nil {
		onDial(bc)
	}
	return bc, nil
}

func (d *pop3CapturingDialer) current() *pop3BoundedConn {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.conn
}

func (c *Pop3Consumer) getClient() *pop3.Conn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client
}

func (c *Pop3Consumer) getConn() net.Conn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn
}

func (c *Pop3Consumer) getBoundedConn() *pop3BoundedConn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.boundedConn
}

func (c *Pop3Consumer) getStopChan() chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopChan
}

// closeStopChanLocked closes c.stopChan at most once. Must be called with c.mu held.
func (c *Pop3Consumer) closeStopChanLocked() {
	if c.stopChan != nil && !c.stopClosed {
		close(c.stopChan)
		c.stopClosed = true
	}
}

func (c *Pop3Consumer) disconnectDeadline() time.Duration {
	const maxShutdownWait = 2 * time.Second
	if c.endpoint != nil && c.endpoint.connectionTimeout > 0 && c.endpoint.connectionTimeout < maxShutdownWait {
		return c.endpoint.connectionTimeout
	}
	return maxShutdownWait
}

func (c *Pop3Consumer) withCommandDeadline(ctx context.Context, fn func() error) error {
	conn := c.getConn()
	if conn != nil && c.endpoint != nil && c.endpoint.connectionTimeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(c.endpoint.connectionTimeout))
		defer func() { _ = conn.SetDeadline(time.Time{}) }()
	}
	if ctx != nil && conn != nil {
		stopAbort := context.AfterFunc(ctx, func() {
			_ = conn.Close()
		})
		defer stopAbort()
	}
	return fn()
}

// Start starts the POP3 consumer.
func (c *Pop3Consumer) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.started || c.stopping {
		c.mu.Unlock()
		return nil
	}
	if c.stopped || c.stopChan == nil || c.stopClosed {
		c.stopChan = make(chan struct{})
		c.stopClosed = false
		c.stopped = false
	}
	stopCh := c.stopChan
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.started = true
	c.wg.Add(1)
	c.mu.Unlock()

	go c.run(ctx, stopCh)
	return nil
}

// run executes the POP3 polling loop.
func (c *Pop3Consumer) run(ctx context.Context, stopCh chan struct{}) {
	defer c.wg.Done()
	defer c.disconnect()

	for {
		select {
		case <-stopCh:
			return
		case <-ctx.Done():
			return
		default:
			c.poll(ctx)
			select {
			case <-stopCh:
				return
			case <-ctx.Done():
				return
			case <-time.After(c.endpoint.pollDelay):
				// Continue
			}
		}
	}
}

// poll connects to the POP3 server and retrieves new messages.
func (c *Pop3Consumer) poll(parentCtx context.Context) {
	ep := c.endpoint

	// Context with timeout for connection establishment
	ctx, cancel := context.WithTimeout(parentCtx, ep.connectionTimeout)
	defer cancel()

	if ep.debugMode {
		gocamel.LogInfo("POP3 polling", "address", ep.address())
	}

	// Connect to POP3 server
	if err := c.connect(ctx); err != nil {
		if !ep.skipFailedMessage {
			gocamel.LogError("POP3 connection error", "error", err)
		}
		return
	}

	deletedAny := false
	defer func() {
		// In POP3 (RFC 1939), DELE only commits when the session enters
		// UPDATE via QUIT, and open sessions hold a static snapshot of the
		// maildrop. Disconnect when configured or when any message was deleted.
		if ep.disconnect || deletedAny {
			c.disconnect()
		}
	}()

	client := c.getClient()
	if client == nil {
		return
	}

	// Stat mailbox
	var count, size int
	err := c.withCommandDeadline(parentCtx, func() error {
		var statErr error
		count, size, statErr = client.Stat()
		return statErr
	})
	if err != nil {
		if !ep.skipFailedMessage {
			gocamel.LogError("POP3 stat error", "error", err)
		}
		c.disconnect()
		return
	}

	if count == 0 {
		if ep.debugMode {
			gocamel.LogDebug("POP3 no new messages")
		}
		return
	}

	if ep.debugMode {
		gocamel.LogInfo("POP3 messages found", "count", count, "size", size)
	}

	// Fetch message list with sizes before calling RETR so oversized entries
	// can be rejected without downloading their payloads.
	var msgs []pop3.MessageID
	err = c.withCommandDeadline(parentCtx, func() error {
		var listErr error
		msgs, listErr = client.List(0)
		return listErr
	})
	if err != nil {
		if !ep.skipFailedMessage {
			gocamel.LogError("POP3 list error", "error", err)
		}
		c.disconnect()
		return
	}

	// Apply fetchSize limit
	if ep.fetchSize > 0 && len(msgs) > ep.fetchSize {
		msgs = msgs[:ep.fetchSize]
	}

	stopCh := c.getStopChan()
	for _, m := range msgs {
		select {
		case <-stopCh:
			return
		case <-parentCtx.Done():
			return
		default:
		}

		deleted, procErr := c.processMessage(parentCtx, m)
		if deleted {
			deletedAny = true
		}
		if procErr != nil {
			if ep.handleFailedMessage {
				if err2 := c.handleFailedMessage(parentCtx, m.ID, procErr); err2 != nil {
					gocamel.LogError("[POP3] error handler", "error", err2)
				}
			} else if !ep.skipFailedMessage {
				gocamel.LogError("POP3 message processing error", "msgID", m.ID, "error", procErr)
			}
			// If the connection was dropped by a transport/size-limit error, stop this poll.
			if c.getClient() == nil {
				return
			}
		}
	}

	if ep.debugMode {
		gocamel.LogDebug("POP3 poll finished")
	}
}

// connect establishes a connection to the POP3 server.
func (c *Pop3Consumer) connect(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	existing := c.client
	c.mu.Unlock()

	if existing != nil {
		err := c.withCommandDeadline(ctx, existing.Noop)
		if err == nil {
			return nil
		}
		c.disconnect()
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	ep := c.endpoint

	dialer := &pop3CapturingDialer{
		ctx:     ctx,
		timeout: ep.connectionTimeout,
		address: ep.address(),
	}
	dialer.onDial = func(bc *pop3BoundedConn) {
		c.mu.Lock()
		if c.stopping || c.stopped {
			c.mu.Unlock()
			_ = bc.Close()
			return
		}
		c.conn = bc
		c.boundedConn = bc
		c.mu.Unlock()
	}
	clientOpt := pop3.Opt{
		Host:        ep.host,
		Port:        ep.port,
		DialTimeout: ep.connectionTimeout,
		Dialer:      dialer,
	}

	// TLS for POP3S
	if ep.isSecure() {
		clientOpt.TLSEnabled = true
	} else {
		// SECURITY: POP3 without TLS transmits the password and every fetched
		// message in cleartext. Use pop3s:// to enable TLS.
		gocamel.LogError("POP3 cleartext: password and messages travel unencrypted. "+
			"Use pop3s:// for TLS.", "host", ep.host, "port", ep.port)
	}

	stopAbort := context.AfterFunc(ctx, func() {
		if bc := dialer.current(); bc != nil {
			_ = bc.Close()
		}
	})
	defer stopAbort()

	// Create client and obtain a connection
	p3 := pop3.New(clientOpt)
	conn, err := p3.NewConn()
	rawConn := dialer.current()
	if err != nil {
		if rawConn != nil {
			_ = rawConn.Close()
		}
		c.mu.Lock()
		if c.conn == rawConn {
			c.conn = nil
			c.boundedConn = nil
		}
		c.mu.Unlock()
		return fmt.Errorf("POP3 connection error: %w", err)
	}

	abort := func() {
		if rawConn != nil {
			_ = rawConn.SetDeadline(time.Now().Add(c.disconnectDeadline()))
		}
		_ = conn.Quit()
		if rawConn != nil {
			_ = rawConn.Close()
		}
		c.mu.Lock()
		if c.conn == rawConn {
			c.conn = nil
			c.boundedConn = nil
		}
		c.mu.Unlock()
	}

	// Publish the transport before authentication so Stop can close it while
	// Auth is blocked waiting for the server.
	c.mu.Lock()
	if c.stopping || c.stopped {
		c.mu.Unlock()
		abort()
		return errors.New("pop3 consumer stopped")
	}
	c.conn = rawConn
	c.boundedConn = rawConn
	c.mu.Unlock()

	// Authentication
	if ep.username != "" && ep.password != "" {
		if err := conn.Auth(ep.username, ep.password); err != nil {
			abort()
			return fmt.Errorf("POP3 authentication error: %w", err)
		}
	}

	if rawConn != nil {
		_ = rawConn.SetDeadline(time.Time{})
	}

	if err := ctx.Err(); err != nil {
		abort()
		return err
	}

	c.mu.Lock()
	if c.stopping || c.stopped {
		c.mu.Unlock()
		abort()
		return errors.New("pop3 consumer stopped")
	}
	c.client = conn
	c.conn = rawConn
	c.boundedConn = rawConn
	c.mu.Unlock()
	return nil
}

// disconnect closes the POP3 connection, attempting QUIT first within a short deadline.
func (c *Pop3Consumer) disconnect() {
	c.mu.Lock()
	client := c.client
	conn := c.conn
	c.client = nil
	c.conn = nil
	c.boundedConn = nil
	c.mu.Unlock()

	if client != nil {
		deadline := c.disconnectDeadline()
		if conn != nil {
			_ = conn.SetDeadline(time.Now().Add(deadline))
		}
		done := make(chan struct{})
		go func() {
			_ = client.Quit()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(deadline):
			if conn != nil {
				_ = conn.Close()
			}
			<-done
		}
	}
	if conn != nil {
		_ = conn.Close()
	}
}

// processMessage fetches and processes a POP3 message.
func (c *Pop3Consumer) processMessage(ctx context.Context, msg pop3.MessageID) (bool, error) {
	ep := c.endpoint
	client := c.getClient()
	if client == nil {
		return false, errors.New("pop3 client not connected")
	}

	if ep.maxMessageSize > 0 && int64(msg.Size) > ep.maxMessageSize {
		return false, fmt.Errorf("message %d exceeds maxMessageSize (%d > %d bytes)", msg.ID, msg.Size, ep.maxMessageSize)
	}

	// Download raw RFC 5322 message bytes via RetrRaw while enforcing a
	// wire-level byte limit on the underlying conn so a lying LIST entry
	// cannot exhaust memory.
	bc := c.getBoundedConn()
	var rawBuf *bytes.Buffer
	err := c.withCommandDeadline(ctx, func() error {
		if bc != nil && ep.maxMessageSize > 0 {
			wireLimit := pop3WireLimit(ep.maxMessageSize)
			bc.setReadLimit(wireLimit)
			defer bc.clearReadLimit()
		}
		var retrErr error
		rawBuf, retrErr = client.RetrRaw(msg.ID)
		return retrErr
	})
	if err != nil {
		// The TCP stream may be out of sync after a failed/aborted RETR.
		c.disconnect()
		return false, fmt.Errorf("message retrieval error: %w", err)
	}

	rawBytes, err := gocamel.ReadLimited(bytes.NewReader(rawBuf.Bytes()), ep.maxMessageSize)
	if err != nil {
		return false, fmt.Errorf("message %d exceeds maxMessageSize: %w", msg.ID, err)
	}

	mailMsg := &MailMessage{
		Headers:     make(map[string]string),
		Attachments: make(map[string][]byte),
	}
	parseRawEmailMessage(rawBytes, ep.maxMessageSize, mailMsg)

	// msg.ID is a sequential index 1..count reported by POP3 LIST; guard
	// negative values before narrowing to uint32 (gosec G115).
	if msg.ID > 0 {
		mailMsg.UID = uint32(msg.ID) // #nosec G115
	}

	exchange := gocamel.NewExchange(ctx)
	c.populateExchange(exchange, mailMsg)

	if err := gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange)); err != nil {
		return false, err
	}

	return c.postProcess(ctx, msg.ID, exchange)
}

func pop3WireLimit(maxMessageSize int64) int64 {
	if maxMessageSize <= 0 {
		return 0
	}
	overhead := maxMessageSize/16 + 4096
	if maxMessageSize > math.MaxInt64-overhead {
		return math.MaxInt64
	}
	return maxMessageSize + overhead
}

// populateExchange populates the exchange with POP3 message data.
func (c *Pop3Consumer) populateExchange(exchange *gocamel.Exchange, msg *MailMessage) {
	populateMailExchange(exchange, msg)
}

// postProcess applies post-processing actions (delete).
func (c *Pop3Consumer) postProcess(ctx context.Context, msgID int, exchange *gocamel.Exchange) (bool, error) {
	ep := c.endpoint

	if ep.delete || c.getBoolHeader(exchange, MailDeleteHeader) {
		client := c.getClient()
		if client == nil {
			return false, errors.New("pop3 client not connected")
		}
		err := c.withCommandDeadline(ctx, func() error {
			return client.Dele(msgID)
		})
		if err != nil {
			return false, fmt.Errorf("deletion error for message %d: %w", msgID, err)
		}
		return true, nil
	}

	return false, nil
}

// handleFailedMessage allows processing an error via the processor.
func (c *Pop3Consumer) handleFailedMessage(ctx context.Context, msgID int, msgErr error) error {
	exchange := gocamel.NewExchange(ctx)
	exchange.SetHeader("CamelMailError", msgErr.Error())
	exchange.SetHeader("CamelMailFailedMsgID", strconv.Itoa(msgID))

	return gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange))
}

// getBoolHeader retrieves a bool value from Out then In headers.
func (c *Pop3Consumer) getBoolHeader(exchange *gocamel.Exchange, header string) bool {
	return getBoolHeaderFromExchange(exchange, header)
}

// Stop stops the POP3 consumer. Safe to call multiple times: stopChan is
// closed at most once under c.mu, and an established POP3 session is given a
// brief window to issue QUIT before the socket is closed.
func (c *Pop3Consumer) Stop() error {
	c.mu.Lock()
	if c.stopping {
		c.mu.Unlock()
		c.wg.Wait()
		return nil
	}
	if !c.started {
		c.stopped = true
		c.closeStopChanLocked()
		c.mu.Unlock()
		c.disconnect()
		return nil
	}
	c.stopping = true
	cancel := c.cancel
	c.cancel = nil
	hasClient := c.client != nil
	conn := c.conn
	c.closeStopChanLocked()
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	// If no POP3 session is established yet (stalled during handshake/auth),
	// close the transport immediately to unblock connect(). Otherwise give the
	// worker a brief deadline to exit and send QUIT before force-closing.
	if !hasClient && conn != nil {
		_ = conn.Close()
	}

	deadline := c.disconnectDeadline()
	if hasClient && conn != nil {
		_ = conn.SetDeadline(time.Now().Add(deadline))
	}

	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(deadline):
		if cn := c.getConn(); cn != nil {
			_ = cn.Close()
		}
		<-done
	}

	c.disconnect()

	c.mu.Lock()
	c.started = false
	c.stopping = false
	c.stopped = true
	c.cancel = nil
	c.mu.Unlock()
	return nil
}
