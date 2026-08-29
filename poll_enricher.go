package gocamel

import (
	"context"
	"fmt"
	"time"
)

// PollEnricher implements the PollEnrich EIP (Content Enricher via polling consumer).
// It polls a message from an external consumer endpoint on demand, with an optional timeout,
// and merges the polled message into the current exchange using an AggregationStrategy.
//
// If no AggregationStrategy is provided, the polled exchange body replaces the current exchange body.
//
// Example DSL usage:
//
//	// Poll with timeout and strategy
//	route, _ := ctx.CreateRouteBuilder().
//	    From("direct:start").
//	    PollEnrich("seda:orders", &MergeStrategy{}, 2*time.Second).
//	    To("direct:next").
//	    Build()
//
//	// Poll without strategy (replaces body)
//	route, _ := ctx.CreateRouteBuilder().
//	    From("direct:start").
//	    PollEnrich("chan:updates", nil, 500*time.Millisecond).
//	    To("direct:next").
//	    Build()
type PollEnricher struct {
	context  *CamelContext
	uri      string
	strategy AggregationStrategy
	timeout  time.Duration
}

// NewPollEnricher creates a new PollEnricher instance.
func NewPollEnricher(camelContext *CamelContext, uri string, strategy AggregationStrategy, timeout time.Duration) *PollEnricher {
	return &PollEnricher{
		context:  camelContext,
		uri:      uri,
		strategy: strategy,
		timeout:  timeout,
	}
}

// Process implements Processor.
func (p *PollEnricher) Process(exchange *Exchange) error {
	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}

	targetURI := p.uri
	if stringsContainsPlaceholder(targetURI) {
		template, err := ParseSimpleTemplate(targetURI)
		if err != nil {
			return fmt.Errorf("pollEnrich: failed to parse URI expression: %w", err)
		}
		resolved, err := template.EvaluateAsString(exchange)
		if err != nil {
			return fmt.Errorf("pollEnrich: failed to evaluate URI expression: %w", err)
		}
		targetURI = resolved
	}

	endpoint, err := p.context.CreateEndpoint(targetURI)
	if err != nil {
		return fmt.Errorf("pollEnrich: failed to create endpoint %q: %w", targetURI, err)
	}

	polledChan := make(chan *Exchange, 1)

	// Create temporary consumer that captures a single message
	consumer, err := endpoint.CreateConsumer(ProcessorFunc(func(polled *Exchange) error {
		select {
		case polledChan <- polled.Copy():
		default:
		}
		return nil
	}))
	if err != nil {
		return fmt.Errorf("pollEnrich: failed to create consumer for %q: %w", targetURI, err)
	}

	consumerCtx, cancelConsumer := context.WithCancel(ctx)
	defer cancelConsumer()

	if err := consumer.Start(consumerCtx); err != nil {
		return fmt.Errorf("pollEnrich: failed to start consumer for %q: %w", targetURI, err)
	}
	defer consumer.Stop()

	var polledExchange *Exchange

	if p.timeout < 0 {
		// Block indefinitely until message or context cancellation
		select {
		case polledExchange = <-polledChan:
		case <-ctx.Done():
			return ctx.Err()
		}
	} else if p.timeout == 0 {
		// Non-blocking poll: check channel or yield shortly
		select {
		case polledExchange = <-polledChan:
		case <-time.After(10 * time.Millisecond):
			// No message available immediately
		case <-ctx.Done():
			return ctx.Err()
		}
	} else {
		// Wait up to timeout
		select {
		case polledExchange = <-polledChan:
		case <-time.After(p.timeout):
			// Timeout reached; polledExchange remains nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if polledExchange != nil {
		polledExchange.PropagateOutToIn()
		if p.strategy != nil {
			merged := p.strategy.Aggregate(exchange, polledExchange)
			if merged != nil {
				exchange.In = merged.In
				exchange.Out = merged.Out
				exchange.Properties = merged.Properties
			}
		} else {
			// Default: replace body with polled message body
			if body := polledExchange.GetIn().GetBody(); body != nil {
				exchange.GetIn().SetBody(body)
			}
		}
	}

	return nil
}

func stringsContainsPlaceholder(s string) bool {
	return placeholderRegex.MatchString(s)
}

// PollEnrich adds a PollEnrich step to the route with an optional AggregationStrategy and timeout.
// Default timeout is 500ms if not specified. Pass timeout < 0 to block indefinitely.
func (b *RouteBuilder) PollEnrich(uri string, strategy AggregationStrategy, timeout ...time.Duration) *RouteBuilder {
	t := 500 * time.Millisecond
	if len(timeout) > 0 {
		t = timeout[0]
	}
	b.container.AddProcessor(NewPollEnricher(b.context, uri, strategy, t))
	return b
}

// PollEnrichWithTimeout adds a PollEnrich step replacing body with the polled message body.
func (b *RouteBuilder) PollEnrichWithTimeout(uri string, timeout time.Duration) *RouteBuilder {
	return b.PollEnrich(uri, nil, timeout)
}
