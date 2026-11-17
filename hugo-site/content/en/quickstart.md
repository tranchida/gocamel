+++
title = "Quick Start"
weight = 2
+++
## Hello World

```go
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
        SetBody("Hello World").
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

## File Processing

The `file` component lives in its own module (it depends on `fsnotify`), so it
must be registered explicitly:

```go
import "github.com/tranchida/gocamel/components/file"

ctx.AddComponent("file", file.NewFileComponent())

route, err := ctx.CreateRouteBuilder().
    From("file://input?delete=true").
    LogSimple("Processing: ${header.CamelFileName}").
    To("file://output").
    Build()
if err != nil {
    log.Fatal(err)
}
```

## HTTP Endpoint

```go
route, err := ctx.CreateRouteBuilder().
    From("http://localhost:8080/hello").
    SimpleSetBody("Hello ${header.name}!").
    Build()
if err != nil {
    log.Fatal(err)
}
```
