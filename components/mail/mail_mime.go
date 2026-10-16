package mail

import (
	"bytes"
	"io"
	"strings"

	"github.com/emersion/go-message/mail"

	gocamel "github.com/tranchida/gocamel"
)

// parseMimeMessageResult holds the result of MIME parsing — body (plain and
// HTML) plus extracted attachments. Used by both MailConsumer (IMAP) and
// Pop3Consumer through the parseMime*Common helpers below.
type parseMimeMessageResult struct {
	bodyText    []byte
	bodyHTML    []byte
	attachments map[string][]byte
}

// parseMimeMessageCommon is the shared MIME parser used by both the IMAP and
// POP3 consumers. It returns plain body, HTML body and the attachments map.
// If the input is not a valid MIME message the raw bytes are returned as the
// plain body.
func parseMimeMessageCommon(data []byte) ([]byte, []byte, map[string][]byte) {
	result := &parseMimeMessageResult{
		attachments: make(map[string][]byte),
	}

	entity, err := mail.CreateReader(bytes.NewReader(data))
	if err != nil {
		// Not valid MIME — return as plain text
		return data, nil, result.attachments
	}

	parseEntityCommon(entity, result, "")

	return result.bodyText, result.bodyHTML, result.attachments
}

// parseEntityCommon recursively walks a MIME entity, populating result.
func parseEntityCommon(entity *mail.Reader, result *parseMimeMessageResult, parentContentType string) {
	header := entity.Header
	contentType, _, _ := header.ContentType()

	switch {
	case strings.HasPrefix(contentType, "multipart/alternative"):
		parseMultipartAlternativeCommon(entity, result)

	case strings.HasPrefix(contentType, "multipart/"): // multipart/mixed, multipart/related, etc.
		for {
			part, err := entity.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				break
			}
			handlePartCommon(part, result)
		}

	case strings.HasPrefix(contentType, "text/"):
		handleTextEntityCommon(entity, result, contentType)

	default:
		handleAttachmentEntityCommon(entity, result)
	}
}

// parseMultipartAlternativeCommon picks the best alternative part (HTML
// preferred over plain text), keeping the first non-text alternative as a
// fallback when neither plain nor HTML is present.
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
			if textPart == nil {
				textPart = partData
			}
		}
	}

	result.bodyText = textPart
	result.bodyHTML = htmlPart
}

// handlePartCommon processes a single multipart part — body text/HTML or
// attachment depending on Content-Type and Content-Disposition.
func handlePartCommon(part *mail.Part, result *parseMimeMessageResult) {
	contentType := part.Header.Get("Content-Type")
	contentDisp := part.Header.Get("Content-Disposition")

	data, err := decodePartCommon(part)
	if err != nil {
		return
	}

	isAttachment := strings.Contains(contentDisp, "attachment")

	if strings.HasPrefix(contentType, "text/") && !isAttachment {
		if strings.HasPrefix(contentType, "text/html") {
			result.bodyHTML = data
		} else {
			result.bodyText = data
		}
	} else {
		filename := extractFilenameFromDispositionCommon(contentDisp, contentType)
		if filename == "" {
			filename = "attachment.bin"
		}
		if len(data) > 0 {
			result.attachments[filename] = data
		}
	}
}

// handleTextEntityCommon processes a leaf text/* entity.
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

// handleAttachmentEntityCommon processes a non-text leaf entity as an attachment.
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

// decodePartCommon reads a part's body. go-message handles transfer encoding
// (base64, quoted-printable) internally.
func decodePartCommon(part *mail.Part) ([]byte, error) {
	return gocamel.ReadLimited(part.Body, gocamel.DefaultMaxBodySize)
}

// extractFilenameFromDispositionCommon returns the filename advertised by
// Content-Disposition, falling back to the Content-Type "name" parameter.
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
	if strings.Contains(contentType, "name=") {
		parts := strings.Split(contentType, "name=")
		if len(parts) > 1 {
			return strings.Trim(parts[1], ` "'`)
		}
	}
	return ""
}

// extractFilenameCommon returns the filename from a mail.Header, looking at
// Content-Disposition first then the Content-Type "name" parameter.
func extractFilenameCommon(header mail.Header) string {
	disp, _, _ := header.ContentDisposition()
	if disp != "" {
		if strings.Contains(disp, "filename=") {
			parts := strings.Split(disp, "filename=")
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

	_, params, _ := header.ContentType()
	if name, ok := params["name"]; ok && name != "" {
		return name
	}

	return ""
}
