package gocamel

import (
	"context"
	"fmt"
	"time"
)

// PollingEndpoint optionally supports receiving exactly one exchange without
// creating or stopping a continuous consumer. A nil result means no message
// arrived. A negative timeout waits indefinitely; zero polls without waiting.
// The caller owns completion of a returned exchange.
type PollingEndpoint interface {
	Poll(ctx context.Context, timeout time.Duration) (*Exchange, error)
}

func pollQueue(ctx context.Context, queue <-chan *Exchange, timeout time.Duration) (*Exchange, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if timeout == 0 {
		select {
		case ex := <-queue:
			return ex, nil
		default:
			return nil, nil
		}
	}
	var deadline <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		deadline = timer.C
	}
	select {
	case ex := <-queue:
		return ex, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-deadline:
		return nil, nil
	}
}

// Poll receives a single queued message, preserving the endpoint lifecycle.
func (e *SedaEndpoint) Poll(ctx context.Context, timeout time.Duration) (*Exchange, error) {
	e.mu.Lock()
	if e.consumer != nil || e.polling || e.stopped {
		e.mu.Unlock()
		return nil, fmt.Errorf("seda: endpoint is stopped or already has a receiver: %s", e.uri)
	}
	e.polling = true
	e.mu.Unlock()
	defer func() { e.mu.Lock(); e.polling = false; e.mu.Unlock() }()
	return pollQueue(ctx, e.queue, timeout)
}

// Poll receives a single queued message, preserving the endpoint lifecycle.
func (e *ChanEndpoint) Poll(ctx context.Context, timeout time.Duration) (*Exchange, error) {
	e.mu.Lock()
	if e.consumer != nil || e.polling || e.stopped {
		e.mu.Unlock()
		return nil, fmt.Errorf("chan: endpoint is stopped or already has a receiver: %s", e.uri)
	}
	e.polling = true
	e.mu.Unlock()
	defer func() { e.mu.Lock(); e.polling = false; e.mu.Unlock() }()
	return pollQueue(ctx, e.queue, timeout)
}
