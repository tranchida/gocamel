package gocamel

import "time"

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
		builder: &RouteBuilder{
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
	builder   *RouteBuilder
	parent    *RouteBuilder
	multicast *Multicast
}

// AggregationStrategy sets the aggregation strategy for the multicast.
func (d *MulticastDefinition) AggregationStrategy(strategy AggregationStrategy) *MulticastDefinition {
	if d == nil || d.multicast == nil {
		return d
	}
	d.multicast.SetAggregationStrategy(strategy)
	return d
}

// ParallelProcessing enables parallel processing for the multicast.
func (d *MulticastDefinition) ParallelProcessing() *MulticastDefinition {
	if d == nil || d.multicast == nil {
		return d
	}
	d.multicast.SetParallelProcessing(true)
	return d
}

// Pipeline begins a Pipeline block for grouping processors within a multicast branch.
func (d *MulticastDefinition) Pipeline() *PipelineDefinition {
	if d == nil || d.builder == nil || d.multicast == nil {
		return nil
	}
	p := NewPipeline()
	d.multicast.AddProcessor(p)

	return &PipelineDefinition{
		builder: &RouteBuilder{
			context:   d.builder.context,
			route:     d.builder.route,
			container: p,
		},
		parent: d,
	}
}

// Process adds a processor to the current container and stays in the multicast context.
func (d *MulticastDefinition) Process(processor Processor) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Process(processor)
	return d
}

// ProcessRef adds a processor looked up by name from the registry and stays in the multicast context.
func (d *MulticastDefinition) ProcessRef(name string) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.ProcessRef(name)
	return d
}

// ProcessFunc adds a processing function to the current container and stays in the multicast context.
func (d *MulticastDefinition) ProcessFunc(f func(*Exchange) error) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.ProcessFunc(f)
	return d
}

// To adds one or more destination endpoints to the current container and stays in the multicast context.
func (d *MulticastDefinition) To(uris ...string) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.To(uris...)
	return d
}

// ToD adds one or more dynamic destination endpoints and stays in the multicast context.
func (d *MulticastDefinition) ToD(uriTemplates ...string) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.ToD(uriTemplates...)
	return d
}

// RecipientList sends to dynamically resolved recipients within this multicast branch.
func (d *MulticastDefinition) RecipientList(expression any) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	if rl, ok := d.builder.buildRecipientList(expression); ok {
		d.builder.container.AddProcessor(rl)
	}
	return d
}

// RoutingSlip routes within this multicast branch through a dynamic endpoint sequence.
func (d *MulticastDefinition) RoutingSlip(expression any) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.RoutingSlip(expression)
	return d
}

// DynamicRouter adds a Dynamic Router EIP step and stays in the multicast context.
func (d *MulticastDefinition) DynamicRouter(expression any) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.DynamicRouter(expression)
	return d
}

// SetBody sets the output message body and stays in the multicast context.
func (d *MulticastDefinition) SetBody(body interface{}) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SetBody(body)
	return d
}

// SetHeader sets a header on the output message and stays in the multicast context.
func (d *MulticastDefinition) SetHeader(key string, value interface{}) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SetHeader(key, value)
	return d
}

// SimpleSetBody sets the body via a Simple expression and stays in the multicast context.
func (d *MulticastDefinition) SimpleSetBody(expression string) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SimpleSetBody(expression)
	return d
}

// SimpleSetHeader sets a header via a Simple expression and stays in the multicast context.
func (d *MulticastDefinition) SimpleSetHeader(headerName, expression string) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SimpleSetHeader(headerName, expression)
	return d
}

// SetHeaders sets multiple headers and stays in the multicast context.
func (d *MulticastDefinition) SetHeaders(headers map[string]any) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SetHeaders(headers)
	return d
}

// SetHeadersFunc sets multiple headers via a function and stays in the multicast context.
func (d *MulticastDefinition) SetHeadersFunc(f func(*Exchange) (map[string]any, error)) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SetHeadersFunc(f)
	return d
}

