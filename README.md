# GoCamel

<p align="center">
  <strong>Enterprise Integration Framework for Go</strong> | <strong>Framework d'Intégration d'Entreprise pour Go</strong>
</p>

<p align="center">
  🇺🇸 English | 🇫🇷 Français
</p>

---

## 🌐 Table of Contents | Table des Matières

- [English Documentation](#english-documentation)
  
- [Documentation Française](#documentation-française)

---

# 🇺🇸 ENGLISH DOCUMENTATION

## Introduction

GoCamel is an enterprise integration library inspired by Apache Camel, written in Go. It provides a powerful Domain Specific Language (DSL) for creating integration routes that connect different systems and services.

## Installation

```bash
go get github.com/tranchida/gocamel
```

## Features

- **Route & Endpoint Architecture**: Build integration flows using a fluent DSL
- **Message Management**: Body and headers handling with type safety
- **Camel Context**: Lifecycle management for routes and components
- **Builder Pattern**: Intuitive route construction
- **Enterprise Integration Patterns (EIP)**: Split, Aggregate, Multicast, Choice, Stop, ToD, and more
- **Simple Language**: Dynamic expression evaluation (${body}, ${header.name}, functions, comparisons)
- **Built-in Logging**: Integrated logging functions
- **Centralized Configuration**: Environment-based credentials management
- **REST Management**: JMX-like monitoring and control API

## Quick Start

```go
package main

import (
    "github.com/tranchida/gocamel"
)

func main() {
    // Create Camel context
    context := gocamel.NewCamelContext()
    
    // Define a route
    route := context.CreateRouteBuilder().
        From("timer:tick?period=5000").
        SetBody("Hello World").
        Log("${body}").
        To("direct:output").
        Build()
    
    context.AddRoute(route)
    context.Start()
    select {}
}
```

## Simple Language

GoCamel includes a dynamic expression language inspired by Apache Camel Simple Language.

### Core Capabilities

- **Data Access**: `${body}`, `${header.name}`, `${exchangeProperty.prop}`
- **Built-in Functions**: `${date:now}`, `${random(100)}`, `${uuid}`
- **Comparisons**: `${header.count > 10}`, `${body == 'active'}`
- **Null-safe Access**: `${body?.field?.subfield}`
- **Bracket Notation**: `${body['key']}`, `${body[0]}`

### Examples

```go
// Dynamic body and headers
builder.From("direct:start").
    SimpleSetBody("Hello ${body} at ${date:now}").
    SimpleSetHeader("X-Request-ID", "${uuid}").
    To("direct:output")

// Content-based routing with Choice
builder.From("direct:start").
    Choice().
        When("${header.priority == 'high'}").
            SimpleSetBody("🚨 HIGH: ${body}").
            To("direct:urgent").
        When("${body['count'] > 100}").
            To("direct:large-batch").
        Otherwise().
            To("direct:normal").
    EndChoice()
```

## Enterprise Integration Patterns (EIP)

### Split EIP

Divide a message into multiple parts and process them individually.

```go
builder.From("direct:start").
    Split(func(e *gocamel.Exchange) (any, error) {
        body := e.GetIn().GetBody().(string)
        return strings.Split(body, ","), nil
    }).
    Log("Processing part: ${body}").
    To("direct:process-part").
    End()
```

### Aggregate EIP

Combine multiple messages into one based on a correlation key.

```go
strategy := &MyAggregationStrategy{}
repo := gocamel.NewMemoryAggregationRepository()

builder.From("direct:start").
    Aggregate(gocamel.NewAggregator(correlationExpr, strategy, repo).
        SetCompletionSize(3)).
    Log("Aggregated message: ${body}")
```

### Multicast EIP

Send a copy of the message to multiple destinations concurrently.

```go
builder.From("direct:start").
    Multicast().
        Pipeline().
            Log("Branch 1: ${body}").
            To("direct:out1").
        End().
        Pipeline().
            Log("Branch 2: ${body}").
            To("direct:out2").
        End().
    End()
```

### Choice EIP

Content-based routing with conditional branches.

```go
builder.From("direct:decision").
    Choice().
        When("${header.type == 'A'}").
            To("direct:typeA").
        When("${header.type == 'B'}").
            To("direct:typeB").
        Otherwise().
            To("direct:default").
    EndChoice()
```

## Available Components

| Component | URI Pattern | Description |
|-----------|-------------|-------------|
| **HTTP** | `http://host:port/path` | HTTP server (Consumer) and client (Producer) |
| **File** | `file://path` | File system operations |
| **FTP** | `ftp://host/path` | FTP client support |
| **SFTP** | `sftp://host/path` | Secure FTP with SSH authentication |
| **SMB** | `smb://host/share` | Windows/Samba share support |
| **Direct** | `direct:name` | In-memory synchronous routing |
| **Timer** | `timer:name` | Periodic timer-based triggers |
| **Cron** | `cron:name` | Cron-based scheduled triggers |
| **Telegram** | `telegram:bots` | Telegram Bot API integration |
| **OpenAI** | `openai:chat` | OpenAI Chat Completion API |
| **Exec** | `exec:command` | System command execution |
| **Mail** | `smtp://...`, `imap://...` | Email sending and receiving |
| **SQL** | `sql://datasource` | SQL query execution |
| **SQL-Stored** | `sql-stored://ds` | Stored procedure calls |
| **XSLT** | `xslt:template` | XML transformation |
| **XSD** | `xsd:schema` | XML schema validation |
| **Template** | `template:name` | Go template processing |

## Configuration

Sensitive parameters (tokens, passwords) can be provided in three ways:

1. **Directly in URI**: `ftp://user:pass@host/path`
2. **Query parameters**: `telegram:bots?authorizationToken=XXX`
3. **Environment variables** (Recommended): `FTP_PASSWORD=***`, `TELEGRAM_AUTHORIZATIONTOKEN=***`

## Example: FTP Integration

```go
package main

import "github.com/tranchida/gocamel"

func main() {
    context := gocamel.NewCamelContext()
    context.AddComponent("ftp", gocamel.NewFTPComponent())

    route := context.CreateRouteBuilder().
        From("ftp://localhost:21/incoming?delay=10s&delete=true").
        Log("New file downloaded").
        SetHeader(gocamel.CamelFileName, "processed.txt").
        To("ftp://localhost:21/processed").
        Build()

    context.AddRoute(route)
    context.Start()
    select {}
}
```

## REST Management API

Enable JMX-like monitoring and control:

```go
mgmt := gocamel.NewManagementServer(context)
mgmt.Start(":8081")  // Access http://localhost:8081/routes
```

**Available endpoints:**
- `GET /routes` - List all routes
- `GET /routes/{id}` - Route details
- `POST /routes/{id}/start` - Start a route
- `POST /routes/{id}/stop` - Stop a route
- `GET /health` - Health check

---

# 🇫🇷 DOCUMENTATION FRANÇAISE

## Introduction

GoCamel est une bibliothèque d'intégration d'entreprise inspirée d'Apache Camel, écrite en Go. Elle fournit un puissant Langage Spécifique au Domaine (DSL) pour créer des routes d'intégration connectant différents systèmes et services.

## Installation

```bash
go get github.com/tranchida/gocamel
```

## Fonctionnalités

- **Architecture Route & Endpoint**: Construction de flux d'intégration avec un DSL fluide
- **Gestion des Messages**: Manipulation du corps et des en-têtes avec sécurité de type
- **Contexte Camel**: Gestion du cycle de vie des routes et composants
- **Pattern Builder**: Construction intuitive des routes
- **Enterprise Integration Patterns (EIP)**: Split, Aggregate, Multicast, Choice, Stop, ToD, et plus
- **Simple Language**: Évaluation d'expressions dynamiques (${body}, ${header.name}, fonctions, comparaisons)
- **Logging Intégré**: Fonctions de logging intégrées
- **Configuration Centralisée**: Gestion des identifiants via variables d'environnement
- **API REST de Management**: Monitoring et contrôle inspiré de JMX

## Démarrage Rapide

```go
package main

import (
    "github.com/tranchida/gocamel"
)

func main() {
    // Créer le contexte Camel
    context := gocamel.NewCamelContext()
    
    // Définir une route
    route := context.CreateRouteBuilder().
        From("timer:tick?period=5000").
        SetBody("Hello World").
        Log("${body}").
        To("direct:output").
        Build()
    
    context.AddRoute(route)
    context.Start()
    select {}
}
```

## Simple Language

GoCamel inclut un langage d'expressions dynamiques inspiré d'Apache Camel Simple Language.

### Capacités Principales

- **Accès aux Données**: `${body}`, `${header.name}`, `${exchangeProperty.prop}`
- **Fonctions Intégrées**: `${date:now}`, `${random(100)}`, `${uuid}`
- **Comparaisons**: `${header.count > 10}`, `${body == 'active'}`
- **Accès Null-safe**: `${body?.field?.subfield}`
- **Notation par Crochets**: `${body['key']}`, `${body[0]}`

### Exemples

```go
// Corps et en-têtes dynamiques
builder.From("direct:start").
    SimpleSetBody("Bonjour ${body} à ${date:now}").
    SimpleSetHeader("X-Request-ID", "${uuid}").
    To("direct:output")

// Routage basé sur le contenu avec Choice
builder.From("direct:start").
    Choice().
        When("${header.priority == 'high'}").
            SimpleSetBody("🚨 URGENT: ${body}").
            To("direct:urgent").
        When("${body['count'] > 100}").
            To("direct:large-batch").
        Otherwise().
            To("direct:normal").
    EndChoice()
```

## Enterprise Integration Patterns (EIP)

### Split EIP

Diviser un message en plusieurs parties et les traiter individuellement.

```go
builder.From("direct:start").
    Split(func(e *gocamel.Exchange) (any, error) {
        body := e.GetIn().GetBody().(string)
        return strings.Split(body, ","), nil
    }).
    Log("Traitement partie: ${body}").
    To("direct:process-part").
    End()
```

### Aggregate EIP

Combiner plusieurs messages en un seul selon une clé de corrélation.

```go
strategy := &MyAggregationStrategy{}
repo := gocamel.NewMemoryAggregationRepository()

builder.From("direct:start").
    Aggregate(gocamel.NewAggregator(correlationExpr, strategy, repo).
        SetCompletionSize(3)).
    Log("Message agrégé: ${body}")
```

### Multicast EIP

Envoyer une copie du message vers plusieurs destinations en parallèle.

```go
builder.From("direct:start").
    Multicast().
        Pipeline().
            Log("Branche 1: ${body}").
            To("direct:out1").
        End().
        Pipeline().
            Log("Branche 2: ${body}").
            To("direct:out2").
        End().
    End()
```

### Choice EIP

Routage basé sur le contenu avec branches conditionnelles.

```go
builder.From("direct:decision").
    Choice().
        When("${header.type == 'A'}").
            To("direct:typeA").
        When("${header.type == 'B'}").
            To("direct:typeB").
        Otherwise().
            To("direct:default").
    EndChoice()
```

## Composants Disponibles

| Composant | Pattern URI | Description |
|-----------|-------------|-------------|
| **HTTP** | `http://host:port/path` | Serveur HTTP (Consumer) et client (Producer) |
| **File** | `file://path` | Opérations sur le système de fichiers |
| **FTP** | `ftp://host/path` | Support client FTP |
| **SFTP** | `sftp://host/path` | FTP sécurisé avec authentification SSH |
| **SMB** | `smb://host/share` | Support des partages Windows/Samba |
| **Direct** | `direct:name` | Routage synchrone en mémoire |
| **Timer** | `timer:name` | Déclencheurs basés sur une minuterie |
| **Cron** | `cron:name` | Déclencheurs planifiés par expression cron |
| **Telegram** | `telegram:bots` | Intégration API Bot Telegram |
| **OpenAI** | `openai:chat` | API Chat Completion OpenAI |
| **Exec** | `exec:command` | Exécution de commandes système |
| **Mail** | `smtp://...`, `imap://...` | Envoi et réception d'emails |
| **SQL** | `sql://datasource` | Exécution de requêtes SQL |
| **SQL-Stored** | `sql-stored://ds` | Appels de procédures stockées |
| **XSLT** | `xslt:template` | Transformation XML |
| **XSD** | `xsd:schema` | Validation de schéma XML |
| **Template** | `template:name` | Traitement de templates Go |

## Configuration

Les paramètres sensibles (tokens, mots de passe) peuvent être fournis de trois façons:

1. **Directement dans l'URI**: `ftp://user:pass@host/path`
2. **Paramètres de requête**: `telegram:bots?authorizationToken=XXX`
3. **Variables d'environnement** (Recommandé): `FTP_PASSWORD=***`, `TELEGRAM_AUTHORIZATIONTOKEN=***`

## Exemple: Intégration FTP

```go
package main

import "github.com/tranchida/gocamel"

func main() {
    context := gocamel.NewCamelContext()
    context.AddComponent("ftp", gocamel.NewFTPComponent())

    route := context.CreateRouteBuilder().
        From("ftp://localhost:21/incoming?delay=10s&delete=true").
        Log("Nouveau fichier téléchargé").
        SetHeader(gocamel.CamelFileName, "processed.txt").
        To("ftp://localhost:21/processed").
        Build()

    context.AddRoute(route)
    context.Start()
    select {}
}
```

## API REST de Management

Activer le monitoring et le contrôle inspiré de JMX:

```go
mgmt := gocamel.NewManagementServer(context)
mgmt.Start(":8081")  // Accès http://localhost:8081/routes
```

**Endpoints disponibles:**
- `GET /routes` - Liste toutes les routes
- `GET /routes/{id}` - Détails d'une route
- `POST /routes/{id}/start` - Démarrer une route
- `POST /routes/{id}/stop` - Arrêter une route
- `GET /health` - Vérification de santé

---

## 🏗️ Project Structure | Structure du Projet

```
gocamel/
├── context.go              # Camel context management | Gestion du contexte
├── exchange.go             # Message exchange | Structure d'échange
├── message.go              # Message structure | Structure de message
├── route.go                # Route management | Gestion des routes
├── route_builder.go        # DSL Builder pattern | Pattern Builder DSL
├── registry.go             # Component registry | Registre des composants
├── aggregator.go           # Aggregate EIP | EIP Aggregate
├── splitter.go             # Split EIP | EIP Split
├── multicast.go            # Multicast EIP | EIP Multicast
├── choice.go               # Choice EIP | EIP Choice
├── http_component.go       # HTTP component | Composant HTTP
├── file_component.go       # File component | Composant File
├── ftp_component.go        # FTP component | Composant FTP
├── sftp_component.go       # SFTP component | Composant SFTP
├── smb_component.go        # SMB component | Composant SMB
├── mail_component.go       # Mail component | Composant Mail
├── sql_component.go        # SQL component | Composant SQL
├── sql_stored_component.go # SQL-Stored component | Composant SQL-Stored
├── telegram_component.go    # Telegram component | Composant Telegram
├── openai_component.go     # OpenAI component | Composant OpenAI
├── template_component.go   # Template component | Composant Template
├── cron_component.go       # Cron component | Composant Cron
└── management.go          # REST API | API REST
```

---

## 📄 License | Licence

MIT License - See [LICENSE](LICENSE) file for details.

Licence MIT - Voir le fichier [LICENSE](LICENSE) pour les détails.

---

<p align="center">
  <strong>GoCamel</strong> - Making Enterprise Integration Simple in Go<br>
  <strong>GoCamel</strong> - Rendre l'Intégration d'Entreprise Simple en Go
</p>

<p align="center">
  Made with ❤️ | Fait avec ❤️
</p>
