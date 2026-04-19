# Exemples

Collection d'exemples pratiques.

## Hello World

```go title="hello.go"
package main

import (
    "github.com/tranchida/gocamel"
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

## File Watcher

```go title="file_watcher.go"
package main

import (
    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()
    
    route := ctx.CreateRouteBuilder().
        From("file://input?noop=true").
        Log("New file: ${header.CamelFileName}").
        ProcessFunc(func(e *gocamel.Exchange) error {
            content := e.GetIn().GetBody().([]byte)
            // Process content...
            e.GetOut().SetBody(content)
            return nil
        }).
        To("file://output").
        Build()
    
    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

## HTTP Proxy

```go title="http_proxy.go"
package main

import (
    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()
    
    // API REST simple
    route := ctx.CreateRouteBuilder().
        From("http://localhost:8080/api").
        SetBody(`{"status":"ok"}`).
        SetHeader("Content-Type", "application/json").
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
    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()
    ctx.AddComponent("openai", gocamel.NewOpenAIComponent())
    
    // Manual producer usage
    endpoint, _ := ctx.CreateEndpoint("openai:chat?model=gpt-3.5-turbo")
    producer, _ := endpoint.CreateProducer()
    
    exchange := gocamel.NewExchange(nil)
    exchange.GetIn().SetBody("Explique l'intégration en Go")
    
    producer.Send(exchange)
    fmt.Println(exchange.GetOut().GetBody())
}
```

## Telegram Bot

```go title="telegram_bot.go"
package main

import (
    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()
    ctx.AddComponent("telegram", gocamel.NewTelegramComponent())
    
    route := ctx.CreateRouteBuilder().
        From("telegram:bots").
        Log("Message from ${header.TelegramUsername}: ${body}").
        SetBody("Merci pour votre message!").
        To("telegram:bots").
        Build()
    
    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

## Scheduled Job

```go title="scheduled_job.go"
package main

import (
    "fmt"
    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()
    ctx.AddComponent("quartz", gocamel.NewQuartzComponent())
    
    // Run every minute
    route := ctx.CreateRouteBuilder().
        From("quartz://jobs/minute?cron=0+*+*+*+*+*").
        ProcessFunc(func(e *gocamel.Exchange) error {
            fmt.Println("Job executed at:", e.GetIn().Headers["quartz.fireTime"])
            return nil
        }).
        Build()
    
    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

## Split & Aggregate

```go title="split_aggregate.go"
package main

import (
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

---

## Simple Language

### Transformation avec expressions Simple

```go title="simple_transformation.go"
package main

import (
    "github.com/tranchida/gocamel"
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

### Routage basé sur le contenu (Choice)

```go title="choice_routing.go"
package main

import (
    "github.com/tranchida/gocamel"
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

### Accès aux données JSON avec Simple Language

```go title="json_processing.go"
package main

import (
    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()
    
    // Body attendu: {"user": {"name": "John", "email": "john@example.com"}}
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

### Accès null-safe et collections

```go title="null_safe_collection.go"
package main

import (
    "github.com/tranchida/gocamel"
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
