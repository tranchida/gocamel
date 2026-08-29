package gocamel

import (
	"fmt"
)

// ChoiceProcessor implements Content-Based Router pattern with When/Otherwise
type ChoiceProcessor struct {
	whens     []*WhenClause
	otherwise Processor
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
		cp.whens = append(cp.whens, &WhenClause{
			Expression: expression,
			processor:  processor,
		})
		return cp
	}
	cp.whens = append(cp.whens, &WhenClause{
		Expression: expression,
		template:   template,
		processor:  processor,
	})
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

// childProcessors exposes the processors of every When clause and the
// Otherwise clause so the route can wire completion timeouts for aggregators
// nested inside a Choice branch.
func (cp *ChoiceProcessor) childProcessors() []Processor {
	var out []Processor
	for _, when := range cp.whens {
		if when.processor != nil {
			out = append(out, when.processor)
		}
	}
	if cp.otherwise != nil {
		out = append(out, cp.otherwise)
	}
	return out
}

// Validate pre-parses every When-clause expression so a malformed Simple
// template is reported at build time (typically from Route.Validate) instead
// of at the first matching message. Returns the first parse failure.
func (cp *ChoiceProcessor) Validate() error {
	for i, when := range cp.whens {
		if when.template != nil {
			continue
		}
		template, err := ParseSimpleTemplate(when.Expression)
		if err != nil {
			return fmt.Errorf("choice when[%d] %q: %w", i, when.Expression, err)
		}
		when.template = template
	}
	return nil
}

// Process implements the Processor interface
func (cp *ChoiceProcessor) Process(exchange *Exchange) error { // Evaluate When clauses in order
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

// childProcessors exposes the nested processors of a When/Otherwise branch.
func (cp *compositeProcessor) childProcessors() []Processor {
	return cp.processors
}

// ChoiceBuilder provides a fluent API for building Choice/When/Otherwise patterns.
// Use .When(expression) to add conditions, chain DSL methods for each branch,
// then call .Otherwise() for the fallback or .EndChoice() to finish.
// Errors during DSL construction are accumulated and surfaced at EndChoice().
type ChoiceBuilder struct {
	routeBuilder        *RouteBuilder
	choice              *ChoiceProcessor
	activeExpression    string
	activeProcessors    []Processor
	otherwiseProcessors []Processor
	err                 error // accumulated during DSL construction, surfaced at EndChoice().
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
	cb.finalizeActiveWhen()
	if _, err := ParseSimpleTemplate(expression); err != nil {
		cb.err = fmt.Errorf("choice When: failed to parse %q: %w", expression, err)
		return cb
	}
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
		// We're not in a When clause - record error with helpful message
		cb.err = fmt.Errorf("Process called without a When clause. Call When() first, or use EndChoice to complete the Choice")
	}
	return cb
}

// ProcessFunc adds a function processor to the current When clause
func (cb *ChoiceBuilder) ProcessFunc(f func(*Exchange) error) *ChoiceBuilder {
	return cb.Process(ProcessorFunc(f))
}

// SetBody sets the body for the current When clause
func (cb *ChoiceBuilder) SetBody(body interface{}) *ChoiceBuilder {
	return cb.Process(setBodyProcessor(body))
}

// SetHeader sets a header for the current When clause
func (cb *ChoiceBuilder) SetHeader(key string, value interface{}) *ChoiceBuilder {
	return cb.Process(setHeaderProcessor(key, value))
}

// SimpleSetBody sets the body using a Simple expression for the current When clause
func (cb *ChoiceBuilder) SimpleSetBody(expression string) *ChoiceBuilder {
	template, err := ParseSimpleTemplate(expression)
	if err != nil {
		cb.err = fmt.Errorf("failed to parse simple expression: %w", err)
		return cb
	}
	return cb.Process(&SimpleLanguageProcessor{Template: template})
}

// SimpleSetHeader sets a header using a Simple expression for the current When clause
func (cb *ChoiceBuilder) SimpleSetHeader(headerName string, expression string) *ChoiceBuilder {
	template, err := ParseSimpleTemplate(expression)
	if err != nil {
		cb.err = fmt.Errorf("failed to parse simple expression: %w", err)
		return cb
	}
	return cb.Process(&SimpleSetHeaderProcessor{
		HeaderName: headerName,
		Expression: template,
	})
}

