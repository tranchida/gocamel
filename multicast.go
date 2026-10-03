package gocamel

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

// childProcessors exposes the nested branch processors so the route can wire
// completion timeouts for aggregators nested inside this multicast.
func (m *Multicast) childProcessors() []Processor {
	return m.processors
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
	propagateOutToInAndReset(exchange)
	branches := make([]*Exchange, len(m.processors))
	for i := range branches {
		if m.ParallelProcessing {
			branches[i] = exchange.DeepCopy()
		} else {
			branches[i] = exchange.Copy()
		}
		branches[i].SetProperty(CamelMulticastIndex, i)
		branches[i].SetProperty(CamelMulticastSize, len(branches))
		branches[i].SetProperty(CamelMulticastComplete, i == len(branches)-1)
	}
	aggregated, err := processFanOut(branches, m.ParallelProcessing, 0, func(i int, branch *Exchange) error {
		return m.processors[i].Process(branch)
	}, m.AggregationStrategy)
	if err != nil {
		return err
	}
	if m.AggregationStrategy != nil {
		publishAggregatedResult(exchange, aggregated)
	}
	return nil
}
