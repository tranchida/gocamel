package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/charset"

	gocamel "github.com/tranchida/gocamel"
)

// MailConsumer implements email reception via IMAP (using go-imap v2).
type MailConsumer struct {
	endpoint   *MailEndpoint
	processor  gocamel.Processor
	stopChan   chan struct{}
	stopClosed bool
	wg         sync.WaitGroup
	client     *imapclient.Client
	conn       net.Conn
	tlsConfig  *tls.Config // optional test override; nil uses default TLS config
	mu         sync.Mutex
	started    bool
	stopping   bool
	stopped    bool
	cancel     context.CancelFunc
	changes    chan struct{} // initialized before connecting; notifications are coalesced
}

func (c *MailConsumer) getClient() *imapclient.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client
}

func (c *MailConsumer) getConn() net.Conn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn
}

func (c *MailConsumer) getStopChan() chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopChan
}

// closeStopChanLocked closes c.stopChan at most once. Must be called with c.mu held.
func (c *MailConsumer) closeStopChanLocked() {
	if c.stopChan != nil && !c.stopClosed {
		close(c.stopChan)
		c.stopClosed = true
	}
}

func (c *MailConsumer) isStopping(ctx context.Context) bool {
	if ctx != nil && ctx.Err() != nil {
		return true
	}
	c.mu.Lock()
	stopping := c.stopping || c.stopped
	stopCh := c.stopChan
	stopClosed := c.stopClosed
	c.mu.Unlock()
	if stopping || stopClosed {
		return true
	}
	if stopCh != nil {
		select {
		case <-stopCh:
			return true
		default:
		}
	}
	return false
}

func (c *MailConsumer) disconnectDeadline() time.Duration {
	const maxShutdownWait = 2 * time.Second
	if c.endpoint != nil && c.endpoint.connectionTimeout > 0 && c.endpoint.connectionTimeout < maxShutdownWait {
		return c.endpoint.connectionTimeout
	}
	return maxShutdownWait
}

