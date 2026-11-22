package gocamel

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// ErrorHandler defines the strategy for handling exceptions during route processing.
// Implementations include DeadLetterChannel, DefaultErrorHandler, NoErrorHandler, etc.
// In the EIP catalog, this is the Error Handler pattern.
type ErrorHandler interface {
	// HandleError processes a failed exchange. It receives the exchange that caused
	// the error and the error itself, and may modify the exchange (e.g. set
	// headers with error details).
	//
	// The return value drives the route:
	//   - nil asks the route to RETRY the processor that failed (redelivery).
	//     It does not mean "swallow the error and move on": a handler that
	//     always returns nil would retry forever, so the route gives up after
	//     MaxErrorHandlerRetries consecutive retries of the same step.
	//   - an error wrapping ErrStopRouting ends routing without failing the
	//     exchange (the error was handled, e.g. dead-lettered).
	//   - any other error fails the exchange with that error.
	HandleError(exchange *Exchange, err error) error
}

// RetryResettable is implemented by error handlers that maintain per-exchange
// retry state. Route calls ResetRetryState after each successful step so that
// the counter is cleared for the next message.
type RetryResettable interface {
	ResetRetryState(exchangeID string)
}

// MaxErrorHandlerRetries bounds how many consecutive times a route retries a
// failing processor because its ErrorHandler returned nil. It is a safety net
// against a handler that returns nil to "ignore" an error, which would
// otherwise spin on the failing step forever.
const MaxErrorHandlerRetries = 10000

// ErrTooManyRetries is wrapped (together with the processor's last error) by
// the error a route returns when MaxErrorHandlerRetries is exceeded.
var ErrTooManyRetries = errors.New("error handler requested too many retries")

// NoErrorHandler is the default error handler: errors are propagated immediately
// without any redelivery or dead-letter routing.
type NoErrorHandler struct{}

func (h *NoErrorHandler) HandleError(exchange *Exchange, err error) error {
	return err // bubble up
}

var globalDeadLetterID atomic.Uint64

const deadLetterSendTimeout = 30 * time.Second

// DeadLetterChannel implements the Dead Letter Channel EIP pattern.
// When an exchange fails during routing, it is sent to a dead letter endpoint
// after optionally retrying (redelivery). The original error is preserved in
// the exchange's properties.
//
// This mirrors Apache Camel's DeadLetterChannel with support for:
//   - Configurable dead letter endpoint URI
//   - Maximum redelivery attempts
//   - Delay between redeliveries (fixed or exponential backoff)
//   - Error details injected into exchange properties
type DeadLetterChannel struct {
	context               *CamelContext
	deadLetterURI         string
	maxRedeliveries       int
	redeliveryDelay       time.Duration
	useExponentialBackoff bool
	backoffMultiplier     float64
	handled               bool
	syncPropertyKey       string
	mu                    sync.Mutex
	retryAttempts         map[string]int // keyed by exchange.ID; protected by mu
}

// DeadLetterChannelOption is a functional option for configuring DeadLetterChannel.
type DeadLetterChannelOption func(*DeadLetterChannel)

// MaxRedeliveries sets the maximum number of redelivery attempts.
// Default: 0 (no redelivery, immediately send to dead letter).
func MaxRedeliveries(n int) DeadLetterChannelOption {
	return func(dlc *DeadLetterChannel) {
		dlc.maxRedeliveries = n
	}
}

// RedeliveryDelay sets the delay between redelivery attempts.
// Default: 1 second.
func RedeliveryDelay(d time.Duration) DeadLetterChannelOption {
	return func(dlc *DeadLetterChannel) {
		dlc.redeliveryDelay = d
	}
}

// ExponentialBackoff enables exponential backoff for redeliveries.
// The delay is multiplied by backoffMultiplier on each attempt.
// Non-positive, NaN, and infinite multipliers keep the default multiplier (2.0).
func ExponentialBackoff(multiplier float64) DeadLetterChannelOption {
	return func(dlc *DeadLetterChannel) {
		dlc.useExponentialBackoff = true
		if math.IsNaN(multiplier) || math.IsInf(multiplier, 0) || multiplier <= 0 {
			LogWarn("dead letter channel: invalid exponential backoff multiplier; using default",
				"multiplier", multiplier, "default", dlc.backoffMultiplier)
			return
		}
		dlc.backoffMultiplier = multiplier
	}
}

// DeadLetterHandled marks exchanges successfully sent to the dead letter
// endpoint as handled (Apache Camel semantics): the route stops and the
// consumer sees a success, e.g. the HTTP consumer answers with the exchange
// instead of a 500. The returned error still wraps the original error (and
// ErrStopRouting) for callers inspecting it. By default the original error is
// propagated to the consumer after dead-lettering.
func DeadLetterHandled(handled bool) DeadLetterChannelOption {
	return func(dlc *DeadLetterChannel) {
		dlc.handled = handled
	}
}

