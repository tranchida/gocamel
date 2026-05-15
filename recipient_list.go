package gocamel

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// RecipientList is a Processor that implements the Recipient List EIP.
// It sends a copy of the exchange to a list of recipients (URIs) resolved
// dynamically at runtime via a static string, a Simple Language expression,
// or a Go function.
type RecipientList struct {
	// StaticURIs is a comma-separated string of URIs (e.g. "direct:a,direct:b").
	StaticURIs string
	// SimpleExpression is a Simple Language expression that resolves to a
	// comma-separated list of URIs at runtime.
	SimpleExpression string
	// URIFunc is a Go function that returns recipient URIs dynamically.
	URIFunc func(*Exchange) ([]string, error)

	// ParallelProcessing enables sending to all recipients concurrently.
	ParallelProcessing bool
	// AggregationStrategy is an optional strategy for aggregating results.
	AggregationStrategy AggregationStrategy

	context *CamelContext
}

// NewRecipientList creates a new RecipientList instance.
func NewRecipientList(context *CamelContext) *RecipientList {
	return &RecipientList{
		context: context,
	}
}

// SetStaticURIs sets a comma-separated list of recipient URIs.
func (r *RecipientList) SetStaticURIs(uris string) *RecipientList {
	r.StaticURIs = uris
	return r
}

// SetSimpleExpression sets a Simple Language expression for dynamic URI resolution.
func (r *RecipientList) SetSimpleExpression(expression string) *RecipientList {
	r.SimpleExpression = expression
	return r
}

// SetURIFunc sets a Go function for dynamic URI resolution.
func (r *RecipientList) SetURIFunc(f func(*Exchange) ([]string, error)) *RecipientList {
	r.URIFunc = f
	return r
}

// SetParallelProcessing enables or disables parallel processing.
func (r *RecipientList) SetParallelProcessing(parallel bool) *RecipientList {
	r.ParallelProcessing = parallel
	return r
}

// SetAggregationStrategy sets the aggregation strategy for collecting results.
func (r *RecipientList) SetAggregationStrategy(strategy AggregationStrategy) *RecipientList {
	r.AggregationStrategy = strategy
	return r
}

// resolveURIs resolves the recipient URIs at runtime from the configured expression.
func (r *RecipientList) resolveURIs(exchange *Exchange) ([]string, error) {
	// Priority: Go function > Simple expression > static string
	if r.URIFunc != nil {
		uris, err := r.URIFunc(exchange)
		if err != nil {
			return nil, fmt.Errorf("recipient list URI function error: %w", err)
		}
		slog.Debug("recipient list resolved URIs via function",
			"count", len(uris),
			"uris", uris,
		)
		return uris, nil
	}

	if r.SimpleExpression != "" {
		// Use Simple Language to evaluate the expression
		resolved, err := evaluateSimpleExpression(r.SimpleExpression, exchange)
		if err != nil {
			return nil, fmt.Errorf("recipient list simple expression error: %w", err)
		}
		// Also interpolate ${header.x} / ${property.x} style placeholders
		resolved = Interpolate(resolved, exchange)
		uris := splitAndTrim(resolved)
		slog.Debug("recipient list resolved URIs via simple expression",
			"expression", r.SimpleExpression,
			"count", len(uris),
			"uris", uris,
		)
		return uris, nil
	}

	if r.StaticURIs != "" {
		// Interpolate any ${...} placeholders in the static string
		resolved := Interpolate(r.StaticURIs, exchange)
		uris := splitAndTrim(resolved)
		slog.Debug("recipient list resolved URIs via static string",
			"count", len(uris),
			"uris", uris,
		)
		return uris, nil
	}

	return nil, nil
}

// splitAndTrim splits a comma-separated string and trims whitespace from each part.
func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// evaluateSimpleExpression evaluates a Simple Language expression and returns the string result.
func evaluateSimpleExpression(expression string, exchange *Exchange) (string, error) {
	template, err := ParseSimpleTemplate(expression)
	if err != nil {
		return "", err
	}
	return template.EvaluateAsString(exchange)
}

