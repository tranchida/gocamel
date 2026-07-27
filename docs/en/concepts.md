# Core Concepts

## Message

The fundamental unit of exchange containing:

- **Body**: The message payload (any type)
- **Headers**: Key-value metadata (map[string]any)

Messages provide typed accessors for convenience:

```go
body, _ := msg.GetBodyAsString()
count, _ := msg.GetHeaderAsInt("X-Count")
```

## Exchange

Container for messages passing through a route:

- **In**: Input message (from consumer)
- **Out**: Output message (to producer)
- **Properties**: Exchange-scoped metadata
- **Context**: Go context for cancellation

Exchanges also proxy typed accessors to the **In** message:

```go
body, _ := exchange.GetBodyAsString()
```

!!! warning "Thread-safety"
    `Exchange` is **not** safe for concurrent use. The `In`/`Out` messages and
    the `Properties` map have no internal locking. EIPs that fan out to
    multiple goroutines (`Multicast`, `Splitter`, `RecipientList` in parallel
    mode) call `exchange.Copy()` once per branch in the caller goroutine
    before launching workers — the parent exchange is then read-only for the
    duration of the fan-out and becomes writable again after `wg.Wait()`
    returns. If you write a custom EIP that fans out, follow the same pattern.

!!! note "Copy vs DeepCopy"
    `Copy()` duplicates the header and property **maps** but shares the body
    (and header values) **by reference**. When the copy runs concurrently with
    the original — `WireTap` taps, parallel `Multicast` branches — use
    `DeepCopy()` instead: it recursively duplicates the common mutable
    container types (`[]byte`, `[]any`, `map[string]any`, `[]string`). Values
    of other types remain shared; treat them as immutable across goroutines.

## Processor

An interface for implementing custom logic. You can use direct instances, closures, or references from the registry.

```go
type MyProcessor struct {}
func (p *MyProcessor) Process(exchange *gocamel.Exchange) error {
    // custom logic
    return nil
}

// In RouteBuilder
builder.Process(&MyProcessor{})
builder.ProcessFunc(func(e *gocamel.Exchange) error { ... })
builder.ProcessRef("myNamedBean")
```

### Panic recovery

A processor that panics **fails its exchange, not the process**. The panic is
recovered, its stack is logged, and it is converted into an error wrapping
`gocamel.ErrProcessorPanic` — an ordinary route error that the configured
`ErrorHandler` (redelivery, dead letter) can act on:

```go
if errors.Is(err, gocamel.ErrProcessorPanic) {
    // the exchange failed on a panic rather than a returned error
}
```

This applies both to the synchronous route pipeline and to every goroutine the
framework owns: SEDA and chan workers, parallel Multicast and Recipient List
branches, WireTap deliveries, Aggregator completion timeouts, and the
component consumers. Without it, a nil map write in a single processor would
take down the whole application.

If you write a custom `Consumer` that invokes a processor from a goroutine you
own, call `gocamel.ProcessSafely` rather than `Process` directly:

```go
err := gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange))
```

## Registry

A central key-value store for named objects (Beans, Processors, Components).

```go
context.GetComponentRegistry().Bind("myProcessor", &MyProcessor{})
```

## Route

A chain of processors that handles a message:

```go
route := context.CreateRouteBuilder().
    From("direct:start").
    Process(processor).
    To("direct:end").
    Build()
```

## Endpoint

URI-addressable resource:

```
component://path?param=value
```

Examples:

- `file:///tmp/data`
- `ftp://host:21/incoming`
- `http://localhost:8080/api`

## Context (CamelContext)

The runtime container managing:

- Routes lifecycle (start/stop)
- Component registry
- Endpoint resolution
- Thread pool management

```go
context := gocamel.NewCamelContext()
context.AddRoute(route)
context.Start()
context.Stop()
```

### Graceful Shutdown

`Stop()` has no deadline: if a consumer's `Stop()` blocks (for example an HTTP
shutdown waiting on an idle keep-alive), `context.Stop()` blocks with it. For
production use prefer `GracefulStop(timeout)`, which cancels the parent
context first (so consumers observing `ctx.Done()` unwind their work loops
immediately) and then waits up to `timeout` for every route to finish
stopping. Routes still running after the deadline are abandoned and the call
returns an error describing the situation.

