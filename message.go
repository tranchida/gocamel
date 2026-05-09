package gocamel

import (
	"maps"
	"regexp"
)

// Message represents a message flowing through a Camel route.
// It carries a body of any type and a set of string-keyed headers.
type Message struct {
	Body    any            // Body carries the message payload.
	Headers map[string]any // Headers carry metadata as key-value pairs.
}

// NewMessage creates a new Message instance
func NewMessage() *Message {
	return &Message{
		Headers: make(map[string]any),
	}
}

// SetBody sets the message body.
func (m *Message) SetBody(body any) {
	m.Body = body
}

// GetBody returns the message body.
func (m *Message) GetBody() any {
	return m.Body
}

// GetBodyAsString returns the message body as a string.
func (m *Message) GetBodyAsString() (string, bool) {
	if m.Body == nil {
		return "", false
	}
	if str, ok := m.Body.(string); ok {
		return str, true
	}
	return "", false
}

// GetBodyAsInt returns the message body as an integer.
func (m *Message) GetBodyAsInt() (int, bool) {
	if m.Body == nil {
		return 0, false
	}
	if i, ok := m.Body.(int); ok {
		return i, true
	}
	if f, ok := m.Body.(float64); ok {
		return int(f), true
	}
	return 0, false
}

// GetBodyAsBool returns the message body as a boolean.
func (m *Message) GetBodyAsBool() (bool, bool) {
	if m.Body == nil {
		return false, false
	}
	if b, ok := m.Body.(bool); ok {
		return b, true
	}
	return false, false
}

// SetHeader sets a header value.
func (m *Message) SetHeader(key string, value any) {
	m.Headers[key] = value
}

// GetHeader returns a header value.
func (m *Message) GetHeader(key string) (any, bool) {
	value, exists := m.Headers[key]
	return value, exists
}

// GetHeaderAsString returns a header value as a string.
func (m *Message) GetHeaderAsString(key string) (string, bool) {
	if value, exists := m.Headers[key]; exists {
		if str, ok := value.(string); ok {
			return str, true
		}
	}
	return "", false
}

// GetHeaderAsInt returns a header value as an integer.
func (m *Message) GetHeaderAsInt(key string) (int, bool) {
	if value, exists := m.Headers[key]; exists {
		if i, ok := value.(int); ok {
			return i, true
		}
		if f, ok := value.(float64); ok {
			return int(f), true
		}
	}
	return 0, false
}

// GetHeaderAsBool returns a header value as a boolean.
func (m *Message) GetHeaderAsBool(key string) (bool, bool) {
	if value, exists := m.Headers[key]; exists {
		if b, ok := value.(bool); ok {
			return b, true
		}
	}
	return false, false
}

// GetHeaders returns all headers.
func (m *Message) GetHeaders() map[string]any {
	return m.Headers
}

// SetHeaders sets multiple headers.
func (m *Message) SetHeaders(headers map[string]any) {
	for key, value := range headers {
		m.Headers[key] = value
	}
}

// RemoveHeader removes a header.
func (m *Message) RemoveHeader(key string) {
	delete(m.Headers, key)
}

// RemoveHeaders removes headers matching the provided pattern,
// except those matching any of the provided exclusion patterns.
// The pattern supports the Apache Camel wildcard '*' (e.g. 'Camel*').
func (m *Message) RemoveHeaders(pattern string, excludePatterns ...string) {
	// Compile the main pattern
	mainRegex := patternToRegex(pattern)

	// Compile exclusion patterns
	var excludeRegexes []*regexp.Regexp
	for _, p := range excludePatterns {
		excludeRegexes = append(excludeRegexes, patternToRegex(p))
	}

	// Filter headers
	for key := range m.Headers {
		if mainRegex.MatchString(key) {
			shouldExclude := false
			for _, exRegex := range excludeRegexes {
				if exRegex.MatchString(key) {
					shouldExclude = true
					break
				}
			}
			
			if !shouldExclude {
				delete(m.Headers, key)
			}
		}
	}
}

// HasHeader returns true if the header exists.
func (m *Message) HasHeader(key string) bool {
	_, exists := m.Headers[key]
	return exists
}

// ClearHeaders removes all headers.
func (m *Message) ClearHeaders() {
	m.Headers = make(map[string]any)
}

// Copy creates a copy of the message.
func (m *Message) Copy() *Message {
	copy := NewMessage()
	copy.Body = m.Body

	// Copy headers
	maps.Copy(copy.Headers, m.Headers)

	return copy
}
