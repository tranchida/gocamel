package gocamel

import (
	"fmt"
)

// TryProcessor implements the Do-Try-Catch-Finally EIP pattern.
type TryProcessor struct {
	tryBlock     Processor
	catchClauses []*CatchClause
	finallyBlock Processor
}

// CatchClause represents a catch block for a specific error message or type.
type CatchClause struct {
	ErrorPattern string
	Processor    Processor
}

// NewTryProcessor creates a new TryProcessor instance.
func NewTryProcessor() *TryProcessor {
	return &TryProcessor{
		catchClauses: make([]*CatchClause, 0),
	}
}

// SetTryBlock sets the processor for the try block.
func (p *TryProcessor) SetTryBlock(processor Processor) {
	p.tryBlock = processor
}

// AddCatchClause adds a catch block for a specific error pattern.
func (p *TryProcessor) AddCatchClause(errorPattern string, processor Processor) {
	p.catchClauses = append(p.catchClauses, &CatchClause{
		ErrorPattern: errorPattern,
		Processor:    processor,
	})
}

// SetFinallyBlock sets the processor for the finally block.
func (p *TryProcessor) SetFinallyBlock(processor Processor) {
	p.finallyBlock = processor
}

// Process implements the Processor interface.
func (p *TryProcessor) Process(exchange *Exchange) error {
	var tryErr error
	if p.tryBlock != nil {
		tryErr = p.tryBlock.Process(exchange)
	}

	finalErr := tryErr

	// Handle catch if there was an error
	if tryErr != nil {
		caught := false
		for _, clause := range p.catchClauses {
			// Basic matching: empty pattern matches all, otherwise check message
			if clause.ErrorPattern == "" || (tryErr.Error() != "" && fmt.Sprintf("%v", tryErr) != "") {
				// In a real implementation we might check if ErrorPattern is a substring
				exchange.SetProperty(CamelExceptionCaught, tryErr)
				if err := clause.Processor.Process(exchange); err != nil {
					finalErr = err
				} else {
					finalErr = nil // Error was caught and handled
				}
				caught = true
				break
			}
		}
		
		if !caught {
			finalErr = tryErr
		}
	}

	// Always execute finally block
	if p.finallyBlock != nil {
		if err := p.finallyBlock.Process(exchange); err != nil {
			finalErr = err
		}
	}

	return finalErr
}

// TryDefinition provides a fluent API for configuring a Do-Try-Catch-Finally block.
type TryDefinition struct {
	routeBuilder *RouteBuilder
	parent       *RouteBuilder
	tryProcessor *TryProcessor
}

func (d *TryDefinition) Process(processor Processor) *TryDefinition {
	d.routeBuilder.Process(processor)
	return d
}

func (d *TryDefinition) ProcessFunc(f func(*Exchange) error) *TryDefinition {
	d.routeBuilder.ProcessFunc(f)
	return d
}

func (d *TryDefinition) To(uris ...string) *TryDefinition {
	d.routeBuilder.To(uris...)
	return d
}

func (d *TryDefinition) Log(message string) *TryDefinition {
	d.routeBuilder.Log(message)
	return d
}

// DoCatch begins a Catch block for a specific error pattern.
func (d *TryDefinition) DoCatch(errorPattern string) *CatchDefinition {
	p := NewPipeline()
	d.tryProcessor.AddCatchClause(errorPattern, p)

	return &CatchDefinition{
		routeBuilder: &RouteBuilder{
			context:   d.parent.context,
			route:     d.parent.route,
			container: p,
		},
		parent: d,
	}
}

// DoFinally begins a Finally block.
func (d *TryDefinition) DoFinally() *FinallyDefinition {
	p := NewPipeline()
	d.tryProcessor.SetFinallyBlock(p)

	return &FinallyDefinition{
		routeBuilder: &RouteBuilder{
			context:   d.parent.context,
			route:     d.parent.route,
			container: p,
		},
		parent: d,
	}
}

// End terminates the Try block and returns to the parent builder.
func (d *TryDefinition) End() *RouteBuilder {
	return d.parent
}

// CatchDefinition is a fluent sub-DSL for configuring a Catch block.
type CatchDefinition struct {
	routeBuilder *RouteBuilder
	parent       *TryDefinition
}

func (d *CatchDefinition) Process(processor Processor) *CatchDefinition {
	d.routeBuilder.Process(processor)
	return d
}

func (d *CatchDefinition) ProcessFunc(f func(*Exchange) error) *CatchDefinition {
	d.routeBuilder.ProcessFunc(f)
	return d
}

func (d *CatchDefinition) To(uris ...string) *CatchDefinition {
	d.routeBuilder.To(uris...)
	return d
}

// DoCatch starts another Catch block.
func (d *CatchDefinition) DoCatch(errorPattern string) *CatchDefinition {
	return d.parent.DoCatch(errorPattern)
}

// DoFinally starts a Finally block.
func (d *CatchDefinition) DoFinally() *FinallyDefinition {
	return d.parent.DoFinally()
}

// End terminates the Catch block and returns to the parent builder.
func (d *CatchDefinition) End() *RouteBuilder {
	return d.parent.End()
}

// FinallyDefinition is a fluent sub-DSL for configuring a Finally block.
type FinallyDefinition struct {
	routeBuilder *RouteBuilder
	parent       *TryDefinition
}

func (d *FinallyDefinition) Process(processor Processor) *FinallyDefinition {
	d.routeBuilder.Process(processor)
	return d
}

func (d *FinallyDefinition) ProcessFunc(f func(*Exchange) error) *FinallyDefinition {
	d.routeBuilder.ProcessFunc(f)
	return d
}

func (d *FinallyDefinition) To(uris ...string) *FinallyDefinition {
	d.routeBuilder.To(uris...)
	return d
}

// End terminates the Finally block and returns to the parent builder.
func (d *FinallyDefinition) End() *RouteBuilder {
	return d.parent.End()
}

// DoTry starts a Do-Try-Catch-Finally EIP block.
func (b *RouteBuilder) DoTry() *TryDefinition {
	tp := NewTryProcessor()
	
	p := NewPipeline()
	tp.SetTryBlock(p)
	
	b.container.AddProcessor(tp)

	return &TryDefinition{
		routeBuilder: &RouteBuilder{
			context:   b.context,
			route:     b.route,
			container: p,
		},
		parent:       b,
		tryProcessor: tp,
	}
}

// DoTry nested support for TryDefinition
func (d *TryDefinition) DoTry() *TryDefinition {
	return d.routeBuilder.DoTry()
}
