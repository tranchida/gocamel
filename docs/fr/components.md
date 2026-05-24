# Components Reference | Référence des Composants

---

# 🇺🇸 English

## Overview

Complete reference of all available components in GoCamel. Components provide connectivity to external systems and
services.

## Core Components

### Direct

Synchronous in-memory routing between routes in the same context.

```go
// Consumer: receives messages from direct endpoint
// Producer: sends messages to direct endpoint
builder.From("direct:start").To("direct:process")
```

| Option         | Type | Default | Description            |
|----------------|------|---------|------------------------|
| `reuseChannel` | bool | `true`  | Reuse the same channel |

---

### Timer

Simple periodic triggering.

```go
builder.From("timer:tick?period=5s")
```

| Option        | Type     | Default | Description                        |
|---------------|----------|---------|------------------------------------|
| `period`      | Duration | `1s`    | Period between triggers            |
| `repeatCount` | int      | `0`     | Number of repetitions (0=infinite) |
| `fixedRate`   | bool     | `false` | Fixed-rate vs fixed-delay mode     |

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

| Option    | Type   | Default | Description             |
|-----------|--------|---------|-------------------------|
| `delete`  | bool   | `false` | Delete after processing |
| `noop`    | bool   | `false` | Do not move/delete file |
| `include` | string | `""`    | Include file pattern    |
| `exclude` | string | `""`    | Exclude file pattern    |

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

Serveur et client HTTP.

```go
// Consumer (serveur)
builder.From("http://localhost:8080/api")

// Producer (client)
builder.To("http://example.com/webhook")
```

#### Authentification, TLS, rate limiting (middleware consumer)

