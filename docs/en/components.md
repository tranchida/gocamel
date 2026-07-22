# Components Reference

## Overview

Complete reference of all available GoCamel components. Components provide connectivity to various systems and services.

## Core Components

### Direct

In-memory synchronous routing between routes in the same context.

```go
// Consumer: receives from direct endpoint
// Producer: sends to direct endpoint
builder.From("direct:start").To("direct:process")
```

Endpoints are identified by **name only** — query parameters are ignored for identity, so
`direct:start` and `direct:start?x=1` resolve to the same endpoint. Only one consumer per
endpoint is allowed.

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

| Option       | Type   | Default | Description                 |
|--------------|--------|---------|-----------------------------|
| `delete`     | bool   | `false` | Delete after processing     |
| `noop`       | bool   | `false` | Do not move/delete file     |
| `include`    | string | `""`    | Include file pattern        |
| `exclude`    | string | `""`    | Exclude file pattern        |
| `preMove`    | string | `""`    | Move file before processing |
| `move`       | string | `""`    | Move file after processing  |
| `moveFailed` | string | `""`    | Move file on failure        |

---

### FTP / FTPS

File transfer via FTP protocol.

```go
// Consumer
builder.From("ftp://host:21/incoming?username=admin")

// Producer
builder.To("ftp://host:21/outgoing?binary=true")
```

**Environment Variables:**

- `FTP_USERNAME` - Username
- `FTP_PASSWORD` - Password

| Option        | Type   | Default | Description          |
|---------------|--------|---------|----------------------|
| `username`    | string | `""`    | FTP username         |
| `password`    | string | `""`    | FTP password         |
| `binary`      | bool   | `true`  | Binary transfer mode |
| `passiveMode` | bool   | `true`  | Use passive mode     |

---

### SFTP

Secure file transfer via SSH.

```go
builder.From("sftp://host:22/data?username=scott")
```

**Authentication Methods:**

- Password: via `password` parameter or `SFTP_PASSWORD` env var
- Key-based: via `privateKeyFile` parameter or `SFTP_PRIVATE_KEY_FILE`

| Option                 | Type   | Default | Description            |
|------------------------|--------|---------|------------------------|
| `username`             | string | `""`    | SSH username           |
| `password`             | string | `""`    | SSH password           |
| `privateKeyFile`       | string | `""`    | Path to private key    |
| `privateKeyPassphrase` | string | `""`    | Private key passphrase |

---

### SMB

Windows/Samba share access.

```go
builder.From("smb://server/share/folder?username=user")
```

| Option     | Type   | Default  | Description     |
|------------|--------|----------|-----------------|
| `username` | string | `""`     | Domain username |
| `password` | string | `""`     | Domain password |
| `share`    | string | required | Share name      |

---

## Network Components

### HTTP

HTTP server and client support.

```go
// Consumer (HTTP server)
builder.From("http://localhost:8080/api")

// Producer (HTTP client)
builder.To("http://example.com/webhook")

// With options
builder.To("http://api.example.com/data?httpMethod=POST")
```

| Option           | Type   | Default | Description              |
|------------------|--------|---------|--------------------------|
| `httpMethod`     | string | `GET`   | HTTP method for producer |
| `bridgeEndpoint` | bool   | `false` | Bridge consumer endpoint |

#### Authentication, TLS, rate limiting (consumer middleware)

The HTTP consumer ships with **no authentication**. Expose it to an untrusted
network only behind a middleware that enforces the security model you need
(token check, mTLS, OAuth, IP allowlist, rate limit, ...). Install the
middleware on the `HTTPComponent` before the route starts:

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

The middleware wraps every handler installed by an `http://` consumer in this
context.

#### Outbound header sanitisation

Both the HTTP producer (`To`) and the consumer response path reject header
names and values containing `\r` or `\n` (CRLF) to prevent HTTP response
splitting (CWE-113). An exchange that tries to set such a header gets an
error back from `Send()`.

#### Hardening & responses

- The embedded server sets a 10-second `ReadHeaderTimeout` to mitigate
  slowloris-style attacks.
