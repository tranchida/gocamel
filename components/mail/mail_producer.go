package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	netmail "net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"

	gocamel "github.com/tranchida/gocamel"
)

// MailProducer implements email sending via SMTP/SMTPS.
type MailProducer struct {
	endpoint  *MailEndpoint
	tlsConfig *tls.Config // optional test override; nil uses default TLS config
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
func (p *MailProducer) Send(exchange *gocamel.Exchange) error {
	ep := p.endpoint

	// Build headers.
	from, to, cc, bcc, subject, contentType := effectiveMailHeaders(ep, exchange)

	// Validate required addresses strictly before dialing to prevent ESMTP
	// envelope injection.
	if strings.TrimSpace(from) == "" {
		return errors.New("sender (from) is required")
	}
	envelopeFrom, err := parseSingleAddressStrict(from)
	if err != nil {
		return fmt.Errorf("invalid sender (from) address: %w", err)
	}

	if strings.TrimSpace(to) == "" && strings.TrimSpace(cc) == "" && strings.TrimSpace(bcc) == "" {
		return errors.New("at least one recipient (to, cc, bcc) is required")
	}

	var recipients []string
	for _, field := range []struct {
		name string
		val  string
	}{
		{"to", to},
		{"cc", cc},
		{"bcc", bcc},
	} {
		parsed, err := parseAddressListStrict(field.val)
		if err != nil {
			return fmt.Errorf("invalid %s address: %w", field.name, err)
		}
		recipients = append(recipients, parsed...)
	}

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
	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}
	return p.sendMail(ctx, envelopeFrom, recipients, message)
}

