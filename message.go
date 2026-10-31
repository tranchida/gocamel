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

func NewMessage() *Message {
	return &Message{
		Headers: make(map[string]any),
	}
}

func (m *Message) SetBody(body any) {
	m.Body = body
}

func (m *Message) GetBody() any {
	return m.Body
}

func (m *Message) GetBodyAsString() (string, bool) {
	if m.Body == nil {
		return "", false
	}
	if str, ok := m.Body.(string); ok {
		return str, true
	}
	return "", false
}

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

func (m *Message) GetBodyAsBool() (bool, bool) {
	if m.Body == nil {
		return false, false
	}
	if b, ok := m.Body.(bool); ok {
		return b, true
	}
	return false, false
}

func (m *Message) SetHeader(key string, value any) {
	m.Headers[key] = value
}

func (m *Message) GetHeader(key string) (any, bool) {
	value, exists := m.Headers[key]
	return value, exists
}

func (m *Message) GetHeaderAsString(key string) (string, bool) {
	if value, exists := m.Headers[key]; exists {
		if str, ok := value.(string); ok {
			return str, true
		}
	}
	return "", false
}

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

func (m *Message) GetHeaderAsBool(key string) (bool, bool) {
	if value, exists := m.Headers[key]; exists {
		if b, ok := value.(bool); ok {
			return b, true
		}
	}
	return false, false
}

func (m *Message) GetHeaders() map[string]any {
	out := make(map[string]any, len(m.Headers))
	maps.Copy(out, m.Headers)
	return out
}

func (m *Message) SetHeaders(headers map[string]any) {
	for key, value := range headers {
		m.Headers[key] = value
	}
}

func (m *Message) RemoveHeader(key string) {
	delete(m.Headers, key)
}

// RemoveHeaders removes headers matching the provided pattern,
// except those matching any of the provided exclusion patterns.
// The pattern supports the Apache Camel wildcard '*' (e.g. 'Camel*').
func (m *Message) RemoveHeaders(pattern string, excludePatterns ...string) {
	mainRegex := patternToRegex(pattern)

	var excludeRegexes []*regexp.Regexp
	for _, p := range excludePatterns {
		excludeRegexes = append(excludeRegexes, patternToRegex(p))
	}

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

func (m *Message) HasHeader(key string) bool {
	_, exists := m.Headers[key]
	return exists
}

func (m *Message) ClearHeaders() {
	m.Headers = make(map[string]any)
}

func (m *Message) Copy() *Message {
	cloned := NewMessage()
	cloned.Body = m.Body
	maps.Copy(cloned.Headers, m.Headers)

	return cloned
}

// DeepCopy returns a copy of the message whose body and header values are
// recursively duplicated for the common mutable container types ([]byte,
// []any, map[string]any, []string). Values of other types are shared by
// reference — treat them as immutable when the copy crosses a goroutine
// boundary (WireTap, parallel Multicast).
func (m *Message) DeepCopy() *Message {
	cloned := NewMessage()
	cloned.Body = deepCopyValue(m.Body)
	for k, v := range m.Headers {
		cloned.Headers[k] = deepCopyValue(v)
	}
	return cloned
}

// deepCopyValue duplicates the common mutable container types used in message
// bodies and headers. Scalars are returned as-is; unknown types are shared by
// reference.
func deepCopyValue(v any) any {
	switch val := v.(type) {
	case []byte:
		if val == nil {
			return []byte(nil)
		}
		cp := make([]byte, len(val))
		copy(cp, val)
		return cp
	case []any:
		if val == nil {
			return []any(nil)
		}
		cp := make([]any, len(val))
		for i, item := range val {
			cp[i] = deepCopyValue(item)
		}
		return cp
	case map[string]any:
		if val == nil {
			return map[string]any(nil)
		}
		cp := make(map[string]any, len(val))
		for k, item := range val {
			cp[k] = deepCopyValue(item)
		}
		return cp
	case []map[string]any:
		if val == nil {
			return []map[string]any(nil)
		}
		cp := make([]map[string]any, len(val))
		for i, item := range val {
			if item != nil {
				cp[i] = deepCopyValue(item).(map[string]any)
			}
		}
		return cp
	case map[string]string:
		if val == nil {
			return map[string]string(nil)
		}
		cp := make(map[string]string, len(val))
		maps.Copy(cp, val)
		return cp
	case []map[string]string:
		if val == nil {
			return []map[string]string(nil)
		}
		cp := make([]map[string]string, len(val))
		for i, item := range val {
			if item != nil {
				itemCopy := make(map[string]string, len(item))
				maps.Copy(itemCopy, item)
				cp[i] = itemCopy
			}
		}
		return cp
	case []string:
		if val == nil {
			return []string(nil)
		}
		cp := make([]string, len(val))
		copy(cp, val)
		return cp
	case [][]string:
		if val == nil {
			return [][]string(nil)
		}
		cp := make([][]string, len(val))
		for i, row := range val {
			if row != nil {
				rowCopy := make([]string, len(row))
				copy(rowCopy, row)
				cp[i] = rowCopy
			}
		}
		return cp
	case map[string][]byte:
		if val == nil {
			return map[string][]byte(nil)
		}
		cp := make(map[string][]byte, len(val))
		for k, b := range val {
			if b != nil {
				bCopy := make([]byte, len(b))
				copy(bCopy, b)
				cp[k] = bCopy
			}
		}
		return cp
	default:
		return v
	}
}
