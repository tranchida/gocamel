package gocamel

import (
	"fmt"
	"strings"
	"time"
)

// RouteBuilder provides a fluent DSL for constructing Camel routes.
// It wraps a Route and delegates method calls to build the processor pipeline.
// Errors during construction are accumulated and surfaced at Build() time.
type RouteBuilder struct {
	context   *CamelContext
	route     *Route
	container ProcessorContainer // Current processor container (route or nested EIP).
	err       error              // accumulated error during DSL construction.
}

// NewRouteBuilder creates a new RouteBuilder instance.
func NewRouteBuilder(context *CamelContext) *RouteBuilder {
	route := context.CreateRoute()
	return &RouteBuilder{
		context:   context,
		route:     route,
		container: route,
	}
}

// adoptErr copies a nested builder's construction error onto b so End() cannot
// drop Filter/Simple parse failures that were recorded on the child.
func (b *RouteBuilder) adoptErr(child *RouteBuilder) {
	if b == nil || child == nil || child == b {
		return
	}
	if child.err != nil && b.err == nil {
		b.err = child.err
	}
}

// From sets the source endpoint of the route.
// Endpoint resolution errors are recorded and surfaced by Build().
func (b *RouteBuilder) From(uri string) *RouteBuilder {
	b.route.From(uri)
	if b.route.err != nil {
		b.err = b.route.err
	}
	return b
}

// Process adds a processor to the current container.
func (b *RouteBuilder) Process(processor Processor) *RouteBuilder {
	b.container.AddProcessor(processor)
	return b
}

// ProcessRef adds a processor looked up by name from the registry.
func (b *RouteBuilder) ProcessRef(name string) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		val, exists := b.context.GetComponentRegistry().Lookup(name)
		if !exists {
			return fmt.Errorf("processor not found in registry: %s", name)
		}
		processor, ok := val.(Processor)
		if !ok {
			return fmt.Errorf("the object found is not a processor: %s", name)
		}
		return processor.Process(exchange)
	})
}

// ProcessFunc adds a processing function to the current container.
func (b *RouteBuilder) ProcessFunc(f func(*Exchange) error) *RouteBuilder {
	b.container.AddProcessor(ProcessorFunc(f))
	return b
}

// SetBody sets the output message body.
func (b *RouteBuilder) SetBody(body interface{}) *RouteBuilder {
	b.container.AddProcessor(setBodyProcessor(body))
	return b
}

// SetHeader sets a header on the output message.
func (b *RouteBuilder) SetHeader(key string, value interface{}) *RouteBuilder {
	b.container.AddProcessor(setHeaderProcessor(key, value))
	return b
}

// SetHeaders sets multiple headers on the output message.
func (b *RouteBuilder) SetHeaders(headers map[string]any) *RouteBuilder {
	b.container.AddProcessor(setHeadersProcessor(headers))
	return b
}

// SetHeadersFunc sets multiple headers via a function.
func (b *RouteBuilder) SetHeadersFunc(f func(*Exchange) (map[string]any, error)) *RouteBuilder {
	b.container.AddProcessor(setHeadersFuncProcessor(f))
	return b
}

// SetProperty sets a property on the exchange.
func (b *RouteBuilder) SetProperty(key string, value any) *RouteBuilder {
	b.container.AddProcessor(setPropertyProcessor(key, value))
	return b
}

// SetPropertyFunc sets a property on the exchange via a function.
func (b *RouteBuilder) SetPropertyFunc(key string, f func(*Exchange) (any, error)) *RouteBuilder {
	b.container.AddProcessor(setPropertyFuncProcessor(key, f))
	return b
}

// RemoveProperty removes a property from the exchange.
func (b *RouteBuilder) RemoveProperty(key string) *RouteBuilder {
	b.container.AddProcessor(removePropertyProcessor(key))
	return b
}

// RemoveProperties removes all properties matching the given pattern,
// excluding those that match any of the provided exclusion patterns.
func (b *RouteBuilder) RemoveProperties(pattern string, excludePatterns ...string) *RouteBuilder {
	b.container.AddProcessor(removePropertiesProcessor(pattern, excludePatterns))
	return b
}

// RemoveHeader removes a header from the incoming message.
func (b *RouteBuilder) RemoveHeader(name string) *RouteBuilder {
	b.container.AddProcessor(removeHeaderProcessor(name))
	return b
}

// RemoveHeaders removes all headers matching the given pattern,
// excluding those that match any of the provided exclusion patterns.
func (b *RouteBuilder) RemoveHeaders(pattern string, excludePatterns ...string) *RouteBuilder {
	b.container.AddProcessor(removeHeadersProcessor(pattern, excludePatterns))
	return b
}

