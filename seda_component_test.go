package gocamel

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSedaComponent_Basic(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("seda", NewSedaComponent())
	ctx.AddComponent("direct", NewDirectComponent())

	var mu sync.Mutex
	var received []string

	_, err := ctx.CreateRouteBuilder().
		From("seda:async?concurrentConsumers=2").
		ProcessFunc(func(e *Exchange) error {
			body, _ := e.GetBodyAsString()
			mu.Lock()
			received = append(received, body)
			mu.Unlock()
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	defer ctx.Stop()

	p, err := ctx.CreateEndpoint("seda:async")
	require.NoError(t, err)

	prod, err := p.CreateProducer()
	require.NoError(t, err)

	err = prod.Start(context.Background())
	require.NoError(t, err)

	for i := 0; i < 10; i++ {
		ex := NewExchange(context.Background())
		ex.In.SetBody(fmt.Sprintf("msg-%d", i))
		err := prod.Send(ex)
		require.NoError(t, err)
	}

	// Wait for processing as it's async
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(received) == 10
	}, 2*time.Second, 100*time.Millisecond)

	mu.Lock()
	assert.Len(t, received, 10)
	mu.Unlock()

	err = prod.Stop()
	require.NoError(t, err)
}

func TestSedaComponent_QueueFull(t *testing.T) {
	ctx := NewCamelContext()
	seda := NewSedaComponent()
	ctx.AddComponent("seda", seda)

	// Small queue size, no consumer to drain it
	p, err := ctx.CreateEndpoint("seda:full?size=2")
	require.NoError(t, err)

	prod, err := p.CreateProducer()
	require.NoError(t, err)

	// Filling the queue
	ex1 := NewExchange(context.Background())
	require.NoError(t, prod.Send(ex1))

	ex2 := NewExchange(context.Background())
	require.NoError(t, prod.Send(ex2))

	// Third one should block, test with timeout context
	ctxTimeout, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	ex3 := NewExchange(ctxTimeout)
	err = prod.Send(ex3)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
}

func TestSedaConsumer_StopReleasesEndpointRegistration(t *testing.T) {
	component := NewSedaComponent()
	endpoint, err := component.CreateEndpoint("seda:restart")
	require.NoError(t, err)

	consumer, err := endpoint.CreateConsumer(ProcessorFunc(func(*Exchange) error { return nil }))
	require.NoError(t, err)
	require.NoError(t, consumer.Start(context.Background()))
	require.NoError(t, consumer.Stop())

	_, err = endpoint.CreateConsumer(ProcessorFunc(func(*Exchange) error { return nil }))
	assert.NoError(t, err)
}

func TestSedaConsumer_StopUnblocksProducerAndDrains(t *testing.T) {
	component := NewSedaComponent()
	endpoint, err := component.CreateEndpoint("seda:block?size=1")
	require.NoError(t, err)

	prod, err := endpoint.CreateProducer()
	require.NoError(t, err)

	// Enqueue before any consumer starts (stopCh is nil, so Send is allowed).
	require.NoError(t, prod.Send(NewExchange(context.Background())))

	consumer, err := endpoint.CreateConsumer(ProcessorFunc(func(*Exchange) error { return nil }))
	require.NoError(t, err)
	require.NoError(t, consumer.Start(context.Background()))
	require.NoError(t, consumer.Stop())

	err = prod.Send(NewExchange(context.Background()))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stopped")
}

