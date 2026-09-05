+++
title = "Enterprise Integration Patterns"
weight = 13
+++
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
- `EndChoice()` - Ends the Choice block and adds it to the **current** container
  (the route, or a surrounding `Split` / `Loop` / `Multicast` so each part is evaluated)
- `End()` - Alternative method

**Expression Operators:**

| Operator | Description      | Example                       |
|----------|------------------|-------------------------------|
| `==`     | Equal            | `${header.type} == 'order'`   |
| `!=`     | Not equal        | `${header.status} != 'error'` |
| `>`      | Greater than     | `${header.count} > 10`        |
| `>=`     | Greater or equal | `${header.count} >= 10`       |
| `<`      | Less than        | `${header.price} < 100`       |
| `<=`     | Less or equal    | `${header.price} <= 100`      |
| `&&`     | AND              | `a > 5 && b == 'x'`           |
| `\|\|`   | OR               | `a > 5 \|\| b == 'y'`         |

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

Rate-limit exchange processing to at most N messages per time window. Exchanges that exceed the rate are **delayed**,
not dropped, until the window resets.

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
- `maxRequests` and `timePeriod` must be greater than zero; invalid values return an error instead of blocking
  indefinitely
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
- Passing `nil` to `DelayFunc` fails route construction; `NewDelayerFunc(nil)` remains constructible but returns a
  processing error instead of panicking

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

### PollEnrich

Poll on-demand from a consumer endpoint (with timeout and optional aggregation strategy).

Supported endpoints are `seda`, `chan`, and custom implementations of `PollingEndpoint`. One call receives at most one exchange and leaves the queue open with its remaining messages intact. Endpoints without this contract, or queues with an existing consumer or concurrent poll, return an error before consuming a message. A zero timeout is non-blocking; a negative timeout waits until a message arrives or the exchange context is cancelled. A positive timeout leaves the original message unchanged when no message arrives.

```go
// Poll from a queue with 2 seconds timeout and custom merge strategy
builder.From("direct:start").
    PollEnrich("seda:pending-orders", &MergeStrategy{}, 2*time.Second).
    To("direct:process")

// Non-blocking poll (timeout 0) replacing body
builder.From("direct:start").
    PollEnrichWithTimeout("chan:updates", 0).
    To("direct:process")
```

---

### Message Validator

Validate in-flight messages against a predicate, failing the exchange with `ErrValidationFailed` if the condition is not met.

```go
// Validate using Simple Language
builder.From("direct:start").
    Validate("${header.priority == 'high'}").
    To("direct:urgent")

// Validate using JSONPath
builder.From("direct:start").
    Validate(gocamel.JsonPath("$.user.active")).
    To("direct:active-users")

// Validate using custom function
builder.From("direct:start").
    ValidateFunc(func(e *gocamel.Exchange) bool {
        body, _ := e.GetBodyAsString()
        return len(body) > 0
    }).
    To("direct:output")
```

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
3. If not present, adds the key eagerly (blocking concurrent duplicates) and proceeds.
4. If present, stops routing (`ErrStopRouting`).
5. If the exchange later **fails** downstream, the key is removed from the repository again
   (via an exchange `Synchronization`), so a redelivery of the same message is processed
   instead of being dropped — at-least-once semantics.

**Repositories:**

- `NewMemoryIdempotentRepository()`: In-memory store (default).
- `NewRedisIdempotentRepository()`: Distributed store backed by Redis.
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
- **Half-Open**: After `OpenTimeout`, a **single trial request at a time** probes the downstream
  service; concurrent requests are diverted to the fallback as if the circuit were still open.
  Once `SuccessThreshold` successful probes complete, the circuit closes. If a probe fails, it
  opens again.

`ErrStopRouting` (Filter, incomplete aggregation, Stop EIP) is **not** a failure:
it does not increment the failure count and does not open the circuit.

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

### SEDA

Staged Event-Driven Architecture. Decouples producers and consumers using an internal queue (Go channel).

```go
// Consumer with concurrent workers
builder.From("seda:process?concurrentConsumers=5&size=1000").
    Log("Asynchronously processing: ${body}").
    To("direct:next")

// Producer
builder.To("seda:process")
```

**How it works:**

1. The producer sends a **copy** of the exchange to an internal queue.
2. The producer returns immediately (non-blocking if queue is not full).
3. One or more consumer workers read from the queue and process the exchange.

**Parameters:**

- `size`: The capacity of the internal queue (default 1000).
- `concurrentConsumers`: The number of worker goroutines (default 1).

Invalid values (negative `size`, non-positive `concurrentConsumers`) fall back to the
defaults with a warning. Parameters are applied when the endpoint is **first created**;
later references to the same queue name reuse the existing endpoint.

---

### Resequencer

Reorder messages that arrived out of order based on a sequence number or timestamp.

```go
builder.From("direct:start").
    Resequence("${header.seq}", 1). // Starts with sequence 1
        Timeout(500 * time.Millisecond).
        Log("Processing in order: ${header.seq}").
        To("direct:next").
    End()
```

**How it works:**

