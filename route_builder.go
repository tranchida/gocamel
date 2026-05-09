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

// From sets the source endpoint of the route.
func (b *RouteBuilder) From(uri string) *RouteBuilder {
	b.route.From(uri)
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
	return b.ProcessFunc(func(exchange *Exchange) error {
		exchange.GetOut().SetBody(body)
		return nil
	})
}

// SetHeader sets a header on the output message.
func (b *RouteBuilder) SetHeader(key string, value interface{}) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		exchange.GetOut().SetHeader(key, value)
		return nil
	})
}

// SetHeaders sets multiple headers on the output message.
func (b *RouteBuilder) SetHeaders(headers map[string]any) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		exchange.GetOut().SetHeaders(headers)
		return nil
	})
}

// SetHeadersFunc sets multiple headers via a function.
func (b *RouteBuilder) SetHeadersFunc(f func(*Exchange) (map[string]any, error)) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		headers, err := f(exchange)
		if err != nil {
			return err
		}
		exchange.GetOut().SetHeaders(headers)
		return nil
	})
}

// SetProperty sets a property on the exchange.
func (b *RouteBuilder) SetProperty(key string, value any) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		exchange.SetProperty(key, value)
		return nil
	})
}

// SetPropertyFunc sets a property on the exchange via a function.
func (b *RouteBuilder) SetPropertyFunc(key string, f func(*Exchange) (any, error)) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		val, err := f(exchange)
		if err != nil {
			return err
		}
		exchange.SetProperty(key, val)
		return nil
	})
}

// RemoveProperty removes a property from the exchange.
func (b *RouteBuilder) RemoveProperty(key string) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		exchange.RemoveProperty(key)
		return nil
	})
}

// RemoveProperties removes all properties matching the given pattern,
// excluding those that match any of the provided exclusion patterns.
func (b *RouteBuilder) RemoveProperties(pattern string, excludePatterns ...string) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		exchange.RemoveProperties(pattern, excludePatterns...)
		return nil
	})
}

// RemoveHeader removes a header from the incoming message.
func (b *RouteBuilder) RemoveHeader(name string) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		exchange.GetIn().RemoveHeader(name)
		return nil
	})
}

// RemoveHeaders removes all headers matching the given pattern,
// excluding those that match any of the provided exclusion patterns.
func (b *RouteBuilder) RemoveHeaders(pattern string, excludePatterns ...string) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		exchange.GetIn().RemoveHeaders(pattern, excludePatterns...)
		return nil
	})
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

// Split begins a Split EIP block.
func (b *RouteBuilder) Split(expression func(*Exchange) (any, error)) *SplitDefinition {
	s := NewSplitter(expression)
	b.container.AddProcessor(s)

	// Create a new RouteBuilder whose container is the splitter.
	return &SplitDefinition{
		RouteBuilder: &RouteBuilder{
			context:   b.context,
			route:     b.route,
			container: s,
		},
		parent:   b,
		splitter: s,
	}
}

// SplitDefinition is a fluent sub-DSL for configuring the Split EIP.
// It wraps an inner RouteBuilder whose container is the Splitter,
// allowing .Process(), .To(), etc. on each split part before calling .End().
type SplitDefinition struct {
	*RouteBuilder
	parent   *RouteBuilder
	splitter *Splitter
}

// AggregationStrategy sets the aggregation strategy for the splitter.
func (d *SplitDefinition) AggregationStrategy(strategy AggregationStrategy) *SplitDefinition {
	d.splitter.SetAggregationStrategy(strategy)
	return d
}

// Process adds a processor to the current container and stays in the split context.
func (d *SplitDefinition) Process(processor Processor) *SplitDefinition {
	d.RouteBuilder.Process(processor)
	return d
}

// ProcessFunc adds a processing function to the current container and stays in the split context.
func (d *SplitDefinition) ProcessFunc(f func(*Exchange) error) *SplitDefinition {
	d.RouteBuilder.ProcessFunc(f)
	return d
}

// To adds one or more destination endpoints to the current container and stays in the split context.
func (d *SplitDefinition) To(uris ...string) *SplitDefinition {
	d.RouteBuilder.To(uris...)
	return d
}

// ToD adds one or more dynamic destination endpoints and stays in the split context.
func (d *SplitDefinition) ToD(uriTemplates ...string) *SplitDefinition {
	d.RouteBuilder.ToD(uriTemplates...)
	return d
}

