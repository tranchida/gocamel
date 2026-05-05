package gocamel

import (
	"fmt"
	"time"
)

// ErrorHandler defines the strategy for handling exceptions during route processing.
// Implementations include DeadLetterChannel, DefaultErrorHandler, NoErrorHandler, etc.
// In the EIP catalog, this is the Error Handler pattern.
type ErrorHandler interface {
	// HandleError processes a failed exchange. It receives the exchange that caused
	// the error and the error itself. It can modify the exchange (e.g., set headers
	// with error details) and may itself return an error if handling fails.
	// Returning nil means the error was successfully handled and the exchange
	// processing should continue (e.g., after redelivery succeeds).
	HandleError(exchange *Exchange, err error) error
}

// NoErrorHandler is the default error handler: errors are propagated immediately
// without any redelivery or dead-letter routing.
type NoErrorHandler struct{}

func (h *NoErrorHandler) HandleError(exchange *Exchange, err error) error {
	return err // bubble up
}

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
	context              *CamelContext
	deadLetterURI        string
	maxRedeliveries      int
	redeliveryDelay      time.Duration
	useExponentialBackoff bool
	backoffMultiplier    float64
	retryAttempts        map[string]int // keyed by exchange property
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
func ExponentialBackoff(multiplier float64) DeadLetterChannelOption {
	return func(dlc *DeadLetterChannel) {
		dlc.useExponentialBackoff = true
		dlc.backoffMultiplier = multiplier
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
		retryAttempts:         make(map[string]int),
	}
	for _, opt := range opts {
		opt(dlc)
	}
	return dlc
}

// HandleError implements the ErrorHandler interface.
// It first attempts redelivery (if configured), then sends the failed exchange
// to the dead letter endpoint.
func (dlc *DeadLetterChannel) HandleError(exchange *Exchange, err error) error {
	// Generate an exchange identifier for retry tracking.
	// Use the exchange pointer address + route ID as a unique key.
	exID := fmt.Sprintf("%p", exchange)

	// 1. Try redelivery
	attempt, exists := dlc.retryAttempts[exID]
	if !exists {
		attempt = 0
	}
	dlc.retryAttempts[exID] = attempt + 1

	if attempt < dlc.maxRedeliveries {
		// Wait before retrying
		delay := dlc.redeliveryDelay
		if dlc.useExponentialBackoff && attempt > 0 {
			for i := 1; i < attempt; i++ {
				delay = time.Duration(float64(delay) * dlc.backoffMultiplier)
			}
		}
		time.Sleep(delay)

		// Reset the output message for the retry
		exchange.Out = NewMessage()
		return nil // signal to retry the exchange
	}

	// 2. Max redeliveries reached or no redelivery configured
	// Clean up retry state
	delete(dlc.retryAttempts, exID)

	// Prepare exchange for dead letter — inject error details as properties and headers
	exchange.SetProperty(CamelExceptionCaught, err)
	exchange.SetProperty(CamelRedeliveryCounter, attempt)
	exchange.SetProperty("CamelDeadLetterChannel", dlc.deadLetterURI)

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
	exchange.GetIn().SetHeader("CamelExceptionMessage", err.Error())

	// Store the error on the exchange itself
	exchange.Error = err

	// Send to dead letter endpoint
	deadEndpoint, createErr := dlc.context.CreateEndpoint(dlc.deadLetterURI)
	if createErr != nil {
		return fmt.Errorf("dead letter channel: failed to create endpoint %s: %w (original error: %v)",
			dlc.deadLetterURI, createErr, err)
	}

	producer, prodErr := deadEndpoint.CreateProducer()
	if prodErr != nil {
		return fmt.Errorf("dead letter channel: failed to create producer for %s: %w (original error: %v)",
			dlc.deadLetterURI, prodErr, err)
	}

	if startErr := producer.Start(exchange.Context); startErr != nil {
		return fmt.Errorf("dead letter channel: failed to start producer: %w", startErr)
	}
	defer producer.Stop()

	// Send the failed exchange to the dead letter queue
	sendErr := producer.Send(exchange)
	if sendErr != nil {
		return fmt.Errorf("dead letter channel: failed to send to %s: %w (original error: %v)",
			dlc.deadLetterURI, sendErr, err)
	}

	// The exchange has been successfully sent to dead letter.
	// Return the original error so the caller knows processing failed.
	return err
}

// ResetRetryState clears the retry counter for a specific exchange.
// Call this after a successful retry to clean up.
func (dlc *DeadLetterChannel) ResetRetryState(exchangeID string) {
	delete(dlc.retryAttempts, exchangeID)
}