1. Evaluates the expression to get the sequence number.
2. If the sequence is the next expected one, it's processed immediately.
3. If it's a future sequence, it's buffered and sorted.
4. If a gap persists longer than the `Timeout`, the resequencer skips the missing sequence and continues with the next
   available one in the buffer.

**Notes:**

- Messages are delivered downstream through the resequencer's **internal pipeline** (the steps
  between `Resequence(...)` and `End()`). The original exchange stops there (`ErrStopRouting`),
  so steps placed *after* `End()` never see out-of-order messages.
- Delivery is serialized to guarantee ordering; downstream steps must not route back into the
  same resequencer.
- Duplicate or outdated sequence numbers are dropped (logged as warnings).

---

### Claim Check

Reduce the size of messages by storing the payload in an external repository and passing only a reference (key).

```go
repo := gocamel.NewMemoryClaimCheckRepository()

builder.From("direct:start").
    // Store body and replace with key
    ClaimCheck(gocamel.ClaimCheckPush, "my-ticket", repo).
    To("direct:other-system"). // Only the key "my-ticket" is sent
    // Restore original body
    ClaimCheck(gocamel.ClaimCheckPop, "my-ticket", repo).
    To("direct:next")
```

**Operations:**

- `ClaimCheckPush`: Stores the body and replaces it with a key. If no key is provided, one is generated.
- `ClaimCheckPop`: Retrieves the data by key, restores the body, and removes it from the repository.
- `ClaimCheckGet`: Retrieves the data by key and restores the body (keeps it in the repository).
- `ClaimCheckDiscard`: Removes the data from the repository without restoring it.

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

| Property             | Type | Description             |
|----------------------|------|-------------------------|
| `CamelSplitIndex`    | int  | Current index (0-based) |
| `CamelSplitSize`     | int  | Total number of parts   |
| `CamelSplitComplete` | bool | Last part indicator     |

> [!note] These properties are branch-scoped
> They describe one part, not the whole message, and are **removed when the
> aggregated result is published back onto the parent exchange**. A processor
> placed after the Split does not see `CamelSplitComplete=true` left over
> from the last part, and a nested Split does not overwrite the bookkeeping
> of the enclosing one. The same rule applies to `CamelMulticast*`,
> `CamelRecipientList*` and `CamelRoutingSlip*`.


> [!warning] A `[]byte` body is not a collection
> Splitting a `[]byte` body yields a **single part**, matching Apache Camel,
> which does not split `byte[]`. Treating it as a list produced one exchange
> per byte — a 10 KB payload became 10 000 exchanges. To divide binary
> content deliberately, split on a decoded value instead:
>
> ```go
> Split(func(e *gocamel.Exchange) (any, error) {
>     return strings.Split(string(e.GetBody().([]byte)), "\n"), nil
> })
> ```


---

### Aggregator

Combine multiple messages into one.

At least one completion condition is required: `SetCompletionSize`,
`SetCompletionTimeout`, or `SetCompletionPredicate`. Without one, `Process`
returns an error instead of buffering forever.

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

| Method                       | Description                          |
|------------------------------|--------------------------------------|
| `SetCompletionSize(n)`       | Complete after n messages            |
| `SetCompletionTimeout(ms)`   | Complete after timeout               |
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

### Transform

A dedicated message transformation EIP that modifies the message body dynamically using a custom Go function or a Simple language expression.

```go
// Transform using a custom Go function
builder.From("direct:start").
    Transform(func(e *gocamel.Exchange) (any, error) {
        input, _ := e.GetBodyAsString()
        return "Transformed: " + input, nil
    }).
    To("direct:output")

// Transform using a Simple Language expression
builder.From("direct:start").
    TransformSimple("Modified: ${body} and type is ${header.Type}").
    To("direct:output")
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

Route an exchange through a sequence of endpoints resolved dynamically by calling a control function after each step.
Routing ends when the function returns an empty string or `nil`.

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

| Property                   | Type | Description                   |
|----------------------------|------|-------------------------------|
| `CamelRoutingSlipIndex`    | int  | Current step index (0-based)  |
| `CamelRoutingSlipSize`     | int  | Total number of routing steps |
| `CamelRoutingSlipComplete` | bool | Last step indicator           |

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

Iterate a route segment a static or dynamic number of times.

```go
// Static count
builder.From("direct:start").
    Loop(3).
        ProcessFunc(func(e *gocamel.Exchange) error {
            // Evaluated 3 times
            idx, _ := e.GetPropertyAsInt(gocamel.CamelLoopIndex)
            size, _ := e.GetPropertyAsInt(gocamel.CamelLoopSize)
            fmt.Printf("Processing loop index %d of %d\n", idx, size)
            return nil
        }).
    End().
    To("direct:output")

// Dynamic count from a Simple Language expression
builder.From("direct:start").
    LoopSimple("${header.LoopCount}").
        To("direct:process-item").
    End()

// Dynamic count from a Go function
builder.From("direct:start").
    LoopFunc(func(e *gocamel.Exchange) (int, error) {
        val, _ := e.GetHeaderAsInt("X-Max-Retries")
        return val, nil
    }).
        To("direct:retry-attempt").
    End()