// SetProperty sets a property on the exchange and stays in the multicast context.
func (d *MulticastDefinition) SetProperty(key string, value any) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SetProperty(key, value)
	return d
}

// SetPropertyFunc sets a property via a function and stays in the multicast context.
func (d *MulticastDefinition) SetPropertyFunc(key string, f func(*Exchange) (any, error)) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SetPropertyFunc(key, f)
	return d
}

// RemoveProperty removes a property from the exchange and stays in the multicast context.
func (d *MulticastDefinition) RemoveProperty(key string) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.RemoveProperty(key)
	return d
}

// RemoveProperties removes properties matching the pattern and stays in the multicast context.
func (d *MulticastDefinition) RemoveProperties(pattern string, excludePatterns ...string) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.RemoveProperties(pattern, excludePatterns...)
	return d
}

// RemoveHeader removes a header from the incoming message and stays in the multicast context.
func (d *MulticastDefinition) RemoveHeader(name string) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.RemoveHeader(name)
	return d
}

// RemoveHeaders removes headers matching the pattern and stays in the multicast context.
func (d *MulticastDefinition) RemoveHeaders(pattern string, excludePatterns ...string) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.RemoveHeaders(pattern, excludePatterns...)
	return d
}

// Marshal adds a dataformat marshal step and stays in the multicast context.
func (d *MulticastDefinition) Marshal(dataFormat DataFormat) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Marshal(dataFormat)
	return d
}

// Unmarshal adds a dataformat unmarshal step and stays in the multicast context.
func (d *MulticastDefinition) Unmarshal(dataFormat DataFormat) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Unmarshal(dataFormat)
	return d
}

// MarshalJson adds a JSON marshal step and stays in the multicast context.
func (d *MulticastDefinition) MarshalJson(opts ...JSONOption) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.MarshalJson(opts...)
	return d
}

// UnmarshalJson adds a JSON unmarshal step and stays in the multicast context.
func (d *MulticastDefinition) UnmarshalJson(opts ...JSONOption) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.UnmarshalJson(opts...)
	return d
}

// MarshalXml adds an XML marshal step and stays in the multicast context.
func (d *MulticastDefinition) MarshalXml(opts ...XMLOption) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.MarshalXml(opts...)
	return d
}

// UnmarshalXml adds an XML unmarshal step and stays in the multicast context.
func (d *MulticastDefinition) UnmarshalXml(opts ...XMLOption) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.UnmarshalXml(opts...)
	return d
}

// MarshalCSV adds a CSV marshal step and stays in the multicast context.
func (d *MulticastDefinition) MarshalCSV(opts ...CSVOption) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.MarshalCSV(opts...)
	return d
}

// UnmarshalCSV adds a CSV unmarshal step and stays in the multicast context.
func (d *MulticastDefinition) UnmarshalCSV(opts ...CSVOption) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.UnmarshalCSV(opts...)
	return d
}

// MarshalZip adds a ZIP marshal step and stays in the multicast context.
func (d *MulticastDefinition) MarshalZip(opts ...ZipOption) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.MarshalZip(opts...)
	return d
}

// UnmarshalZip adds a ZIP unmarshal step and stays in the multicast context.
func (d *MulticastDefinition) UnmarshalZip(opts ...ZipOption) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.UnmarshalZip(opts...)
	return d
}

// MarshalTarGz adds a tar.gz marshal step and stays in the multicast context.
func (d *MulticastDefinition) MarshalTarGz(opts ...TarGzOption) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.MarshalTarGz(opts...)
	return d
}

// UnmarshalTarGz adds a tar.gz unmarshal step and stays in the multicast context.
func (d *MulticastDefinition) UnmarshalTarGz(opts ...TarGzOption) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.UnmarshalTarGz(opts...)
	return d
}

// Transform adds a Transform EIP step and stays in the multicast context.
func (d *MulticastDefinition) Transform(transformFn func(*Exchange) (any, error)) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Transform(transformFn)
	return d
}

// TransformSimple adds a Simple-language Transform EIP step and stays in the multicast context.
func (d *MulticastDefinition) TransformSimple(expression string) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.TransformSimple(expression)
	return d
}

