# Enterprise Integration Patterns

## Overview

GoCamel implements the Enterprise Integration Patterns (EIP) from the classic book by Gregor Hohpe and Bobby Woolf.

## Message Routing

### Choice

Content-based routing with conditional branches.

```go
builder.From("direct:start").
    Choice().
        When("${header.priority == 'high'}").
            Log("High priority: ${body}").
            To("direct:urgent").
        When("${header.type} == 'order'&& ${body['amount']} > 1000").
            Log("Large order").
            To("direct:large-orders").
        When("${header.type} == 'email'").
            To("direct:emails").
        Otherwise().
            Log("Default").
            To("direct:normal").
    EndChoice()
```

**Syntax:**
- `When(expression)` - Adds a conditional branch
- `Otherwise()` - Adds a default branch
- `EndChoice()` - Ends the Choice block
- `End()` - Alternative method

**Expression Operators:**

| Operator | Description | Example |
|----------|-------------|---------|
| `==` | Equal | `${header.type} == 'order'` |
| `!=` | Not equal | `${header.status} != 'error'` |
| `>` | Greater than | `${header.count} > 10` |
| `>=` | Greater or equal | `${header.count} >= 10` |
| `<` | Less than | `${header.price} < 100` |
| `<=` | Less or equal | `${header.price} <= 100` |
| `&&` | AND | `a > 5 && b == 'x'` |
| `\|\|` | OR | `a > 5 \|\| b == 'y'` |

---

### Filter

Filter messages based on a condition. Exchanges that do not match are silently dropped (`ErrStopRouting`).

```go
// Simple Language expression (comparison inside ${...})
builder.From("direct:start").
    Filter("${header.type == 'order'}").
    To("direct:orders").
    Build()

// Go predicate
builder.From("direct:start").
    Filter(func(e *gocamel.Exchange) (bool, error) {
        body, ok := e.GetBodyAsString()
        return ok && body != "", nil
    }).
    To("direct:nonEmpty").
    Build()
```

---

### Throttle

Rate-limit exchange processing to at most N messages per time window. Exchanges that exceed the rate are **delayed**, not dropped, until the window resets.

```go
// Max 100 messages per second
builder.From("direct:start").
    Throttle(100, time.Second).
    To("direct:downstream").
    Build()

// Max 10 messages per 500ms (smoother than 1s window)
builder.From("direct:start").
    Throttle(10, 500*time.Millisecond).
    To("direct:api").
    Build()
```

**Notes:**
- Uses a fixed-window counter; for smoother behaviour use smaller windows
- Blocking: the goroutine sleeps until the window allows the exchange through
- `maxRequests` and `timePeriod` must be greater than zero; invalid values return an error instead of blocking indefinitely
- If the exchange context is cancelled while waiting for the next window, `Process` returns an error

---

### Delay

Pause exchange processing for a fixed or dynamically computed duration. The sleep respects context cancellation.

```go
// Fixed delay
builder.From("direct:start").
    Delay(500 * time.Millisecond).
    To("direct:next").
    Build()

// Dynamic delay from a header
builder.From("direct:start").
    DelayFunc(func(e *gocamel.Exchange) (time.Duration, error) {
        ms, ok := e.GetHeaderAsInt("X-Delay-Ms")
        if !ok {
            return 0, nil  // no delay if header absent
        }
        return time.Duration(ms) * time.Millisecond, nil
    }).
    To("direct:next").
    Build()
```

**Notes:**
- Blocking: the goroutine sleeps for the computed duration
- If the exchange context is cancelled during the sleep, `Process` returns an error
- Return `0` from `DelayFunc` to skip the delay for a given exchange
- Passing `nil` to `DelayFunc` fails route construction; `NewDelayerFunc(nil)` remains constructible but returns a processing error instead of panicking

---

### Content Enricher

Fetch additional data from an external resource and merge it into the current exchange.

```go
// With aggregation strategy
type MergeStrategy struct{}

func (s *MergeStrategy) Aggregate(original, enriched *gocamel.Exchange) *gocamel.Exchange {
    origBody, _ := original.GetBodyAsString()
    enrichBody, _ := enriched.GetBodyAsString()
    original.GetIn().SetBody(origBody + " enriched with: " + enrichBody)
    return original
}

builder.From("direct:start").
    Enrich("http://api.example.com/data", &MergeStrategy{}).
    To("direct:next").
    Build()

// Without strategy — replaces body with enrichment response
builder.From("direct:start").
    Enrich("direct:lookup", nil).
    To("direct:next").
    Build()
```

