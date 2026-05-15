package gocamel

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"
	pop3 "github.com/knadh/go-pop3"
)

// Supported mail protocols
const (
	ProtocolSMTP  = "smtp"
	ProtocolSMTPS = "smtps"
	ProtocolPOP3  = "pop3"
	ProtocolPOP3S = "pop3s"
	ProtocolIMAP  = "imap"
	ProtocolIMAPS = "imaps"
)

// Standard Mail component headers
const (
	// Inbound headers (consumer)
	MailFrom        = "From"
	MailTo          = "To"
	MailCC          = "Cc"
	MailBCC         = "Bcc"
	MailSubject     = "Subject"
	MailMessageID   = "Message-ID"
	MailDate        = "Date"
	MailContentType = "Content-Type"
	MailSize        = "Size"
	MailUID         = "UID"
	MailReplyTo     = "Reply-To"

	// Attachment headers
	MailAttachmentPrefix = "CamelMailAttachment"

	// Control headers
	MailDeleteHeader = "CamelMailDelete"
	MailMoveToHeader = "CamelMailMoveTo"
	MailCopyToHeader = "CamelMailCopyTo"
)

// Default configuration
const (
	DefaultSMTPPort          = 25
	DefaultSMTPSPort         = 465
	DefaultPOP3Port          = 110
	DefaultPOP3SPort         = 995
	DefaultIMAPPort          = 143
	DefaultIMAPSPort         = 993
	DefaultFolderName        = "INBOX"
	DefaultContentType       = "text/plain"
	DefaultConnectionTimeout = 30000 // ms
	DefaultPollDelay         = 60000 // ms
	DefaultFetchSize         = -1    // unlimited
)

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

// MailComponent implements the Mail component for GoCamel.
// Supports SMTP/SMTPS (sending) and POP3/POP3S/IMAP/IMAPS (receiving).
type MailComponent struct {
	mu sync.RWMutex
	// shared configuration
	defaultFrom       string
	defaultSubject    string
	connectionTimeout time.Duration
	debugMode         bool
}

// NewMailComponent creates a MailComponent with default values.
func NewMailComponent() *MailComponent {
	return &MailComponent{
		defaultFrom:       "gocamel@localhost",
		defaultSubject:    "",
		connectionTimeout: DefaultConnectionTimeout * time.Millisecond,
		debugMode:         false,
	}
}

// SetDefaultFrom sets the default sender address.
func (c *MailComponent) SetDefaultFrom(from string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.defaultFrom = from
}

// SetDefaultSubject sets the default email subject.
func (c *MailComponent) SetDefaultSubject(subject string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	c.defaultSubject = subject
}

// CreateEndpoint creates a MailEndpoint from a URI.
//
// Supported formats:
//
//	smtp://host:port?to=dest@example.com&from=src@example.com
//	smtps://host:port?username=user&password=pass
//	imap://host:port?folderName=INBOX&username=user&password=pass
//	pop3://host:port?delete=true&unseen=true
//
// Common options:
//
//	username, password : Authentication credentials
//	from, to, cc, bcc  : Email addresses (producer)
//	subject            : Message subject (producer)
//	contentType        : text/plain or text/html (producer)
//	folderName         : Folder to check (consumer, default: INBOX)
//	delete             : Delete messages after processing (consumer)
//	unseen             : Only process unread messages (consumer, default: true)
//	moveTo             : Move messages to this folder after processing
//	copyTo             : Copy messages to this folder after processing
//	connectionTimeout  : Connection timeout in ms (default: 30000)
//	pollDelay          : Delay between polls for consumers (ms, default: 60000)
//	disconnect         : Disconnect after each poll (consumer)
//	peek               : Mark messages as SEEN only after successful processing
//	fetchSize          : Max messages to retrieve per poll (default: -1 = unlimited)
//	skipFailedMessage  : Skip messages that fail (consumer)
//	handleFailedMessage: Route errors through the processor (consumer)
func (c *MailComponent) CreateEndpoint(uri string) (Endpoint, error) {
	u, err := ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("URI mail invalid: %w", err)
	}

	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case ProtocolSMTP, ProtocolSMTPS, ProtocolPOP3, ProtocolPOP3S, ProtocolIMAP, ProtocolIMAPS:
		// OK
	default:
		return nil, fmt.Errorf("Unsupported mail protocol: %s", scheme)
	}

	// Host and port
	host := u.Hostname()
	if host == "" {
		host = u.Host
	}
	if host == "" {
		return nil, fmt.Errorf("host required in mail URI: %s", uri)
	}

	port := u.Port()
	if port == "" {
		port = getDefaultPort(scheme)
	}

	portNum, err := strconv.Atoi(port)
	if err != nil || portNum <= 0 || portNum > 65535 {
		return nil, fmt.Errorf("port invalid in l'URI mail: %s", port)
	}

	// Query parameters
	q := u.Query()

	return &MailEndpoint{
		uri:                 uri,
		scheme:              scheme,
		host:                host,
		port:                portNum,
		username:            q.Get("username"),
		password:            q.Get("password"),
		from:                getFirstNotEmpty(q.Get("from"), c.defaultFrom),
		to:                  q.Get("to"),
		cc:                  q.Get("cc"),
		bcc:                 q.Get("bcc"),
		subject:             getFirstNotEmpty(q.Get("subject"), c.defaultSubject),
		contentType:         getFirstNotEmpty(q.Get("contentType"), DefaultContentType),
		folderName:          getFirstNotEmpty(q.Get("folderName"), DefaultFolderName),
		delete:              q.Get("delete") == "true",
		unseen:              q.Get("unseen") != "false", // defaut: true
		moveTo:              q.Get("moveTo"),
		copyTo:              q.Get("copyTo"),
		disconnect:          q.Get("disconnect") == "true",
		peek:                q.Get("peek") != "false",        // defaut: true
		closeFolder:         q.Get("closeFolder") != "false", // defaut: true
		useIdle:             q.Get("idle") == "true",
		connectionTimeout:   parseDurationMs(q.Get("connectionTimeout"), c.connectionTimeout),
		pollDelay:           parseDurationMs(q.Get("pollDelay"), DefaultPollDelay),
		fetchSize:           parseInt(q.Get("fetchSize"), DefaultFetchSize),
		skipFailedMessage:   q.Get("skipFailedMessage") == "true",
		handleFailedMessage: q.Get("handleFailedMessage") == "true",
		debugMode:           q.Get("debugMode") == "true",
		component:           c,
	}, nil
}

