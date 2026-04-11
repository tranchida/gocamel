package gocamel

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestTimerComponent_CreateEndpoint(t *testing.T) {
	component := NewTimerComponent()

	tests := []struct {
		name        string
		uri         string
		wantErr     bool
		wantName    string
		wantPeriod  int64
		wantDelay   int64
		wantRepeat  int64
		wantFixed   bool
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

	// Wait a bit, then stop
	time.Sleep(50 * time.Millisecond)
	err = consumer.Stop()
	if err != nil {
		t.Fatalf("Failed to stop consumer: %v", err)
	}

	// Record count and wait a bit more to ensure it stopped
	countAfterStop := processor.count.Load()
	time.Sleep(50 * time.Millisecond)

	if processor.count.Load() != countAfterStop {
		t.Errorf("Consumer did not stop, count increased from %d to %d", countAfterStop, processor.count.Load())
	}

	if countAfterStop == 0 {
		t.Errorf("Consumer did not process any events before stopping")
	}
}
