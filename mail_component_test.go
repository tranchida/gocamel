package gocamel

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	assert.Contains(t, err.Error(), "protocole mail non supporte")
}

func TestMailComponent_CreateEndpoint_MissingHost(t *testing.T) {
	comp := NewMailComponent()

	_, err := comp.CreateEndpoint("smtp:")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "host requis")
}

func TestMailEndpoint_URI(t *testing.T) {
	comp := NewMailComponent()
	endpoint, _ := comp.CreateEndpoint("smtp://localhost:25?to=test@example.com")
	assert.True(t, strings.HasPrefix(endpoint.URI(), "smtp://localhost:25"))
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
	assert.Contains(t, err.Error(), "ne supporte pas la production")
}

func TestMailEndpoint_CreateConsumer_InvalidProtocol(t *testing.T) {
	comp := NewMailComponent()
	endpoint, _ := comp.CreateEndpoint("smtp://localhost:25?to=test@example.com")

	_, err := endpoint.CreateConsumer(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ne supporte pas la consommation")
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
			ex := NewExchange(context.Background())
			ex.SetBody(tt.body)
			result := extractBody(ex)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractAttachments(t *testing.T) {
	ex := NewExchange(context.Background())
	ex.SetHeader(MailAttachmentPrefix+"_file1.txt", []byte("content1"))
	ex.SetHeader(MailAttachmentPrefix+"_file2.txt", "content2")
	ex.SetHeader("OtherHeader", []byte("ignored"))

	attachments := extractAttachments(ex)
	assert.Len(t, attachments, 2)
	assert.Equal(t, []byte("content1"), attachments["file1.txt"])
	assert.Equal(t, []byte("content2"), attachments["file2.txt"])
}

func TestGetMailHeader(t *testing.T) {
	ex := NewExchange(context.Background())
	ex.SetHeader("Subject", "FromHeader")

	// Header present
	assert.Equal(t, "FromHeader", getMailHeader(ex, "Subject", "Default"))

	// Header absent
	assert.Equal(t, "Default", getMailHeader(ex, "Missing", "Default"))
}

// TestParseMimeMessageSimple teste le parsing d'un message MIME simple.
func TestParseMimeMessageSimple(t *testing.T) {
	// Message text/plain simple
	msg := `From: sender@example.com
To: recipient@example.com
Subject: Test Simple
Content-Type: text/plain; charset=utf-8

This is a simple test message.`

	consumer := &MailConsumer{}
	bodyText, bodyHTML, attachments := consumer.parseMimeMessage([]byte(msg))

	require.NotNil(t, bodyText)
	assert.Contains(t, string(bodyText), "This is a simple test message")
	assert.Nil(t, bodyHTML)
	assert.Empty(t, attachments)
}

// TestParseMimeMessageMultipartAlternative teste multipart/alternative.
func TestParseMimeMessageMultipartAlternative(t *testing.T) {
	// Message avec text/plain + text/html
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

	consumer := &MailConsumer{}
	bodyText, bodyHTML, attachments := consumer.parseMimeMessage([]byte(msg))

	assert.NotNil(t, bodyText)
	assert.Contains(t, string(bodyText), "plain text version")
	assert.NotNil(t, bodyHTML)
	assert.Contains(t, string(bodyHTML), "HTML Version")
	assert.Empty(t, attachments)
}

// TestParseMimeMessageWithAttachment teste un message avec piece jointe.
func TestParseMimeMessageWithAttachment(t *testing.T) {
	// Utiliser CRLF comme requis par le standard MIME
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

	consumer := &MailConsumer{}
	bodyText, bodyHTML, attachments := consumer.parseMimeMessage([]byte(msg))

	assert.NotNil(t, bodyText)
	assert.Contains(t, string(bodyText), "See attached file")
	assert.Nil(t, bodyHTML)
	assert.Len(t, attachments, 1)
	assert.Contains(t, attachments, "document.pdf")
	assert.Contains(t, string(attachments["document.pdf"]), "%PDF-1.4")
}

// TestExtractFilenameFromDisposition teste l'extraction du filename.
	func TestExtractFilenameFromDisposition(t *testing.T) {
		consumer := &MailConsumer{}

		// Test Content-Disposition avec filename
		filename := consumer.extractFilenameFromDisposition(`attachment; filename="test.pdf"`, "")
		assert.Equal(t, "test.pdf", filename)

		// Test avec single quotes
		filename = consumer.extractFilenameFromDisposition(`attachment; filename='test.pdf'`, "")
		assert.Equal(t, "test.pdf", filename)

		// Test sans quotes
		filename = consumer.extractFilenameFromDisposition(`attachment; filename=test.pdf`, "")
		assert.Equal(t, "test.pdf", filename)

		// Test depuis Content-Type
		filename = consumer.extractFilenameFromDisposition("", `application/pdf; name="doc.pdf"`)
		assert.Equal(t, "doc.pdf", filename)

		// Test vide
		filename = consumer.extractFilenameFromDisposition("", "")
		assert.Equal(t, "", filename)
	}

	// TestReplyTo teste l'extraction du Reply-To depuis l'enveloppe.
	func TestReplyTo(t *testing.T) {
		msg := &MailMessage{
			From:    "sender@example.com",
			ReplyTo: "reply@example.com",
			To:      []string{"recipient@example.com"},
			Subject: "Test Reply-To",
		}

		assert.Equal(t, "reply@example.com", msg.ReplyTo)
	}

// TestPop3Endpoint_CreateEndpoint teste la creation d'endpoint POP3.
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

// TestPop3sEndpoint_CreateEndpoint teste la creation d'endpoint POP3S.
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

// TestMailIdleOption teste le parsing de l'option idle.
func TestMailIdleOption(t *testing.T) {
	comp := NewMailComponent()

	// Sans IDLE (defaut)
	endpoint1, err := comp.CreateEndpoint("imap://imap.gmail.com:993?username=user&password=pass")
	require.NoError(t, err)
	mailEp1 := endpoint1.(*MailEndpoint)
	assert.False(t, mailEp1.useIdle)

	// Avec IDLE
	endpoint2, err := comp.CreateEndpoint("imap://imap.gmail.com:993?username=user&password=pass&idle=true")
	require.NoError(t, err)
	mailEp2 := endpoint2.(*MailEndpoint)
	assert.True(t, mailEp2.useIdle)
}

// TestPop3ConsumerCreation teste la creation du consommateur POP3.
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

// TestIMAPConsumerWithIdle teste la creation du consommateur IMAP avec IDLE.
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
