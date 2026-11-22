package gocamel

import (
	"fmt"
	"time"
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
	if expression == nil {
		b.recordErr(fmt.Errorf("Split: expression function must not be nil"))
		s := NewSplitter(nil)
		return &SplitDefinition{
			builder: &RouteBuilder{
				context:   b.context,
				route:     b.route,
				container: s,
			},
			parent:   b,
			splitter: s,
		}
	}

	s := NewSplitter(expression)
	b.container.AddProcessor(s)

	// Create a new RouteBuilder whose container is the splitter.
	return &SplitDefinition{
		builder: &RouteBuilder{
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
	builder  *RouteBuilder
	parent   *RouteBuilder
	splitter *Splitter
}

// AggregationStrategy sets the aggregation strategy for the splitter.
func (d *SplitDefinition) AggregationStrategy(strategy AggregationStrategy) *SplitDefinition {
	if d == nil || d.splitter == nil {
		return d
	}
	d.splitter.SetAggregationStrategy(strategy)
	return d
}

// ParallelProcessing processes split parts concurrently.
func (d *SplitDefinition) ParallelProcessing(parallel bool) *SplitDefinition {
	if d == nil || d.splitter == nil {
		return d
	}
	d.splitter.SetParallelProcessing(parallel)
	return d
}

// MaxConcurrency bounds the parts processed at the same time with
// ParallelProcessing (default DefaultMaxConcurrency).
func (d *SplitDefinition) MaxConcurrency(n int) *SplitDefinition {
	if d == nil || d.splitter == nil {
		return d
	}
	d.splitter.SetMaxConcurrency(n)
	return d
}

// Process adds a processor to the current container and stays in the split context.
func (d *SplitDefinition) Process(processor Processor) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Process(processor)
	return d
}

// ProcessRef adds a processor looked up by name from the registry and stays in the split context.
func (d *SplitDefinition) ProcessRef(name string) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.ProcessRef(name)
	return d
}

// ProcessFunc adds a processing function to the current container and stays in the split context.
func (d *SplitDefinition) ProcessFunc(f func(*Exchange) error) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.ProcessFunc(f)
	return d
}

// To adds one or more destination endpoints to the current container and stays in the split context.
func (d *SplitDefinition) To(uris ...string) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.To(uris...)
	return d
}

// ToD adds one or more dynamic destination endpoints and stays in the split context.
func (d *SplitDefinition) ToD(uriTemplates ...string) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.ToD(uriTemplates...)
	return d
}

// RecipientList adds a Recipient List EIP that sends each split part to dynamic recipients.
func (d *SplitDefinition) RecipientList(expression any) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	if rl, ok := d.builder.buildRecipientList(expression); ok {
		d.builder.container.AddProcessor(rl)
	}
	return d
}

// RoutingSlip routes each split part through a dynamically resolved sequence of endpoints.
func (d *SplitDefinition) RoutingSlip(expression any) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.RoutingSlip(expression)
	return d
}

// DynamicRouter adds a Dynamic Router EIP step and stays in the split context.
func (d *SplitDefinition) DynamicRouter(expression any) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.DynamicRouter(expression)
	return d
}

// SetBody sets the output message body and stays in the split context.
func (d *SplitDefinition) SetBody(body interface{}) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SetBody(body)
	return d
}

// SetHeader sets a header on the output message and stays in the split context.
func (d *SplitDefinition) SetHeader(key string, value interface{}) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SetHeader(key, value)
	return d
}

// SimpleSetBody sets the body via a Simple expression and stays in the split context.
func (d *SplitDefinition) SimpleSetBody(expression string) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SimpleSetBody(expression)
	return d
}

// SimpleSetHeader sets a header via a Simple expression and stays in the split context.
func (d *SplitDefinition) SimpleSetHeader(headerName, expression string) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SimpleSetHeader(headerName, expression)
	return d
}

// Marshal adds a dataformat marshal step and stays in the split context.
func (d *SplitDefinition) Marshal(dataFormat DataFormat) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Marshal(dataFormat)
	return d
}

// Unmarshal adds a dataformat unmarshal step and stays in the split context.
func (d *SplitDefinition) Unmarshal(dataFormat DataFormat) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Unmarshal(dataFormat)
	return d
}

