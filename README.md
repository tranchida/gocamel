# GoCamel

GoCamel est une bibliothèque d'intégration d'entreprise inspirée d'Apache Camel, écrite en Go. Elle permet de créer des routes d'intégration pour connecter différents systèmes et services.

## Installation

```bash
go get github.com/tranchida/gocamel
```

## Fonctionnalités

- Architecture basée sur les routes et les endpoints
- Support des composants HTTP et File
- Gestion des messages avec corps et en-têtes
- Contexte Camel pour la gestion du cycle de vie
- Pattern Builder pour la création de routes

## Exemples d'utilisation

### Exemple HTTP

```go
package main

import (
    "fmt"
    "time"
    "github.com/tranchida/gocamel"
)

func main() {
    context := gocamel.NewCamelContext()
    
    // Enregistrement du composant HTTP
    context.AddComponent("http", gocamel.NewHTTPComponent())

    // Création d'une route qui écoute sur le port 8080
    route := context.CreateRouteBuilder().
        From("http://localhost:8080/echo").
        ProcessFunc(func(exchange *gocamel.Exchange) error {
            exchange.GetOut().SetBody("Hello, World!")
            exchange.GetOut().SetHeader("Content-Type", "text/plain")
            exchange.GetOut().SetHeader("Status-Code", "200")
            exchange.GetOut().SetHeader("X-Processed-At", time.Now().Format(time.RFC3339))
            return nil
        }).
        Build()

    context.AddRoute(route)
    context.Start()

    fmt.Println("Serveur démarré sur http://localhost:8080/echo")
    select {}
}
```

### Exemple File

```go
package main

import (
    "fmt"
    "os"
    "path/filepath"
    "time"
    "github.com/tranchida/gocamel"
)

func main() {
    // Création d'un répertoire temporaire
    tempDir, _ := os.MkdirTemp("", "gocamel-test-*")
    defer os.RemoveAll(tempDir)

    context := gocamel.NewCamelContext()
    context.AddComponent("file", gocamel.NewFileComponent())

    // Création d'une route qui surveille le répertoire
    route := context.CreateRouteBuilder().
        From("file://" + tempDir).
        ProcessFunc(func(exchange *gocamel.Exchange) error {
            if fileName, ok := exchange.GetIn().GetHeader("CamelFileName"); ok {
                fmt.Printf("Nouveau fichier: %s\n", fileName)
            }
            return nil
        }).
        Build()

    context.AddRoute(route)
    context.Start()

    // Création de fichiers de test
    for i := 1; i <= 3; i++ {
        content := fmt.Sprintf("Contenu du fichier test %d", i)
        filename := filepath.Join(tempDir, fmt.Sprintf("test%d.txt", i))
        os.WriteFile(filename, []byte(content), 0644)
        time.Sleep(time.Second)
    }
}
```

## Structure du projet

```
gocamel/
├── context.go         # Gestion du contexte Camel
├── exchange.go        # Structure d'échange de messages
├── message.go         # Structure de message
├── route.go          # Gestion des routes
├── route_builder.go  # Pattern Builder pour les routes
├── registry.go       # Registre des composants
├── http_component.go # Composant HTTP
└── file_component.go # Composant File
```

## Composants disponibles

### HTTP Component

Le composant HTTP permet de créer des endpoints HTTP pour envoyer et recevoir des requêtes.

```go
context.AddComponent("http", gocamel.NewHTTPComponent())
```

### File Component

Le composant File permet de lire, écrire et surveiller des fichiers.

```go
context.AddComponent("file", gocamel.NewFileComponent())
```

## Licence

MIT 