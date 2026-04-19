# Core Concepts | Concepts Fondamentaux

---

# 🇺🇸 English

## Message

The fundamental unit of exchange containing:
- **Body**: The message payload (any type)
- **Headers**: Key-value metadata (map[string]any)
- **Attachments**: Optional file attachments

```go
msg := gocamel.NewMessage()
msg.SetBody("Hello World")
msg.SetHeader("Content-Type", "text/plain")
```

## Exchange

Container for messages passing through a route:
- **In**: Input message (from consumer)
- **Out**: Output message (to producer)
- **Properties**: Exchange-scoped metadata
- **Context**: Go context for cancellation

```go
exchange := gocamel.NewExchange(context.Background())
exchange.GetIn().SetBody(input)
```

## Route

A chain of processors that handles a message:

```go
route := context.CreateRouteBuilder().
    From("direct:start").       // Source endpoint
    Process(processor).         // Custom processing
    To("direct:end").          // Destination endpoint
    Build()
```

## Endpoint

URI-addressable resource:

```
component://path?param=value
```

Examples:
- `file:///tmp/data`
- `ftp://host:21/incoming`
- `http://localhost:8080/api`
- `direct:myRoute`

## Context (CamelContext)

The runtime container managing:
- Routes lifecycle (start/stop)
- Component registry
- Endpoint resolution
- Thread pool management

```go
context := gocamel.NewCamelContext()
context.AddRoute(route)
context.Start()  // Start all routes
context.Stop()   // Stop all routes
```

## Component

Factory for endpoints of a specific type:

```go
context.AddComponent("ftp", gocamel.NewFTPComponent())
context.AddComponent("http", gocamel.NewHTTPComponent())
```

---

# 🇫🇷 Français

## Message

L'unité fondamentale d'échange contenant:
- **Body**: Le contenu du message (n'importe quel type)
- **Headers**: Métadonnées clé-valeur (map[string]any)
- **Attachments**: Pièces jointes optionnelles

```go
msg := gocamel.NewMessage()
msg.SetBody("Hello World")
msg.SetHeader("Content-Type", "text/plain")
```

## Exchange

Conteneur pour les messages traversant une route:
- **In**: Message d'entrée (du consumer)
- **Out**: Message de sortie (vers le producer)
- **Properties**: Métadonnées liées à l'exchange
- **Context**: Contexte Go pour l'annulation

```go
exchange := gocamel.NewExchange(context.Background())
exchange.GetIn().SetBody(input)
```

## Route

Une chaîne de processeurs qui traite un message:

```go
route := context.CreateRouteBuilder().
    From("direct:start").       // Endpoint source
    Process(processor).         // Traitement personnalisé
    To("direct:end").          // Endpoint destination
    Build()
```

## Endpoint

Ressource adressable par URI:

```
component://path?param=value
```

Exemples:
- `file:///tmp/data`
- `ftp://host:21/incoming`
- `http://localhost:8080/api`
- `direct:myRoute`

## Context (CamelContext)

Conteneur d'exécution gérant:
- Cycle de vie des routes (démarrage/arrêt)
- Registre des composants
- Résolution des endpoints
- Gestion du pool de threads

```go
context := gocamel.NewCamelContext()
context.AddRoute(route)
context.Start()  // Démarrer toutes les routes
context.Stop()   // Arrêter toutes les routes
```

## Component

Usine pour créer des endpoints d'un type spécifique:

```go
context.AddComponent("ftp", gocamel.NewFTPComponent())
context.AddComponent("http", gocamel.NewHTTPComponent())
```
