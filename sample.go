package gocamel

import (
	"fmt"
	"sync"
	"time"
)

// Sampler implements the Sampling EIP.
// It lets only a sample of the exchanges through and silently drops the others
// (ErrStopRouting), regardless of their content. Two modes are supported:
//
//   - time based: the first exchange of every period passes (see NewSamplerPeriod);
//   - count based: one exchange out of every N passes (see NewSamplerEvery).
//
// Unlike Throttler, which delays exchanges, Sampler discards them.
//
// Example DSL usage:
//
//	// At most one message per second
//	route, _ := ctx.CreateRouteBuilder().
//	    From("direct:sensor").
//	    Sample(time.Second).
//	    To("direct:dashboard").
//	    Build()
//
//	// One message out of every 100
//	route, _ := ctx.CreateRouteBuilder().
//	    From("direct:sensor").
//	    SampleEvery(100).
//	    To("direct:dashboard").
//	    Build()
type Sampler struct {
	byCount bool
	period  time.Duration
	every   int64

	mu      sync.Mutex // Protects next and counter
	next    time.Time  // Earliest time at which the next exchange passes (period mode)
	counter int64      // Exchanges seen since the last one that passed (count mode)
}

// NewSamplerPeriod creates a Sampler that lets the first exchange of every
// period through and drops the others.
func NewSamplerPeriod(period time.Duration) *Sampler {
	return &Sampler{period: period}
}

// NewSamplerEvery creates a Sampler that lets one exchange out of every n through.
// The first exchange always passes.
func NewSamplerEvery(n int64) *Sampler {
	return &Sampler{byCount: true, every: n}
}

// Validate implements the Validator interface.
func (s *Sampler) Validate() error {
	if s.byCount && s.every <= 0 {
		return fmt.Errorf("sample: count must be greater than 0")
	}
	if !s.byCount && s.period <= 0 {
		return fmt.Errorf("sample: period must be greater than 0")
	}
	return nil
}

// Process implements the Processor interface.
// It returns ErrStopRouting for exchanges that are not part of the sample.
func (s *Sampler) Process(exchange *Exchange) error {
	if err := s.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.byCount {
		pass := s.counter == 0
		s.counter = (s.counter + 1) % s.every
		if pass {
			return nil
		}
		return ErrStopRouting
	}

	now := time.Now()
	if now.Before(s.next) {
		return ErrStopRouting
	}
	s.next = now.Add(s.period)
	return nil
}
