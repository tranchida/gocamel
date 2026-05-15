package gocamel

import "time"

// MailMessage represents a received email message.
// Shared by MailConsumer (IMAP) and Pop3Consumer.
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
