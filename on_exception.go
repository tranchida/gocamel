package gocamel

import (
	"errors"
	"fmt"
	"reflect"
	"time"
)

// Defaults of an OnException clause, aligned with Apache Camel's redelivery
// policy (no redelivery, 1s base delay, 60s cap) except for the backoff, which
// is exponential (multiplier 2) unless BackOffMultiplier(1) is set.
const (
	DefaultOnExceptionRedeliveryDelay    = time.Second
	DefaultOnExceptionBackOffMultiplier  = 2.0
	DefaultOnExceptionMaxRedeliveryDelay = 60 * time.Second
)

var errorInterfaceType = reflect.TypeFor[error]()

// ErrorType returns an OnException target that matches any error in the chain
// assignable to T, using errors.As. It is the readable form of new(T):
//
//	OnException(gocamel.ErrorType[*MyError]())   // same as OnException(new(*MyError))
//	OnException(gocamel.ErrorType[net.Error]())  // interface targets work too
func ErrorType[T error]() any {
	return new(T)
}

// onExceptionClause is the runtime form of one OnException block. It is built
// by the DSL before the route starts and is read-only afterwards, so it is
// shared by concurrent exchanges without locking.
type onExceptionClause struct {
	matchers            []func(error) bool
	maximumRedeliveries int
	redeliveryDelay     time.Duration
	backOffMultiplier   float64
	maxRedeliveryDelay  time.Duration
	handled             bool
	continued           bool
	handler             *Pipeline
}

func newOnExceptionClause() *onExceptionClause {
	return &onExceptionClause{
		redeliveryDelay:    DefaultOnExceptionRedeliveryDelay,
		backOffMultiplier:  DefaultOnExceptionBackOffMultiplier,
		maxRedeliveryDelay: DefaultOnExceptionMaxRedeliveryDelay,
		handler:            NewPipeline(),
	}
}

// newOnExceptionMatcher turns an OnException target into a predicate:
//   - a non-nil pointer to an error type or to an interface type (the
//     errors.As target convention, e.g. new(*MyError), &MyValueError{},
//     ErrorType[*MyError]()) matches by type with errors.As;
//   - any other error value (a sentinel such as io.EOF) matches with errors.Is.
func newOnExceptionMatcher(target any) (func(error) bool, error) {
	if target == nil {
		return nil, errors.New("target is nil")
	}
	v := reflect.ValueOf(target)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil, fmt.Errorf("target is a nil %T", target)
		}
		elem := v.Type().Elem()
		if elem.Kind() == reflect.Interface || elem.Implements(errorInterfaceType) {
			// A fresh target per call keeps the matcher safe for concurrent exchanges.
			return func(err error) bool {
				return errors.As(err, reflect.New(elem).Interface())
			}, nil
		}
	}
	if sentinel, ok := target.(error); ok {
		return func(err error) bool { return errors.Is(err, sentinel) }, nil
	}
	return nil, fmt.Errorf("unsupported target %T (want an error value or a pointer to an error type)", target)
}

func (c *onExceptionClause) matches(err error) bool {
	for _, m := range c.matchers {
		if m(err) {
			return true
		}
	}
	return false
}

// delay returns the wait before redelivery number redeliveries+1.
func (c *onExceptionClause) delay(redeliveries int) time.Duration {
	d := exponentialBackoffDelay(c.redeliveryDelay, c.backOffMultiplier, redeliveries)
	if d > c.maxRedeliveryDelay {
		d = c.maxRedeliveryDelay
	}
	return d
}

// onExceptionOutcome tells processFrom what to do after a clause handled an error.
type onExceptionOutcome int

const (
	onExceptionFail      onExceptionOutcome = iota // return the accompanying error
	onExceptionRedeliver                           // retry the failed processor
	onExceptionContinue                            // skip the failed processor
)

// matchOnException returns the first declared clause matching err, or nil.
//
// An exchange already delivered to a dead letter endpoint (IsDeadLettered) is
// never matched: its failure has been taken care of, and broker consumers
// acknowledge it for the same reason. This happens when a called route (e.g.
// via direct:) dead-letters the exchange and propagates the original error
// back to a calling route with a matching clause.
func (r *Route) matchOnException(exchange *Exchange, err error) *onExceptionClause {
	if len(r.onExceptions) == 0 || IsDeadLettered(exchange) {
		return nil
	}
	for _, c := range r.onExceptions {
		if c.matches(err) {
			return c
		}
	}
	return nil
}

// handle applies the clause to an error raised by a route processor that has
// already been redelivered `redeliveries` times.
func (c *onExceptionClause) handle(r *Route, exchange *Exchange, err error, redeliveries int) (onExceptionOutcome, error) {
	if redeliveries < c.maximumRedeliveries {
		if sleepErr := sleepWithContext(exchange, c.delay(redeliveries)); sleepErr != nil {
			return onExceptionFail, fmt.Errorf("onException: redelivery aborted: %w (original error: %w)", sleepErr, err)
		}
		exchange.Out = NewMessage()
		return onExceptionRedeliver, nil
	}

	exchange.SetProperty(CamelExceptionCaught, err)
	exchange.SetProperty(CamelRedeliveryCounter, redeliveries)
	exchange.SetProperty(CamelFailureRouteId, r.ID)

	// The handler sees the message the failed processor received, not a
	// partial output it may have written before failing.
	exchange.Out = NewMessage()
	if herr := safeProcess(func() error { return c.handler.Process(exchange) }); herr != nil && !errors.Is(herr, ErrStopRouting) {
		return onExceptionFail, fmt.Errorf("onException handler failed: %w (original error: %w)", herr, err)
	}

	switch {
	case c.continued:
		return onExceptionContinue, nil
	case c.handled:
		return onExceptionFail, fmt.Errorf("%w: handled by onException: %w", ErrStopRouting, err)
	default:
		return onExceptionFail, err
	}
}

