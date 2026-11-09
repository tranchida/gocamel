package gocamel

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type countingSync struct {
	completed atomic.Int32
	failed    atomic.Int32
}

func (s *countingSync) OnComplete(*Exchange) { s.completed.Add(1) }
func (s *countingSync) OnFailure(*Exchange)  { s.failed.Add(1) }

func TestDirectConsumer_StopWaitsForInFlightSendAndBlocksLazyProducerRecreation(t *testing.T) {
	ctx := NewCamelContext()
	counting := &lifecycleCountingComponent{}
	ctx.AddComponent("counting", counting)

	inProcessor := make(chan struct{})
	release := make(chan struct{})

	route, err := ctx.CreateRouteBuilder().
		From("direct:inflight").
		ProcessFunc(func(*Exchange) error {
			close(inProcessor)
			<-release
			return nil
		}).
		To("counting:sink").
		Build()
	require.NoError(t, err)
	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })

	prod, err := ctx.GetProducer("direct:inflight")
	require.NoError(t, err)

	sendDone := make(chan error, 1)
	go func() {
		sendDone <- prod.Send(NewExchange(context.Background()))
	}()
	<-inProcessor

	stopDone := make(chan error, 1)
	go func() {
		stopDone <- route.Stop()
	}()
	assert.Never(t, func() bool { return len(stopDone) > 0 }, 50*time.Millisecond, 5*time.Millisecond)

	close(release)
	require.NoError(t, <-sendDone)
	require.NoError(t, <-stopDone)

	assert.Equal(t, int32(1), counting.created.Load())
	assert.Equal(t, int32(1), counting.stopped.Load())
}

func TestLazyProducer_RejectsSendAfterStopAndResetsOnRestart(t *testing.T) {
	ctx := NewCamelContext()
	counting := &lifecycleCountingComponent{}
	ctx.AddComponent("counting", counting)

	lp := newLazyProducer(ctx, "counting:target")
	require.NoError(t, lp.Process(NewExchange(context.Background())))
	assert.Equal(t, int32(1), counting.created.Load())

	require.NoError(t, lp.Stop())
	assert.Equal(t, int32(1), counting.stopped.Load())

	err := lp.Process(NewExchange(context.Background()))
	require.Error(t, err)
	assert.Equal(t, int32(1), counting.created.Load(), "must not recreate producer after Stop")

	lp.reset()
	require.NoError(t, lp.Process(NewExchange(context.Background())))
	assert.Equal(t, int32(2), counting.created.Load())
	require.NoError(t, lp.Stop())
}

func TestWireTap_StopRejectsNewTapsAndCompletesExchange(t *testing.T) {
	ctx := NewCamelContext()
	var syncs countingSync

	_, err := ctx.CreateRouteBuilder().
		From("direct:tap-uow").
		ProcessFunc(func(e *Exchange) error {
			e.AddSynchronization(&syncs)
			return nil
		}).
		Build()
	require.NoError(t, err)
	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })

	wt := NewWireTap(ctx, "direct:tap-uow")
	require.NoError(t, wt.Process(NewExchange(context.Background())))
	require.NoError(t, wt.Stop())
	assert.Equal(t, int32(1), syncs.completed.Load())

	// While stopped, Process is a no-op and does not spawn a tap.
	require.NoError(t, wt.Process(NewExchange(context.Background())))
	assert.Equal(t, int32(1), syncs.completed.Load())

	wt.Start()
	require.NoError(t, wt.Process(NewExchange(context.Background())))
	require.NoError(t, wt.Stop())
	assert.Equal(t, int32(2), syncs.completed.Load())
}