// SetID sets the route ID.
func (b *RouteBuilder) SetID(id string) *RouteBuilder {
	b.route.SetID(id)
	return b
}

// SetDescription sets the route description.
func (b *RouteBuilder) SetDescription(description string) *RouteBuilder {
	b.route.SetDescription(description)
	return b
}

// Transacted enables transactional mode for the route.
// In GoCamel, this ensures that Exchange synchronizations (such as source message acknowledgement)
// are executed with the appropriate status at the end of the route.
func (b *RouteBuilder) Transacted() *RouteBuilder {
	b.route.SetTransacted(true)
	return b
}

// Aggregate adds an Aggregator processor to the current container.
func (b *RouteBuilder) Aggregate(aggregator *Aggregator) *RouteBuilder {
	b.container.AddProcessor(aggregator)
	return b
}

// Log adds a processor that logs the message (structured).
func (b *RouteBuilder) Log(message string) *RouteBuilder {
	b.container.AddProcessor(logProcessor(message, b.route.ID))
	return b
}

// LogBody adds a processor that logs the message body (structured).
func (b *RouteBuilder) LogBody(message string) *RouteBuilder {
	b.container.AddProcessor(logBodyProcessor(message, b.route.ID))
	return b
}

// LogHeaders adds a processor that logs the message headers (structured).
func (b *RouteBuilder) LogHeaders(message string) *RouteBuilder {
	b.container.AddProcessor(logHeadersProcessor(message, b.route.ID))
	return b
}

// LogSimple adds a processor that logs a message evaluated via the Simple Language.
func (b *RouteBuilder) LogSimple(expression string) *RouteBuilder {
	template, err := ParseSimpleTemplate(expression)
	if err != nil {
		b.err = fmt.Errorf("failed to parse simple expression for log: %w", err)
		return b
	}
	return b.ProcessFunc(func(exchange *Exchange) error {
		result, err := template.EvaluateAsString(exchange)
		if err != nil {
			LogError("Error evaluating log expression",
				"error", err,
				"expression", expression,
				"route_id", b.route.ID,
			)
			return nil // Don't fail the exchange just because logging failed
		}
		LogInfo("LogSimple", "message", result, "route_id", b.route.ID)
		return nil
	})
}

// Intercept adds an interceptor around the current route's processing.
// Interceptors are the extension point for cross-cutting concerns such as
// metrics or tracing (see the observability/prometheus and observability/otel
// modules).
func (b *RouteBuilder) Intercept(interceptor RouteInterceptor) *RouteBuilder {
	b.route.Intercept(interceptor)
	return b
}

// EnableMessageHistory enables Message History tracking for the current route.
func (b *RouteBuilder) EnableMessageHistory() *RouteBuilder {
	b.route.EnableMessageHistory()
	return b
}

// ErrorHandler configures a custom ErrorHandler for this route.
// This is a terminal DSL operation — it sets the handler on the route
// being built. Multiple calls will overwrite previous settings.
//
// Example:
//
//	route, err := ctx.CreateRouteBuilder().
//	    From("direct:start").
//	    ErrorHandler(NewDeadLetterChannel(ctx, "log:errors", MaxRedeliveries(3))).
//	    ProcessFunc(myProcessor).
//	    Build()
func (b *RouteBuilder) ErrorHandler(handler ErrorHandler) *RouteBuilder {
	b.route.SetErrorHandler(handler)
	return b
}

// DeadLetterChannel configures a DeadLetterChannel error handler for this route.
// This is a convenience method equivalent to:
//
//	ErrorHandler(NewDeadLetterChannel(b.context, deadLetterURI, opts...))
//
// Example:
//
//	route, err := ctx.CreateRouteBuilder().
//	    From("direct:start").
//	    DeadLetterChannel("file:errors", MaxRedeliveries(3), RedeliveryDelay(2*time.Second)).
//	    ProcessFunc(myProcessor).
//	    Build()
func (b *RouteBuilder) DeadLetterChannel(deadLetterURI string, opts ...DeadLetterChannelOption) *RouteBuilder {
	dlc := NewDeadLetterChannel(b.context, deadLetterURI, opts...)
	b.route.SetErrorHandler(dlc)
	return b
}