// RecipientList adds a Recipient List EIP that sends each split part to dynamic recipients.
func (d *SplitDefinition) RecipientList(expression any) *SplitDefinition {
	rl := NewRecipientList(d.context)
	switch expr := expression.(type) {
	case string:
		if strings.Contains(expr, "${") {
			rl.SetSimpleExpression(expr)
		} else {
			rl.SetStaticURIs(expr)
		}
	case func(*Exchange) ([]string, error):
		rl.SetURIFunc(expr)
	}
	d.container.AddProcessor(rl)
	return d
}

// RoutingSlip routes each split part through a dynamically resolved sequence of endpoints.
func (d *SplitDefinition) RoutingSlip(expression any) *SplitDefinition {
	d.RouteBuilder.RoutingSlip(expression)
	return d
}

// SetBody sets the output message body and stays in the split context.
func (d *SplitDefinition) SetBody(body interface{}) *SplitDefinition {
	d.RouteBuilder.SetBody(body)
	return d
}

// SetHeader sets a header on the output message and stays in the split context.
func (d *SplitDefinition) SetHeader(key string, value interface{}) *SplitDefinition {
	d.RouteBuilder.SetHeader(key, value)
	return d
}

// SetHeaders sets multiple headers and stays in the split context.
func (d *SplitDefinition) SetHeaders(headers map[string]any) *SplitDefinition {
	d.RouteBuilder.SetHeaders(headers)
	return d
}

// SetHeadersFunc sets multiple headers via a function and stays in the split context.
func (d *SplitDefinition) SetHeadersFunc(f func(*Exchange) (map[string]any, error)) *SplitDefinition {
	d.RouteBuilder.SetHeadersFunc(f)
	return d
}

// SetProperty sets a property on the exchange and stays in the split context.
func (d *SplitDefinition) SetProperty(key string, value any) *SplitDefinition {
	d.RouteBuilder.SetProperty(key, value)
	return d
}

// SetPropertyFunc sets a property via a function and stays in the split context.
func (d *SplitDefinition) SetPropertyFunc(key string, f func(*Exchange) (any, error)) *SplitDefinition {
	d.RouteBuilder.SetPropertyFunc(key, f)
	return d
}

// RemoveProperty removes a property from the exchange and stays in the split context.
func (d *SplitDefinition) RemoveProperty(key string) *SplitDefinition {
	d.RouteBuilder.RemoveProperty(key)
	return d
}

// RemoveProperties removes properties matching the given pattern and stays in the split context.
func (d *SplitDefinition) RemoveProperties(pattern string, excludePatterns ...string) *SplitDefinition {
	d.RouteBuilder.RemoveProperties(pattern, excludePatterns...)
	return d
}

// RemoveHeader removes a header from the incoming message and stays in the split context.
func (d *SplitDefinition) RemoveHeader(name string) *SplitDefinition {
	d.RouteBuilder.RemoveHeader(name)
	return d
}

// RemoveHeaders removes headers matching the given pattern and stays in the split context.
func (d *SplitDefinition) RemoveHeaders(pattern string, excludePatterns ...string) *SplitDefinition {
	d.RouteBuilder.RemoveHeaders(pattern, excludePatterns...)
	return d
}

// Stop halts processing of the current part and stays in the split context.
func (d *SplitDefinition) Stop() *SplitDefinition {
	d.RouteBuilder.Stop()
	return d
}

// Aggregate adds an aggregator and stays in the split context.
func (d *SplitDefinition) Aggregate(aggregator *Aggregator) *SplitDefinition {
	d.RouteBuilder.Aggregate(aggregator)
	return d
}

// Log adds a log step and stays in the split context.
func (d *SplitDefinition) Log(message string) *SplitDefinition {
	d.RouteBuilder.Log(message)
	return d
}

// LogBody adds a body log step and stays in the split context.
func (d *SplitDefinition) LogBody(message string) *SplitDefinition {
	d.RouteBuilder.LogBody(message)
	return d
}

// LogHeaders adds a headers log step and stays in the split context.
func (d *SplitDefinition) LogHeaders(message string) *SplitDefinition {
	d.RouteBuilder.LogHeaders(message)
	return d
}

// WireTap adds a WireTap step and stays in the split context.
func (d *SplitDefinition) WireTap(uri string) *SplitDefinition {
	d.RouteBuilder.WireTap(uri)
	return d
}

// End terminates the Split block and returns to the parent builder.
func (d *SplitDefinition) End() *RouteBuilder {
	return d.parent
}

// Log adds a processor that logs the message (structured).
func (b *RouteBuilder) Log(message string) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		logInfo(message,
			"exchange_id", fmt.Sprintf("%p", exchange),
			"route_id", b.route.ID,
		)
		return nil
	})
}