func TestPollingEndpoint_ConcurrentPollsAllowed(t *testing.T) {
	for _, uri := range []string{"seda:concurrent-poll", "chan:concurrent-poll"} {
		t.Run(uri, func(t *testing.T) {
			ctx := NewCamelContext()
			ep, err := ctx.CreateEndpoint(uri)
			require.NoError(t, err)
			pe := ep.(PollingEndpoint)
			prod, err := ep.CreateProducer()
			require.NoError(t, err)

			const workers = 4
			var wg sync.WaitGroup
			errs := make(chan error, workers)
			for i := 0; i < workers; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					ex, err := pe.Poll(context.Background(), 200*time.Millisecond)
					if err != nil {
						errs <- err
						return
					}
					if ex == nil {
						errs <- errors.New("expected non-nil exchange")
					}
				}()
			}

			for i := 0; i < workers; i++ {
				ex := NewExchange(context.Background())
				ex.SetBody(i)
				require.NoError(t, prod.Send(ex))
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				require.NoError(t, err)
			}
		})
	}
}

func TestSedaAndChan_SendVsStopDoesNotStrandExchanges(t *testing.T) {
	for _, scheme := range []string{"seda", "chan"} {
		t.Run(scheme, func(t *testing.T) {
			ctx := NewCamelContext()
			uri := scheme + ":race-queue"
			ep, err := ctx.CreateEndpoint(uri)
			require.NoError(t, err)
			consumer, err := ep.CreateConsumer(ProcessorFunc(func(*Exchange) error { return nil }))
			require.NoError(t, err)
			prod, err := ep.CreateProducer()
			require.NoError(t, err)

			for iter := 0; iter < 20; iter++ {
				require.NoError(t, consumer.Start(context.Background()))
				var wg sync.WaitGroup
				wg.Add(2)
				go func() {
					defer wg.Done()
					for i := 0; i < 20; i++ {
						_ = prod.Send(NewExchange(context.Background()))
					}
				}()
				go func() {
					defer wg.Done()
					_ = consumer.Stop()
				}()
				wg.Wait()

				if sedaEp, ok := ep.(*SedaEndpoint); ok {
					assert.Equal(t, 0, len(sedaEp.queue))
				}
				if chanEp, ok := ep.(*ChanEndpoint); ok {
					assert.Equal(t, 0, len(chanEp.queue))
				}
			}
		})
	}
}

func TestDeepCopyValue_ExtendedTypesAndRepositories(t *testing.T) {
	ex := NewExchange(context.Background())
	rows := []map[string]any{{"a": []byte("x")}}
	strMap := map[string]string{"k": "v"}
	strMapSlice := []map[string]string{{"k": "v"}}
	csvRecords := [][]string{{"r1c1", "r1c2"}}
	byteMap := map[string][]byte{"f": []byte("data")}

	ex.SetBody(rows)
	ex.SetHeader("strMap", strMap)
	ex.SetHeader("strMapSlice", strMapSlice)
	ex.SetHeader("csv", csvRecords)
	ex.SetProperty("byteMap", byteMap)

	cp := ex.DeepCopy()
	rows[0]["a"].([]byte)[0] = 'Z'
	strMap["k"] = "mutated"
	strMapSlice[0]["k"] = "mutated"
	csvRecords[0][0] = "mutated"
	byteMap["f"][0] = 'Z'

	assert.Equal(t, []byte("x"), cp.GetBody().([]map[string]any)[0]["a"])
	hdrStrMap, _ := cp.GetHeader("strMap")
	assert.Equal(t, "v", hdrStrMap.(map[string]string)["k"])
	hdrStrMapSlice, _ := cp.GetHeader("strMapSlice")
	assert.Equal(t, "v", hdrStrMapSlice.([]map[string]string)[0]["k"])
	hdrCSV, _ := cp.GetHeader("csv")
	assert.Equal(t, "r1c1", hdrCSV.([][]string)[0][0])
	propByteMap, _ := cp.GetProperty("byteMap")
	assert.Equal(t, []byte("data"), propByteMap.(map[string][]byte)["f"])

	// MemoryClaimCheckRepository isolation
	ccRepo := NewMemoryClaimCheckRepository()
	buf := []byte("original")
	require.NoError(t, ccRepo.Set(context.Background(), "k1", buf))
	buf[0] = 'X'
	got, err := ccRepo.Get(context.Background(), "k1")
	require.NoError(t, err)
	assert.Equal(t, []byte("original"), got)
	got.([]byte)[0] = 'Y'
	got2, err := ccRepo.Get(context.Background(), "k1")
	require.NoError(t, err)
	assert.Equal(t, []byte("original"), got2)

	// MemorySagaRepository isolation
	sagaRepo := NewMemorySagaRepository()
	bodyBytes := []byte("saga-body")
	nestedHdr := map[string]any{"inner": []byte("hdr")}
	require.NoError(t, sagaRepo.Save(context.Background(), "s1", SagaState{
		SagaID:  "s1",
		Status:  SagaRunning,
		Message: SagaMessage{Body: bodyBytes, Headers: nestedHdr},
	}))
	bodyBytes[0] = 'X'
	nestedHdr["inner"].([]byte)[0] = 'X'
	loaded, err := sagaRepo.Load(context.Background(), "s1")
	require.NoError(t, err)
	assert.Equal(t, []byte("saga-body"), loaded.Message.Body)
	assert.Equal(t, []byte("hdr"), loaded.Message.Headers["inner"])
}

