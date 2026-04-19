# Installation

## Requirements

- Go 1.21 or later
- Module-aware Go project (go.mod)

## Install

```bash
go get github.com/tranchida/gocamel
```

Or add to `go.mod`:

```go
require github.com/tranchida/gocamel v0.1.0
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
    "github.com/tranchida/gocamel"
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