// buildMessage builds a MIME message.
func (p *MailProducer) buildMessage(from, to, cc, subject, contentType string, body []byte, exchange *gocamel.Exchange) ([]byte, error) {
	var buf bytes.Buffer

	// Headers are written verbatim into the message text, so any CR or LF in a
	// header value would let an attacker inject arbitrary SMTP headers. Strip
	// them before writing (SECURITY).
	from = sanitizeHeaderValue(from)
	to = sanitizeHeaderValue(to)
	cc = sanitizeHeaderValue(cc)
	subject = sanitizeHeaderValue(subject)
	contentType = sanitizeHeaderValue(contentType)

	// Required headers
	fmt.Fprintf(&buf, "From: %s\r\n", from)
	if to != "" {
		fmt.Fprintf(&buf, "To: %s\r\n", to)
	}
	if cc != "" {
		fmt.Fprintf(&buf, "Cc: %s\r\n", cc)
	}
	fmt.Fprintf(&buf, "Subject: %s\r\n", subject)
	fmt.Fprintf(&buf, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&buf, "Message-Id: <%s@%s>\r\n", generateMessageID(), sanitizeHeaderValue(p.endpoint.host))
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")

	formattedContentType := formatContentTypeHeader(contentType)

	// Handle attachments
	attachments := extractAttachments(exchange)

	if len(attachments) > 0 {
		// Multipart with attachments
		boundary := generateBoundary()
		fmt.Fprintf(&buf, "Content-Type: multipart/mixed; boundary=\"%s\"\r\n\r\n", boundary)

		// Text part
		fmt.Fprintf(&buf, "--%s\r\n", boundary)
		fmt.Fprintf(&buf, "Content-Type: %s\r\n", formattedContentType)
		fmt.Fprintf(&buf, "Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		qp := quotedprintable.NewWriter(&buf)
		if _, err := qp.Write(body); err != nil {
			return nil, err
		}
		if err := qp.Close(); err != nil {
			return nil, err
		}
		buf.WriteString("\r\n")

		// Attachments
		for name, data := range attachments {
			safeName := sanitizeAttachmentFilename(name)
			if safeName == "" {
				safeName = "attachment.bin"
			}
			disp := mime.FormatMediaType("attachment", map[string]string{"filename": safeName})
			if disp == "" {
				disp = "attachment"
			}
			fmt.Fprintf(&buf, "--%s\r\n", boundary)
			fmt.Fprintf(&buf, "Content-Type: application/octet-stream\r\n")
			fmt.Fprintf(&buf, "Content-Disposition: %s\r\n", disp)
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
		fmt.Fprintf(&buf, "Content-Type: %s\r\n", formattedContentType)
		fmt.Fprintf(&buf, "Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		qp := quotedprintable.NewWriter(&buf)
		if _, err := qp.Write(body); err != nil {
			return nil, err
		}
		if err := qp.Close(); err != nil {
			return nil, err
		}
	}

	return buf.Bytes(), nil
}

// sendMail sends the message via SMTP/SMTPS with retry and exponential backoff.
// The backoff and the connection establishment are aborted when ctx ends.
func (p *MailProducer) sendMail(ctx context.Context, from string, recipients []string, message []byte) error {
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
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return fmt.Errorf("mail send cancelled after %d attempts: %w (last error: %v)", attempt, err, lastErr)
			}
			return fmt.Errorf("mail send cancelled: %w", err)
		}

		if attempt > 0 {
			// Exponential backoff: 1s, 2s, 4s, 8s
			delay := baseDelay * time.Duration(1<<(attempt-1))
			if delay > maxDelay {
				delay = maxDelay
			}
			// Cap the backoff by the connection timeout when it is
			// shorter, so misconfigured endpoints fail fast.
			if ep.connectionTimeout > 0 && delay > ep.connectionTimeout {
				delay = ep.connectionTimeout
			}
			if ep.debugMode {
				gocamel.LogInfo("Mail retry", "attempt", attempt+1, "max", maxRetries, "error", lastErr, "delay", delay)
			}
			if !sleepWithContext(ctx, delay) {
				return fmt.Errorf("mail send cancelled after %d attempts: %w (last error: %v)", attempt, ctx.Err(), lastErr)
			}
		}

		err := p.trySendMail(ctx, addr, auth, from, recipients, message)
		if err == nil {
			// Success
			if attempt > 0 && ep.debugMode {
				gocamel.LogInfo("Mail send succeeded after retries", "attempts", attempt+1)
			}
			return nil
		}

		lastErr = err
		if ep.debugMode {
			gocamel.LogError("Mail send attempt failed", "attempt", attempt+1, "max", maxRetries, "error", err)
		}

		// Do not retry permanent, context-cancellation, or invalid-address errors.
		if isNonRetryableError(err) || ctx.Err() != nil {
			return fmt.Errorf("non-recoverable error: %w", err)
		}
	}

	return fmt.Errorf("failed after %d attempts: %w", maxRetries, lastErr)
}