// Start starts the mail consumer.
func (c *MailConsumer) Start(ctx context.Context) error {
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
	if c.changes == nil {
		c.changes = make(chan struct{}, 1)
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

// run executes the polling or IDLE loop based on configuration.
func (c *MailConsumer) run(ctx context.Context, stopCh chan struct{}) {
	defer c.wg.Done()
	defer c.disconnect()

	ep := c.endpoint

	// If IDLE is enabled and this is IMAP, use IDLE
	if ep.useIdle && ep.isIMAP() {
		if err := c.runWithIdle(ctx); err != nil {
			if c.isStopping(ctx) {
				return
			}
			if ep.debugMode {
				gocamel.LogError("Mail IDLE failed, fallback to polling", "error", err)
			}
			// Fall back to standard polling
			c.runPolling(ctx, stopCh)
		}
	} else {
		c.runPolling(ctx, stopCh)
	}
}

// runPolling executes the standard polling loop.
func (c *MailConsumer) runPolling(ctx context.Context, stopCh chan struct{}) {
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

// runWithIdle uses IMAP IDLE for real-time push notifications.
// Returns an error if IDLE is not supported or fails.
func (c *MailConsumer) runWithIdle(parentCtx context.Context) error {
	ep := c.endpoint
	c.mu.Lock()
	if c.changes == nil {
		c.changes = make(chan struct{}, 1)
	}
	changes := c.changes
	stopCh := c.stopChan
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(parentCtx, ep.connectionTimeout)
	defer cancel()
	if err := c.connect(ctx); err != nil {
		if c.isStopping(parentCtx) {
			return nil
		}
		return fmt.Errorf("connection error for IDLE: %w", err)
	}
	// Poll once before IDLE so mail already present is not left waiting for a
	// future notification. The selected mailbox also enables unilateral updates.
	if _, err := c.selectFolder(ep.folderName); err != nil {
		if c.isStopping(parentCtx) {
			return nil
		}
		return err
	}
	if err := c.processNewMessages(parentCtx); err != nil {
		if c.isStopping(parentCtx) {
			return nil
		}
		return err
	}
	for {
		select {
		case <-stopCh:
			return nil
		case <-parentCtx.Done():
			return nil
		default:
		}
		client := c.getClient()
		if client == nil {
			if c.isStopping(parentCtx) {
				return nil
			}
			return errors.New("client disconnected during IDLE")
		}
		idle, err := client.Idle()
		if err != nil {
			if c.isStopping(parentCtx) {
				return nil
			}
			return fmt.Errorf("IDLE start error: %w", err)
		}
		done := make(chan error, 1)
		go func() { done <- idle.Wait() }()
		stopping := false
		select {
		case <-stopCh:
			stopping = true
		case <-parentCtx.Done():
			stopping = true
		case <-changes:
		case err := <-done:
			_ = idle.Close()
			if c.isStopping(parentCtx) {
				return nil
			}
			if err == nil {
				err = errors.New("IDLE ended unexpectedly")
			}
			return err
		}
		if stopping {
			deadline := c.disconnectDeadline()
			if conn := c.getConn(); conn != nil {
				_ = conn.SetDeadline(time.Now().Add(deadline))
			}
			_ = idle.Close()
			select {
			case <-done:
			case <-time.After(deadline):
				_ = client.Close()
				if conn := c.getConn(); conn != nil {
					_ = conn.Close()
				}
				<-done
			}
			return nil
		}
		// Close must precede Wait: EXISTS notifications do not complete IDLE.
		if conn := c.getConn(); conn != nil && ep.connectionTimeout > 0 {
			_ = conn.SetDeadline(time.Now().Add(ep.connectionTimeout))
		}
		if err := idle.Close(); err != nil {
			_ = client.Close()
			<-done
			if c.isStopping(parentCtx) {
				return nil
			}
			return err
		}
		err = <-done
		if conn := c.getConn(); conn != nil {
			_ = conn.SetDeadline(time.Time{})
		}
		if err != nil {
			if c.isStopping(parentCtx) {
				return nil
			}
			return err
		}
		if c.isStopping(parentCtx) {
			return nil
		}
		if err := c.processNewMessages(parentCtx); err != nil {
			if c.isStopping(parentCtx) {
				return nil
			}
			gocamel.LogError("Mail error processing IDLE notification", "error", err)
		}
	}
}

func (c *MailConsumer) mailboxChanged(data *imapclient.UnilateralDataMailbox) {
	if data.NumMessages == nil {
		return
	}
	select {
	case c.changes <- struct{}{}:
	default:
	}
}

// processNewMessages searches for and processes unread messages.
func (c *MailConsumer) processNewMessages(parentCtx context.Context) error {
	ep := c.endpoint

	var uids []imap.UID
	var err error

	if ep.unseen {
		uids, err = c.searchUnseen()
	} else {
		uids, err = c.searchAll()
	}

	if err != nil {
		return fmt.Errorf("message search error: %w", err)
	}

	if len(uids) == 0 {
		return nil
	}

	if ep.debugMode {
		gocamel.LogInfo("Mail messages found via IDLE", "count", len(uids))
	}

	// Apply fetchSize limit
	if ep.fetchSize > 0 && len(uids) > ep.fetchSize {
		uids = uids[:ep.fetchSize]
	}

	stopCh := c.getStopChan()
	for _, uid := range uids {
		select {
		case <-stopCh:
			return nil
		case <-parentCtx.Done():
			return nil
		default:
		}

		if err := c.processMessage(parentCtx, uid, nil); err != nil {
			if ep.handleFailedMessage {
				if err2 := c.handleFailedMessage(parentCtx, uid, err); err2 != nil {
					gocamel.LogError("Mail error in error handler", "error", err2)
				}
			} else if !ep.skipFailedMessage {
				gocamel.LogError("Mail error processing message", "uid", uid, "error", err)
			}
		}
	}

	return nil
}

// poll connects to the IMAP server and fetches new messages.
func (c *MailConsumer) poll(parentCtx context.Context) {
	ep := c.endpoint

	// Context with timeout for this poll operation
	ctx, cancel := context.WithTimeout(parentCtx, ep.connectionTimeout)
	defer cancel()

	if ep.debugMode {
		gocamel.LogInfo("Mail polling", "address", ep.address(), "folder", ep.folderName)
	}

	// Connect to IMAP server
	if err := c.connect(ctx); err != nil {
		if !ep.skipFailedMessage {
			gocamel.LogError("Mail connection error", "error", err)
		}
		return
	}

	// Disconnect at the end if configured
	if ep.disconnect {
		defer c.disconnect()
	}

	// Select folder
	selected, err := c.selectFolder(ep.folderName)
	if err != nil {
		if !ep.skipFailedMessage {
			gocamel.LogError("Mail folder selection error", "folder", ep.folderName, "error", err)
		}
		return
	}

	// Search for unread messages if unseen option is set
	var uids []imap.UID
	if ep.unseen {
		uids, err = c.searchUnseen()
	} else {
		uids, err = c.searchAll()
	}
	if err != nil {
		if !ep.skipFailedMessage {
			gocamel.LogError("Mail search error", "error", err)
		}
		return
	}

	// If no results, return
	if len(uids) == 0 {
		if ep.debugMode {
			gocamel.LogDebug("Mail no new messages")
		}
		return
	}

	if ep.debugMode {
		gocamel.LogInfo("Mail messages found", "count", len(uids))
	}

	// Apply fetchSize limit
	if ep.fetchSize > 0 && len(uids) > ep.fetchSize {
		uids = uids[:ep.fetchSize]
	}

	stopCh := c.getStopChan()
	for _, uid := range uids {
		select {
		case <-stopCh:
			return
		case <-parentCtx.Done():
			return
		default:
		}

		if err := c.processMessage(parentCtx, uid, selected); err != nil {
			if ep.handleFailedMessage {
				if err2 := c.handleFailedMessage(parentCtx, uid, err); err2 != nil {
					gocamel.LogError("Mail error in error handler", "error", err2)
				}
			} else if !ep.skipFailedMessage {
				gocamel.LogError("Mail error processing message", "uid", uid, "error", err)
			}
		}
	}

	if ep.debugMode {
		gocamel.LogDebug("Mail poll finished")
	}
}

// connect establishes a connection to the IMAP server.
func (c *MailConsumer) connect(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	ep := c.endpoint

	c.mu.Lock()
	existingClient := c.client
	existingConn := c.conn
	c.mu.Unlock()

	if existingClient != nil {
		// Already connected — verify it's still alive without holding c.mu
		if existingConn != nil && ep.connectionTimeout > 0 {
			_ = existingConn.SetDeadline(time.Now().Add(ep.connectionTimeout))
		}
		err := existingClient.Noop().Wait()
		if existingConn != nil {
			_ = existingConn.SetDeadline(time.Time{})
		}
		if err == nil {
			return nil
		}
		// Reconnection needed: close the stale connection so its socket and
		// server-side session are released rather than leaked on every
		// reconnect cycle.
		_ = existingClient.Close()
		if existingConn != nil {
			_ = existingConn.Close()
		}
		c.mu.Lock()
		if c.client == existingClient {
			c.client = nil
			c.conn = nil
		}
		c.mu.Unlock()
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	var tlsConfig *tls.Config
	if c.tlsConfig != nil {
		tlsConfig = c.tlsConfig.Clone()
	} else {
		tlsConfig = &tls.Config{
			ServerName: ep.host,
			MinVersion: tls.VersionTLS12,
		}
	}

	dialer := &net.Dialer{Timeout: ep.connectionTimeout}
	rawConn, err := dialer.DialContext(ctx, "tcp", ep.address())
	if err != nil {
		return fmt.Errorf("IMAP connection error: %w", err)
	}

	if ep.connectionTimeout > 0 {
		_ = rawConn.SetDeadline(time.Now().Add(ep.connectionTimeout))
	}
	stopAbort := context.AfterFunc(ctx, func() {
		_ = rawConn.Close()
	})
	defer stopAbort()

	opts := &imapclient.Options{
		TLSConfig:             tlsConfig,
		UnilateralDataHandler: &imapclient.UnilateralDataHandler{Mailbox: c.mailboxChanged},
		WordDecoder:           &mime.WordDecoder{CharsetReader: charset.Reader},
		Dialer:                dialer,
	}

	var conn net.Conn = rawConn
	var client *imapclient.Client

	if ep.isSecure() {
		// IMAPS native TLS (port 993)
		if tlsConfig.NextProtos == nil {
			tlsConfig.NextProtos = []string{"imap"}
		}
		tlsConn := tls.Client(rawConn, tlsConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = rawConn.Close()
			return fmt.Errorf("IMAP connection error: %w", err)
		}
		conn = tlsConn
		client = imapclient.New(tlsConn, opts)
	} else {
		// IMAP with STARTTLS (port 143)
		client, err = imapclient.NewStartTLS(rawConn, opts)
		if err != nil {
			_ = rawConn.Close()
			return fmt.Errorf("IMAP connection error: %w", err)
		}
	}

	// Authentication
	if ep.username != "" && ep.password != "" {
		if err := client.Login(ep.username, ep.password).Wait(); err != nil {
			_ = client.Close()
			_ = conn.Close()
			return fmt.Errorf("authentication error: %w", err)
		}
	}

	_ = rawConn.SetDeadline(time.Time{})

	c.mu.Lock()
	if c.stopping || c.stopped {
		c.mu.Unlock()
		_ = client.Close()
		_ = conn.Close()
		return errors.New("mail consumer stopped")
	}
	c.client = client
	c.conn = conn
	c.mu.Unlock()
	return nil
}

// disconnect closes the connection without holding c.mu across network I/O.
func (c *MailConsumer) disconnect() {
	c.mu.Lock()
	client := c.client
	conn := c.conn
	c.client = nil
	c.conn = nil
	c.mu.Unlock()

	if client != nil {
		deadline := c.disconnectDeadline()
		if conn != nil {
			_ = conn.SetDeadline(time.Now().Add(deadline))
		}
		done := make(chan struct{})
		go func() {
			_ = client.Logout().Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(deadline):
		}
		_ = client.Close()
		if conn != nil {
			_ = conn.Close()
		}
	}
}

// selectFolder selects a folder.
func (c *MailConsumer) selectFolder(folderName string) (*imap.SelectData, error) {
	client := c.getClient()
	if client == nil {
		return nil, errors.New("client not connected")
	}

	opts := &imap.SelectOptions{}
	return client.Select(folderName, opts).Wait()
}

// searchUnseen searches for unread messages.
func (c *MailConsumer) searchUnseen() ([]imap.UID, error) {
	client := c.getClient()
	if client == nil {
		return nil, errors.New("client not connected")
	}

	criteria := &imap.SearchCriteria{
		NotFlag: []imap.Flag{imap.FlagSeen},
	}

	searchData, err := client.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return nil, err
	}

	return searchData.AllUIDs(), nil
}

// searchAll searches for all messages.
func (c *MailConsumer) searchAll() ([]imap.UID, error) {
	client := c.getClient()
	if client == nil {
		return nil, errors.New("client not connected")
	}

	// Empty criteria = all messages
	criteria := &imap.SearchCriteria{}

	searchData, err := client.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return nil, err
	}

	return searchData.AllUIDs(), nil
}

// fetchMessage retrieves a message, streaming body literals through gocamel.ReadLimited
// so oversized literals cannot exhaust memory even if RFC822.SIZE is inaccurate.
func (c *MailConsumer) fetchMessage(uid imap.UID, options *imap.FetchOptions) (*imapclient.FetchMessageBuffer, error) {
	client := c.getClient()
	if client == nil {
		return nil, errors.New("client not connected")
	}

	var maxBytes int64
	if c.endpoint != nil {
		maxBytes = c.endpoint.maxMessageSize
	}

	uidSet := imap.UIDSetNum(uid)
	cmd := client.Fetch(uidSet, options)

	var result *imapclient.FetchMessageBuffer
	for {
		msg := cmd.Next()
		if msg == nil {
			break
		}
		buf := &imapclient.FetchMessageBuffer{SeqNum: msg.SeqNum}
		for {
			item := msg.Next()
			if item == nil {
				break
			}
			switch it := item.(type) {
			case imapclient.FetchItemDataUID:
				buf.UID = it.UID
			case imapclient.FetchItemDataFlags:
				buf.Flags = it.Flags
			case imapclient.FetchItemDataEnvelope:
				buf.Envelope = it.Envelope
			case imapclient.FetchItemDataInternalDate:
				buf.InternalDate = it.Time
			case imapclient.FetchItemDataRFC822Size:
				buf.RFC822Size = it.Size
			case imapclient.FetchItemDataBodySection:
				var b []byte
				if it.Literal != nil {
					var err error
					b, err = gocamel.ReadLimited(it.Literal, maxBytes)
					if err != nil {
						_ = cmd.Close()
						return nil, fmt.Errorf("message %d body read error: %w", uid, err)
					}
				}
				buf.BodySection = append(buf.BodySection, imapclient.FetchBodySectionBuffer{
					Section: it.Section,
					Bytes:   b,
				})
			}
		}
		if result == nil {
			result = buf
		}
	}

	if err := cmd.Close(); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("message %d not found", uid)
	}
	return result, nil
}

// processMessage fetches and processes a message.
func (c *MailConsumer) processMessage(ctx context.Context, uid imap.UID, _ *imap.SelectData) error {
	ep := c.endpoint

	var msg *imapclient.FetchMessageBuffer
	bodySection := &imap.FetchItemBodySection{Peek: ep.peek}

	if ep.maxMessageSize > 0 {
		// Fetch metadata (envelope, flags, size) first so an oversized message
		// is rejected before its — potentially large — body is downloaded.
		meta, err := c.fetchMessage(uid, &imap.FetchOptions{
			UID:        true,
			Flags:      true,
			Envelope:   true,
			RFC822Size: true,
		})
		if err != nil {
			return fmt.Errorf("message metadata retrieval error: %w", err)
		}

		if meta.RFC822Size > ep.maxMessageSize {
			return fmt.Errorf("message %d exceeds maxMessageSize (%d > %d bytes)", uid, meta.RFC822Size, ep.maxMessageSize)
		}

		// Fetch the body and merge it into the metadata buffer for parsing.
		body, err := c.fetchMessage(uid, &imap.FetchOptions{
			BodySection: []*imap.FetchItemBodySection{bodySection},
		})
		if err != nil {
			return fmt.Errorf("message body retrieval error: %w", err)
		}
		meta.BodySection = body.BodySection
		msg = meta
	} else {
		// Fetch message with all required info in a single round-trip.
		var err error
		msg, err = c.fetchMessage(uid, &imap.FetchOptions{
			UID:         true,
			Flags:       true,
			Envelope:    true,
			BodySection: []*imap.FetchItemBodySection{bodySection},
		})
		if err != nil {
			return fmt.Errorf("message retrieval error: %w", err)
		}
	}

	// Parse the message
	mailMsg, err := c.parseMessage(msg)
	if err != nil {
		return fmt.Errorf("message parsing error: %w", err)
	}

	// Create exchange
	exchange := gocamel.NewExchange(ctx)
	c.populateExchange(exchange, mailMsg)

	// Process via processor
	if err := gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange)); err != nil {
		return err
	}

	// When peek=true, BODY.PEEK[] left \Seen untouched during FETCH; mark \Seen
	// only now that route processing succeeded.
	if ep.peek && !mailMsg.Seen {
		if err := c.markSeen(uid); err != nil {
			gocamel.LogError("Mail SEEN marking error", "error", err)
		}
	}

	// Post-processing based on headers set by the processor
	return c.postProcess(uid, exchange)
}

