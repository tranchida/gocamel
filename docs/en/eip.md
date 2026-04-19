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

Filter messages based on a condition.

```go
builder.From("direct:start").
    Filter("${header.active} == true").
        To("direct:process").
    End()
```

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

```go
// Not yet implemented in current version
// Use error handling in processor
```

---

## EIP Pattern Summary

| Pattern | Category | Description |
|---------|----------|-------------|
| Choice | Routing | Content-based routing |
| Filter | Routing | Conditional filtering |
| Multicast | Routing | Multiple destinations |
| Split | Transformation | Message splitting |
| Aggregate | Transformation | Message aggregation |
| Transform | Transformation | Content transformation |
| ToD | Endpoint | Dynamic endpoint |
| Stop | Control | Stop routing |
| SetHeader | Headers | Header manipulation |
| SetProperty | Properties | Exchange properties |