func TestTimerConsumer_ConcurrentStartStopLifecycle(t *testing.T) {
	ep, err := NewTimerComponent().CreateEndpoint("timer:race?delay=1&period=2")
	require.NoError(t, err)
	consumer, err := ep.CreateConsumer(ProcessorFunc(func(*Exchange) error { return nil }))
	require.NoError(t, err)

	for i := 0; i < 10; i++ {
		require.NoError(t, consumer.Start(context.Background()))
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = consumer.Stop()
		}()
		go func() {
			defer wg.Done()
			_ = consumer.Start(context.Background())
		}()
		wg.Wait()
		require.NoError(t, consumer.Stop())
	}
}

func TestNetConsumer_ContextCancelUnblocksAndRestartWorks(t *testing.T) {
	for _, proto := range []string{"tcp", "udp"} {
		t.Run(proto, func(t *testing.T) {
			ep, err := NewNetComponent().CreateEndpoint("net:" + proto + "://127.0.0.1:0?sync=false")
			require.NoError(t, err)
			consumer, err := ep.CreateConsumer(ProcessorFunc(func(*Exchange) error { return nil }))
			require.NoError(t, err)

			ctx, cancel := context.WithCancel(context.Background())
			require.NoError(t, consumer.Start(ctx))
			cancel()
			require.NoError(t, consumer.Stop())

			// Restart on the same consumer instance
			require.NoError(t, consumer.Start(context.Background()))
			require.NoError(t, consumer.Stop())
		})
	}
}

func TestHTTPConsumer_RestartOnSameConsumerInstance(t *testing.T) {
	ep, err := NewHTTPComponent().CreateEndpoint("http://127.0.0.1:0/restart")
	require.NoError(t, err)
	consumer, err := ep.CreateConsumer(ProcessorFunc(func(e *Exchange) error {
		e.GetOut().SetBody("ok")
		return nil
	}))
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		require.NoError(t, consumer.Start(context.Background()))
		require.NoError(t, consumer.Stop())
	}
}

func TestManagementServer_RestartAndContextStop(t *testing.T) {
	ctx := NewCamelContext()
	_, err := ctx.CreateRouteBuilder().From("direct:m").SetID("m").Build()
	require.NoError(t, err)
	require.NoError(t, ctx.Start())

	mgmt := NewManagementServer(ctx)
	require.NoError(t, mgmt.Start("127.0.0.1:0"))
	require.NotNil(t, mgmt.Addr())
	require.NoError(t, mgmt.Stop())
	assert.Nil(t, mgmt.Addr())

	// Restart works on the same ManagementServer instance
	require.NoError(t, mgmt.Start("127.0.0.1:0"))
	require.NotNil(t, mgmt.Addr())

	// CamelContext.Stop stops the ManagementServer too
	require.NoError(t, ctx.Stop())
	assert.Nil(t, mgmt.Addr())
}

