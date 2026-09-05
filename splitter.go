package gocamel

import (
	"fmt"
	"reflect"
)

// Splitter is a Processor that implements the Split EIP.
// It divides a message into multiple parts and processes them individually.
type Splitter struct {
	Expression          func(*Exchange) (any, error)
	processors          []Processor
	AggregationStrategy AggregationStrategy
	ParallelProcessing  bool
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

// childProcessors exposes the nested processors so the route can wire
// completion timeouts for aggregators nested inside this splitter.
func (s *Splitter) childProcessors() []Processor {
	return s.processors
}

// SetAggregationStrategy sets the aggregation strategy for collecting results
func (s *Splitter) SetAggregationStrategy(strategy AggregationStrategy) *Splitter {
	s.AggregationStrategy = strategy
	return s
}

// SetParallelProcessing enables concurrent processing of split parts.
func (s *Splitter) SetParallelProcessing(parallel bool) *Splitter {
	s.ParallelProcessing = parallel
	return s
}

// Process executes the split on the provided exchange
func (s *Splitter) Process(exchange *Exchange) error {
	propagateOutToInAndReset(exchange)
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
		splitParts = []any{parts}
	}
	branches := make([]*Exchange, len(splitParts))
	for i, part := range splitParts {
		if s.ParallelProcessing {
			branches[i] = exchange.DeepCopy()
			part = deepCopyValue(part)
		} else {
			branches[i] = exchange.Copy()
		}
		branches[i].In.SetBody(part)
		branches[i].SetProperty(CamelSplitIndex, i)
		branches[i].SetProperty(CamelSplitSize, len(splitParts))
		branches[i].SetProperty(CamelSplitComplete, i == len(splitParts)-1)
	}
	aggregated, err := processFanOut(branches, s.ParallelProcessing, func(_ int, branch *Exchange) error {
		return s.processPart(branch)
	}, s.AggregationStrategy)
	if err != nil {
		return fmt.Errorf("split: %w", err)
	}
	if s.AggregationStrategy != nil {
		publishAggregatedResult(exchange, aggregated)
	}
	return nil
}

func (s *Splitter) processPart(exchange *Exchange) error {
	for _, p := range s.processors {
		propagateOutToInAndReset(exchange)
		if err := p.Process(exchange); err != nil {
			return err
		}
	}
	return nil
}
