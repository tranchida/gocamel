+++
title = "Installation"
weight = 20
+++
## Requirements

- Go 1.21 or later
- Module-aware Go project (go.mod)

## Install

```bash
go get gitlab.com/tranchida/gocamel
```

Or add to `go.mod`:

```go
require gitlab.com/tranchida/gocamel v0.1.0
```

Then:

```bash
go mod tidy
```

## Verify

```go
package main

import (
    "fmt"
    "gitlab.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()
    fmt.Println("GoCamel context created successfully!")
}
```

Run:

```bash
go run test.go
```