```

**Exchange Properties during Loop:**

| Property | Type | Description |
|---|---|---|
| `CamelLoopIndex` | int | Current iteration index (0-based) |
| `CamelLoopSize` | int | Total number of loop iterations |
| `CamelLoopComplete` | bool | Boolean indicating if this is the last iteration |

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
3. If a match is found, the error is considered "handled" and routing continues after the `DoTry` block (unless the
   catch block itself fails).
4. The `DoFinally` block is **always** executed, regardless of whether an error occurred or was caught.

---

## Transactions & Sagas

### Saga

The Saga EIP provides a mechanism to coordinate a series of local transactions across distributed services without locking databases. It defines a set of actions and their corresponding compensating actions (rollbacks).

If any action in the Saga block fails or returns an error, the Saga orchestrator automatically triggers the compensating steps for all completed actions in reverse order (LIFO - Last In, First Out). Action panics are recovered and treated as failures (compensations still run). `ErrStopRouting` from an action stops the saga **without** compensation — it is a control signal, not a failure.

```go
builder.From("direct:saga-route").
    Saga().
        // Action 1: Reserve Hotel. Compensation 1: Cancel Hotel.
        Action("direct:reserve-hotel", "direct:cancel-hotel").
        // Action 2: Reserve Flight. Compensation 2: Cancel Flight.
        Action("direct:reserve-flight", "direct:cancel-flight").
    End().
    To("direct:saga-completed")
```

You can also use custom Go functions for actions and compensations directly using `ActionFunc`:

```go
builder.From("direct:saga-route").
    Saga().
        ActionFunc(
            func(e *gocamel.Exchange) error {
                // Reserve booking
                return nil
            },
            func(e *gocamel.Exchange) error {
                // Cancel/compensate booking
                return nil
            },
        ).
    End()
```

---

## Message Tracking & Diagnostics

### Message History

The Message History EIP enables tracing the chronological journey of a message as it flows through the various route nodes and processors.

To enable tracking for a route, invoke `.EnableMessageHistory()` on your route builder. You can then retrieve and inspect the history using `gocamel.GetMessageHistory(exchange)`.

```go
// 1. Enable tracking in the route definition
builder.From("direct:start").
    EnableMessageHistory().
    SetBody("Hello GoCamel").
    TransformSimple("Modified: ${body}").
    To("direct:output")

// 2. Retrieve history inside a downstream processor
builder.From("direct:output").
    ProcessFunc(func(e *gocamel.Exchange) error {
        history := gocamel.GetMessageHistory(e)
        for i, entry := range history {
            fmt.Printf("[%d] Route: %s | Processor: %T | Time: %s\n", 
                i, entry.RouteID, entry.Processor, entry.Timestamp.Format(time.RFC3339))
        }
        return nil
    })
```

---

## EIP Pattern Summary

| Pattern             | Category       | Description                        |
|---------------------|----------------|------------------------------------|
| Choice              | Routing        | Content-based routing              |
| Filter              | Routing        | Conditional filtering              |
| Idempotent Consumer | Routing        | Prevent duplicate messages         |
| Circuit Breaker     | Routing        | Resilience pattern for failures    |
| Load Balancer       | Routing        | Load distribution across endpoints |
| Dynamic Router      | Routing        | Dynamic decision-based routing     |
| Resequencer         | Routing        | Reorder messages by sequence       |
| Claim Check         | Routing        | Store payload and pass reference   |
| Throttle            | Routing        | Rate limiting                      |
| Delay               | Routing        | Pause before next step             |
| Multicast           | Routing        | Multiple destinations              |
| Recipient List      | Routing        | Dynamic recipient routing          |
| Routing Slip        | Routing        | Dynamic sequential routing         |
| Wire Tap            | Routing        | Async fire-and-forget copy         |
| Split               | Transformation | Message splitting                  |
| Aggregate           | Transformation | Message aggregation                |
| Content Enricher    | Transformation | Enrich with external data          |
| Transform           | Transformation | Content transformation             |
| ToD                 | Endpoint       | Dynamic endpoint                   |
| Stop                | Control        | Stop routing                       |
| DoTry               | Control        | Try-Catch-Finally error handling   |
| SetHeader           | Headers        | Header manipulation                |
| SetProperty         | Properties     | Exchange properties                |
| Loop                | Control        | Iterate with static/dynamic counter|
| Saga                | Control        | Saga transactional pattern         |
| Message History     | Diagnostics    | Track message processing trace     |

## Branch completion and aggregation

Split, Multicast and RecipientList complete each processed branch on success or failure, including idempotency rollback callbacks. Parallel branches copy supported mutable bodies, including each split part, before workers start. Custom mutable types still require application-defined copying or immutable use. Workers finish before aggregation begins; the strategy is called serially in input order, and strategy panics become `ErrProcessorPanic` errors. Sequential processing stops at the first failing branch.

Invalid `Resequence` expressions accumulate a build error while leaving the DSL chain usable; `Timeout`, `Capacity` and `End` do not panic because an earlier expression was invalid.
