package gocamel

import (
	"maps"
	"regexp"
)

// Message represents a message in a route
type Message struct {
	Body    any
	Headers map[string]any
}

// NewMessage creates a new Message instance
func NewMessage() *Message {
	return &Message{
		Headers: make(map[string]any),
	}
}

// SetBody définit le corps du message
func (m *Message) SetBody(body any) {
	m.Body = body
}

// GetBody récupère le corps du message
func (m *Message) GetBody() any {
	return m.Body
}

// GetBodyAsString récupère le corps du message sous forme de chaîne
func (m *Message) GetBodyAsString() (string, bool) {
	if m.Body == nil {
		return "", false
	}
	if str, ok := m.Body.(string); ok {
		return str, true
	}
	return "", false
}

// GetBodyAsInt récupère le corps du message sous forme d'entier
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

// GetBodyAsBool récupère le corps du message sous forme de booléen
func (m *Message) GetBodyAsBool() (bool, bool) {
	if m.Body == nil {
		return false, false
	}
	if b, ok := m.Body.(bool); ok {
		return b, true
	}
	return false, false
}

// SetHeader définit un en-tête
func (m *Message) SetHeader(key string, value any) {
	m.Headers[key] = value
}

// GetHeader récupère un en-tête
func (m *Message) GetHeader(key string) (any, bool) {
	value, exists := m.Headers[key]
	return value, exists
}

// GetHeaderAsString récupère un en-tête sous forme de chaîne
func (m *Message) GetHeaderAsString(key string) (string, bool) {
	if value, exists := m.Headers[key]; exists {
		if str, ok := value.(string); ok {
			return str, true
		}
	}
	return "", false
}

// GetHeaderAsInt récupère un en-tête sous forme d'entier
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

// GetHeaderAsBool récupère un en-tête sous forme de booléen
func (m *Message) GetHeaderAsBool(key string) (bool, bool) {
	if value, exists := m.Headers[key]; exists {
		if b, ok := value.(bool); ok {
			return b, true
		}
	}
	return false, false
}

// GetHeaders récupère tous les en-têtes
func (m *Message) GetHeaders() map[string]any {
	return m.Headers
}

// SetHeaders définit plusieurs en-têtes
func (m *Message) SetHeaders(headers map[string]any) {
	for key, value := range headers {
		m.Headers[key] = value
	}
}

// RemoveHeader supprime un en-tête
func (m *Message) RemoveHeader(key string) {
	delete(m.Headers, key)
}

// RemoveHeaders supprime les en-têtes correspondants au pattern fourni, 
// sauf ceux qui correspondent aux patterns d'exclusion fournis.
// Le pattern supporte les jokers Apache Camel '*' (ex: 'Camel*').
func (m *Message) RemoveHeaders(pattern string, excludePatterns ...string) {
	// Compilation du pattern principal
	mainRegex := patternToRegex(pattern)
	
	// Compilation des patterns d'exclusion
	var excludeRegexes []*regexp.Regexp
	for _, p := range excludePatterns {
		excludeRegexes = append(excludeRegexes, patternToRegex(p))
	}
	
	// Filtrage des headers
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

// HasHeader vérifie si un en-tête existe
func (m *Message) HasHeader(key string) bool {
	_, exists := m.Headers[key]
	return exists
}

// ClearHeaders supprime tous les en-têtes
func (m *Message) ClearHeaders() {
	m.Headers = make(map[string]any)
}

// Copy crée une copie du message
func (m *Message) Copy() *Message {
	copy := NewMessage()
	copy.Body = m.Body

	// Copie des en-têtes
	maps.Copy(copy.Headers, m.Headers)

	return copy
}
