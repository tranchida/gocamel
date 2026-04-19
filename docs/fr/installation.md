# Installation

## Prérequis

- Go 1.21 ou ultérieur
- Projet Go avec modules (go.mod)

## Installation

```bash
go get github.com/tranchida/gocamel
```

Ou ajoutez à `go.mod`:

```go
require github.com/tranchida/gocamel v0.1.0
```

Puis:

```bash
go mod tidy
```

## Vérification

```go
package main

import (
    "fmt"
    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()
    fmt.Println("Contexte GoCamel créé avec succès!")
}
```

Exécutez:

```bash
go run test.go
```
