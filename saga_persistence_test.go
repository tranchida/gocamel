package gocamel

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// syncBuffer is a goroutine-safe log sink: background goroutines of other
// routes may log while a test reads the buffer.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func captureSagaLogs(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	original := GetLogger()
	SetLogger(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { SetLogger(original) })
	return buf
}

// recorder collects the order in which compensations ran, with the saga ID
// and the business header they received.
type recorder struct {
	mu      sync.Mutex
	calls   []string
	sagaIDs []string
	orders  []string
}

func (r *recorder) record(name string) func(*Exchange) error {
	return func(e *Exchange) error {
		id, _ := e.GetIn().GetHeaderAsString(CamelSagaId)
		order, _ := e.GetIn().GetHeaderAsString("OrderId")
		r.mu.Lock()
		defer r.mu.Unlock()
		r.calls = append(r.calls, name)
		r.sagaIDs = append(r.sagaIDs, id)
		r.orders = append(r.orders, order)
		return nil
	}
}

func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func startContext(t *testing.T, c *CamelContext) {
	t.Helper()
	require.NoError(t, c.Start())
	t.Cleanup(func() { _ = c.Stop() })
}

func sendTo(t *testing.T, c *CamelContext, uri string, ex *Exchange) error {
	t.Helper()
	producer, err := c.GetProducer(uri)
	require.NoError(t, err)
	return producer.Send(ex)
}

// addBookingRoutes registers the URI-based action and compensation routes
// used by the crash/recovery scenarios.
func addBookingRoutes(t *testing.T, c *CamelContext, rec *recorder) {
	t.Helper()
	for uri, fn := range map[string]func(*Exchange) error{
		"direct:book-hotel":    func(*Exchange) error { return nil },
		"direct:book-flight":   func(*Exchange) error { return nil },
		"direct:cancel-hotel":  rec.record("cancel-hotel"),
		"direct:cancel-flight": rec.record("cancel-flight"),
	} {
		_, err := c.CreateRouteBuilder().From(uri).ProcessFunc(fn).Build()
		require.NoError(t, err)
	}
}

// crashSagaAfterTwoSteps runs a saga whose third step terminates its goroutine
// with runtime.Goexit: no compensation and no cleanup of the persisted state
// run, which is what a process crash leaves behind. It returns the saga ID.
func crashSagaAfterTwoSteps(t *testing.T, repo SagaRepository, firstStepFunc bool) string {
	t.Helper()
	c1 := NewCamelContext()
	rec1 := &recorder{}
	addBookingRoutes(t, c1, rec1)
	saga := c1.CreateRouteBuilder().From("direct:saga").Saga().Repository(repo)
	if firstStepFunc {
		saga = saga.ActionFunc(func(*Exchange) error { return nil }, rec1.record("func-cancel-hotel"))
	} else {
		saga = saga.Action("direct:book-hotel", "direct:cancel-hotel")
	}
	_, err := saga.
		Action("direct:book-flight", "direct:cancel-flight").
		ActionFunc(func(*Exchange) error { runtime.Goexit(); return nil }, nil).
		End().
		Build()
	require.NoError(t, err)
	require.NoError(t, c1.Start())

	ex := NewExchange(context.Background())
	ex.GetIn().SetHeader("OrderId", "order-42")
	ex.GetIn().SetHeader(CamelSagaId, "saga-crash-1")
	done := make(chan struct{})
	go func() {
		defer close(done)
		producer, err := c1.GetProducer("direct:saga")
		if err != nil {
			return
		}
		_ = producer.Send(ex)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("crashed saga goroutine did not exit")
	}
	require.NoError(t, c1.Stop())
	assert.Empty(t, rec1.snapshot(), "no compensation may run in the crashed process")

	state, err := repo.Load(context.Background(), "saga-crash-1")
	require.NoError(t, err)
	require.Len(t, state.CompletedSteps, 2)
	assert.Equal(t, SagaRunning, state.Status)
	return "saga-crash-1"
}

