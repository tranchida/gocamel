# Components Reference | Référence des Composants

---

# 🇺🇸 English

## Overview

Complete reference of all available components in GoCamel. Components provide connectivity to external systems and services.

## Core Components

### Direct

Synchronous in-memory routing between routes in the same context.

```go
// Consumer: receives messages from direct endpoint
// Producer: sends messages to direct endpoint
builder.From("direct:start").To("direct:process")
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `reuseChannel` | bool | `true` | Reuse the same channel |

---

### Timer

Simple periodic triggering.

```go
builder.From("timer:tick?period=5s")
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `period` | Duration | `1s` | Period between triggers |
| `repeatCount` | int | `0` | Number of repetitions (0=infinite) |
| `fixedRate` | bool | `false` | Fixed-rate vs fixed-delay mode |

---

## File Transfer Components

### File

Local file system operations.

```go
// Consumer (read)
builder.From("file://input?delete=true")

// Producer (write)
builder.To("file://output")
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `delete` | bool | `false` | Delete after processing |
| `noop` | bool | `false` | Do not move/delete file |
| `include` | string | `""` | Include file pattern |
| `exclude` | string | `""` | Exclude file pattern |

---

### FTP / FTPS

File transfer via FTP/FTPS.

```go
builder.From("ftp://host:21/incoming?username=${env:FTP_USER}")
```

---

### SFTP

Secure file transfer via SSH.

```go
builder.From("sftp://host:22/data?username=scott")
```

---

### SMB

Windows/Samba share access.

```go
builder.From("smb://server/share/folder")
```

---

## Network Components

### HTTP

HTTP server and client.

```go
// Consumer (server)
builder.From("http://localhost:8080/api")

// Producer (client)
builder.To("http://example.com/webhook")
```

---

## Messaging Components

### Telegram

Telegram Bot integration for receiving/sending messages.

```go
ctx.AddComponent("telegram", gocamel.NewTelegramComponent())
builder.From("telegram:bots").Log("${body}")
```

---

## AI/LLM Components

### OpenAI

ChatGPT / GPT-4 integration.

```go
ctx.AddComponent("openai", gocamel.NewOpenAIComponent())

endpoint, _ := ctx.CreateEndpoint("openai:chat?model=gpt-4")
producer, _ := endpoint.CreateProducer()
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `model` | string | `gpt-3.5-turbo` | OpenAI model to use |

---

## Scheduling Components

### Cron

Advanced scheduling with cron expressions.

```go
ctx.AddComponent("cron", gocamel.NewCronComponent())

// Cron expression:
builder.From("cron://group/job?cron=0+*+*+*+*+*")

// Simple interval:
builder.From("cron://poller?trigger.repeatInterval=5000")
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `cron` | string | — | 6-field cron expression |
| `trigger.repeatInterval` | int | — | Interval in milliseconds |
| `trigger.repeatCount` | int | `-1` | Max executions (-1=infinite) |
| `triggerStartDelay` | int | `500` | Delay before first trigger (ms) |
| `stateful` | bool | `false` | Prevent concurrent executions |

---

## Email Components

### Mail (SMTP/SMTPS/IMAP/IMAPS/POP3/POP3S)

Send and receive emails.

```go
// Send via SMTP
builder.To("smtps://smtp.gmail.com:465?to=dest@example.com")

// Receive via IMAP with IDLE
builder.From("imaps://imap.gmail.com:993?folderName=INBOX&idle=true")

// Receive via POP3
builder.From("pop3s://pop.gmail.com:995?username=user&password=pass")
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `username` | string | `""` | Authentication credentials |
| `password` | string | `""` | Authentication credentials |
| `to` | string | `""` | Recipient(s) (Producer) |
| `subject` | string | `""` | Email subject (Producer) |
| `contentType` | string | `"text/plain"` | MIME type (Producer) |
| `folderName` | string | `"INBOX"` | IMAP folder (Consumer) |
| `unseen` | bool | `true` | Unread messages only |
| `idle` | bool | `false` | IMAP IDLE for push notifications |
| `delete` | bool | `false` | Delete after processing |
| `fetchSize` | int | `-1` | Max messages per poll |
| `pollDelay` | int | `60000` | Delay between polls (ms) |