// OnExceptionDefinition is the fluent sub-DSL of one OnException clause.
// Call End() to return to the RouteBuilder.
type OnExceptionDefinition struct {
	parent  *RouteBuilder
	clause  *onExceptionClause
	handler *RouteBuilder // builds the clause's handler pipeline
}

// fail records the first configuration error on the parent builder; Build()
// surfaces it.
func (d *OnExceptionDefinition) fail(err error) {
	if d.parent.err == nil {
		d.parent.err = err
	}
}

// MaximumRedeliveries sets how many times the failed processor is retried
// before the clause's handler runs. Default: 0 (handler runs immediately).
// Negative values (Camel's "forever") are rejected at Build time.
func (d *OnExceptionDefinition) MaximumRedeliveries(n int) *OnExceptionDefinition {
	if n < 0 {
		d.fail(fmt.Errorf("OnException: MaximumRedeliveries must be >= 0, got %d", n))
		return d
	}
	d.clause.maximumRedeliveries = n
	return d
}

// RedeliveryDelay sets the delay before the first redelivery. Later delays
// grow by BackOffMultiplier, capped by MaximumRedeliveryDelay. Default: 1s.
func (d *OnExceptionDefinition) RedeliveryDelay(delay time.Duration) *OnExceptionDefinition {
	if delay < 0 {
		d.fail(fmt.Errorf("OnException: RedeliveryDelay must be >= 0, got %s", delay))
		return d
	}
	d.clause.redeliveryDelay = delay
	return d
}

// BackOffMultiplier sets the exponential backoff factor between redeliveries.
// Default: 2. Use 1 for a fixed delay.
func (d *OnExceptionDefinition) BackOffMultiplier(multiplier float64) *OnExceptionDefinition {
	if multiplier < 1 {
		d.fail(fmt.Errorf("OnException: BackOffMultiplier must be >= 1, got %v", multiplier))
		return d
	}
	d.clause.backOffMultiplier = multiplier
	return d
}

// MaximumRedeliveryDelay caps the backoff between redeliveries. Default: 60s.
func (d *OnExceptionDefinition) MaximumRedeliveryDelay(delay time.Duration) *OnExceptionDefinition {
	if delay <= 0 {
		d.fail(fmt.Errorf("OnException: MaximumRedeliveryDelay must be > 0, got %s", delay))
		return d
	}
	d.clause.maxRedeliveryDelay = delay
	return d
}

// Handled marks matching errors as handled (Apache Camel semantics): after the
// handler runs, routing STOPS — no further route processors run — and no error
// reaches the consumer or sender, which receives the exchange as left by the
// handler. The route itself returns an error wrapping ErrStopRouting and the
// original error. Default: false — the original error propagates after the
// handler.
func (d *OnExceptionDefinition) Handled(handled bool) *OnExceptionDefinition {
	d.clause.handled = handled
	return d
}

// Continued makes routing carry on with the processor after the failed one,
// as if the error had not occurred (Apache Camel semantics). The clause's
// handler still runs first, and the next processor receives its output.
// Continued takes precedence over Handled. Default: false.
func (d *OnExceptionDefinition) Continued(continued bool) *OnExceptionDefinition {
	d.clause.continued = continued
	return d
}

// Process adds a processor to the clause's handler.
func (d *OnExceptionDefinition) Process(processor Processor) *OnExceptionDefinition {
	d.handler.Process(processor)
	return d
}

// ProcessFunc adds a processing function to the clause's handler.
func (d *OnExceptionDefinition) ProcessFunc(f func(*Exchange) error) *OnExceptionDefinition {
	d.handler.ProcessFunc(f)
	return d
}

// To adds one or more recovery endpoints to the clause's handler.
func (d *OnExceptionDefinition) To(uris ...string) *OnExceptionDefinition {
	d.handler.To(uris...)
	return d
}

// SetBody sets the output body in the clause's handler.
func (d *OnExceptionDefinition) SetBody(body any) *OnExceptionDefinition {
	d.handler.SetBody(body)
	return d
}

// SetHeader sets an output header in the clause's handler.
func (d *OnExceptionDefinition) SetHeader(key string, value any) *OnExceptionDefinition {
	d.handler.SetHeader(key, value)
	return d
}

// Log adds a log step to the clause's handler.
func (d *OnExceptionDefinition) Log(message string) *OnExceptionDefinition {
	d.handler.Log(message)
	return d
}

// End terminates the OnException clause and returns to the RouteBuilder.
func (d *OnExceptionDefinition) End() *RouteBuilder {
	d.parent.adoptErr(d.handler)
	return d.parent
}
