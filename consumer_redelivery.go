package gocamel

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

// Defaults of ConsumerRedeliveryPolicy.
const (
	DefaultConsumerMaxRedeliveries    = 10
	DefaultConsumerRedeliveryDelay    = 100 * time.Millisecond
	DefaultConsumerMaxRedeliveryDelay = 30 * time.Second
)

// ConsumerRedeliveryPolicy controls how a broker consumer (Kafka, STOMP...)
// re-runs a message whose route failed before acknowledging it.
//
// Redeliveries back off exponentially from Delay up to MaxDelay, so a
// message that keeps failing does not spin and flood the logs. When
// MaxRedeliveries is reached the consumer gives up on the message and
// acknowledges it so the rest of the partition or subscription can progress.
type ConsumerRedeliveryPolicy struct {
	// MaxRedeliveries bounds the redeliveries of one message. A negative
	// value retries until the route succeeds (no message is ever skipped);
	// 0 acknowledges a failed message right away.
	MaxRedeliveries int
	Delay           time.Duration // first redelivery delay
	MaxDelay        time.Duration // cap of the exponential backoff
}

// NewConsumerRedeliveryPolicy returns the default policy: 10 redeliveries
// with an exponential backoff from 100ms to 30s (about 80s in total) before
// the message is logged and acknowledged.
func NewConsumerRedeliveryPolicy() ConsumerRedeliveryPolicy {
	return ConsumerRedeliveryPolicy{
		MaxRedeliveries: DefaultConsumerMaxRedeliveries,
		Delay:           DefaultConsumerRedeliveryDelay,
		MaxDelay:        DefaultConsumerMaxRedeliveryDelay,
	}
}

// ParseConsumerRedeliveryPolicy reads the policy from the endpoint URI options
// maxRedeliveries, redeliveryDelay and maxRedeliveryDelay (delays in
// milliseconds). Invalid values are logged and replaced by the default.
func ParseConsumerRedeliveryPolicy(u *url.URL) ConsumerRedeliveryPolicy {
	p := NewConsumerRedeliveryPolicy()
	if v := GetConfigValue(u, "maxRedeliveries"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			p.MaxRedeliveries = n
		} else {
			LogWarn(u.Scheme+": invalid maxRedeliveries; using default", "value", v, "default", p.MaxRedeliveries)
		}
	}
	p.Delay = parseMillisOption(u, "redeliveryDelay", p.Delay)
	p.MaxDelay = parseMillisOption(u, "maxRedeliveryDelay", p.MaxDelay)
	if p.MaxDelay < p.Delay {
		p.MaxDelay = p.Delay
	}
	return p
}

func parseMillisOption(u *url.URL, key string, def time.Duration) time.Duration {
	v := GetConfigValue(u, key)
	if v == "" {
		return def
	}
	ms, err := strconv.ParseInt(v, 10, 64)
	if err != nil || ms < 0 {
		LogWarn(u.Scheme+": invalid "+key+"; using default", "value", v, "default", def)
		return def
	}
	return time.Duration(ms) * time.Millisecond
}

// Exhausted reports whether a message already redelivered `redeliveries`
// times must be given up on.
func (p ConsumerRedeliveryPolicy) Exhausted(redeliveries int) bool {
	return p.MaxRedeliveries >= 0 && redeliveries >= p.MaxRedeliveries
}

// Backoff returns the delay before redelivery number redeliveries+1.
func (p ConsumerRedeliveryPolicy) Backoff(redeliveries int) time.Duration {
	delay := exponentialBackoffDelay(p.Delay, 2, redeliveries)
	if p.MaxDelay > 0 && delay > p.MaxDelay {
		delay = p.MaxDelay
	}
	return delay
}

// Wait sleeps for Backoff(redeliveries). It returns false when ctx is
// cancelled first, i.e. the consumer is stopping.
func (p ConsumerRedeliveryPolicy) Wait(ctx context.Context, redeliveries int) bool {
	timer := time.NewTimer(p.Backoff(redeliveries))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