func testSagaRecoveryAfterCrash(t *testing.T, repo SagaRepository) {
	sagaID := crashSagaAfterTwoSteps(t, repo, false)

	// "Restart": a fresh context with the same route definitions and repository.
	c2 := NewCamelContext()
	rec := &recorder{}
	addBookingRoutes(t, c2, rec)
	_, err := c2.CreateRouteBuilder().From("direct:saga").
		Saga().Repository(repo).RecoverIncomplete(true).
		Action("direct:book-hotel", "direct:cancel-hotel").
		Action("direct:book-flight", "direct:cancel-flight").
		End().
		Build()
	require.NoError(t, err)
	startContext(t, c2)

	// Recovery runs synchronously during Start: LIFO, flight then hotel.
	assert.Equal(t, []string{"cancel-flight", "cancel-hotel"}, rec.snapshot())
	assert.Equal(t, []string{sagaID, sagaID}, rec.sagaIDs)
	assert.Equal(t, []string{"order-42", "order-42"}, rec.orders, "compensations receive the last checkpoint's headers")

	_, err = repo.Load(context.Background(), sagaID)
	assert.ErrorIs(t, err, ErrSagaNotFound, "a fully compensated saga is removed")
}

func TestSagaRecovery_MemoryRepository(t *testing.T) {
	testSagaRecoveryAfterCrash(t, NewMemorySagaRepository())
}

func TestSagaRecovery_DisabledByDefaultLogsOnly(t *testing.T) {
	repo := NewMemorySagaRepository()
	sagaID := crashSagaAfterTwoSteps(t, repo, false)
	logs := captureSagaLogs(t)

	c2 := NewCamelContext()
	rec := &recorder{}
	addBookingRoutes(t, c2, rec)
	_, err := c2.CreateRouteBuilder().From("direct:saga").
		Saga().Repository(repo).
		Action("direct:book-hotel", "direct:cancel-hotel").
		Action("direct:book-flight", "direct:cancel-flight").
		End().
		Build()
	require.NoError(t, err)
	startContext(t, c2)

	assert.Empty(t, rec.snapshot(), "recovery is opt-in: nothing is compensated")
	out := logs.String()
	assert.Contains(t, out, "level=WARN")
	assert.Contains(t, out, sagaID)
	assert.Contains(t, out, "RecoverIncomplete(true)")

	state, err := repo.Load(context.Background(), sagaID)
	require.NoError(t, err)
	assert.Len(t, state.CompletedSteps, 2, "state is kept for a later recovery")
}

func TestSagaRecovery_FuncCompensationIsReportedNotSkipped(t *testing.T) {
	repo := NewMemorySagaRepository()
	sagaID := crashSagaAfterTwoSteps(t, repo, true)

	c2 := NewCamelContext()
	rec := &recorder{}
	addBookingRoutes(t, c2, rec)
	startContext(t, c2)

	saga := NewSagaProcessor()
	saga.SetRepository(repo)
	err := saga.Recover(context.Background(), c2)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSagaCompensationNotRecoverable)
	assert.Contains(t, err.Error(), sagaID)

	// The URI-based step is still compensated; the func-based one is kept.
	assert.Equal(t, []string{"cancel-flight"}, rec.snapshot())
	state, err := repo.Load(context.Background(), sagaID)
	require.NoError(t, err)
	assert.Equal(t, SagaCompensating, state.Status)
	require.Len(t, state.CompletedSteps, 1)
	assert.True(t, state.CompletedSteps[0].InProcessCompensation)
}

func TestSagaRecovery_FuncCompensationLoggedAtStart(t *testing.T) {
	repo := NewMemorySagaRepository()
	sagaID := crashSagaAfterTwoSteps(t, repo, true)
	logs := captureSagaLogs(t)

	c2 := NewCamelContext()
	addBookingRoutes(t, c2, &recorder{})
	_, err := c2.CreateRouteBuilder().From("direct:saga").
		Saga().Repository(repo).RecoverIncomplete(true).
		Action("direct:book-flight", "direct:cancel-flight").
		End().
		Build()
	require.NoError(t, err)
	startContext(t, c2)

	out := logs.String()
	assert.Contains(t, out, "level=ERROR")
	assert.Contains(t, out, sagaID)
	assert.Contains(t, out, "cannot be rebuilt")
}

