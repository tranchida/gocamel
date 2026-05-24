package gocamel

import (
	"time"
)

// Message History property constant
const CamelMessageHistory = "CamelMessageHistory"

// MessageHistoryEntry records the path of a message across route nodes.
type MessageHistoryEntry struct {
	NodeID    string
	RouteID   string
	Timestamp time.Time
	Elapsed   time.Duration
}

// GetMessageHistory retrieves the message history list from the exchange properties.
// Returns nil if no history is recorded.
func GetMessageHistory(exchange *Exchange) []MessageHistoryEntry {
	if exchange == nil || exchange.Properties == nil {
		return nil
	}
	val, ok := exchange.Properties[CamelMessageHistory]
	if !ok {
		return nil
	}
	history, ok := val.([]MessageHistoryEntry)
	if !ok {
		return nil
	}
	return history
}