**How it works:**
1. A copy of the current exchange is sent to the enrichment URI
2. The response is received
3. `AggregationStrategy.Aggregate(original, response)` merges the two
4. If no strategy is provided, the response body replaces the original body

---

### Idempotent Consumer

Prevent duplicate messages from being processed.

```go
repo := gocamel.NewMemoryIdempotentRepository()

builder.From("direct:start").
    IdempotentConsumer("${header.MessageId}", repo).
    To("direct:next").
    Build()
```

**How it works:**
1. Evaluates the expression to get a unique key.
2. Checks if the key exists in the repository.
3. If not present, adds the key and proceeds.
4. If present, stops routing (`ErrStopRouting`).

**Repositories:**
- `NewMemoryIdempotentRepository()`: In-memory store (default).
- SQL-based repositories can be implemented via the `IdempotentRepository` interface.

---

### Circuit Breaker

Protect routes from cascading failures when calling unstable external services.

```go
builder.From("direct:start").
    CircuitBreaker().
        FailureThreshold(5).           // Open after 5 failures
        OpenTimeout(30 * time.Second).  // Stay open for 30s
        SuccessThreshold(2).           // Close after 2 successes in Half-Open
        Process(unstableProcessor).
        OnFallback().
            Log("Circuit open or failed, calling fallback").
            To("direct:fallback").
        End().
    End().
    To("direct:result").
    Build()
```

**States:**
- **Closed**: Requests pass through. Failures are counted.
- **Open**: Requests fail immediately or go to fallback.
- **Half-Open**: After `OpenTimeout`, allows a few requests to test if the service has recovered. If `SuccessThreshold` is reached, it closes. If any fail, it opens again.

---

### Load Balancer

Distribute messages across multiple endpoints.

```go
builder.From("direct:start").
    LoadBalance().RoundRobin().
        To("direct:server1").
        To("direct:server2").
        To("direct:server3").
    End().
    Build()
```

**Strategies:**
- `RoundRobin()`: Circular distribution (default).
- `Random()`: Random distribution.
- `Custom(strategy)`: Provide a custom implementation of the `LoadBalancerStrategy` interface.

---

### Multicast

Send a copy of the message to multiple destinations.

```go
builder.From("direct:start").
    Multicast().
        To("direct:archive").
        To("direct:audit").
        To("direct:analytics").
    End()
```

**With Parallel Processing:**

```go
builder.From("direct:start").
    Multicast().ParallelProcessing().
        To("direct:branch1").
        To("direct:branch2").
        To("direct:branch3").
    End()
```

**With Strategy:**

```go
strategy := &MyAggregationStrategy{}

builder.From("direct:start").
    Multicast().Strategy(strategy).
        To("direct:a").
        To("direct:b").
        To("direct:c").
    End()
```

---

## Message Transformation

### Splitter

Divide a message into parts.

```go
builder.From("direct:start").
    Split(func(e *gocamel.Exchange) (any, error) {
        body := e.GetIn().GetBody().(string)
        // Split by comma
        return strings.Split(body, ","), nil
    }).
    Log("Processing part: ${body}").
    To("direct:process").
    End() // End Split
```

**With Aggregation:**

```go
type StringJoinStrategy struct{}

func (s *StringJoinStrategy) Aggregate(oldEx, newEx *gocamel.Exchange) *gocamel.Exchange {
    if oldEx == nil {
        return newEx
    }
    old := oldEx.GetIn().GetBody().(string)
    new := newEx.GetIn().GetBody().(string)
    oldEx.GetIn().SetBody(old + "," + new)
    return oldEx
}

strategy := &StringJoinStrategy{}

builder.From("direct:start").
    Split(splitter).Strategy(strategy).
        To("direct:process").
    End()
```

**Exchange Properties during Split:**

| Property | Type | Description |
|----------|------|-------------|
| `CamelSplitIndex` | int | Current index (0-based) |
| `CamelSplitSize` | int | Total number of parts |
| `CamelSplitComplete` | bool | Last part indicator |

