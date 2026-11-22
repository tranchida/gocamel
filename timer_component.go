package gocamel

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// TimerComponent implements Component for timer management.
type TimerComponent struct{}

// NewTimerComponent creates a new TimerComponent
func NewTimerComponent() *TimerComponent {
	return &TimerComponent{}
}

// CreateEndpoint creates a TimerEndpoint from the URI.
func (c *TimerComponent) CreateEndpoint(uri string) (Endpoint, error) {
	parsedURL, err := ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("error parsing timer URI: %w", err)
	}

	timerName := parsedURL.Host
	if timerName == "" {
		timerName = parsedURL.Opaque // Handle format like timer:foo
		if timerName == "" && parsedURL.Path != "" {
			timerName = parsedURL.Path
		}
	}

	if timerName == "" {
		return nil, errors.New("timer name is required")
	}

	endpoint := &TimerEndpoint{
		uri:         uri,
		timerName:   timerName,
		period:      1000,
		delay:       1000,
		repeatCount: 0,
		fixedRate:   false,
	}

	query := parsedURL.Query()

	if val := query.Get("period"); val != "" {
		v, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			LogWarn("timer: invalid period; using default", "value", val, "default", endpoint.period)
		} else if v <= 0 {
			return nil, fmt.Errorf("timer: period must be positive, got %d", v)
		} else {
			endpoint.period = v
		}
	}

	if val := query.Get("delay"); val != "" {
		if v, err := strconv.ParseInt(val, 10, 64); err == nil {
			endpoint.delay = v
		} else {
			LogWarn("timer: invalid delay; using default", "value", val, "default", endpoint.delay)
		}
	}

	if val := query.Get("repeatCount"); val != "" {
		if v, err := strconv.ParseInt(val, 10, 64); err == nil && v >= 0 {
			endpoint.repeatCount = v
		} else {
			LogWarn("timer: invalid repeatCount; using default", "value", val, "default", endpoint.repeatCount)
		}
	}

	if val := query.Get("fixedRate"); val != "" {
		if v, err := strconv.ParseBool(val); err == nil {
			endpoint.fixedRate = v
		} else {
			LogWarn("timer: invalid fixedRate; using default", "value", val, "default", endpoint.fixedRate)
		}
	}

	return endpoint, nil
}

// TimerEndpoint represents a timer endpoint.
type TimerEndpoint struct {
	uri         string
	timerName   string
	period      int64
	delay       int64
	repeatCount int64
	fixedRate   bool
}

// URI returns the endpoint URI.
func (e *TimerEndpoint) URI() string {
	return e.uri
}

// CreateProducer returns an error: Timer supports consumers only.
func (e *TimerEndpoint) CreateProducer() (Producer, error) {
	return nil, errors.New("the timer component does not support producers, only consumers")
}

// CreateConsumer creates a consumer for the timer.
func (e *TimerEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return &TimerConsumer{
		endpoint:  e,
		processor: processor,
		stopChan:  make(chan struct{}),
	}, nil
}

// TimerConsumer fires events periodically.
type TimerConsumer struct {
	endpoint   *TimerEndpoint
	processor  Processor
	stopChan   chan struct{}
	transition chan struct{}
	cancel     context.CancelFunc
	running    bool
	mu         sync.Mutex
	wg         sync.WaitGroup
}

// Start begins message generation by the timer.
func (c *TimerConsumer) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	acquireConsumerTransition(&c.mu, &c.transition)
	defer releaseConsumerTransition(&c.mu, &c.transition)

	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return errors.New("timer is already running")
	}

	runCtx, cancel := context.WithCancel(ctx)
	stopChan := make(chan struct{})
	c.stopChan = stopChan
	c.cancel = cancel
	c.running = true
	c.wg.Add(1)
	c.mu.Unlock()

	go c.run(runCtx, cancel, stopChan)
	return nil
}

// Stop stops the timer, cancels any in-flight exchange context, and waits for
// the firing goroutine to finish so a subsequent Start cannot overlap with the
// previous run.
func (c *TimerConsumer) Stop() error {
	acquireConsumerTransition(&c.mu, &c.transition)
	defer releaseConsumerTransition(&c.mu, &c.transition)

	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return nil
	}
	cancel := c.cancel
	stopChan := c.stopChan
	c.cancel = nil
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	closeStopCh(stopChan)
	c.wg.Wait()

	c.mu.Lock()
	c.running = false
	c.mu.Unlock()
	return nil
}

func (c *TimerConsumer) run(ctx context.Context, cancel context.CancelFunc, stopChan <-chan struct{}) {
	defer c.wg.Done()
	defer cancel()

	// Initial wait (delay).
	delayDuration := time.Duration(c.endpoint.delay) * time.Millisecond

	if delayDuration > 0 {
		delayTimer := time.NewTimer(delayDuration)
		select {
		case <-delayTimer.C:
		case <-stopChan:
			delayTimer.Stop()
			return
		case <-ctx.Done():
			delayTimer.Stop()
			return
		}
	}

	periodDuration := time.Duration(c.endpoint.period) * time.Millisecond
	waitDuration := periodDuration
	if waitDuration <= 0 {
		waitDuration = time.Millisecond
	}
	counter := int64(0)

	var ticker *time.Ticker
	var periodTimer *time.Timer
	if c.endpoint.fixedRate && periodDuration > 0 {
		ticker = time.NewTicker(periodDuration)
		defer ticker.Stop()
	} else {
		periodTimer = time.NewTimer(waitDuration)
		periodTimer.Stop()
		defer periodTimer.Stop()
	}

	for {
		// Guard against delay <= 0 on an already-cancelled context and select
		// ties between tick readiness and shutdown/cancellation.
		select {
		case <-stopChan:
			return
		case <-ctx.Done():
			return
		default:
		}

		// Check the repeat count limit.
		if c.endpoint.repeatCount > 0 && counter >= c.endpoint.repeatCount {
			return
		}

		counter++
		firedTime := time.Now()

		exchange := NewExchange(ctx)
		exchange.SetProperty(CamelTimerName, c.endpoint.timerName)
		exchange.SetProperty(CamelTimerPeriod, c.endpoint.period)
		exchange.SetProperty(CamelTimerCounter, counter)
		exchange.GetIn().SetHeader(CamelTimerFiredTime, firedTime)

		if err := CompleteExchange(exchange, ProcessSafely(c.processor, exchange)); err != nil {
			LogError("error processing timer event", "name", c.endpoint.timerName, "error", err)
		}

		if c.endpoint.repeatCount > 0 && counter >= c.endpoint.repeatCount {
			return
		}

		if ticker != nil {
			select {
			case <-ticker.C:
			case <-stopChan:
				return
			case <-ctx.Done():
				return
			}
		} else {
			periodTimer.Reset(waitDuration)
			select {
			case <-periodTimer.C:
			case <-stopChan:
				return
			case <-ctx.Done():
				return
			}
		}
	}
}