// parseMessage extracts the data from an IMAP message.
func (c *MailConsumer) parseMessage(msg *imapclient.FetchMessageBuffer) (*MailMessage, error) {
	mailMsg := &MailMessage{
		UID:         uint32(msg.UID),
		Headers:     make(map[string]string),
		Attachments: make(map[string][]byte),
	}

	// Extract info from IMAP envelope if available
	if msg.Envelope != nil {
		mailMsg.Subject = msg.Envelope.Subject
		if len(msg.Envelope.From) > 0 {
			mailMsg.From = msg.Envelope.From[0].Addr()
		}
		if len(msg.Envelope.ReplyTo) > 0 {
			mailMsg.ReplyTo = msg.Envelope.ReplyTo[0].Addr()
		}
		for _, to := range msg.Envelope.To {
			mailMsg.To = append(mailMsg.To, to.Addr())
		}
		for _, cc := range msg.Envelope.Cc {
			mailMsg.Cc = append(mailMsg.Cc, cc.Addr())
		}
		if msg.Envelope.MessageID != "" {
			mailMsg.MessageID = msg.Envelope.MessageID
		}
		if !msg.Envelope.Date.IsZero() {
			mailMsg.Date = msg.Envelope.Date
		}
	}

	// Flag state
	for _, flag := range msg.Flags {
		if flag == imap.FlagSeen {
			mailMsg.Seen = true
		}
		if flag == imap.FlagDeleted {
			mailMsg.Deleted = true
		}
	}

	maxSize := gocamel.DefaultMaxBodySize
	if c.endpoint != nil {
		maxSize = c.endpoint.maxMessageSize
	}

	// Extract headers, Content-Type, body, and attachments from BodySection
	if len(msg.BodySection) > 0 && len(msg.BodySection[0].Bytes) > 0 {
		parseRawEmailMessage(msg.BodySection[0].Bytes, maxSize, mailMsg)
	}
	if mailMsg.Date.IsZero() {
		mailMsg.Date = time.Now()
	}

	return mailMsg, nil
}

