package gocamel

import (
	"fmt"
)

// ChoiceProcessor implements Content-Based Router pattern with When/Otherwise
type ChoiceProcessor struct {
	whens      []*WhenClause
	otherwise  Processor
}

// WhenClause represents a single when condition with its associated processor
type WhenClause struct {
	Expression string
	template   *SimpleTemplate
	processor  Processor
}

// NewChoiceProcessor creates a new Choice processor
func NewChoiceProcessor() *ChoiceProcessor {
	return &ChoiceProcessor{
		whens: make([]*WhenClause, 0),
	}
}

// When adds a new condition with a processor
func (cp *ChoiceProcessor) When(expression string, processor Processor) *ChoiceProcessor {
	template, err := ParseSimpleTemplate(expression)
	if err != nil {
		// Store the expression; we'll parse it during Process
		cp.whens = append(cp.whens, &WhenClause{
			Expression: expression,
			processor:  processor,
		})
	} else {
		cp.whens = append(cp.whens, &WhenClause{
			Expression: expression,
			template:   template,
			processor:  processor,
		})
	}
	return cp
}

// WhenFunc adds a new condition with a function processor
func (cp *ChoiceProcessor) WhenFunc(expression string, f func(*Exchange) error) *ChoiceProcessor {
	return cp.When(expression, ProcessorFunc(f))
}

// Otherwise sets the default processor when no When conditions match
func (cp *ChoiceProcessor) Otherwise(processor Processor) *ChoiceProcessor {
	cp.otherwise = processor
	return cp
}

// OtherwiseFunc sets the default processor with a function
func (cp *ChoiceProcessor) OtherwiseFunc(f func(*Exchange) error) *ChoiceProcessor {
	return cp.Otherwise(ProcessorFunc(f))
}

// Process implements the Processor interface
func (cp *ChoiceProcessor) Process(exchange *Exchange) error {
	// Evaluate When clauses in order
	for _, when := range cp.whens {
		// Parse template if not already done
		if when.template == nil {
			template, err := ParseSimpleTemplate(when.Expression)
			if err != nil {
				return fmt.Errorf("failed to parse when expression '%s': %w", when.Expression, err)
			}
			when.template = template
		}

		// Evaluate condition
		result, err := when.template.EvaluateAsBool(exchange)
		if err != nil {
			return fmt.Errorf("failed to evaluate when expression '%s': %w", when.Expression, err)
		}

		if result {
			// Execute the processor for this when clause
			return when.processor.Process(exchange)
		}
	}

	// No When clause matched, execute Otherwise if present
	if cp.otherwise != nil {
		return cp.otherwise.Process(exchange)
	}

	// No match and no otherwise - continue silently
	return nil
}

// compositeProcessor processes multiple processors in sequence
type compositeProcessor struct {
	processors []Processor
}

func (cp *compositeProcessor) Process(exchange *Exchange) error {
	for _, p := range cp.processors {
		if err := p.Process(exchange); err != nil {
			return err
		}
	}
	return nil
}

// ChoiceBuilder provides fluent API for building Choice patterns within RouteBuilder
type ChoiceBuilder struct {
	routeBuilder      *RouteBuilder
	choice            *ChoiceProcessor
	activeExpression  string
	activeProcessors  []Processor
	otherwiseProcessors []Processor
}

// Choice starts a Choice pattern in the RouteBuilder
func (b *RouteBuilder) Choice() *ChoiceBuilder {
	choice := NewChoiceProcessor()
	cb := &ChoiceBuilder{
		routeBuilder:        b,
		choice:              choice,
		activeProcessors:    make([]Processor, 0),
		otherwiseProcessors: make([]Processor, 0),
	}
	return cb
}