// OnException starts a route-scoped clause handling errors that match one of
// the targets (Apache Camel's onException). A target is either an error value,
// matched with errors.Is (sentinels such as io.EOF), or a non-nil pointer to an
// error or interface type, matched with errors.As (new(*MyError),
// ErrorType[*MyError]()). A pointer to a struct whose Error method has a
// pointer receiver (&MyError{}) is an error value and only matches itself.
//
// When a route processor fails, the clauses are tried in declaration order
// and the FIRST matching one wins, so declare specific clauses before generic
// ones. A matched clause replaces the route's ErrorHandler for that error: the
// failed processor is redelivered up to MaximumRedeliveries times, then the
// clause's handler processors run, then Handled/Continued decide the outcome.
// Unmatched errors, and exchanges already dead-lettered (IsDeadLettered), keep
// the default ErrorHandler behaviour.
//
// Clauses apply to errors returned by the route's top-level processors; a
// redelivery re-runs the whole top-level step (e.g. a whole Split block).
//
// Example:
//
//	route, err := ctx.CreateRouteBuilder().
//	    OnException(ErrorType[*ValidationError]()).
//	        Handled(true).
//	        To("direct:invalid").
//	    End().
//	    OnException(io.ErrUnexpectedEOF).
//	        MaximumRedeliveries(3).
//	        RedeliveryDelay(100 * time.Millisecond).
//	    End().
//	    From("direct:in").
//	    To("http://backend/api").
//	    Build()
func (b *RouteBuilder) OnException(targets ...any) *OnExceptionDefinition {
	clause := newOnExceptionClause()
	d := &OnExceptionDefinition{
		parent: b,
		clause: clause,
		handler: &RouteBuilder{
			context:   b.context,
			route:     b.route,
			container: clause.handler,
		},
	}
	if len(targets) == 0 {
		d.fail(fmt.Errorf("OnException: at least one error target is required"))
	}
	for i, target := range targets {
		matcher, err := newOnExceptionMatcher(target)
		if err != nil {
			d.fail(fmt.Errorf("OnException: target[%d]: %w", i, err))
			continue
		}
		clause.matchers = append(clause.matchers, matcher)
	}
	b.route.onExceptions = append(b.route.onExceptions, clause)
	return d
}

// Build finalizes route construction.
// Returns the route or an error accumulated during DSL construction,
// including From() endpoint resolution failures stored on the route.
func (b *RouteBuilder) Build() (*Route, error) {
	if b.err != nil {
		return nil, b.err
	}
	if b.route.err != nil {
		return nil, b.route.err
	}
	return b.route, nil
}

// SimpleSetBody sets the body using a Simple Language expression
func (b *RouteBuilder) SimpleSetBody(expression string) *RouteBuilder {
	template, err := ParseSimpleTemplate(expression)
	if err != nil {
		b.err = fmt.Errorf("failed to parse simple expression: %w", err)
		return b
	}

	b.container.AddProcessor(&SimpleLanguageProcessor{Template: template})
	return b
}

// SimpleSetHeader sets a header using a Simple Language expression
func (b *RouteBuilder) SimpleSetHeader(headerName string, expression string) *RouteBuilder {
	template, err := ParseSimpleTemplate(expression)
	if err != nil {
		b.err = fmt.Errorf("failed to parse simple expression: %w", err)
		return b
	}

	b.container.AddProcessor(&SimpleSetHeaderProcessor{
		HeaderName: headerName,
		Expression: template,
	})
	return b
}

// Marshal adds a dataformat marshal step to the current route.
func (b *RouteBuilder) Marshal(dataFormat DataFormat) *RouteBuilder {
	b.container.AddProcessor(&MarshalProcessor{DataFormat: dataFormat})
	return b
}

// Transform adds a Transform/Content Filter EIP step.
// The transformFn function takes the current exchange and returns a new body.
func (b *RouteBuilder) Transform(transformFn func(*Exchange) (any, error)) *RouteBuilder {
	b.container.AddProcessor(NewTransformer(transformFn))
	return b
}

// TransformSimple adds a Transform EIP step evaluated via Simple Language.
func (b *RouteBuilder) TransformSimple(expression string) *RouteBuilder {
	t, err := NewSimpleTransformer(expression)
	if err != nil {
		b.err = err
		return b
	}
	b.container.AddProcessor(t)
	return b
}

// Unmarshal adds a dataformat unmarshal step to the current route.
func (b *RouteBuilder) Unmarshal(dataFormat DataFormat) *RouteBuilder {
	b.container.AddProcessor(&UnmarshalProcessor{DataFormat: dataFormat})
	return b
}

// Stop halts processing of the current exchange.
func (b *RouteBuilder) Stop() *RouteBuilder {
	b.container.AddProcessor(stopProcessor())
	return b
}

