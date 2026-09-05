+++
title = "Installation"
weight = 20
+++
## Prérequis

- Go 1.27.0 ou ultérieur
- Projet Go avec modules (go.mod)

## Installation

```bash
go get gitlab.com/tranchida/gocamel@v0.12.2
```

Ou ajoutez à `go.mod`:

```go
require gitlab.com/tranchida/gocamel v0.12.2
```

Pour utiliser des composants optionnels, installez les modules individuels :

```bash
go get gitlab.com/tranchida/gocamel/components/kafka@v0.12.2
go get gitlab.com/tranchida/gocamel/components/grpc@v0.12.2
go get gitlab.com/tranchida/gocamel/components/redis@v0.12.2
go get gitlab.com/tranchida/gocamel/components/nats@v0.12.2
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
    "gitlab.com/tranchida/gocamel"
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
