+++
title = "Examples"
weight = 22
+++
## Overview

Collection of practical examples demonstrating GoCamel features.

## Hello World

```go title="hello.go"
package main

import (
    "gitlab.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    route := ctx.CreateRouteBuilder().
        From("timer:tick?period=5s").
        SetBody("Hello GoCamel!").
        Log("${body}").
        Build()

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

## OpenAI Integration

```go title="openai_example.go"
package main

import (
    "fmt"
    "gitlab.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()
    ctx.AddComponent("openai", gocamel.NewOpenAIComponent())

    // Manual producer usage
    endpoint, _ := ctx.CreateEndpoint("openai:chat?model=gpt-3.5-turbo")
    producer, _ := endpoint.CreateProducer()

    exchange := gocamel.NewExchange(nil)
    exchange.GetIn().SetBody("Explain Go integration")

    producer.Send(exchange)
    fmt.Println(exchange.GetOut().GetBody())
}
```

---

## File Processing

### File to File

```go
package main

import (
    "gitlab.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    // Move files from input to output
    route := ctx.CreateRouteBuilder().
        From("file://input?delete=true").
        Log("Processing: ${header.CamelFileName}").
        To("file://output").
        Build()

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

### File to File with Transformation

```go
route := ctx.CreateRouteBuilder().
    From("file://input?include=*.txt&delete=true").
    ProcessFunc(func(e *gocamel.Exchange) error {
        content := e.GetIn().GetBody().(string)
        // Transform content
        e.GetOut().SetBody(strings.ToUpper(content))
        return nil
    }).
    SetHeader("CamelFileName", "${header.CamelFileName}.processed").
    To("file://output").
    Build()
```

---

## HTTP Endpoints

### HTTP Echo Server

```go title="http_echo.go"
package main

import (
    "gitlab.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()
    ctx.AddComponent("http", gocamel.NewHTTPComponent())

    route := ctx.CreateRouteBuilder().
        From("http://localhost:8080/hello").
        SetBody("Hello ${header.name}!").
        Build()

    ctx.AddRoute(route)

    // Management API
    mgmt := gocamel.NewManagementServer(ctx)
    mgmt.Start("127.0.0.1:8081")

    ctx.Start()
    select {}
}
```

### HTTP Consumer with Processing

```go
route := ctx.CreateRouteBuilder().
    From("http://localhost:8080/api/process").
    Choice().
        When("${header.Content-Type} == 'application/json'").
            ProcessFunc(func(e *gocamel.Exchange) error {
                // Process JSON
                e.GetOut().SetBody(`{"status":"ok"}`)
                e.GetOut().SetHeader("Content-Type", "application/json")
                return nil
            }).
        Otherwise().
            SetBody("Unsupported content type").
    EndChoice().
    Build()
```

---

## FTP Integration

### FTP Download

```go
package main

import (
    "gitlab.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()
    ctx.AddComponent("ftp", gocamel.NewFTPComponent())

    // Download from FTP
    route := ctx.CreateRouteBuilder().
        From("ftp://ftp.example.com/incoming?delete=true&passiveMode=true").
        Log("Downloaded: ${header.CamelFileName}").
        To("file://downloads").
        Build()

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

### SFTP with Key Auth

```go
route := ctx.CreateRouteBuilder().
    From("sftp://secure.example.com/data?username=admin").
    SetHeader("CamelFileName", "${header.CamelFileName}.secure").
    To("file://secure-downloads").
    Build()
```

---

## Content-Based Routing

### Choice Routing

```go title="choice_routing.go"
package main

import (
    "gitlab.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    route := ctx.CreateRouteBuilder().
        From("direct:start").
        Choice().
            When("${header.priority == 'high'}").
                SimpleSetBody("🚨 HIGH PRIORITY: ${body}").
                SetHeader("X-Urgent", "true").
                To("direct:urgent-queue").
            When("${header.priority == 'medium'}").
                SimpleSetBody("⚠️ MEDIUM: ${body}").
                To("direct:normal-queue").
            When("${body['count'] > 100}").
                SimpleSetBody("📦 LARGE BATCH: ${body['count']} items").
                To("direct:batch-queue").
            Otherwise().
                SimpleSetBody("📄 LOW: ${body}").
                To("direct:low-queue").
        EndChoice().
        Build()

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

---

## Simple Language

### Transformation with Simple Language Expressions

```go title="simple_transformation.go"
package main

import (
    "gitlab.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    route := ctx.CreateRouteBuilder().
        From("direct:start").
        SimpleSetBody("📨 Message: ${body}").
        SimpleSetHeader("X-Timestamp", "${date:now}").
        SimpleSetHeader("X-Request-ID", "${uuid}").
        Log("Processing at ${header.X-Timestamp}").
        To("direct:output").
        Build()

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

### JSON Processing with Simple Language

```go title="json_processing.go"
package main

import (
    "gitlab.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    // Expected body: {"user": {"name": "John", "email": "john@example.com"}}
    route := ctx.CreateRouteBuilder().
        From("direct:api").
        Choice().
            When("${header.Content-Type == 'application/json'}").
                SimpleSetHeader("X-User-Name", "${body['user']['name']}").
                SimpleSetHeader("X-User-Email", "${body['user']['email']}").
                SimpleSetBody("👤 User ${body['user']['name']} registered").
                Log("📝 User registration: ${body}").
                To("direct:process-json").
            When("${header.Content-Type == 'text/plain'}").
                SimpleSetBody("📝 Message received: ${body}").
                To("direct:process-text").
            Otherwise().
                SimpleSetBody("❌ Unsupported type: ${body}").
                To("direct:error").
        EndChoice().
        Build()

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

### Null-safe & Collections

```go title="null_safe_collection.go"
package main

import (
    "gitlab.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    // Body: [{"name": "A"}, {"name": "B"}, {"name": "C"}]
    route := ctx.CreateRouteBuilder().
        From("direct:start").
        Log("First: ${body[0]['name']}").
        Log("Last: ${body[last]['name']}").
        Log("User (null-safe): ${body?.user?.name}").
        SimpleSetHeader("X-First-Item", "${body[0]['name']}").
        SimpleSetHeader("X-Last-Item", "${body[last]['name']}").
        To("direct:output").
        Build()

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

---

## Message Splitting

### CSV Processing

```go
route := ctx.CreateRouteBuilder().
    From("file://csv-input").
    Split(func(e *gocamel.Exchange) (any, error) {
        body := e.GetIn().GetBody().(string)
        // Split CSV into lines
        return strings.Split(body, "\n"), nil
    }).
    Log("Processing line ${in.header.CamelSplitIndex}/${in.header.CamelSplitSize}").
    ProcessFunc(func(e *gocamel.Exchange) error {
        // Process each line
        return nil
    }).
    To("direct:processed").
    End(). // End split
    Log("All lines processed").
    Build()
```

---

## Aggregation

### Split & Aggregate

```go title="split_aggregate.go"
package main

import (
    "strings"
    "gitlab.com/tranchida/gocamel"
)

type StringConcatStrategy struct{}

func (s *StringConcatStrategy) Aggregate(
    oldExchange,
    newExchange *gocamel.Exchange,
) *gocamel.Exchange {
    if oldExchange == nil {
        return newExchange
    }
    oldBody := oldExchange.GetOut().GetBody().(string)
    newBody := newExchange.GetOut().GetBody().(string)
    oldExchange.GetOut().SetBody(oldBody + newBody)
    return oldExchange
}

func main() {
    ctx := gocamel.NewCamelContext()

    strategy := &StringConcatStrategy{}
    repo := gocamel.NewMemoryAggregationRepository()

    route := ctx.CreateRouteBuilder().
        From("direct:start").
        Split(func(e *gocamel.Exchange) (any, error) {
            body := e.GetIn().GetBody().(string)
            return strings.Split(body, ","), nil
        }).
        To("direct:transform").
        End().
        Aggregate(gocamel.NewAggregator(
            func(e *gocamel.Exchange) string { return "group" },
            strategy,
            repo,
        ).SetCompletionSize(3)).
        Build()

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

### Order Aggregation

```go
type OrderAggregationStrategy struct{}

func (s *OrderAggregationStrategy) Aggregate(
    oldEx, newEx *gocamel.Exchange,
) *gocamel.Exchange {
    if oldEx == nil {
        return newEx
    }

    // Combine orders with same ID
    old := oldEx.GetIn().GetBody()
    new := newEx.GetIn().GetBody()

    combined := fmt.Sprintf("%s + %s", old, new)
    oldEx.GetIn().SetBody(combined)
    return oldEx
}

// Usage
strategy := &OrderAggregationStrategy{}
repo := gocamel.NewMemoryAggregationRepository()

aggregator := gocamel.NewAggregator("${header.orderId}", strategy, repo).
    SetCompletionSize(3).
    SetCompletionTimeout(30000)

route := ctx.CreateRouteBuilder().
    From("direct:line-items").
    Aggregate(aggregator).
        Log("Order complete: ${body}").
        To("direct:fulfillment").
        End().
    Build()
```

---

## Multicast

### Parallel Processing

```go
route := ctx.CreateRouteBuilder().
    From("direct:incoming").
    Multicast().ParallelProcessing().
        To("direct:archive").
        To("direct:audit").
        To("direct:cache").
    End().
    Log("Multicast complete").
    Build()
```

---

## Scheduled Jobs

### Timer

```go title="timer.go"
package main

import (
    "gitlab.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    // Run every 5 seconds
    route := ctx.CreateRouteBuilder().
        From("timer:tick?period=5s").
        Log("Tick at ${date:now}").
        Build()

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

### Cron Expression

```go
ctx.AddComponent("cron", gocamel.NewCronComponent())

// Run every minute
route := ctx.CreateRouteBuilder().
    From("cron://group/minutely?cron=0+*+*+*+*+*").
    Log("Cron trigger at ${header.fireTime}").
    Build()
```

---

## Database Integration

### SQL Query

```go
import (
    "database/sql"
    _ "modernc.org/sqlite"
    "gitlab.com/tranchida/gocamel"
)

func main() {
    db, _ := sql.Open("sqlite", "app.db")
    defer db.Close()

    ctx := gocamel.NewCamelContext()

    sqlComp := gocamel.NewSQLComponent()
    sqlComp.RegisterDataSource("appdb", db)
    ctx.AddComponent("sql", sqlComp)

    route := ctx.CreateRouteBuilder().
        From("timer:poll?period=60s").
        To("sql://appdb?query=SELECT+*+FROM+events+WHERE+processed=false").
        Split().
            Log("Event: ${body}").
            To("direct:process-event").
        End().
        Build()

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

### SQL Insert

```go
route := ctx.CreateRouteBuilder().
    From("direct:new-user").
    SetHeader("CamelSqlParameters", []any{
        "${header.name}",
        "${header.email}",
    }).
    To("sql://appdb?query=INSERT+INTO+users(name,email)+VALUES(?,?)").
    Log("User created, rows affected: ${header.CamelSqlRowCount}").
    Build()
```

---

## Telegram Bot

```go title="telegram_bot.go"
package main

import (
    "gitlab.com/tranchida/gocamel"
)

func main() {
    // Set TELEGRAM_AUTHORIZATIONTOKEN env var
    ctx := gocamel.NewCamelContext()
    ctx.AddComponent("telegram", gocamel.NewTelegramComponent())

    route := ctx.CreateRouteBuilder().
        From("telegram:bots").
        Log("Message from ${header.chatId}: ${body}").
        SetBody("Echo: ${body}").
        To("telegram:bots").
        Build()

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

---

## Template Processing

Generating formatted messages with native Go templates (inspired by Apache Camel's Velocity component).

```go title="template_email.go"
package main

import (
    "gitlab.com/tranchida/gocamel"
)

// templates/email.tmpl:
// Hello {{.Headers.name}},
//
// {{.Body}}
//
// Sent on {{now | formatDate "2006-01-02"}}

func main() {
    ctx := gocamel.NewCamelContext()
    ctx.AddComponent("template", gocamel.NewTemplateComponent())

    route := ctx.CreateRouteBuilder().
        From("direct:notify").
        SetHeader("name", "Alice").
        SetBody("Your order is ready!").
        To("template:templates/email.tmpl").
        Log("Generated email: ${body}").
        Build()

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

### With template cache

```go
To("template:templates/email.tmpl?contentCache=true")
```

### Available template functions

| Function     | Description         | Example   |
|--------------|---------------------|-----------|
| `upper`      | Uppercase           | `{{.Body  | upper}}` |
| `lower`      | Lowercase           | `{{.Body  | lower}}` |
| `trim`       | Trim whitespace     | `{{.Body  | trim}}` |
| `now`        | Current date        | `{{now}}` |
| `formatDate` | Format date         | `{{now    | formatDate "2006-01-02"}}` |
| `toString`   | Convert to string   | `{{.Body  | toString}}` |
| `safeHTML`   | Unescaped HTML      | `{{.Body  | safeHTML}}` |

```

---

## Error Handling

```go
route := ctx.CreateRouteBuilder().
    From("direct:process").
    DoTry().
        ProcessFunc(func(e *gocamel.Exchange) error {
            // Risky operation
            return riskyOperation(e)
        }).
    DoCatch(Exception.class).
        Log("Error: ${exception.message}").
        To("direct:error-handler").
    EndDoTry().
    Build()
```

---

## Complete Application

### File Processor with All Features

```go
package main

import (
    "gitlab.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    // Main processing route
    mainRoute := ctx.CreateRouteBuilder().
        SetID("file-processor").
        From("file://input?include=*.json&delete=true").
        Log("Processing: ${header.CamelFileName}").

        // Parse JSON
        ProcessFunc(func(e *gocamel.Exchange) error {
            // Parse JSON here
            return nil
        }).

        // Route by type
        Choice().
            When("${body.type} == 'order'").
                To("direct:process-order").
            When("${body.type} == 'refund'").
                To("direct:process-refund").
            Otherwise().
                To("direct:unknown").
        EndChoice().

        Build()

    // Order processing
    orderRoute := ctx.CreateRouteBuilder().
        SetID("order-processor").
        From("direct:process-order").
        Log("Processing order: ${body.id}").
        To("file://output/orders").
        Build()

    // Refund processing
    refundRoute := ctx.CreateRouteBuilder().
        SetID("refund-processor").
        From("direct:process-refund").
        Log("Processing refund: ${body.id}").
        To("file://output/refunds").
        Build()

    // Add routes
    ctx.AddRoute(mainRoute)
    ctx.AddRoute(orderRoute)
    ctx.AddRoute(refundRoute)

    // Management
    mgmt := gocamel.NewManagementServer(ctx)
    mgmt.Start("127.0.0.1:8081")

    ctx.Start()
    select {}
}
```