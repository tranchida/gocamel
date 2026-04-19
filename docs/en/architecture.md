# Architecture

## Overview

GoCamel follows a modular architecture inspired by Apache Camel but adapted to Go idioms.

```mermaid
graph LR
    subgraph "Core Layer"
        A[CamelContext] --> B[Registry]
        A --> C[RouteController]
        A --> D[TypeConverter]
    end
    
    subgraph "Integration Layer"  
        E[Components] --> F[Endpoints]
        F --> G[Consumers]
        F --> H[Producers]
    end
    
    subgraph "DSL Layer"
        I[RouteBuilder] --> J[Processors]
        J --> K[EIP Patterns]
    end
    
    C --> E
    K --> G
    K --> H
```

## Layers

### 1. Core Layer

GoCamel core, independent of transports:

- **CamelContext** — Container for routes and components
- **Registry** — Named component registry
- **Exchange** — In-flight message container
- **Message** — Message body + headers

```go
// Manual creation
camelCtx := &gocamel.CamelContext{}
camelCtx.Initialize()
```

### 2. Integration Layer

Communication components:

```
Component → Endpoint → {Consumer | Producer}
```

**Example flow:**

```go
// Chained creation
component := gocamel.NewHTTPComponent()
ctx.AddComponent("http", component)

endpoint, _ := ctx.CreateEndpoint("http://localhost:8080/api")
consumer, _ := endpoint.CreateConsumer(processor)
producer, _ := endpoint.CreateProducer()
```

### 3. DSL Layer

Fluent API for building routes:

```
From("...")
    .[Processor]()
    .[EIP Pattern]()
    .To("...")
```

## Design Patterns

### Factory Pattern

```go
// Component factory
compFactories := map[string]func() gocamel.Component{
    "http":  gocamel.NewHTTPComponent,
    "file":  gocamel.NewFileComponent,
    "timer": gocamel.NewTimerComponent,
}
```

### Builder Pattern

```go
// Fluent route construction
route := builder.
    From("direct:start").
    SetHeader("X-Id", uuid.New().String()).
    ProcessFunc(transform).
    To("direct:end").
    Build()
```

### Strategy Pattern

EIPs use the Strategy Pattern:

```go
type Splitter interface {
    Split(*Exchange) (any, error)
}

type AggregationStrategy interface {
    Aggregate(oldExchange, newExchange *Exchange) *Exchange
}
```

## Concurrency

GoCamel leverages Go goroutines:

```mermaid
graph TB
    subgraph "Route Thread"
        C[Consumer] --> P1[Processor 1]
        P1 --> P2[Processor 2]
        P2 --> P3[Producer]
    end
    
    subgraph "Multicast"
        M[Multicast] --x B1[Branch 1]
        M --x B2[Branch 2]
        M --x B3[Branch 3]
    end
```

Multicast branches execute in **parallel** when configured:

```go
builder.Multicast().ParallelProcessing(). ...
```

## Extension Points

### Custom Component

```go
type MyComponent struct{}

func (c *MyComponent) CreateEndpoint(uri string) (Endpoint, error) {
    return &MyEndpoint{uri: uri}, nil
}

// Registration
ctx.AddComponent("myproto", &MyComponent{})
```

### Custom Processor

```go
func MyProcessor(exchange *gocamel.Exchange) error {
    // Custom logic
    return nil
}

builder.From("...").ProcessFunc(MyProcessor).To("...")
```
