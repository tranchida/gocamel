package gocamel

import (
	"fmt"
	"strings"
)

// This file implements the Multicast EIP sub-DSL and the nested Pipeline DSL
// used for grouping processors inside a multicast branch.
//
// The methods on MulticastDefinition and PipelineDefinition are
// type-preserving wrappers around the underlying RouteBuilder methods. They
// exist so that .Multicast().Pipeline().Foo().End().End() stays type-safe
// within their respective contexts — Go does not let an embedded method
// return the embedding type, so each method must be re-declared with the
// correct return type.

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
	default:
		d.err = fmt.Errorf("unsupported RecipientList expression type: %T", expression)
		return d
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

// Marshal adds a dataformat marshal step and stays in the multicast context.
func (d *MulticastDefinition) Marshal(dataFormat DataFormat) *MulticastDefinition {
	d.RouteBuilder.Marshal(dataFormat)
	return d
}

// Unmarshal adds a dataformat unmarshal step and stays in the multicast context.
func (d *MulticastDefinition) Unmarshal(dataFormat DataFormat) *MulticastDefinition {
	d.RouteBuilder.Unmarshal(dataFormat)
	return d
}

// WireTap adds a WireTap step and stays in the multicast context.
func (d *MulticastDefinition) WireTap(uri string) *MulticastDefinition {
	d.RouteBuilder.WireTap(uri)
	return d
}

// End terminates the Multicast block and returns to the parent builder.
func (d *MulticastDefinition) End() *RouteBuilder {
	d.parent.adoptErr(d.RouteBuilder)
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
	default:
		d.err = fmt.Errorf("unsupported RecipientList expression type: %T", expression)
		return d
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

// Marshal adds a dataformat marshal step and stays in the pipeline context.
func (d *PipelineDefinition) Marshal(dataFormat DataFormat) *PipelineDefinition {
	d.RouteBuilder.Marshal(dataFormat)
	return d
}

// Unmarshal adds a dataformat unmarshal step and stays in the pipeline context.
func (d *PipelineDefinition) Unmarshal(dataFormat DataFormat) *PipelineDefinition {
	d.RouteBuilder.Unmarshal(dataFormat)
	return d
}

// WireTap adds a WireTap step and stays in the pipeline context.
func (d *PipelineDefinition) WireTap(uri string) *PipelineDefinition {
	d.RouteBuilder.WireTap(uri)
	return d
}

// End terminates the Pipeline block and returns to the multicast builder.
func (d *PipelineDefinition) End() *MulticastDefinition {
	d.parent.adoptErr(d.RouteBuilder)
	return d.parent
}
