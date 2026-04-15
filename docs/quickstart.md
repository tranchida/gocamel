# Quick Start

## Prérequis

- Go 1.21 ou supérieur
- Git

## Installation

```bash
# Créer un nouveau projet
go mod init my-gocamel-app

# Installer GoCamel
go get github.com/tranchida/gocamel
```

## Premier exemple: Timer to HTTP

Créez un fichier `main.go`:

```go title="main.go"
package main

import (
    "fmt"
    "github.com/tranchida/gocamel"
)

func main() {
    // Créer le contexte Camel
    ctx := gocamel.NewCamelContext()
    
    // Définir une route
    route := ctx.CreateRouteBuilder().
        // Déclencheur toutes les 5 secondes
        From("timer:tick?period=5s").
        // Définir le corps du message
        SetBody("Bonjour GoCamel!").
        // Logger le message
        Log("${body}").
        // Envoyer vers un webhook HTTP
        To("http://localhost:8080/webhook").
        Build()
    
    // Ajouter la route au contexte
    ctx.AddRoute(route)
    
    // Démarrer le contexte
    ctx.Start()
    fmt.Println("🐪 GoCamel démarré - Appuyez sur Ctrl+C pour arrêter")
    
    // Bloquer indéfiniment
    select {}
}
```

## Exécution

```bash
# Lancer
go run main.go

# Résultat attendu:
# 🐪 GoCamel démarré - Appuyez sur Ctrl+C pour arrêter
# 2026/04/16 10:15:23 INFO Bonjour GoCamel!
# 2026/04/16 10:15:28 INFO Bonjour GoCamel!
# ...
```

## Exemple avec File

```go title="file_watcher.go"
package main

import (
    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()
    
    route := ctx.CreateRouteBuilder().
        // Surveille le dossier "input"
        From("file://input?noop=true").
        // Logger le nom de fichier
        Log("Fichier reçu: ${header.CamelFileName}").
        // Déplacer vers output
        To("file://output").
        Build()
    
    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

## Exemple avec OpenAI

```go title="openai_bot.go"
package main

import (
    "fmt"
    "github.com/tranchida/gocamel"
)

func main() {
    // Nécessite: export OPENAI_API_KEY=sk-...
    ctx := gocamel.NewCamelContext()
    ctx.AddComponent("openai", gocamel.NewOpenAIComponent())
    
    // Créer un exchange avec une question
    exchange := gocamel.NewExchange(ctx.GetContext())
    exchange.GetIn().SetBody("Explique GoCamel en une phrase")
    
    // Appeler OpenAI
    endpoint, _ := ctx.CreateEndpoint("openai:chat?model=gpt-3.5-turbo")
    producer, _ := endpoint.CreateProducer()
    producer.Send(exchange)
    
    fmt.Println("Réponse:", exchange.GetOut().GetBody())
}
```

## Next Steps

- 📚 [Concepts](concepts.md) — Comprendre les bases
- 🔌 [Composants](components.md) — Explorer tous les composants
- 🧩 [EIP Patterns](eip.md) — Maîtriser les patterns d'intégration
