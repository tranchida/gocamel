package gocamel

import (
	"fmt"
)

// SagaStep represents an individual action and its compensating step in a Saga.
type SagaStep struct {
	Action       Processor
	Compensation Processor
}

// SagaProcessor orchestrates the Saga EIP.
// It runs a sequence of processors (actions) one by one.
// If any action returns an error, the orchestrator triggers the rollback sequence,
// running the compensations of all already-completed steps in reverse order (LIFO).
type SagaProcessor struct {
	steps []SagaStep
}

// NewSagaProcessor creates a new SagaProcessor.
func NewSagaProcessor() *SagaProcessor {
	return &SagaProcessor{
		steps: make([]SagaStep, 0),
	}
}

// AddStep adds a new action and compensation step to the Saga.
func (s *SagaProcessor) AddStep(action, compensation Processor) {
	s.steps = append(s.steps, SagaStep{
		Action:       action,
		Compensation: compensation,
	})
}

// childProcessors exposes the nested action and compensation processors so the
// route can wire completion timeouts for aggregators nested inside this saga.
func (s *SagaProcessor) childProcessors() []Processor {
	procs := make([]Processor, 0, len(s.steps)*2)
	for _, step := range s.steps {
		if step.Action != nil {
			procs = append(procs, step.Action)
		}
		if step.Compensation != nil {
			procs = append(procs, step.Compensation)
		}
	}
	return procs
}

// Process runs the Saga.
// If an action fails, it catches the error, triggers compensating steps for all
// completed actions in reverse order (LIFO), and returns the action error.
func (s *SagaProcessor) Process(exchange *Exchange) error {
	var completedCompensations []Processor

	for _, step := range s.steps {
		// Run the action
		err := step.Action.Process(exchange)
		if err != nil {
			// An action failed! Execute compensations in LIFO order
			for i := len(completedCompensations) - 1; i >= 0; i-- {
				comp := completedCompensations[i]
				// We run compensation on a copy of the exchange to avoid contaminating the failure state
				compExchange := exchange.Copy()
				// A failing or panicking compensation must not prevent the remaining ones from
				// running, but it MUST be visible to operators.
				if compErr := safeProcess(func() error { return comp.Process(compExchange) }); compErr != nil {
					LogError("saga: compensation failed",
						"compensation_index", i,
						"error", compErr,
						"original_error", err,
					)
				}
			}
			return fmt.Errorf("saga action failed: %w", err)
		}

		// Success! Save the compensation for rollback if a future step fails
		if step.Compensation != nil {
			completedCompensations = append(completedCompensations, step.Compensation)
		}
	}

	return nil
}