// ---------------------------------------------------------------------------
// Helper functions
// ---------------------------------------------------------------------------

func getDefaultPort(scheme string) string {
	switch scheme {
	case ProtocolSMTP:
		return strconv.Itoa(DefaultSMTPPort)
	case ProtocolSMTPS:
		return strconv.Itoa(DefaultSMTPSPort)
	case ProtocolPOP3:
		return strconv.Itoa(DefaultPOP3Port)
	case ProtocolPOP3S:
		return strconv.Itoa(DefaultPOP3SPort)
	case ProtocolIMAP:
		return strconv.Itoa(DefaultIMAPPort)
	case ProtocolIMAPS:
		return strconv.Itoa(DefaultIMAPSPort)
	default:
		return strconv.Itoa(DefaultSMTPPort)
	}
}

func getFirstNotEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func parseInt(s string, defaultVal int) int {
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return defaultVal
	}
	return v
}

func parseDurationMs(s string, defaultVal time.Duration) time.Duration {
	if s == "" {
		return defaultVal
	}
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return defaultVal
	}
	return time.Duration(ms) * time.Millisecond
}

// ---------------------------------------------------------------------------
// Endpoint
// ---------------------------------------------------------------------------

// MailEndpoint represents a configured mail endpoint.
type MailEndpoint struct {
	uri                 string
	scheme              string
	host                string
	port                int
	username            string
	password            string
	from                string
	to                  string
	cc                  string
	bcc                 string
	subject             string
	contentType         string
	folderName          string
	delete              bool
	unseen              bool
	moveTo              string
	copyTo              string
	disconnect          bool
	peek                bool
	closeFolder         bool
	useIdle             bool
	connectionTimeout   time.Duration
	pollDelay           time.Duration
	fetchSize           int
	skipFailedMessage   bool
	handleFailedMessage bool
	debugMode           bool
	component           *MailComponent
}

func (e *MailEndpoint) URI() string { return e.uri }

// isProducerProtocol returns true if the protocol is for sending (SMTP/SMTPS).
func (e *MailEndpoint) isProducerProtocol() bool {
	return e.scheme == ProtocolSMTP || e.scheme == ProtocolSMTPS
}

// isConsumerProtocol returns true if the protocol is for receiving (POP3/IMAP).
func (e *MailEndpoint) isConsumerProtocol() bool {
	return e.scheme == ProtocolPOP3 || e.scheme == ProtocolPOP3S ||
		e.scheme == ProtocolIMAP || e.scheme == ProtocolIMAPS
}

// isIMAP returns true if the protocol is IMAP/IMAPS.
func (e *MailEndpoint) isIMAP() bool {
	return e.scheme == ProtocolIMAP || e.scheme == ProtocolIMAPS
}

// CreateProducer creates a mail producer if the protocol supports it.
func (e *MailEndpoint) CreateProducer() (Producer, error) {
	if !e.isProducerProtocol() {
		return nil, fmt.Errorf("protocol %s does not support producing", e.scheme)
	}
	return &MailProducer{
		endpoint: e,
	}, nil
}

// CreateConsumer creates a mail consumer if the protocol supports it.
func (e *MailEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	if !e.isConsumerProtocol() {
		return nil, fmt.Errorf("protocol %s does not support consuming", e.scheme)
	}
	// POP3 uses a dedicated consumer
	if e.scheme == ProtocolPOP3 || e.scheme == ProtocolPOP3S {
		return &Pop3Consumer{
			endpoint:  e,
			processor: processor,
			stopChan:  make(chan struct{}),
		}, nil
	}
	// IMAP uses MailConsumer with IDLE support
	return &MailConsumer{
		endpoint:  e,
		processor: processor,
		stopChan:  make(chan struct{}),
	}, nil
}

// address returns the full host:port address.
func (e *MailEndpoint) address() string {
	return fmt.Sprintf("%s:%d", e.host, e.port)
}

// isSecure returns true if the protocol uses SSL/TLS.
func (e *MailEndpoint) isSecure() bool {
	return e.scheme == ProtocolSMTPS || e.scheme == ProtocolPOP3S || e.scheme == ProtocolIMAPS
}

// ---------------------------------------------------------------------------
// Producer
// ---------------------------------------------------------------------------

