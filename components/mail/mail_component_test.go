package mail

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gocamel "github.com/tranchida/gocamel"
)

func TestNewMailComponent(t *testing.T) {
	comp := NewMailComponent()
	require.NotNil(t, comp)
}

func TestMailComponent_SetDefaults(t *testing.T) {
	comp := NewMailComponent()

	comp.SetDefaultFrom("test@example.com")
	comp.SetDefaultSubject("Test Subject")

	endpoint, err := comp.CreateEndpoint("smtp://localhost:25?to=dest@example.com")
	require.NoError(t, err)

	mailEp, ok := endpoint.(*MailEndpoint)
	require.True(t, ok)
	assert.Equal(t, "test@example.com", mailEp.from)
}

func TestMailComponent_CreateEndpoint_ValidSMTP(t *testing.T) {
	comp := NewMailComponent()

	endpoint, err := comp.CreateEndpoint("smtp://mail.example.com:587?to=dest@example.com&from=src@example.com")
	require.NoError(t, err)
	require.NotNil(t, endpoint)

	mailEp, ok := endpoint.(*MailEndpoint)
	require.True(t, ok)

	assert.Equal(t, "smtp", mailEp.scheme)
	assert.Equal(t, "mail.example.com", mailEp.host)
	assert.Equal(t, 587, mailEp.port)
	assert.Equal(t, "dest@example.com", mailEp.to)
	assert.Equal(t, "src@example.com", mailEp.from)
}

func TestMailComponent_CreateEndpoint_ValidSMTPS(t *testing.T) {
	comp := NewMailComponent()

	endpoint, err := comp.CreateEndpoint("smtps://smtp.gmail.com:465?username=user@gmail.com&password=pass")
	require.NoError(t, err)
	require.NotNil(t, endpoint)

	mailEp, ok := endpoint.(*MailEndpoint)
	require.True(t, ok)

	assert.Equal(t, "smtps", mailEp.scheme)
	assert.Equal(t, "smtp.gmail.com", mailEp.host)
	assert.Equal(t, 465, mailEp.port)
	assert.Equal(t, "user@gmail.com", mailEp.username)
	assert.Equal(t, "pass", mailEp.password)
}

func TestMailComponent_CreateEndpoint_ValidIMAP(t *testing.T) {
	comp := NewMailComponent()

	endpoint, err := comp.CreateEndpoint("imap://imap.gmail.com:993?folderName=INBOX&username=user&password=pass")
	require.NoError(t, err)
	require.NotNil(t, endpoint)

	mailEp, ok := endpoint.(*MailEndpoint)
	require.True(t, ok)

	assert.Equal(t, "imap", mailEp.scheme)
	assert.Equal(t, "INBOX", mailEp.folderName)
	assert.True(t, mailEp.unseen)
	assert.True(t, mailEp.peek)
}

func TestMailComponent_CreateEndpoint_DefaultPorts(t *testing.T) {
	tests := []struct {
		uri      string
		expected int
	}{
		{"smtp://localhost", DefaultSMTPPort},
		{"smtps://localhost", DefaultSMTPSPort},
		{"pop3://localhost", DefaultPOP3Port},
		{"pop3s://localhost", DefaultPOP3SPort},
		{"imap://localhost", DefaultIMAPPort},
		{"imaps://localhost", DefaultIMAPSPort},
	}

	comp := NewMailComponent()

	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			endpoint, err := comp.CreateEndpoint(tt.uri + "?to=test@example.com")
			require.NoError(t, err)

			mailEp := endpoint.(*MailEndpoint)
			assert.Equal(t, tt.expected, mailEp.port)
		})
	}
}

func TestMailComponent_CreateEndpoint_InvalidScheme(t *testing.T) {
	comp := NewMailComponent()

	_, err := comp.CreateEndpoint("http://example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mail protocol")
}

func TestMailComponent_CreateEndpoint_MissingHost(t *testing.T) {
	comp := NewMailComponent()

	_, err := comp.CreateEndpoint("smtp:")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "host required")
}

func TestMailEndpoint_URI(t *testing.T) {
	comp := NewMailComponent()
	endpoint, _ := comp.CreateEndpoint("smtp://localhost:25?to=test@example.com")
	assert.True(t, strings.HasPrefix(endpoint.URI(), "smtp://localhost:25"))
}