- The response body is taken from `Out` (or `In` when no `Out` was set, per
  Camel InOut semantics). `[]byte` and `string` bodies are written as-is;
  any other non-nil body is written using its `%v` string representation.

#### Shutdown

The consumer goroutine is tracked by an internal `WaitGroup` and `Stop()`
waits for `http.Server.Shutdown` to complete (bounded by a 30-second
internal deadline) before returning. `Stop()` is idempotent.

---

## Messaging Components

### Telegram

Telegram Bot API integration for receiving and sending messages.

```go
// Required: Set TELEGRAM_AUTHORIZATIONTOKEN env variable
ctx.AddComponent("telegram", gocamel.NewTelegramComponent())

// Consumer (webhook/polling mode)
builder.From("telegram:bots").Log("${body}")

// Producer (send messages)
builder.To("telegram:bots")
```

**Environment Variables:**

- `TELEGRAM_AUTHORIZATIONTOKEN` - Bot API token

| Option               | Type   | Default | Description   |
|----------------------|--------|---------|---------------|
| `authorizationToken` | string | env var | Bot API token |

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

**Notes:**

- Endpoints are cached **by channel name**: every producer and consumer using the same
  name shares one endpoint, and only **one consumer** per channel is allowed (a second
  `From("chan:orders")` fails at startup).
- URI options are applied when the endpoint is **first created**; later references with
  different options log a warning and reuse the existing endpoint.

---

## AI Components

### OpenAI

OpenAI API integration for ChatGPT/GPT-4.

```go
ctx.AddComponent("openai", gocamel.NewOpenAIComponent())

// Producer only - send chat completion request
endpoint, _ := ctx.CreateEndpoint("openai:chat?model=gpt-4")
producer, _ := endpoint.CreateProducer()

exchange := gocamel.NewExchange(context.Background())
exchange.GetIn().SetBody("Hello, how are you?")
producer.Send(exchange)

fmt.Println(exchange.GetOut().GetBody()) // AI response
```

**Environment Variables:**

- `OPENAI_AUTHORIZATIONTOKEN` or `OPENAI_API_KEY` - API key

| Option               | Type   | Default         | Description  |
|----------------------|--------|-----------------|--------------|
| `model`              | string | `gpt-3.5-turbo` | Model to use |
| `authorizationToken` | string | env var         | API key      |

---

## Scheduling Components

### Cron

Advanced scheduling with cron expressions or simple intervals.

```go
ctx.AddComponent("cron", gocamel.NewCronComponent())

// Cron trigger (6 fields, including seconds)
builder.From("cron://group/job?cron=0+*+*+*+*+*")

// Simple interval trigger
builder.From("cron://poller?trigger.repeatInterval=5000")
```

**Cron Expression Format (6 fields):**

```
second minute hour day month dayOfWeek
0 * * * * *  # Every minute
0 */5 * * * * # Every 5 minutes
cron=0 0 12 * * *  # Every day at noon
```

| Option                   | Type   | Default | Description                     |
|--------------------------|--------|---------|---------------------------------|
| `cron`                   | string | `""`    | 6-field cron expression         |
| `trigger.repeatInterval` | int    | `""`    | Interval in ms (simple trigger) |
| `trigger.repeatCount`    | int    | `-1`    | Max repetitions                 |
| `triggerStartDelay`      | int    | `500`   | Initial delay in ms             |
| `stateful`               | bool   | `false` | Prevent concurrent execution    |

**Exchange Headers:**

- `fireTime` - Trigger execution time
- `nextFireTime` - Next scheduled time
- `triggerName` - Trigger identifier

---

## Mail Components

### SMTP/SMTPS (Send)

Send emails via SMTP.

```go
builder.To("smtps://smtp.gmail.com:465?to=recipient@example.com&subject=Hello")
```

