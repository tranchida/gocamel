package mail

import (
	"bytes"
	"io"
	"mime"
	netmail "net/mail"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset"
	"github.com/emersion/go-message/mail"

	gocamel "github.com/tranchida/gocamel"
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// parseMimeMessageResult holds the result of MIME parsing — body (plain and
// HTML) plus extracted attachments. Used by both MailConsumer (IMAP) and
// Pop3Consumer through the parseMime*Common helpers below.
type parseMimeMessageResult struct {
	bodyText    []byte
	bodyHTML    []byte
	attachments map[string][]byte
}

type mimeParamHeader interface {
	ContentDisposition() (string, map[string]string, error)
	ContentType() (string, map[string]string, error)
}

// parseMimeMessageCommon is the shared MIME parser using DefaultMaxBodySize.
func parseMimeMessageCommon(data []byte) ([]byte, []byte, map[string][]byte) {
	return parseMimeMessageWithLimit(data, gocamel.DefaultMaxBodySize)
}

// parseMimeMessageWithLimit parses a MIME message enforcing maxSize on each
// decoded part. If the input is not a valid MIME message the raw bytes are
// returned as the plain body.
func parseMimeMessageWithLimit(data []byte, maxSize int64) ([]byte, []byte, map[string][]byte) {
	result := &parseMimeMessageResult{
		attachments: make(map[string][]byte),
	}

	raw := bytes.TrimPrefix(data, utf8BOM)
	entity, err := mail.CreateReader(bytes.NewReader(raw))
	if (err != nil && !message.IsUnknownCharset(err)) || entity == nil {
		// Not valid MIME — return as plain text
		return raw, nil, result.attachments
	}
	defer entity.Close()

	parseEntityCommon(entity, result, maxSize)

	return result.bodyText, result.bodyHTML, result.attachments
}

// parseRawEmailMessage extracts RFC 5322 envelope headers and MIME bodies/attachments
// from raw message bytes into msg, tolerating unknown charsets and enforcing maxSize.
func parseRawEmailMessage(raw []byte, maxSize int64, msg *MailMessage) {
	if msg.Headers == nil {
		msg.Headers = make(map[string]string)
	}
	if msg.Attachments == nil {
		msg.Attachments = make(map[string][]byte)
	}

	cleaned := bytes.TrimPrefix(raw, utf8BOM)
	entity, err := mail.CreateReader(bytes.NewReader(cleaned))
	if (err != nil && !message.IsUnknownCharset(err)) || entity == nil {
		if msg.Body == nil && msg.BodyHTML == nil && len(msg.Attachments) == 0 {
			msg.Body = cleaned
		}
		if msg.Date.IsZero() {
			msg.Date = time.Now()
		}
		return
	}
	defer entity.Close()

	fields := entity.Header.Fields()
	for fields.Next() {
		key := fields.Key()
		text, fieldErr := fields.Text()
		if fieldErr != nil {
			text = fields.Value()
		}
		msg.Headers[key] = text

		switch strings.ToLower(key) {
		case "from":
			if msg.From == "" {
				msg.From = text
			}
		case "reply-to":
			if msg.ReplyTo == "" {
				msg.ReplyTo = text
			}
		case "to":
			if len(msg.To) == 0 {
				msg.To = parseAddresses(text)
			}
		case "cc":
			if len(msg.Cc) == 0 {
				msg.Cc = parseAddresses(text)
			}
		case "subject":
			if msg.Subject == "" {
				msg.Subject = text
			}
		case "message-id":
			if msg.MessageID == "" {
				msg.MessageID = text
			}
		case "content-type":
			if msg.ContentType == "" {
				msg.ContentType = text
			}
		case "date":
			if msg.Date.IsZero() {
				if parsed, parseErr := netmail.ParseDate(text); parseErr == nil {
					msg.Date = parsed
				}
			}
		}
	}

	if msg.Date.IsZero() {
		msg.Date = time.Now()
	}

	result := &parseMimeMessageResult{
		attachments: msg.Attachments,
	}
	parseEntityCommon(entity, result, maxSize)

	msg.Body = result.bodyText
	msg.BodyHTML = result.bodyHTML
	msg.Attachments = result.attachments

	if msg.Body == nil && msg.BodyHTML == nil && len(msg.Attachments) == 0 {
		msg.Body = cleaned
	}
}

// parseEntityCommon recursively walks a MIME entity, populating result.
func parseEntityCommon(entity *mail.Reader, result *parseMimeMessageResult, maxSize int64) {
	rawCT, _, _ := entity.Header.ContentType()
	contentType, _ := parseMediaTypeOrDefault(rawCT, DefaultContentType)

	switch {
	case strings.HasPrefix(contentType, "multipart/alternative"):
		parseMultipartAlternativeCommon(entity, result, maxSize)

	case strings.HasPrefix(contentType, "multipart/"): // multipart/mixed, multipart/related, etc.
		for {
			part, err := entity.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil && !message.IsUnknownCharset(err) {
				break
			}
			if part == nil {
				continue
			}
			handlePartCommon(part, result, maxSize)
		}

	case strings.HasPrefix(contentType, "text/"):
		handleTextEntityCommon(entity, result, contentType, maxSize)

	default:
		handleAttachmentEntityCommon(entity, result, maxSize)
	}
}

// parseMultipartAlternativeCommon picks the best alternative part (HTML
// preferred over plain text), keeping the first non-text alternative as a
// fallback when neither plain nor HTML is present.
func parseMultipartAlternativeCommon(entity *mail.Reader, result *parseMimeMessageResult, maxSize int64) {
	var textPart []byte
	var htmlPart []byte

	for {
		part, err := entity.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil && !message.IsUnknownCharset(err) {
			break
		}
		if part == nil {
			continue
		}

		contentType, _ := parseMediaTypeOrDefault(part.Header.Get("Content-Type"), DefaultContentType)
		dispType, _ := parseMediaTypeOrDefault(part.Header.Get("Content-Disposition"), "")
		partData, err := decodePartCommon(part, maxSize)
		if err != nil {
			continue
		}

		if dispType == "attachment" {
			filename := extractPartFilename(part)
			if len(partData) > 0 {
				result.attachments[filename] = partData
			}
			continue
		}

		switch {
		case contentType == "text/plain":
			textPart = partData
		case contentType == "text/html":
			htmlPart = partData
		default:
			if textPart == nil {
				textPart = partData
			}
		}
	}

	if textPart != nil {
		result.bodyText = textPart
	}
	if htmlPart != nil {
		result.bodyHTML = htmlPart
	}
}

// handlePartCommon processes a single multipart part — body text/HTML or
// attachment depending on Content-Type and Content-Disposition.
func handlePartCommon(part *mail.Part, result *parseMimeMessageResult, maxSize int64) {
	contentType, _ := parseMediaTypeOrDefault(part.Header.Get("Content-Type"), DefaultContentType)
	dispType, _ := parseMediaTypeOrDefault(part.Header.Get("Content-Disposition"), "")

	data, err := decodePartCommon(part, maxSize)
	if err != nil {
		return
	}

	isAttachment := dispType == "attachment"

	if strings.HasPrefix(contentType, "text/") && !isAttachment {
		if contentType == "text/html" || strings.HasPrefix(contentType, "text/html") {
			result.bodyHTML = data
		} else {
			result.bodyText = data
		}
		return
	}

	filename := extractPartFilename(part)
	if len(data) > 0 {
		result.attachments[filename] = data
	}
}

// handleTextEntityCommon processes a leaf text/* entity.
func handleTextEntityCommon(entity *mail.Reader, result *parseMimeMessageResult, contentType string, maxSize int64) {
	part, err := entity.NextPart()
	if (err != nil && !message.IsUnknownCharset(err)) || part == nil {
		return
	}
	data, err := decodePartCommon(part, maxSize)
	if err != nil {
		return
	}

	if strings.HasPrefix(contentType, "text/html") {
		result.bodyHTML = data
	} else {
		result.bodyText = data
	}
}

// handleAttachmentEntityCommon processes a non-text leaf entity as an attachment.
func handleAttachmentEntityCommon(entity *mail.Reader, result *parseMimeMessageResult, maxSize int64) {
	filename := extractFilenameCommon(entity.Header)
	if filename == "" {
		filename = "attachment.bin"
	}
	part, err := entity.NextPart()
	if (err != nil && !message.IsUnknownCharset(err)) || part == nil {
		return
	}
	data, err := decodePartCommon(part, maxSize)
	if err != nil {
		return
	}

	if len(data) > 0 {
		result.attachments[filename] = data
	}
}

// decodePartCommon reads a part's body bounded by maxSize.
// go-message handles transfer encoding (base64, quoted-printable) internally.
func decodePartCommon(part *mail.Part, maxSize int64) ([]byte, error) {
	return gocamel.ReadLimited(part.Body, maxSize)
}

func parseMediaTypeOrDefault(raw, defaultType string) (string, map[string]string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultType, nil
	}
	mediaType, params, err := mime.ParseMediaType(trimmed)
	if err != nil {
		base, _, _ := strings.Cut(trimmed, ";")
		base = strings.ToLower(strings.TrimSpace(base))
		if base == "" {
			return defaultType, nil
		}
		return base, nil
	}
	return mediaType, params
}