func TestSedaAndChan_PreStartBlockedSenderDoesNotDeadlockStop(t *testing.T) {
	newQueueEndpoint := func(t *testing.T, scheme, name string, unbuffered bool, shutdownMs int) Endpoint {
		t.Helper()
		sizeParam := "size=1"
		if unbuffered {
			sizeParam = "size=0"
		}
		if scheme == "chan" {
			sizeParam = "bufferSize=1"
			if unbuffered {
				sizeParam = "bufferSize=0"
			}
		}
		uri := fmt.Sprintf("%s:%s?%s&shutdownTimeout=%d", scheme, name, sizeParam, shutdownMs)
		if scheme == "seda" {
			ep, err := NewSedaComponent().CreateEndpoint(uri)
			require.NoError(t, err)
			return ep
		}
		ep, err := NewChanComponent().CreateEndpoint(uri)
		require.NoError(t, err)
		return ep
	}

	for _, scheme := range []string{"seda", "chan"} {
		t.Run(scheme+"/start_then_stop", func(t *testing.T) {
			ep := newQueueEndpoint(t, scheme, "prestart-start-stop", false, 20)
			prod, err := ep.CreateProducer()
			require.NoError(t, err)

			// Fill the buffer of 1 synchronously before any consumer starts.
			require.NoError(t, prod.Send(NewExchange(context.Background())))

			// Block two senders before Consumer.Start(). When Start() runs, the
			// worker takes item 1 and blocks on its processor; item 2 moves into
			// the buffer of 1; item 3 remains blocked in its pre-Start Send().
			sendErr1 := make(chan error, 1)
			sendErr2 := make(chan error, 1)
			go func() { sendErr1 <- prod.Send(NewExchange(context.Background())) }()
			go func() { sendErr2 <- prod.Send(NewExchange(context.Background())) }()

			// Wait briefly so the goroutines enter Send() before Consumer.Start().
			select {
			case err := <-sendErr1:
				t.Fatalf("pre-start sender 1 returned unexpectedly: %v", err)
			case err := <-sendErr2:
				t.Fatalf("pre-start sender 2 returned unexpectedly: %v", err)
			case <-time.After(25 * time.Millisecond):
			}

			procEntered := make(chan struct{})
			var once sync.Once
			consumer, err := ep.CreateConsumer(ProcessorFunc(func(e *Exchange) error {
				once.Do(func() { close(procEntered) })
				<-e.Context.Done()
				return e.Context.Err()
			}))
			require.NoError(t, err)

			require.NoError(t, consumer.Start(context.Background()))
			select {
			case <-procEntered:
			case <-time.After(2 * time.Second):
				t.Fatal("consumer worker did not pick up first queued item")
			}

			stopDone := make(chan error, 1)
			go func() { stopDone <- consumer.Stop() }()

			select {
			case err := <-stopDone:
				require.NoError(t, err)
			case <-time.After(2 * time.Second):
				t.Fatal("Stop deadlocked on pre-start blocked sender")
			}

			// At least the second blocked sender must have observed consumer stop.
			select {
			case err := <-sendErr2:
				if err != nil {
					assert.Contains(t, err.Error(), "stopped")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("blocked sender did not unblock")
			}
			select {
			case <-sendErr1:
			case <-time.After(2 * time.Second):
				t.Fatal("sender 1 did not unblock")
			}
		})

		t.Run(scheme+"/stop_before_start", func(t *testing.T) {
			ep := newQueueEndpoint(t, scheme, "prestart-stop-only", true, 20)
			prod, err := ep.CreateProducer()
			require.NoError(t, err)

			consumer, err := ep.CreateConsumer(ProcessorFunc(func(*Exchange) error { return nil }))
			require.NoError(t, err)

			sendErr := make(chan error, 1)
			go func() { sendErr <- prod.Send(NewExchange(context.Background())) }()

			select {
			case err := <-sendErr:
				t.Fatalf("pre-start unbuffered sender returned unexpectedly: %v", err)
			case <-time.After(25 * time.Millisecond):
			}

			stopDone := make(chan error, 1)
			go func() { stopDone <- consumer.Stop() }()

			select {
			case err := <-stopDone:
				require.NoError(t, err)
			case <-time.After(2 * time.Second):
				t.Fatal("Stop before Start deadlocked on pre-start blocked sender")
			}

			select {
			case err := <-sendErr:
				require.Error(t, err)
				assert.Contains(t, err.Error(), "stopped")
			case <-time.After(2 * time.Second):
				t.Fatal("pre-start sender did not unblock on Stop before Start")
			}
		})
	}
}

func TestSedaAndChan_ConcurrentStartStopNoRace(t *testing.T) {
	for _, scheme := range []string{"seda", "chan"} {
		t.Run(scheme, func(t *testing.T) {
			var ep Endpoint
			var err error
			if scheme == "seda" {
				ep, err = NewSedaComponent().CreateEndpoint("seda:concurrent-lifecycle")
			} else {
				ep, err = NewChanComponent().CreateEndpoint("chan:concurrent-lifecycle")
			}
			require.NoError(t, err)

			var processed atomic.Int32
			consumer, err := ep.CreateConsumer(ProcessorFunc(func(*Exchange) error {
				processed.Add(1)
				return nil
			}))
			require.NoError(t, err)

			// Start(nil) must not panic.
			var nilCtx context.Context
			require.NotPanics(t, func() {
				require.NoError(t, consumer.Start(nilCtx))
			})
			require.NoError(t, consumer.Stop())

			// Concurrent Start/Stop/Stop transitions must not race or panic.
			for i := 0; i < 20; i++ {
				var wg sync.WaitGroup
				wg.Add(3)
				go func() {
					defer wg.Done()
					_ = consumer.Start(context.Background())
				}()
				go func() {
					defer wg.Done()
					_ = consumer.Stop()
				}()
				go func() {
					defer wg.Done()
					_ = consumer.Stop()
				}()
				wg.Wait()
				require.NoError(t, consumer.Stop())
			}

			// Restarting the same consumer instance must re-register it on the endpoint.
			require.NoError(t, consumer.Start(context.Background()))
			_, dupErr := ep.CreateConsumer(ProcessorFunc(func(*Exchange) error { return nil }))
			require.Error(t, dupErr, "restarted consumer must claim endpoint registration")

			prod, err := ep.CreateProducer()
			require.NoError(t, err)
			before := processed.Load()
			require.NoError(t, prod.Send(NewExchange(context.Background())))
			require.Eventually(t, func() bool {
				return processed.Load() > before
			}, 2*time.Second, 10*time.Millisecond)
			require.NoError(t, consumer.Stop())
		})
	}
}

func TestSedaComponent_EmptyQueueNameAndZeroShutdownTimeout(t *testing.T) {
	_, err := NewSedaComponent().CreateEndpoint("seda:")
	require.Error(t, err)

	ep, err := NewSedaComponent().CreateEndpoint("seda:discard?shutdownTimeout=0")
	require.NoError(t, err)
	prod, err := ep.CreateProducer()
	require.NoError(t, err)

	var processed atomic.Int32
	entered := make(chan struct{})
	var once sync.Once
	consumer, err := ep.CreateConsumer(ProcessorFunc(func(e *Exchange) error {
		once.Do(func() { close(entered) })
		processed.Add(1)
		<-e.Context.Done()
		return e.Context.Err()
	}))
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		require.NoError(t, prod.Send(NewExchange(context.Background())))
	}
	require.NoError(t, consumer.Start(context.Background()))
	<-entered
	require.NoError(t, consumer.Stop())
	assert.Equal(t, int32(1), processed.Load(), "shutdownTimeout=0 must discard remaining queued exchanges instead of draining them")
	assert.Equal(t, 0, len(ep.(*SedaEndpoint).queue), "leftover queue items must be drained on Stop")
}