---

## Transformation Components

### XSLT

XML transformation via XSL stylesheet.

```go
builder.To("xslt:file://transform.xsl")
```

---

### Template

Go template processing (inspired by Apache Camel Velocity).

```go
// Basic template
builder.To("template:templates/email.tmpl")

// With caching
builder.To("template:templates/item.tmpl?contentCache=true")

// Dynamic template
builder.To("template:default.tmpl?allowTemplateFromHeader=true")
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `contentCache` | bool | `false` | Cache template in memory |
| `allowTemplateFromHeader` | bool | `false` | Allow override via `CamelTemplatePath` header |
| `startDelimiter` | string | `{{` | Start delimiter |
| `endDelimiter` | string | `}}` | End delimiter |

**Template Variables:**

```
{{.Body}}              # Message body
{{.Headers.name}}      # Header value
{{.Exchange.ID}}       # Exchange ID
{{.Exchange.Created}}  # Creation timestamp
```

**Template Functions:**

```
{{.Body | upper}}
{{.Body | lower}}
{{.Body | trim}}
{{now | formatDate "2006-01-02 15:04:05"}}
{{"hello" | contains "ell"}}
```

---

### XSD

XML Schema validation.

```go
builder.To("xsd:file://schema.xsd")
```

---

## Execution Components

### Exec

Execute system commands.

```go
builder.To("exec:ls -la")
```

---

## Database Components

### SQL

SQL query execution (`SELECT`, `INSERT`, `UPDATE`, `DELETE`) via `database/sql`.

```go
import (
    "database/sql"
    _ "github.com/mattn/go-sqlite3"
)

db, _ := sql.Open("sqlite3", "./app.db")

sqlComp := gocamel.NewSQLComponent()
sqlComp.RegisterDataSource("appdb", db)
ctx.AddComponent("sql", sqlComp)

// SELECT -> Out.Body = []map[string]any
builder.From("direct:list").
    To("sql://appdb?query=SELECT+id,name+FROM+users").
    Log("${body}")

// SELECT with single row -> Out.Body = map[string]any
builder.From("direct:one").
    SetHeader(gocamel.SqlParameters, []any{42}).
    To("sql://appdb?query=SELECT+*+FROM+users+WHERE+id=?&outputType=SelectOne")

// INSERT/UPDATE/DELETE -> Out.Body = affected rows (int64)
builder.From("direct:insert").
    SetHeader(gocamel.SqlParameters, []any{"alice", "alice@example.com"}).
    To("sql://appdb?query=INSERT+INTO+users(name,email)+VALUES(?,?)")
```

**URI Format:**

```
sql://<datasourceName>?query=<SQL>
sql://logical?dataSourceRef=<datasourceName>&query=<SQL>
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `query` | string | required | SQL query with optional `${header.x}`, `${body}`, `${property.x}` |
| `dataSourceRef` | string | host URI | Registered datasource name |
| `outputType` | string | `SelectList` | `SelectList` or `SelectOne` |
| `batch` | bool | `false` | Batch mode: body = `[][]any` |
| `transacted` | bool | `false` | Wrap in transaction |

**Query Parameters:**

Provided via `CamelSqlParameters` header or body as `[]any`.

**Output Headers:**

| Header | Description |
|--------|-------------|
| `CamelSqlRowCount` | Rows returned/affected |
| `CamelSqlColumnNames` | Column names for SELECT |

**Result Body:**

| Case | Out.Body Type |
|------|---------------|
| `SELECT` + `SelectList` | `[]map[string]any` |
| `SELECT` + `SelectOne` | `map[string]any` (or `nil`) |
| `INSERT/UPDATE/DELETE` | `int64` (affected rows) |
| `batch=true` | `int64` (total affected) |

---

### SQL-Stored

Stored procedure execution with IN, OUT, and INOUT parameter support.

