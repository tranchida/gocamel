package gocamel

import (
	"context"
	"fmt"
)

// ClaimCheckOperation defines the operations for the Claim Check EIP.
type ClaimCheckOperation string

const (
	ClaimCheckPush    ClaimCheckOperation = "Push"    // Stores and replaces body with key
	ClaimCheckPop     ClaimCheckOperation = "Pop"     // Retrieves and restores body, removes from repo
	ClaimCheckGet     ClaimCheckOperation = "Get"     // Retrieves and restores body, keeps in repo
	ClaimCheckDiscard ClaimCheckOperation = "Discard" // Removes from repo
)

// ClaimCheck implements the Claim Check EIP.
type ClaimCheck struct {
	operation ClaimCheckOperation
	key       string // Static key or simple expression
	repo      ClaimCheckRepository
}

// NewClaimCheck creates a new ClaimCheck processor.
func NewClaimCheck(operation ClaimCheckOperation, key string, repo ClaimCheckRepository) *ClaimCheck {
	return &ClaimCheck{
		operation: operation,
		key:       key,
		repo:      repo,
	}
}

// Validate implements the Validator interface for build-time validation.
func (p *ClaimCheck) Validate() error {
	if p == nil {
		return fmt.Errorf("claim check: processor is nil")
	}
	if isNilValue(p.repo) {
		return fmt.Errorf("claim check: repository must not be nil")
	}
	switch p.operation {
	case ClaimCheckPush, ClaimCheckPop, ClaimCheckGet, ClaimCheckDiscard:
		return nil
	default:
		return fmt.Errorf("unsupported claim check operation: %s", p.operation)
	}
}

// Process implements the Processor interface.
func (p *ClaimCheck) Process(exchange *Exchange) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if exchange == nil {
		return fmt.Errorf("claim check: exchange must not be nil")
	}

	// A nil exchange context must not be passed to the repository: user-supplied
	// repositories may call ctx.Done()/ctx.Err()/ctx.Value() and panic on nil.
	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}

	resolvedKey := p.key
	if resolvedKey != "" {
		resolvedKey = Interpolate(resolvedKey, exchange)
	}

	switch p.operation {
	case ClaimCheckPush:
		body := exchange.GetIn().GetBody()
		var key string
		var err error
		if resolvedKey != "" {
			err = p.repo.Set(ctx, resolvedKey, body)
			key = resolvedKey
		} else {
			key, err = p.repo.Add(ctx, body)
		}
		if err != nil {
			return fmt.Errorf("claim check push error: %w", err)
		}
		exchange.GetIn().SetBody(key)
		exchange.SetHeader("CamelClaimCheckKey", key)

	case ClaimCheckPop, ClaimCheckGet:
		if resolvedKey == "" {
			// Try to get from header
			if k, ok := exchange.GetIn().GetHeader("CamelClaimCheckKey"); ok {
				resolvedKey = fmt.Sprintf("%v", k)
			} else {
				return fmt.Errorf("claim check %s error: no key provided and header CamelClaimCheckKey missing", p.operation)
			}
		}

		var data any
		var exists bool
		var err error
		if lookupRepo, ok := p.repo.(ClaimCheckLookupRepository); ok {
			if p.operation == ClaimCheckPop {
				data, exists, err = lookupRepo.Pop(ctx, resolvedKey)
			} else {
				data, exists, err = lookupRepo.Lookup(ctx, resolvedKey)
			}
		} else {
			if p.operation == ClaimCheckPop {
				data, err = p.repo.Remove(ctx, resolvedKey)
			} else {
				data, err = p.repo.Get(ctx, resolvedKey)
			}
			exists = data != nil
		}

		if err != nil {
			return fmt.Errorf("claim check %s error: %w", p.operation, err)
		}
		if !exists {
			return fmt.Errorf("claim check %s error: data not found for key %s", p.operation, resolvedKey)
		}
		exchange.GetIn().SetBody(data)

	case ClaimCheckDiscard:
		if resolvedKey == "" {
			if k, ok := exchange.GetIn().GetHeader("CamelClaimCheckKey"); ok {
				resolvedKey = fmt.Sprintf("%v", k)
			} else {
				return fmt.Errorf("claim check discard error: no key provided")
			}
		}
		_, err := p.repo.Remove(ctx, resolvedKey)
		if err != nil {
			return fmt.Errorf("claim check discard error: %w", err)
		}

	default:
		return fmt.Errorf("unsupported claim check operation: %s", p.operation)
	}

	return nil
}

// ClaimCheck adds a Claim Check EIP step.
func (b *RouteBuilder) ClaimCheck(operation ClaimCheckOperation, key string, repo ClaimCheckRepository) *RouteBuilder {
	cc := NewClaimCheck(operation, key, repo)
	if err := cc.Validate(); err != nil {
		b.recordErr(err)
		return b
	}
	b.container.AddProcessor(cc)
	return b
}