func TestMailEndpoint_URI_RedactsCredentials(t *testing.T) {
	comp := NewMailComponent()
	endpoint, err := comp.CreateEndpoint("smtps://smtp.gmail.com:465?username=user@gmail.com&password=super-secret-password&to=dest@example.com")
	require.NoError(t, err)

	uri := endpoint.URI()
	assert.NotContains(t, uri, "super-secret-password", "URI() must not leak the password")
	assert.Contains(t, uri, "password=xxxxx", "URI() must mask the password with xxxxx")
	assert.Contains(t, uri, "to=dest%40example.com", "URI() must preserve non-sensitive parameters")
}

func TestMailEndpoint_isProducerProtocol(t *testing.T) {
	tests := []struct {
		scheme   string
		expected bool
	}{
		{"smtp", true},
		{"smtps", true},
		{"pop3", false},
		{"pop3s", false},
		{"imap", false},
		{"imaps", false},
	}

	for _, tt := range tests {
		t.Run(tt.scheme, func(t *testing.T) {
			comp := NewMailComponent()
			endpoint, _ := comp.CreateEndpoint(tt.scheme + "://localhost:25?to=test@example.com")
			mailEp := endpoint.(*MailEndpoint)
			assert.Equal(t, tt.expected, mailEp.isProducerProtocol())
		})
	}
}

func TestMailEndpoint_isConsumerProtocol(t *testing.T) {
	tests := []struct {
		scheme   string
		expected bool
	}{
		{"smtp", false},
		{"smtps", false},
		{"pop3", true},
		{"pop3s", true},
		{"imap", true},
		{"imaps", true},
	}

	for _, tt := range tests {
		t.Run(tt.scheme, func(t *testing.T) {
			comp := NewMailComponent()
			endpoint, _ := comp.CreateEndpoint(tt.scheme + "://localhost:25?to=test@example.com")
			mailEp := endpoint.(*MailEndpoint)
			assert.Equal(t, tt.expected, mailEp.isConsumerProtocol())
		})
	}
}

func TestMailEndpoint_CreateProducer(t *testing.T) {
	comp := NewMailComponent()
	endpoint, _ := comp.CreateEndpoint("smtp://localhost:25?to=test@example.com")

	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)
	require.NotNil(t, producer)

	_, ok := producer.(*MailProducer)
	assert.True(t, ok)
}

func TestMailEndpoint_CreateProducer_InvalidProtocol(t *testing.T) {
	comp := NewMailComponent()
	endpoint, _ := comp.CreateEndpoint("imap://localhost:993")

	_, err := endpoint.CreateProducer()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support producing")
}