// To sends the exchange to an endpoint for the current When clause
func (cb *ChoiceBuilder) To(uri string) *ChoiceBuilder {
	lp := newLazyProducer(cb.routeBuilder.context, uri)
	cb.routeBuilder.route.lazyProducers = append(cb.routeBuilder.route.lazyProducers, lp)
	return cb.Process(lp)
}

// Log logs a message for the current When clause
func (cb *ChoiceBuilder) Log(message string) *ChoiceBuilder {
	return cb.ProcessFunc(func(exchange *Exchange) error {
		LogInfo("Choice log", "message", message)
		return nil
	})
}

// LogBody logs the body for the current When clause
func (cb *ChoiceBuilder) LogBody(message string) *ChoiceBuilder {
	return cb.ProcessFunc(func(exchange *Exchange) error {
		LogInfo("Choice log", "message", message, "body", exchange.GetIn().GetBody())
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

func (cb *ChoiceBuilder) EndChoice() *RouteBuilder {
	if cb.err != nil {
		cb.routeBuilder.err = cb.err
		return cb.routeBuilder
	}

	cb.finalizeActiveWhen()

	if len(cb.otherwiseProcessors) > 0 {
		cb.choice.Otherwise(&compositeProcessor{processors: cb.otherwiseProcessors})
	}

	cb.routeBuilder.container.AddProcessor(cb.choice)
	return cb.routeBuilder
}

// OtherwiseBuilder provides a fluent API for the Otherwise (fallback) clause
// of a Choice pattern. Chain DSL methods, then call .EndChoice() to finish.
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
	return ob.Process(setBodyProcessor(body))
}

// SetHeader sets a header for the Otherwise clause
func (ob *OtherwiseBuilder) SetHeader(key string, value interface{}) *OtherwiseBuilder {
	return ob.Process(setHeaderProcessor(key, value))
}

// SimpleSetBody sets the body using a Simple expression for the Otherwise clause
func (ob *OtherwiseBuilder) SimpleSetBody(expression string) *OtherwiseBuilder {
	template, err := ParseSimpleTemplate(expression)
	if err != nil {
		ob.choiceBuilder.err = fmt.Errorf("failed to parse simple expression: %w", err)
		return ob
	}
	return ob.Process(&SimpleLanguageProcessor{Template: template})
}

// SimpleSetHeader sets a header using a Simple expression for the Otherwise clause
func (ob *OtherwiseBuilder) SimpleSetHeader(headerName string, expression string) *OtherwiseBuilder {
	template, err := ParseSimpleTemplate(expression)
	if err != nil {
		ob.choiceBuilder.err = fmt.Errorf("failed to parse simple expression: %w", err)
		return ob
	}
	return ob.Process(&SimpleSetHeaderProcessor{
		HeaderName: headerName,
		Expression: template,
	})
}

// To sends the exchange to an endpoint for the Otherwise clause
func (ob *OtherwiseBuilder) To(uri string) *OtherwiseBuilder {
	lp := newLazyProducer(ob.choiceBuilder.routeBuilder.context, uri)
	ob.choiceBuilder.routeBuilder.route.lazyProducers = append(ob.choiceBuilder.routeBuilder.route.lazyProducers, lp)
	return ob.Process(lp)
}

// Log logs a message for the Otherwise clause
func (ob *OtherwiseBuilder) Log(message string) *OtherwiseBuilder {
	return ob.ProcessFunc(func(exchange *Exchange) error {
		LogInfo("Choice otherwise log", "message", message)
		return nil
	})
}

// LogBody logs the body for the Otherwise clause
func (ob *OtherwiseBuilder) LogBody(message string) *OtherwiseBuilder {
	return ob.ProcessFunc(func(exchange *Exchange) error {
		LogInfo("Choice otherwise log", "message", message, "body", exchange.GetIn().GetBody())
		return nil
	})
}

// EndChoice returns to the RouteBuilder
func (ob *OtherwiseBuilder) EndChoice() *RouteBuilder {
	return ob.choiceBuilder.EndChoice()
}