| Option        | Type   | Default        | Description   |
|---------------|--------|----------------|---------------|
| `username`    | string | `""`           | SMTP username |
| `password`    | string | `""`           | SMTP password |
| `to`          | string | `""`           | Recipient(s)  |
| `subject`     | string | `""`           | Email subject |
| `contentType` | string | `"text/plain"` | MIME type     |

---

### IMAP/IMAPS (Receive)

Receive emails via IMAP with IDLE support.

```go
builder.From("imaps://imap.gmail.com:993?folderName=INBOX&idle=true")
```

| Option       | Type   | Default   | Description             |
|--------------|--------|-----------|-------------------------|
| `username`   | string | `""`      | IMAP username           |
| `password`   | string | `""`      | IMAP password           |
| `folderName` | string | `"INBOX"` | Folder to poll          |
| `unseen`     | bool   | `true`    | Only unread messages    |
| `idle`       | bool   | `false`   | Use IMAP IDLE mode      |
| `delete`     | bool   | `false`   | Delete after processing |
| `fetchSize`  | int    | `-1`      | Messages per poll       |
| `pollDelay`  | int    | `60000`   | Poll interval (ms)      |

---

### POP3/POP3S (Receive)

Receive emails via POP3.

```go
builder.From("pop3s://pop.gmail.com:995?username=user&password=pass")
```

---

## Database Components

### SQL

SQL query execution via `database/sql`.

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

// SELECT single row -> Out.Body = map[string]any
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

| Option          | Type   | Default      | Description                 |
|-----------------|--------|--------------|-----------------------------|
| `query`         | string | required     | SQL query string            |
| `dataSourceRef` | string | host path    | Datasource name             |
| `outputType`    | string | `SelectList` | `SelectList` or `SelectOne` |
| `batch`         | bool   | `false`      | Batch execution mode        |
| `transacted`    | bool   | `false`      | Wrap in transaction         |

**Query Parameters:**

Provide via `CamelSqlParameters` header or body as `[]any`.

**Output Headers:**

- `CamelSqlRowCount` - Rows returned/affected
- `CamelSqlColumnNames` - Column names (SELECT)

**Result Body:**

| Case                    | Out.Body Type             |
|-------------------------|---------------------------|
| `SELECT` + `SelectList` | `[]map[string]any`        |
| `SELECT` + `SelectOne`  | `map[string]any` or `nil` |
| `INSERT/UPDATE/DELETE`  | `int64` (affected rows)   |

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
| `dataSourceRef` | string | host path    | Datasource name             |
| `outputType`    | string | `SelectList` | `SelectList` or `SelectOne` |
| `transacted`    | bool   | `false`      | Execute in transaction      |
| `noop`          | bool   | `false`      | Test mode (no execution)    |

**Parameter Directions:**

| Direction             | Description           |
|-----------------------|-----------------------|
| `ParamDirectionIn`    | Input only            |
| `ParamDirectionOut`   | Output only           |
| `ParamDirectionInOut` | Both input and output |

Example:

```go
params := []gocamel.StoredProcedureParam{
    {Name: "inParam", Direction: gocamel.ParamDirectionIn, Value: "input"},
    {Name: "outParam", Direction: gocamel.ParamDirectionOut},
    {Name: "inOutParam", Direction: gocamel.ParamDirectionInOut, Value: 123},
}
```

---

### MongoDB

MongoDB integration for CRUD operations. Producer-only.

#### URI Format

```
mongodb://connectionName?database=mydb&collection=mycoll&operation=find
```

#### Options

| Option          | Type   | Required | Description                                                              |
|-----------------|--------|----------|--------------------------------------------------------------------------|
| `database`      | string | Yes      | Database name                                                            |
| `collection`    | string | Yes      | Collection name                                                          |
| `operation`     | string | Yes      | Operation: find, findOne, insert, insertOne, save, update, remove, count |
| `connectionRef` | string | No       | Registered connection reference                                          |
| `allowDeleteAll` | bool   | No       | Allow `remove` with an empty filter (default: `false`)                    |

For safety, `remove` rejects nil and empty filters. Set `allowDeleteAll=true`
explicitly only when deleting the entire collection is intentional.

#### Input Headers

