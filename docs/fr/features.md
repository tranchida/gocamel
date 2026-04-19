# Features Overview | Vue d'Ensemble des Fonctionnalités

---

# 🇺🇸 English

## Core Features

### Enterprise Integration Patterns (EIP)

GoCamel implements proven integration patterns from the *Enterprise Integration Patterns* book by Gregor Hohpe and Bobby Woolf:

| Pattern | Description |
|---------|-------------|
| **Choice** | Content-based routing with conditions |
| **Split** | Divide message into parts |
| **Aggregate** | Combine multiple messages |
| **Multicast** | Send to multiple destinations |
| **Stop** | Stop routing without error |
| **ToD** | Dynamic endpoint URI resolution |

### Security Features

GoCamel includes built-in security utilities to protect against common vulnerabilities:

- **Path Validation** — Protection against directory traversal attacks
- **SQL Injection Prevention** — Input validation for database queries
- **Input Sanitization** — Null byte and control character removal
- **Safe Path Operations** — Bounds-checking for file operations

### Type-Safe Message Exchange

```go
exchange := gocamel.NewExchange(context.Background())
exchange.GetIn().SetBody("Hello")
exchange.GetIn().SetHeader("X-ID", "123")
```

### DSL (Domain Specific Language)

Fluent builder pattern for route construction:

```go
builder.From("direct:start").
    SetHeader("X-Trace", "abc").
    Log("Processing: ${body}").
    To("direct:end")
```

### Simple Language

Expression language for dynamic routing:

```go
builder.From("direct:input").
    Choice().
        When("${header.priority == 'high'}").
            To("direct:urgent").
        Otherwise().
            To("direct:normal").
    EndChoice()
```

### Component Architecture

- **Producer**: Sends messages to endpoints
- **Consumer**: Receives messages from endpoints
- **Endpoint**: URI-based resource location
- **Component**: Factory for endpoints

### Management API

REST API for monitoring and control:

```go
mgmt := gocamel.NewManagementServer(context)
mgmt.Start(":8081")
```

---

# 🇫🇷 Français

## Fonctionnalités Principales

### Enterprise Integration Patterns (EIP)

GoCamel implémente les patterns d'intégration éprouvés du livre *Enterprise Integration Patterns* de Gregor Hohpe et Bobby Woolf:

| Pattern | Description |
|---------|-------------|
| **Choice** | Routage basé sur le contenu avec conditions |
| **Split** | Division du message en parties |
| **Aggregate** | Combinaison de plusieurs messages |
| **Multicast** | Envoi vers plusieurs destinations |
| **Stop** | Arrêt du routage sans erreur |
| **ToD** | Résolution dynamique d'URI d'endpoint |

### Fonctionnalités de Sécurité

GoCamel inclut des utilitaires de sécurité intégrés pour se protéger contre les vulnérabilités courantes :

- **Validation des Chemins** — Protection contre les attaques de traversée de répertoire
- **Prévention des Injections SQL** — Validation des entrées pour les requêtes base de données
- **Assainissement des Entrées** — Suppression des octets nuls et caractères de contrôle
- **Opérations de Chemins Sécurisées** — Vérification des limites pour les opérations fichiers

### Échange de Messages Type-Safe

```go
exchange := gocamel.NewExchange(context.Background())
exchange.GetIn().SetBody("Bonjour")
exchange.GetIn().SetHeader("X-ID", "123")
```

### DSL (Domain Specific Language)

Pattern Builder fluide pour la construction de routes:

```go
builder.From("direct:start").
    SetHeader("X-Trace", "abc").
    Log("Traitement: ${body}").
    To("direct:end")
```

### Simple Language

Langage d'expressions pour le routage dynamique:

```go
builder.From("direct:input").
    Choice().
        When("${header.priority == 'high'}").
            To("direct:urgent").
        Otherwise().
            To("direct:normal").
    EndChoice()
```

### Architecture des Composants

- **Producer**: Envoie des messages vers les endpoints
- **Consumer**: Reçoit des messages depuis les endpoints
- **Endpoint**: Localisation de ressource basée sur URI
- **Component**: Usine pour créer des endpoints

### API de Management

API REST pour monitoring et contrôle:

```go
mgmt := gocamel.NewManagementServer(context)
mgmt.Start(":8081")
```
