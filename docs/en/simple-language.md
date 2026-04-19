# Simple Language

## Overview

Simple Language is a dynamic expression language inspired by Apache Camel. It enables embedding expression placeholders in strings for dynamic routing, message transformation, and conditional processing.

## Syntax

Expressions are enclosed in `${...}`:

```go
"Hello ${body}"                 // Message body
"Priority: ${header.priority}"  // Header value
"ID: ${exchangeProperty.id}"    // Exchange property
```

## Built-in Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `${body}` | Message body | `${body}` |
| `${header.name}` | Header value | `${header.Content-Type}` |
| `${exchangeProperty.name}` | Exchange property | `${exchangeProperty.correlationId}` |

## Built-in Functions

| Function | Description | Example |
|----------|-------------|---------|
| `${date:now}` | Current timestamp | `${date:now:yyyy-MM-dd}` |
| `${random(max)}` | Random number | `${random(100)}` |
| `${uuid}` | UUID generation | `${uuid}` |
| `${env:VAR}` | Environment variable | `${env:USER}` |

## Comparison Operators

| Operator | Description | Example |
|----------|-------------|---------|
| `==` | Equal | `${body} == 'active'` |
| `!=` | Not equal | `${body} != 'inactive'` |
| `>` | Greater than | `${header.count} > 10` |
| `>=` | Greater or equal | `${header.count} >= 10` |
| `<` | Less than | `${header.count} < 100` |
| `<=` | Less or equal | `${header.count} <= 100` |
| `&&` | Logical AND | `a > 5 && b == 'x'` |
| `||` | Logical OR | `a > 5 || b == 'y'` |

## Null-safe Navigation

```go
${body?.field?.subfield}       // Safe access
```

## Bracket Notation

```go
${body['key']}                 // Map access
${body[0]}                     // Array index
${body['user']['name']}        // Nested access
```

## Usage Examples

### In Choice EIP

```go
builder.From("direct:input").
    Choice().
        When("${header.priority} == 'high'").
            To("direct:urgent").
        When("${header.count} > 100").
            To("direct:large-batch").
        Otherwise().
            To("direct:normal").
    EndChoice()
```

### In Headers and Body

```go
builder.From("direct:start").
    SimpleSetHeader("X-Request-ID", "${uuid}").
    SimpleSetBody("Processed at ${date:now} for user ${header.username}").
    To("direct:output")
```

### In Logging

```go
builder.From("direct:start").
    Log("Processing order ${header.orderId} with value ${body?.total}")
```

### In ToD (Dynamic URI)

```go
builder.From("direct:start").
    SetHeader("filename", "report.txt").
    ToD("file://output/${header.filename}")
```

## Complex Expressions

### Null-safe Chain

```go
"User name: ${body?.user?.profile?.name}"
```

### Combined Conditions

```go
builder.From("direct:start").
    Choice().
        When("${header.type} == 'A' && ${body.status} == 'active'").
            To("direct:processA").
        When("${header.priority} > 5 || ${random(10)} > 7").
            To("direct:random-priority").
    EndChoice()
```

## Date Formatting

```go
${date:now:yyyy-MM-dd HH:mm:ss}
${date:now:ISO8601}
```

## Complete Reference

### String Functions (if available)

```go
${body.toUpperCase()}
${body.substring(0, 5)}
${header.name.trim()}
```

### Math Functions

```go
${random(100)}        // 0-99
${random(1, 10)}      // 1-10 range
```