// Stop halts processing of the current branch and stays in the multicast context.
func (d *MulticastDefinition) Stop() *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Stop()
	return d
}

// Filter adds a Message Filter EIP step and stays in the multicast context.
func (d *MulticastDefinition) Filter(expression any) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Filter(expression)
	return d
}

// IdempotentConsumer adds an Idempotent Consumer EIP step and stays in the multicast context.
func (d *MulticastDefinition) IdempotentConsumer(expression any, repo IdempotentRepository) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.IdempotentConsumer(expression, repo)
	return d
}

// Delay adds a fixed Delay EIP step and stays in the multicast context.
func (d *MulticastDefinition) Delay(delay time.Duration) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Delay(delay)
	return d
}

// DelayFunc adds a dynamic Delay EIP step and stays in the multicast context.
func (d *MulticastDefinition) DelayFunc(fn func(*Exchange) (time.Duration, error)) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.DelayFunc(fn)
	return d
}

// Enrich adds a Content Enricher EIP step and stays in the multicast context.
func (d *MulticastDefinition) Enrich(uri string, strategy AggregationStrategy) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Enrich(uri, strategy)
	return d
}

// PollEnrich adds a PollEnrich step and stays in the multicast context.
func (d *MulticastDefinition) PollEnrich(uri string, strategy AggregationStrategy, timeout ...time.Duration) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.PollEnrich(uri, strategy, timeout...)
	return d
}

// PollEnrichWithTimeout adds a PollEnrich step and stays in the multicast context.
func (d *MulticastDefinition) PollEnrichWithTimeout(uri string, timeout time.Duration) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.PollEnrichWithTimeout(uri, timeout)
	return d
}

// Throttle adds a Throttle EIP step and stays in the multicast context.
func (d *MulticastDefinition) Throttle(maxRequests int64, timePeriod time.Duration) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Throttle(maxRequests, timePeriod)
	return d
}

// Sample adds a time-window Sampling EIP step and stays in the multicast context.
func (d *MulticastDefinition) Sample(period time.Duration) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Sample(period)
	return d
}

// SampleEvery adds a count-based Sampling EIP step and stays in the multicast context.
func (d *MulticastDefinition) SampleEvery(n int64) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SampleEvery(n)
	return d
}

// Validate adds a validation step and stays in the multicast context.
func (d *MulticastDefinition) Validate(predicate any) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Validate(predicate)
	return d
}

// ValidateFunc adds a function-predicate validation step and stays in the multicast context.
func (d *MulticastDefinition) ValidateFunc(fn func(*Exchange) bool) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.ValidateFunc(fn)
	return d
}

// ClaimCheck adds a Claim Check EIP step and stays in the multicast context.
func (d *MulticastDefinition) ClaimCheck(operation ClaimCheckOperation, key string, repo ClaimCheckRepository) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.ClaimCheck(operation, key, repo)
	return d
}

// Aggregate adds an aggregator and stays in the multicast context.
func (d *MulticastDefinition) Aggregate(aggregator *Aggregator) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Aggregate(aggregator)
	return d
}

// Log adds a log step and stays in the multicast context.
func (d *MulticastDefinition) Log(message string) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Log(message)
	return d
}

// LogBody adds a body log step and stays in the multicast context.
func (d *MulticastDefinition) LogBody(message string) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.LogBody(message)
	return d
}

// LogHeaders adds a headers log step and stays in the multicast context.
func (d *MulticastDefinition) LogHeaders(message string) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.LogHeaders(message)
	return d
}

// LogSimple adds a Simple-expression log step and stays in the multicast context.
func (d *MulticastDefinition) LogSimple(expression string) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.LogSimple(expression)
	return d
}

// WireTap adds a WireTap step and stays in the multicast context.
func (d *MulticastDefinition) WireTap(uri string) *MulticastDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.WireTap(uri)
	return d
}