// MarshalJson adds a JSON marshal step and stays in the split context.
func (d *SplitDefinition) MarshalJson(opts ...JSONOption) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.MarshalJson(opts...)
	return d
}

// UnmarshalJson adds a JSON unmarshal step and stays in the split context.
func (d *SplitDefinition) UnmarshalJson(opts ...JSONOption) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.UnmarshalJson(opts...)
	return d
}

// MarshalXml adds an XML marshal step and stays in the split context.
func (d *SplitDefinition) MarshalXml(opts ...XMLOption) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.MarshalXml(opts...)
	return d
}

// UnmarshalXml adds an XML unmarshal step and stays in the split context.
func (d *SplitDefinition) UnmarshalXml(opts ...XMLOption) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.UnmarshalXml(opts...)
	return d
}

// MarshalCSV adds a CSV marshal step and stays in the split context.
func (d *SplitDefinition) MarshalCSV(opts ...CSVOption) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.MarshalCSV(opts...)
	return d
}

// UnmarshalCSV adds a CSV unmarshal step and stays in the split context.
func (d *SplitDefinition) UnmarshalCSV(opts ...CSVOption) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.UnmarshalCSV(opts...)
	return d
}

// MarshalZip adds a ZIP marshal step and stays in the split context.
func (d *SplitDefinition) MarshalZip(opts ...ZipOption) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.MarshalZip(opts...)
	return d
}

// UnmarshalZip adds a ZIP unmarshal step and stays in the split context.
func (d *SplitDefinition) UnmarshalZip(opts ...ZipOption) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.UnmarshalZip(opts...)
	return d
}

// MarshalTarGz adds a tar.gz marshal step and stays in the split context.
func (d *SplitDefinition) MarshalTarGz(opts ...TarGzOption) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.MarshalTarGz(opts...)
	return d
}

// UnmarshalTarGz adds a tar.gz unmarshal step and stays in the split context.
func (d *SplitDefinition) UnmarshalTarGz(opts ...TarGzOption) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.UnmarshalTarGz(opts...)
	return d
}

// Transform adds a Transform EIP step and stays in the split context.
func (d *SplitDefinition) Transform(transformFn func(*Exchange) (any, error)) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Transform(transformFn)
	return d
}

// TransformSimple adds a Simple-language Transform EIP step and stays in the split context.
func (d *SplitDefinition) TransformSimple(expression string) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.TransformSimple(expression)
	return d
}

// Filter adds a Message Filter EIP step and stays in the split context.
func (d *SplitDefinition) Filter(expression any) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Filter(expression)
	return d
}

// IdempotentConsumer adds an Idempotent Consumer EIP step and stays in the split context.
func (d *SplitDefinition) IdempotentConsumer(expression any, repo IdempotentRepository) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.IdempotentConsumer(expression, repo)
	return d
}

// Delay adds a fixed Delay EIP step and stays in the split context.
func (d *SplitDefinition) Delay(delay time.Duration) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Delay(delay)
	return d
}

// DelayFunc adds a dynamic Delay EIP step and stays in the split context.
func (d *SplitDefinition) DelayFunc(fn func(*Exchange) (time.Duration, error)) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.DelayFunc(fn)
	return d
}

// Enrich adds a Content Enricher EIP step and stays in the split context.
func (d *SplitDefinition) Enrich(uri string, strategy AggregationStrategy) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Enrich(uri, strategy)
	return d
}

// PollEnrich adds a PollEnrich step and stays in the split context.
func (d *SplitDefinition) PollEnrich(uri string, strategy AggregationStrategy, timeout ...time.Duration) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.PollEnrich(uri, strategy, timeout...)
	return d
}

// PollEnrichWithTimeout adds a PollEnrich step and stays in the split context.
func (d *SplitDefinition) PollEnrichWithTimeout(uri string, timeout time.Duration) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.PollEnrichWithTimeout(uri, timeout)
	return d
}

// Throttle adds a Throttle EIP step and stays in the split context.
func (d *SplitDefinition) Throttle(maxRequests int64, timePeriod time.Duration) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Throttle(maxRequests, timePeriod)
	return d
}

