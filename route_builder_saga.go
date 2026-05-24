package gocamel

// This file implements the Saga EIP sub-DSL.

// Saga begins a Saga EIP block.
func (b *RouteBuilder) Saga() *SagaDefinition {
	s := NewSagaProcessor()
	b.container.AddProcessor(s)

	return &SagaDefinition{
		parent: b,
		saga:   s,
	}
}

// SagaDefinition is a fluent sub-DSL for configuring the Saga EIP.
type SagaDefinition struct {
	parent *RouteBuilder
	saga   *SagaProcessor
}

// Action adds a new action endpoint and its corresponding compensation endpoint by URI.
func (d *SagaDefinition) Action(actionURI string, compensationURI string) *SagaDefinition {
	action := newLazyProducer(d.parent.context, actionURI)
	comp := newLazyProducer(d.parent.context, compensationURI)
	d.parent.route.lazyProducers = append(d.parent.route.lazyProducers, action, comp)
	d.saga.AddStep(action, comp)
	return d
}

// ActionFunc adds a new action and compensation using in-line Go processing functions.
func (d *SagaDefinition) ActionFunc(action func(*Exchange) error, compensation func(*Exchange) error) *SagaDefinition {
	d.saga.AddStep(ProcessorFunc(action), ProcessorFunc(compensation))
	return d
}

// End terminates the Saga block and returns to the parent builder.
func (d *SagaDefinition) End() *RouteBuilder {
	return d.parent
}
