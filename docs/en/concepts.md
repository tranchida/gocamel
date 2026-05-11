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

## Monitoring (Observability)

GoCamel includes native support for Prometheus and OpenTelemetry, enabling production-grade monitoring for your
integration pipelines.

### Prometheus (Metrics)

Expose metrics for any route by calling `.EnableMetrics()` in the DSL. The following metrics are collected
automatically:

- `gocamel_exchanges_total`: Total processed exchanges (with `route_id` and `status` labels).
- `gocamel_exchange_duration_seconds`: Processing time distribution.
- `gocamel_exchanges_inflight`: Number of exchanges currently in flight.

### OpenTelemetry (Tracing)

Enable distributed tracing with `.EnableTracing()`. GoCamel creates an OTel **Span** for each route execution,
providing:

- Route and Exchange identifiers in span attributes.
- Automatic trace context propagation through the pipeline.
- Error recording on the span if processing fails.

```go
route, _ := context.CreateRouteBuilder().
    From("timer:tick?period=5000").
    EnableMetrics().
    EnableTracing().
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

- **Synchronization**: You can register callbacks (`OnComplete`, `OnFailure`) on an `Exchange`.
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
