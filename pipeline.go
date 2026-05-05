package gocamel

import (
	"errors"
)

// Pipeline is a Processor that executes a series of Processors sequentially.
// It propagates the Out message as the next In between steps, mirroring
// Apache Camel's message flow. Used for grouping processors inside a Multicast branch.
type Pipeline struct {
	processors []Processor
}

// NewPipeline creates a new Pipeline instance.
func NewPipeline() *Pipeline {
	return &Pipeline{
		processors: make([]Processor, 0),
	}
}

// AddProcessor adds a processor to the pipeline.
func (p *Pipeline) AddProcessor(processor Processor) {
	p.processors = append(p.processors, processor)
}

// Process executes all processors in sequence, propagating Out→In between steps.
// Returns ErrStopRouting immediately to abort processing, per EIP Stop semantics.
func (p *Pipeline) Process(exchange *Exchange) error {
	for _, processor := range p.processors {
		// Propagate Out → In if the previous processor set an output
		if exchange.HasOut() {
			exchange.In.SetBody(exchange.Out.GetBody())
			exchange.In.SetHeaders(exchange.Out.GetHeaders())
			// Reuse the Out message by resetting it instead of allocating a new one
			exchange.Out.Body = nil
			exchange.Out.Headers = make(map[string]any)
		}

		if err := processor.Process(exchange); err != nil {
			if errors.Is(err, ErrStopRouting) {
				return err // propagate Stop signal
			}
			return err
		}
	}
	return nil
}