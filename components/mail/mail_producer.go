package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net/smtp"
	"strings"
	"time"

	gocamel "gitlab.com/tranchida/gocamel"
)

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
func (p *MailProducer) Send(exchange *gocamel.Exchange) error {
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
			fmt.Fprintf(&buf, "Content-Disposition: attachment; filename=\"%s\"\r\n", sanitizeHeaderValue(name))
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
			delay := baseDelay * time.Duration(1<<(attempt-1))
			if delay > maxDelay {
				delay = maxDelay
			}
			if ep.debugMode {
				gocamel.LogInfo("Mail retry", "attempt", attempt+1, "max", maxRetries, "error", lastErr, "delay", delay)
			}
			time.Sleep(delay)
		}

		err := p.trySendMail(addr, auth, from, recipients, message)
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
			MinVersion: tls.VersionTLS12,
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

	// Standard SMTP with STARTTLS if available.
	// SECURITY: smtp.SendMail attempts STARTTLS but silently falls back to
	// cleartext if the server does not advertise it. Credentials and the
	// message body may travel unencrypted. Use ProtocolSMTPS or a server that
	// requires STARTTLS to guarantee encryption.
	gocamel.LogError("SMTP cleartext: STARTTLS is opportunistic — credentials may travel unencrypted. "+
		"Use smtps:// for guaranteed TLS.", "host", ep.host, "port", ep.port)
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
// Producer-side helpers
// ---------------------------------------------------------------------------

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

func generateMessageID() string {
	return fmt.Sprintf("%d.%d.%d", time.Now().UnixNano(), time.Now().Unix(), time.Now().Nanosecond())
}

func generateBoundary() string {
	return fmt.Sprintf("----=_Part_%d_%d", time.Now().UnixNano(), time.Now().Unix())
}