// NewDeadLetterChannel creates a new DeadLetterChannel error handler.
// The deadLetterURI is the endpoint where failed exchanges will be sent
// (e.g., "log:dead", "file:errors", "jms:queue:DLQ").
func NewDeadLetterChannel(context *CamelContext, deadLetterURI string, opts ...DeadLetterChannelOption) *DeadLetterChannel {
	dlc := &DeadLetterChannel{
		context:               context,
		deadLetterURI:         deadLetterURI,
		maxRedeliveries:       0,
		redeliveryDelay:       1 * time.Second,
		useExponentialBackoff: false,
		backoffMultiplier:     2.0,
		syncPropertyKey:       fmt.Sprintf("CamelDeadLetterSynchronized.%d", globalDeadLetterID.Add(1)),
		retryAttempts:         make(map[string]int),
	}
	for _, opt := range opts {
		opt(dlc)
	}
	return dlc
}

// deadLetterSynchronization cleans up retryAttempts when an exchange succeeds
// after one or more retries — preventing a map leak on the happy path.
type deadLetterSynchronization struct {
	dlc         *DeadLetterChannel
	exchangeID  string
	syncPropKey string
}

func (s *deadLetterSynchronization) OnComplete(exchange *Exchange) {
	s.dlc.mu.Lock()
	delete(s.dlc.retryAttempts, s.exchangeID)
	s.dlc.mu.Unlock()
	if exchange != nil && s.syncPropKey != "" {
		exchange.RemoveProperty(s.syncPropKey)
	}
}

func (s *deadLetterSynchronization) OnFailure(exchange *Exchange) {
	s.OnComplete(exchange)
}

// HandleError implements the ErrorHandler interface.
// It first attempts redelivery (if configured), then sends the failed exchange
// to the dead letter endpoint.
func (dlc *DeadLetterChannel) HandleError(exchange *Exchange, err error) error {
	if exchange == nil {
		return err
	}
	if IsDeadLettered(exchange) {
		return err
	}

	exID := exchange.ID

	dlc.mu.Lock()
	if dlc.syncPropertyKey == "" {
		dlc.syncPropertyKey = fmt.Sprintf("CamelDeadLetterSynchronized.%d", globalDeadLetterID.Add(1))
	}
	if dlc.retryAttempts == nil {
		dlc.retryAttempts = make(map[string]int)
	}
	syncPropKey := dlc.syncPropertyKey
	attempt := dlc.retryAttempts[exID]
	canRetry := attempt < dlc.maxRedeliveries
	if canRetry {
		dlc.retryAttempts[exID] = attempt + 1
	} else {
		delete(dlc.retryAttempts, exID)
	}
	dlc.mu.Unlock()

	// 1. Try redelivery
	if canRetry {
		// Track synchronization registration on the exchange itself rather than in
		// a map on DeadLetterChannel, so DirectProducer exchanges that never call
		// Exchange.Done() cannot leak entries on the error handler.
		syncedID, _ := exchange.GetPropertyAsString(syncPropKey)
		if syncedID != exID {
			exchange.SetProperty(syncPropKey, exID)
			exchange.AddSynchronization(&deadLetterSynchronization{
				dlc:         dlc,
				exchangeID:  exID,
				syncPropKey: syncPropKey,
			})
		}

		// Wait before retrying: base delay for the first redelivery, then
		// base * multiplier^attempt when exponential backoff is enabled.
		delay := dlc.redeliveryDelay
		if dlc.useExponentialBackoff {
			delay = exponentialBackoffDelay(delay, dlc.backoffMultiplier, attempt)
		}
		if sleepErr := sleepWithContext(exchange, delay); sleepErr != nil {
			// Context cancelled while waiting — abort redelivery, clean up state,
			// and surface the cancellation alongside the original error.
			dlc.mu.Lock()
			delete(dlc.retryAttempts, exID)
			dlc.mu.Unlock()
			exchange.RemoveProperty(syncPropKey)
			return fmt.Errorf("dead letter channel: redelivery aborted: %w (original error: %w)", sleepErr, err)
		}

		// Reset the output message for the retry
		exchange.Out = NewMessage()
		return nil // signal to retry the exchange
	}

	// 2. Max redeliveries reached or no redelivery configured — clean up tracking property
	exchange.RemoveProperty(syncPropKey)

	redactedURI := RedactURI(dlc.deadLetterURI)

	// Prepare exchange for dead letter — inject error details as properties and headers
	exchange.SetProperty(CamelExceptionCaught, err)
	exchange.SetProperty(CamelRedeliveryCounter, attempt)
	exchange.SetProperty("CamelDeadLetterChannel", redactedURI)

	// Try to preserve failure endpoint/route from existing properties if available
	failureEndpoint, _ := exchange.GetProperty(CamelFailureEndpoint)
	if failureEndpoint == nil {
		if v, ok := exchange.GetIn().GetHeader("CamelToEndpoint"); ok {
			exchange.SetProperty(CamelFailureEndpoint, v)
		}
	}
	failureRouteID, _ := exchange.GetProperty(CamelFailureRouteId)
	if failureRouteID == nil {
		if v, ok := exchange.GetIn().GetHeader("CamelFailureRouteId"); ok {
			exchange.SetProperty(CamelFailureRouteId, v)
		}
	}

	// Set error headers on the IN message for visibility
	exchange.GetIn().SetHeader("CamelRedelivered", attempt > 0)
	exchange.GetIn().SetHeader("CamelRedeliveryCounter", attempt)
	if err != nil {
		exchange.GetIn().SetHeader("CamelExceptionMessage", err.Error())
	}

	// Store the error on the exchange itself and clear any partial output from the failed processor.
	exchange.Error = err
	exchange.Out = NewMessage()

	if dlc.context == nil {
		return fmt.Errorf("dead letter channel: CamelContext is nil (original error: %w)", err)
	}

	// Send to dead letter endpoint via the shared producer cache. Creating,
	// starting and stopping a producer for every dead-lettered message would
	// re-open a connection on exactly the path where the system is already
	// under stress; the cache keeps one started producer per URI and
	// CamelContext.Stop() owns its lifecycle.
	producer, release, prodErr := dlc.context.acquireProducer(dlc.deadLetterURI)
	if prodErr != nil {
		return fmt.Errorf("dead letter channel: failed to obtain producer for %s: %w (original error: %w)",
			redactedURI, prodErr, err)
	}
	defer release()

	// If the exchange context was already cancelled (or missing), send using a
	// bounded WithoutCancel context so DLQ delivery still succeeds.
	origCtx := exchange.Context
	if origCtx == nil || origCtx.Err() != nil {
		baseCtx := origCtx
		if baseCtx == nil {
			baseCtx = context.Background()
		}
		dlqCtx, cancel := context.WithTimeout(context.WithoutCancel(baseCtx), deadLetterSendTimeout)
		defer cancel()
		exchange.Context = dlqCtx
		defer func() { exchange.Context = origCtx }()
	}

	// Send the failed exchange to the dead letter queue
	if sendErr := producer.Send(exchange); sendErr != nil {
		return fmt.Errorf("dead letter channel: failed to send to %s: %w (original error: %w)",
			redactedURI, sendErr, err)
	}
	exchange.SetProperty(CamelDeadLettered, true)

	// The exchange has been successfully sent to dead letter. By default the
	// original error is returned so the consumer knows processing failed;
	// with DeadLetterHandled(true) the exchange counts as handled.
	if dlc.handled {
		return fmt.Errorf("%w: dead-lettered to %s: %w", ErrStopRouting, redactedURI, err)
	}
	return err
}

