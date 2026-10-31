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
		if v, err := strconv.ParseInt(val, 10, 64); err == nil {
			endpoint.period = v
		}
	}

	if val := query.Get("delay"); val != "" {
		if v, err := strconv.ParseInt(val, 10, 64); err == nil {
			endpoint.delay = v
		}
	}

	if val := query.Get("repeatCount"); val != "" {
		if v, err := strconv.ParseInt(val, 10, 64); err == nil {
			endpoint.repeatCount = v
		}
	}

	if val := query.Get("fixedRate"); val != "" {
		if v, err := strconv.ParseBool(val); err == nil {
			endpoint.fixedRate = v
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
	endpoint  *TimerEndpoint
	processor Processor
	stopChan  chan struct{}
	running   bool
	stopping  bool
	mu        sync.Mutex
	wg        sync.WaitGroup
}

// Start begins message generation by the timer.
func (c *TimerConsumer) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running || c.stopping {
		return errors.New("timer is already running")
	}

	if ctx == nil {
		ctx = context.Background()
	}
	stopChan := make(chan struct{})
	c.stopChan = stopChan
	c.running = true

	c.wg.Add(1)
	go c.run(ctx, stopChan)

	return nil
}

// Stop stops the timer and waits for the firing goroutine (and any in-flight
// exchange) to finish, so a subsequent Start cannot overlap with the previous run.
func (c *TimerConsumer) Stop() error {
	c.mu.Lock()
	if !c.running || c.stopping {
		c.mu.Unlock()
		return nil
	}

	c.stopping = true
	close(c.stopChan)
	c.mu.Unlock()
	c.wg.Wait()

	c.mu.Lock()
	c.running = false
	c.stopping = false
	c.mu.Unlock()
	return nil
}

func (c *TimerConsumer) run(ctx context.Context, stopChan <-chan struct{}) {
	defer c.wg.Done()

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
	} else if delayDuration < 0 {
		// Immediate start.
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
