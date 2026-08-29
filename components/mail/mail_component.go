package mail

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	gocamel "gitlab.com/tranchida/gocamel"
)

// The Mail component is split across multiple files in this package:
//   mail_component.go        — component, constants, URI parsing helpers
//   mail_endpoint.go         — MailEndpoint
//   mail_producer.go         — SMTP / SMTPS sending
//   mail_consumer_imap.go    — IMAP / IMAPS reception (poll + IDLE)
//   mail_consumer_pop3.go    — POP3 / POP3S reception
//   mail_message.go          — MailMessage data type
//   mail_mime.go             — shared MIME parsing helpers

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

	// MailHeaderPrefix prefixes raw envelope headers of a received email so
	// that they cannot collide with (or spoof) the well-known producer-side
	// headers such as "From" or "To" (SECURITY).
	MailHeaderPrefix = "CamelMailHeader_"

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
	c.mu.Lock()
	defer c.mu.Unlock()
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
//	allowHeaderOverride: Opt-in: From/To/Cc/Bcc/Subject/Content-Type exchange
//	                     headers may override the URI configuration (producer,
//	                     default: false)
//	requireStartTLS    : Refuse to send when the SMTP server does not advertise
//	                     STARTTLS (producer, default: false; recommended: true)
func (c *MailComponent) CreateEndpoint(uri string) (gocamel.Endpoint, error) {
	u, err := gocamel.ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("URI mail invalid: %w", err)
	}

	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case ProtocolSMTP, ProtocolSMTPS, ProtocolPOP3, ProtocolPOP3S, ProtocolIMAP, ProtocolIMAPS:
		// OK
	default:
		return nil, fmt.Errorf("unsupported mail protocol: %s", scheme)
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
		return nil, fmt.Errorf("invalid port in mail URI: %s", port)
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
		maxMessageSize:      gocamel.ParseInt64Option(q.Get("maxMessageSize")),
		allowHeaderOverride: parseBool(q.Get("allowHeaderOverride"), false),
		requireStartTLS:     parseBool(q.Get("requireStartTLS"), false),
		component:           c,
	}, nil
}

// ---------------------------------------------------------------------------
// URI helpers
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

func parseBool(s string, defaultVal bool) bool {
	if s == "" {
		return defaultVal
	}
	b, err := strconv.ParseBool(s)
	if err != nil {
		return defaultVal
	}
	return b
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
