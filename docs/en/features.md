# Features

## Core Capabilities

### Enterprise Integration Framework

GoCamel is a lightweight, Go-native integration framework inspired by Apache Camel. It provides:

- **Modular Architecture** — Component-based design
- **Type Safety** — Full Go type safety
- **Concurrency** — Built on Go goroutines
- **Zero External Dependencies** — Optional database drivers only

### Route & Endpoint Model

```go
context := gocamel.NewCamelContext()

route := context.CreateRouteBuilder().
    From("direct:input").      // Consumer endpoint
    Process(myProcessor).      // Custom processor
    To("direct:output").       // Producer endpoint
    Build()

context.AddRoute(route)
context.Start()
```

### Message Model

```
┌───────────────────────────────────────────────┐
│                    Exchange                     │
│  ┌─────────────────────────────────────────┐  │
│  │               Properties                │  │
│  │  correlationId: "abc-123"              │  │
│  │  routeId: "route-1"                    │  │
│  └─────────────────────────────────────────┘  │
│  ┌─────────────────────────────────────────┐  │
│  │                  In                     │  │
│  │  Headers: map[string]any                │  │
│  │    Content-Type: "application/json"    │  │
│  │                                        │  │
│  │  Body: any                              │  │
│  │    {"name": "John", "age": 30}         │  │
│  └─────────────────────────────────────────┘  │
│  ┌─────────────────────────────────────────┐  │
│  │                  Out                    │  │
│  │  Headers: map[string]any                │  │
│  │    X-Processed: "true"                 │  │
│  │                                        │  │
│  │  Body: any                              │  │
│  │    {"name": "John", "age": 31}         │  │
│  └─────────────────────────────────────────┘  │
└───────────────────────────────────────────────┘
```

### Simple Language

Dynamic expression language for routing and transformations:

```go
// Header access
SimpleSetHeader("X-ID", "${uuid}")

// Body manipulation
SimpleSetBody("Hello ${body}")

// Conditional routing
When("${header.priority} == 'high'")

// Null-safe access
When("${body?.user?.name} != ''")

// Date/time
When("${date:now:yyyy-MM-dd}")
```

### Component Ecosystem

#### Core (Built-in)
- **Direct** — In-memory routing
- **Timer** — Scheduled execution

#### File Transfer
- **File** — Local filesystem
- **FTP/FTPS** — File Transfer Protocol
- **SFTP** — SSH File Transfer
- **SMB** — Windows shares

#### Network
- **HTTP** — HTTP server/client

#### Messaging
- **Telegram** — Bot API
- **Mail** — SMTP/IMAP/POP3

#### AI
- **OpenAI** — GPT models

#### Database
- **SQL** — Query execution
- **SQL-Stored** — Stored procedures

#### Scheduling
- **Cron** — Cron expressions

#### Transformation
- **XSLT** — XML transformation
- **XSD** — Schema validation
- **Template** — Go templates
- **Exec** — Command execution

### Enterprise Integration Patterns

| Pattern | Description | Status |
|---------|-------------|--------|
| Choice | Content-based router | ✅ |
| Split | Message splitter | ✅ |
| Aggregate | Message aggregator | ✅ |
| Multicast | Multiple destinations | ✅ |
| Filter | Conditional filtering | ✅ |
| Transform | Message transformation | ✅ |
| ToD | Dynamic endpoint | ✅ |
| Stop | Stop routing | ✅ |
| Pipeline | Sequential branches | ✅ |
| SetHeader | Header manipulation | ✅ |
| SetProperty | Property manipulation | ✅ |

### REST Management API

Monitor and control routes via HTTP:

```go
mgmt := gocamel.NewManagementServer(context)
mgmt.Start(":8081")
```

**Endpoints:**
- `GET /routes` — List routes
- `GET /routes/{id}` — Route details
- `POST /routes/{id}/start` — Start route
- `POST /routes/{id}/stop` — Stop route
- `GET /health` — Health check

### Configuration

Multiple credential sources:

1. **Direct in URI** — `ftp://user:pass@host`
2. **Query parameters** — `?username=user&password=pass`
3. **Environment variables** — `FTP_PASSWORD` (recommended)

```go
builder.From("ftp://host?username=${env:FTP_USER}")
```

### Testing Support

```go
import "testing"

func TestRoute(t *testing.T) {
    ctx := gocamel.NewCamelContext()
    
    // Build test route
    route := ctx.CreateRouteBuilder().
        From("direct:test").
        SetBody("processed").
        To("direct:result").
        Build()
    
    ctx.AddRoute(route)
    ctx.Start()
    defer ctx.Stop()
    
    // Send test message
    exchange := gocamel.NewExchange(context.Background())
    exchange.GetIn().SetBody("input")
    
    endpoint, _ := ctx.CreateEndpoint("direct:test")
    producer, _ := endpoint.CreateProducer()
    producer.Send(exchange)
    
    // Assert
    result := exchange.GetOut().GetBody()
    if result != "processed" {
        t.Fail()
    }
}
```

### Performance

- **Minimal allocations** — Object pooling
- **Goroutine efficiency** — Lightweight threads
- **No reflection** — Type-safe operations
- **Lazy initialization** — On-demand loading

### Extensibility

```go
// Custom Component
type CustomComponent struct{}

func (c *CustomComponent) CreateEndpoint(uri string) (gocamel.Endpoint, error) {
    return &CustomEndpoint{}, nil
}

type CustomEndpoint struct{}

func (e *CustomEndpoint) CreateProducer() (gocamel.Producer, error) {
    return &CustomProducer{}, nil
}

func (e *CustomEndpoint) CreateConsumer(p gocamel.Processor) (gocamel.Consumer, error) {
    return &CustomConsumer{}, nil
}

// Register
ctx.AddComponent("custom", &CustomComponent{})
```

### Best Practices

- Use **environment variables** for credentials
- Define **route IDs** for management
- Implement **error handling** in processors
- Use **appropriate storage** for aggregations
- Configure **timeouts** for external systems
- Enable **REST API** for production monitoring