// MailProducer implements email sending via SMTP/SMTPS.
type MailProducer struct {
	endpoint *MailEndpoint
}

// Start starts the mail producer.
func (p *MailProducer) Start(ctx context.Context) error {
	return nil
}

// Stop stops the mail producer.
func (p *MailProducer) Stop() error {
	return nil
}

// Send sends email via SMTP/SMTPS.
func (p *MailProducer) Send(exchange *Exchange) error {
	ep := p.endpoint

	// Build headers
	from := getMailHeader(exchange, "From", ep.from)
	to := getMailHeader(exchange, "To", ep.to)
	cc := getMailHeader(exchange, "Cc", ep.cc)
	bcc := getMailHeader(exchange, "Bcc", ep.bcc)
	subject := getMailHeader(exchange, "Subject", ep.subject)
	contentType := getMailHeader(exchange, "Content-Type", ep.contentType)

	// Validate required addresses
	if from == "" {
		return errors.New("sender (from) is required")
	}
	if to == "" && cc == "" && bcc == "" {
		return errors.New("at least one recipient (to, cc, bcc) is required")
	}

	// Extract recipients
	recipients := parseAddresses(to)
	recipients = append(recipients, parseAddresses(cc)...)
	recipients = append(recipients, parseAddresses(bcc)...)

	if len(recipients) == 0 {
		return errors.New("no valid recipient")
	}

	// Message body
	body := extractBody(exchange)

	// Build MIME message
	message, err := p.buildMessage(from, to, cc, subject, contentType, body, exchange)
	if err != nil {
		return fmt.Errorf("error building message: %w", err)
	}

	// Connect and send
	return p.sendMail(from, recipients, message)
}

// buildMessage builds a MIME message.
func (p *MailProducer) buildMessage(from, to, cc, subject, contentType string, body []byte, exchange *Exchange) ([]byte, error) {
	var buf bytes.Buffer

	// Required headers
	fmt.Fprintf(&buf, "From: %s\r\n", from)
	if to != "" {
		fmt.Fprintf(&buf, "To: %s\r\n", to)
	}
	if cc != "" {
		fmt.Fprintf(&buf, "Cc: %s\r\n", cc)
	}
	fmt.Fprintf(&buf, "Subject: %s\r\n", subject)
	fmt.Fprintf(&buf, "Date: %s\r\n", time.Now().Format(time.RFC1123))
	fmt.Fprintf(&buf, "Message-Id: <%s@%s>\r\n", generateMessageID(), p.endpoint.host)
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")

	// Handle attachments
	attachments := extractAttachments(exchange)

	if len(attachments) > 0 {
		// Multipart with attachments
		boundary := generateBoundary()
		fmt.Fprintf(&buf, "Content-Type: multipart/mixed; boundary=\"%s\"\r\n\r\n", boundary)

		// Text part
		fmt.Fprintf(&buf, "--%s\r\n", boundary)
		fmt.Fprintf(&buf, "Content-Type: %s; charset=UTF-8\r\n", contentType)
		fmt.Fprintf(&buf, "Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		buf.Write(body)
		buf.WriteString("\r\n")

		// Attachments
		for name, data := range attachments {
			fmt.Fprintf(&buf, "--%s\r\n", boundary)
			fmt.Fprintf(&buf, "Content-Type: application/octet-stream\r\n")
			fmt.Fprintf(&buf, "Content-Disposition: attachment; filename=\"%s\"\r\n", name)
			fmt.Fprintf(&buf, "Content-Transfer-Encoding: base64\r\n\r\n")

			encoded := base64.StdEncoding.EncodeToString(data)
			for i := 0; i < len(encoded); i += 76 {
				end := i + 76
				if end > len(encoded) {
					end = len(encoded)
				}
				buf.WriteString(encoded[i:end])
				buf.WriteString("\r\n")
			}
		}

		// End of message
		fmt.Fprintf(&buf, "--%s--\r\n", boundary)
	} else {
		// Simple message without attachments
		fmt.Fprintf(&buf, "Content-Type: %s; charset=UTF-8\r\n", contentType)
		fmt.Fprintf(&buf, "Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		buf.Write(body)
	}

	return buf.Bytes(), nil
}

// sendMail sends the message via SMTP/SMTPS with retry and exponential backoff.
func (p *MailProducer) sendMail(from string, recipients []string, message []byte) error {
	ep := p.endpoint
	addr := ep.address()

	var auth smtp.Auth
	if ep.username != "" && ep.password != "" {
		auth = smtp.PlainAuth("", ep.username, ep.password, ep.host)
	}

	// Configure retry with exponential backoff
	maxRetries := 5
	baseDelay := time.Second
	maxDelay := 8 * time.Second

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 1s, 2s, 4s, 8s
			delay := baseDelay * time.Duration(1<<uint(attempt-1))
			if delay > maxDelay {
				delay = maxDelay
			}
			if ep.debugMode {
				logInfo("Mail retry", "attempt", attempt+1, "max", maxRetries, "error", lastErr, "delay", delay)
			}
			time.Sleep(delay)
		}

		err := p.trySendMail(addr, auth, from, recipients, message)
		if err == nil {
			// Success
			if attempt > 0 && ep.debugMode {
				logInfo("Mail send succeeded after retries", "attempts", attempt+1)
			}
			return nil
		}

		lastErr = err
		if ep.debugMode {
			logError("Mail send attempt failed", "attempt", attempt+1, "max", maxRetries, "error", err)
		}

		// Do not retry certain errors (authentication, invalid address, etc.)
		if isNonRetryableError(err) {
			return fmt.Errorf("non-recoverable error: %w", err)
		}
	}

	return fmt.Errorf("failed after %d attempts: %w", maxRetries, lastErr)
}