func TestSagaRecovery_SkipsSagasInFlight(t *testing.T) {
	repo := NewMemorySagaRepository()
	c := NewCamelContext()
	rec := &recorder{}
	addBookingRoutes(t, c, rec)
	entered := make(chan struct{})
	release := make(chan struct{})
	_, err := c.CreateRouteBuilder().From("direct:saga").
		Saga().Repository(repo).
		Action("direct:book-hotel", "direct:cancel-hotel").
		ActionFunc(func(*Exchange) error {
			close(entered)
			<-release
			return nil
		}, nil).
		End().
		Build()
	require.NoError(t, err)
	startContext(t, c)

	done := make(chan error, 1)
	go func() {
		ex := NewExchange(context.Background())
		ex.GetIn().SetHeader(CamelSagaId, "live-saga")
		done <- sendTo(t, c, "direct:saga", ex)
	}()
	<-entered

	other := NewSagaProcessor()
	other.SetRepository(repo)
	assert.NoError(t, other.Recover(context.Background(), c))
	assert.Empty(t, rec.snapshot(), "a running saga of this process is not recovered")

	close(release)
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("saga did not complete")
	}
}

func TestSaga_CheckpointsAfterEachStepAndRemovesOnSuccess(t *testing.T) {
	repo := NewMemorySagaRepository()
	c := NewCamelContext()
	var seen SagaState
	var seenErr error
	_, err := c.CreateRouteBuilder().From("direct:saga").
		Saga().Repository(repo).
		ActionFunc(func(*Exchange) error { return nil }, func(*Exchange) error { return nil }).
		ActionFunc(func(*Exchange) error { return nil }, nil). // no compensation: not recorded
		ActionFunc(func(e *Exchange) error {
			id, _ := e.GetIn().GetHeaderAsString(CamelSagaId)
			seen, seenErr = repo.Load(context.Background(), id)
			return nil
		}, nil).
		End().
		Build()
	require.NoError(t, err)
	startContext(t, c)

	ex := NewExchange(context.Background())
	require.NoError(t, sendTo(t, c, "direct:saga", ex))

	require.NoError(t, seenErr)
	assert.Equal(t, SagaRunning, seen.Status)
	assert.Equal(t, []CompletedStep{{Index: 0, InProcessCompensation: true}}, seen.CompletedSteps)

	id, ok := ex.GetIn().GetHeaderAsString(CamelSagaId)
	require.True(t, ok)
	assert.Len(t, id, 36, "a UUID is generated when no CamelSagaId header is present")
	_, err = repo.Load(context.Background(), id)
	assert.ErrorIs(t, err, ErrSagaNotFound)
}

func TestSaga_FailedCompensationIsKeptForRecovery(t *testing.T) {
	repo := NewMemorySagaRepository()
	c := NewCamelContext()
	_, err := c.CreateRouteBuilder().From("direct:saga").
		Saga().Repository(repo).
		ActionFunc(func(*Exchange) error { return nil }, func(*Exchange) error { return nil }).
		ActionFunc(func(*Exchange) error { return nil }, func(*Exchange) error { return errors.New("cancel failed") }).
		ActionFunc(func(*Exchange) error { return errors.New("step 3 failed") }, nil).
		End().
		Build()
	require.NoError(t, err)
	startContext(t, c)

	ex := NewExchange(context.Background())
	ex.GetIn().SetHeader(CamelSagaId, "saga-fail")
	require.Error(t, sendTo(t, c, "direct:saga", ex))

	state, err := repo.Load(context.Background(), "saga-fail")
	require.NoError(t, err)
	assert.Equal(t, SagaCompensating, state.Status)
	assert.Equal(t, []CompletedStep{{Index: 1, InProcessCompensation: true}}, state.CompletedSteps)
}

