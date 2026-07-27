package gocamel

import (
	"errors"
	"fmt"
	"reflect"
)

// Splitter is a Processor that implements the Split EIP.
// It divides a message into multiple parts and processes them individually.
type Splitter struct {
	Expression          func(*Exchange) (any, error)
	processors          []Processor
	AggregationStrategy AggregationStrategy
}

// NewSplitter creates a new Splitter instance
func NewSplitter(expression func(*Exchange) (any, error)) *Splitter {
	return &Splitter{
		Expression: expression,
		processors: make([]Processor, 0),
	}
}

// AddProcessor adds a processor for processing each split message part
func (s *Splitter) AddProcessor(processor Processor) {
	s.processors = append(s.processors, processor)
}

// SetAggregationStrategy sets the aggregation strategy for collecting results
func (s *Splitter) SetAggregationStrategy(strategy AggregationStrategy) *Splitter {
	s.AggregationStrategy = strategy
	return s
}

// Process executes the split on the provided exchange
func (s *Splitter) Process(exchange *Exchange) error {
	parts, err := s.Expression(exchange)
	if err != nil {
		return fmt.Errorf("split expression error: %w", err)
	}

	if parts == nil {
		return nil
	}

	// Fast path: check for common slice types to avoid reflection overhead
	var splitParts []any
	isList := true

	switch p := parts.(type) {
	case []any:
		splitParts = p
	case []string:
		splitParts = make([]any, len(p))
		for i, item := range p {
			splitParts[i] = item
		}
	case []int:
		splitParts = make([]any, len(p))
		for i, item := range p {
			splitParts[i] = item
		}
	case []byte:
		// A []byte body is a binary payload, not a collection: splitting it
		// yielded one exchange per byte, so a 10 KB message produced 10 000
		// exchanges. Treat it as a single, indivisible part — matching Apache
		// Camel, which does not split byte[]. Use a Simple/expression split
		// (e.g. on the decoded string) to divide binary content deliberately.
		isList = false
	case []int64:
		splitParts = make([]any, len(p))
		for i, item := range p {
			splitParts[i] = item
		}
	case []float64:
		splitParts = make([]any, len(p))
		for i, item := range p {
			splitParts[i] = item
		}
	default:
		// Fallback to reflection
		v := reflect.ValueOf(parts)
		if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
			isList = false
		} else {
			length := v.Len()
			splitParts = make([]any, length)
			for i := 0; i < length; i++ {
				splitParts[i] = v.Index(i).Interface()
			}
		}
	}

	if !isList {
		partExchange := exchange.Copy()
		partExchange.In.SetBody(parts)
		partExchange.SetProperty(CamelSplitIndex, 0)
		partExchange.SetProperty(CamelSplitSize, 1)
		partExchange.SetProperty(CamelSplitComplete, true)

		if err := s.processPart(partExchange); err != nil {
			if !errors.Is(err, ErrStopRouting) {
				return err
			}
			// In case of Stop EIP, continue to aggregation
		}

		if s.AggregationStrategy != nil {
			aggregatedExchange := s.AggregationStrategy.Aggregate(nil, partExchange)
			if aggregatedExchange != nil {
				publishAggregatedResult(exchange, aggregatedExchange)
			}
		}
		return nil
	}

	length := len(splitParts)
	if length == 0 {
		return nil
	}

	var aggregatedExchange *Exchange
	for i := 0; i < length; i++ {
		part := splitParts[i]

		// For each part, create a copy of the original exchange
		partExchange := exchange.Copy()
		partExchange.In.SetBody(part)

		// Can add split-specific properties (index, total, etc.)
		partExchange.SetProperty(CamelSplitIndex, i)
		partExchange.SetProperty(CamelSplitSize, length)
		partExchange.SetProperty(CamelSplitComplete, i == length-1)

		if err := s.processPart(partExchange); err != nil {
			if !errors.Is(err, ErrStopRouting) {
				return err
			}
			// In case of Stop EIP, ignore the error and continue to the next part
		}

		if s.AggregationStrategy != nil {
			aggregatedExchange = s.AggregationStrategy.Aggregate(aggregatedExchange, partExchange)
		}
	}

	if s.AggregationStrategy != nil && aggregatedExchange != nil {
		// Update the original exchange with the aggregation result
		publishAggregatedResult(exchange, aggregatedExchange)
	}

	return nil
}

func (s *Splitter) processPart(exchange *Exchange) error {
	for _, p := range s.processors {
		if err := p.Process(exchange); err != nil {
			return err
		}
	}
	return nil
}