// populateExchange populates the exchange with message data.
func (c *MailConsumer) populateExchange(exchange *gocamel.Exchange, msg *MailMessage) {
	populateMailExchange(exchange, msg)
}

// markSeen marks a message as read.
func (c *MailConsumer) markSeen(uid imap.UID) error {
	client := c.getClient()
	if client == nil {
		return errors.New("client not connected")
	}

	uidSet := imap.UIDSetNum(uid)
	store := &imap.StoreFlags{
		Op:     imap.StoreFlagsAdd,
		Silent: true,
		Flags:  []imap.Flag{imap.FlagSeen},
	}

	cmd := client.Store(uidSet, store, nil)
	_, err := cmd.Collect()
	return err
}

// postProcess applies post-processing actions (delete, move, copy).
func (c *MailConsumer) postProcess(uid imap.UID, exchange *gocamel.Exchange) error {
	ep := c.endpoint

	// Delete
	if ep.delete || c.getBoolHeader(exchange, MailDeleteHeader) {
		if err := c.deleteMessage(uid); err != nil {
			return fmt.Errorf("deletion error: %w", err)
		}
		return nil
	}

	// MoveTo
	if moveTo := c.getStringHeader(exchange, MailMoveToHeader); moveTo != "" {
		if err := c.moveMessage(uid, moveTo); err != nil {
			return fmt.Errorf("move error: %w", err)
		}
		return nil
	}

	// MoveTo from config
	if ep.moveTo != "" {
		if err := c.moveMessage(uid, ep.moveTo); err != nil {
			return fmt.Errorf("move error: %w", err)
		}
		return nil
	}

	// CopyTo
	if copyTo := c.getStringHeader(exchange, MailCopyToHeader); copyTo != "" {
		if err := c.copyMessage(uid, copyTo); err != nil {
			return fmt.Errorf("copy error: %w", err)
		}
	}

	// CopyTo from config
	if ep.copyTo != "" {
		if err := c.copyMessage(uid, ep.copyTo); err != nil {
			return fmt.Errorf("copy error: %w", err)
		}
	}

	return nil
}

