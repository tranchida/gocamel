package gocamel

import "maps"

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
