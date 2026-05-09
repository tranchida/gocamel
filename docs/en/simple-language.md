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
| `${date:now:format}` | Current timestamp | `${date:now:2006-01-02}` |
| `${date:command+offset:format}` | Date with arithmetic | `${date:now+24h:2006-01-02}` |
| `${date:header.name:format}` | Date from header | `${date:header.birthDate:2006}` |
| `${date:property.name:format}` | Date from property | `${date:property.expiry:RFC3339}` |
| `${date:file:format}` | Last modified date of file | `${date:file:2006-01-02}` |
| `${random(max)}` | Random number | `${random(100)}` |
| `${uuid}` | UUID generation | `${uuid}` |
| `${env:VAR}` | Environment variable | `${env:USER}` |

**Arithmetic Offsets:**
- `s`: Seconds (e.g., `+30s`)
- `m`: Minutes (e.g., `-5m`)
- `h`: Hours (e.g., `+12h`)
- `d`: Days (e.g., `-7d`)

---


## File Language

The File Language is an extension of the Simple Language dedicated to file-related operations. It provides functions to extract parts of the filename, path, size, etc.

| Function | Description | Example |
|----------|-------------|---------|
| `${file:name}` | The name of the file (including path) | `data/reports/monthly.json` |
| `${file:name.noext}` | The name of the file but without the extension | `data/reports/monthly` |
| `${file:name.ext}` | The file extension | `json` |
| `${file:ext}` | Alias for `${file:name.ext}` | `json` |
| `${file:onlyname}` | The name of the file (without path) | `monthly.json` |
| `${file:onlyname.noext}` | The name of the file (without path) and without extension | `monthly` |
| `${file:path}` | The full path of the file | `/abs/path/data/reports/monthly.json` |
| `${file:size}` | The length (size) of the file | `1234` |
| `${file:modified}` | The last modified date of the file (RFC3339 format) | `2023-05-10T10:00:00Z` |

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

Mathematical operations using the `math(...)` function. Supports basic operators (+, -, *, /, %) and parentheses.

| Function | Description | Example |
|----------|-------------|---------|
| `${math(body * 2)}` | Multiply body by 2 | `20` |
| `${math(header.val + 10)}` | Add 10 to header | `15` |
| `${math((body + 2) * 3)}` | Complex expression | `36` |

### Numeric Functions

Numeric utilities for rounding and transformations. Can be used standalone or inside `math()`.

| Function | Description | Example |
|----------|-------------|---------|
| `${round(val)}` | Round to nearest integer | `${round(5.6)}` -> `6` |
| `${floor(val)}` | Round down | `${floor(5.6)}` -> `5` |
| `${ceil(val)}` | Round up | `${ceil(5.1)}` -> `6` |
| `${abs(val)}` | Absolute value | `${abs(-10)}` -> `10` |
| `${sqrt(val)}` | Square root | `${sqrt(16)}` -> `4` |
| `${sin(val)}`, `${cos(val)}`, `${tan(val)}` | Trigonometric functions | `${sin(0)}` -> `0` |

---
