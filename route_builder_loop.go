package gocamel

import (
	"fmt"
	"time"
)

// This file implements the Loop EIP sub-DSL.

// Loop begins a Loop EIP block with a static count.
func (b *RouteBuilder) Loop(count int) *LoopDefinition {
	l := NewStaticLoopProcessor(count)
	b.container.AddProcessor(l)

	return &LoopDefinition{
		builder: &RouteBuilder{
			context:   b.context,
			route:     b.route,
			container: l,
		},
		parent: b,
		loop:   l,
	}
}

// LoopFunc begins a Loop EIP block with a dynamic count function.
func (b *RouteBuilder) LoopFunc(expression func(*Exchange) (int, error)) *LoopDefinition {
	if expression == nil {
		b.recordErr(fmt.Errorf("LoopFunc: expression function must not be nil"))
		l := NewLoopProcessor(nil)
		return &LoopDefinition{
			builder: &RouteBuilder{
				context:   b.context,
				route:     b.route,
				container: l,
			},
			parent: b,
			loop:   l,
		}
	}

	l := NewLoopProcessor(expression)
	b.container.AddProcessor(l)

	return &LoopDefinition{
		builder: &RouteBuilder{
			context:   b.context,
			route:     b.route,
			container: l,
		},
		parent: b,
		loop:   l,
	}
}

// LoopSimple begins a Loop EIP block determined dynamically by a Simple expression.
func (b *RouteBuilder) LoopSimple(expression string) *LoopDefinition {
	l, err := NewSimpleLoopProcessor(expression)
	if err != nil {
		b.recordErr(err)
		// Isolate chained loop steps onto a detached builder so they cannot leak onto the parent route.
		return &LoopDefinition{
			builder: &RouteBuilder{
				context:   b.context,
				route:     b.route,
				container: NewPipeline(),
			},
			parent: b,
		}
	}
	b.container.AddProcessor(l)

	return &LoopDefinition{
		builder: &RouteBuilder{
			context:   b.context,
			route:     b.route,
			container: l,
		},
		parent: b,
		loop:   l,
	}
}

// LoopDefinition is a fluent sub-DSL for configuring the Loop EIP.
// It wraps an inner RouteBuilder whose container is the LoopProcessor,
// allowing nested operations on each iteration before calling .End().
type LoopDefinition struct {
	builder *RouteBuilder
	parent  *RouteBuilder
	loop    *LoopProcessor
}

// Process adds a processor to the loop container and stays in the loop context.
func (d *LoopDefinition) Process(processor Processor) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Process(processor)
	return d
}

// ProcessRef adds a processor looked up by name from the registry and stays in the loop context.
func (d *LoopDefinition) ProcessRef(name string) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.ProcessRef(name)
	return d
}

// ProcessFunc adds a processing function to the loop container and stays in the loop context.
func (d *LoopDefinition) ProcessFunc(f func(*Exchange) error) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.ProcessFunc(f)
	return d
}

// To adds one or more destination endpoints to the loop container and stays in the loop context.
func (d *LoopDefinition) To(uris ...string) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.To(uris...)
	return d
}

// ToD adds one or more dynamic destination endpoints and stays in the loop context.
func (d *LoopDefinition) ToD(uriTemplates ...string) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.ToD(uriTemplates...)
	return d
}

// RecipientList adds a Recipient List EIP step and stays in the loop context.
func (d *LoopDefinition) RecipientList(expression any) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	if rl, ok := d.builder.buildRecipientList(expression); ok {
		d.builder.container.AddProcessor(rl)
	}
	return d
}

// RoutingSlip adds a Routing Slip EIP step and stays in the loop context.
func (d *LoopDefinition) RoutingSlip(expression any) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.RoutingSlip(expression)
	return d
}

// DynamicRouter adds a Dynamic Router EIP step and stays in the loop context.
func (d *LoopDefinition) DynamicRouter(expression any) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.DynamicRouter(expression)
	return d
}

// SetBody sets the output message body and stays in the loop context.
func (d *LoopDefinition) SetBody(body interface{}) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SetBody(body)
	return d
}

// SetHeader sets a header on the output message and stays in the loop context.
func (d *LoopDefinition) SetHeader(key string, value interface{}) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SetHeader(key, value)
	return d
}

// SimpleSetBody sets the body via a Simple expression and stays in the loop context.
func (d *LoopDefinition) SimpleSetBody(expression string) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SimpleSetBody(expression)
	return d
}

