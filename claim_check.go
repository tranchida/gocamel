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

// Process implements the Processor interface.
func (p *ClaimCheck) Process(exchange *Exchange) error {
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
		var err error
		if p.operation == ClaimCheckPop {
			data, err = p.repo.Remove(ctx, resolvedKey)
		} else {
			data, err = p.repo.Get(ctx, resolvedKey)
		}

		if err != nil {
			return fmt.Errorf("claim check %s error: %w", p.operation, err)
		}
		if data == nil {
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
	b.container.AddProcessor(NewClaimCheck(operation, key, repo))
	return b
}