// trySendMail performs a single send attempt. Every network phase (connect,
// TLS handshake, greeting, STARTTLS, AUTH, MAIL/RCPT, DATA, QUIT) is bounded
// by the connection timeout and aborted when ctx ends.
//
// Over standard SMTP, STARTTLS is opportunistic by default: the send falls
// back to cleartext if the server does not advertise it (SECURITY). With
// requireStartTLS=true the send fails instead, guaranteeing encryption.
// Use ProtocolSMTPS or requireStartTLS=true to guarantee encryption.
func (p *MailProducer) trySendMail(ctx context.Context, addr string, auth smtp.Auth, from string, recipients []string, message []byte) error {
	ep := p.endpoint

	if err := ctx.Err(); err != nil {
		return err
	}

	if ep.scheme != ProtocolSMTPS && !ep.requireStartTLS {
		gocamel.LogError("SMTP cleartext: STARTTLS is opportunistic — credentials may travel unencrypted. "+
			"Use smtps:// or requireStartTLS=true for guaranteed TLS.", "host", ep.host, "port", ep.port)
	}

	conn, err := p.dialSMTP(ctx, addr)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("mail send aborted: %w", ctxErr)
		}
		return err
	}

	stopAbort := context.AfterFunc(ctx, func() {
		_ = conn.Close()
	})
	defer stopAbort()

	wrapErr := func(prefix string, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("%s: %w", prefix, ctxErr)
		}
		return fmt.Errorf("%s: %w", prefix, err)
	}

	p.extendDeadline(conn)
	client, err := smtp.NewClient(conn, ep.host)
	if err != nil {
		_ = conn.Close()
		return wrapErr("SMTP client creation error", err)
	}
	defer client.Close()

	if ep.scheme != ProtocolSMTPS {
		p.extendDeadline(conn)
		ok, _ := client.Extension("STARTTLS")
		if !ok && ep.requireStartTLS {
			return fmt.Errorf("requireStartTLS=true but server %s does not advertise STARTTLS: refusing to send", addr)
		}
		if ok {
			if err := client.StartTLS(p.tlsConfigForSend()); err != nil {
				return wrapErr("STARTTLS negotiation error", err)
			}
		}
	}

	if auth != nil {
		p.extendDeadline(conn)
		if ok, _ := client.Extension("AUTH"); !ok {
			return errors.New("smtp: server doesn't support AUTH")
		}
		if err := client.Auth(auth); err != nil {
			return wrapErr("authentication error", err)
		}
	}

	p.extendDeadline(conn)
	if err := client.Mail(from); err != nil {
		return wrapErr("MAIL FROM error", err)
	}
	for _, rcpt := range recipients {
		p.extendDeadline(conn)
		if err := client.Rcpt(rcpt); err != nil {
			return wrapErr(fmt.Sprintf("RCPT TO %s error", rcpt), err)
		}
	}

	p.extendDeadline(conn)
	w, err := client.Data()
	if err != nil {
		return wrapErr("DATA error", err)
	}
	if _, err := w.Write(message); err != nil {
		return wrapErr("message write error", err)
	}
	if err := w.Close(); err != nil {
		return wrapErr("DATA close error", err)
	}

	// The server has already accepted the message (250 OK on DATA close).
	// QUIT is strictly best-effort so a dropped connection does not cause
	// sendMail to retry and deliver duplicates.
	p.extendDeadline(conn)
	if err := client.Quit(); err != nil && ep.debugMode {
		gocamel.LogError("SMTP QUIT failed after message accepted", "error", err)
	}
	return nil
}

// dialSMTP opens the connection of a send attempt: plain TCP for smtp (TLS
// is negotiated later via STARTTLS), native TLS for smtps (port 465). The
// connect and the TLS handshake are bounded by the connection timeout.
func (p *MailProducer) dialSMTP(ctx context.Context, addr string) (net.Conn, error) {
	ep := p.endpoint

	dialer := &net.Dialer{Timeout: ep.connectionTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("SMTP connection error: %w", err)
	}
	if ep.scheme != ProtocolSMTPS {
		return conn, nil
	}

	p.extendDeadline(conn)
	tlsConn := tls.Client(conn, p.tlsConfigForSend())
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("TLS connection error: %w", err)
	}
	return tlsConn, nil
}

func (p *MailProducer) tlsConfigForSend() *tls.Config {
	if p.tlsConfig != nil {
		return p.tlsConfig.Clone()
	}
	return &tls.Config{
		ServerName: p.endpoint.host,
		MinVersion: tls.VersionTLS12,
	}
}

// extendDeadline bounds the next protocol phase with the connection timeout.
// On a TLS connection the deadline also applies to the underlying TCP conn.
func (p *MailProducer) extendDeadline(conn net.Conn) {
	if p.endpoint.connectionTimeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(p.endpoint.connectionTimeout))
	}
}

