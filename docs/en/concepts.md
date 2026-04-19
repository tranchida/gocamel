# Core Concepts

## Message

The fundamental unit of exchange containing:
- **Body**: The message payload (any type)
- **Headers**: Key-value metadata (map[string]any)
- **Attachments**: Optional file attachments

```go
msg := gocamel.NewMessage()
msg.SetBody("Hello World")
msg.SetHeader("Content-Type", "text/plain")
```

## Exchange

Container for messages passing through a route:
- **In**: Input message (from consumer)
- **Out**: Output message (to producer)
- **Properties**: Exchange-scoped metadata
- **Context**: Go context for cancellation

```go
exchange := gocamel.NewExchange(context.Background())
exchange.GetIn().SetBody(input)
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