---

### Aggregator

Combine multiple messages into one.

```go
// Define correlation expression and completion condition
correlationExpr := "${header.orderId}"
strategy := &OrderAggregationStrategy{}
repo := gocamel.NewMemoryAggregationRepository()

aggregator := gocamel.NewAggregator(correlationExpr, strategy, repo).
    SetCompletionSize(3).    // Complete when 3 messages received
    SetCompletionTimeout(5000) // Or after 5 seconds

builder.From("direct:start").
    Aggregate(aggregator).
        Log("Aggregated: ${body}").
    End()
```

**Aggregation Strategy:**

```go
type OrderAggregationStrategy struct{}

func (s *OrderAggregationStrategy) Aggregate(
    oldExchange,
    newExchange *gocamel.Exchange,
) *gocamel.Exchange {
    if oldExchange == nil {
        // First message
        return newExchange
    }
    
    // Combine messages
    old := oldExchange.GetIn().GetBody().(Order)
    new := newExchange.GetIn().GetBody().(Order)
    
    old.Items = append(old.Items, new.Items...)
    old.Total += new.Total
    
    oldExchange.GetIn().SetBody(old)
    return oldExchange
}
```

**Completion Conditions:**

| Method | Description |
|--------|-------------|
| `SetCompletionSize(n)` | Complete after n messages |
| `SetCompletionTimeout(ms)` | Complete after timeout |
| `SetCompletionPredicate(fn)` | Complete when predicate returns true |

**Storage Options:**

```go
// In-memory (default)
repo := gocamel.NewMemoryAggregationRepository()

// SQLite persistence
repo := gocamel.NewSQLAggregationRepository(db, "schema")
```

---

### Transformer

Transform message content.

```go
// Set body directly
builder.From("direct:start").
    SetBody("Hello World")

// Set body via function
builder.From("direct:start").
    SetBodyFunc(func(e *gocamel.Exchange) (any, error) {
        input := e.GetIn().GetBody().(string)
        return strings.ToUpper(input), nil
    })

// Transform via Simple Language
builder.From("direct:start").
    SimpleSetBody("Processed: ${body} at ${date:now}")
```

---

## Messaging Systems

### Pipeline

Sequential processing within multicast.

```go
builder.From("direct:start").
    Multicast().
        Pipeline().
            Log("Step 1").
            To("direct:step1").
        End().
        Pipeline().
            Log("Step 2").
            To("direct:step2").
        End().
    End()
```

---

### ToD (Dynamic To)

Send to dynamically computed endpoint.

```go
// Header determines destination
builder.From("direct:start").
    SetHeader("dest", "direct:output").
    ToD("${header.dest}")

// URI with expression
builder.From("direct:start").
    ToD("file://output/${header.filename}")
```

---

### Recipient List

Send to multiple recipients computed at runtime.

```go
// Recipients from header
builder.From("direct:start").
    SetHeader("recipients", "direct:a,direct:b,direct:c").
    RecipientList("${header.recipients}")
```

---

### Dynamic Router

Route an exchange through a sequence of endpoints resolved dynamically by calling a control function after each step. Routing ends when the function returns an empty string or `nil`.

```go
// Using a Go function
builder.From("direct:start").
    DynamicRouter(func(e *gocamel.Exchange) (string, error) {
        // Decide next step based on state/content
        count, _ := e.GetProperty("step")
        e.SetProperty("step", count.(int) + 1)
        
        if count == 0 { return "direct:a", nil }
        if count == 1 { return "direct:b", nil }
        return "", nil // End routing
    })

// Using Simple Language
builder.From("direct:start").
    DynamicRouter("${header.nextEndpoint}")
```

**How it works:**
1. Calls the control function/expression.
2. If result is non-empty, sends exchange to that URI.
3. Propagates output to input and repeats from step 1.

---

### Routing Slip

Route an exchange through a dynamic sequence of endpoints. Unlike Recipient List,
each step receives the output from the previous step.

```go
// Routing slip from a header
builder.From("direct:start").
    RoutingSlip("${header.routingSlip}")

// Go function
builder.From("direct:start").
    RoutingSlip(func(e *gocamel.Exchange) ([]string, error) {
        return []string{"direct:validate", "direct:transform", "direct:send"}, nil
    })
```