```go
import (
    "database/sql"
    _ "github.com/mattn/go-sqlite3"
)

db, _ := sql.Open("mysql", "user:pass@/mydb")

sqlStored := gocamel.NewSQLStoredComponent()
sqlStored.RegisterDataSource("mydb", db)
ctx.AddComponent("sql-stored", sqlStored)

// Simple call with IN parameters
builder.From("direct:call").
    SetBody([]gocamel.StoredProcedureParam{
        {Name: "userId", Direction: gocamel.ParamDirectionIn, Value: 42},
    }).
    To("sql-stored://mydb?procedure=GET_USER_BY_ID").
    Log("${body}")
```

**URI Format:**

```
sql-stored://datasourceName?procedure=NAME
sql-stored://logical?dataSourceRef=dsName&procedure=NAME
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `procedure` | string | required | Stored procedure name |
| `dataSourceRef` | string | host URI | Datasource name |
| `outputType` | string | `SelectList` | `SelectList` or `SelectOne` |
| `transacted` | bool | `false` | Execute in transaction |
| `noop` | bool | `false` | Test mode: do not execute |

**Parameter Directions:**

| Direction | Description |
|-----------|-------------|
| `ParamDirectionIn` | Input only |
| `ParamDirectionOut` | Output only |
| `ParamDirectionInOut` | Input and output |

---

## Component Configuration

### Authentication

Credentials can be provided via environment variables:

```go
builder.From("ftp://host?username=${env:FTP_USER}")
```

### Common Options

```go
// File: polling options
builder.From("file://data?delay=10s&delete=true")

// HTTP: method
builder.To("http://api?httpMethod=POST")
```

---

---

# 🇫🇷 Français

## Vue d'Ensemble

Référence complète de tous les composants disponibles dans GoCamel. Les composants fournissent la connectivité aux systèmes et services externes.

## Composants Core

### Direct

Routage synchrone en mémoire entre routes du même contexte.

```go
// Consumer: reçoit des messages du endpoint direct
// Producer: envoie des messages vers le endpoint direct
builder.From("direct:start").To("direct:process")
```

| Option | Type | Défaut | Description |
|--------|------|--------|-------------|
| `reuseChannel` | bool | `true` | Réutiliser le même channel |

---

### Timer

Déclenchement périodique simple.

```go
builder.From("timer:tick?period=5s")
```

| Option | Type | Défaut | Description |
|--------|------|--------|-------------|
| `period` | Duration | `1s` | Période entre déclenchements |
| `repeatCount` | int | `0` | Nombre de répétitions (0=infini) |
| `fixedRate` | bool | `false` | Mode fixed-rate vs fixed-delay |

---

## Composants de Transfert de Fichiers

### File

Opérations sur le système de fichiers local.

```go
// Consumer (lecture)
builder.From("file://input?delete=true")

// Producer (écriture)
builder.To("file://output")
```

| Option | Type | Défaut | Description |
|--------|------|--------|-------------|
| `delete` | bool | `false` | Supprimer après traitement |
| `noop` | bool | `false` | Ne pas déplacer/supprimer le fichier |
| `include` | string | `""` | Pattern fichiers inclus |
| `exclude` | string | `""` | Pattern fichiers exclus |

---

### FTP / FTPS

Transfert de fichiers via FTP/FTPS.

```go
builder.From("ftp://host:21/incoming?username=${env:FTP_USER}")
```

---

### SFTP

Transfert sécurisé via SSH.

```go
builder.From("sftp://host:22/data?username=scott")
```

---

### SMB

Accès aux partages Windows/Samba.

```go
builder.From("smb://server/share/folder")
```

---

## Composants Réseau

### HTTP

Serveur et client HTTP.

```go
// Consumer (serveur)
builder.From("http://localhost:8080/api")

// Producer (client)
builder.To("http://example.com/webhook")
```

---

## Composants de Messagerie

### Telegram

Intégration Bot Telegram pour recevoir/envoyer des messages.

```go
ctx.AddComponent("telegram", gocamel.NewTelegramComponent())
builder.From("telegram:bots").Log("${body}")
```

---

## Composants IA/LLM

### OpenAI

Intégration ChatGPT / GPT-4.

```go
ctx.AddComponent("openai", gocamel.NewOpenAIComponent())

