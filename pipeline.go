package gocamel

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

// childProcessors exposes the nested processors so the route can wire
// completion timeouts for aggregators nested inside this pipeline.
func (p *Pipeline) childProcessors() []Processor {
	return p.processors
}

// Process executes all processors in sequence, propagating Out→In between steps.
// Errors (including the ErrStopRouting control signal) are returned immediately.
func (p *Pipeline) Process(exchange *Exchange) error {
	for _, processor := range p.processors {
		// Propagate Out → In if the previous processor set an output.
		// Headers-only outputs propagate too: the body is only overwritten
		// when the previous processor actually produced one.
		if exchange.Out.Body != nil || len(exchange.Out.Headers) > 0 {
			exchange.PropagateOutToIn()
			// Reuse the Out message by resetting it instead of allocating a new one
			exchange.Out.Body = nil
			exchange.Out.Headers = make(map[string]any)
		}

		if err := processor.Process(exchange); err != nil {
			return err
		}
	}
	return nil
}
