package gocamel

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTimerComponent_CreateEndpoint(t *testing.T) {
	component := NewTimerComponent()

	tests := []struct {
		name       string
		uri        string
		wantErr    bool
		wantName   string
		wantPeriod int64
		wantDelay  int64
		wantRepeat int64
		wantFixed  bool
	}{
		{
			name:       "Basic timer",
			uri:        "timer:foo",
			wantErr:    false,
			wantName:   "foo",
			wantPeriod: 1000,
			wantDelay:  1000,
			wantRepeat: 0,
			wantFixed:  false,
		},
		{
			name:       "Timer with path",
			uri:        "timer://foo",
			wantErr:    false,
			wantName:   "foo",
			wantPeriod: 1000,
			wantDelay:  1000,
			wantRepeat: 0,
			wantFixed:  false,
		},
		{
			name:       "Timer with all parameters",
			uri:        "timer:bar?period=2000&delay=500&repeatCount=5&fixedRate=true",
			wantErr:    false,
			wantName:   "bar",
			wantPeriod: 2000,
			wantDelay:  500,
			wantRepeat: 5,
			wantFixed:  true,
		},
		{
			name:    "Missing timer name",
			uri:     "timer:",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			endpoint, err := component.CreateEndpoint(tt.uri)

			if (err != nil) != tt.wantErr {
				t.Errorf("TimerComponent.CreateEndpoint() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				timerEndpoint := endpoint.(*TimerEndpoint)
				if timerEndpoint.timerName != tt.wantName {
					t.Errorf("TimerEndpoint.timerName = %v, want %v", timerEndpoint.timerName, tt.wantName)
				}
				if timerEndpoint.period != tt.wantPeriod {
					t.Errorf("TimerEndpoint.period = %v, want %v", timerEndpoint.period, tt.wantPeriod)
				}
				if timerEndpoint.delay != tt.wantDelay {
					t.Errorf("TimerEndpoint.delay = %v, want %v", timerEndpoint.delay, tt.wantDelay)
				}
				if timerEndpoint.repeatCount != tt.wantRepeat {
					t.Errorf("TimerEndpoint.repeatCount = %v, want %v", timerEndpoint.repeatCount, tt.wantRepeat)
				}
				if timerEndpoint.fixedRate != tt.wantFixed {
					t.Errorf("TimerEndpoint.fixedRate = %v, want %v", timerEndpoint.fixedRate, tt.wantFixed)
				}
				if timerEndpoint.URI() != tt.uri {
					t.Errorf("TimerEndpoint.URI() = %v, want %v", timerEndpoint.URI(), tt.uri)
				}

				// CreateProducer should fail
				_, err := endpoint.CreateProducer()
				if err == nil {
					t.Error("TimerEndpoint.CreateProducer() should have returned an error")
				}
			}
		})
	}
}

type mockTimerProcessor struct {
	count atomic.Int32
}

func (p *mockTimerProcessor) Process(exchange *Exchange) error {
	p.count.Add(1)
	return nil
}

func TestTimerConsumer_RepeatCount(t *testing.T) {
	component := NewTimerComponent()
	endpoint, _ := component.CreateEndpoint("timer:test?delay=10&period=10&repeatCount=3")

	processor := &mockTimerProcessor{}
	consumer, _ := endpoint.CreateConsumer(processor)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := consumer.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start consumer: %v", err)
	}

	// Wait for processing to complete or timeout
	<-ctx.Done()

	consumer.Stop()

	if processor.count.Load() != 3 {
		t.Errorf("Expected processor to be called 3 times, got %d", processor.count.Load())
	}
}

func TestTimerConsumer_Stop(t *testing.T) {
	component := NewTimerComponent()
	// No repeatCount, so it would fire forever
	endpoint, _ := component.CreateEndpoint("timer:test?delay=10&period=10")

	processor := &mockTimerProcessor{}
	consumer, _ := endpoint.CreateConsumer(processor)

	ctx := context.Background()

	err := consumer.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start consumer: %v", err)
	}

	// Wait for at least one tick, then stop
	assert.Eventually(t, func() bool { return processor.count.Load() > 0 },
		500*time.Millisecond, 10*time.Millisecond)
	err = consumer.Stop()
	if err != nil {
		t.Fatalf("Failed to stop consumer: %v", err)
	}

	// Record count and wait a bit more to ensure it stopped
	countAfterStop := processor.count.Load()
	// time.Sleep is intentional: asserting absence within a window
	time.Sleep(50 * time.Millisecond)

	if processor.count.Load() != countAfterStop {
		t.Errorf("Consumer did not stop, count increased from %d to %d", countAfterStop, processor.count.Load())
	}

	if countAfterStop == 0 {
		t.Errorf("Consumer did not process any events before stopping")
	}
}

