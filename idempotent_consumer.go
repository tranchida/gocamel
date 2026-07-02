package gocamel

import (
	"fmt"
)

// IdempotentConsumer implements the Idempotent Consumer EIP.
//
// The key is added to the repository eagerly (before downstream processing) so
// that concurrent duplicates are blocked. If the exchange later FAILS, the key
// is removed again via a Synchronization so the message can be redelivered —
// otherwise a transient downstream error would permanently mark the message as
// consumed and lose it.
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

// idempotentSynchronization removes the eagerly-added key when the exchange
// ultimately fails, restoring at-least-once semantics.
type idempotentSynchronization struct {
	repo IdempotentRepository
	key  string
}

func (s *idempotentSynchronization) OnComplete(exchange *Exchange) {}

func (s *idempotentSynchronization) OnFailure(exchange *Exchange) {
	if err := s.repo.Remove(exchange.Context, s.key); err != nil {
		logError("idempotent consumer: failed to remove key after exchange failure; "+
			"future redeliveries of this message will be treated as duplicates",
			"key", s.key, "error", err)
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

	// Roll the key back if the exchange fails downstream (fires on Done).
	exchange.AddSynchronization(&idempotentSynchronization{repo: p.repo, key: key})

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
