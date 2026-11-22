package gocamel

import (
	"fmt"
)

// ContentEnricher implements the Content Enricher EIP.
// It calls an external endpoint, retrieves a response exchange, and merges
// it into the current exchange using an AggregationStrategy.
//
// If no AggregationStrategy is provided, the enrichment response body
// replaces the current exchange body.
//
// The enrichment endpoint receives a copy of the original exchange so that
// the original is never modified before the merge.
//
// Example DSL usage:
//
//	// With strategy
//	route, _ := ctx.CreateRouteBuilder().
//	    From("direct:start").
//	    Enrich("http://api.example.com/data", &MergeStrategy{}).
//	    To("direct:enriched").
//	    Build()
//
//	// Without strategy (replaces body)
//	route, _ := ctx.CreateRouteBuilder().
//	    From("direct:start").
//	    Enrich("direct:lookup", nil).
//	    To("direct:next").
//	    Build()
type ContentEnricher struct {
	context  *CamelContext
	uri      string
	strategy AggregationStrategy
}

// NewContentEnricher creates a new ContentEnricher that calls the given URI
// and merges the response via strategy. Pass nil strategy to replace body only.
func NewContentEnricher(camelContext *CamelContext, uri string, strategy AggregationStrategy) *ContentEnricher {
	return &ContentEnricher{
		context:  camelContext,
		uri:      uri,
		strategy: strategy,
	}
}

// Process implements the Processor interface.
func (e *ContentEnricher) Process(exchange *Exchange) (resultErr error) {
	if e == nil || e.context == nil {
		return fmt.Errorf("enrich: CamelContext must not be nil")
	}
	// Send a copy so the enrichment endpoint cannot corrupt the original.
	enrichExchange := exchange.Copy()
	enrichExchange.Out = NewMessage()

	producer, release, err := e.context.acquireProducer(e.uri)
	if err != nil {
		_ = CompleteExchange(enrichExchange, err)
		return fmt.Errorf("enrich: failed to get producer for %q: %w", RedactURI(e.uri), err)
	}
	defer release()
	defer func() {
		_ = CompleteExchange(enrichExchange, resultErr)
	}()

	if err := safeProcess(func() error { return producer.Send(enrichExchange) }); err != nil {
		resultErr = fmt.Errorf("enrich: failed to send to %q: %w", RedactURI(e.uri), err)
		return resultErr
	}

	// The enrichment response is in enrichExchange.Out (or In if Out is empty).
	responseExchange := enrichExchange.Copy()
	responseExchange.PropagateOutToIn()

	if e.strategy != nil {
		var merged *Exchange
		if err := safeProcess(func() error {
			merged = e.strategy.Aggregate(exchange, responseExchange)
			return nil
		}); err != nil {
			resultErr = err
			return resultErr
		}
		if merged != nil {
			exchange.In = merged.In
			exchange.Out = merged.Out
			exchange.Properties = merged.Properties
		}
	} else {
		// Default: replace body with enrichment response.
		if body := responseExchange.GetIn().GetBody(); body != nil {
			exchange.GetIn().SetBody(body)
		}
	}

	return nil
}