func TestTimerConsumer_RecoversProcessorPanic(t *testing.T) {
	component := NewTimerComponent()
	endpoint, err := component.CreateEndpoint("timer:panic?delay=5&period=10&repeatCount=2")
	if err != nil {
		t.Fatal(err)
	}

	var n atomic.Int32
	consumer, err := endpoint.CreateConsumer(ProcessorFunc(func(*Exchange) error {
		n.Add(1)
		panic("boom")
	}))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := consumer.Start(ctx); err != nil {
		t.Fatal(err)
	}
	<-ctx.Done()
	if err := consumer.Stop(); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 2 {
		t.Errorf("expected 2 ticks after panics, got %d", n.Load())
	}
}

func TestTimerConsumer_CancelsInFlightContextOnStop(t *testing.T) {
	ep, err := NewTimerComponent().CreateEndpoint("timer:cancel?delay=1&period=50")
	require.NoError(t, err)

	entered := make(chan struct{})
	var once sync.Once
	ctxErrCh := make(chan error, 1)
	delayer := NewDelayer(5 * time.Second)

	consumer, err := ep.CreateConsumer(ProcessorFunc(func(e *Exchange) error {
		once.Do(func() { close(entered) })
		err := delayer.Process(e)
		select {
		case ctxErrCh <- err:
		default:
		}
		return err
	}))
	require.NoError(t, err)

	require.NoError(t, consumer.Start(context.Background()))
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("timer tick did not start")
	}

	stopStart := time.Now()
	require.NoError(t, consumer.Stop())
	assert.Less(t, time.Since(stopStart), time.Second, "Stop must cancel in-flight timer exchange context promptly")

	select {
	case err := <-ctxErrCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight timer processor did not observe cancellation")
	}
}

func TestTimerConsumer_DelayZeroCancelledCtxAndValidationAndConcurrentStop(t *testing.T) {
	t.Run("delay_zero_on_cancelled_context_does_not_fire", func(t *testing.T) {
		ep, err := NewTimerComponent().CreateEndpoint("timer:immediate?delay=0&period=50&repeatCount=1")
		require.NoError(t, err)

		var calls atomic.Int32
		consumer, err := ep.CreateConsumer(ProcessorFunc(func(*Exchange) error {
			calls.Add(1)
			return nil
		}))
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		require.NoError(t, consumer.Start(ctx))
		require.NoError(t, consumer.Stop())
		assert.Equal(t, int32(0), calls.Load(), "delay<=0 with cancelled context must not fire")
	})

	t.Run("rejects_non_positive_period_and_warns_invalid_options", func(t *testing.T) {
		comp := NewTimerComponent()
		_, err := comp.CreateEndpoint("timer:bad?period=0")
		require.Error(t, err)
		_, err = comp.CreateEndpoint("timer:bad?period=-5")
		require.Error(t, err)

		logBuf := &syncBuffer{}
		prev := GetLogger()
		t.Cleanup(func() { SetLogger(prev) })
		SetLogger(slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelWarn})))

		_, err = comp.CreateEndpoint("timer:warn?period=abc&delay=nope&repeatCount=-2&fixedRate=maybe")
		require.NoError(t, err)

		logged := logBuf.String()
		assert.Contains(t, logged, "period")
		assert.Contains(t, logged, "delay")
		assert.Contains(t, logged, "repeatCount")
		assert.Contains(t, logged, "fixedRate")
	})

	t.Run("concurrent_stop_waits_for_worker_completion", func(t *testing.T) {
		ep, err := NewTimerComponent().CreateEndpoint("timer:concstop?delay=1&period=20")
		require.NoError(t, err)

		entered := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		consumer, err := ep.CreateConsumer(ProcessorFunc(func(*Exchange) error {
			once.Do(func() { close(entered) })
			<-release
			return nil
		}))
		require.NoError(t, err)
		require.NoError(t, consumer.Start(context.Background()))
		<-entered

		stop1Done := make(chan error, 1)
		stop2Done := make(chan error, 1)
		go func() { stop1Done <- consumer.Stop() }()
		// Wait briefly for stop1 to enter wg.Wait(), then call concurrent stop2.
		assert.Never(t, func() bool { return len(stop1Done) > 0 }, 30*time.Millisecond, 5*time.Millisecond)
		go func() { stop2Done <- consumer.Stop() }()
		assert.Never(t, func() bool { return len(stop2Done) > 0 }, 30*time.Millisecond, 5*time.Millisecond)

		close(release)
		require.NoError(t, <-stop1Done)
		require.NoError(t, <-stop2Done)
	})
}