// SimpleSetHeader sets a header via a Simple expression and stays in the loop context.
func (d *LoopDefinition) SimpleSetHeader(headerName, expression string) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SimpleSetHeader(headerName, expression)
	return d
}

// SetHeaders sets multiple headers and stays in the loop context.
func (d *LoopDefinition) SetHeaders(headers map[string]any) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SetHeaders(headers)
	return d
}

// SetHeadersFunc sets multiple headers via a function and stays in the loop context.
func (d *LoopDefinition) SetHeadersFunc(f func(*Exchange) (map[string]any, error)) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SetHeadersFunc(f)
	return d
}

// SetProperty sets a property on the exchange and stays in the loop context.
func (d *LoopDefinition) SetProperty(key string, value any) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SetProperty(key, value)
	return d
}

// SetPropertyFunc sets a property via a function and stays in the loop context.
func (d *LoopDefinition) SetPropertyFunc(key string, f func(*Exchange) (any, error)) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SetPropertyFunc(key, f)
	return d
}

// RemoveProperty removes a property from the exchange and stays in the loop context.
func (d *LoopDefinition) RemoveProperty(key string) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.RemoveProperty(key)
	return d
}

// RemoveProperties removes properties matching the pattern and stays in the loop context.
func (d *LoopDefinition) RemoveProperties(pattern string, excludePatterns ...string) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.RemoveProperties(pattern, excludePatterns...)
	return d
}

// RemoveHeader removes a header from the incoming message and stays in the loop context.
func (d *LoopDefinition) RemoveHeader(name string) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.RemoveHeader(name)
	return d
}

// RemoveHeaders removes headers matching the pattern and stays in the loop context.
func (d *LoopDefinition) RemoveHeaders(pattern string, excludePatterns ...string) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.RemoveHeaders(pattern, excludePatterns...)
	return d
}

// Marshal adds a dataformat marshal step and stays in the loop context.
func (d *LoopDefinition) Marshal(dataFormat DataFormat) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Marshal(dataFormat)
	return d
}

// Unmarshal adds a dataformat unmarshal step and stays in the loop context.
func (d *LoopDefinition) Unmarshal(dataFormat DataFormat) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Unmarshal(dataFormat)
	return d
}

// MarshalJson adds a JSON marshal step and stays in the loop context.
func (d *LoopDefinition) MarshalJson(opts ...JSONOption) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.MarshalJson(opts...)
	return d
}

// UnmarshalJson adds a JSON unmarshal step and stays in the loop context.
func (d *LoopDefinition) UnmarshalJson(opts ...JSONOption) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.UnmarshalJson(opts...)
	return d
}

// MarshalXml adds an XML marshal step and stays in the loop context.
func (d *LoopDefinition) MarshalXml(opts ...XMLOption) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.MarshalXml(opts...)
	return d
}

// UnmarshalXml adds an XML unmarshal step and stays in the loop context.
func (d *LoopDefinition) UnmarshalXml(opts ...XMLOption) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.UnmarshalXml(opts...)
	return d
}

// MarshalCSV adds a CSV marshal step and stays in the loop context.
func (d *LoopDefinition) MarshalCSV(opts ...CSVOption) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.MarshalCSV(opts...)
	return d
}

// UnmarshalCSV adds a CSV unmarshal step and stays in the loop context.
func (d *LoopDefinition) UnmarshalCSV(opts ...CSVOption) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.UnmarshalCSV(opts...)
	return d
}

// MarshalZip adds a ZIP marshal step and stays in the loop context.
func (d *LoopDefinition) MarshalZip(opts ...ZipOption) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.MarshalZip(opts...)
	return d
}

// UnmarshalZip adds a ZIP unmarshal step and stays in the loop context.
func (d *LoopDefinition) UnmarshalZip(opts ...ZipOption) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.UnmarshalZip(opts...)
	return d
}

// MarshalTarGz adds a tar.gz marshal step and stays in the loop context.
func (d *LoopDefinition) MarshalTarGz(opts ...TarGzOption) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.MarshalTarGz(opts...)
	return d
}

// UnmarshalTarGz adds a tar.gz unmarshal step and stays in the loop context.
func (d *LoopDefinition) UnmarshalTarGz(opts ...TarGzOption) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.UnmarshalTarGz(opts...)
	return d
}

// Transform adds a Transform EIP step and stays in the loop context.
func (d *LoopDefinition) Transform(transformFn func(*Exchange) (any, error)) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Transform(transformFn)
	return d
}

