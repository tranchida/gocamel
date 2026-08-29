package mail

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-message"
	pop3 "github.com/knadh/go-pop3"

	gocamel "gitlab.com/tranchida/gocamel"
)

// Pop3Consumer implements email reception via POP3/POP3S.
type Pop3Consumer struct {
	endpoint  *MailEndpoint
	processor gocamel.Processor
	stopChan  chan struct{}
	stopOnce  sync.Once
	wg        sync.WaitGroup
	client    *pop3.Conn
	mu        sync.Mutex
}

// Start starts the POP3 consumer.
func (c *Pop3Consumer) Start(ctx context.Context) error {
	c.wg.Add(1)
	go c.run(ctx)
	return nil
}

// run executes the POP3 polling loop.
func (c *Pop3Consumer) run(ctx context.Context) {
	defer c.wg.Done()

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

// poll connects to the POP3 server and retrieves new messages.
func (c *Pop3Consumer) poll(parentCtx context.Context) {
	ep := c.endpoint

	// Context with timeout for this operation
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
	// Honor the documented disconnect option (like the IMAP consumer): with
	// disconnect=false the connection is kept between polls; with disconnect
	// =true (and on context end) it is torn down after each poll. Previously
	// the unconditional defer made the keep-alive path dead code.
	if ep.disconnect {
		defer c.disconnect()
	}

	// List messages
	count, size, err := c.client.Stat()
	if err != nil {
		if !ep.skipFailedMessage {
			gocamel.LogError("POP3 stat error", "error", err)
		}
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

	// Build list of message IDs
	var msgIDs []int
	for i := 1; i <= count; i++ {
		msgIDs = append(msgIDs, i)
	}

	// Apply fetchSize limit
	if ep.fetchSize > 0 && len(msgIDs) > ep.fetchSize {
		msgIDs = msgIDs[:ep.fetchSize]
	}

	// Process messages in reverse order (oldest first)
	for i := len(msgIDs) - 1; i >= 0; i-- {
		select {
		case <-c.stopChan:
			return
		case <-parentCtx.Done():
			return
		default:
		}

		msgID := msgIDs[i]
		if err := c.processMessage(parentCtx, msgID); err != nil {
			if ep.handleFailedMessage {
				if err2 := c.handleFailedMessage(parentCtx, msgID, err); err2 != nil {
					gocamel.LogError("[POP3] error handler", "error", err2)
				}
			} else if !ep.skipFailedMessage {
				gocamel.LogError("POP3 message processing error", "msgID", msgID, "error", err)
			}
		}
	}

	if ep.debugMode {
		gocamel.LogDebug("POP3 poll finished")
	}
}

// connect establishes a connection to the POP3 server.
func (c *Pop3Consumer) connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client != nil {
		// Already connected
		return nil
	}

	ep := c.endpoint

	// Create POP3 client
	clientOpt := pop3.Opt{
		Host: ep.host,
		Port: ep.port,
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

	// Create client and obtain a connection
	p3 := pop3.New(clientOpt)
	conn, err := p3.NewConn()
	if err != nil {
		return fmt.Errorf("POP3 connection error: %w", err)
	}

	// Authentication
	if ep.username != "" && ep.password != "" {
		if err := conn.Auth(ep.username, ep.password); err != nil {
			conn.Quit()
			return fmt.Errorf("POP3 authentication error: %w", err)
		}
	}

	c.client = conn
	return nil
}

// disconnect closes the POP3 connection.
func (c *Pop3Consumer) disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client != nil {
		c.client.Quit()
		c.client = nil
	}
}

// processMessage fetches and processes a POP3 message.
func (c *Pop3Consumer) processMessage(ctx context.Context, msgID int) error {
	// Download the full message
	msgData, err := c.client.Retr(msgID)
	if err != nil {
		return fmt.Errorf("message retrieval error: %w", err)
	}

	// Parse the message
	mailMsg, err := c.parsePop3Message(msgData)
	if err != nil {
		return fmt.Errorf("message parsing error: %w", err)
	}
	// msgID is a sequential index 1..count built from the server-reported
	// message count (see fetch loop); it cannot exceed uint32 on any real
	// POP3 server, so the narrowing conversion cannot wrap (gosec G115).
	mailMsg.UID = uint32(msgID) // #nosec G115

	// Create exchange
	exchange := gocamel.NewExchange(ctx)
	c.populateExchange(exchange, mailMsg)

	// Process via processor
	if err := gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange)); err != nil {
		return err
	}

	// Post-processing based on options
	return c.postProcess(msgID, exchange)
}