// trySendMail performs a single send attempt.
func (p *MailProducer) trySendMail(addr string, auth smtp.Auth, from string, recipients []string, message []byte) error {
	ep := p.endpoint

	if ep.scheme == ProtocolSMTPS {
		// Native TLS connection (port 465)
		tlsConfig := &tls.Config{
			ServerName: ep.host,
		}
		conn, err := tls.Dial("tcp", addr, tlsConfig)
		if err != nil {
			return fmt.Errorf("TLS connection error: %w", err)
		}
		defer conn.Close()

		client, err := smtp.NewClient(conn, ep.host)
		if err != nil {
			return fmt.Errorf("SMTP client creation error: %w", err)
		}
		defer client.Close()

		if auth != nil {
			if err := client.Auth(auth); err != nil {
				return fmt.Errorf("authentication error: %w", err)
			}
		}

		if err := client.Mail(from); err != nil {
			return fmt.Errorf("MAIL FROM error: %w", err)
		}

		for _, rcpt := range recipients {
			if err := client.Rcpt(rcpt); err != nil {
				return fmt.Errorf("RCPT TO %s error: %w", rcpt, err)
			}
		}

		w, err := client.Data()
		if err != nil {
			return fmt.Errorf("DATA error: %w", err)
		}

		if _, err := w.Write(message); err != nil {
			return fmt.Errorf("message write error: %w", err)
		}

		if err := w.Close(); err != nil {
			return fmt.Errorf("DATA close error: %w", err)
		}

		return client.Quit()
	}

	// Standard SMTP with STARTTLS if available
	return smtp.SendMail(addr, auth, from, recipients, message)
}