// Process executes the recipient list on the provided exchange.
func (r *RecipientList) Process(exchange *Exchange) error {
	uris, err := r.resolveURIs(exchange)
	if err != nil {
		return err
	}

	if len(uris) == 0 {
		slog.Debug("recipient list resolved to empty list, skipping")
		return nil
	}

	if r.ParallelProcessing {
		return r.processParallel(exchange, uris)
	}

	return r.processSequential(exchange, uris)
}

func (r *RecipientList) processSequential(exchange *Exchange, uris []string) error {
	var aggregatedExchange *Exchange

	for i, uri := range uris {
		// Create a copy of the original exchange for each recipient
		recipientExchange := exchange.Copy()
		recipientExchange.SetProperty(CamelRecipientListIndex, i)
		recipientExchange.SetProperty(CamelRecipientListSize, len(uris))
		recipientExchange.SetProperty(CamelRecipientListComplete, i == len(uris)-1)

		// Create a producer processor for this URI. The producer is stopped
		// after sending to avoid leaking resources — recipient lists routinely
		// fan out to dozens of URIs that may each open a connection.
		processor := newLazyProducer(r.context, uri)
		processErr := processor.Process(recipientExchange)
		if stopErr := processor.Stop(); stopErr != nil {
			logError("recipient list: producer stop error", "uri", uri, "error", stopErr)
		}

		if processErr != nil {
			if !errors.Is(processErr, ErrStopRouting) {
				return fmt.Errorf("recipient list error sending to %s: %w", uri, processErr)
			}
			// In case of Stop EIP, ignore the error and continue to the next recipient
		}

		if r.AggregationStrategy != nil {
			aggregatedExchange = r.AggregationStrategy.Aggregate(aggregatedExchange, recipientExchange)
		}
	}

	if r.AggregationStrategy != nil && aggregatedExchange != nil {
		exchange.In = aggregatedExchange.In
		exchange.Out = aggregatedExchange.Out
		exchange.Properties = aggregatedExchange.Properties
	}

	return nil
}

func (r *RecipientList) processParallel(exchange *Exchange, uris []string) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var aggregatedExchange *Exchange
	var firstErr error

	// Copy and configure each branch exchange in the caller goroutine BEFORE
	// spawning workers (see Multicast.processParallel for rationale).
	branches := make([]*Exchange, len(uris))
	for i := range uris {
		recipientExchange := exchange.Copy()
		recipientExchange.SetProperty(CamelRecipientListIndex, i)
		recipientExchange.SetProperty(CamelRecipientListSize, len(uris))
		recipientExchange.SetProperty(CamelRecipientListComplete, i == len(uris)-1)
		branches[i] = recipientExchange
	}

	for i, uri := range uris {
		wg.Add(1)
		go func(index int, uri string, recipientExchange *Exchange) {
			defer wg.Done()

			processor := newLazyProducer(r.context, uri)
			// Stop the per-call producer after sending — mirrors the fix
			// applied to dynamic_router to avoid leaking producer resources.
			defer func() {
				if stopErr := processor.Stop(); stopErr != nil {
					logError("recipient list: producer stop error", "uri", uri, "error", stopErr)
				}
			}()

			err := processor.Process(recipientExchange)

			mu.Lock()
			defer mu.Unlock()

			if err != nil && !errors.Is(err, ErrStopRouting) {
				if firstErr == nil {
					firstErr = fmt.Errorf("recipient list error sending to %s: %w", uri, err)
				}
				return
			}

			if r.AggregationStrategy != nil {
				aggregatedExchange = r.AggregationStrategy.Aggregate(aggregatedExchange, recipientExchange)
			}
		}(i, uri, branches[i])
	}

	wg.Wait()

	if firstErr != nil {
		return fmt.Errorf("recipient list parallel error: %w", firstErr)
	}

	if r.AggregationStrategy != nil && aggregatedExchange != nil {
		exchange.In = aggregatedExchange.In
		exchange.Out = aggregatedExchange.Out
		exchange.Properties = aggregatedExchange.Properties
	}

	return nil
}
