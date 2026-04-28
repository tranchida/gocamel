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

## Unit of Work & Transactions

GoCamel supporte un modèle transactionnel basé sur le pattern **Unit of Work**. Cela garantit que la source du message (par exemple, un fichier, un email ou un enregistrement de base de données) n'est marquée comme "consommée" qu'une fois que toute la route a été traitée avec succès.

- **Synchronization**: Vous pouvez enregistrer des callbacks (`OnComplete`, `OnFailure`) sur un `Exchange`.
- **Route Transactionnelle**: Marquez une route comme transactionnelle en utilisant `.Transacted()` dans le DSL.
- **Composants Transactionnels**: Les composants tels que `file`, `ftp`, `sftp`, `smb` et `mail` supportent ce modèle en retardant la suppression ou le déplacement du fichier source jusqu'à la fin de la route.

```go
context.CreateRouteBuilder().
    From("file:///data/in?move=.done&moveFailed=.error").
    Transacted(). // Active le comportement transactionnel
    To("http://api.service.com").
    Build()
```