// isNonRetryableError determines whether an error should not be retried.
func isNonRetryableError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	// Authentication errors
	if strings.Contains(errStr, "auth") || strings.Contains(errStr, "credential") {
		return true
	}
	// Invalid address errors
	if strings.Contains(errStr, "invalid") || strings.Contains(errStr, "bad address") {
		return true
	}
	// Syntax errors
	if strings.Contains(errStr, "syntax") {
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Full IMAP consumer using go-imap v2
// ---------------------------------------------------------------------------

// MailConsumer implements email reception via IMAP (using go-imap v2).
type MailConsumer struct {
	endpoint  *MailEndpoint
	processor Processor
	stopChan  chan struct{}
	stopOnce  sync.Once
	wg        sync.WaitGroup
	client    *imapclient.Client
	mu        sync.Mutex
}

// Start starts the mail consumer.
func (c *MailConsumer) Start(ctx context.Context) error {
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
				logError("Mail IDLE failed, fallback to polling", "error", err)
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

	if ep.debugMode {
		logInfo("Mail IMAP IDLE started", "address", ep.address(), "folder", ep.folderName)
	}

	// Connect to server
	ctx, cancel := context.WithTimeout(parentCtx, ep.connectionTimeout)
	defer cancel()

	if err := c.connect(ctx); err != nil {
		return fmt.Errorf("connection error for IDLE: %w", err)
	}

	// Select folder
	if _, err := c.selectFolder(ep.folderName); err != nil {
		c.disconnect()
		return fmt.Errorf("folder selection error for IDLE: %w", err)
	}

	for {
		c.mu.Lock()
		client := c.client
		c.mu.Unlock()

		if client == nil {
			c.disconnect()
			return errors.New("client disconnected during IDLE")
		}

		// Start IDLE
		idleCmd, err := client.Idle()
		if err != nil {
			c.disconnect()
			return fmt.Errorf("IDLE start error: %w", err)
		}

		if ep.debugMode {
			logDebug("Mail IDLE active, waiting for notifications")
		}

		// Wait for either a notification or a stop signal
		idleDone := make(chan error, 1)
		go func() {
			// Wait blocks until a notification arrives or IDLE ends
			err := idleCmd.Wait()
			idleDone <- err
		}()

		select {
		case <-c.stopChan:
			// Stop requested
			idleCmd.Close()

			return nil

		case <-parentCtx.Done():
			// Context cancelled
			idleCmd.Close()

			return nil

		case err := <-idleDone:
			// IDLE ended (notification or error)
			idleCmd.Close()

			if err != nil {
				if ep.debugMode {
					logError("Mail IDLE terminated with error", "error", err)
				}
				c.disconnect()
				return err
			}

			// Notification received — process new messages
			if ep.debugMode {
				logDebug("Mail IDLE detected change, processing")
			}

			if processErr := c.processNewMessages(parentCtx); processErr != nil {
				if !ep.skipFailedMessage {
					logError("Mail error processing messages", "error", processErr)
				}
			}

			// Small delay to avoid tight loop
			time.Sleep(100 * time.Millisecond)
			// Continue the loop to restart IDLE
		}
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
		logInfo("Mail messages found via IDLE", "count", len(uids))
	}

	// Apply fetchSize limit
	if ep.fetchSize > 0 && len(uids) > ep.fetchSize {
		uids = uids[:ep.fetchSize]
	}

	selected, err := c.selectFolder(ep.folderName)
	if err != nil {
		return fmt.Errorf("folder selection error: %w", err)
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

		if err := c.processMessage(parentCtx, uid, selected); err != nil {
			if ep.handleFailedMessage {
				if err2 := c.handleFailedMessage(parentCtx, uid, err); err2 != nil {
					logError("Mail error in error handler", "error", err2)
				}
			} else if !ep.skipFailedMessage {
				logError("Mail error processing message", "uid", uid, "error", err)
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
		logInfo("Mail polling", "address", ep.address(), "folder", ep.folderName)
	}

	// Connect to IMAP server
	if err := c.connect(ctx); err != nil {
		if !ep.skipFailedMessage {
			logError("Mail connection error", "error", err)
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
			logError("Mail folder selection error", "folder", ep.folderName, "error", err)
		}
		return
	}

	// Search for unread messages if unseen option is set
	var uids []imap.UID
	if ep.unseen {
		uids, err = c.searchUnseen()
		if err != nil {
			if ep.debugMode {
				logError("Mail search error", "error", err)
			}
			// Fallback: use Fetch with range
			uids = nil
		}
	} else {
		// Otherwise search all messages
		uids, err = c.searchAll()
		if err != nil {
			if ep.debugMode {
				logError("Mail search all error", "error", err)
			}
			uids = nil
		}
	}

	// If no results, return
	if len(uids) == 0 {
		if ep.debugMode {
			logDebug("Mail no new messages")
		}
		return
	}

	if ep.debugMode {
		logInfo("Mail messages found", "count", len(uids))
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
					logError("Mail error in error handler", "error", err2)
				}
			} else if !ep.skipFailedMessage {
				logError("Mail error processing message", "uid", uid, "error", err)
			}
		}
	}

	if ep.debugMode {
		logDebug("Mail poll finished")
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
		// Reconnection needed
		c.client = nil
	}

	ep := c.endpoint

	// Connection options — TLS config
	tlsConfig := &tls.Config{
		ServerName: ep.host,
	}

	var client *imapclient.Client
	var err error

	if ep.isSecure() {
		// IMAPS native TLS (port 993)
		client, err = imapclient.DialTLS(ep.address(), &imapclient.Options{
			TLSConfig: tlsConfig,
		})
	} else {
		// IMAP with optional STARTTLS (port 143)
		client, err = imapclient.DialStartTLS(ep.address(), &imapclient.Options{
			TLSConfig: tlsConfig,
		})
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

	// Fetch message with all required info
	fetchOptions := &imap.FetchOptions{
		UID:         true,
		Flags:       true,
		Envelope:    true,
		BodySection: []*imap.FetchItemBodySection{{}}, // full body
	}

	msg, err := c.fetchMessage(uid, fetchOptions)
	if err != nil {
		return fmt.Errorf("message retrieval error: %w", err)
	}

	// Parse the message
	mailMsg, err := c.parseMessage(msg)
	if err != nil {
		return fmt.Errorf("message parsing error: %w", err)
	}

	// Create exchange
	exchange := NewExchange(ctx)
	c.populateExchange(exchange, mailMsg)

	// Mark as seen if peek=false
	if !ep.peek {
		if err := c.markSeen(uid); err != nil {
			logError("Mail SEEN marking error", "error", err)
		}
	}

	// Process via processor
	if err := c.processor.Process(exchange); err != nil {
		// Processing failed — rollback if peek=true
		if ep.peek {
			if err2 := c.markUnseen(uid); err2 != nil {
				logError("Mail SEEN rollback error", "error", err2)
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
			mailMsg.Body, mailMsg.BodyHTML, mailMsg.Attachments = c.parseMimeMessage(bodyData)
			if mailMsg.Body == nil {
				// Fallback: raw content
				mailMsg.Body = bodyData
			}
		}
	}

	return mailMsg, nil
}

// parseMimeMessageResult holds the result of MIME parsing.
type parseMimeMessageResult struct {
	bodyText    []byte
	bodyHTML    []byte
	attachments map[string][]byte
}

// parseMimeMessage parses a MIME message and extracts body and attachments.
// Improved version handling multipart/alternative and various encodings.
// Delegates to the common function shared with POP3.
func (c *MailConsumer) parseMimeMessage(data []byte) ([]byte, []byte, map[string][]byte) {
	return parseMimeMessageCommon(data)
}

// parseEntity recursively parses a MIME entity.
func (c *MailConsumer) parseEntity(entity *mail.Reader, result *parseMimeMessageResult, parentContentType string) {
	// Message headers
	header := entity.Header
	contentType, _, _ := header.ContentType()

	switch {
	case strings.HasPrefix(contentType, "multipart/alternative"):
		// Choose the best available part (HTML preferred over plain text)
		c.parseMultipartAlternative(entity, result)

	case strings.HasPrefix(contentType, "multipart/"): // multipart/mixed, multipart/related, etc.
		// Scan all parts
		for {
			part, err := entity.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				break
			}
			// Process the part directly
			c.handlePart(part, result)
		}

	case strings.HasPrefix(contentType, "text/"):
		// Plain text content
		c.handleTextEntity(entity, result, contentType)

	default:
		// Other content (binary, attachment, etc.)
		c.handleAttachmentEntity(entity, result)
	}
}

// parseMultipartAlternative handles multipart/alternative by choosing the best format.
func (c *MailConsumer) parseMultipartAlternative(entity *mail.Reader, result *parseMimeMessageResult) {
	var textPart []byte
	var htmlPart []byte

	for {
		part, err := entity.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}

		// Use Get to obtain the Content-Type from PartHeader
		contentType := part.Header.Get("Content-Type")
		partData, err := c.decodePart(part)
		if err != nil {
			continue
		}

		switch {
		case strings.HasPrefix(contentType, "text/plain"):
			textPart = partData
		case strings.HasPrefix(contentType, "text/html"):
			htmlPart = partData
		default:
			// Other alternative — keep it if we have nothing better
			if textPart == nil {
				textPart = partData
			}
		}
	}

	result.bodyText = textPart
	result.bodyHTML = htmlPart
}

