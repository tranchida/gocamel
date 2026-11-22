package gocamel

import (
	"fmt"
	"strings"
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
	// MaxConcurrency bounds the recipients called at the same time when
	// ParallelProcessing is set (<= 0: DefaultMaxConcurrency).
	MaxConcurrency int
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

// SetMaxConcurrency bounds the recipients called at the same time when
// parallel processing is enabled. n <= 0 restores DefaultMaxConcurrency.
func (r *RecipientList) SetMaxConcurrency(n int) *RecipientList {
	r.MaxConcurrency = n
	return r
}

// SetAggregationStrategy sets the aggregation strategy for collecting results.
func (r *RecipientList) SetAggregationStrategy(strategy AggregationStrategy) *RecipientList {
	r.AggregationStrategy = strategy
	return r
}

// Validate implements the Validator interface for build-time validation.
func (r *RecipientList) Validate() error {
	if r == nil {
		return nil
	}
	if r.SimpleExpression != "" {
		if _, err := ParseSimpleTemplate(r.SimpleExpression); err != nil {
			return fmt.Errorf("recipient list: invalid simple expression: %w", err)
		}
	}
	return nil
}

// resolveURIs resolves the recipient URIs at runtime from the configured expression.
func (r *RecipientList) resolveURIs(exchange *Exchange) ([]string, error) {
	// Priority: Go function > Simple expression > static string
	if r.URIFunc != nil {
		uris, err := r.URIFunc(exchange)
		if err != nil {
			return nil, fmt.Errorf("recipient list URI function error: %w", err)
		}
		LogDebug("recipient list resolved URIs via function",
			"count", len(uris),
			"uris", redactURIs(uris),
		)
		return uris, nil
	}

	if r.SimpleExpression != "" {
		uris, err := evaluateURIListTemplate(r.SimpleExpression, exchange)
		if err != nil {
			return nil, fmt.Errorf("recipient list simple expression error: %w", err)
		}
		LogDebug("recipient list resolved URIs via simple expression",
			"expression", r.SimpleExpression,
			"count", len(uris),
			"uris", redactURIs(uris),
		)
		return uris, nil
	}

	if r.StaticURIs != "" {
		uris := interpolateURIList(r.StaticURIs, exchange)
		LogDebug("recipient list resolved URIs via static string",
			"count", len(uris),
			"uris", redactURIs(uris),
		)
		return uris, nil
	}

	return nil, nil
}

// evaluateURIListTemplate evaluates a comma-separated URI list template safely.
// Whole-list expressions without literal URI delimiters outside ${...}
// (e.g. "${header.targets}") are evaluated as a string and split on commas,
// preserving explicit whole-list delegation. Templates with literal URI syntax
// are split on commas outside ${...} first, and each item is evaluated via
// SimpleTemplate with URI escaping (including comma escaping).
func evaluateURIListTemplate(expr string, exchange *Exchange) ([]string, error) {
	tmpl, err := ParseSimpleTemplate(expr)
	if err != nil {
		return nil, err
	}
	if !tmpl.hasLiteralURIDelimiters() {
		resolved, err := tmpl.evaluateAsURIListString(exchange)
		if err != nil {
			return nil, err
		}
		return splitAndTrim(resolved), nil
	}

	items := tmpl.splitCommaOutsidePlaceholders()
	uris := make([]string, 0, len(items))
	for _, item := range items {
		resolved, err := item.evaluateURI(exchange, true)
		if err != nil {
			return nil, err
		}
		trimmed := strings.TrimSpace(resolved)
		if trimmed != "" {
			uris = append(uris, trimmed)
		}
	}
	return uris, nil
}

func interpolateURIList(staticURIs string, exchange *Exchange) []string {
	rawList := splitAndTrim(staticURIs)
	uris := make([]string, 0, len(rawList))
	for _, raw := range rawList {
		if resolved := strings.TrimSpace(interpolateURI(raw, exchange, true)); resolved != "" {
			uris = append(uris, resolved)
		}
	}
	return uris
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
		LogDebug("recipient list resolved to empty list, skipping")
		return nil
	}

	if r.ParallelProcessing {
		return r.processParallel(exchange, uris)
	}

	return r.processSequential(exchange, uris)
}

// sendToURI resolves a started producer from the CamelContext producer cache
// (created once per URI, reused across messages, stopped with the context) and
// sends the exchange to it. This avoids creating and tearing down a connection
// per message, which is what newLazyProducer + Stop per call did previously.
// Out is propagated to In so the next recipient/step sees the response, mirroring
// the behaviour of ToD and WireTap.
func (r *RecipientList) sendToURI(uri string, exchange *Exchange) error {
	producer, release, err := r.context.acquireProducer(uri)
	if err != nil {
		return fmt.Errorf("recipient list: producer error for %s: %w", RedactURI(uri), err)
	}
	defer release()
	propagateOutToInAndReset(exchange)
	return producer.Send(exchange)
}

func (r *RecipientList) processSequential(exchange *Exchange, uris []string) error {
	return r.processRecipients(exchange, uris, false)
}

func (r *RecipientList) processParallel(exchange *Exchange, uris []string) error {
	return r.processRecipients(exchange, uris, true)
}

func (r *RecipientList) processRecipients(exchange *Exchange, uris []string, parallel bool) error {
	propagateOutToInAndReset(exchange)
	branches := make([]*Exchange, len(uris))
	for i := range uris {
		if parallel {
			branches[i] = exchange.DeepCopy()
		} else {
			branches[i] = exchange.Copy()
		}
		branches[i].SetProperty(CamelRecipientListIndex, i)
		branches[i].SetProperty(CamelRecipientListSize, len(uris))
		branches[i].SetProperty(CamelRecipientListComplete, i == len(uris)-1)
	}
	aggregated, err := processFanOut(branches, parallel, effectiveMaxConcurrency(r.MaxConcurrency), func(i int, branch *Exchange) error {
		return r.sendToURI(uris[i], branch)
	}, r.AggregationStrategy)
	if err != nil {
		return fmt.Errorf("recipient list: %w", err)
	}
	if r.AggregationStrategy != nil {
		publishAggregatedResult(exchange, aggregated)
	}
	return nil
}
