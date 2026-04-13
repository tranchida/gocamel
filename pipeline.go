package gocamel

// Pipeline est un Processor qui exécute une série de processeurs de manière séquentielle.
// Il est utilisé pour grouper des processeurs, par exemple dans une branche de Multicast.
type Pipeline struct {
	processors []Processor
}

// NewPipeline crée une nouvelle instance de Pipeline
func NewPipeline() *Pipeline {
	return &Pipeline{
		processors: make([]Processor, 0),
	}
}

// AddProcessor ajoute un processeur au pipeline
func (p *Pipeline) AddProcessor(processor Processor) {
	p.processors = append(p.processors, processor)
}

// Process exécute tous les processeurs du pipeline
func (p *Pipeline) Process(exchange *Exchange) error {
	for _, processor := range p.processors {
		// Propagation de la sortie vers l'entrée si une modification a eu lieu
		if exchange.HasOut() {
			exchange.In.SetBody(exchange.Out.GetBody())
			exchange.In.SetHeaders(exchange.Out.GetHeaders())
			exchange.Out = NewMessage() // Reset Out pour le prochain processeur
		}

		if err := processor.Process(exchange); err != nil {
			return err
		}
	}
	return nil
}