// finalizeActiveWhen finalizes the currently building When clause
func (cb *ChoiceBuilder) finalizeActiveWhen() {
	if cb.activeExpression != "" && len(cb.activeProcessors) > 0 {
		cb.choice.When(cb.activeExpression, &compositeProcessor{processors: cb.activeProcessors})
		cb.activeExpression = ""
		cb.activeProcessors = make([]Processor, 0)
	}
}

// When adds a When condition to the Choice
func (cb *ChoiceBuilder) When(expression string) *ChoiceBuilder {
	// Finalize any previous when clause first
	cb.finalizeActiveWhen()

	// Start a new when clause
	cb.activeExpression = expression
	cb.activeProcessors = make([]Processor, 0)
	return cb
}

// Process adds a processor to the current When clause
func (cb *ChoiceBuilder) Process(processor Processor) *ChoiceBuilder {
	if cb.activeExpression != "" {
		// We're in a When clause
		cb.activeProcessors = append(cb.activeProcessors, processor)
	} else {
		// We're not in a When clause - panic with helpful message
		panic("Process called without a When clause. Call When() first, or use EndChoice to complete the Choice.")
	}
	return cb
}

// ProcessFunc adds a function processor to the current When clause
func (cb *ChoiceBuilder) ProcessFunc(f func(*Exchange) error) *ChoiceBuilder {
	return cb.Process(ProcessorFunc(f))
}

// SetBody sets the body for the current When clause
func (cb *ChoiceBuilder) SetBody(body interface{}) *ChoiceBuilder {
	return cb.Process(ProcessorFunc(func(exchange *Exchange) error {
		exchange.GetOut().SetBody(body)
		return nil
	}))
}

// SetHeader sets a header for the current When clause
func (cb *ChoiceBuilder) SetHeader(key string, value interface{}) *ChoiceBuilder {
	return cb.Process(ProcessorFunc(func(exchange *Exchange) error {
		exchange.GetOut().SetHeader(key, value)
		return nil
	}))
}

// SimpleSetBody sets the body using a Simple expression for the current When clause
func (cb *ChoiceBuilder) SimpleSetBody(expression string) *ChoiceBuilder {
	template, err := ParseSimpleTemplate(expression)
	if err != nil {
		panic(fmt.Sprintf("failed to parse simple expression: %v", err))
	}
	return cb.Process(&SimpleLanguageProcessor{Template: template})
}

// SimpleSetHeader sets a header using a Simple expression for the current When clause
func (cb *ChoiceBuilder) SimpleSetHeader(headerName string, expression string) *ChoiceBuilder {
	template, err := ParseSimpleTemplate(expression)
	if err != nil {
		panic(fmt.Sprintf("failed to parse simple expression: %v", err))
	}
	return cb.Process(&SimpleSetHeaderProcessor{
		HeaderName: headerName,
		Expression: template,
	})
}

// To sends the exchange to an endpoint for the current When clause
func (cb *ChoiceBuilder) To(uri string) *ChoiceBuilder {
	return cb.Process(ProcessorFunc(func(exchange *Exchange) error {
		exchange.SetProperty("CamelToEndpoint", uri)
		return nil
	}))
}

// Log logs a message for the current When clause
func (cb *ChoiceBuilder) Log(message string) *ChoiceBuilder {
	return cb.ProcessFunc(func(exchange *Exchange) error {
		fmt.Printf("[Choice] %s\n", message)
		return nil
	})
}

// LogBody logs the body for the current When clause
func (cb *ChoiceBuilder) LogBody(message string) *ChoiceBuilder {
	return cb.ProcessFunc(func(exchange *Exchange) error {
		fmt.Printf("[Choice] %s: %v\n", message, exchange.GetIn().GetBody())
		return nil
	})
}

// Otherwise starts the Otherwise clause
func (cb *ChoiceBuilder) Otherwise() *OtherwiseBuilder {
	// Finalize the current When clause before starting Otherwise
	cb.finalizeActiveWhen()
	return &OtherwiseBuilder{
		choiceBuilder: cb,
	}
}

