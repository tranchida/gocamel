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
    "log"

    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    // timer period and delay are integer milliseconds (default 1000)
    route, err := ctx.CreateRouteBuilder().
        From("timer:tick?period=5000").
        SetBody("Hello GoCamel!").
        LogSimple("${body}").
        Build()
    if err != nil {
        log.Fatal(err)
    }

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

    "github.com/tranchida/gocamel"
    "github.com/tranchida/gocamel/components/openai"
)

func main() {
    ctx := gocamel.NewCamelContext()
    ctx.AddComponent("openai", openai.NewOpenAIComponent())

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
    "log"

    "github.com/tranchida/gocamel"
    "github.com/tranchida/gocamel/components/file"
)

func main() {
    ctx := gocamel.NewCamelContext()
    // file is an external module (fsnotify) and must be registered
    ctx.AddComponent("file", file.NewFileComponent())

    // Move files from input to output
    route, err := ctx.CreateRouteBuilder().
        From("file://input?delete=true").
        LogSimple("Processing: ${header.CamelFileName}").
        To("file://output").
        Build()
    if err != nil {
        log.Fatal(err)
    }

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

### File to File with Transformation

```go
// requires: ctx.AddComponent("file", file.NewFileComponent())
route, err := ctx.CreateRouteBuilder().
    From("file://input?include=*.txt&delete=true").
    ProcessFunc(func(e *gocamel.Exchange) error {
        content := e.GetIn().GetBody().(string)
        // Transform content
        e.GetOut().SetBody(strings.ToUpper(content))
        return nil
    }).
    SimpleSetHeader("CamelFileName", "${header.CamelFileName}.processed").
    To("file://output").
    Build()
if err != nil {
    log.Fatal(err)
}
```

---

## HTTP Endpoints

### HTTP Echo Server

```go title="http_echo.go"
package main

import (
    "log"

    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()
    ctx.AddComponent("http", gocamel.NewHTTPComponent())

    route, err := ctx.CreateRouteBuilder().
        From("http://localhost:8080/hello").
        SimpleSetBody("Hello ${header.name}!").
        Build()
    if err != nil {
        log.Fatal(err)
    }

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
route, err := ctx.CreateRouteBuilder().
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
if err != nil {
    log.Fatal(err)
}
```

---

## FTP Integration

### FTP Download

```go
package main

import (
    "log"

    "github.com/tranchida/gocamel"
    "github.com/tranchida/gocamel/components/file"
    "github.com/tranchida/gocamel/components/ftp"
)

func main() {
    ctx := gocamel.NewCamelContext()
    ctx.AddComponent("ftp", ftp.NewFTPComponent())
    ctx.AddComponent("file", file.NewFileComponent())

    // Download from FTP — FTP_USERNAME / FTP_PASSWORD are read from the environment
    route, err := ctx.CreateRouteBuilder().
        From("ftp://ftp.example.com/incoming?delete=true&passiveMode=true").
        LogSimple("Downloaded: ${header.CamelFileName}").
        To("file://downloads").
        Build()
    if err != nil {
        log.Fatal(err)
    }

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

### SFTP with Key Auth

```go
// requires: ctx.AddComponent("sftp", sftp.NewSFTPComponent())
//           ctx.AddComponent("file", file.NewFileComponent())
route, err := ctx.CreateRouteBuilder().
    From("sftp://secure.example.com/data?username=admin").
    SimpleSetHeader("CamelFileName", "${header.CamelFileName}.secure").
    To("file://secure-downloads").
    Build()
if err != nil {
    log.Fatal(err)
}
```

---

## Content-Based Routing

### Choice Routing

```go title="choice_routing.go"
package main

import (
    "log"

    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    route, err := ctx.CreateRouteBuilder().
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
    if err != nil {
        log.Fatal(err)
    }

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
    "log"

    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    route, err := ctx.CreateRouteBuilder().
        From("direct:start").
        SimpleSetBody("📨 Message: ${body}").
        SimpleSetHeader("X-Timestamp", "${date:now}").
        SimpleSetHeader("X-Request-ID", "${uuid}").
        LogSimple("Processing at ${header.X-Timestamp}").
        To("direct:output").
        Build()
    if err != nil {
        log.Fatal(err)
    }
    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

### JSON Processing with Simple Language

```go title="json_processing.go"
package main

import (
    "log"

    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    // Expected body: {"user": {"name": "John", "email": "john@example.com"}}
    route, err := ctx.CreateRouteBuilder().
        From("direct:api").
        Choice().
            When("${header.Content-Type == 'application/json'}").
                SimpleSetHeader("X-User-Name", "${body['user']['name']}").
                SimpleSetHeader("X-User-Email", "${body['user']['email']}").
                SimpleSetBody("👤 User ${body['user']['name']} registered").
                LogSimple("📝 User registration: ${body}").
                To("direct:process-json").
            When("${header.Content-Type == 'text/plain'}").
                SimpleSetBody("📝 Message received: ${body}").
                To("direct:process-text").
            Otherwise().
                SimpleSetBody("❌ Unsupported type: ${body}").
                To("direct:error").
        EndChoice().
        Build()
    if err != nil {
        log.Fatal(err)
    }
    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

### Null-safe & Collections

```go title="null_safe_collection.go"
package main

import (
    "log"

    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    // Body: [{"name": "A"}, {"name": "B"}, {"name": "C"}]
    route, err := ctx.CreateRouteBuilder().
        From("direct:start").
        LogSimple("First: ${body[0]['name']}").
        LogSimple("Last: ${body[last]['name']}").
        LogSimple("User (null-safe): ${body?.user?.name}").
        SimpleSetHeader("X-First-Item", "${body[0]['name']}").
        SimpleSetHeader("X-Last-Item", "${body[last]['name']}").
        To("direct:output").
        Build()
    if err != nil {
        log.Fatal(err)
    }
    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

---

## Message Splitting

### CSV Processing

```go
// requires: ctx.AddComponent("file", file.NewFileComponent())
route, err := ctx.CreateRouteBuilder().
    From("file://csv-input").
    Split(func(e *gocamel.Exchange) (any, error) {
        body := e.GetIn().GetBody().(string)
        // Split CSV into lines
        return strings.Split(body, "\n"), nil
    }).
    // CamelSplitIndex/CamelSplitSize are exchange properties, not headers
    LogSimple("Processing line ${exchangeProperty.CamelSplitIndex}/${exchangeProperty.CamelSplitSize}").
    ProcessFunc(func(e *gocamel.Exchange) error {
        // Process each line
        return nil
    }).
    To("direct:processed").
    End(). // End split
    Log("All lines processed").
    Build()
if err != nil {
    log.Fatal(err)
}
```

---

## Aggregation

### Split & Aggregate

```go title="split_aggregate.go"
package main

import (
    "log"
    "strings"

    "github.com/tranchida/gocamel"
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

    route, err := ctx.CreateRouteBuilder().
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
    if err != nil {
        log.Fatal(err)
    }

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

aggregator := gocamel.NewAggregator(
    func(e *gocamel.Exchange) string {
        orderId, _ := e.GetHeaderAsString("orderId")
        return orderId
    },
    strategy,
    repo,
).
    SetCompletionSize(3).
    SetCompletionTimeout(30 * time.Second)

route, err := ctx.CreateRouteBuilder().
    From("direct:line-items").
    Aggregate(aggregator).
    LogSimple("Order complete: ${body}").
    To("direct:fulfillment").
    Build()
if err != nil {
    log.Fatal(err)
}
```

---

## Multicast

### Parallel Processing

```go
route, err := ctx.CreateRouteBuilder().
    From("direct:incoming").
    Multicast().ParallelProcessing().
        To("direct:archive").
        To("direct:audit").
        To("direct:cache").
    End().
    Log("Multicast complete").
    Build()
if err != nil {
    log.Fatal(err)
}
```

---

## Scheduled Jobs

### Timer

```go title="timer.go"
package main

import (
    "log"

    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    // Run every 5 seconds (timer period is in milliseconds)
    route, err := ctx.CreateRouteBuilder().
        From("timer:tick?period=5000").
        LogSimple("Tick at ${date:now}").
        Build()
    if err != nil {
        log.Fatal(err)
    }

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

### Cron Expression

```go
import "github.com/tranchida/gocamel/components/cron"

ctx.AddComponent("cron", cron.NewCronComponent())

// Run every minute
route, err := ctx.CreateRouteBuilder().
    From("cron://group/minutely?cron=0+*+*+*+*+*").
    LogSimple("Cron trigger at ${header.fireTime}").
    Build()
if err != nil {
    log.Fatal(err)
}
```

---

## Database Integration

### SQL Query

```go
import (
    "database/sql"
    "log"

    _ "modernc.org/sqlite"
    "github.com/tranchida/gocamel"
)

func main() {
    db, _ := sql.Open("sqlite", "app.db")
    defer db.Close()

    ctx := gocamel.NewCamelContext()

    sqlComp := gocamel.NewSQLComponent()
    sqlComp.RegisterDataSource("appdb", db)
    ctx.AddComponent("sql", sqlComp)

    route, err := ctx.CreateRouteBuilder().
        From("timer:poll?period=60000"). // period in ms: every minute
        To("sql://appdb?query=SELECT+*+FROM+events+WHERE+processed=false").
        Split(func(e *gocamel.Exchange) (any, error) {
            // Out.Body is []map[string]any (SelectList)
            return e.GetIn().GetBody(), nil
        }).
            LogSimple("Event: ${body}").
            To("direct:process-event").
        End().
        Build()
    if err != nil {
        log.Fatal(err)
    }

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

### SQL Insert

```go
route, err := ctx.CreateRouteBuilder().
    From("direct:new-user").
    ProcessFunc(func(e *gocamel.Exchange) error {
        // Bind message values as positional parameters (never interpolate them)
        name, _ := e.GetHeaderAsString("name")
        email, _ := e.GetHeaderAsString("email")
        e.GetIn().SetHeader(gocamel.SqlParameters, []any{name, email})
        return nil
    }).
    To("sql://appdb?query=INSERT+INTO+users(name,email)+VALUES(?,?)").
    LogSimple("User created, rows affected: ${header.CamelSqlRowCount}").
    Build()
if err != nil {
    log.Fatal(err)
}
```

---

## Telegram Bot

```go title="telegram_bot.go"
package main

import (
    "log"

    "github.com/tranchida/gocamel"
    "github.com/tranchida/gocamel/components/telegram"
)

func main() {
    // Set TELEGRAM_AUTHORIZATIONTOKEN env var
    ctx := gocamel.NewCamelContext()
    ctx.AddComponent("telegram", telegram.NewTelegramComponent())

    route, err := ctx.CreateRouteBuilder().
        From("telegram:bots").
        LogSimple("Message from ${header.chatId}: ${body}").
        SimpleSetBody("Echo: ${body}").
        To("telegram:bots").
        Build()
    if err != nil {
        log.Fatal(err)
    }

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
    "log"

    "github.com/tranchida/gocamel"
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

    route, err := ctx.CreateRouteBuilder().
        From("direct:notify").
        SetHeader("name", "Alice").
        SetBody("Your order is ready!").
        To("template:templates/email.tmpl").
        LogSimple("Generated email: ${body}").
        Build()
    if err != nil {
        log.Fatal(err)
    }

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

---

## Error Handling

```go
route, err := ctx.CreateRouteBuilder().
    From("direct:process").
    DoTry().
        ProcessFunc(func(e *gocamel.Exchange) error {
            // Risky operation
            return riskyOperation(e)
        }).
    DoCatch("connection refused"). // matches on the error text
        Log("Service unreachable, applying recovery").
        To("direct:recovery").
    DoCatch(""). // catch all other errors
        LogSimple("Error: ${exchangeProperty.CamelExceptionCaught}").
        To("direct:error-handler").
    End().
    Build()
if err != nil {
    log.Fatal(err)
}
```

---

## Complete Application

### File Processor with All Features

```go
package main

import (
    "log"

    "github.com/tranchida/gocamel"
    "github.com/tranchida/gocamel/components/file"
)

func main() {
    ctx := gocamel.NewCamelContext()
    ctx.AddComponent("file", file.NewFileComponent())

    // Main processing route
    mainRoute, err := ctx.CreateRouteBuilder().
        SetID("file-processor").
        From("file://input?include=*.json&delete=true").
        LogSimple("Processing: ${header.CamelFileName}").

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
    if err != nil {
        log.Fatal(err)
    }

    // Order processing
    orderRoute, err := ctx.CreateRouteBuilder().
        SetID("order-processor").
        From("direct:process-order").
        LogSimple("Processing order: ${body.id}").
        To("file://output/orders").
        Build()
    if err != nil {
        log.Fatal(err)
    }

    // Refund processing
    refundRoute, err := ctx.CreateRouteBuilder().
        SetID("refund-processor").
        From("direct:process-refund").
        LogSimple("Processing refund: ${body.id}").
        To("file://output/refunds").
        Build()
    if err != nil {
        log.Fatal(err)
    }

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