// Filter adds a Message Filter EIP step. The expression can be:
//   - A func(*Exchange) (bool, error) — arbitrary Go predicate
//   - A string — evaluated as a Simple Language expression
//
// Exchanges that do not match the predicate are silently dropped (ErrStopRouting).
//
// Example:
//
//	// Go predicate
//	route, _ := ctx.CreateRouteBuilder().
//	    From("direct:start").
//	    Filter(func(e *Exchange) (bool, error) {
//	        body, _ := e.GetBodyAsString()
//	        return body != "", nil
//	    }).
//	    To("direct:nonEmpty").
//	    Build()
//
//	// Simple Language
//	route, _ := ctx.CreateRouteBuilder().
//	    From("direct:start").
//	    Filter("${header.type} == 'order'").
//	    To("direct:orders").
//	    Build()
func (b *RouteBuilder) Filter(expression any) *RouteBuilder {
	switch expr := expression.(type) {
	case func(*Exchange) (bool, error):
		b.container.AddProcessor(NewMessageFilter(expr))
	case string:
		f, err := NewSimpleMessageFilter(expr)
		if err != nil {
			b.err = err
			return b
		}
		b.container.AddProcessor(f)
	default:
		b.err = fmt.Errorf("Filter: unsupported expression type %T (want func(*Exchange)(bool,error) or string)", expression)
	}
	return b
}

// IdempotentConsumer adds an Idempotent Consumer EIP step.
// The expression evaluates to a unique key for the exchange.
// If the key has been seen before (according to the repository), the exchange is dropped.
//
// Example:
//
//	repo := gocamel.NewMemoryIdempotentRepository()
//	route, _ := ctx.CreateRouteBuilder().
//	    From("direct:start").
//	    IdempotentConsumer("${header.MessageId}", repo).
//	    To("direct:next").
//	    Build()
func (b *RouteBuilder) IdempotentConsumer(expression any, repo IdempotentRepository) *RouteBuilder {
	switch expr := expression.(type) {
	case func(*Exchange) (string, error):
		b.container.AddProcessor(NewIdempotentConsumer(expr, repo))
	case string:
		p, err := NewSimpleIdempotentConsumer(expr, repo)
		if err != nil {
			b.err = err
			return b
		}
		b.container.AddProcessor(p)
	default:
		b.err = fmt.Errorf("IdempotentConsumer: unsupported expression type %T (want func(*Exchange)(string,error) or string)", expression)
	}
	return b
}

// To adds one or more destination endpoints to the current container.
// If multiple URIs are provided, a Multicast is created automatically.
func (b *RouteBuilder) To(uris ...string) *RouteBuilder {
	if len(uris) == 0 {
		return b
	}
	if len(uris) == 1 {
		lp := newLazyProducer(b.context, uris[0])
		b.route.lazyProducers = append(b.route.lazyProducers, lp)
		b.container.AddProcessor(lp)
	} else {
		m := NewMulticast()
		for _, uri := range uris {
			lp := newLazyProducer(b.context, uri)
			b.route.lazyProducers = append(b.route.lazyProducers, lp)
			m.AddProcessor(lp)
		}
		b.container.AddProcessor(m)
	}
	return b
}

// ToD adds one or more dynamic destination endpoints to the current container.
// If multiple templates are provided, a Multicast is created automatically.
func (b *RouteBuilder) ToD(uriTemplates ...string) *RouteBuilder {
	if len(uriTemplates) == 0 {
		return b
	}
	if len(uriTemplates) == 1 {
		b.container.AddProcessor(createToDProcessor(b.context, uriTemplates[0]))
	} else {
		m := NewMulticast()
		for _, uriTemplate := range uriTemplates {
			m.AddProcessor(createToDProcessor(b.context, uriTemplate))
		}
		b.container.AddProcessor(m)
	}
	return b
}

// Delay adds a Delay EIP step that pauses exchange processing for the given
// fixed duration. The sleep respects context cancellation.
//
// Example:
//
//	route, _ := ctx.CreateRouteBuilder().
//	    From("direct:start").
//	    Delay(500 * time.Millisecond).
//	    To("direct:next").
//	    Build()
func (b *RouteBuilder) Delay(d time.Duration) *RouteBuilder {
	b.container.AddProcessor(NewDelayer(d))
	return b
}

// DelayFunc adds a Delay EIP step whose duration is computed per exchange.
// Return 0 to skip the delay for a given exchange.
//
// Example:
//
//	route, _ := ctx.CreateRouteBuilder().
//	    From("direct:start").
//	    DelayFunc(func(e *gocamel.Exchange) (time.Duration, error) {
//	        ms, ok := e.GetHeaderAsInt("X-Delay-Ms")
//	        if !ok {
//	            return 0, nil
//	        }
//	        return time.Duration(ms) * time.Millisecond, nil
//	    }).
//	    To("direct:next").
//	    Build()
func (b *RouteBuilder) DelayFunc(fn func(*Exchange) (time.Duration, error)) *RouteBuilder {
	if fn == nil {
		b.err = fmt.Errorf("DelayFunc: delay function is nil")
		return b
	}
	b.container.AddProcessor(NewDelayerFunc(fn))
	return b
}