// parsePop3Message parses a retrieved POP3 message.
func (c *Pop3Consumer) parsePop3Message(msgData *message.Entity) (*MailMessage, error) {
	mailMsg := &MailMessage{
		Headers:     make(map[string]string),
		Attachments: make(map[string][]byte),
		Date:        time.Now(),
	}

	// Parse MIME with go-message
	// Read full body from msgData.Body, bounded so an oversized email cannot
	// exhaust memory (maxMessageSize, 0 = unlimited).
	bodyData, err := gocamel.ReadLimited(msgData.Body, c.endpoint.maxMessageSize)
	if err != nil {
		return nil, fmt.Errorf("error reading body: %w", err)
	}

	body, bodyHTML, attachments := parseMimeMessageCommon(bodyData)
	mailMsg.Body = body
	mailMsg.BodyHTML = bodyHTML
	mailMsg.Attachments = attachments

	// Extract headers from the message.Header entity
	// Use Fields() and iterate manually
	mailMsg.Headers = make(map[string]string)
	fields := msgData.Header.Fields()
	for fields.Next() {
		key := fields.Key()
		text, err := fields.Text()
		if err != nil {
			text = fields.Value() // fallback to raw value
		}
		mailMsg.Headers[key] = text

		// Extract important fields
		switch key {
		case "From":
			mailMsg.From = text
		case "Reply-To":
			mailMsg.ReplyTo = text
		case "To":
			mailMsg.To = parseAddresses(text)
		case "Cc":
			mailMsg.Cc = parseAddresses(text)
		case "Subject":
			mailMsg.Subject = text
		case "Message-Id":
			mailMsg.MessageID = text
		case "Content-Type":
			mailMsg.ContentType = text
		case "Date":
			if date, err := time.Parse(time.RFC1123, text); err == nil {
				mailMsg.Date = date
			}
		}
	}

	return mailMsg, nil
}

// populateExchange populates the exchange with POP3 message data.
func (c *Pop3Consumer) populateExchange(exchange *gocamel.Exchange, msg *MailMessage) {
	exchange.SetBody(msg.Body)
	exchange.SetHeader(MailFrom, msg.From)
	exchange.SetHeader(MailReplyTo, msg.ReplyTo)
	exchange.SetHeader(MailTo, strings.Join(msg.To, ", "))
	exchange.SetHeader(MailCC, strings.Join(msg.Cc, ", "))
	exchange.SetHeader(MailSubject, msg.Subject)
	exchange.SetHeader(MailMessageID, msg.MessageID)
	if !msg.Date.IsZero() {
		exchange.SetHeader(MailDate, msg.Date.Format(time.RFC3339))
	}
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

	// Attachments
	for name, data := range msg.Attachments {
		exchange.SetHeader(MailAttachmentPrefix+"_"+name, data)
	}
}

// postProcess applies post-processing actions (delete).
func (c *Pop3Consumer) postProcess(msgID int, exchange *gocamel.Exchange) error {
	ep := c.endpoint

	// Delete
	if ep.delete || c.getBoolHeader(exchange, MailDeleteHeader) {
		if err := c.client.Dele(msgID); err != nil {
			return fmt.Errorf("deletion error for message %d: %w", msgID, err)
		}
		return nil
	}

	return nil
}

// handleFailedMessage allows processing an error via the processor.
func (c *Pop3Consumer) handleFailedMessage(ctx context.Context, msgID int, msgErr error) error {
	exchange := gocamel.NewExchange(ctx)
	exchange.SetHeader("CamelMailError", msgErr.Error())
	exchange.SetHeader("CamelMailFailedMsgID", strconv.Itoa(msgID))

	return gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange))
}

// getBoolHeader retrieves a bool value from a header.
func (c *Pop3Consumer) getBoolHeader(exchange *gocamel.Exchange, header string) bool {
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

// Stop stops the POP3 consumer. Safe to call multiple times: the stop channel
// is closed at most once via sync.Once to prevent a "close of closed channel"
// panic when Stop() is invoked more than once (e.g. during CamelContext rollback).
func (c *Pop3Consumer) Stop() error {
	c.stopOnce.Do(func() { close(c.stopChan) })
	c.wg.Wait()
	c.disconnect()
	return nil
}