// handlePart processes a multipart part (simplified).
func (c *MailConsumer) handlePart(part *mail.Part, result *parseMimeMessageResult) {
	// PartHeader does not have a ContentType() method — use Get()
	contentType := part.Header.Get("Content-Type")
	contentDisp := part.Header.Get("Content-Disposition")

	data, err := c.decodePart(part)
	if err != nil {
		return
	}

	// Check if it is an attachment via Content-Disposition
	isAttachment := strings.Contains(contentDisp, "attachment")

	// If it is text and not an attachment
	if strings.HasPrefix(contentType, "text/") && !isAttachment {
		if strings.HasPrefix(contentType, "text/html") {
			result.bodyHTML = data
		} else {
			result.bodyText = data
		}
	} else {
		// Attachment or other content
		filename := extractFilenameFromDispositionCommon(contentDisp, contentType)
		if filename == "" {
			filename = "attachment.bin"
		}
		if len(data) > 0 {
			result.attachments[filename] = data
		}
	}
}

// handleSimplePart processes a simple non-MIME part.
func (c *MailConsumer) handleSimplePart(part *mail.Part, result *parseMimeMessageResult) {
	// PartHeader does not have a ContentType() method — use Get()
	contentType := part.Header.Get("Content-Type")
	data, err := c.decodePart(part)
	if err != nil {
		return
	}

	if strings.HasPrefix(contentType, "text/") {
		if result.bodyText == nil {
			result.bodyText = data
		}
	} else {
		// Likely an attachment — extract filename from PartHeader manually
		contentDisp := part.Header.Get("Content-Disposition")
		filename := extractFilenameFromDispositionCommon(contentDisp, part.Header.Get("Content-Type"))
		if filename == "" {
			filename = "attachment.bin"
		}
		if len(data) > 0 {
			result.attachments[filename] = data
		}
	}
}

// handleTextEntity processes a text entity.
func (c *MailConsumer) handleTextEntity(entity *mail.Reader, result *parseMimeMessageResult, contentType string) {
	// Read body from entity — entity.Reader is not an io.Reader;
	// NextPart must be used to read parts.
	part, err := entity.NextPart()
	if err != nil {
		return
	}
	data, err := c.decodePart(part)
	if err != nil {
		return
	}

	if strings.HasPrefix(contentType, "text/html") {
		result.bodyHTML = data
	} else {
		result.bodyText = data
	}
}

// handleAttachmentEntity processes an entity as an attachment.
func (c *MailConsumer) handleAttachmentEntity(entity *mail.Reader, result *parseMimeMessageResult) {
	filename := extractFilenameCommon(entity.Header)
	if filename == "" {
		filename = "attachment.bin"
	}
	part, err := entity.NextPart()
	if err != nil {
		return
	}
	data, err := c.decodePart(part)
	if err != nil {
		return
	}

	if len(data) > 0 {
		result.attachments[filename] = data
	}
}

// decodePart decodes a part (base64, quoted-printable).
func (c *MailConsumer) decodePart(part *mail.Part) ([]byte, error) {
	// go-message already handles decoding via the part body
	return io.ReadAll(part.Body)
}

