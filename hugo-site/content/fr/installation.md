+++
title = "Installation"
weight = 20
+++
## Prérequis

- Go 1.27.0 ou ultérieur
- Projet Go avec modules (go.mod)

## Installation

```bash
go get github.com/tranchida/gocamel@v0.16.0
```

Ou ajoutez à `go.mod`:

```go
require github.com/tranchida/gocamel v0.16.0
```

Pour utiliser des composants optionnels, installez les modules individuels :

```bash
go get github.com/tranchida/gocamel/components/kafka@v0.16.0
go get github.com/tranchida/gocamel/components/grpc@v0.16.0
go get github.com/tranchida/gocamel/components/redis@v0.16.0
go get github.com/tranchida/gocamel/components/nats@v0.16.0
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
