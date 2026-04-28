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

## Component

Factory for endpoints of a specific type:

```go
context.AddComponent("ftp", gocamel.NewFTPComponent())
context.AddComponent("http", gocamel.NewHTTPComponent())
```

## Unit of Work & Transactions

GoCamel supports a transactional model based on the **Unit of Work** pattern. This ensures that the message source (e.g., a file, an email, or a database record) is only marked as "consumed" once the entire route has been processed successfully.

- **Synchronization**: You can register callbacks (`OnComplete`, `OnFailure`) on an `Exchange`.
- **Transacted Route**: Mark a route as transactional using `.Transacted()` in the DSL.
- **Transactional Components**: Components like `file`, `ftp`, `sftp`, `smb`, and `mail` support this model by delaying deletion or movement of the source file until the route completes.

```go
context.CreateRouteBuilder().
    From("file:///data/in?move=.done&moveFailed=.error").
    Transacted(). // Enable transactional behavior
    To("http://api.service.com").
    Build()
```