func TestMailEndpoint_CreateConsumer_InvalidProtocol(t *testing.T) {
	comp := NewMailComponent()
	endpoint, _ := comp.CreateEndpoint("smtp://localhost:25?to=test@example.com")

	_, err := endpoint.CreateConsumer(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support consuming")
}

func TestMailProducer_StartStop(t *testing.T) {
	comp := NewMailComponent()
	endpoint, _ := comp.CreateEndpoint("smtp://localhost:25?to=test@example.com")
	producer, _ := endpoint.CreateProducer()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := producer.Start(ctx)
	require.NoError(t, err)

	err = producer.Stop()
	require.NoError(t, err)
}

func TestParseAddresses(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"", nil},
		{"test@example.com", []string{"test@example.com"}},
		{"a@example.com,b@example.com", []string{"a@example.com", "b@example.com"}},
		{"  a@example.com  ,  b@example.com  ", []string{"a@example.com", "b@example.com"}},
		{"a@example.com, , b@example.com", []string{"a@example.com", "b@example.com"}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := parseAddresses(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetFirstNotEmpty(t *testing.T) {
	assert.Equal(t, "first", getFirstNotEmpty("first", "second"))
	assert.Equal(t, "second", getFirstNotEmpty("", "second", "third"))
	assert.Equal(t, "third", getFirstNotEmpty("", "", "third"))
	assert.Equal(t, "", getFirstNotEmpty("", "", ""))
}

func TestParseInt(t *testing.T) {
	assert.Equal(t, 42, parseInt("42", 10))
	assert.Equal(t, 10, parseInt("", 10))
	assert.Equal(t, 10, parseInt("invalid", 10))
}

func TestParseDurationMs(t *testing.T) {
	defaultVal := 5 * time.Second
	assert.Equal(t, 42*time.Millisecond, parseDurationMs("42", defaultVal))
	assert.Equal(t, defaultVal, parseDurationMs("", defaultVal))
	assert.Equal(t, defaultVal, parseDurationMs("invalid", defaultVal))
}

func TestExtractBody(t *testing.T) {
	tests := []struct {
		name     string
		body     interface{}
		expected []byte
	}{
		{"string", "hello", []byte("hello")},
		{"bytes", []byte("hello"), []byte("hello")},
		{"int", 123, []byte("123")},
		{"nil", nil, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ex := gocamel.NewExchange(context.Background())
			ex.SetBody(tt.body)
			result := extractBody(ex)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractAttachments(t *testing.T) {
	ex := gocamel.NewExchange(context.Background())
	ex.SetHeader(MailAttachmentPrefix+"_file1.txt", []byte("content1"))
	ex.SetHeader(MailAttachmentPrefix+"_file2.txt", "content2")
	ex.SetHeader("OtherHeader", []byte("ignored"))

	attachments := extractAttachments(ex)
	assert.Len(t, attachments, 2)
	assert.Equal(t, []byte("content1"), attachments["file1.txt"])
	assert.Equal(t, []byte("content2"), attachments["file2.txt"])
}

func TestGetMailHeader(t *testing.T) {
	ex := gocamel.NewExchange(context.Background())
	ex.SetHeader("Subject", "FromHeader")

	// Header present
	assert.Equal(t, "FromHeader", getMailHeader(ex, "Subject", "Default"))

	// Header absent
	assert.Equal(t, "Default", getMailHeader(ex, "Missing", "Default"))
}

// TestMailComponent_AllowHeaderOverride tests that From/To headers are
// ignored by default and only applied when the endpoint opts in with
// allowHeaderOverride=true.
func TestMailComponent_AllowHeaderOverride(t *testing.T) {
	comp := NewMailComponent()

	tests := []struct {
		name     string
		uri      string
		wantFrom string
		wantTo   string
	}{
		{
			name:     "without flag the URI config is used, headers are ignored",
			uri:      "smtp://localhost:25?from=uri@example.com&to=dest@example.com",
			wantFrom: "uri@example.com",
			wantTo:   "dest@example.com",
		},
		{
			name:     "with flag the From/To headers override the URI config",
			uri:      "smtp://localhost:25?from=uri@example.com&to=dest@example.com&allowHeaderOverride=true",
			wantFrom: "header@example.com",
			wantTo:   "header-dest@example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			endpoint, err := comp.CreateEndpoint(tt.uri)
			require.NoError(t, err)
			mailEp := endpoint.(*MailEndpoint)

			ex := gocamel.NewExchange(context.Background())
			ex.SetHeader("From", "header@example.com")
			ex.SetHeader("To", "header-dest@example.com")

			from, to, _, _, _, _ := effectiveMailHeaders(mailEp, ex)
			assert.Equal(t, tt.wantFrom, from)
			assert.Equal(t, tt.wantTo, to)
		})
	}
}

// TestMailComponent_RequireStartTLS_Option tests parsing of the
// requireStartTLS option (default false, opt-in true).
func TestMailComponent_RequireStartTLS_Option(t *testing.T) {
	comp := NewMailComponent()

	endpoint, err := comp.CreateEndpoint("smtp://localhost:25?to=test@example.com")
	require.NoError(t, err)
	assert.False(t, endpoint.(*MailEndpoint).requireStartTLS)

	endpoint, err = comp.CreateEndpoint("smtp://localhost:25?to=test@example.com&requireStartTLS=true")
	require.NoError(t, err)
	assert.True(t, endpoint.(*MailEndpoint).requireStartTLS)
}

// TestMailProducer_RequireStartTLS_RefusesPlainServer verifies that a producer
// configured with requireStartTLS=true refuses to send when the SMTP server
// does not advertise STARTTLS, using an in-process stub SMTP server.
func TestMailProducer_RequireStartTLS_RefusesPlainServer(t *testing.T) {
	// In-process stub SMTP server without STARTTLS support.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				fmt.Fprintf(conn, "220 stub.example.com ESMTP\r\n")
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					switch {
					case strings.HasPrefix(strings.ToUpper(line), "EHLO"):
						fmt.Fprintf(conn, "250-stub.example.com\r\n250 8BITMIME\r\n")
					case strings.HasPrefix(strings.ToUpper(line), "QUIT"):
						fmt.Fprintf(conn, "221 Bye\r\n")
						return
					default:
						fmt.Fprintf(conn, "250 OK\r\n")
					}
				}
			}(conn)
		}
	}()
	// Unblock Accept when the test is done.
	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	comp := NewMailComponent()
	// connectionTimeout is short: the "STARTTLS not advertised" error string
	// does not match isNonRetryableError, so the producer retries with
	// backoff before surfacing the failure.
	endpoint, err := comp.CreateEndpoint(fmt.Sprintf("smtp://%s?from=src@example.com&to=dest@example.com&requireStartTLS=true&connectionTimeout=100", ln.Addr().String()))
	require.NoError(t, err)
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("hello")

	err = producer.Send(ex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "STARTTLS")
}

// TestPopulateExchange_HeadersPrefixed verifies that raw envelope headers of
// a received email are stored under the CamelMailHeader_ prefix so they
// cannot collide with the producer-side From/To headers.
func TestPopulateExchange_HeadersPrefixed(t *testing.T) {
	msg := &MailMessage{
		From:    "sender@example.com",
		To:      []string{"dest@example.com"},
		Subject: "Hello",
		Date:    time.Now(),
		Headers: map[string]string{
			"X-Spam-Score": "5.0",
		},
	}

	consumers := []struct {
		name     string
		populate func(exchange *gocamel.Exchange, msg *MailMessage)
	}{
		{"imap", (&MailConsumer{}).populateExchange},
		{"pop3", (&Pop3Consumer{}).populateExchange},
	}

	for _, c := range consumers {
		t.Run(c.name, func(t *testing.T) {
			ex := gocamel.NewExchange(context.Background())
			c.populate(ex, msg)

			// Raw envelope headers are prefixed.
			v, ok := ex.GetIn().GetHeader(MailHeaderPrefix + "X-Spam-Score")
			assert.True(t, ok)
			assert.Equal(t, "5.0", v)

			// The unprefixed header does not exist.
			_, ok = ex.GetIn().GetHeader("X-Spam-Score")
			assert.False(t, ok)

			// Well-known headers use the dedicated constants.
			from, ok := ex.GetIn().GetHeader(MailFrom)
			assert.True(t, ok)
			assert.Equal(t, "sender@example.com", from)
		})
	}
}

// TestParseMimeMessageSimple tests the parsing of a simple MIME message.
func TestParseMimeMessageSimple(t *testing.T) {
	// Simple text/plain message
	msg := `From: sender@example.com
To: recipient@example.com
Subject: Test Simple
Content-Type: text/plain; charset=utf-8

This is a simple test message.`

	bodyText, bodyHTML, attachments := parseMimeMessageCommon([]byte(msg))

	require.NotNil(t, bodyText)
	assert.Contains(t, string(bodyText), "This is a simple test message")
	assert.Nil(t, bodyHTML)
	assert.Empty(t, attachments)
}

// TestParseMimeMessageMultipartAlternative tests multipart/alternative.
func TestParseMimeMessageMultipartAlternative(t *testing.T) {
	// Message with text/plain + text/html
	boundary := "----=_Part_test_123456"
	msg := fmt.Sprintf(`From: sender@example.com
To: recipient@example.com
Subject: Test multipart/alternative
Content-Type: multipart/alternative; boundary="%s"

------=_Part_test_123456
Content-Type: text/plain; charset=utf-8

This is the plain text version.

------=_Part_test_123456
Content-Type: text/html; charset=utf-8

<html><body><h1>HTML Version</h1></body></html>

------=_Part_test_123456--`, boundary)

	bodyText, bodyHTML, attachments := parseMimeMessageCommon([]byte(msg))

	assert.NotNil(t, bodyText)
	assert.Contains(t, string(bodyText), "plain text version")
	assert.NotNil(t, bodyHTML)
	assert.Contains(t, string(bodyHTML), "HTML Version")
	assert.Empty(t, attachments)
}

// TestParseMimeMessageWithAttachment tests a message with an attachment.
func TestParseMimeMessageWithAttachment(t *testing.T) {
	// Use CRLF as required by the MIME standard
	msg := "From: sender@example.com\r\n" +
		"To: recipient@example.com\r\n" +
		"Subject: Test with attachment\r\n" +
		"Content-Type: multipart/mixed; boundary=\"boundary123\"\r\n" +
		"\r\n" +
		"--boundary123\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"See attached file.\r\n" +
		"\r\n" +
		"--boundary123\r\n" +
		"Content-Type: application/pdf; name=\"document.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"document.pdf\"\r\n" +
		"\r\n" +
		"%PDF-1.4 fake pdf content\r\n" +
		"--boundary123--"

	bodyText, bodyHTML, attachments := parseMimeMessageCommon([]byte(msg))

	assert.NotNil(t, bodyText)
	assert.Contains(t, string(bodyText), "See attached file")
	assert.Nil(t, bodyHTML)
	assert.Len(t, attachments, 1)
	assert.Contains(t, attachments, "document.pdf")
	assert.Contains(t, string(attachments["document.pdf"]), "%PDF-1.4")
}

// TestExtractFilenameFromDisposition tests filename extraction.
func TestExtractFilenameFromDisposition(t *testing.T) {
	// Test Content-Disposition with filename
	filename := extractFilenameFromDispositionCommon(`attachment; filename="test.pdf"`, "")
	assert.Equal(t, "test.pdf", filename)

	// Test with single quotes
	filename = extractFilenameFromDispositionCommon(`attachment; filename='test.pdf'`, "")
	assert.Equal(t, "test.pdf", filename)

	// Test without quotes
	filename = extractFilenameFromDispositionCommon(`attachment; filename=test.pdf`, "")
	assert.Equal(t, "test.pdf", filename)

	// Test from Content-Type
	filename = extractFilenameFromDispositionCommon("", `application/pdf; name="doc.pdf"`)
	assert.Equal(t, "doc.pdf", filename)

	// Empty test
	filename = extractFilenameFromDispositionCommon("", "")
	assert.Equal(t, "", filename)
}

// TestReplyTo tests Reply-To extraction from the envelope.
func TestReplyTo(t *testing.T) {
	msg := &MailMessage{
		From:    "sender@example.com",
		ReplyTo: "reply@example.com",
		To:      []string{"recipient@example.com"},
		Subject: "Test Reply-To",
	}

	assert.Equal(t, "reply@example.com", msg.ReplyTo)
}

// TestPop3Endpoint_CreateEndpoint tests POP3 endpoint creation.
func TestPop3Endpoint_CreateEndpoint_ValidPOP3(t *testing.T) {
	comp := NewMailComponent()

	endpoint, err := comp.CreateEndpoint("pop3://pop.example.com:110?username=user&password=pass&delete=true")
	require.NoError(t, err)
	require.NotNil(t, endpoint)

	mailEp, ok := endpoint.(*MailEndpoint)
	require.True(t, ok)

	assert.Equal(t, "pop3", mailEp.scheme)
	assert.Equal(t, "pop.example.com", mailEp.host)
	assert.Equal(t, 110, mailEp.port)
	assert.Equal(t, true, mailEp.delete)
	assert.Equal(t, "user", mailEp.username)
	assert.Equal(t, "pass", mailEp.password)
}

// TestPop3sEndpoint_CreateEndpoint tests POP3S endpoint creation.
func TestPop3sEndpoint_CreateEndpoint_ValidPOP3S(t *testing.T) {
	comp := NewMailComponent()

	endpoint, err := comp.CreateEndpoint("pop3s://pop.gmail.com:995?username=user&password=pass&fetchSize=50")
	require.NoError(t, err)
	require.NotNil(t, endpoint)

	mailEp, ok := endpoint.(*MailEndpoint)
	require.True(t, ok)

	assert.Equal(t, "pop3s", mailEp.scheme)
	assert.True(t, mailEp.isSecure())
	assert.Equal(t, 50, mailEp.fetchSize)
}

// TestMailIdleOption tests parsing of the idle option.
func TestMailIdleOption(t *testing.T) {
	comp := NewMailComponent()

	// Without IDLE (default)
	endpoint1, err := comp.CreateEndpoint("imap://imap.gmail.com:993?username=user&password=pass")
	require.NoError(t, err)
	mailEp1 := endpoint1.(*MailEndpoint)
	assert.False(t, mailEp1.useIdle)

	// With IDLE
	endpoint2, err := comp.CreateEndpoint("imap://imap.gmail.com:993?username=user&password=pass&idle=true")
	require.NoError(t, err)
	mailEp2 := endpoint2.(*MailEndpoint)
	assert.True(t, mailEp2.useIdle)
}

// TestPop3ConsumerCreation tests POP3 consumer creation.
func TestPop3ConsumerCreation(t *testing.T) {
	comp := NewMailComponent()

	endpoint, err := comp.CreateEndpoint("pop3://localhost:110")
	require.NoError(t, err)

	consumer, err := endpoint.CreateConsumer(nil)
	require.NoError(t, err)
	require.NotNil(t, consumer)

	_, ok := consumer.(*Pop3Consumer)
	assert.True(t, ok, "Should create Pop3Consumer for POP3 protocol")
}

// TestIMAPConsumerWithIdle tests IMAP consumer creation with IDLE.
func TestIMAPConsumerWithIdle(t *testing.T) {
	comp := NewMailComponent()

	endpoint, err := comp.CreateEndpoint("imap://localhost:143?idle=true")
	require.NoError(t, err)

	consumer, err := endpoint.CreateConsumer(nil)
	require.NoError(t, err)
	require.NotNil(t, consumer)

	_, ok := consumer.(*MailConsumer)
	assert.True(t, ok, "Should create MailConsumer for IMAP protocol")

	mailConsumer := consumer.(*MailConsumer)
	assert.True(t, mailConsumer.endpoint.useIdle)
}
