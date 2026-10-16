package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	gocamel "github.com/tranchida/gocamel"
)

// MailConsumer implements email reception via IMAP (using go-imap v2).
type MailConsumer struct {
	endpoint  *MailEndpoint
	processor gocamel.Processor
	stopChan  chan struct{}
	stopOnce  sync.Once
	wg        sync.WaitGroup
	client    *imapclient.Client
	mu        sync.Mutex
	changes   chan struct{} // initialized before connecting; notifications are coalesced
}

// Start starts the mail consumer.
func (c *MailConsumer) Start(ctx context.Context) error {
	c.changes = make(chan struct{}, 1)
	c.wg.Add(1)
	go c.run(ctx)
	return nil
}

// run executes the polling or IDLE loop based on configuration.
func (c *MailConsumer) run(ctx context.Context) {
	defer c.wg.Done()

	ep := c.endpoint

	// If IDLE is enabled and this is IMAP, use IDLE
	if ep.useIdle && ep.isIMAP() {
		if err := c.runWithIdle(ctx); err != nil {
			if ep.debugMode {
				gocamel.LogError("Mail IDLE failed, fallback to polling", "error", err)
			}
			// Fall back to standard polling
			c.runPolling(ctx)
		}
	} else {
		c.runPolling(ctx)
	}
}

