package gocamel

import (
	"fmt"
	"time"
)

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

// ActionFunc adds a new action and compensation using in-line Go processing
// functions. A nil compensation means the step has nothing to undo.
//
// Function compensations are in-process only: they are persisted as pending
// but cannot be rebuilt by recovery after a process restart. Use Action with
// a compensation URI for steps that must be recoverable.
func (d *SagaDefinition) ActionFunc(action func(*Exchange) error, compensation func(*Exchange) error) *SagaDefinition {
	var comp Processor
	if compensation != nil {
		comp = ProcessorFunc(compensation)
	}
	d.saga.AddStep(ProcessorFunc(action), comp)
	return d
}

// Repository persists the saga state to repo (default: a MemorySagaRepository,
// lost on process exit). Use a SQLSagaRepository so pending compensations
// survive a crash. A nil repository is a build error.
func (d *SagaDefinition) Repository(repo SagaRepository) *SagaDefinition {
	if repo == nil {
		d.fail(fmt.Errorf("saga: repository must not be nil"))
		return d
	}
	d.saga.SetRepository(repo)
	return d
}

// Timeout bounds each action and each compensation. An action exceeding it
// fails with ErrSagaStepTimeout and the completed steps are compensated; the
// timed-out step itself is not (its outcome is unknown, as for any failed
// step). 0 disables the bound; a negative value is a build error.
func (d *SagaDefinition) Timeout(timeout time.Duration) *SagaDefinition {
	if timeout < 0 {
		d.fail(fmt.Errorf("saga: timeout must not be negative, got %s", timeout))
		return d
	}
	d.saga.SetTimeout(timeout)
	return d
}

// RecoverIncomplete enables compensating, when the CamelContext starts, the
// incomplete sagas left in the repository by a previous run. Off by default:
// incomplete sagas are then only logged. Only compensations declared by URI
// (Action) can be recovered; see SagaProcessor.Recover.
func (d *SagaDefinition) RecoverIncomplete(enabled bool) *SagaDefinition {
	d.saga.SetRecoverIncomplete(enabled)
	return d
}

func (d *SagaDefinition) fail(err error) {
	if d.parent.err == nil {
		d.parent.err = err
	}
}

// End terminates the Saga block and returns to the parent builder.
func (d *SagaDefinition) End() *RouteBuilder {
	return d.parent
}