```go
// Bound shutdown to 30 seconds.
if err := context.GracefulStop(30 * time.Second); err != nil {
    log.Printf("graceful stop incomplete: %v", err)
}
```

## Route Validation

`Route.Validate() error` walks the pipeline at build time and reports problems
that would otherwise surface only on the first matching message:

- the accumulated construction error from `From()` (e.g. an unknown component
  scheme),
- the absence of a source endpoint,
- validation errors from any processor implementing the `Validator` interface
  (notably `ChoiceProcessor`, whose When-clause Simple expressions are
  re-parsed to surface syntax errors).

`Start()` does **not** call `Validate()` automatically — opt in from tests or
CI to fail fast on broken routes:

```go
route, err := context.CreateRouteBuilder().
    From("direct:start").
    Choice().
        When("${header.priority == 'high'}").To("direct:urgent").
        Otherwise().To("direct:normal").
    EndChoice().
    Build()
if err != nil {
    log.Fatal(err)
}
if err := route.Validate(); err != nil {
    log.Fatalf("invalid route: %v", err) // would catch a malformed When expression here
}
```

Custom processors can plug into the same mechanism by implementing the
`Validator` interface:

```go
type Validator interface {
    Validate() error
}
```

## Monitoring (Observability)

GoCamel includes native support for Prometheus and OpenTelemetry, enabling production-grade monitoring for your
integration pipelines.

### Prometheus (Metrics)

Expose metrics for any route with the interceptor from the
`gitlab.com/tranchida/gocamel/observability/prometheus` module:
`.Intercept(gocamelprom.Metrics())`. The following metrics are collected
automatically:

- `gocamel_exchanges_total`: Total processed exchanges (with `route_id` and `status` labels).
- `gocamel_exchange_duration_seconds`: Processing time distribution.
- `gocamel_exchanges_inflight`: Number of exchanges currently in flight.

### OpenTelemetry (Tracing)

Enable distributed tracing with the interceptor from the
`gitlab.com/tranchida/gocamel/observability/otel` module:
`.Intercept(gocamelotel.Tracing())`. GoCamel creates an OTel **Span** for each route execution,
providing:

- Route and Exchange identifiers in span attributes.
- Automatic trace context propagation through the pipeline.
- Error recording on the span if processing fails.

```go
route, _ := context.CreateRouteBuilder().
    From("timer:tick?period=5000").
    Intercept(gocamelprom.Metrics()).
    Intercept(gocamelotel.Tracing()).
    To("http://api.service.com").
    Build()
```

## Component

Factory for endpoints of a specific type:

```go
context.AddComponent("ftp", gocamel.NewFTPComponent())
context.AddComponent("http", gocamel.NewHTTPComponent())
```

## Unit of Work & Transactions

GoCamel supports a transactional model based on the **Unit of Work** pattern. This ensures that the message source (
e.g., a file, an email, or a database record) is only marked as "consumed" once the entire route has been processed
successfully.

- **Synchronization**: You can register callbacks (`OnComplete`, `OnFailure`) on an `Exchange`
  via `AddSynchronization`. **Every** consumer that creates exchanges (direct, seda, chan, timer,
  cron, http, file, ftp, sftp, smb, mail, redis, nats, telegram, ...) fires these callbacks when
  route processing completes. The `ErrStopRouting` control signal (Stop EIP, idempotent
  duplicates, aggregation buffering) counts as **success**, not failure.
- **Transacted Route**: Mark a route as transactional using `.Transacted()` in the DSL.
- **Transactional Components**: Components like `file`, `ftp`, `sftp`, `smb`, and `mail` support this model by delaying
  deletion or movement of the source file until the route completes.

```go
context.CreateRouteBuilder().
    From("file:///data/in?move=.done&moveFailed=.error").
    Transacted(). // Enable transactional behavior
    To("http://api.service.com").
    Build()
```