Le consumer HTTP ne fournit **aucune authentification par défaut**. Ne
l'exposez sur un réseau non fiable qu'à travers un middleware qui applique
le modèle de sécurité dont vous avez besoin (vérification de token, mTLS,
OAuth, allowlist d'IP, rate limit, ...). Installez le middleware sur
`HTTPComponent` avant le démarrage de la route :

```go
http := gocamel.NewHTTPComponent()
http.SetMiddleware(func(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.Header.Get("Authorization") != "Bearer "+os.Getenv("API_TOKEN") {
            http.Error(w, "unauthorized", http.StatusUnauthorized)
            return
        }
        next.ServeHTTP(w, r)
    })
})
ctx.AddComponent("http", http)
```

Le middleware enveloppe chaque handler installé par un consumer `http://`
dans ce contexte.

#### Sanitisation des headers sortants

Le producer HTTP (`To`) et le chemin de réponse du consumer rejettent les
noms et valeurs de header contenant `\r` ou `\n` (CRLF) pour empêcher le
HTTP response splitting (CWE-113). Un exchange qui tente de positionner un
tel header reçoit une erreur de `Send()`.

#### Arrêt

La goroutine du consumer est suivie par un `WaitGroup` interne et `Stop()`
attend que `http.Server.Shutdown` se termine (borné par un délai interne de
30 secondes) avant de retourner. `Stop()` est idempotent.

---

## Messaging Components

### Telegram

Telegram Bot integration for receiving/sending messages.

```go
ctx.AddComponent("telegram", gocamel.NewTelegramComponent())
builder.From("telegram:bots").Log("${body}")
```

---

### NATS

Asynchronous event-driven messaging with NATS. High performance publisher and subscriber with Go NATS client.

```go
ctx.AddComponent("nats", gocamel.NewNATSComponent())

// Consumer: reactive asynchronous subscription
builder.From("nats://orders?servers=nats://localhost:4222").
    ProcessFunc(func(e *gocamel.Exchange) error {
        // NATS payload is received as a byte slice
        payload := e.GetIn().GetBody().([]byte)
        fmt.Printf("Received order: %s\n", string(payload))
        return nil
    })

// Producer: publish to subject
builder.To("nats://orders?servers=nats://localhost:4222")

// Dynamically override subject in header
exchange.In.SetHeader(gocamel.CamelNATSSubject, "urgent-orders")
```

> [!WARNING]
> When writing URIs, always use double slashes (e.g. `nats://orders`) instead of `nats:orders`, otherwise `url.Parse` treats it as an Opaque URI, which contaminates the subject string with query parameters.

| Option | Type | Default | Description |
|---|---|---|---|
| `servers` | string | `nats://127.0.0.1:4222` | NATS server addresses (comma-separated) |
| `subject` | string | parsed from path | NATS subject to subscribe/publish to |

**Exchange Headers:**

- `CamelNATSSubject` - Subject of the received message or target subject for publishing.

---

### Redis

Send commands (SET, GET, DEL, PUBLISH) or subscribe to Redis Pub/Sub channels.

```go
ctx.AddComponent("redis", gocamel.NewRedisComponent())

// Consumer: Subscribe to Pub/Sub channel
builder.From("redis://localhost:6379?subscribe=true&channel=mychannel").
    Log("Received Pub/Sub message: ${body}")

// Producer: Execute commands
builder.To("redis://localhost:6379?command=SET&key=mykey")
```

**Supported Commands:**

- `SET`: Saves the exchange body as the value of `key`.
- `GET`: Retrieves the value of `key` and writes it to `Out.Body`.
- `DEL`: Deletes `key` and writes the number of affected keys to `Out.Body`.
- `PUBLISH`: Publishes the exchange body to the specified channel/key.

**Exchange Headers (Override URI parameters):**

- `CamelRedisCommand` - The Redis command to execute (e.g. `"GET"`).
- `CamelRedisKey` - The target Redis key.
- `CamelRedisChannel` - Target Pub/Sub channel for publish or source channel for subscribe.

| Option | Type | Default | Description |
|---|---|---|---|
| `command` | string | `SET` | Redis command for producer |
| `key` | string | `""` | Redis key to operate on |
| `channel` | string | `""` | Redis Pub/Sub channel |
| `subscribe` | bool | `false` | If true, acts as a Pub/Sub consumer |

---

### Redis Idempotent Repository

The Redis Idempotent Repository provides a distributed implementation of the `IdempotentRepository` interface, allowing multiple GoCamel instances in a cluster to coordinate and avoid processing duplicate messages.

```go
import (
    "time"
    "github.com/redis/go-redis/v9"
)

rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
// Key prefix "idempotent:" with 24-hour expiration TTL
repo := gocamel.NewRedisIdempotentRepository(rdb, "idempotent:", 24*time.Hour)

builder.From("direct:start").
    IdempotentConsumer("${header.MessageId}", repo).
    To("direct:process")
```

---

### Go Channel

A native, ultra-fast intra-process integration component utilizing native Go channels. It enables decoupling route segments asynchronously inside the same process.

```go
ctx.AddComponent("chan", gocamel.NewChanComponent())

// Consumer: processes exchanges asynchronously from channel
builder.From("chan:orders?bufferSize=500").
    To("direct:process-order")

// Producer: writes a copy of the exchange to the channel asynchronously
builder.To("chan:orders")
```

| Option | Type | Default | Description |
|---|---|---|---|
| `bufferSize` | int | `100` | Capacity of the internal buffered channel |

---

## AI/LLM Components

### OpenAI

ChatGPT / GPT-4 integration.

```go
ctx.AddComponent("openai", gocamel.NewOpenAIComponent())

endpoint, _ := ctx.CreateEndpoint("openai:chat?model=gpt-4")
producer, _ := endpoint.CreateProducer()
```

| Option  | Type   | Default         | Description         |
|---------|--------|-----------------|---------------------|
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

| Option                   | Type   | Default | Description                     |
|--------------------------|--------|---------|---------------------------------|
| `cron`                   | string | —       | 6-field cron expression         |
| `trigger.repeatInterval` | int    | —       | Interval in milliseconds        |
| `trigger.repeatCount`    | int    | `-1`    | Max executions (-1=infinite)    |
| `triggerStartDelay`      | int    | `500`   | Delay before first trigger (ms) |
| `stateful`               | bool   | `false` | Prevent concurrent executions   |

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

| Option        | Type   | Default        | Description                      |
|---------------|--------|----------------|----------------------------------|
| `username`    | string | `""`           | Authentication credentials       |
| `password`    | string | `""`           | Authentication credentials       |
| `to`          | string | `""`           | Recipient(s) (Producer)          |
| `subject`     | string | `""`           | Email subject (Producer)         |
| `contentType` | string | `"text/plain"` | MIME type (Producer)             |
| `folderName`  | string | `"INBOX"`      | IMAP folder (Consumer)           |
| `unseen`      | bool   | `true`         | Unread messages only             |
| `idle`        | bool   | `false`        | IMAP IDLE for push notifications |
| `delete`      | bool   | `false`        | Delete after processing          |
| `fetchSize`   | int    | `-1`           | Max messages per poll            |
| `pollDelay`   | int    | `60000`        | Delay between polls (ms)         |

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

| Option                    | Type   | Default | Description                                   |
|---------------------------|--------|---------|-----------------------------------------------|
| `contentCache`            | bool   | `false` | Cache template in memory                      |
| `allowTemplateFromHeader` | bool   | `false` | Allow override via `CamelTemplatePath` header |
| `startDelimiter`          | string | `{{`    | Start delimiter                               |
| `endDelimiter`            | string | `}}`    | End delimiter                                 |

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

| Option          | Type   | Default      | Description                                                       |
|-----------------|--------|--------------|-------------------------------------------------------------------|
| `query`         | string | required     | SQL query with optional `${header.x}`, `${body}`, `${property.x}` |
| `dataSourceRef` | string | host URI     | Registered datasource name                                        |
| `outputType`    | string | `SelectList` | `SelectList` or `SelectOne`                                       |
| `batch`         | bool   | `false`      | Batch mode: body = `[][]any`                                      |
| `transacted`    | bool   | `false`      | Wrap in transaction                                               |

**Query Parameters:**

Provided via `CamelSqlParameters` header or body as `[]any`.

**Output Headers:**

| Header                | Description             |
|-----------------------|-------------------------|
| `CamelSqlRowCount`    | Rows returned/affected  |
| `CamelSqlColumnNames` | Column names for SELECT |

**Result Body:**

| Case                    | Out.Body Type               |
|-------------------------|-----------------------------|
| `SELECT` + `SelectList` | `[]map[string]any`          |
| `SELECT` + `SelectOne`  | `map[string]any` (or `nil`) |
| `INSERT/UPDATE/DELETE`  | `int64` (affected rows)     |
| `batch=true`            | `int64` (total affected)    |

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

| Option          | Type   | Default      | Description                 |
|-----------------|--------|--------------|-----------------------------|
| `procedure`     | string | required     | Stored procedure name       |
| `dataSourceRef` | string | host URI     | Datasource name             |
| `outputType`    | string | `SelectList` | `SelectList` or `SelectOne` |
| `transacted`    | bool   | `false`      | Execute in transaction      |
| `noop`          | bool   | `false`      | Test mode: do not execute   |

**Parameter Directions:**

| Direction             | Description      |
|-----------------------|------------------|
| `ParamDirectionIn`    | Input only       |
| `ParamDirectionOut`   | Output only      |
| `ParamDirectionInOut` | Input and output |

---

### MongoDB

Intégration avec MongoDB pour exécuter des opérations CRUD. Producer-only.

#### Format URI

```
mongodb://connectionName?database=mydb&collection=mycoll&operation=find
```

#### Options

| Option          | Type   | Requis | Description                                                              |
|-----------------|--------|--------|--------------------------------------------------------------------------|
| `database`      | string | Oui    | Nom de la base de données                                                |
| `collection`    | string | Oui    | Nom de la collection                                                     |
| `operation`     | string | Oui    | Opération: find, findOne, insert, insertOne, save, update, remove, count |
| `connectionRef` | string | Non    | Référence connexion preregistered                                        |

#### Headers

| Header                   | Mode  | Description                          |
|--------------------------|-------|--------------------------------------|
| `CamelMongoDbDatabase`   | R/W   | Nom base de données                  |
| `CamelMongoDbCollection` | R/W   | Nom collection                       |
| `CamelMongoDbOperation`  | R/W   | Opération à exécuter                 |
| `CamelMongoDbCriteria`   | Write | Filtre/critères (map ou JSON string) |
| `CamelMongoDbLimit`      | Write | Limite résultats                     |
| `CamelMongoDbSkip`       | Write | Sauter N documents                   |
| `CamelMongoDbSort`       | Write | Tri (json: {"field": 1})             |

#### Headers Sortie

| Header                    | Description                       |
|---------------------------|-----------------------------------|
| `CamelMongoDbResultTotal` | Nombre documents trouvés/affectés |
| `CamelMongoDbOid`         | ObjectID document inséré          |

#### Exemple

```go
import (
    "context"
    "go.mongodb.org/mongo-driver/mongo"
    "go.mongodb.org/mongo-driver/mongo/options"
)

// Créer composant
mongoComp := gocamel.NewMongoDBComponent()

// Connexion
client, _ := mongo.Connect(context.Background(), options.Client().ApplyURI("mongodb://localhost:27017"))
conn := gocamel.CreateMongoDBConnection(client, "mongodb://localhost:27017", "mydb")
mongoComp.RegisterConnection("myconn", conn)

builder.WithComponent("mongodb", mongoComp)

// Insert
builder.From("timer:tick?period=5s").
    SetBody(map[string]any{"name": "test", "value": 123}).
    To("mongodb://myconn?database=mydb&collection=items&operation=insert")

// Query avec filtre
builder.From("direct:search").
    SetHeader(gocamel.CamelMongoDbCriteria, map[string]any{"status": "active"}).
    SetHeader(gocamel.CamelMongoDbLimit, 10).
    To("mongodb://myconn?database=mydb&collection=items&operation=find")
```

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

Référence complète de tous les composants disponibles dans GoCamel. Les composants fournissent la connectivité aux
systèmes et services externes.

## Composants Core

### Direct

Routage synchrone en mémoire entre routes du même contexte.

```go
// Consumer: reçoit des messages du endpoint direct
// Producer: envoie des messages vers le endpoint direct
builder.From("direct:start").To("direct:process")
```

| Option         | Type | Défaut | Description                |
|----------------|------|--------|----------------------------|
| `reuseChannel` | bool | `true` | Réutiliser le même channel |

---

### Timer

Déclenchement périodique simple.

```go
builder.From("timer:tick?period=5s")
```

| Option        | Type     | Défaut  | Description                      |
|---------------|----------|---------|----------------------------------|
| `period`      | Duration | `1s`    | Période entre déclenchements     |
| `repeatCount` | int      | `0`     | Nombre de répétitions (0=infini) |
| `fixedRate`   | bool     | `false` | Mode fixed-rate vs fixed-delay   |

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

| Option    | Type   | Défaut  | Description                          |
|-----------|--------|---------|--------------------------------------|
| `delete`  | bool   | `false` | Supprimer après traitement           |
| `noop`    | bool   | `false` | Ne pas déplacer/supprimer le fichier |
| `include` | string | `""`    | Pattern fichiers inclus              |
| `exclude` | string | `""`    | Pattern fichiers exclus              |

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

### NATS

Messagerie asynchrone événementielle avec NATS. Éditeur et abonné haute performance avec le client Go NATS.

```go
ctx.AddComponent("nats", gocamel.NewNATSComponent())

// Consumer: abonnement asynchrone réactif
builder.From("nats://orders?servers=nats://localhost:4222").
    ProcessFunc(func(e *gocamel.Exchange) error {
        // Le payload NATS est reçu sous forme de slice d'octets ([]byte)
        payload := e.GetIn().GetBody().([]byte)
        fmt.Printf("Commande reçue: %s\n", string(payload))
        return nil
    })

// Producer: publication sur un sujet (subject)
builder.To("nats://orders?servers=nats://localhost:4222")

// Surcharger dynamiquement le sujet dans un header
exchange.In.SetHeader(gocamel.CamelNATSSubject, "urgent-orders")
```

> [!WARNING]
> Lors de l'écriture des URIs, utilisez toujours des doubles slashes (ex: `nats://orders`) plutôt que `nats:orders`, sinon `url.Parse` traite l'URI comme opaque, ce qui contamine le nom du sujet avec les paramètres de requête.

| Option | Type | Défaut | Description |
|---|---|---|---|
| `servers` | string | `nats://127.0.0.1:4222` | Adresses des serveurs NATS (séparées par des virgules) |
| `subject` | string | extrait du chemin | Sujet NATS auquel s'abonner / publier |

**Headers Exchange :**

- `CamelNATSSubject` - Sujet du message reçu ou sujet cible pour la publication.

---

### Redis

Envoi de commandes (SET, GET, DEL, PUBLISH) ou abonnement à des canaux Pub/Sub Redis.

```go
ctx.AddComponent("redis", gocamel.NewRedisComponent())

// Consumer: S'abonner à un canal Pub/Sub
builder.From("redis://localhost:6379?subscribe=true&channel=mychannel").
    Log("Message Pub/Sub reçu: ${body}")

// Producer: Exécuter des commandes
builder.To("redis://localhost:6379?command=SET&key=mykey")
```

**Commandes supportées :**

- `SET`: Enregistre le corps du message comme valeur de `key`.
- `GET`: Récupère la valeur de `key` et l'écrit dans `Out.Body`.
- `DEL`: Supprime la clé `key` et écrit le nombre de clés supprimées dans `Out.Body`.
- `PUBLISH`: Publique le corps de l'exchange sur le canal/clé spécifié.

**Headers Exchange (Surcharge des paramètres de l'URI) :**

- `CamelRedisCommand` - La commande Redis à exécuter (ex: `"GET"`).
- `CamelRedisKey` - La clé Redis cible.
- `CamelRedisChannel` - Le canal Pub/Sub cible pour la publication ou source pour l'abonnement.

| Option | Type | Défaut | Description |
|---|---|---|---|
| `command` | string | `SET` | Commande Redis pour le producteur |
| `key` | string | `""` | Clé Redis sur laquelle opérer |
| `channel` | string | `""` | Canal Pub/Sub Redis |
| `subscribe` | bool | `false` | Si vrai, agit en tant que consommateur Pub/Sub |

---

### Redis Idempotent Repository

Le Redis Idempotent Repository fournit une implémentation distribuée de l'interface `IdempotentRepository`, permettant à plusieurs instances de GoCamel dans un cluster de se coordonner pour éviter de traiter des messages en doublon.

```go
import (
    "time"
    "github.com/redis/go-redis/v9"
)

rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
// Préfixe de clé "idempotent:" avec un TTL d'expiration de 24 heures
repo := gocamel.NewRedisIdempotentRepository(rdb, "idempotent:", 24*time.Hour)

builder.From("direct:start").
    IdempotentConsumer("${header.MessageId}", repo).
    To("direct:process")
```

---

### Go Channel

Un composant d'intégration intra-processus natif et ultra-rapide utilisant des canaux (channels) Go natifs. Il permet de découpler de manière asynchrone des segments de route au sein d'un même processus.

```go
ctx.AddComponent("chan", gocamel.NewChanComponent())

// Consumer: traite les exchanges de manière asynchrone depuis le canal
builder.From("chan:orders?bufferSize=500").
    To("direct:process-order")

// Producer: écrit une copie de l'exchange de manière asynchrone dans le canal
builder.To("chan:orders")
```

| Option | Type | Défaut | Description |
|---|---|---|---|
| `bufferSize` | int | `100` | Capacité du canal bufferisé interne |

---

## Composants IA/LLM

### OpenAI

Intégration ChatGPT / GPT-4.

```go
ctx.AddComponent("openai", gocamel.NewOpenAIComponent())

endpoint, _ := ctx.CreateEndpoint("openai:chat?model=gpt-4")
producer, _ := endpoint.CreateProducer()
```

| Option  | Type   | Défaut          | Description              |
|---------|--------|-----------------|--------------------------|
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

| Option                   | Type   | Défaut  | Description                            |
|--------------------------|--------|---------|----------------------------------------|
| `cron`                   | string | —       | Expression cron 6 champs               |
| `trigger.repeatInterval` | int    | —       | Intervalle en millisecondes            |
| `trigger.repeatCount`    | int    | `-1`    | Max exécutions (-1=infini)             |
| `triggerStartDelay`      | int    | `500`   | Délai avant premier déclenchement (ms) |
| `stateful`               | bool   | `false` | Empêcher exécutions concurrentes       |

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

| Option        | Type   | Défaut         | Description                       |
|---------------|--------|----------------|-----------------------------------|
| `username`    | string | `""`           | Credentials d'authentification    |
| `password`    | string | `""`           | Credentials d'authentification    |
| `to`          | string | `""`           | Destinataire(s) (Producer)        |
| `subject`     | string | `""`           | Sujet du message (Producer)       |
| `contentType` | string | `"text/plain"` | Type MIME (Producer)              |
| `folderName`  | string | `"INBOX"`      | Dossier IMAP (Consumer)           |
| `unseen`      | bool   | `true`         | Messages non lus seulement        |
| `idle`        | bool   | `false`        | IMAP IDLE pour notifications push |
| `delete`      | bool   | `false`        | Supprimer après traitement        |
| `fetchSize`   | int    | `-1`           | Messages max par poll             |
| `pollDelay`   | int    | `60000`        | Délai entre polls (ms)            |

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

| Option                    | Type   | Défaut  | Description                                    |
|---------------------------|--------|---------|------------------------------------------------|
| `contentCache`            | bool   | `false` | Cache le template en mémoire                   |
| `allowTemplateFromHeader` | bool   | `false` | Permet override via header `CamelTemplatePath` |
| `startDelimiter`          | string | `{{`    | Délimiteur de début                            |
| `endDelimiter`            | string | `}}`    | Délimiteur de fin                              |

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

| Option          | Type   | Défaut       | Description                                                           |
|-----------------|--------|--------------|-----------------------------------------------------------------------|
| `query`         | string | obligatoire  | Requête SQL avec `${header.x}`, `${body}`, `${property.x}` optionnels |
| `dataSourceRef` | string | host URI     | Nom datasource enregistrée                                            |
| `outputType`    | string | `SelectList` | `SelectList` ou `SelectOne`                                           |
| `batch`         | bool   | `false`      | Mode batch: body = `[][]any`                                          |
| `transacted`    | bool   | `false`      | Englober dans une transaction                                         |

**Paramètres de Requête:**

Fournis via header `CamelSqlParameters` ou body comme `[]any`.

**Headers de Sortie:**

| Header                | Description                   |
|-----------------------|-------------------------------|
| `CamelSqlRowCount`    | Lignes retournées/affectées   |
| `CamelSqlColumnNames` | Noms des colonnes pour SELECT |

**Corps Résultat:**

| Cas                     | Type Out.Body               |
|-------------------------|-----------------------------|
| `SELECT` + `SelectList` | `[]map[string]any`          |
| `SELECT` + `SelectOne`  | `map[string]any` (ou `nil`) |
| `INSERT/UPDATE/DELETE`  | `int64` (lignes affectées)  |
| `batch=true`            | `int64` (total affectées)   |

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

| Option          | Type   | Défaut       | Description                   |
|-----------------|--------|--------------|-------------------------------|
| `procedure`     | string | obligatoire  | Nom procédure stockée         |
| `dataSourceRef` | string | host URI     | Nom datasource                |
| `outputType`    | string | `SelectList` | `SelectList` ou `SelectOne`   |
| `transacted`    | bool   | `false`      | Exécuter dans une transaction |
| `noop`          | bool   | `false`      | Mode test: ne pas exécuter    |

**Direction des Paramètres:**

| Direction             | Description      |
|-----------------------|------------------|
| `ParamDirectionIn`    | Entrée seule     |
| `ParamDirectionOut`   | Sortie seule     |
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
