package gocamel

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// CamelSagaId is the header carrying the ID under which a saga's state is
// persisted. A saga uses the incoming value, or generates a UUID and sets the
// header when it is absent.
const CamelSagaId = "CamelSagaId"

// ErrSagaStepTimeout is returned (wrapping context.DeadlineExceeded) when a
// saga action exceeds the timeout configured with SagaDefinition.Timeout.
var ErrSagaStepTimeout = errors.New("saga step timed out")

// ErrSagaCompensationNotRecoverable is reported by SagaProcessor.Recover for a
// persisted step whose compensation is an in-process Go function (ActionFunc):
// a function cannot be serialised, so it cannot be rebuilt after a restart.
var ErrSagaCompensationNotRecoverable = errors.New("saga compensation cannot be rebuilt after a restart")

// SagaStep represents an individual action and its compensating step in a Saga.
type SagaStep struct {
	Action       Processor
	Compensation Processor
}

// SagaProcessor orchestrates the Saga EIP.
// It runs a sequence of processors (actions) one by one.
// If any action returns an error, the orchestrator triggers the rollback sequence,
// running the compensations of all already-completed steps in reverse order (LIFO).
//
// Persistence: after each action whose step has a compensation, the saga
// state (pending compensations plus a snapshot of the In message) is saved to
// the SagaRepository under the CamelSagaId header. A crash between an action
// and its checkpoint loses that step, so a crash right after a checkpoint and
// before the next action is the worst case: compensation on recovery is
// at-least-once, and compensations must be idempotent.
//
// Only compensations declared by endpoint URI (SagaDefinition.Action) can be
// run by Recover after a process restart; ActionFunc compensations are
// in-process only.
//
// The configuration (repository, timeout, recovery) must be set before the
// route starts; it is read without locking while exchanges are processed.
type SagaProcessor struct {
	steps             []SagaStep
	repository        SagaRepository
	timeout           time.Duration
	recoverIncomplete bool
}

// NewSagaProcessor creates a new SagaProcessor backed by a MemorySagaRepository.
func NewSagaProcessor() *SagaProcessor {
	return &SagaProcessor{
		steps:      make([]SagaStep, 0),
		repository: NewMemorySagaRepository(),
	}
}

// AddStep adds a new action and compensation step to the Saga.
func (s *SagaProcessor) AddStep(action, compensation Processor) {
	s.steps = append(s.steps, SagaStep{
		Action:       action,
		Compensation: compensation,
	})
}

// SetRepository sets the repository the saga state is persisted to.
func (s *SagaProcessor) SetRepository(repo SagaRepository) {
	s.repository = repo
}

// SetTimeout bounds each action and compensation (0 disables the bound).
func (s *SagaProcessor) SetTimeout(timeout time.Duration) {
	s.timeout = timeout
}

// SetRecoverIncomplete enables compensating the incomplete sagas found in
// the repository when the CamelContext starts.
func (s *SagaProcessor) SetRecoverIncomplete(enabled bool) {
	s.recoverIncomplete = enabled
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
	sagaID, release := acquireSagaID(exchange)
	defer release()
	// Persisting must outlive a cancelled request context: the compensation
	// state is exactly what is needed when the caller gave up.
	ctx := context.WithoutCancel(exchangeContext(exchange))
	state := SagaState{SagaID: sagaID, Status: SagaRunning}
	saved := false

	for i, step := range s.steps {
		if step.Action == nil {
			continue
		}
		err := s.run(step.Action, exchange, true)
		if err != nil {
			if errors.Is(err, ErrStopRouting) {
				if saved {
					s.removeState(ctx, sagaID)
				}
				return err
			}
			s.compensate(ctx, exchange, &state, saved, err)
			return fmt.Errorf("saga action failed: %w", err)
		}

		if step.Compensation == nil {
			continue
		}
		state.CompletedSteps = append(state.CompletedSteps, completedStep(i, step.Compensation))
		if err := s.checkpoint(ctx, &state, exchange); err != nil {
			s.compensate(ctx, exchange, &state, false, err)
			return fmt.Errorf("saga %s: checkpoint after step %d failed: %w", sagaID, i, err)
		}
		saved = true
	}

	if saved {
		s.removeState(ctx, sagaID)
	}
	return nil
}