// TransformSimple adds a Simple-language Transform EIP step and stays in the loop context.
func (d *LoopDefinition) TransformSimple(expression string) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.TransformSimple(expression)
	return d
}

// Stop halts processing of the current iteration and stays in the loop context.
func (d *LoopDefinition) Stop() *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Stop()
	return d
}

// Filter adds a Message Filter EIP step and stays in the loop context.
func (d *LoopDefinition) Filter(expression any) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Filter(expression)
	return d
}

// IdempotentConsumer adds an Idempotent Consumer EIP step and stays in the loop context.
func (d *LoopDefinition) IdempotentConsumer(expression any, repo IdempotentRepository) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.IdempotentConsumer(expression, repo)
	return d
}

// Delay adds a fixed Delay EIP step and stays in the loop context.
func (d *LoopDefinition) Delay(delay time.Duration) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Delay(delay)
	return d
}

// DelayFunc adds a dynamic Delay EIP step and stays in the loop context.
func (d *LoopDefinition) DelayFunc(fn func(*Exchange) (time.Duration, error)) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.DelayFunc(fn)
	return d
}

// Enrich adds a Content Enricher EIP step and stays in the loop context.
func (d *LoopDefinition) Enrich(uri string, strategy AggregationStrategy) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Enrich(uri, strategy)
	return d
}

// PollEnrich adds a PollEnrich step and stays in the loop context.
func (d *LoopDefinition) PollEnrich(uri string, strategy AggregationStrategy, timeout ...time.Duration) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.PollEnrich(uri, strategy, timeout...)
	return d
}

// PollEnrichWithTimeout adds a PollEnrich step and stays in the loop context.
func (d *LoopDefinition) PollEnrichWithTimeout(uri string, timeout time.Duration) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.PollEnrichWithTimeout(uri, timeout)
	return d
}

// Throttle adds a Throttle EIP step and stays in the loop context.
func (d *LoopDefinition) Throttle(maxRequests int64, timePeriod time.Duration) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Throttle(maxRequests, timePeriod)
	return d
}

// Sample adds a time-window Sampling EIP step and stays in the loop context.
func (d *LoopDefinition) Sample(period time.Duration) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Sample(period)
	return d
}

// SampleEvery adds a count-based Sampling EIP step and stays in the loop context.
func (d *LoopDefinition) SampleEvery(n int64) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SampleEvery(n)
	return d
}

// Validate adds a validation step and stays in the loop context.
func (d *LoopDefinition) Validate(predicate any) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Validate(predicate)
	return d
}

// ValidateFunc adds a function-predicate validation step and stays in the loop context.
func (d *LoopDefinition) ValidateFunc(fn func(*Exchange) bool) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.ValidateFunc(fn)
	return d
}

// ClaimCheck adds a Claim Check EIP step and stays in the loop context.
func (d *LoopDefinition) ClaimCheck(operation ClaimCheckOperation, key string, repo ClaimCheckRepository) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.ClaimCheck(operation, key, repo)
	return d
}

// Aggregate adds an aggregator and stays in the loop context.
func (d *LoopDefinition) Aggregate(aggregator *Aggregator) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Aggregate(aggregator)
	return d
}

// Log adds a log step and stays in the loop context.
func (d *LoopDefinition) Log(message string) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Log(message)
	return d
}

// LogBody adds a body log step and stays in the loop context.
func (d *LoopDefinition) LogBody(message string) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.LogBody(message)
	return d
}

// LogHeaders adds a headers log step and stays in the loop context.
func (d *LoopDefinition) LogHeaders(message string) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.LogHeaders(message)
	return d
}

// LogSimple adds a Simple-expression log step and stays in the loop context.
func (d *LoopDefinition) LogSimple(expression string) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.LogSimple(expression)
	return d
}

// WireTap adds a WireTap step and stays in the loop context.
func (d *LoopDefinition) WireTap(uri string) *LoopDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.WireTap(uri)
	return d
}

// Choice starts a Choice pattern nested inside this loop container.
func (d *LoopDefinition) Choice() *ChoiceBuilder {
	if d == nil || d.builder == nil {
		return nil
	}
	return d.builder.Choice()
}

// End terminates the Loop block and returns to the parent builder.
func (d *LoopDefinition) End() *RouteBuilder {
	if d == nil {
		return nil
	}
	if d.parent != nil {
		d.parent.adoptErr(d.builder)
	}
	return d.parent
}
