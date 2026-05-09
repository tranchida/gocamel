package gocamel

import (
	"fmt"
)

// IdempotentConsumer implements the Idempotent Consumer EIP.
type IdempotentConsumer struct {
	expression func(*Exchange) (string, error)
	repo       IdempotentRepository
}

// NewIdempotentConsumer creates a new IdempotentConsumer processor.
func NewIdempotentConsumer(expression func(*Exchange) (string, error), repo IdempotentRepository) *IdempotentConsumer {
	return &IdempotentConsumer{
		expression: expression,
		repo:       repo,
	}
}

// Process implements the Processor interface.
func (p *IdempotentConsumer) Process(exchange *Exchange) error {
	key, err := p.expression(exchange)
	if err != nil {
		return fmt.Errorf("idempotent consumer: failed to evaluate expression: %w", err)
	}

	added, err := p.repo.Add(exchange.Context, key)
	if err != nil {
		return fmt.Errorf("idempotent consumer: repository error: %w", err)
	}

	if !added {
		// Duplicate message, stop routing
		return ErrStopRouting
	}

	return nil
}

// NewSimpleIdempotentConsumer creates a new IdempotentConsumer using a Simple Language expression.
func NewSimpleIdempotentConsumer(expression string, repo IdempotentRepository) (*IdempotentConsumer, error) {
	template, err := ParseSimpleTemplate(expression)
	if err != nil {
		return nil, fmt.Errorf("idempotent consumer: failed to parse simple expression: %w", err)
	}

	return NewIdempotentConsumer(func(e *Exchange) (string, error) {
		return template.EvaluateAsString(e)
	}, repo), nil
}