func TestSaga_FullyCompensatedSagaIsRemoved(t *testing.T) {
	repo := NewMemorySagaRepository()
	c := NewCamelContext()
	_, err := c.CreateRouteBuilder().From("direct:saga").
		Saga().Repository(repo).
		ActionFunc(func(*Exchange) error { return nil }, func(*Exchange) error { return nil }).
		ActionFunc(func(*Exchange) error { return errors.New("boom") }, nil).
		End().
		Build()
	require.NoError(t, err)
	startContext(t, c)

	ex := NewExchange(context.Background())
	ex.GetIn().SetHeader(CamelSagaId, "saga-comp")
	require.Error(t, sendTo(t, c, "direct:saga", ex))
	_, err = repo.Load(context.Background(), "saga-comp")
	assert.ErrorIs(t, err, ErrSagaNotFound)
}

type failingSagaRepository struct{ MemorySagaRepository }

func (r *failingSagaRepository) Save(context.Context, string, SagaState) error {
	return errors.New("database unavailable")
}

func TestSaga_CheckpointFailureCompensatesCompletedSteps(t *testing.T) {
	repo := &failingSagaRepository{MemorySagaRepository: *NewMemorySagaRepository()}
	c := NewCamelContext()
	var compensated, secondRan bool
	_, err := c.CreateRouteBuilder().From("direct:saga").
		Saga().Repository(repo).
		ActionFunc(func(*Exchange) error { return nil }, func(*Exchange) error { compensated = true; return nil }).
		ActionFunc(func(*Exchange) error { secondRan = true; return nil }, nil).
		End().
		Build()
	require.NoError(t, err)
	startContext(t, c)

	err = sendTo(t, c, "direct:saga", NewExchange(context.Background()))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checkpoint")
	assert.True(t, compensated, "a step whose completion cannot be persisted is compensated")
	assert.False(t, secondRan)
}

func TestSaga_StepTimeoutCompensatesEarlierSteps(t *testing.T) {
	for name, blocking := range map[string]func(e *Exchange, release <-chan struct{}) error{
		"cooperative": func(e *Exchange, _ <-chan struct{}) error {
			<-e.Context.Done()
			return e.Context.Err()
		},
		"ignoresContext": func(_ *Exchange, release <-chan struct{}) error {
			<-release
			return nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := NewCamelContext()
			release := make(chan struct{})
			t.Cleanup(func() { close(release) })
			var mu sync.Mutex
			var compensations []string
			comp := func(name string) func(*Exchange) error {
				return func(*Exchange) error {
					mu.Lock()
					defer mu.Unlock()
					compensations = append(compensations, name)
					return nil
				}
			}
			_, err := c.CreateRouteBuilder().From("direct:saga").
				Saga().Timeout(50*time.Millisecond).
				ActionFunc(func(e *Exchange) error { e.GetIn().SetHeader("Step1", "done"); return nil }, comp("first")).
				ActionFunc(func(e *Exchange) error { return blocking(e, release) }, comp("second")).
				End().
				Build()
			require.NoError(t, err)
			startContext(t, c)

			ex := NewExchange(context.Background())
			startedAt := time.Now()
			err = sendTo(t, c, "direct:saga", ex)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrSagaStepTimeout)
			assert.ErrorIs(t, err, context.DeadlineExceeded)
			assert.Less(t, time.Since(startedAt), 2*time.Second)

			mu.Lock()
			defer mu.Unlock()
			assert.Equal(t, []string{"first"}, compensations)
			v, _ := ex.GetIn().GetHeaderAsString("Step1")
			assert.Equal(t, "done", v, "results of a step that finished in time are kept")
		})
	}
}

