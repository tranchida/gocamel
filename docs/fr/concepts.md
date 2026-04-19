# Concepts Fondamentaux

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
    From("direct:start").
    Process(processor).
    To("direct:end").
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

## Context (CamelContext)

Conteneur d'exécution gérant:
- Cycle de vie des routes (démarrage/arrêt)
- Registre des composants
- Résolution des endpoints
- Gestion du pool de threads

```go
context := gocamel.NewCamelContext()
context.AddRoute(route)
context.Start()
context.Stop()
```

## Component

Usine pour créer des endpoints d'un type spécifique:

```go
context.AddComponent("ftp", gocamel.NewFTPComponent())
context.AddComponent("http", gocamel.NewHTTPComponent())
```
