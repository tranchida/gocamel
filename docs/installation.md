# Installation Guide | Guide d'Installation

---

# 🇺🇸 English

## Requirements

- Go 1.21 or later
- Module-aware Go project (go.mod)

## Installation

### Using go get

```bash
go get github.com/tranchida/gocamel
```

### Using go.mod

Add to your `go.mod` file:

```go
require github.com/tranchida/gocamel v0.1.0
```

Then run:

```bash
go mod tidy
```

## Verification

Create a simple test file:

```go
// test.go
package main

import (
    "fmt"
    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()
    fmt.Println("GoCamel context created successfully!")
    _ = ctx
}
```

Run it:

```bash
go run test.go
```

## Dependencies

GoCamel uses minimal external dependencies:
- Standard library for core functionality
- github.com/go-co-op/gocron/v2 for cron scheduling
- github.com/mattn/go-sqlite3 for SQLite support (optional)

## Next Steps

- Read the [Quick Start Guide](quickstart.md)
- Explore [Concepts](concepts.md)
- Check [Examples](examples.md)

---

# 🇫🇷 Français

## Prérequis

- Go 1.21 ou ultérieur
- Projet Go avec modules (go.mod)

## Installation

### Utilisation de go get

```bash
go get github.com/tranchida/gocamel
```

### Utilisation de go.mod

Ajoutez à votre fichier `go.mod`:

```go
require github.com/tranchida/gocamel v0.1.0
```

Puis exécutez:

```bash
go mod tidy
```

## Vérification

Créez un fichier de test simple:

```go
// test.go
package main

import (
    "fmt"
    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()
    fmt.Println("Contexte GoCamel créé avec succès!")
    _ = ctx
}
```

Exécutez-le:

```bash
go run test.go
```

## Dépendances

GoCamel utilise un minimum de dépendances externes:
- Bibliothèque standard pour la fonctionnalité de base
- github.com/go-co-op/gocron/v2 pour la planification cron
- github.com/mattn/go-sqlite3 pour le support SQLite (optionnel)

## Prochaines Étapes

- Lisez le [Guide de Démarrage Rapide](quickstart.md)
- Explorez les [Concepts](concepts.md)
- Consultez les [Exemples](examples.md)