func extractPartFilename(part *mail.Part) string {
	var filename string
	if mh, ok := part.Header.(mimeParamHeader); ok {
		_, dispParams, _ := mh.ContentDisposition()
		filename = dispParams["filename"]
		if filename == "" {
			_, ctParams, _ := mh.ContentType()
			filename = ctParams["name"]
		}
	}
	if filename == "" {
		filename = extractFilenameFromDispositionCommon(
			part.Header.Get("Content-Disposition"),
			part.Header.Get("Content-Type"),
		)
	}
	filename = sanitizeAttachmentFilename(filename)
	if filename == "" {
		return "attachment.bin"
	}
	return filename
}

// extractFilenameFromDispositionCommon returns the sanitized filename advertised by
// Content-Disposition, falling back to the Content-Type "name" parameter.
func extractFilenameFromDispositionCommon(contentDisposition, contentType string) string {
	if contentDisposition != "" {
		if _, params, err := mime.ParseMediaType(contentDisposition); err == nil {
			if fn := params["filename"]; fn != "" {
				return sanitizeAttachmentFilename(trimSingleQuotes(fn))
			}
		} else if fn := extractFallbackParam(contentDisposition, "filename="); fn != "" {
			return sanitizeAttachmentFilename(fn)
		}
	}
	if contentType != "" {
		if _, params, err := mime.ParseMediaType(contentType); err == nil {
			if name := params["name"]; name != "" {
				return sanitizeAttachmentFilename(trimSingleQuotes(name))
			}
		} else if name := extractFallbackParam(contentType, "name="); name != "" {
			return sanitizeAttachmentFilename(name)
		}
	}
	return ""
}