// Sample adds a time-window Sampling EIP step and stays in the split context.
func (d *SplitDefinition) Sample(period time.Duration) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Sample(period)
	return d
}

// SampleEvery adds a count-based Sampling EIP step and stays in the split context.
func (d *SplitDefinition) SampleEvery(n int64) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SampleEvery(n)
	return d
}

// Validate adds a validation step and stays in the split context.
func (d *SplitDefinition) Validate(predicate any) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Validate(predicate)
	return d
}

// ValidateFunc adds a function-predicate validation step and stays in the split context.
func (d *SplitDefinition) ValidateFunc(fn func(*Exchange) bool) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.ValidateFunc(fn)
	return d
}

// ClaimCheck adds a Claim Check EIP step and stays in the split context.
func (d *SplitDefinition) ClaimCheck(operation ClaimCheckOperation, key string, repo ClaimCheckRepository) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.ClaimCheck(operation, key, repo)
	return d
}

// Choice starts a Choice pattern nested inside this split container.
func (d *SplitDefinition) Choice() *ChoiceBuilder {
	if d == nil || d.builder == nil {
		return nil
	}
	return d.builder.Choice()
}

// SetHeaders sets multiple headers and stays in the split context.
func (d *SplitDefinition) SetHeaders(headers map[string]any) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SetHeaders(headers)
	return d
}

// SetHeadersFunc sets multiple headers via a function and stays in the split context.
func (d *SplitDefinition) SetHeadersFunc(f func(*Exchange) (map[string]any, error)) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SetHeadersFunc(f)
	return d
}

// SetProperty sets a property on the exchange and stays in the split context.
func (d *SplitDefinition) SetProperty(key string, value any) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SetProperty(key, value)
	return d
}

// SetPropertyFunc sets a property via a function and stays in the split context.
func (d *SplitDefinition) SetPropertyFunc(key string, f func(*Exchange) (any, error)) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.SetPropertyFunc(key, f)
	return d
}

// RemoveProperty removes a property from the exchange and stays in the split context.
func (d *SplitDefinition) RemoveProperty(key string) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.RemoveProperty(key)
	return d
}

// RemoveProperties removes properties matching the given pattern and stays in the split context.
func (d *SplitDefinition) RemoveProperties(pattern string, excludePatterns ...string) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.RemoveProperties(pattern, excludePatterns...)
	return d
}

// RemoveHeader removes a header from the incoming message and stays in the split context.
func (d *SplitDefinition) RemoveHeader(name string) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.RemoveHeader(name)
	return d
}

// RemoveHeaders removes headers matching the given pattern and stays in the split context.
func (d *SplitDefinition) RemoveHeaders(pattern string, excludePatterns ...string) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.RemoveHeaders(pattern, excludePatterns...)
	return d
}

// Stop halts processing of the current part and stays in the split context.
func (d *SplitDefinition) Stop() *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Stop()
	return d
}

// Aggregate adds an aggregator and stays in the split context.
func (d *SplitDefinition) Aggregate(aggregator *Aggregator) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Aggregate(aggregator)
	return d
}

// Log adds a log step and stays in the split context.
func (d *SplitDefinition) Log(message string) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.Log(message)
	return d
}

// LogBody adds a body log step and stays in the split context.
func (d *SplitDefinition) LogBody(message string) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.LogBody(message)
	return d
}

// LogHeaders adds a headers log step and stays in the split context.
func (d *SplitDefinition) LogHeaders(message string) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.LogHeaders(message)
	return d
}

// LogSimple adds a Simple-expression log step and stays in the split context.
func (d *SplitDefinition) LogSimple(expression string) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.LogSimple(expression)
	return d
}

// WireTap adds a WireTap step and stays in the split context.
func (d *SplitDefinition) WireTap(uri string) *SplitDefinition {
	if d == nil || d.builder == nil {
		return d
	}
	d.builder.WireTap(uri)
	return d
}

// End terminates the Split block and returns to the parent builder.
func (d *SplitDefinition) End() *RouteBuilder {
	if d == nil {
		return nil
	}
	if d.parent != nil {
		d.parent.adoptErr(d.builder)
	}
	return d.parent
}