// deleteMessage deletes a message using UIDEXPUNGE when supported by the server.
func (c *MailConsumer) deleteMessage(uid imap.UID) error {
	client := c.getClient()
	if client == nil {
		return errors.New("client not connected")
	}

	uidSet := imap.UIDSetNum(uid)

	// Add the \Deleted flag
	store := &imap.StoreFlags{
		Op:     imap.StoreFlagsAdd,
		Silent: true,
		Flags:  []imap.Flag{imap.FlagDeleted},
	}

	cmd := client.Store(uidSet, store, nil)
	if _, err := cmd.Collect(); err != nil {
		return err
	}

	caps := client.Caps()
	var expungeCmd *imapclient.ExpungeCommand
	if caps.Has(imap.CapUIDPlus) || caps.Has(imap.CapIMAP4rev2) {
		expungeCmd = client.UIDExpunge(uidSet)
	} else {
		expungeCmd = client.Expunge()
	}
	_, err := expungeCmd.Collect()
	return err
}

// moveMessage moves a message to another folder. go-imap v2's Client.Move
// automatically falls back to COPY + STORE + (UID)EXPUNGE when CapMove is absent.
func (c *MailConsumer) moveMessage(uid imap.UID, folder string) error {
	client := c.getClient()
	if client == nil {
		return errors.New("client not connected")
	}

	uidSet := imap.UIDSetNum(uid)
	_, err := client.Move(uidSet, folder).Wait()
	return err
}