// EndChoice completes the Choice pattern and adds it to the route
func (cb *ChoiceBuilder) EndChoice() *RouteBuilder {
	// Finalize any active when clause
	cb.finalizeActiveWhen()

	// Add Otherwise if any processors were collected
	if len(cb.otherwiseProcessors) > 0 {
		cb.choice.Otherwise(&compositeProcessor{processors: cb.otherwiseProcessors})
	}

	// Add the choice processor to the route
	cb.routeBuilder.route.AddProcessor(cb.choice)
	return cb.routeBuilder
}

// OtherwiseBuilder provides fluent API for the Otherwise clause
type OtherwiseBuilder struct {
	choiceBuilder *ChoiceBuilder
}

// Process adds a processor to the Otherwise clause
func (ob *OtherwiseBuilder) Process(processor Processor) *OtherwiseBuilder {
	ob.choiceBuilder.otherwiseProcessors = append(ob.choiceBuilder.otherwiseProcessors, processor)
	return ob
}

// ProcessFunc adds a function processor to the Otherwise clause
func (ob *OtherwiseBuilder) ProcessFunc(f func(*Exchange) error) *OtherwiseBuilder {
	return ob.Process(ProcessorFunc(f))
}

// SetBody sets the body for the Otherwise clause
func (ob *OtherwiseBuilder) SetBody(body interface{}) *OtherwiseBuilder {
	return ob.Process(ProcessorFunc(func(exchange *Exchange) error {
		exchange.GetOut().SetBody(body)
		return nil
	}))
}

// SetHeader sets a header for the Otherwise clause
func (ob *OtherwiseBuilder) SetHeader(key string, value interface{}) *OtherwiseBuilder {
	return ob.Process(ProcessorFunc(func(exchange *Exchange) error {
		exchange.GetOut().SetHeader(key, value)
		return nil
	}))
}

// SimpleSetBody sets the body using a Simple expression for the Otherwise clause
func (ob *OtherwiseBuilder) SimpleSetBody(expression string) *OtherwiseBuilder {
	template, err := ParseSimpleTemplate(expression)
	if err != nil {
		panic(fmt.Sprintf("failed to parse simple expression: %v", err))
	}
	return ob.Process(&SimpleLanguageProcessor{Template: template})
}

// SimpleSetHeader sets a header using a Simple expression for the Otherwise clause
func (ob *OtherwiseBuilder) SimpleSetHeader(headerName string, expression string) *OtherwiseBuilder {
	template, err := ParseSimpleTemplate(expression)
	if err != nil {
		panic(fmt.Sprintf("failed to parse simple expression: %v", err))
	}
	return ob.Process(&SimpleSetHeaderProcessor{
		HeaderName: headerName,
		Expression: template,
	})
}

// To sends the exchange to an endpoint for the Otherwise clause
func (ob *OtherwiseBuilder) To(uri string) *OtherwiseBuilder {
	return ob.Process(ProcessorFunc(func(exchange *Exchange) error {
		exchange.SetProperty("CamelToEndpoint", uri)
		return nil
	}))
}

// Log logs a message for the Otherwise clause
func (ob *OtherwiseBuilder) Log(message string) *OtherwiseBuilder {
	return ob.ProcessFunc(func(exchange *Exchange) error {
		fmt.Printf("[Choice Otherwise] %s\n", message)
		return nil
	})
}

// LogBody logs the body for the Otherwise clause
func (ob *OtherwiseBuilder) LogBody(message string) *OtherwiseBuilder {
	return ob.ProcessFunc(func(exchange *Exchange) error {
		fmt.Printf("[Choice Otherwise] %s: %v\n", message, exchange.GetIn().GetBody())
		return nil
	})
}

// EndChoice returns to the RouteBuilder
func (ob *OtherwiseBuilder) EndChoice() *RouteBuilder {
	return ob.choiceBuilder.EndChoice()
}