**Exchange Properties during Routing Slip:**

| Property | Type | Description |
|----------|------|-------------|
| `CamelRoutingSlipIndex` | int | Current step index (0-based) |
| `CamelRoutingSlipSize` | int | Total number of routing steps |
| `CamelRoutingSlipComplete` | bool | Last step indicator |

---

## Control Flow

### Stop

Stop routing without error.

```go
builder.From("direct:start").
    Choice().
        When("${header.skip} == true").
            Log("Skipping").
            Stop().
        Otherwise().
            To("direct:process").
    EndChoice()
```

**Note:** Subsequent processors won't be executed.

---

### Loop

Iterate with counter.

```go
// Not yet implemented in current version
// Use Split with index instead
```

---

## Message Headers

### SetHeader

```go
// Set header directly
builder.SetHeader("X-Request-ID", uuid.New().String())

// Set header via Simple Language
builder.SimpleSetHeader("X-Timestamp", "${date:now}")

// Set multiple headers
builder.SetHeaders(map[string]any{
    "X-Trace-ID": traceId,
    "X-Request-Id": requestId,
})

// Set via function
builder.SetHeadersFunc(func(e *gocamel.Exchange) (map[string]any, error) {
    return map[string]any{
        "X-Generated": generateId(),
    }, nil
})
```

---

### RemoveHeader

```go
// Remove specific header
builder.RemoveHeader("X-Temp-ID")

// Remove by pattern
builder.RemoveHeaders("X-Debug*")

// Remove with exclusions
builder.RemoveHeaders("X-*", "X-Keep-This")
```

---

## Exchange Properties

### SetProperty

Exchange-scoped variables (not in message).

```go
builder.SetProperty("correlationId", "abc-123")

builder.SetPropertyFunc("timestamp", func(e *gocamel.Exchange) (any, error) {
    return time.Now().UnixMilli(), nil
})
```

---

### RemoveProperty

```go
builder.RemoveProperty("temp")

builder.RemoveProperties("processing*")
```

---

## Error Handling

### Do-Try-Catch-Finally

Structured error handling within a route.

```go
builder.From("direct:start").
    DoTry().
        To("http://unstable-service").
    DoCatch("connection refused").
        Log("Service is down, applying recovery").
        To("direct:recovery").
    DoCatch(""). // Catch all other errors
        Log("Unexpected error: ${exchangeProperty.CamelExceptionCaught}").
        To("direct:error-handler").
    DoFinally().
        Log("Processing finished (success or failure)").
    End().
    To("direct:next")
```

**How it works:**
1. Executes the processors in the `DoTry` block.
2. If an error occurs, it searches for a matching `DoCatch` block (empty string matches any error).
3. If a match is found, the error is considered "handled" and routing continues after the `DoTry` block (unless the catch block itself fails).
4. The `DoFinally` block is **always** executed, regardless of whether an error occurred or was caught.

---

## EIP Pattern Summary

| Pattern | Category | Description |
|---------|----------|-------------|
| Choice | Routing | Content-based routing |
| Filter | Routing | Conditional filtering |
| Idempotent Consumer | Routing | Prevent duplicate messages |
| Circuit Breaker | Routing | Resilience pattern for failures |
| Load Balancer | Routing | Load distribution across endpoints |
| Dynamic Router | Routing | Dynamic decision-based routing |
| Throttle | Routing | Rate limiting |
| Delay | Routing | Pause before next step |
| Multicast | Routing | Multiple destinations |
| Recipient List | Routing | Dynamic recipient routing |
| Routing Slip | Routing | Dynamic sequential routing |
| Wire Tap | Routing | Async fire-and-forget copy |
| Split | Transformation | Message splitting |
| Aggregate | Transformation | Message aggregation |
| Content Enricher | Transformation | Enrich with external data |
| Transform | Transformation | Content transformation |
| ToD | Endpoint | Dynamic endpoint |
| Stop | Control | Stop routing |
| DoTry | Control | Try-Catch-Finally error handling |
| SetHeader | Headers | Header manipulation |
| SetProperty | Properties | Exchange properties |