// Enrich adds a Content Enricher EIP step. It calls the given URI, retrieves
// a response, and merges it into the current exchange via the AggregationStrategy.
// Pass nil as strategy to simply replace the exchange body with the response body.
//
// Example:
//
//	// Merge enrichment into current exchange
//	route, _ := ctx.CreateRouteBuilder().
//	    From("direct:start").
//	    Enrich("direct:lookup", &MyMergeStrategy{}).
//	    To("direct:next").
//	    Build()
//
//	// Replace body (no strategy)
//	route, _ := ctx.CreateRouteBuilder().
//	    From("direct:start").
//	    Enrich("http://api.example.com/extra", nil).
//	    To("direct:next").
//	    Build()
func (b *RouteBuilder) Enrich(uri string, strategy AggregationStrategy) *RouteBuilder {
	b.container.AddProcessor(NewContentEnricher(b.context, uri, strategy))
	return b
}

// Throttle adds a Throttle EIP step that limits the exchange rate to at most
// maxRequests per timePeriod. Exchanges that exceed the rate are delayed until
// the current window resets (they are never dropped).
//
// Example:
//
//	// Max 100 messages per second
//	route, _ := ctx.CreateRouteBuilder().
//	    From("direct:start").
//	    Throttle(100, time.Second).
//	    To("direct:downstream").
//	    Build()
func (b *RouteBuilder) Throttle(maxRequests int64, timePeriod time.Duration) *RouteBuilder {
	b.container.AddProcessor(NewThrottler(maxRequests, timePeriod))
	return b
}

// Sample adds a Sampling EIP step that lets only the first exchange of every
// period through. The other exchanges are dropped silently (never delayed).
// A non-positive period is reported by Route.Validate().
//
// Example:
//
//	// At most one message per second
//	route, _ := ctx.CreateRouteBuilder().
//	    From("direct:sensor").
//	    Sample(time.Second).
//	    To("direct:dashboard").
//	    Build()
func (b *RouteBuilder) Sample(period time.Duration) *RouteBuilder {
	b.container.AddProcessor(NewSamplerPeriod(period))
	return b
}

// SampleEvery adds a Sampling EIP step that lets one exchange out of every n
// through (the first one passes). The other exchanges are dropped silently.
// A non-positive n is reported by Route.Validate().
//
// Example:
//
//	// One message out of every 100
//	route, _ := ctx.CreateRouteBuilder().
//	    From("direct:sensor").
//	    SampleEvery(100).
//	    To("direct:dashboard").
//	    Build()
func (b *RouteBuilder) SampleEvery(n int64) *RouteBuilder {
	b.container.AddProcessor(NewSamplerEvery(n))
	return b
}

// WireTap adds a WireTap processor that asynchronously sends a copy of the exchange
// to the specified endpoint without blocking the main route.
// The copy is independent: changes to the tapped exchange do not affect
// the main route and vice-versa.
//
// Example:
//
//	route, err := ctx.CreateRouteBuilder().
//	    From("direct:start").
//	    WireTap("direct:audit").
//	    To("direct:result").
//	    Build()
func (b *RouteBuilder) WireTap(uri string) *RouteBuilder {
	b.container.AddProcessor(NewWireTap(b.context, uri))
	return b
}

// RoutingSlip adds a Routing Slip EIP processor that sends the exchange through
// a sequence of endpoints resolved dynamically at runtime.
//
// The expression can be:
//   - A string of comma-separated URIs (e.g. "direct:a,direct:b")
//   - A Simple Language expression (e.g. "${header.routingSlip}")
//   - A Go function returning []string
//
// Unlike RecipientList, RoutingSlip uses the same exchange for each step and
// propagates each response into the next request.
func (b *RouteBuilder) RoutingSlip(expression any) *RouteBuilder {
	rs := NewRoutingSlip(b.context)

	switch expr := expression.(type) {
	case string:
		if strings.Contains(expr, "${") {
			rs.SetSimpleExpression(expr)
		} else {
			rs.SetStaticURIs(expr)
		}
	case func(*Exchange) ([]string, error):
		rs.SetURIFunc(expr)
	default:
		b.err = fmt.Errorf("unsupported RoutingSlip expression type: %T", expression)
		return b
	}

	b.container.AddProcessor(rs)
	return b
}
