package gocamel

import (
	"errors"
	"fmt"
	"sync"
)

// Multicast is a Processor that implements the Multicast EIP.
// It sends a copy of the message to multiple destinations or processors.
type Multicast struct {
	processors          []Processor
	AggregationStrategy AggregationStrategy
	ParallelProcessing  bool
}

// NewMulticast creates a new Multicast instance
func NewMulticast() *Multicast {
	return &Multicast{
		processors: make([]Processor, 0),
	}
}

// AddProcessor adds a processor (a branch) to the multicast
func (m *Multicast) AddProcessor(processor Processor) {
	m.processors = append(m.processors, processor)
}

// SetAggregationStrategy sets the aggregation strategy for collecting results
func (m *Multicast) SetAggregationStrategy(strategy AggregationStrategy) *Multicast {
	m.AggregationStrategy = strategy
	return m
}

// SetParallelProcessing enables or disables parallel processing
func (m *Multicast) SetParallelProcessing(parallel bool) *Multicast {
	m.ParallelProcessing = parallel
	return m
}

// Process executes the multicast on the provided exchange
func (m *Multicast) Process(exchange *Exchange) error {
	if len(m.processors) == 0 {
		return nil
	}

	if m.ParallelProcessing {
		return m.processParallel(exchange)
	}

	return m.processSequential(exchange)
}

func (m *Multicast) processSequential(exchange *Exchange) error {
	var aggregatedExchange *Exchange

	for i, p := range m.processors {
		// For each branch, create a copy of the original exchange
		branchExchange := exchange.Copy()
		
		// Can add multicast-specific properties
		branchExchange.SetProperty("CamelMulticastIndex", i)
		branchExchange.SetProperty("CamelMulticastSize", len(m.processors))
		branchExchange.SetProperty("CamelMulticastComplete", i == len(m.processors)-1)

		if err := p.Process(branchExchange); err != nil {
			if !errors.Is(err, ErrStopRouting) {
				return err
			}
			// In case of Stop EIP, ignore the error and continue to the next branch
		}

		if m.AggregationStrategy != nil {
			aggregatedExchange = m.AggregationStrategy.Aggregate(aggregatedExchange, branchExchange)
		}
	}

	if m.AggregationStrategy != nil && aggregatedExchange != nil {
		// Update the original exchange with the aggregation result
		exchange.In = aggregatedExchange.In
		exchange.Out = aggregatedExchange.Out
		exchange.Properties = aggregatedExchange.Properties
	}

	return nil
}

func (m *Multicast) processParallel(exchange *Exchange) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var aggregatedExchange *Exchange
	var firstErr error

	for i, p := range m.processors {
		wg.Add(1)
		go func(index int, processor Processor) {
			defer wg.Done()

			branchExchange := exchange.Copy()
			branchExchange.SetProperty("CamelMulticastIndex", index)
			branchExchange.SetProperty("CamelMulticastSize", len(m.processors))
			branchExchange.SetProperty("CamelMulticastComplete", index == len(m.processors)-1)

			err := processor.Process(branchExchange)
			
			mu.Lock()
			defer mu.Unlock()

			if err != nil && !errors.Is(err, ErrStopRouting) {
				if firstErr == nil {
					firstErr = err
				}
				return
			}

			if m.AggregationStrategy != nil {
				aggregatedExchange = m.AggregationStrategy.Aggregate(aggregatedExchange, branchExchange)
			}
		}(i, p)
	}

	wg.Wait()

	if firstErr != nil {
		return fmt.Errorf("multicast parallel error: %w", firstErr)
	}

	if m.AggregationStrategy != nil && aggregatedExchange != nil {
		exchange.In = aggregatedExchange.In
		exchange.Out = aggregatedExchange.Out
		exchange.Properties = aggregatedExchange.Properties
	}

	return nil
}