endpoint, _ := ctx.CreateEndpoint("openai:chat?model=gpt-4")
producer, _ := endpoint.CreateProducer()
```

| Option | Type | Défaut | Description |
|--------|------|--------|-------------|
| `model` | string | `gpt-3.5-turbo` | Modèle OpenAI à utiliser |

---

## Composants de Planification

### Cron

Planification avancée avec expressions cron.

```go
ctx.AddComponent("cron", gocamel.NewCronComponent())

// Expression cron:
builder.From("cron://group/job?cron=0+*+*+*+*+*")

// Intervalle simple:
builder.From("cron://poller?trigger.repeatInterval=5000")
```

| Option | Type | Défaut | Description |
|--------|------|--------|-------------|
| `cron` | string | — | Expression cron 6 champs |
| `trigger.repeatInterval` | int | — | Intervalle en millisecondes |
| `trigger.repeatCount` | int | `-1` | Max exécutions (-1=infini) |
| `triggerStartDelay` | int | `500` | Délai avant premier déclenchement (ms) |
| `stateful` | bool | `false` | Empêcher exécutions concurrentes |

---

## Composants Email

### Mail (SMTP/SMTPS/IMAP/IMAPS/POP3/POP3S)

Envoi et réception d'emails.

```go
// Envoi via SMTP
builder.To("smtps://smtp.gmail.com:465?to=dest@example.com")

// Réception via IMAP avec IDLE
builder.From("imaps://imap.gmail.com:993?folderName=INBOX&idle=true")

// Réception via POP3
builder.From("pop3s://pop.gmail.com:995?username=user&password=pass")
```

| Option | Type | Défaut | Description |
|--------|------|--------|-------------|
| `username` | string | `""` | Credentials d'authentification |
| `password` | string | `""` | Credentials d'authentification |
| `to` | string | `""` | Destinataire(s) (Producer) |
| `subject` | string | `""` | Sujet du message (Producer) |
| `contentType` | string | `"text/plain"` | Type MIME (Producer) |
| `folderName` | string | `"INBOX"` | Dossier IMAP (Consumer) |
| `unseen` | bool | `true` | Messages non lus seulement |
| `idle` | bool | `false` | IMAP IDLE pour notifications push |
| `delete` | bool | `false` | Supprimer après traitement |
| `fetchSize` | int | `-1` | Messages max par poll |
| `pollDelay` | int | `60000` | Délai entre polls (ms) |

---

## Composants de Transformation

### XSLT

Transformation XML via feuille de style XSL.

```go
builder.To("xslt:file://transform.xsl")
```

---

### Template

Traitement de templates Go (inspiré par Apache Camel Velocity).

```go
// Template basique
builder.To("template:templates/email.tmpl")

// Avec cache
builder.To("template:templates/item.tmpl?contentCache=true")

// Template dynamique
builder.To("template:default.tmpl?allowTemplateFromHeader=true")
```

| Option | Type | Défaut | Description |
|--------|------|--------|-------------|
| `contentCache` | bool | `false` | Cache le template en mémoire |
| `allowTemplateFromHeader` | bool | `false` | Permet override via header `CamelTemplatePath` |
| `startDelimiter` | string | `{{` | Délimiteur de début |
| `endDelimiter` | string | `}}` | Délimiteur de fin |

**Variables de Template:**

```
{{.Body}}              # Corps du message
{{.Headers.name}}      # Valeur d'un header
{{.Exchange.ID}}       # ID de l'échange
{{.Exchange.Created}}  # Timestamp de création
```

**Fonctions de Template:**

```
{{.Body | upper}}
{{.Body | lower}}
{{.Body | trim}}
{{now | formatDate "2006-01-02 15:04:05"}}
{{"hello" | contains "ell"}}
```

---

### XSD

Validation XML via schéma.

```go
builder.To("xsd:file://schema.xsd")
```

---

## Composants d'Exécution

### Exec

Exécution de commandes système.

```go
builder.To("exec:ls -la")
```

---

## Composants Base de Données

### SQL

Exécution de requêtes SQL (`SELECT`, `INSERT`, `UPDATE`, `DELETE`) via `database/sql`.

```go
import (
    "database/sql"
    _ "github.com/mattn/go-sqlite3"
)

db, _ := sql.Open("sqlite3", "./app.db")