func TestSaga_CompensationTimeoutDoesNotBlock(t *testing.T) {
	repo := NewMemorySagaRepository()
	c := NewCamelContext()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	_, err := c.CreateRouteBuilder().From("direct:saga").
		Saga().Repository(repo).Timeout(50*time.Millisecond).
		ActionFunc(func(*Exchange) error { return nil }, func(*Exchange) error { <-release; return nil }).
		ActionFunc(func(*Exchange) error { return errors.New("boom") }, nil).
		End().
		Build()
	require.NoError(t, err)
	startContext(t, c)

	ex := NewExchange(context.Background())
	ex.GetIn().SetHeader(CamelSagaId, "saga-slow-comp")
	require.Error(t, sendTo(t, c, "direct:saga", ex))
	state, err := repo.Load(context.Background(), "saga-slow-comp")
	require.NoError(t, err)
	assert.Len(t, state.CompletedSteps, 1, "a timed-out compensation is kept for recovery")
}

func TestSaga_InvalidConfigurationIsABuildError(t *testing.T) {
	c := NewCamelContext()
	_, err := c.CreateRouteBuilder().From("direct:a").Saga().Timeout(-time.Second).End().Build()
	assert.Error(t, err)

	_, err = c.CreateRouteBuilder().From("direct:b").Saga().Repository(nil).End().Build()
	assert.Error(t, err)

	var nilSQLRepo *SQLSagaRepository
	_, err = c.CreateRouteBuilder().From("direct:c").Saga().Repository(nilSQLRepo).End().Build()
	assert.Error(t, err, "typed-nil SagaRepository must be rejected at Build time")

	var nilMemRepo *MemorySagaRepository
	_, err = c.CreateRouteBuilder().From("direct:d").Saga().Repository(nilMemRepo).End().Build()
	assert.Error(t, err, "typed-nil MemorySagaRepository must be rejected at Build time")

	var nilDef *SagaDefinition
	assert.NotPanics(t, func() {
		assert.Nil(t, nilDef.Repository(nil))
	})
}

func TestSaga_NestedSagaGetsItsOwnID(t *testing.T) {
	repo := NewMemorySagaRepository()
	c := NewCamelContext()
	var innerID string
	_, err := c.CreateRouteBuilder().From("direct:inner").
		Saga().Repository(repo).
		ActionFunc(func(e *Exchange) error {
			innerID, _ = e.GetIn().GetHeaderAsString(CamelSagaId)
			return nil
		}, func(*Exchange) error { return nil }).
		End().
		Build()
	require.NoError(t, err)
	_, err = c.CreateRouteBuilder().From("direct:outer").
		Saga().Repository(repo).
		Action("direct:inner", "direct:inner").
		End().
		Build()
	require.NoError(t, err)
	startContext(t, c)

	ex := NewExchange(context.Background())
	ex.GetIn().SetHeader(CamelSagaId, "outer-saga")
	require.NoError(t, sendTo(t, c, "direct:outer", ex))

	assert.NotEmpty(t, innerID)
	assert.NotEqual(t, "outer-saga", innerID, "a nested saga must not share the running saga's state")
	outerID, _ := ex.GetIn().GetHeaderAsString(CamelSagaId)
	assert.Equal(t, "outer-saga", outerID)
}