func TestAggregator_TimeoutCompletesExchangeAndStopWithoutConfigureIsSafe(t *testing.T) {
	var syncs countingSync
	agg := NewAggregator(func(*Exchange) string { return "k" }, latestStrategy{}, NewMemoryAggregationRepository()).
		SetCompletionSize(5).
		SetCompletionTimeout(20 * time.Millisecond)

	ctx := NewCamelContext()
	_, err := ctx.CreateRouteBuilder().
		From("direct:agg-uow").
		Aggregate(agg).
		ProcessFunc(func(e *Exchange) error {
			e.AddSynchronization(&syncs)
			return nil
		}).
		Build()
	require.NoError(t, err)
	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })

	prod, err := ctx.GetProducer("direct:agg-uow")
	require.NoError(t, err)
	require.NoError(t, prod.Send(NewExchange(context.Background())))

	require.Eventually(t, func() bool {
		return syncs.completed.Load() == 1
	}, time.Second, 5*time.Millisecond)

	// Unconfigured aggregator Stop() prevents subsequent startTimeout from racing WaitGroup
	unconfigured := NewAggregator(func(*Exchange) string { return "k" }, latestStrategy{}, NewMemoryAggregationRepository()).
		SetCompletionSize(5).
		SetCompletionTimeout(50 * time.Millisecond)
	require.NoError(t, unconfigured.Stop())
	assert.ErrorIs(t, unconfigured.Process(NewExchange(context.Background())), ErrStopRouting)
}

func TestResequencer_StopWaitsForSynchronousDeliveryAndCompletesExchange(t *testing.T) {
	var syncs countingSync
	inDelivery := make(chan struct{})
	release := make(chan struct{})

	r := NewResequencer(func(e *Exchange) (int64, error) {
		v, _ := e.GetHeaderAsInt("seq")
		return int64(v), nil
	}, 1, time.Second)
	r.SetProcessor(ProcessorFunc(func(e *Exchange) error {
		e.AddSynchronization(&syncs)
		close(inDelivery)
		<-release
		return nil
	}))

	processDone := make(chan error, 1)
	go func() {
		ex := NewExchange(context.Background())
		ex.SetHeader("seq", 1)
		processDone <- r.Process(ex)
	}()
	<-inDelivery

	stopDone := make(chan error, 1)
	go func() {
		stopDone <- r.Stop()
	}()
	assert.Never(t, func() bool { return len(stopDone) > 0 }, 50*time.Millisecond, 5*time.Millisecond)

	close(release)
	assert.ErrorIs(t, <-processDone, ErrStopRouting)
	require.NoError(t, <-stopDone)
	assert.Equal(t, int32(1), syncs.completed.Load())
}

func TestDeadLetterChannel_CleansUpSynchronizedMapAndEnricherCompletesChildExchange(t *testing.T) {
	ctx := NewCamelContext()
	var enrichSyncs countingSync

	_, err := ctx.CreateRouteBuilder().
		From("direct:dlq-clean").
		ProcessFunc(func(*Exchange) error { return nil }).
		Build()
	require.NoError(t, err)

	_, err = ctx.CreateRouteBuilder().
		From("direct:enrich-uow").
		ProcessFunc(func(e *Exchange) error {
			e.AddSynchronization(&enrichSyncs)
			e.GetOut().SetBody("enriched")
			return nil
		}).
		Build()
	require.NoError(t, err)
	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })

	dlc := NewDeadLetterChannel(ctx, "direct:dlq-clean")
	ex := NewExchange(context.Background())
	_ = dlc.HandleError(ex, errors.New("boom"))
	dlc.mu.Lock()
	syncLen := len(dlc.synchronized)
	retryLen := len(dlc.retryAttempts)
	dlc.mu.Unlock()
	assert.Equal(t, 0, syncLen)
	assert.Equal(t, 0, retryLen)

	enricher := NewContentEnricher(ctx, "direct:enrich-uow", nil)
	parentEx := NewExchange(context.Background())
	require.NoError(t, enricher.Process(parentEx))
	assert.Equal(t, "enriched", parentEx.GetBody())
	assert.Equal(t, int32(1), enrichSyncs.completed.Load())
}

