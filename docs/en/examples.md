# Examples

## Overview

Collection of practical examples demonstrating GoCamel features.

## File Processing

### File to File

```go
package main

import (
    "github.com/tranchida/gocamel"
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

```go
package main

import (
    "github.com/tranchida/gocamel"
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
    mgmt.Start(":8081")
    
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
    "github.com/tranchida/gocamel"
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

### Router Example

```go
package main

import (
    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    route := ctx.CreateRouteBuilder().
        From("direct:orders").
        Choice().
            When("${header.orderType} == 'priority'").
                Log("Priority order: ${body}").
                To("direct:priority-queue").
            When("${body['amount']} > 1000").
                Log("Large order").
                To("direct:finance-approval").
            When("${header.orderType} == 'standard'").
                To("direct:standard-queue").
            Otherwise().
                Log("Unknown order type").
                To("direct:error").
        EndChoice().
        Build()

    // Priority handler
    priorityRoute := ctx.CreateRouteBuilder().
        From("direct:priority-queue").
        Log("Processing priority order...").
        Build()

    ctx.AddRoute(route)
    ctx.AddRoute(priorityRoute)
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

```go
package main

import (
    "github.com/tranchida/gocamel"
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
    _ "github.com/mattn/go-sqlite3"
    "github.com/tranchida/gocamel"
)

func main() {
    db, _ := sql.Open("sqlite3", "app.db")
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

```go
package main

import (
    "github.com/tranchida/gocamel"
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

```go
// template.txt: "Hello {{.Headers.name}}, your order is {{.Body}}"

route := ctx.CreateRouteBuilder().
    From("direct:process").
    SetHeader("name", "John").
    SetBody("12345").
    To("template:templates/email.tmpl").
    Log("Result: ${body}").
    Build()
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
    "github.com/tranchida/gocamel"
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
    mgmt.Start(":8081")

    ctx.Start()
    select {}
}
```