sqlComp := gocamel.NewSQLComponent()
sqlComp.RegisterDataSource("appdb", db)
ctx.AddComponent("sql", sqlComp)

// SELECT -> Out.Body = []map[string]any
builder.From("direct:list").
    To("sql://appdb?query=SELECT+id,name+FROM+users").
    Log("${body}")

// SELECT une seule ligne -> Out.Body = map[string]any
builder.From("direct:one").
    SetHeader(gocamel.SqlParameters, []any{42}).
    To("sql://appdb?query=SELECT+*+FROM+users+WHERE+id=?&outputType=SelectOne")

// INSERT/UPDATE/DELETE -> Out.Body = lignes affectées (int64)
builder.From("direct:insert").
    SetHeader(gocamel.SqlParameters, []any{"alice", "alice@example.com"}).
    To("sql://appdb?query=INSERT+INTO+users(name,email)+VALUES(?,?)")
```

**Format URI:**

```
sql://<datasourceName>?query=<SQL>
sql://logical?dataSourceRef=<datasourceName>&query=<SQL>
```

| Option | Type | Défaut | Description |
|--------|------|--------|-------------|
| `query` | string | obligatoire | Requête SQL avec `${header.x}`, `${body}`, `${property.x}` optionnels |
| `dataSourceRef` | string | host URI | Nom datasource enregistrée |
| `outputType` | string | `SelectList` | `SelectList` ou `SelectOne` |
| `batch` | bool | `false` | Mode batch: body = `[][]any` |
| `transacted` | bool | `false` | Englober dans une transaction |

**Paramètres de Requête:**

Fournis via header `CamelSqlParameters` ou body comme `[]any`.

**Headers de Sortie:**

| Header | Description |
|--------|-------------|
| `CamelSqlRowCount` | Lignes retournées/affectées |
| `CamelSqlColumnNames` | Noms des colonnes pour SELECT |

**Corps Résultat:**

| Cas | Type Out.Body |
|-----|---------------|
| `SELECT` + `SelectList` | `[]map[string]any` |
| `SELECT` + `SelectOne` | `map[string]any` (ou `nil`) |
| `INSERT/UPDATE/DELETE` | `int64` (lignes affectées) |
| `batch=true` | `int64` (total affectées) |

---

### SQL-Stored

Exécution de procédures stockées avec support paramètres IN, OUT et INOUT.

```go
import (
    "database/sql"
    _ "github.com/mattn/go-sqlite3"
)

db, _ := sql.Open("mysql", "user:pass@/mydb")

sqlStored := gocamel.NewSQLStoredComponent()
sqlStored.RegisterDataSource("mydb", db)
ctx.AddComponent("sql-stored", sqlStored)

// Appel simple avec paramètres IN
builder.From("direct:call").
    SetBody([]gocamel.StoredProcedureParam{
        {Name: "userId", Direction: gocamel.ParamDirectionIn, Value: 42},
    }).
    To("sql-stored://mydb?procedure=GET_USER_BY_ID").
    Log("${body}")
```

**Format URI:**

```
sql-stored://datasourceName?procedure=NAME
sql-stored://logical?dataSourceRef=dsName&procedure=NAME
```

| Option | Type | Défaut | Description |
|--------|------|--------|-------------|
| `procedure` | string | obligatoire | Nom procédure stockée |
| `dataSourceRef` | string | host URI | Nom datasource |
| `outputType` | string | `SelectList` | `SelectList` ou `SelectOne` |
| `transacted` | bool | `false` | Exécuter dans une transaction |
| `noop` | bool | `false` | Mode test: ne pas exécuter |

**Direction des Paramètres:**

| Direction | Description |
|-----------|-------------|
| `ParamDirectionIn` | Entrée seule |
| `ParamDirectionOut` | Sortie seule |
| `ParamDirectionInOut` | Entrée et sortie |

---

## Configuration des Composants

### Authentification

Les identifiants peuvent être fournis via variables d'environnement:

```go
builder.From("ftp://host?username=${env:FTP_USER}")
```

### Options Communes

```go
// File: options de polling
builder.From("file://data?delay=10s&delete=true")

// HTTP: méthode
builder.To("http://api?httpMethod=POST")
```