// compensate runs the pending compensations last to first. Each one gets a
// copy of the exchange. A failed compensation stays in the persisted state so
// a later Recover can retry it; the saga state is removed once nothing is left.
func (s *SagaProcessor) compensate(ctx context.Context, exchange *Exchange, state *SagaState, persist bool, cause error) {
	if len(state.CompletedSteps) == 0 {
		return
	}
	state.Status = SagaCompensating
	if persist {
		s.saveState(ctx, state)
	}
	var failed []CompletedStep
	for i := len(state.CompletedSteps) - 1; i >= 0; i-- {
		completed := state.CompletedSteps[i]
		comp := s.steps[completed.Index].Compensation
		if compErr := s.run(comp, exchange.Copy(), false); compErr != nil {
			LogError("saga: compensation failed",
				"saga_id", state.SagaID,
				"compensation_index", completed.Index,
				"error", compErr,
				"original_error", cause,
			)
			failed = append([]CompletedStep{completed}, failed...)
		}
		state.CompletedSteps = append(state.CompletedSteps[:i:i], failed...)
		if persist && (i > 0 || len(failed) > 0) {
			s.saveState(ctx, state)
		}
	}
	if len(failed) > 0 {
		if !persist {
			s.saveState(ctx, state)
		}
		LogError("saga: compensations failed; state kept for recovery",
			"saga_id", state.SagaID, "pending_compensations", len(failed))
		return
	}
	state.Status = SagaCompensated
	if persist {
		s.removeState(ctx, state.SagaID)
	}
}

// checkpoint persists the state with a snapshot of the In message.
func (s *SagaProcessor) checkpoint(ctx context.Context, state *SagaState, exchange *Exchange) error {
	in := exchange.GetIn().DeepCopy()
	state.Message = SagaMessage{Body: in.Body, Headers: in.Headers}
	state.UpdatedAt = time.Now().UTC()
	return s.repository.Save(ctx, state.SagaID, *state)
}

func (s *SagaProcessor) saveState(ctx context.Context, state *SagaState) {
	state.UpdatedAt = time.Now().UTC()
	if err := s.repository.Save(ctx, state.SagaID, *state); err != nil {
		LogError("saga: failed to persist saga state", "saga_id", state.SagaID, "status", state.Status, "error", err)
	}
}

func (s *SagaProcessor) removeState(ctx context.Context, sagaID string) {
	if err := s.repository.Remove(ctx, sagaID); err != nil {
		LogError("saga: failed to remove finished saga state", "saga_id", sagaID, "error", err)
	}
}

// run executes p on exchange, recovering panics. With a timeout configured,
// p runs in its own goroutine on a deep copy whose Context carries the
// deadline, so a processor that ignores its context cannot block the saga;
// when p returns in time and apply is set, its result replaces the exchange
// content. On timeout the copy is abandoned: p may still be running.
func (s *SagaProcessor) run(p Processor, exchange *Exchange, apply bool) error {
	if s.timeout <= 0 {
		return ProcessSafely(p, exchange)
	}
	ctx, cancel := context.WithTimeout(exchangeContext(exchange), s.timeout)
	defer cancel()
	work := exchange.DeepCopy()
	work.Context = ctx
	done := make(chan error, 1)
	go func() { done <- ProcessSafely(p, work) }()

	select {
	case err := <-done:
		if apply {
			exchange.In, exchange.Out, exchange.Properties, exchange.Error = work.In, work.Out, work.Properties, work.Error
		}
		if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%w after %s: %w", ErrSagaStepTimeout, s.timeout, err)
		}
		return err
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%w after %s: %w", ErrSagaStepTimeout, s.timeout, ctx.Err())
		}
		return ctx.Err()
	}
}