// LogBody adds a processor that logs the message body (structured).
func (b *RouteBuilder) LogBody(message string) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		logInfo(message,
			"body", exchange.GetIn().GetBody(),
			"route_id", b.route.ID,
		)
		return nil
	})
}

// LogHeaders adds a processor that logs the message headers (structured).
func (b *RouteBuilder) LogHeaders(message string) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		logInfo(message,
			"headers", exchange.GetIn().GetHeaders(),
			"route_id", b.route.ID,
		)
		return nil
	})
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
			logError("Error evaluating log expression",
				"error", err,
				"expression", expression,
				"route_id", b.route.ID,
			)
			return nil // Don't fail the exchange just because logging failed
		}
		logInfo("LogSimple", "message", result, "route_id", b.route.ID)
		return nil
	})
}

// EnableMetrics enables Prometheus metrics collection for the current route.
func (b *RouteBuilder) EnableMetrics() *RouteBuilder {
	b.route.EnableMetrics()
	return b
}

// EnableTracing enables OpenTelemetry tracing for the current route.
func (b *RouteBuilder) EnableTracing() *RouteBuilder {
	b.route.EnableTracing()
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

// Build finalizes route construction.
// Returns the route or nil if an error occurred during DSL construction.
func (b *RouteBuilder) Build() (*Route, error) {
	if b.err != nil {
		return nil, b.err
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

	b.route.AddProcessor(&SimpleLanguageProcessor{Template: template})
	return b
}

// SimpleSetHeader sets a header using a Simple Language expression
func (b *RouteBuilder) SimpleSetHeader(headerName string, expression string) *RouteBuilder {
	template, err := ParseSimpleTemplate(expression)
	if err != nil {
		b.err = fmt.Errorf("failed to parse simple expression: %w", err)
		return b
	}

	b.route.AddProcessor(&SimpleSetHeaderProcessor{
		HeaderName: headerName,
		Expression: template,
	})
	return b
}

// Stop halts processing of the current exchange.
func (b *RouteBuilder) Stop() *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		return ErrStopRouting
	})
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