// runPolling executes the standard polling loop.
func (c *MailConsumer) runPolling(ctx context.Context) {
	for {
		select {
		case <-c.stopChan:
			return
		case <-ctx.Done():
			return
		default:
			c.poll(ctx)
			select {
			case <-c.stopChan:
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
	if c.changes == nil {
		c.changes = make(chan struct{}, 1)
	}
	ctx, cancel := context.WithTimeout(parentCtx, ep.connectionTimeout)
	defer cancel()
	if err := c.connect(ctx); err != nil {
		return fmt.Errorf("connection error for IDLE: %w", err)
	}
	// Poll once before IDLE so mail already present is not left waiting for a
	// future notification. The selected mailbox also enables unilateral updates.
	if _, err := c.selectFolder(ep.folderName); err != nil {
		return err
	}
	if err := c.processNewMessages(parentCtx); err != nil {
		return err
	}
	for {
		select {
		case <-c.stopChan:
			return nil
		case <-parentCtx.Done():
			return nil
		default:
		}
		c.mu.Lock()
		client := c.client
		c.mu.Unlock()
		if client == nil {
			return errors.New("client disconnected during IDLE")
		}
		idle, err := client.Idle()
		if err != nil {
			return fmt.Errorf("IDLE start error: %w", err)
		}
		done := make(chan error, 1)
		go func() { done <- idle.Wait() }()
		stopping := false
		select {
		case <-c.stopChan:
			stopping = true
		case <-parentCtx.Done():
			stopping = true
		case <-c.changes:
		case err := <-done:
			_ = idle.Close()
			if err == nil {
				err = errors.New("IDLE ended unexpectedly")
			}
			return err
		}
		// Close must precede Wait: EXISTS notifications do not complete IDLE.
		if err := idle.Close(); err != nil {
			_ = client.Close()
			<-done
			return err
		}
		if err := <-done; err != nil {
			return err
		}
		if stopping {
			return nil
		}
		if err := c.processNewMessages(parentCtx); err != nil {
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

	// Search for unread messages
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

	// Process each message
	for _, uid := range uids {
		select {
		case <-c.stopChan:
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
		if err != nil {
			if ep.debugMode {
				gocamel.LogError("Mail search error", "error", err)
			}
			// Fallback: use Fetch with range
			uids = nil
		}
	} else {
		// Otherwise search all messages
		uids, err = c.searchAll()
		if err != nil {
			if ep.debugMode {
				gocamel.LogError("Mail search all error", "error", err)
			}
			uids = nil
		}
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

	// Process messages
	for _, uid := range uids {
		select {
		case <-c.stopChan:
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
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client != nil {
		// Already connected — verify it's still alive
		noopCmd := c.client.Noop()
		if err := noopCmd.Wait(); err == nil {
			return nil
		}
		// Reconnection needed: close the stale connection so its socket and
		// server-side session are released rather than leaked on every
		// reconnect cycle.
		c.client.Close()
		c.client = nil
	}

	ep := c.endpoint

	// Connection options — TLS config
	tlsConfig := &tls.Config{
		ServerName: ep.host,
		MinVersion: tls.VersionTLS12,
	}

	var client *imapclient.Client
	var err error

	opts := &imapclient.Options{
		TLSConfig:             tlsConfig,
		UnilateralDataHandler: &imapclient.UnilateralDataHandler{Mailbox: c.mailboxChanged},
		Dialer:                &net.Dialer{Timeout: ep.connectionTimeout},
	}

	if ep.isSecure() {
		// IMAPS native TLS (port 993)
		client, err = imapclient.DialTLS(ep.address(), opts)
	} else {
		// IMAP with optional STARTTLS (port 143)
		client, err = imapclient.DialStartTLS(ep.address(), opts)
	}

	if err != nil {
		return fmt.Errorf("IMAP connection error: %w", err)
	}

	// Authentication
	if ep.username != "" && ep.password != "" {
		if err := client.Login(ep.username, ep.password).Wait(); err != nil {
			client.Close()
			return fmt.Errorf("authentication error: %w", err)
		}
	}

	c.client = client
	return nil
}

// disconnect closes the connection.
func (c *MailConsumer) disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client != nil {
		c.client.Logout().Wait()
		c.client.Close()
		c.client = nil
	}
}

// selectFolder selects a folder.
func (c *MailConsumer) selectFolder(folderName string) (*imap.SelectData, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		return nil, errors.New("client not connected")
	}

	opts := &imap.SelectOptions{}
	return c.client.Select(folderName, opts).Wait()
}

// searchUnseen searches for unread messages.
func (c *MailConsumer) searchUnseen() ([]imap.UID, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		return nil, errors.New("client not connected")
	}

	criteria := &imap.SearchCriteria{
		NotFlag: []imap.Flag{imap.FlagSeen},
	}

	searchData, err := c.client.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return nil, err
	}

	return searchData.AllUIDs(), nil
}

// searchAll searches for all messages.
func (c *MailConsumer) searchAll() ([]imap.UID, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		return nil, errors.New("client not connected")
	}

	// Empty criteria = all messages
	criteria := &imap.SearchCriteria{}

	searchData, err := c.client.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return nil, err
	}

	return searchData.AllUIDs(), nil
}

// fetchMessage retrieves the content of a message.
func (c *MailConsumer) fetchMessage(uid imap.UID, options *imap.FetchOptions) (*imapclient.FetchMessageBuffer, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		return nil, errors.New("client not connected")
	}

	// Use UID as sequence set
	uidSet := imap.UIDSetNum(uid)

	cmd := c.client.Fetch(uidSet, options)
	messages, err := cmd.Collect()
	if err != nil {
		return nil, err
	}

	if len(messages) == 0 {
		return nil, fmt.Errorf("message %d not found", uid)
	}

	return messages[0], nil
}

// processMessage fetches and processes a message.
func (c *MailConsumer) processMessage(ctx context.Context, uid imap.UID, selected *imap.SelectData) error {
	ep := c.endpoint

	var msg *imapclient.FetchMessageBuffer

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
			BodySection: []*imap.FetchItemBodySection{{}}, // full body
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
			BodySection: []*imap.FetchItemBodySection{{}}, // full body
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

	// Mark as seen if peek=false
	if !ep.peek {
		if err := c.markSeen(uid); err != nil {
			gocamel.LogError("Mail SEEN marking error", "error", err)
		}
	}

	// Process via processor
	if err := gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange)); err != nil {
		// Processing failed — rollback if peek=true
		if ep.peek {
			if err2 := c.markUnseen(uid); err2 != nil {
				gocamel.LogError("Mail SEEN rollback error", "error", err2)
			}
		}
		return err
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
		Date:        time.Now(),
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

	// Extract body from BodySection
	if len(msg.BodySection) > 0 {
		bodyData := msg.BodySection[0].Bytes

		// Parse with go-message if this is a MIME message
		if len(bodyData) > 0 {
			mailMsg.Body, mailMsg.BodyHTML, mailMsg.Attachments = parseMimeMessageCommon(bodyData)
			if mailMsg.Body == nil {
				// Fallback: raw content
				mailMsg.Body = bodyData
			}
		}
	}

	return mailMsg, nil
}

// populateExchange populates the exchange with message data.
func (c *MailConsumer) populateExchange(exchange *gocamel.Exchange, msg *MailMessage) {
	exchange.SetBody(msg.Body)
	exchange.SetHeader(MailFrom, msg.From)
	exchange.SetHeader(MailReplyTo, msg.ReplyTo)
	exchange.SetHeader(MailTo, strings.Join(msg.To, ", "))
	exchange.SetHeader(MailCC, strings.Join(msg.Cc, ", "))
	exchange.SetHeader(MailSubject, msg.Subject)
	exchange.SetHeader(MailMessageID, msg.MessageID)
	exchange.SetHeader(MailDate, msg.Date.Format(time.RFC3339))
	exchange.SetHeader(MailContentType, msg.ContentType)
	exchange.SetHeader(MailSize, strconv.Itoa(len(msg.Body)))
	exchange.SetHeader(MailUID, strconv.FormatUint(uint64(msg.UID), 10))

	// If an HTML version exists, add it as a property
	if msg.BodyHTML != nil {
		exchange.SetProperty("CamelMailBodyHTML", msg.BodyHTML)
	}

	for k, v := range msg.Headers {
		if k != MailFrom && k != MailTo && k != MailSubject && k != MailDate && k != MailContentType {
			// SECURITY: prefix raw envelope headers so they cannot collide
			// with the well-known producer-side headers ("From", "To", ...).
			exchange.SetHeader(MailHeaderPrefix+k, v)
		}
	}

	// Attachments as special headers
	for name, data := range msg.Attachments {
		exchange.SetHeader(MailAttachmentPrefix+"_"+name, data)
	}
}

// markSeen marks a message as read.
func (c *MailConsumer) markSeen(uid imap.UID) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		return errors.New("client not connected")
	}

	uidSet := imap.UIDSetNum(uid)
	store := &imap.StoreFlags{
		Op:     imap.StoreFlagsAdd,
		Silent: true,
		Flags:  []imap.Flag{imap.FlagSeen},
	}

	cmd := c.client.Store(uidSet, store, nil)
	// Wait for the command to finish by consuming data
	_, err := cmd.Collect()
	cmd.Close()
	return err
}

