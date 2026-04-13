package gocamel

import (
	"maps"
	"regexp"
	"strings"
)

// Message représente un message dans une route
type Message struct {
	Body    any
	Headers map[string]any
}

// NewMessage crée une nouvelle instance de Message
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

// SetHeader définit un en-tête
func (m *Message) SetHeader(key string, value any) {
	m.Headers[key] = value
}

// GetHeader récupère un en-tête
func (m *Message) GetHeader(key string) (any, bool) {
	value, exists := m.Headers[key]
	return value, exists
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

// patternToRegex convertit un pattern avec '*' en Regexp
func patternToRegex(pattern string) *regexp.Regexp {
	// Échapper les caractères spéciaux de regex, puis remplacer '*' par '.*'
	quoted := regexp.QuoteMeta(pattern)
	regexStr := "^" + strings.ReplaceAll(quoted, "\\*", ".*") + "$"
	re, _ := regexp.Compile(regexStr)
	return re
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
