package gocamel

import (
	"fmt"
	"strings"
)

// This file implements the Split EIP sub-DSL.
//
// The methods on SplitDefinition are type-preserving wrappers around the
// underlying RouteBuilder methods. They exist so that .Split().Foo().End()
// stays type-safe within the Split DSL — Go does not let an embedded method
// return the embedding type, so each method must be re-declared with the
// correct return type. The actual processor construction lives either on
// RouteBuilder (which the wrapper delegates to) or in dsl_processors.go.

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

// ParallelProcessing processes split parts concurrently.
func (d *SplitDefinition) ParallelProcessing(parallel bool) *SplitDefinition {
	d.splitter.SetParallelProcessing(parallel)
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
	default:
		d.err = fmt.Errorf("unsupported RecipientList expression type: %T", expression)
		return d
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

// SimpleSetBody sets the body via a Simple expression and stays in the split context.
func (d *SplitDefinition) SimpleSetBody(expression string) *SplitDefinition {
	d.RouteBuilder.SimpleSetBody(expression)
	return d
}

// SimpleSetHeader sets a header via a Simple expression and stays in the split context.
func (d *SplitDefinition) SimpleSetHeader(headerName, expression string) *SplitDefinition {
	d.RouteBuilder.SimpleSetHeader(headerName, expression)
	return d
}

// Marshal adds a dataformat marshal step and stays in the split context.
func (d *SplitDefinition) Marshal(dataFormat DataFormat) *SplitDefinition {
	d.RouteBuilder.Marshal(dataFormat)
	return d
}

// Unmarshal adds a dataformat unmarshal step and stays in the split context.
func (d *SplitDefinition) Unmarshal(dataFormat DataFormat) *SplitDefinition {
	d.RouteBuilder.Unmarshal(dataFormat)
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
	d.parent.adoptErr(d.RouteBuilder)
	return d.parent
}
