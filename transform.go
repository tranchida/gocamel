package gocamel

import (
	"fmt"
)

// Transformer implements the Transform EIP.
// It modifies the content of the message body using a dynamic function
// or a Simple Language expression.
type Transformer struct {
	transformFn func(*Exchange) (any, error)
}

// NewTransformer creates a new Transformer with a dynamic Go function.
func NewTransformer(fn func(*Exchange) (any, error)) *Transformer {
	return &Transformer{
		transformFn: fn,
	}
}

// NewSimpleTransformer creates a new Transformer with a Simple expression.
func NewSimpleTransformer(simpleExpression string) (*Transformer, error) {
	template, err := ParseSimpleTemplate(simpleExpression)
	if err != nil {
		return nil, fmt.Errorf("failed to parse transform simple expression: %w", err)
	}

	return &Transformer{
		transformFn: func(e *Exchange) (any, error) {
			return template.Evaluate(e)
		},
	}, nil
}

// Process executes the transformation on the exchange, setting the new body on the IN message.
func (t *Transformer) Process(exchange *Exchange) error {
	if t.transformFn == nil {
		return fmt.Errorf("transform: transform function is nil")
	}

	newBody, err := t.transformFn(exchange)
	if err != nil {
		return fmt.Errorf("transform: execution failed: %w", err)
	}

	exchange.In.SetBody(newBody)
	return nil
}