func TestSaga_ConcurrentSagasAreIsolated(t *testing.T) {
	repo := NewMemorySagaRepository()
	c := NewCamelContext()
	var mu sync.Mutex
	compensated := map[string]int{}
	var barrier sync.WaitGroup
	barrier.Add(2)
	_, err := c.CreateRouteBuilder().From("direct:saga").
		Saga().Repository(repo).Timeout(5*time.Second).
		ActionFunc(func(e *Exchange) error {
			barrier.Done()
			barrier.Wait() // both sagas are in flight together
			return nil
		}, func(e *Exchange) error {
			body, _ := e.GetIn().GetBody().(string)
			mu.Lock()
			defer mu.Unlock()
			compensated[body]++
			return nil
		}).
		ActionFunc(func(e *Exchange) error {
			if e.GetIn().GetBody() == "fail" {
				return errors.New("rejected")
			}
			return nil
		}, nil).
		End().
		Build()
	require.NoError(t, err)
	startContext(t, c)

	errs := make(map[string]error)
	var wg sync.WaitGroup
	var errMu sync.Mutex
	for _, body := range []string{"ok", "fail"} {
		wg.Add(1)
		go func(body string) {
			defer wg.Done()
			ex := NewExchange(context.Background())
			ex.GetIn().SetBody(body)
			producer, err := c.GetProducer("direct:saga")
			if err == nil {
				err = producer.Send(ex)
			}
			errMu.Lock()
			errs[body] = err
			errMu.Unlock()
		}(body)
	}
	wg.Wait()

	assert.NoError(t, errs["ok"])
	assert.Error(t, errs["fail"])
	mu.Lock()
	assert.Equal(t, map[string]int{"fail": 1}, compensated)
	mu.Unlock()
	ids, err := repo.ListIncomplete(context.Background())
	require.NoError(t, err)
	assert.Empty(t, ids)
}

type failOnSaveAttemptRepo struct {
	*MemorySagaRepository
	mu        sync.Mutex
	saveCalls int
	failOn    int
}

func (r *failOnSaveAttemptRepo) Save(ctx context.Context, sagaID string, state SagaState) error {
	r.mu.Lock()
	r.saveCalls++
	n := r.saveCalls
	r.mu.Unlock()
	if n == r.failOn {
		return errors.New("checkpoint save failed")
	}
	return r.MemorySagaRepository.Save(ctx, sagaID, state)
}

type sagaTestSync struct {
	completed atomic.Bool
	failed    atomic.Bool
}

func (s *sagaTestSync) OnComplete(*Exchange) { s.completed.Store(true) }
func (s *sagaTestSync) OnFailure(*Exchange)  { s.failed.Store(true) }

