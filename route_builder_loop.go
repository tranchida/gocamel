package gocamel

// This file implements the Loop EIP sub-DSL.

// Loop begins a Loop EIP block with a static count.
func (b *RouteBuilder) Loop(count int) *LoopDefinition {
	l := NewStaticLoopProcessor(count)
	b.container.AddProcessor(l)

	return &LoopDefinition{
		RouteBuilder: &RouteBuilder{
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
	l := NewLoopProcessor(expression)
	b.container.AddProcessor(l)

	return &LoopDefinition{
		RouteBuilder: &RouteBuilder{
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
		b.err = err
		// Return empty LoopDefinition with error-carrying parent builder
		return &LoopDefinition{
			RouteBuilder: b,
			parent:       b,
		}
	}
	b.container.AddProcessor(l)

	return &LoopDefinition{
		RouteBuilder: &RouteBuilder{
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
	*RouteBuilder
	parent *RouteBuilder
	loop   *LoopProcessor
}

// Process adds a processor to the loop container and stays in the loop context.
func (d *LoopDefinition) Process(processor Processor) *LoopDefinition {
	d.RouteBuilder.Process(processor)
	return d
}

// ProcessFunc adds a processing function to the loop container and stays in the loop context.
func (d *LoopDefinition) ProcessFunc(f func(*Exchange) error) *LoopDefinition {
	d.RouteBuilder.ProcessFunc(f)
	return d
}

// To adds one or more destination endpoints to the loop container and stays in the loop context.
func (d *LoopDefinition) To(uris ...string) *LoopDefinition {
	d.RouteBuilder.To(uris...)
	return d
}

// ToD adds one or more dynamic destination endpoints and stays in the loop context.
func (d *LoopDefinition) ToD(uriTemplates ...string) *LoopDefinition {
	d.RouteBuilder.ToD(uriTemplates...)
	return d
}

// SetBody sets the output message body and stays in the loop context.
func (d *LoopDefinition) SetBody(body interface{}) *LoopDefinition {
	d.RouteBuilder.SetBody(body)
	return d
}

// SetHeader sets a header on the output message and stays in the loop context.
func (d *LoopDefinition) SetHeader(key string, value interface{}) *LoopDefinition {
	d.RouteBuilder.SetHeader(key, value)
	return d
}

// SimpleSetBody sets the body via a Simple expression and stays in the loop context.
func (d *LoopDefinition) SimpleSetBody(expression string) *LoopDefinition {
	d.RouteBuilder.SimpleSetBody(expression)
	return d
}

// SimpleSetHeader sets a header via a Simple expression and stays in the loop context.
func (d *LoopDefinition) SimpleSetHeader(headerName, expression string) *LoopDefinition {
	d.RouteBuilder.SimpleSetHeader(headerName, expression)
	return d
}

// SetHeaders sets multiple headers and stays in the loop context.
func (d *LoopDefinition) SetHeaders(headers map[string]any) *LoopDefinition {
	d.RouteBuilder.SetHeaders(headers)
	return d
}

// SetHeadersFunc sets multiple headers via a function and stays in the loop context.
func (d *LoopDefinition) SetHeadersFunc(f func(*Exchange) (map[string]any, error)) *LoopDefinition {
	d.RouteBuilder.SetHeadersFunc(f)
	return d
}

// SetProperty sets a property on the exchange and stays in the loop context.
func (d *LoopDefinition) SetProperty(key string, value any) *LoopDefinition {
	d.RouteBuilder.SetProperty(key, value)
	return d
}

// SetPropertyFunc sets a property via a function and stays in the loop context.
func (d *LoopDefinition) SetPropertyFunc(key string, f func(*Exchange) (any, error)) *LoopDefinition {
	d.RouteBuilder.SetPropertyFunc(key, f)
	return d
}

// RemoveProperty removes a property from the exchange and stays in the loop context.
func (d *LoopDefinition) RemoveProperty(key string) *LoopDefinition {
	d.RouteBuilder.RemoveProperty(key)
	return d
}

// RemoveProperties removes properties matching the pattern and stays in the loop context.
func (d *LoopDefinition) RemoveProperties(pattern string, excludePatterns ...string) *LoopDefinition {
	d.RouteBuilder.RemoveProperties(pattern, excludePatterns...)
	return d
}

// RemoveHeader removes a header from the incoming message and stays in the loop context.
func (d *LoopDefinition) RemoveHeader(name string) *LoopDefinition {
	d.RouteBuilder.RemoveHeader(name)
	return d
}

// RemoveHeaders removes headers matching the pattern and stays in the loop context.
func (d *LoopDefinition) RemoveHeaders(pattern string, excludePatterns ...string) *LoopDefinition {
	d.RouteBuilder.RemoveHeaders(pattern, excludePatterns...)
	return d
}

// Stop halts processing of the current iteration and stays in the loop context.
func (d *LoopDefinition) Stop() *LoopDefinition {
	d.RouteBuilder.Stop()
	return d
}

// Log adds a log step and stays in the loop context.
func (d *LoopDefinition) Log(message string) *LoopDefinition {
	d.RouteBuilder.Log(message)
	return d
}

// WireTap adds a WireTap step and stays in the loop context.
func (d *LoopDefinition) WireTap(uri string) *LoopDefinition {
	d.RouteBuilder.WireTap(uri)
	return d
}

// End terminates the Loop block and returns to the parent builder.
func (d *LoopDefinition) End() *RouteBuilder {
	d.parent.adoptErr(d.RouteBuilder)
	return d.parent
}