// IsDeadLettered reports whether a DeadLetterChannel delivered the exchange to
// its dead letter endpoint. Even when the route still returns the original
// error (DeadLetterHandled(false), the default), the message has been taken
// care of: a broker consumer must acknowledge it rather than redeliver it,
// otherwise every redelivery would dead-letter a duplicate.
func IsDeadLettered(exchange *Exchange) bool {
	if exchange == nil {
		return false
	}
	v, _ := exchange.GetPropertyAsBool(CamelDeadLettered)
	return v
}

// ResetRetryState clears the retry counter for a specific exchange ID.
func (dlc *DeadLetterChannel) ResetRetryState(exchangeID string) {
	dlc.mu.Lock()
	delete(dlc.retryAttempts, exchangeID)
	dlc.mu.Unlock()
}

// maxRedeliveryDelay caps exponential backoff so float64(duration)*multiplier
// cannot overflow time.Duration into a negative sleep.
const maxRedeliveryDelay = 24 * time.Hour

func exponentialBackoffDelay(base time.Duration, multiplier float64, attempt int) time.Duration {
	if base <= 0 {
		return 0
	}
	if math.IsNaN(multiplier) || math.IsInf(multiplier, 0) || multiplier <= 0 {
		if base > maxRedeliveryDelay {
			return maxRedeliveryDelay
		}
		return base
	}
	delay := base
	if delay > maxRedeliveryDelay {
		delay = maxRedeliveryDelay
	}
	for i := 0; i < attempt; i++ {
		next := float64(delay) * multiplier
		if math.IsNaN(next) || math.IsInf(next, 1) || next >= float64(maxRedeliveryDelay) {
			return maxRedeliveryDelay
		}
		delay = time.Duration(next)
		if delay <= 0 {
			return 0
		}
	}
	return delay
}