func TestSaga_CompensatesWithDetachedContextAndCheckpointPersistAndOutPropagation(t *testing.T) {
	t.Run("cancelled exchange context still runs context-aware compensation", func(t *testing.T) {
		for _, timeout := range []time.Duration{0, 250 * time.Millisecond} {
			name := "no-timeout"
			if timeout > 0 {
				name = "with-timeout"
			}
			t.Run(name, func(t *testing.T) {
				c := NewCamelContext()
				var compCtxErr error
				var compRan bool
				exCtx, cancel := context.WithCancel(context.Background())

				b := c.CreateRouteBuilder().From("direct:saga").Saga()
				if timeout > 0 {
					b = b.Timeout(timeout)
				}
				_, err := b.
					ActionFunc(
						func(*Exchange) error { return nil },
						func(e *Exchange) error {
							compRan = true
							compCtxErr = e.Context.Err()
							return compCtxErr
						},
					).
					ActionFunc(
						func(e *Exchange) error {
							cancel()
							return e.Context.Err()
						},
						nil,
					).
					End().
					Build()
				require.NoError(t, err)
				startContext(t, c)

				ex := NewExchange(exCtx)
				err = sendTo(t, c, "direct:saga", ex)
				require.Error(t, err)
				assert.NotErrorIs(t, err, ErrSagaStepTimeout)
				assert.True(t, compRan, "step 1 compensation must execute even after exchange context cancellation")
				assert.NoError(t, compCtxErr, "compensation must receive a detached non-cancelled context")
			})
		}
	})

	t.Run("checkpoint failure after prior saved step removes saga row on successful compensation", func(t *testing.T) {
		repo := &failOnSaveAttemptRepo{
			MemorySagaRepository: NewMemorySagaRepository(),
			failOn:               2, // Step 0 checkpoint succeeds (saved == true), Step 1 checkpoint fails.
		}
		c := NewCamelContext()
		var comp1, comp0 bool
		_, err := c.CreateRouteBuilder().From("direct:saga").
			Saga().Repository(repo).
			ActionFunc(func(*Exchange) error { return nil }, func(*Exchange) error { comp0 = true; return nil }).
			ActionFunc(func(*Exchange) error { return nil }, func(*Exchange) error { comp1 = true; return nil }).
			End().
			Build()
		require.NoError(t, err)
		startContext(t, c)

		ex := NewExchange(context.Background())
		ex.GetIn().SetHeader(CamelSagaId, "saga-checkpoint-2")
		err = sendTo(t, c, "direct:saga", ex)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "checkpoint")
		assert.True(t, comp1)
		assert.True(t, comp0)

		_, loadErr := repo.Load(context.Background(), "saga-checkpoint-2")
		assert.ErrorIs(t, loadErr, ErrSagaNotFound, "compensated saga must be removed so recovery does not replay it")
		ids, listErr := repo.ListIncomplete(context.Background())
		require.NoError(t, listErr)
		assert.Empty(t, ids)
	})

	t.Run("Out propagates to step 2 and checkpoint and failed step 2 Out does not overwrite URI compensation In", func(t *testing.T) {
		repo := NewMemorySagaRepository()
		c := NewCamelContext()

		var compBody any
		var compHeader string
		var compHasHeader bool
		_, err := c.CreateRouteBuilder().From("direct:comp-uri").ProcessFunc(func(e *Exchange) error {
			compBody = e.GetIn().GetBody()
			compHeader, compHasHeader = e.GetIn().GetHeaderAsString("X-Step1")
			return nil
		}).Build()
		require.NoError(t, err)

		_, err = c.CreateRouteBuilder().From("direct:step1").ProcessFunc(func(e *Exchange) error {
			e.GetOut().SetBody("step1-out")
			e.GetOut().SetHeader("X-Step1", "from-step1")
			return nil
		}).Build()
		require.NoError(t, err)

		var step2InBody any
		var step2Header string
		var savedState SagaState
		var loadErr error
		_, err = c.CreateRouteBuilder().From("direct:saga").
			Saga().Repository(repo).
			Action("direct:step1", "direct:comp-uri").
			ActionFunc(func(e *Exchange) error {
				step2InBody = e.GetIn().GetBody()
				step2Header, _ = e.GetIn().GetHeaderAsString("X-Step1")
				id, _ := e.GetIn().GetHeaderAsString(CamelSagaId)
				savedState, loadErr = repo.Load(context.Background(), id)
				// Write partial error output before failing.
				e.GetOut().SetBody("step2-error-out")
				e.GetOut().SetHeader("X-Step1", "corrupted")
				return errors.New("step 2 failed")
			}, nil).
			End().
			Build()
		require.NoError(t, err)
		startContext(t, c)

		ex := NewExchange(context.Background())
		ex.GetIn().SetBody("initial")
		require.Error(t, sendTo(t, c, "direct:saga", ex))

		assert.Equal(t, "step1-out", step2InBody)
		assert.Equal(t, "from-step1", step2Header)
		require.NoError(t, loadErr)
		assert.Equal(t, "step1-out", savedState.Message.Body)
		assert.Equal(t, "from-step1", savedState.Message.Headers["X-Step1"])

		assert.Equal(t, "step1-out", compBody, "URI compensation must see step 1 In, not failed step 2 Out")
		assert.True(t, compHasHeader)
		assert.Equal(t, "from-step1", compHeader)
	})

	t.Run("Synchronization callbacks run on timed steps, compensations, and recovery", func(t *testing.T) {
		c := NewCamelContext()
		stepSyncFail := &sagaTestSync{}
		compSync := &sagaTestSync{}
		stepSyncOK := &sagaTestSync{}

		_, err := c.CreateRouteBuilder().From("direct:saga-fail").
			Saga().Timeout(500*time.Millisecond).
			ActionFunc(
				func(e *Exchange) error { e.AddSynchronization(stepSyncFail); return nil },
				func(e *Exchange) error { e.AddSynchronization(compSync); return nil },
			).
			ActionFunc(func(*Exchange) error { return errors.New("boom") }, nil).
			End().
			Build()
		require.NoError(t, err)

		_, err = c.CreateRouteBuilder().From("direct:saga-ok").
			Saga().Timeout(500*time.Millisecond).
			ActionFunc(
				func(e *Exchange) error { e.AddSynchronization(stepSyncOK); return nil },
				nil,
			).
			End().
			Build()
		require.NoError(t, err)
		startContext(t, c)

		exFail := NewExchange(context.Background())
		err = CompleteExchange(exFail, sendTo(t, c, "direct:saga-fail", exFail))
		require.Error(t, err)
		assert.True(t, stepSyncFail.failed.Load(), "timed step synchronization must run OnFailure")
		assert.True(t, compSync.completed.Load(), "compensation synchronization must run OnComplete")

		exOK := NewExchange(context.Background())
		err = CompleteExchange(exOK, sendTo(t, c, "direct:saga-ok", exOK))
		require.NoError(t, err)
		assert.True(t, stepSyncOK.completed.Load(), "timed step synchronization must run OnComplete")

		// Recovery exchange also runs Synchronization callbacks.
		repo := NewMemorySagaRepository()
		require.NoError(t, repo.Save(context.Background(), "rec-sync", SagaState{
			SagaID:         "rec-sync",
			Status:         SagaRunning,
			CompletedSteps: []CompletedStep{{Index: 0, CompensationURI: "direct:rec-comp"}},
			UpdatedAt:      time.Now().UTC(),
		}))
		recSync := &sagaTestSync{}
		c2 := NewCamelContext()
		_, err = c2.CreateRouteBuilder().From("direct:rec-comp").ProcessFunc(func(e *Exchange) error {
			e.AddSynchronization(recSync)
			return nil
		}).Build()
		require.NoError(t, err)
		startContext(t, c2)

		recSaga := NewSagaProcessor()
		recSaga.SetRepository(repo)
		require.NoError(t, recSaga.Recover(context.Background(), c2))
		assert.True(t, recSync.completed.Load(), "recovery compensation synchronization must run")
	})

	t.Run("parent context deadline is distinguished from saga step timeout", func(t *testing.T) {
		c := NewCamelContext()
		_, err := c.CreateRouteBuilder().From("direct:saga-parent-deadline").
			Saga().Timeout(5*time.Second).
			ActionFunc(func(e *Exchange) error {
				<-e.Context.Done()
				return e.Context.Err()
			}, nil).
			End().
			Build()
		require.NoError(t, err)
		startContext(t, c)

		parentCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		err = sendTo(t, c, "direct:saga-parent-deadline", NewExchange(parentCtx))
		require.Error(t, err)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
		assert.NotErrorIs(t, err, ErrSagaStepTimeout)
	})
}

