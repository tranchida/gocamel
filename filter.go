package gocamel

import "fmt"

// MessageFilter implements the Message Filter EIP.
// It evaluates a predicate on each exchange; if the predicate returns false
// the exchange is silently stopped (ErrStopRouting) without being treated as
// a failure. If true, the exchange continues through the route unchanged.
//
// The predicate can be:
//   - A Go function: func(*Exchange) (bool, error)
//   - A Simple Language expression string (e.g. "${header.type} == 'order'")
type MessageFilter struct {
	predicate func(*Exchange) (bool, error)
}

// NewMessageFilter creates a MessageFilter with the given Go predicate function.
func NewMessageFilter(predicate func(*Exchange) (bool, error)) *MessageFilter {
	return &MessageFilter{predicate: predicate}
}

// NewSimpleMessageFilter creates a MessageFilter whose predicate is a Simple Language expression.
func NewSimpleMessageFilter(expression string) (*MessageFilter, error) {
	tmpl, err := ParseSimpleTemplate(expression)
	if err != nil {
		return nil, fmt.Errorf("filter: invalid simple expression %q: %w", expression, err)
	}
	return &MessageFilter{
		predicate: func(exchange *Exchange) (bool, error) {
			return tmpl.EvaluateAsBool(exchange)
		},
	}, nil
}

// Process implements the Processor interface.
func (f *MessageFilter) Process(exchange *Exchange) error {
	pass, err := f.predicate(exchange)
	if err != nil {
		return fmt.Errorf("filter predicate error: %w", err)
	}
	if !pass {
		return ErrStopRouting
	}
	return nil
}