// populateExchange populates the exchange with message data.
func (c *MailConsumer) populateExchange(exchange *Exchange, msg *MailMessage) {
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
			exchange.SetHeader(k, v)
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
func (c *MailConsumer) postProcess(uid imap.UID, exchange *Exchange) error {
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
	exchange := NewExchange(ctx)
	exchange.SetHeader("CamelMailError", msgErr.Error())
	exchange.SetHeader("CamelMailFailedUID", strconv.FormatUint(uint64(uid), 10))

	return c.processor.Process(exchange)
}

// getStringHeader retrieves a string value from a header.
func (c *MailConsumer) getStringHeader(exchange *Exchange, header string) string {
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
func (c *MailConsumer) getBoolHeader(exchange *Exchange, header string) bool {
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

// ---------------------------------------------------------------------------
// Data types
// ---------------------------------------------------------------------------

// MailMessage represents a received email message.
type MailMessage struct {
	MessageID   string
	From        string
	ReplyTo     string
	To          []string
	Cc          []string
	Bcc         []string
	Subject     string
	Date        time.Time
	Body        []byte
	BodyHTML    []byte
	ContentType string
	Headers     map[string]string
	Attachments map[string][]byte
	Seen        bool
	Deleted     bool
	UID         uint32
}

// ---------------------------------------------------------------------------
// Helper functions for the producer
// ---------------------------------------------------------------------------

func getMailHeader(exchange *Exchange, header, defaultVal string) string {
	if v, exists := exchange.GetIn().GetHeader(header); exists {
		switch s := v.(type) {
		case string:
			if s != "" {
				return s
			}
		}
	}
	return defaultVal
}

func parseAddresses(s string) []string {
	if s == "" {
		return nil
	}
	var addrs []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			addrs = append(addrs, part)
		}
	}
	return addrs
}

func extractBody(exchange *Exchange) []byte {
	switch v := exchange.GetIn().GetBody().(type) {
	case []byte:
		return v
	case string:
		return []byte(v)
	default:
		if v != nil {
			return []byte(fmt.Sprintf("%v", v))
		}
		return nil
	}
}

func extractAttachments(exchange *Exchange) map[string][]byte {
	attachments := make(map[string][]byte)

	for key, value := range exchange.GetIn().GetHeaders() {
		if strings.HasPrefix(key, MailAttachmentPrefix+"_") {
			name := strings.TrimPrefix(key, MailAttachmentPrefix+"_")
			switch data := value.(type) {
			case []byte:
				attachments[name] = data
			case string:
				attachments[name] = []byte(data)
			}
		}
	}

	return attachments
}

func generateMessageID() string {
	return fmt.Sprintf("%d.%d.%d", time.Now().UnixNano(), time.Now().Unix(), time.Now().Nanosecond())
}

func generateBoundary() string {
	return fmt.Sprintf("----=_Part_%d_%d", time.Now().UnixNano(), time.Now().Unix())
}

// ---------------------------------------------------------------------------
// Consumer POP3
// ---------------------------------------------------------------------------

// Pop3Consumer implements email reception via POP3/POP3S.
type Pop3Consumer struct {
	endpoint  *MailEndpoint
	processor Processor
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

// pop3MessageID represents a message in the POP3 listing.
type pop3MessageID struct {
	ID   int
	Size int
	UID  string
}

// poll connects to the POP3 server and retrieves new messages.
func (c *Pop3Consumer) poll(parentCtx context.Context) {
	ep := c.endpoint

	// Context with timeout for this operation
	ctx, cancel := context.WithTimeout(parentCtx, ep.connectionTimeout)
	defer cancel()

	if ep.debugMode {
		logInfo("POP3 polling", "address", ep.address())
	}

	// Connect to POP3 server
	if err := c.connect(ctx); err != nil {
		if !ep.skipFailedMessage {
			logError("POP3 connection error", "error", err)
		}
		return
	}
	defer c.disconnect()

	// List messages
	count, size, err := c.client.Stat()
	if err != nil {
		if !ep.skipFailedMessage {
			logError("POP3 stat error", "error", err)
		}
		return
	}

	if count == 0 {
		if ep.debugMode {
			logDebug("POP3 no new messages")
		}
		return
	}

	if ep.debugMode {
		logInfo("POP3 messages found", "count", count, "size", size)
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
					logError("[POP3] error handler", "error", err2)
				}
			} else if !ep.skipFailedMessage {
				logError("POP3 message processing error", "msgID", msgID, "error", err)
			}
		}
	}

	if ep.debugMode {
		logDebug("POP3 poll finished")
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
	mailMsg.UID = uint32(msgID)

	// Create exchange
	exchange := NewExchange(ctx)
	c.populateExchange(exchange, mailMsg)

	// Process via processor
	if err := c.processor.Process(exchange); err != nil {
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
	// Read full body from msgData.Body
	bodyData, err := io.ReadAll(msgData.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading body: %w", err)
	}

	body, bodyHTML, attachments := c.parseMimeMessage(bodyData)
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

// parseMimeMessage parses a MIME message for POP3.
func (c *Pop3Consumer) parseMimeMessage(data []byte) ([]byte, []byte, map[string][]byte) {
	return parseMimeMessageCommon(data)
}

// populateExchange populates the exchange with POP3 message data.
func (c *Pop3Consumer) populateExchange(exchange *Exchange, msg *MailMessage) {
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
			exchange.SetHeader(k, v)
		}
	}

	// Attachments
	for name, data := range msg.Attachments {
		exchange.SetHeader(MailAttachmentPrefix+"_"+name, data)
	}
}

// postProcess applies post-processing actions (delete).
func (c *Pop3Consumer) postProcess(msgID int, exchange *Exchange) error {
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
	exchange := NewExchange(ctx)
	exchange.SetHeader("CamelMailError", msgErr.Error())
	exchange.SetHeader("CamelMailFailedMsgID", strconv.Itoa(msgID))

	return c.processor.Process(exchange)
}