// copyMessage copies a message to another folder.
func (c *MailConsumer) copyMessage(uid imap.UID, folder string) error {
	client := c.getClient()
	if client == nil {
		return errors.New("client not connected")
	}

	uidSet := imap.UIDSetNum(uid)
	_, err := client.Copy(uidSet, folder).Wait()
	return err
}

// handleFailedMessage allows processing an error via the processor.
func (c *MailConsumer) handleFailedMessage(ctx context.Context, uid imap.UID, msgErr error) error {
	exchange := gocamel.NewExchange(ctx)
	exchange.SetHeader("CamelMailError", msgErr.Error())
	exchange.SetHeader("CamelMailFailedUID", strconv.FormatUint(uint64(uid), 10))

	return gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange))
}

// getStringHeader retrieves a string value from Out then In headers.
func (c *MailConsumer) getStringHeader(exchange *gocamel.Exchange, header string) string {
	return getStringHeaderFromExchange(exchange, header)
}

// getBoolHeader retrieves a bool value from Out then In headers.
func (c *MailConsumer) getBoolHeader(exchange *gocamel.Exchange, header string) bool {
	return getBoolHeaderFromExchange(exchange, header)
}

// Stop stops the mail consumer. Safe to call multiple times: stopChan is
// closed at most once under c.mu to prevent data races when Stop() is invoked
// concurrently or across Start/Stop cycles.
func (c *MailConsumer) Stop() error {
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
	conn := c.conn
	c.closeStopChanLocked()
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	deadline := c.disconnectDeadline()
	if conn != nil {
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
		if cl := c.getClient(); cl != nil {
			_ = cl.Close()
		}
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