// Recover compensates the incomplete sagas listed by the repository, last
// completed step first, regardless of SetRecoverIncomplete. Sagas currently
// running in this process are skipped.
//
// Compensations declared by URI are sent through camelCtx's producer cache
// with an exchange rebuilt from the last checkpoint's message snapshot and
// the CamelSagaId header. Compensations declared as Go functions cannot be
// rebuilt: they are reported as ErrSagaCompensationNotRecoverable (never
// skipped silently) and kept in the repository, with status
// SagaCompensating, for manual handling. Remaining URI compensations still
// run. The returned error joins every failure.
func (s *SagaProcessor) Recover(ctx context.Context, camelCtx *CamelContext) error {
	ids, err := s.repository.ListIncomplete(ctx)
	if err != nil {
		return fmt.Errorf("saga recovery: failed to list incomplete sagas: %w", err)
	}
	var errs []error
	for _, id := range ids {
		if !tryAcquireSagaID(id) {
			continue
		}
		err := s.recoverSaga(ctx, camelCtx, id)
		releaseSagaID(id)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *SagaProcessor) recoverSaga(ctx context.Context, camelCtx *CamelContext, sagaID string) error {
	state, err := s.repository.Load(ctx, sagaID)
	if errors.Is(err, ErrSagaNotFound) {
		return nil // finished since it was listed
	}
	if err != nil {
		return fmt.Errorf("saga %s: failed to load state: %w", sagaID, err)
	}
	if !state.Status.isIncomplete() {
		return nil
	}
	LogWarn("saga: recovering incomplete saga",
		"saga_id", sagaID, "status", state.Status, "pending_compensations", len(state.CompletedSteps))
	state.Status = SagaCompensating
	s.saveState(ctx, &state)

	var errs []error
	var failed []CompletedStep
	for i := len(state.CompletedSteps) - 1; i >= 0; i-- {
		completed := state.CompletedSteps[i]
		var stepErr error
		if completed.CompensationURI == "" {
			stepErr = fmt.Errorf("%w: saga %s step %d has an in-process (ActionFunc) compensation that cannot be rebuilt; compensate it manually, then remove the saga from the repository",
				ErrSagaCompensationNotRecoverable, sagaID, completed.Index)
			LogError("saga: compensation cannot be rebuilt after a restart; only compensations declared by endpoint URI are recoverable",
				"saga_id", sagaID, "step_index", completed.Index)
		} else {
			uri := completed.CompensationURI
			send := ProcessorFunc(func(ex *Exchange) error { return camelCtx.sendToURI(uri, ex) })
			if err := s.run(send, recoveryExchange(ctx, state), false); err != nil {
				stepErr = fmt.Errorf("saga %s: compensation %s of step %d failed: %w", sagaID, uri, completed.Index, err)
				LogError("saga: recovered compensation failed", "saga_id", sagaID, "uri", uri, "error", err)
			}
		}
		if stepErr != nil {
			errs = append(errs, stepErr)
			failed = append([]CompletedStep{completed}, failed...)
		}
		state.CompletedSteps = append(state.CompletedSteps[:i:i], failed...)
		s.saveState(ctx, &state)
	}

	if len(failed) == 0 {
		s.removeState(ctx, sagaID)
		LogInfo("saga: incomplete saga compensated", "saga_id", sagaID)
	}
	return errors.Join(errs...)
}

// recoverOnStart is called by CamelContext.Start once every route is
// started, so that compensation endpoints served by routes are reachable.
func (s *SagaProcessor) recoverOnStart(ctx context.Context, camelCtx *CamelContext) {
	if s.recoverIncomplete {
		if err := s.Recover(ctx, camelCtx); err != nil {
			LogError("saga: recovery of incomplete sagas finished with errors", "error", err)
		}
		return
	}
	ids, err := s.repository.ListIncomplete(ctx)
	if err != nil {
		LogWarn("saga: failed to list incomplete sagas", "error", err)
		return
	}
	for _, id := range ids {
		if sagaIDInFlight(id) {
			continue
		}
		LogWarn("saga: incomplete saga found in repository; its completed steps are NOT compensated because recovery is disabled. Enable it with Saga().RecoverIncomplete(true)",
			"saga_id", id)
	}
}

func recoveryExchange(ctx context.Context, state SagaState) *Exchange {
	ex := NewExchange(ctx)
	ex.GetIn().SetBody(state.Message.Body)
	ex.GetIn().SetHeaders(state.Message.Headers)
	ex.GetIn().SetHeader(CamelSagaId, state.SagaID)
	return ex
}

// completedStep describes a finished step for persistence: a compensation
// declared by URI is recorded by that URI (read at run time, so a
// Route.AdviceWith substitution is honoured), anything else as in-process.
func completedStep(index int, compensation Processor) CompletedStep {
	if lp, ok := compensation.(*lazyProducer); ok {
		lp.mu.Lock()
		uri := lp.uri
		lp.mu.Unlock()
		return CompletedStep{Index: index, CompensationURI: uri}
	}
	return CompletedStep{Index: index, InProcessCompensation: true}
}

func exchangeContext(exchange *Exchange) context.Context {
	if exchange.Context != nil {
		return exchange.Context
	}
	return context.Background()
}

// sagasInFlight holds the IDs of the sagas running in this process. It keeps
// two exchanges (or a nested saga) from sharing one persisted state, and
// keeps recovery away from sagas that are still running.
var sagasInFlight = struct {
	sync.Mutex
	ids map[string]struct{}
}{ids: make(map[string]struct{})}

func tryAcquireSagaID(id string) bool {
	sagasInFlight.Lock()
	defer sagasInFlight.Unlock()
	if _, busy := sagasInFlight.ids[id]; busy {
		return false
	}
	sagasInFlight.ids[id] = struct{}{}
	return true
}

func releaseSagaID(id string) {
	sagasInFlight.Lock()
	defer sagasInFlight.Unlock()
	delete(sagasInFlight.ids, id)
}

func sagaIDInFlight(id string) bool {
	sagasInFlight.Lock()
	defer sagasInFlight.Unlock()
	_, busy := sagasInFlight.ids[id]
	return busy
}

// acquireSagaID reserves the saga ID for exchange: the CamelSagaId header if
// set and not already running, otherwise a fresh UUID written to the header.
// When the header named a running saga (a nested saga), the header is
// restored on release so the enclosing saga keeps its ID.
func acquireSagaID(exchange *Exchange) (string, func()) {
	prev, hasPrev := exchange.GetIn().GetHeaderAsString(CamelSagaId)
	hasPrev = hasPrev && prev != ""
	if hasPrev && tryAcquireSagaID(prev) {
		return prev, func() { releaseSagaID(prev) }
	}
	id := generateUUID()
	for !tryAcquireSagaID(id) {
		id = generateUUID()
	}
	exchange.GetIn().SetHeader(CamelSagaId, id)
	return id, func() {
		releaseSagaID(id)
		if hasPrev {
			exchange.GetIn().SetHeader(CamelSagaId, prev)
		}
	}
}
