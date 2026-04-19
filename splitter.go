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

	// Determine how to iterate over 'parts'
	v := reflect.ValueOf(parts)
	
	// If it's not a slice or array, treat it as a single element
	if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
		partExchange := exchange.Copy()
		partExchange.In.SetBody(parts)
		partExchange.SetProperty("CamelSplitIndex", 0)
		partExchange.SetProperty("CamelSplitSize", 1)
		partExchange.SetProperty("CamelSplitComplete", true)

		if err := s.processPart(partExchange, parts, 0, 1); err != nil {
			if !errors.Is(err, ErrStopRouting) {
				return err
			}
			// In case of Stop EIP, continue to aggregation
		}

		if s.AggregationStrategy != nil {
			aggregatedExchange := s.AggregationStrategy.Aggregate(nil, partExchange)
			if aggregatedExchange != nil {
				exchange.In = aggregatedExchange.In
				exchange.Out = aggregatedExchange.Out
				exchange.Properties = aggregatedExchange.Properties
			}
		}
		return nil
	}

	length := v.Len()
	if length == 0 {
		return nil
	}

	var aggregatedExchange *Exchange
	for i := 0; i < length; i++ {
		part := v.Index(i).Interface()
		
		// For each part, create a copy of the original exchange
		partExchange := exchange.Copy()
		partExchange.In.SetBody(part)
		
		// Can add split-specific properties (index, total, etc.)
		partExchange.SetProperty("CamelSplitIndex", i)
		partExchange.SetProperty("CamelSplitSize", length)
		partExchange.SetProperty("CamelSplitComplete", i == length-1)

		if err := s.processPart(partExchange, part, i, length); err != nil {
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
		exchange.In = aggregatedExchange.In
		exchange.Out = aggregatedExchange.Out
		exchange.Properties = aggregatedExchange.Properties
	}

	return nil
}

func (s *Splitter) processPart(exchange *Exchange, part any, index, size int) error {
	for _, p := range s.processors {
		if err := p.Process(exchange); err != nil {
			return err
		}
	}
	return nil
}
