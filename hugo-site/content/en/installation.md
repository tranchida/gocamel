+++
title = "Installation"
weight = 20
+++
## Requirements

- Go 1.27.0 or later
- Module-aware Go project (go.mod)

## Install

```bash
go get github.com/tranchida/gocamel@v0.19.1
```

Or add to `go.mod`:

```go
require github.com/tranchida/gocamel v0.19.1
```

To use optional components, install the individual modules:

```bash
go get github.com/tranchida/gocamel/components/kafka@v0.19.1
go get github.com/tranchida/gocamel/components/grpc@v0.19.1
go get github.com/tranchida/gocamel/components/redis@v0.19.1
go get github.com/tranchida/gocamel/components/nats@v0.19.1
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
