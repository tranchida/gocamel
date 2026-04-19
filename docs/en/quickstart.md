# Quick Start

## Hello World

```go
package main

import (
    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()
    
    route := ctx.CreateRouteBuilder().
        From("timer:tick?period=5s").
        SetBody("Hello World").
        Log("${body}").
        Build()
    
    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

## File Processing

```go
route := ctx.CreateRouteBuilder().
    From("file://input?delete=true").
    Log("Processing: ${body}").
    To("file://output").
    Build()
```

## HTTP Endpoint

```go
route := ctx.CreateRouteBuilder().
    From("http://localhost:8080/hello").
    SetBody("Hello ${header.name}!").
    Build()
```