| Header                   | Mode  | Description                              |
|--------------------------|-------|------------------------------------------|
| `CamelMongoDbDatabase`   | R/W   | Database name                            |
| `CamelMongoDbCollection` | R/W   | Collection name                          |
| `CamelMongoDbOperation`  | R/W   | Operation to execute                     |
| `CamelMongoDbCriteria`   | Write | Filter/criteria (map[string]any or JSON) |
| `CamelMongoDbLimit`      | Write | Result limit                             |
| `CamelMongoDbSkip`       | Write | Skip N documents                         |
| `CamelMongoDbSort`       | Write | Sort order (json: {"field": 1})          |

#### Output Headers

| Header                    | Description                    |
|---------------------------|--------------------------------|
| `CamelMongoDbResultTotal` | Total documents found/affected |
| `CamelMongoDbOid`         | ObjectID of inserted document  |

#### Example

```go
import (
    "context"
    "go.mongodb.org/mongo-driver/mongo"
    "go.mongodb.org/mongo-driver/mongo/options"
)

// Create component
mongoComp := gocamel.NewMongoDBComponent()

// Connect
client, _ := mongo.Connect(context.Background(), options.Client().ApplyURI("mongodb://localhost:27017"))
conn := gocamel.CreateMongoDBConnection(client, "mongodb://localhost:27017", "mydb")
mongoComp.RegisterConnection("myconn", conn)

builder.WithComponent("mongodb", mongoComp)

// Insert
builder.From("timer:tick?period=5s").
    SetBody(map[string]any{"name": "test", "value": 123}).
    To("mongodb://myconn?database=mydb&collection=items&operation=insert")

// Query with filter
builder.From("direct:search").
    SetHeader(gocamel.CamelMongoDbCriteria, map[string]any{"status": "active"}).
    SetHeader(gocamel.CamelMongoDbLimit, 10).
    To("mongodb://myconn?database=mydb&collection=items&operation=find")
```

---

## Transformation Components

### XSLT

XML transformation using XSL stylesheets.

```go
builder.To("xslt:file://transform.xsl")
```

| Option               | Type   | Default | Description              |
|----------------------|--------|---------|--------------------------|
| `transformerFactory` | string | `""`    | Custom transformer class |

---

### XSD

XML Schema validation.

```go
builder.To("xsd:file://schema.xsd")
```

| Option           | Type   | Default  | Description     |
|------------------|--------|----------|-----------------|
| `schemaResource` | string | required | XSD schema path |

---

### Template

Go template processing (inspired by Apache Camel Velocity).

```go
// Basic template
builder.To("template:templates/email.tmpl")

// With caching
builder.To("template:templates/item.tmpl?contentCache=true")

// Dynamic template from header
builder.To("template:default.tmpl?allowTemplateFromHeader=true")
```

| Option                    | Type   | Default | Description                               |
|---------------------------|--------|---------|-------------------------------------------|
| `contentCache`            | bool   | `false` | Cache template in memory                  |
| `allowTemplateFromHeader` | bool   | `false` | Allow `CamelTemplatePath` header override |
| `startDelimiter`          | string | `{{`    | Template start delimiter                  |
| `endDelimiter`            | string | `}}`    | Template end delimiter                    |

**Template Variables:**

```
{{.Body}}              # Message body
{{.Headers.name}}      # Header value
{{.Exchange.ID}}         # Exchange ID
{{.Exchange.Created}}    # Creation timestamp
```

**Template Functions:**

```go
{{.Body | upper}}
{{.Body | lower}}
{{.Body | trim}}
{{now | formatDate "2006-01-02 15:04:05"}}
{{"hello" | contains "ell"}}
```

---

## Execution Components

### Exec

Execute system commands.

```go
builder.To("exec:ls -la")
```