// Choice starts a Choice pattern nested inside this multicast container.
func (d *MulticastDefinition) Choice() *ChoiceBuilder {
	if d == nil || d.builder == nil {
		return nil
	}
	return d.builder.Choice()
}

// End terminates the Multicast block and returns to the parent builder.
func (d *MulticastDefinition) End() *RouteBuilder {
	if d == nil {
		return nil
	}
	if d.parent != nil {
		d.parent.adoptErr(d.builder)
	}
	return d.parent
}

// PipelineDefinition is a fluent sub-DSL for grouping processors inside a
// multicast branch. Call .End() to return to the MulticastDefinition.
type PipelineDefinition struct {
	builder *RouteBuilder
	parent  *MulticastDefinition
}

// Process adds a processor to the current container and stays in the pipeline context.
func (d *PipelineDefinition) Process(processor Processor) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Process(processor)
	return d
}

// ProcessRef adds a processor looked up by name from the registry and stays in the pipeline context.
func (d *PipelineDefinition) ProcessRef(name string) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.ProcessRef(name)
	return d
}

// ProcessFunc adds a processing function to the current container and stays in the pipeline context.
func (d *PipelineDefinition) ProcessFunc(f func(*Exchange) error) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.ProcessFunc(f)
	return d
}

// To adds one or more destination endpoints to the current container and stays in the pipeline context.
func (d *PipelineDefinition) To(uris ...string) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.To(uris...)
	return d
}

// ToD adds one or more dynamic destination endpoints and stays in the pipeline context.
func (d *PipelineDefinition) ToD(uriTemplates ...string) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.ToD(uriTemplates...)
	return d
}

// RecipientList sends to dynamically resolved recipients within this pipeline branch.
func (d *PipelineDefinition) RecipientList(expression any) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	if rl, ok := d.builder.buildRecipientList(expression); ok {
		d.builder.container.AddProcessor(rl)
	}
	return d
}

// RoutingSlip routes within this pipeline branch through a dynamic endpoint sequence.
func (d *PipelineDefinition) RoutingSlip(expression any) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.RoutingSlip(expression)
	return d
}

// DynamicRouter adds a Dynamic Router EIP step and stays in the pipeline context.
func (d *PipelineDefinition) DynamicRouter(expression any) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.DynamicRouter(expression)
	return d
}

// SetBody sets the output message body and stays in the pipeline context.
func (d *PipelineDefinition) SetBody(body interface{}) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SetBody(body)
	return d
}

// SetHeader sets a header on the output message and stays in the pipeline context.
func (d *PipelineDefinition) SetHeader(key string, value interface{}) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SetHeader(key, value)
	return d
}

// SimpleSetBody sets the body via a Simple expression and stays in the pipeline context.
func (d *PipelineDefinition) SimpleSetBody(expression string) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SimpleSetBody(expression)
	return d
}

// SimpleSetHeader sets a header via a Simple expression and stays in the pipeline context.
func (d *PipelineDefinition) SimpleSetHeader(headerName, expression string) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SimpleSetHeader(headerName, expression)
	return d
}

// SetHeaders sets multiple headers and stays in the pipeline context.
func (d *PipelineDefinition) SetHeaders(headers map[string]any) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SetHeaders(headers)
	return d
}

// SetHeadersFunc sets multiple headers via a function and stays in the pipeline context.
func (d *PipelineDefinition) SetHeadersFunc(f func(*Exchange) (map[string]any, error)) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SetHeadersFunc(f)
	return d
}

// SetProperty sets a property on the exchange and stays in the pipeline context.
func (d *PipelineDefinition) SetProperty(key string, value any) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SetProperty(key, value)
	return d
}

// SetPropertyFunc sets a property via a function and stays in the pipeline context.
func (d *PipelineDefinition) SetPropertyFunc(key string, f func(*Exchange) (any, error)) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SetPropertyFunc(key, f)
	return d
}

// RemoveProperty removes a property from the exchange and stays in the pipeline context.
func (d *PipelineDefinition) RemoveProperty(key string) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.RemoveProperty(key)
	return d
}

