package gocamel

import (
	"fmt"
	"strings"
)

// This file implements the Recipient List EIP sub-DSL.
//
// Unlike SplitDefinition and MulticastDefinition, RecipientListDefinition does
// NOT embed *RouteBuilder: its only verbs are AggregationStrategy() and
// ParallelProcessing(), so there is nothing to type-preserve. The recipient
// list processor is fully configured at construction time.

// RecipientList adds a Recipient List EIP processor that sends the exchange
// to a list of recipients resolved dynamically at runtime.
//
// The expression can be:
//   - A string of comma-separated URIs (e.g. "direct:a,direct:b")
//   - A Simple Language expression (e.g. "${header.recipients}")
//   - A Go function returning []string
//
// Example:
//
//	// Static comma-separated list
//	route, _ := ctx.CreateRouteBuilder().
//	    From("direct:start").
//	    RecipientList("direct:a,direct:b").
//	    Build()
//
//	// Simple Language expression
//	route, _ := ctx.CreateRouteBuilder().
//	    From("direct:start").
//	    RecipientList("${header.recipientList}").
//	    Build()
//
//	// Go function
//	route, _ := ctx.CreateRouteBuilder().
//	    From("direct:start").
//	    RecipientList(func(e *Exchange) ([]string, error) {
//	        return []string{"direct:a", "direct:b"}, nil
//	    }).
//	    Build()
func (b *RouteBuilder) RecipientList(expression any) *RecipientListDefinition {
	rl := NewRecipientList(b.context)

	switch expr := expression.(type) {
	case string:
		// Heuristic: if it contains "${", treat it as a Simple Language expression.
		if strings.Contains(expr, "${") {
			rl.SetSimpleExpression(expr)
		} else {
			rl.SetStaticURIs(expr)
		}
	case func(*Exchange) ([]string, error):
		rl.SetURIFunc(expr)
	default:
		b.err = fmt.Errorf("unsupported RecipientList expression type: %T", expression)
		return &RecipientListDefinition{
			parent:        b,
			recipientList: rl,
		}
	}

	b.container.AddProcessor(rl)

	return &RecipientListDefinition{
		parent:        b,
		recipientList: rl,
	}
}

// RecipientListDefinition is a fluent sub-DSL for configuring the Recipient List EIP.
// Supports .ParallelProcessing(), .AggregationStrategy(), and .End() to return
// to the parent builder.
type RecipientListDefinition struct {
	parent        *RouteBuilder
	recipientList *RecipientList
}

// AggregationStrategy sets the aggregation strategy for the recipient list.
func (d *RecipientListDefinition) AggregationStrategy(strategy AggregationStrategy) *RecipientListDefinition {
	d.recipientList.SetAggregationStrategy(strategy)
	return d
}

// ParallelProcessing enables parallel processing for the recipient list.
func (d *RecipientListDefinition) ParallelProcessing() *RecipientListDefinition {
	d.recipientList.SetParallelProcessing(true)
	return d
}

// End terminates the RecipientList block and returns to the parent builder.
func (d *RecipientListDefinition) End() *RouteBuilder {
	return d.parent
}
