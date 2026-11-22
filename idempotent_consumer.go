package gocamel

import (
	"context"
	"fmt"
	"reflect"
	"time"
)

const camelIdempotentClaimsProperty = "CamelIdempotentClaims"

type idempotentClaim struct {
	exchangeID string
	repo       IdempotentRepository
	key        string
}

func sameIdempotentRepo(a, b IdempotentRepository) bool {
	ta, tb := reflect.TypeOf(a), reflect.TypeOf(b)
	if ta != tb {
		return false
	}
	if ta == nil {
		return true
	}
	if ta.Comparable() {
		return a == b
	}
	return reflect.DeepEqual(a, b)
}

func hasActiveIdempotentClaim(exchange *Exchange, repo IdempotentRepository, key string) bool {
	existing, ok := exchange.GetProperty(camelIdempotentClaimsProperty)
	if !ok {
		return false
	}
	claims, ok := existing.([]idempotentClaim)
	if !ok {
		return false
	}
	for _, c := range claims {
		if c.exchangeID == exchange.ID && c.key == key && sameIdempotentRepo(c.repo, repo) {
			return true
		}
	}
	return false
}

func addActiveIdempotentClaim(exchange *Exchange, repo IdempotentRepository, key string) {
	existing, _ := exchange.GetProperty(camelIdempotentClaimsProperty)
	claims, _ := existing.([]idempotentClaim)
	// Copy-on-write slice so Exchange.Copy/DeepCopy never races over a shared backing array.
	next := make([]idempotentClaim, len(claims)+1)
	copy(next, claims)
	next[len(claims)] = idempotentClaim{
		exchangeID: exchange.ID,
		repo:       repo,
		key:        key,
	}
	exchange.SetProperty(camelIdempotentClaimsProperty, next)
}

func removeActiveIdempotentClaim(exchange *Exchange, exchangeID string, repo IdempotentRepository, key string) {
	if exchange == nil {
		return
	}
	existing, ok := exchange.GetProperty(camelIdempotentClaimsProperty)
	if !ok {
		return
	}
	claims, ok := existing.([]idempotentClaim)
	if !ok || len(claims) == 0 {
		return
	}
	var kept []idempotentClaim
	removed := false
	for _, c := range claims {
		if c.exchangeID == exchangeID && c.key == key && sameIdempotentRepo(c.repo, repo) {
			removed = true
			continue
		}
		kept = append(kept, c)
	}
	if !removed {
		return
	}
	if len(kept) == 0 {
		exchange.RemoveProperty(camelIdempotentClaimsProperty)
		return
	}
	exchange.SetProperty(camelIdempotentClaimsProperty, kept)
}

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

// idempotentRollbackTimeout bounds the repository call that removes the key of
// a failed exchange.
const idempotentRollbackTimeout = 30 * time.Second

// idempotentSynchronization removes the eagerly-added key when the exchange
// ultimately fails, restoring at-least-once semantics.
type idempotentSynchronization struct {
	repo       IdempotentRepository
	key        string
	exchangeID string
}

func (s *idempotentSynchronization) OnComplete(exchange *Exchange) {
	removeActiveIdempotentClaim(exchange, s.exchangeID, s.repo, s.key)
}

func (s *idempotentSynchronization) OnFailure(exchange *Exchange) {
	removeActiveIdempotentClaim(exchange, s.exchangeID, s.repo, s.key)
	var ctx context.Context
	if exchange != nil {
		ctx = exchange.Context
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// The exchange often failed because its context was cancelled (client
	// gone, shutdown). Rolling the key back with that context would fail on a
	// context-aware repository (Redis, SQL) and leave the key behind, so the
	// redelivered message would be dropped as a duplicate. Keep the context
	// values, drop its cancellation, and bound the call instead.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), idempotentRollbackTimeout)
	defer cancel()
	if err := s.repo.Remove(ctx, s.key); err != nil {
		LogError("idempotent consumer: failed to remove key after exchange failure; "+
			"future redeliveries of this message will be treated as duplicates",
			"key", s.key, "error", err)
	}
}

// Validate implements the Validator interface for build-time validation.
func (p *IdempotentConsumer) Validate() error {
	if p == nil {
		return fmt.Errorf("idempotent consumer: processor is nil")
	}
	if p.expression == nil {
		return fmt.Errorf("idempotent consumer: expression must not be nil")
	}
	if isNilValue(p.repo) {
		return fmt.Errorf("idempotent consumer: repository must not be nil")
	}
	return nil
}

// Process implements the Processor interface.
func (p *IdempotentConsumer) Process(exchange *Exchange) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if exchange == nil {
		return fmt.Errorf("idempotent consumer: exchange must not be nil")
	}
	key, err := p.expression(exchange)
	if err != nil {
		return fmt.Errorf("idempotent consumer: failed to evaluate expression: %w", err)
	}

	// Intra-exchange redelivery (e.g. OnException or DeadLetterChannel retrying
	// a direct: sub-route) re-enters with the same exchange ID while the
	// synchronization has not yet fired; skip re-adding so it is not dropped.
	if hasActiveIdempotentClaim(exchange, p.repo, key) {
		return nil
	}

	// A nil exchange context must not be passed to the repository: user-supplied
	// repositories may call ctx.Done()/ctx.Err()/ctx.Value() and panic on nil.
	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}

	added, err := p.repo.Add(ctx, key)
	if err != nil {
		return fmt.Errorf("idempotent consumer: repository error: %w", err)
	}

	if !added {
		// Duplicate message, stop routing
		return ErrStopRouting
	}

	addActiveIdempotentClaim(exchange, p.repo, key)
	// Roll the key back if the exchange fails downstream (fires on Done).
	exchange.AddSynchronization(&idempotentSynchronization{
		repo:       p.repo,
		key:        key,
		exchangeID: exchange.ID,
	})

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