// RemoveProperties removes properties matching the pattern and stays in the pipeline context.
func (d *PipelineDefinition) RemoveProperties(pattern string, excludePatterns ...string) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.RemoveProperties(pattern, excludePatterns...)
	return d
}

// RemoveHeader removes a header from the incoming message and stays in the pipeline context.
func (d *PipelineDefinition) RemoveHeader(name string) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.RemoveHeader(name)
	return d
}

// RemoveHeaders removes headers matching the pattern and stays in the pipeline context.
func (d *PipelineDefinition) RemoveHeaders(pattern string, excludePatterns ...string) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.RemoveHeaders(pattern, excludePatterns...)
	return d
}

// Marshal adds a dataformat marshal step and stays in the pipeline context.
func (d *PipelineDefinition) Marshal(dataFormat DataFormat) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Marshal(dataFormat)
	return d
}

// Unmarshal adds a dataformat unmarshal step and stays in the pipeline context.
func (d *PipelineDefinition) Unmarshal(dataFormat DataFormat) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Unmarshal(dataFormat)
	return d
}

// MarshalJson adds a JSON marshal step and stays in the pipeline context.
func (d *PipelineDefinition) MarshalJson(opts ...JSONOption) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.MarshalJson(opts...)
	return d
}

// UnmarshalJson adds a JSON unmarshal step and stays in the pipeline context.
func (d *PipelineDefinition) UnmarshalJson(opts ...JSONOption) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.UnmarshalJson(opts...)
	return d
}

// MarshalXml adds an XML marshal step and stays in the pipeline context.
func (d *PipelineDefinition) MarshalXml(opts ...XMLOption) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.MarshalXml(opts...)
	return d
}

// UnmarshalXml adds an XML unmarshal step and stays in the pipeline context.
func (d *PipelineDefinition) UnmarshalXml(opts ...XMLOption) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.UnmarshalXml(opts...)
	return d
}

// MarshalCSV adds a CSV marshal step and stays in the pipeline context.
func (d *PipelineDefinition) MarshalCSV(opts ...CSVOption) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.MarshalCSV(opts...)
	return d
}

// UnmarshalCSV adds a CSV unmarshal step and stays in the pipeline context.
func (d *PipelineDefinition) UnmarshalCSV(opts ...CSVOption) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.UnmarshalCSV(opts...)
	return d
}

// MarshalZip adds a ZIP marshal step and stays in the pipeline context.
func (d *PipelineDefinition) MarshalZip(opts ...ZipOption) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.MarshalZip(opts...)
	return d
}

// UnmarshalZip adds a ZIP unmarshal step and stays in the pipeline context.
func (d *PipelineDefinition) UnmarshalZip(opts ...ZipOption) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.UnmarshalZip(opts...)
	return d
}

// MarshalTarGz adds a tar.gz marshal step and stays in the pipeline context.
func (d *PipelineDefinition) MarshalTarGz(opts ...TarGzOption) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.MarshalTarGz(opts...)
	return d
}

// UnmarshalTarGz adds a tar.gz unmarshal step and stays in the pipeline context.
func (d *PipelineDefinition) UnmarshalTarGz(opts ...TarGzOption) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.UnmarshalTarGz(opts...)
	return d
}

// Transform adds a Transform EIP step and stays in the pipeline context.
func (d *PipelineDefinition) Transform(transformFn func(*Exchange) (any, error)) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Transform(transformFn)
	return d
}

// TransformSimple adds a Simple-language Transform EIP step and stays in the pipeline context.
func (d *PipelineDefinition) TransformSimple(expression string) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.TransformSimple(expression)
	return d
}

// Stop halts processing of the current pipeline branch and stays in the pipeline context.
func (d *PipelineDefinition) Stop() *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Stop()
	return d
}

// Filter adds a Message Filter EIP step and stays in the pipeline context.
func (d *PipelineDefinition) Filter(expression any) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Filter(expression)
	return d
}