func TestCircuitBreaker_StaleClosedRequestDoesNotCorruptHalfOpen(t *testing.T) {
	cb := NewCircuitBreaker()
	cb.SetFailureThreshold(1)
	cb.SetSuccessThreshold(2)
	cb.SetOpenTimeout(10 * time.Millisecond)

	slowStarted := make(chan struct{})
	releaseSlow := make(chan struct{})
	var call atomic.Int32

	cb.SetProcessor(ProcessorFunc(func(*Exchange) error {
		switch call.Add(1) {
		case 1:
			close(slowStarted)
			<-releaseSlow
			return errors.New("stale failure from closed era")
		case 2:
			return errors.New("opens circuit immediately")
		default:
			return nil
		}
	}))

	slowDone := make(chan error, 1)
	go func() {
		slowDone <- cb.Process(NewExchange(context.Background()))
	}()
	<-slowStarted

	// Call 2 fails and opens the circuit while Call 1 is still in flight.
	require.Error(t, cb.Process(NewExchange(context.Background())))

	time.Sleep(20 * time.Millisecond)
	// Call 3 transitions Open -> HalfOpen and succeeds (1 of 2 successes needed to close).
	require.NoError(t, cb.Process(NewExchange(context.Background())))

	// Now release Call 1 (from the old Closed state); its failure must not reopen HalfOpen.
	close(releaseSlow)
	require.Error(t, <-slowDone)

	// Call 4 succeeds in HalfOpen and closes the circuit (would fail with ErrCircuitOpen if Call 1 reopened it).
	require.NoError(t, cb.Process(NewExchange(context.Background())))
	cb.mu.RLock()
	state := cb.state
	cb.mu.RUnlock()
	assert.Equal(t, StateClosed, state)
	_ = http.StatusOK
}

func TestLifecycle_ConcurrentStartStopDoesNotLeakOrDeadlock(t *testing.T) {
	t.Run("http", func(t *testing.T) {
		ep, err := NewHTTPComponent().CreateEndpoint("http://127.0.0.1:0/concurrent")
		require.NoError(t, err)
		consumer, err := ep.CreateConsumer(ProcessorFunc(func(*Exchange) error { return nil }))
		require.NoError(t, err)

		for i := 0; i < 10; i++ {
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				_ = consumer.Start(context.Background())
			}()
			go func() {
				defer wg.Done()
				_ = consumer.Stop()
			}()
			wg.Wait()
			require.NoError(t, consumer.Stop())
		}
	})

	t.Run("net", func(t *testing.T) {
		ep, err := NewNetComponent().CreateEndpoint("net:tcp://127.0.0.1:0?sync=false")
		require.NoError(t, err)
		consumer, err := ep.CreateConsumer(ProcessorFunc(func(*Exchange) error { return nil }))
		require.NoError(t, err)

		for i := 0; i < 10; i++ {
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				_ = consumer.Start(context.Background())
			}()
			go func() {
				defer wg.Done()
				_ = consumer.Stop()
			}()
			wg.Wait()
			require.NoError(t, consumer.Stop())
		}
	})

	t.Run("management", func(t *testing.T) {
		ctx := NewCamelContext()
		mgmt := NewManagementServer(ctx)
		for i := 0; i < 10; i++ {
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				_ = mgmt.Start("127.0.0.1:0")
			}()
			go func() {
				defer wg.Done()
				_ = mgmt.Stop()
			}()
			wg.Wait()
			require.NoError(t, mgmt.Stop())
		}
	})
}
