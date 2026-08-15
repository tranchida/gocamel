+++
title = "Features"
weight = 1
+++
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

```mermaid
flowchart TB
    subgraph Exchange["Exchange"]
        direction TB
        subgraph Properties["Properties"]
            P["correlationId: 'abc-123'<br/>routeId: 'route-1'"]
        end
        subgraph In["In (Message)"]
            I["<b>Headers:</b> map[string]any<br/>Content-Type: 'application/json'<br/><br/><b>Body:</b> any<br/>{'name': 'John', 'age': 30}"]
        end
        subgraph Out["Out (Message)"]
            O["<b>Headers:</b> map[string]any<br/>X-Processed: 'true'<br/><br/><b>Body:</b> any<br/>{'name': 'John', 'age': 31}"]
        end
    end
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

#### Security

- **Path Validation** — Protection against directory traversal attacks
- **SQL Injection Prevention** — Parameter binding; the statement is trusted
  configuration and is never interpolated with message data
- **Input Sanitization** — Null byte and control character removal
- **Safe Path Operations** — Bounds-checking for file operations, with symlink
  resolution so a link inside the base directory cannot escape it
- **No credential reflection** — the HTTP consumer never echoes inbound
  request headers back in its response
- **Bounded body reads** — HTTP request and response bodies are size-limited
  (32 MiB by default) to prevent memory exhaustion
- **Panic containment** — a panicking processor fails its exchange instead of
  the process

#### File Transfer

- **File** — Local filesystem
- **FTP/FTPS** — File Transfer Protocol
- **SFTP** — SSH File Transfer
- **SMB** — Windows shares

#### Network

- **HTTP** — HTTP server/client
- **Net** — Raw TCP/UDP sockets
- **gRPC** — High-performance RPC client/server

#### Messaging

- **Kafka** — Apache Kafka distributed event streaming
- **NATS** — High-performance event messaging
- **Redis** — Commands and Pub/Sub
- **Telegram** — Bot API
- **Mail** — SMTP/IMAP/POP3
- **Go Channel** — In-memory buffered channels

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

| Pattern     | Description            | Status |
|-------------|------------------------|--------|
| Choice      | Content-based router   | ✅      |
| Split       | Message splitter       | ✅      |
| Aggregate   | Message aggregator     | ✅      |
| Multicast   | Multiple destinations  | ✅      |
| Filter      | Conditional filtering  | ✅      |
| Transform   | Message transformation | ✅      |
| Enrich      | Content enricher       | ✅      |
| PollEnrich  | On-demand enricher     | ✅      |
| Validate    | Message validator      | ✅      |
| ToD         | Dynamic endpoint       | ✅      |
| Stop        | Stop routing           | ✅      |
| Pipeline    | Sequential branches    | ✅      |
| SetHeader   | Header manipulation    | ✅      |
| SetProperty | Property manipulation  | ✅      |

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