// sleepWithContext waits for d and reports false if ctx ended first.
func sleepWithContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// isNonRetryableError determines whether an error should not be retried.
//
// Context cancellation and deadline expiry stop retries immediately.
// Otherwise an SMTP reply decides first: net/smtp surfaces it as
// *textproto.Error, and RFC 5321 defines 5xx as permanent and 4xx as
// transient, whatever the text. Network errors are transient. Only errors
// carrying neither fall back to keyword matching.
func isNonRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var reply *textproto.Error
	if errors.As(err, &reply) {
		switch {
		case reply.Code >= 500 && reply.Code < 600:
			return true
		case reply.Code >= 400 && reply.Code < 500:
			return false
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
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
// Producer-side helpers
// ---------------------------------------------------------------------------

// effectiveMailHeaders resolves the From/To/Cc/Bcc/Subject/Content-Type values
// used to build the outgoing email. SECURITY: exchange headers only override
// the URI configuration when the endpoint explicitly opts in with
// allowHeaderOverride=true. Headers can be influenced by untrusted input, so
// by default the trusted endpoint configuration is used.
func effectiveMailHeaders(ep *MailEndpoint, exchange *gocamel.Exchange) (from, to, cc, bcc, subject, contentType string) {
	if ep.allowHeaderOverride {
		return getMailHeader(exchange, "From", ep.from),
			getMailHeader(exchange, "To", ep.to),
			getMailHeader(exchange, "Cc", ep.cc),
			getMailHeader(exchange, "Bcc", ep.bcc),
			getMailHeader(exchange, "Subject", ep.subject),
			getMailHeader(exchange, "Content-Type", ep.contentType)
	}
	return ep.from, ep.to, ep.cc, ep.bcc, ep.subject, ep.contentType
}

func getMailHeader(exchange *gocamel.Exchange, header, defaultVal string) string {
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

// sanitizeHeaderValue strips carriage returns and line feeds from a value that
// is about to be written into an SMTP message header, preventing header
// injection (SECURITY).
func sanitizeHeaderValue(v string) string {
	if !strings.ContainsAny(v, "\r\n") {
		return v
	}
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, v)
}

func formatContentTypeHeader(contentType string) string {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err == nil && mediaType != "" {
		if _, hasCharset := params["charset"]; !hasCharset {
			if params == nil {
				params = make(map[string]string)
			}
			params["charset"] = "UTF-8"
		}
		if formatted := mime.FormatMediaType(mediaType, params); formatted != "" {
			return formatted
		}
	}
	if strings.Contains(strings.ToLower(contentType), "charset=") {
		return contentType
	}
	return contentType + "; charset=UTF-8"
}

func parseSingleAddressStrict(s string) (string, error) {
	if strings.ContainsAny(s, "\r\n\x00") {
		return "", errors.New("address contains control characters")
	}
	addr, err := netmail.ParseAddress(s)
	if err != nil {
		return "", err
	}
	if addr.Address == "" || strings.ContainsAny(addr.Address, " \t\r\n<>") {
		return "", fmt.Errorf("invalid address %q", addr.Address)
	}
	return addr.Address, nil
}

func parseAddressListStrict(s string) ([]string, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	if strings.ContainsAny(s, "\r\n\x00") {
		return nil, errors.New("address list contains control characters")
	}
	list, err := netmail.ParseAddressList(s)
	if err != nil {
		return nil, err
	}
	addrs := make([]string, 0, len(list))
	for _, a := range list {
		if a.Address == "" || strings.ContainsAny(a.Address, " \t\r\n<>") {
			return nil, fmt.Errorf("invalid address %q", a.Address)
		}
		addrs = append(addrs, a.Address)
	}
	return addrs, nil
}

func parseAddresses(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	list, err := netmail.ParseAddressList(s)
	if err != nil {
		return nil
	}
	addrs := make([]string, 0, len(list))
	for _, a := range list {
		if a.Address != "" {
			addrs = append(addrs, a.Address)
		}
	}
	return addrs
}

func extractBody(exchange *gocamel.Exchange) []byte {
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

func extractAttachments(exchange *gocamel.Exchange) map[string][]byte {
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

func randomHex(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func generateMessageID() string {
	return fmt.Sprintf("%d.%s", time.Now().UnixNano(), randomHex(16))
}

func generateBoundary() string {
	return fmt.Sprintf("----=_Part_%d_%s", time.Now().UnixNano(), randomHex(12))
}