// getBoolHeader retrieves a bool value from a header.
func (c *Pop3Consumer) getBoolHeader(exchange *Exchange, header string) bool {
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

// parseMimeMessageCommon is a shared utility function for MIME parsing.
func parseMimeMessageCommon(data []byte) ([]byte, []byte, map[string][]byte) {
	result := &parseMimeMessageResult{
		attachments: make(map[string][]byte),
	}

	// Try parsing as a mail entity
	entity, err := mail.CreateReader(bytes.NewReader(data))
	if err != nil {
		// Not valid MIME — return as plain text
		return data, nil, result.attachments
	}

	// Recursively analyse parts
	parseEntityCommon(entity, result, "")

	return result.bodyText, result.bodyHTML, result.attachments
}

// parseEntityCommon is a utility function for parsing a MIME entity.
func parseEntityCommon(entity *mail.Reader, result *parseMimeMessageResult, parentContentType string) {
	// Message headers
	header := entity.Header
	contentType, _, _ := header.ContentType()

	switch {
	case strings.HasPrefix(contentType, "multipart/alternative"):
		// Choose the best available part (HTML preferred over plain text)
		parseMultipartAlternativeCommon(entity, result)

	case strings.HasPrefix(contentType, "multipart/"): // multipart/mixed, multipart/related, etc.
		// Scan all parts
		for {
			part, err := entity.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				break
			}
			// Process the part directly
			handlePartCommon(part, result)
		}

	case strings.HasPrefix(contentType, "text/"):
		// Plain text content
		handleTextEntityCommon(entity, result, contentType)

	default:
		// Other content (binary, attachment, etc.)
		handleAttachmentEntityCommon(entity, result)
	}
}

// parseMultipartAlternativeCommon handles multipart/alternative.
func parseMultipartAlternativeCommon(entity *mail.Reader, result *parseMimeMessageResult) {
	var textPart []byte
	var htmlPart []byte

	for {
		part, err := entity.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}

		// Use Get to obtain the Content-Type from PartHeader
		contentType := part.Header.Get("Content-Type")
		partData, err := decodePartCommon(part)
		if err != nil {
			continue
		}

		switch {
		case strings.HasPrefix(contentType, "text/plain"):
			textPart = partData
		case strings.HasPrefix(contentType, "text/html"):
			htmlPart = partData
		default:
			// Other alternative — keep it if we have nothing better
			if textPart == nil {
				textPart = partData
			}
		}
	}

	result.bodyText = textPart
	result.bodyHTML = htmlPart
}

// handlePartCommon processes a multipart part.
func handlePartCommon(part *mail.Part, result *parseMimeMessageResult) {
	// PartHeader does not have a ContentType() method — use Get()
	contentType := part.Header.Get("Content-Type")
	contentDisp := part.Header.Get("Content-Disposition")

	data, err := decodePartCommon(part)
	if err != nil {
		return
	}

	// Check if it is an attachment via Content-Disposition
	isAttachment := strings.Contains(contentDisp, "attachment")

	// If it is text and not an attachment
	if strings.HasPrefix(contentType, "text/") && !isAttachment {
		if strings.HasPrefix(contentType, "text/html") {
			result.bodyHTML = data
		} else {
			result.bodyText = data
		}
	} else {
		// Attachment or other content
		filename := extractFilenameFromDispositionCommon(contentDisp, contentType)
		if filename == "" {
			filename = "attachment.bin"
		}
		if len(data) > 0 {
			result.attachments[filename] = data
		}
	}
}

// handleTextEntityCommon processes a text entity.
func handleTextEntityCommon(entity *mail.Reader, result *parseMimeMessageResult, contentType string) {
	part, err := entity.NextPart()
	if err != nil {
		return
	}
	data, err := decodePartCommon(part)
	if err != nil {
		return
	}

	if strings.HasPrefix(contentType, "text/html") {
		result.bodyHTML = data
	} else {
		result.bodyText = data
	}
}

// handleAttachmentEntityCommon processes an entity as an attachment.
func handleAttachmentEntityCommon(entity *mail.Reader, result *parseMimeMessageResult) {
	filename := extractFilenameCommon(entity.Header)
	if filename == "" {
		filename = "attachment.bin"
	}
	part, err := entity.NextPart()
	if err != nil {
		return
	}
	data, err := decodePartCommon(part)
	if err != nil {
		return
	}

	if len(data) > 0 {
		result.attachments[filename] = data
	}
}

// decodePartCommon decodes a part.
func decodePartCommon(part *mail.Part) ([]byte, error) {
	return io.ReadAll(part.Body)
}

// extractFilenameFromDispositionCommon extracts the filename from Content-Disposition and Content-Type.
func extractFilenameFromDispositionCommon(contentDisposition, contentType string) string {
	if contentDisposition != "" {
		if strings.Contains(contentDisposition, "filename=") {
			parts := strings.Split(contentDisposition, "filename=")
			if len(parts) > 1 {
				filename := strings.Trim(parts[1], ` "'`)
				if idx := strings.Index(filename, ";"); idx > 0 {
					filename = filename[:idx]
					filename = strings.Trim(filename, ` "'`)
				}
				return filename
			}
		}
	}
	// Try Content-Type name parameter
	if strings.Contains(contentType, "name=") {
		parts := strings.Split(contentType, "name=")
		if len(parts) > 1 {
			return strings.Trim(parts[1], ` "'`)
		}
	}
	return ""
}

// extractFilenameCommon extracts the filename from headers.
func extractFilenameCommon(header mail.Header) string {
	// Content-Disposition
	disp, _, _ := header.ContentDisposition()
	if disp != "" {
		if strings.Contains(disp, "filename=") {
			parts := strings.Split(disp, "filename=")
			if len(parts) > 1 {
				filename := strings.Trim(parts[1], ` "'`)
				// Remove extra parameters
				if idx := strings.Index(filename, ";"); idx > 0 {
					filename = filename[:idx]
					filename = strings.Trim(filename, ` "'`)
				}
				return filename
			}
		}
	}

	// Content-Type name parameter
	_, params, _ := header.ContentType()
	if name, ok := params["name"]; ok && name != "" {
		return name
	}

	return ""
}