func TestSagaRecovery_RedactsCompensationURI(t *testing.T) {
	repo := NewMemorySagaRepository()
	secretURI := "missingcomp://user:s3cr3tpass@127.0.0.1:1/cancel?password=topsecret&token=tok123"
	require.NoError(t, repo.Save(context.Background(), "saga-secret", SagaState{
		SagaID:         "saga-secret",
		Status:         SagaRunning,
		CompletedSteps: []CompletedStep{{Index: 0, CompensationURI: secretURI}},
		UpdatedAt:      time.Now().UTC(),
	}))

	logs := captureSagaLogs(t)
	c := NewCamelContext()
	startContext(t, c)

	saga := NewSagaProcessor()
	saga.SetRepository(repo)
	err := saga.Recover(context.Background(), c)
	require.Error(t, err)

	assert.NotContains(t, err.Error(), "s3cr3tpass")
	assert.NotContains(t, err.Error(), "topsecret")
	assert.NotContains(t, err.Error(), "tok123")
	assert.Contains(t, err.Error(), "xxxxx")

	logOut := logs.String()
	assert.NotContains(t, logOut, "s3cr3tpass")
	assert.NotContains(t, logOut, "topsecret")
	assert.NotContains(t, logOut, "tok123")
	assert.Contains(t, logOut, "xxxxx")
}
