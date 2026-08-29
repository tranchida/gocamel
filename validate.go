package gocamel

import (
	"errors"
	"fmt"
)

// ErrValidationFailed is returned when a Validate processor predicate evaluates to false.
var ErrValidationFailed = errors.New("predicate validation failed")

// ValidateProcessor evaluates a predicate on each message and fails the exchange
// with ErrValidationFailed if the predicate evaluates to false.
type ValidateProcessor struct {
	predicate any
	template  *SimpleTemplate
}

// NewValidateProcessor creates a new ValidateProcessor with the given predicate.
// The predicate can be a Simple string expression, an Expression, or a func(*Exchange) bool.
func NewValidateProcessor(predicate any) *ValidateProcessor {
	vp := &ValidateProcessor{predicate: predicate}
	if exprStr, ok := predicate.(string); ok {
		if t, err := ParseSimpleTemplate(exprStr); err == nil {
			vp.template = t
		}
	}
	return vp
}

// Validate implements the Validator interface for build-time validation.
func (vp *ValidateProcessor) Validate() error {
	if vp == nil || vp.predicate == nil {
		return fmt.Errorf("validate: predicate is nil")
	}
	if exprStr, ok := vp.predicate.(string); ok {
		if _, err := ParseSimpleTemplate(exprStr); err != nil {
			return fmt.Errorf("validate: invalid simple expression %q: %w", exprStr, err)
		}
	}
	return nil
}

// Process implements Processor.
func (vp *ValidateProcessor) Process(exchange *Exchange) error {
	if vp == nil || vp.predicate == nil {
		return fmt.Errorf("validate: predicate is nil")
	}

	var valid bool
	var evalErr error

	switch p := vp.predicate.(type) {
	case func(*Exchange) bool:
		valid = p(exchange)
	case Expression:
		val, err := p.Evaluate(exchange)
		if err != nil {
			evalErr = err
		} else {
			valid = toBool(val)
		}
	case string:
		template := vp.template
		if template == nil {
			var err error
			template, err = ParseSimpleTemplate(p)
			if err != nil {
				return fmt.Errorf("validate: failed to parse expression %q: %w", p, err)
			}
			vp.template = template
		}
		res, err := template.EvaluateAsBool(exchange)
		if err != nil {
			evalErr = err
		} else {
			valid = res
		}
	default:
		return fmt.Errorf("validate: unsupported predicate type %T", vp.predicate)
	}

	if evalErr != nil {
		return fmt.Errorf("%w: evaluation error: %v", ErrValidationFailed, evalErr)
	}

	if !valid {
		return fmt.Errorf("%w for predicate %v", ErrValidationFailed, vp.predicate)
	}

	return nil
}

// Validate adds a message validation step to the route.
// If the predicate evaluates to false, an error wrapping ErrValidationFailed is returned.
func (b *RouteBuilder) Validate(predicate any) *RouteBuilder {
	vp := NewValidateProcessor(predicate)
	if err := vp.Validate(); err != nil {
		b.err = err
		return b
	}
	b.container.AddProcessor(vp)
	return b
}

// ValidateFunc adds a message validation step using a custom function predicate.
func (b *RouteBuilder) ValidateFunc(fn func(*Exchange) bool) *RouteBuilder {
	return b.Validate(fn)
}
