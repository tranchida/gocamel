package gocamel

import "fmt"

// This file holds the three Processor types that expose Simple Language
// expressions to the route DSL: SimpleSetBodyProcessor, SimpleSetHeaderProcessor
// and SimpleLanguageProcessor.

// SimpleSetBodyProcessor sets the body using a Simple Expression
type SimpleSetBodyProcessor struct {
	Expression Expression
}

// Process implements the Processor interface for SimpleSetBody
func (p *SimpleSetBodyProcessor) Process(exchange *Exchange) error {
	result, err := p.Expression.Evaluate(exchange)
	if err != nil {
		return err
	}
	exchange.GetOut().SetBody(result)
	exchange.GetOut().SetHeaders(exchange.GetIn().GetHeaders())
	return nil
}

// SimpleSetHeaderProcessor sets a header using a Simple Expression
type SimpleSetHeaderProcessor struct {
	HeaderName string
	Expression Expression
}

// Process implements the Processor interface for SimpleSetHeader
func (p *SimpleSetHeaderProcessor) Process(exchange *Exchange) error {
	result, err := p.Expression.Evaluate(exchange)
	if err != nil {
		return err
	}
	exchange.GetOut().SetHeader(p.HeaderName, result)
	return nil
}

// SimpleLanguageProcessor is a generic processor that evaluates a Simple expression
// and sets the result to the body
type SimpleLanguageProcessor struct {
	Template *SimpleTemplate
}

// Process implements the Processor interface
func (p *SimpleLanguageProcessor) Process(exchange *Exchange) error {
	result, err := p.Template.EvaluateAsString(exchange)
	if err != nil {
		return fmt.Errorf("simple language evaluation error: %w", err)
	}
	exchange.GetOut().SetBody(result)
	exchange.GetOut().SetHeaders(exchange.GetIn().GetHeaders())
	return nil
}