// markUnseen removes the SEEN flag.
func (c *MailConsumer) markUnseen(uid imap.UID) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		return errors.New("client not connected")
	}

	uidSet := imap.UIDSetNum(uid)
	store := &imap.StoreFlags{
		Op:     imap.StoreFlagsDel,
		Silent: true,
		Flags:  []imap.Flag{imap.FlagSeen},
	}

	cmd := c.client.Store(uidSet, store, nil)
	_, err := cmd.Collect()
	cmd.Close()
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

// deleteMessage deletes a message.
func (c *MailConsumer) deleteMessage(uid imap.UID) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		return errors.New("client not connected")
	}

	uidSet := imap.UIDSetNum(uid)

	// Add the \Deleted flag
	store := &imap.StoreFlags{
		Op:     imap.StoreFlagsAdd,
		Silent: true,
		Flags:  []imap.Flag{imap.FlagDeleted},
	}

	cmd := c.client.Store(uidSet, store, nil)
	_, err := cmd.Collect()
	cmd.Close()
	if err != nil {
		return err
	}

	// Expunge (permanent deletion)
	expungeCmd := c.client.Expunge()
	_, err = expungeCmd.Collect()
	expungeCmd.Close()
	return err
}

// moveMessage moves a message to another folder.
func (c *MailConsumer) moveMessage(uid imap.UID, folder string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		return errors.New("client not connected")
	}

	uidSet := imap.UIDSetNum(uid)

	// Use MOVE if supported by the server; otherwise fall back to COPY + DELETE.
	// In go-imap v2, Move returns MoveData.
	_, err := c.client.Move(uidSet, folder).Wait()
	if err != nil {
		// MOVE not supported — fall back to COPY + DELETE
		_, err := c.client.Copy(uidSet, folder).Wait()
		if err != nil {
			return err
		}
		// Mark as deleted in the source folder
		store := &imap.StoreFlags{
			Op:     imap.StoreFlagsAdd,
			Silent: true,
			Flags:  []imap.Flag{imap.FlagDeleted},
		}
		cmd := c.client.Store(uidSet, store, nil)
		_, err = cmd.Collect()
		cmd.Close()
		if err != nil {
			return err
		}
		expungeCmd := c.client.Expunge()
		_, err = expungeCmd.Collect()
		expungeCmd.Close()
		return err
	}

	return nil
}

// copyMessage copies a message to another folder.
func (c *MailConsumer) copyMessage(uid imap.UID, folder string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		return errors.New("client not connected")
	}

	uidSet := imap.UIDSetNum(uid)
	_, err := c.client.Copy(uidSet, folder).Wait()
	return err
}

// handleFailedMessage allows processing an error via the processor.
func (c *MailConsumer) handleFailedMessage(ctx context.Context, uid imap.UID, msgErr error) error {
	exchange := gocamel.NewExchange(ctx)
	exchange.SetHeader("CamelMailError", msgErr.Error())
	exchange.SetHeader("CamelMailFailedUID", strconv.FormatUint(uint64(uid), 10))

	return gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange))
}

// getStringHeader retrieves a string value from a header.
func (c *MailConsumer) getStringHeader(exchange *gocamel.Exchange, header string) string {
	if v, exists := exchange.GetOut().GetHeader(header); exists {
		if s, ok := v.(string); ok {
			return s
		}
	}
	if v, exists := exchange.GetIn().GetHeader(header); exists {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// getBoolHeader retrieves a bool value from a header.
func (c *MailConsumer) getBoolHeader(exchange *gocamel.Exchange, header string) bool {
	if v, exists := exchange.GetOut().GetHeader(header); exists {
		if b, ok := v.(bool); ok {
			return b
		}
		if s, ok := v.(string); ok {
			return strings.EqualFold(s, "true")
		}
	}
	return false
}

// Stop stops the mail consumer. Safe to call multiple times: the stop channel
// is closed at most once via sync.Once to prevent a "close of closed channel"
// panic when Stop() is invoked more than once (e.g. during CamelContext rollback).
func (c *MailConsumer) Stop() error {
	c.stopOnce.Do(func() { close(c.stopChan) })
	c.wg.Wait()
	c.disconnect()
	return nil
}