// IdempotentConsumer adds an Idempotent Consumer EIP step and stays in the pipeline context.
func (d *PipelineDefinition) IdempotentConsumer(expression any, repo IdempotentRepository) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.IdempotentConsumer(expression, repo)
	return d
}

// Delay adds a fixed Delay EIP step and stays in the pipeline context.
func (d *PipelineDefinition) Delay(delay time.Duration) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Delay(delay)
	return d
}

// DelayFunc adds a dynamic Delay EIP step and stays in the pipeline context.
func (d *PipelineDefinition) DelayFunc(fn func(*Exchange) (time.Duration, error)) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.DelayFunc(fn)
	return d
}

// Enrich adds a Content Enricher EIP step and stays in the pipeline context.
func (d *PipelineDefinition) Enrich(uri string, strategy AggregationStrategy) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Enrich(uri, strategy)
	return d
}

// PollEnrich adds a PollEnrich step and stays in the pipeline context.
func (d *PipelineDefinition) PollEnrich(uri string, strategy AggregationStrategy, timeout ...time.Duration) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.PollEnrich(uri, strategy, timeout...)
	return d
}

// PollEnrichWithTimeout adds a PollEnrich step and stays in the pipeline context.
func (d *PipelineDefinition) PollEnrichWithTimeout(uri string, timeout time.Duration) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.PollEnrichWithTimeout(uri, timeout)
	return d
}

// Throttle adds a Throttle EIP step and stays in the pipeline context.
func (d *PipelineDefinition) Throttle(maxRequests int64, timePeriod time.Duration) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Throttle(maxRequests, timePeriod)
	return d
}

// Sample adds a time-window Sampling EIP step and stays in the pipeline context.
func (d *PipelineDefinition) Sample(period time.Duration) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Sample(period)
	return d
}

// SampleEvery adds a count-based Sampling EIP step and stays in the pipeline context.
func (d *PipelineDefinition) SampleEvery(n int64) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SampleEvery(n)
	return d
}

// Validate adds a validation step and stays in the pipeline context.
func (d *PipelineDefinition) Validate(predicate any) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Validate(predicate)
	return d
}

// ValidateFunc adds a function-predicate validation step and stays in the pipeline context.
func (d *PipelineDefinition) ValidateFunc(fn func(*Exchange) bool) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.ValidateFunc(fn)
	return d
}

// ClaimCheck adds a Claim Check EIP step and stays in the pipeline context.
func (d *PipelineDefinition) ClaimCheck(operation ClaimCheckOperation, key string, repo ClaimCheckRepository) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.ClaimCheck(operation, key, repo)
	return d
}

// Aggregate adds an aggregator and stays in the pipeline context.
func (d *PipelineDefinition) Aggregate(aggregator *Aggregator) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Aggregate(aggregator)
	return d
}

// Log adds a log step and stays in the pipeline context.
func (d *PipelineDefinition) Log(message string) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Log(message)
	return d
}

// LogBody adds a body log step and stays in the pipeline context.
func (d *PipelineDefinition) LogBody(message string) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.LogBody(message)
	return d
}

// LogHeaders adds a headers log step and stays in the pipeline context.
func (d *PipelineDefinition) LogHeaders(message string) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.LogHeaders(message)
	return d
}

// LogSimple adds a Simple-expression log step and stays in the pipeline context.
func (d *PipelineDefinition) LogSimple(expression string) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.LogSimple(expression)
	return d
}

// WireTap adds a WireTap step and stays in the pipeline context.
func (d *PipelineDefinition) WireTap(uri string) *PipelineDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.WireTap(uri)
	return d
}

// Choice starts a Choice pattern nested inside this pipeline container.
func (d *PipelineDefinition) Choice() *ChoiceBuilder {
	if d == nil || d.builder == nil {
		return nil
	}
	return d.builder.Choice()
}

// End terminates the Pipeline block and returns to the multicast builder.
func (d *PipelineDefinition) End() *MulticastDefinition {
	if d == nil {
		return nil
	}
	if d.parent != nil && d.parent.builder != nil {
		d.parent.builder.adoptErr(d.builder)
	}
	return d.parent
}