// To adds one or more destination endpoints to the current container.
// If multiple URIs are provided, a Multicast is created automatically.
func (b *RouteBuilder) To(uris ...string) *RouteBuilder {
	if len(uris) == 0 {
		return b
	}
	if len(uris) == 1 {
		b.container.AddProcessor(createToProcessor(b.context, uris[0]))
	} else {
		m := NewMulticast()
		for _, uri := range uris {
			m.AddProcessor(createToProcessor(b.context, uri))
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

// Multicast begins a Multicast EIP block.
func (b *RouteBuilder) Multicast() *MulticastDefinition {
	m := NewMulticast()
	b.container.AddProcessor(m)

	return &MulticastDefinition{
		RouteBuilder: &RouteBuilder{
			context:   b.context,
			route:     b.route,
			container: m,
		},
		parent:    b,
		multicast: m,
	}
}

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

// MulticastDefinition is a fluent sub-DSL for configuring the Multicast EIP.
// Supports .ParallelProcessing(), .AggregationStrategy(), .Pipeline() for
// grouping processors, and .End() to return to the parent builder.
type MulticastDefinition struct {
	*RouteBuilder
	parent    *RouteBuilder
	multicast *Multicast
}

// AggregationStrategy sets the aggregation strategy for the multicast.
func (d *MulticastDefinition) AggregationStrategy(strategy AggregationStrategy) *MulticastDefinition {
	d.multicast.SetAggregationStrategy(strategy)
	return d
}

// ParallelProcessing enables parallel processing for the multicast.
func (d *MulticastDefinition) ParallelProcessing() *MulticastDefinition {
	d.multicast.SetParallelProcessing(true)
	return d
}

// Pipeline begins a Pipeline block for grouping processors within a multicast branch.
func (d *MulticastDefinition) Pipeline() *PipelineDefinition {
	p := NewPipeline()
	d.multicast.AddProcessor(p)

	return &PipelineDefinition{
		RouteBuilder: &RouteBuilder{
			context:   d.context,
			route:     d.route,
			container: p,
		},
		parent: d,
	}
}

// Process adds a processor to the current container and stays in the multicast context.
func (d *MulticastDefinition) Process(processor Processor) *MulticastDefinition {
	d.RouteBuilder.Process(processor)
	return d
}

// ProcessFunc adds a processing function to the current container and stays in the multicast context.
func (d *MulticastDefinition) ProcessFunc(f func(*Exchange) error) *MulticastDefinition {
	d.RouteBuilder.ProcessFunc(f)
	return d
}

// To adds one or more destination endpoints to the current container and stays in the multicast context.
func (d *MulticastDefinition) To(uris ...string) *MulticastDefinition {
	d.RouteBuilder.To(uris...)
	return d
}

// ToD adds one or more dynamic destination endpoints and stays in the multicast context.
func (d *MulticastDefinition) ToD(uriTemplates ...string) *MulticastDefinition {
	d.RouteBuilder.ToD(uriTemplates...)
	return d
}

// RecipientList sends to dynamically resolved recipients within this multicast branch.
func (d *MulticastDefinition) RecipientList(expression any) *MulticastDefinition {
	rl := NewRecipientList(d.context)
	switch expr := expression.(type) {
	case string:
		if strings.Contains(expr, "${") {
			rl.SetSimpleExpression(expr)
		} else {
			rl.SetStaticURIs(expr)
		}
	case func(*Exchange) ([]string, error):
		rl.SetURIFunc(expr)
	}
	d.container.AddProcessor(rl)
	return d
}

// RoutingSlip routes within this multicast branch through a dynamic endpoint sequence.
func (d *MulticastDefinition) RoutingSlip(expression any) *MulticastDefinition {
	d.RouteBuilder.RoutingSlip(expression)
	return d
}

// SetBody sets the output message body and stays in the multicast context.
func (d *MulticastDefinition) SetBody(body interface{}) *MulticastDefinition {
	d.RouteBuilder.SetBody(body)
	return d
}

// SetHeader sets a header on the output message and stays in the multicast context.
func (d *MulticastDefinition) SetHeader(key string, value interface{}) *MulticastDefinition {
	d.RouteBuilder.SetHeader(key, value)
	return d
}

// WireTap adds a WireTap step and stays in the multicast context.
func (d *MulticastDefinition) WireTap(uri string) *MulticastDefinition {
	d.RouteBuilder.WireTap(uri)
	return d
}

// End terminates the Multicast block and returns to the parent builder.
func (d *MulticastDefinition) End() *RouteBuilder {
	return d.parent
}

// PipelineDefinition is a fluent sub-DSL for grouping processors inside a
// multicast branch. Call .End() to return to the MulticastDefinition.
type PipelineDefinition struct {
	*RouteBuilder
	parent *MulticastDefinition
}

// Process adds a processor to the current container and stays in the pipeline context.
func (d *PipelineDefinition) Process(processor Processor) *PipelineDefinition {
	d.RouteBuilder.Process(processor)
	return d
}

// ProcessFunc adds a processing function to the current container and stays in the pipeline context.
func (d *PipelineDefinition) ProcessFunc(f func(*Exchange) error) *PipelineDefinition {
	d.RouteBuilder.ProcessFunc(f)
	return d
}

// To adds one or more destination endpoints to the current container and stays in the pipeline context.
func (d *PipelineDefinition) To(uris ...string) *PipelineDefinition {
	d.RouteBuilder.To(uris...)
	return d
}

// ToD adds one or more dynamic destination endpoints and stays in the pipeline context.
func (d *PipelineDefinition) ToD(uriTemplates ...string) *PipelineDefinition {
	d.RouteBuilder.ToD(uriTemplates...)
	return d
}

// RecipientList sends to dynamically resolved recipients within this pipeline branch.
func (d *PipelineDefinition) RecipientList(expression any) *PipelineDefinition {
	rl := NewRecipientList(d.context)
	switch expr := expression.(type) {
	case string:
		if strings.Contains(expr, "${") {
			rl.SetSimpleExpression(expr)
		} else {
			rl.SetStaticURIs(expr)
		}
	case func(*Exchange) ([]string, error):
		rl.SetURIFunc(expr)
	}
	d.container.AddProcessor(rl)
	return d
}

// RoutingSlip routes within this pipeline branch through a dynamic endpoint sequence.
func (d *PipelineDefinition) RoutingSlip(expression any) *PipelineDefinition {
	d.RouteBuilder.RoutingSlip(expression)
	return d
}

// SetBody sets the output message body and stays in the pipeline context.
func (d *PipelineDefinition) SetBody(body interface{}) *PipelineDefinition {
	d.RouteBuilder.SetBody(body)
	return d
}

// WireTap adds a WireTap step and stays in the pipeline context.
func (d *PipelineDefinition) WireTap(uri string) *PipelineDefinition {
	d.RouteBuilder.WireTap(uri)
	return d
}

// End terminates the Pipeline block and returns to the multicast builder.
func (d *PipelineDefinition) End() *MulticastDefinition {
	return d.parent
}
