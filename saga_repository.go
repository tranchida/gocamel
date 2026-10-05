package gocamel

import (
	"context"
	"errors"
	"maps"
	"time"
)

// ErrSagaNotFound is returned by SagaRepository.Load when no state is stored
// for the requested saga ID.
var ErrSagaNotFound = errors.New("saga not found")

// DefaultSagaListLimit bounds the number of saga IDs returned by
// ListIncomplete in the built-in repositories, so a backlog of stuck sagas
// cannot turn a context start into an unbounded scan. Sagas beyond the bound
// are returned by a later ListIncomplete once earlier ones are resolved
// (compensated and removed); oldest sagas come first.
const DefaultSagaListLimit = 1000

// SagaStatus is the lifecycle status of a persisted saga.
type SagaStatus string

const (
	// SagaRunning: actions are executing; CompletedSteps lists the steps whose
	// action finished and whose compensation is pending if the saga fails.
	SagaRunning SagaStatus = "RUNNING"
	// SagaCompensating: an action failed (or a recovery started) and the
	// compensations of CompletedSteps are being run, last step first.
	SagaCompensating SagaStatus = "COMPENSATING"
	// SagaCompleted is a terminal status: every action succeeded. The built-in
	// processor removes completed sagas; repositories that keep history
	// instead must exclude this status from ListIncomplete.
	SagaCompleted SagaStatus = "COMPLETED"
	// SagaCompensated is a terminal status: every compensation succeeded.
	// Like SagaCompleted it is never listed by ListIncomplete.
	SagaCompensated SagaStatus = "COMPENSATED"
)

// isIncomplete reports whether a saga in this status still has work pending.
func (s SagaStatus) isIncomplete() bool {
	return s == SagaRunning || s == SagaCompensating
}

// CompletedStep records a saga step whose action completed and whose
// compensation has not run yet.
//
// Only CompensationURI survives a process restart: a Go function cannot be
// serialised, so a step declared with ActionFunc is persisted with
// InProcessCompensation set and cannot be compensated by recovery (see
// SagaProcessor.Recover).
type CompletedStep struct {
	// Index is the position of the step in the saga definition.
	Index int `json:"index"`
	// CompensationURI is the endpoint URI of the compensation for steps
	// declared with Action(actionURI, compensationURI).
	CompensationURI string `json:"compensationUri,omitempty"`
	// InProcessCompensation is true when the compensation is an in-process
	// Processor (ActionFunc, or a Processor passed to AddStep) that recovery
	// cannot rebuild after a restart.
	InProcessCompensation bool `json:"inProcessCompensation,omitempty"`
}

// SagaMessage is the message snapshot persisted with a saga checkpoint. On
// recovery it becomes the In message of each compensation exchange.
//
// Persistent repositories store it as JSON: the body and header values must
// be JSON-serialisable, and types change on the way back (numbers become
// float64, []byte becomes a base64 string, structs become map[string]any).
type SagaMessage struct {
	Body    any            `json:"body,omitempty"`
	Headers map[string]any `json:"headers,omitempty"`
}

// SagaState is the persisted state of one saga.
type SagaState struct {
	SagaID string     `json:"sagaId"`
	Status SagaStatus `json:"status"`
	// CompletedSteps lists the steps with a pending compensation, in
	// execution order (compensation runs them last to first).
	CompletedSteps []CompletedStep `json:"completedSteps,omitempty"`
	// Message is the exchange's In message at the last checkpoint.
	Message   SagaMessage `json:"message"`
	UpdatedAt time.Time   `json:"updatedAt"`
}

// clone copies the slice and header map so a stored state does not alias the
// caller's (in-memory repositories would otherwise share live maps).
func (s SagaState) clone() SagaState {
	s.CompletedSteps = append([]CompletedStep(nil), s.CompletedSteps...)
	if s.Message.Headers != nil {
		s.Message.Headers = maps.Clone(s.Message.Headers)
	}
	return s
}

// SagaRepository persists saga state so that the compensations of completed
// steps survive a process crash.
//
// It follows the shape of AggregationRepository and IdempotentRepository
// (context-first methods keyed by a string ID), with Load returning
// ErrSagaNotFound rather than a nil value because SagaState is a value type.
// Implementations must be safe for concurrent use.
type SagaRepository interface {
	// Save persists (inserts or replaces) the saga state. The SagaProcessor
	// calls it after each step whose action completed and after each
	// compensation.
	Save(ctx context.Context, sagaID string, state SagaState) error
	// Load returns the saga state, or ErrSagaNotFound if it is unknown.
	Load(ctx context.Context, sagaID string) (SagaState, error)
	// ListIncomplete returns the IDs of sagas whose status is SagaRunning or
	// SagaCompensating, oldest first. The scan is bounded: implementations
	// return at most an implementation-defined number of IDs
	// (DefaultSagaListLimit for the built-in repositories).
	ListIncomplete(ctx context.Context) ([]string, error)
	// Remove deletes a finished saga's state. Removing an unknown saga is not
	// an error.
	Remove(ctx context.Context, sagaID string) error
}