| Option                | Type   | Default | Description                                          |
|-----------------------|--------|---------|------------------------------------------------------|
| `args`                | string | `""`    | Command arguments                                    |
| `workingDir`          | string | `""`    | Working directory                                    |
| `timeout`             | int    | `0`     | Timeout in ms (0=no timeout)                         |
| `outFile`             | string | `""`    | Read the result from this file instead of stdout     |
| `useStderrOnEmpty`    | bool   | `false` | Use stderr as body when stdout is empty              |
| `allowHeaderOverride` | bool   | `false` | Allow `CamelExecCommand*` headers to override the URI |

**Per-message overrides (opt-in):**

When `allowHeaderOverride=true`, these headers override the URI settings for a single message:

| Header                       | Overrides         |
|------------------------------|-------------------|
| `CamelExecCommandExecutable` | Executable        |
| `CamelExecCommandArgs`       | Arguments         |
| `CamelExecCommandWorkingDir` | Working directory |
| `CamelExecCommandTimeout`    | Timeout (ms)      |

!!! danger "Security"
    Header overrides let a message choose **which binary is executed**. They are
    disabled by default; only enable `allowHeaderOverride=true` when message
    headers cannot be influenced by untrusted input (e.g. headers coming from an
    HTTP or mail consumer). Executables, arguments, and working directories are
    always validated against shell metacharacters and path traversal.

**Output:**

- `Out.Body` = command stdout
- Header `CamelExecExitValue` = exit code
- Headers `CamelExecStdout` / `CamelExecStderr` = raw command output

---

## Component Configuration

### Authentication

Credentials can be provided via environment variables:

```go
// FTP with env var
builder.From("ftp://host?username=${env:FTP_USER}")

// Or parameter  
builder.From("ftp://host?username=admin&password=${env:FTP_PASS}")
```

### Common Options

Many components share common polling options:

```go
// File polling
builder.From("file://data?delay=10s&delete=true")

// FTP polling
builder.From("ftp://host/incoming?delay=30s&include=*.xml")
```

| Option    | Type     | Default | Description     |
|-----------|----------|---------|-----------------|
| `delay`   | Duration | varies  | Poll interval   |
| `include` | string   | `""`    | Include pattern |
| `exclude` | string   | `""`    | Exclude pattern |

---

## Component Summary Table

| Component  | Category   | Consumer | Producer | URI Pattern               |
|------------|------------|----------|----------|---------------------------|
| Direct     | Core       | ✅        | ✅        | `direct:name`             |
| Timer      | Core       | ✅        | ❌        | `timer:name`              |
| File       | File       | ✅        | ✅        | `file://path`             |
| FTP        | File       | ✅        | ✅        | `ftp://host/path`         |
| SFTP       | File       | ✅        | ✅        | `sftp://host/path`        |
| SMB        | File       | ✅        | ✅        | `smb://host/share`        |
| HTTP       | Network    | ✅        | ✅        | `http://host:port/path`   |
| Telegram   | Messaging  | ✅        | ✅        | `telegram:bots`           |
| OpenAI     | AI         | ❌        | ✅        | `openai:chat`             |
| Cron       | Scheduling | ✅        | ❌        | `cron://group/job`        |
| SMTP       | Mail       | ❌        | ✅        | `smtp://host:port`        |
| IMAP       | Mail       | ✅        | ❌        | `imap://host:port`        |
| POP3       | Mail       | ✅        | ❌        | `pop3://host:port`        |
| SQL        | Database   | ❌        | ✅        | `sql://datasource`        |
| SQL-Stored | Database   | ❌        | ✅        | `sql-stored://datasource` |
| MongoDB    | Database   | ❌        | ✅        | `mongodb:connectionName`  |
| XSLT       | Transform  | ❌        | ✅        | `xslt:template`           |
| XSD        | Transform  | ❌        | ✅        | `xsd:schema`              |
| Template   | Transform  | ❌        | ✅        | `template:template`       |
| Exec       | Execution  | ❌        | ✅        | `exec:command`            |
| NATS       | Messaging  | ✅        | ✅        | `nats://subject`          |
| Redis      | Messaging  | ✅        | ✅        | `redis://host:port`       |
| Go Channel | Core       | ✅        | ✅        | `chan:channelName`        |