func trimSingleQuotes(s string) string {
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return s[1 : len(s)-1]
	}
	return s
}

func extractFallbackParam(headerVal, key string) string {
	lower := strings.ToLower(headerVal)
	idx := strings.Index(lower, key)
	if idx < 0 {
		return ""
	}
	rest := headerVal[idx+len(key):]
	if semi := strings.Index(rest, ";"); semi >= 0 {
		rest = rest[:semi]
	}
	return strings.Trim(rest, ` "'`)
}

// extractFilenameCommon returns the sanitized filename from a mail.Header,
// looking at Content-Disposition first then the Content-Type "name" parameter.
func extractFilenameCommon(header mail.Header) string {
	_, params, _ := header.ContentDisposition()
	if filename := params["filename"]; filename != "" {
		return sanitizeAttachmentFilename(trimSingleQuotes(filename))
	}

	_, params, _ = header.ContentType()
	if name := params["name"]; name != "" {
		return sanitizeAttachmentFilename(trimSingleQuotes(name))
	}

	return ""
}

// sanitizeAttachmentFilename removes control chars and directory traversal
// segments from an attachment filename so downstream file sinks stay safe.
func sanitizeAttachmentFilename(name string) string {
	name = gocamel.SanitizeInput(name)
	name = strings.ReplaceAll(name, "\r", "")
	name = strings.ReplaceAll(name, "\n", "")
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = strings.ReplaceAll(name, "\\", "/")
	base := filepath.Base(name)
	if base == "" || base == "." || base == ".." || base == "/" {
		return ""
	}
	return base
}

func populateMailExchange(exchange *gocamel.Exchange, msg *MailMessage) {
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

	if msg.BodyHTML != nil {
		exchange.SetProperty("CamelMailBodyHTML", msg.BodyHTML)
	}

	for k, v := range msg.Headers {
		if !strings.EqualFold(k, MailFrom) &&
			!strings.EqualFold(k, MailTo) &&
			!strings.EqualFold(k, MailSubject) &&
			!strings.EqualFold(k, MailDate) &&
			!strings.EqualFold(k, MailContentType) {
			// SECURITY: prefix raw envelope headers so they cannot collide
			// with the well-known producer-side headers ("From", "To", ...).
			exchange.SetHeader(MailHeaderPrefix+k, v)
		}
	}

	for name, data := range msg.Attachments {
		safeName := sanitizeAttachmentFilename(name)
		if safeName == "" {
			safeName = "attachment.bin"
		}
		exchange.SetHeader(MailAttachmentPrefix+"_"+safeName, data)
	}
}

func getStringHeaderFromExchange(exchange *gocamel.Exchange, header string) string {
	if exchange == nil {
		return ""
	}
	for _, msg := range []*gocamel.Message{exchange.GetOut(), exchange.GetIn()} {
		if msg == nil {
			continue
		}
		if v, exists := msg.GetHeader(header); exists {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

func getBoolHeaderFromExchange(exchange *gocamel.Exchange, header string) bool {
	if exchange == nil {
		return false
	}
	for _, msg := range []*gocamel.Message{exchange.GetOut(), exchange.GetIn()} {
		if msg == nil {
			continue
		}
		if v, exists := msg.GetHeader(header); exists {
			if b, ok := v.(bool); ok {
				return b
			}
			if s, ok := v.(string); ok {
				return strings.EqualFold(s, "true")
			}
		}
	}
	return false
}
