package gocamel

import (
	"errors"
	"fmt"
	"strconv"
)

// Loop EIP property constants
const (
	CamelLoopIndex    = "CamelLoopIndex"
	CamelLoopSize     = "CamelLoopSize"
	CamelLoopComplete = "CamelLoopComplete"
)

// LoopProcessor implements the Loop EIP.
// It executes a pipeline of processors repeatedly, either a fixed number of times
// or a dynamic number determined per exchange.
type LoopProcessor struct {
	expression func(*Exchange) (int, error)
	processors []Processor
}

// NewLoopProcessor creates a new LoopProcessor with a dynamic count function.
func NewLoopProcessor(expression func(*Exchange) (int, error)) *LoopProcessor {
	return &LoopProcessor{
		expression: expression,
		processors: make([]Processor, 0),
	}
}

// NewStaticLoopProcessor creates a LoopProcessor with a fixed iteration count.
func NewStaticLoopProcessor(count int) *LoopProcessor {
	return &LoopProcessor{
		expression: func(_ *Exchange) (int, error) { return count, nil },
		processors: make([]Processor, 0),
	}
}

// NewSimpleLoopProcessor creates a LoopProcessor whose iteration count is determined by a Simple expression.
func NewSimpleLoopProcessor(simpleExpression string) (*LoopProcessor, error) {
	template, err := ParseSimpleTemplate(simpleExpression)
	if err != nil {
		return nil, fmt.Errorf("failed to parse loop count expression: %w", err)
	}

	return &LoopProcessor{
		expression: func(e *Exchange) (int, error) {
			val, err := template.Evaluate(e)
			if err != nil {
				return 0, err
			}

			switch v := val.(type) {
			case int:
				return v, nil
			case int64:
				return int(v), nil
			case float64:
				return int(v), nil
			case string:
				i, err := strconv.Atoi(v)
				if err != nil {
					return 0, fmt.Errorf("failed to parse loop count string %q: %w", v, err)
				}
				return i, nil
			default:
				return 0, fmt.Errorf("unsupported loop count type %T", val)
			}
		},
		processors: make([]Processor, 0),
	}, nil
}

// AddProcessor adds a processor to the loop pipeline.
func (l *LoopProcessor) AddProcessor(processor Processor) {
	l.processors = append(l.processors, processor)
}

// Process runs the loop pipeline for the resolved number of times.
// In accordance with standard Camel Loop EIP, it routes the same exchange
// sequentially through the iterations (allowing updates to body/headers to carry over).
func (l *LoopProcessor) Process(exchange *Exchange) error {
	count, err := l.expression(exchange)
	if err != nil {
		return fmt.Errorf("loop: expression error: %w", err)
	}

	if count <= 0 {
		return nil
	}

	for i := 0; i < count; i++ {
		// Set loop state properties
		exchange.SetProperty(CamelLoopIndex, i)
		exchange.SetProperty(CamelLoopSize, count)
		exchange.SetProperty(CamelLoopComplete, i == count-1)

		// Process the exchange through all nested processors in the loop
		for _, p := range l.processors {
			if err := p.Process(exchange); err != nil {
				if errors.Is(err, ErrStopRouting) {
					// Stop EIP stops the loop without propagating a failure
					return nil
				}
				return err
			}
		}
	}

	return nil
}
