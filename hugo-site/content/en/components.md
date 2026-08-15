+++
title = "Components Reference"
weight = 12
+++
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
| `fileExist`  | string | `Override` | Behavior if destination file exists (`Override`, `Append`, `Fail`, `Ignore`) |

#### Headers

##### Set on Consumer Exchanges
- `CamelFileName`: The relative file name
- `CamelFilePath`: The full path of the file
- `CamelFileLength`: The file size in bytes
- `CamelFileLastModified`: Last modification timestamp (`time.Time`)

##### Consumed by Producer
- `CamelFileName`: Overrides destination file name when the producer URI points to a directory

#### Read lock

When watching a directory, the consumer waits for a newly created file to
stop changing before reading it. `fsnotify` signals `Create` as soon as the
inode exists — long before a writer has finished — so reading immediately
delivered a truncated (often empty) body for anything not written atomically:
a shell redirect, a copy, an upload.

The consumer samples size and mtime every 100 ms and reads once two
consecutive samples match, giving up after 10 seconds (the file is skipped
and a warning is logged). This is the equivalent of Camel's
`readLock=changed`.

> [!tip] Prefer an atomic handoff
> The read lock is a heuristic: a writer that stalls for longer than the
> sampling interval mid-write can still be observed as stable. For a
> guaranteed handoff, have the producer write to a temporary name and
> `rename` it into the watched directory — rename is atomic.


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

| Option           | Type   | Default | Description                                        |
|------------------|--------|---------|----------------------------------------------------|
| `username`       | string | `""`    | FTP username                                       |
| `password`       | string | `""`    | FTP password                                       |
| `binary`         | bool   | `true`  | Binary transfer mode                               |
| `passiveMode`    | bool   | `true`  | Use passive mode                                   |
| `maxMessageSize` | int    | `0`     | Max bytes a polled file may hold (0 = unlimited)   |

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
| `maxMessageSize`       | int    | `0`     | Max bytes a polled file may hold (0 = unlimited) |

---

### SMB

Windows/Samba share access.

```go
builder.From("smb://server/share/folder?username=user")
```

| Option           | Type   | Default  | Description                                        |
|------------------|--------|----------|----------------------------------------------------|
| `username`       | string | `""`     | Domain username                                    |
| `password`       | string | `""`     | Domain password                                    |
| `share`          | string | required | Share name                                         |
| `maxMessageSize` | int    | `0`      | Max bytes a polled file may hold (0 = unlimited)   |

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

#### Response headers

The response body is taken from `Out` (or `In` when no `Out` was set, per Camel
InOut semantics). `[]byte` and `string` bodies are written as-is; any other
non-nil body is written using its `%v` string representation.

Because the consumer copies every inbound request header onto the exchange, a
route that does not set `Out` would answer with the request's own headers. Two
filters prevent that:

- **Unchanged inbound headers are not echoed.** A header that comes back
  identical to the one received is dropped, so `Cookie` and `Authorization`
  are never reflected to the client. A header the route sets or modifies is
  always sent.
- **Hop-by-hop and framing headers are never emitted** (`Connection`,
  `Keep-Alive`, `Transfer-Encoding`, `Content-Length`, `Host`, ...). Echoing
  the request's `Content-Length` corrupted the response framing.

The same hop-by-hop filter applies to the producer, so a `From(http://...)`
→ `To(http://...)` bridge does not forward the inbound `Content-Length` or
`Host` to the upstream.

The producer forwards every other inbound header by default, including
`Cookie` and `Authorization` — the normal behaviour for a generic HTTP client
that calls an authenticated upstream. In a proxy/bridge scenario where that
would leak a downstream client's credentials to a third-party upstream, strip
them explicitly:

```go
http := gocamel.NewHTTPComponent()
http.SetSkipAuthorizationHeaders(true)
```

When enabled, `Authorization` and `Cookie` are dropped from every outbound
request produced by this component.

#### Response status

A route sets the status code with the `CamelHttpResponseCode` header on the
response message (the legacy `Status-Code` header is still honoured). Values
outside 100–599 are ignored:

```go
builder.From("http://localhost:8080/api").
    ProcessFunc(func(e *gocamel.Exchange) error {
        e.GetOut().SetHeader(gocamel.CamelHttpResponseCode, 201)
        e.GetOut().SetHeader("Content-Type", "application/json")
        e.GetOut().SetBody(`{"created":true}`)
        return nil
    })
```

#### Producer error handling

An upstream response with status **≥ 300 is an error** (the Apache Camel
default). The error wraps `gocamel.ErrHTTPStatus`, and the response body and
headers are still published on `Out` so the route can inspect them:

```go
if errors.Is(err, gocamel.ErrHTTPStatus) {
    code, _ := exchange.GetOut().GetHeader(gocamel.CamelHttpResponseCode)
    // code is an int; the body holds the upstream error payload
}
```

To treat every response as a success and branch on the status yourself:

```go
http := gocamel.NewHTTPComponent()
http.SetThrowExceptionOnFailure(false)
```

The producer also sets `CamelHttpResponseCode` (int) and
`CamelHttpResponseText` (e.g. `"500 Internal Server Error"`) on `Out`.

#### Hardening

- The embedded server sets a 10-second `ReadHeaderTimeout` (slow header
  attacks), a 60-second `ReadTimeout` (slow body attacks) and a 60-second
  `IdleTimeout` (idle keep-alive connections).
- Request and response bodies are read through a size limit —
  `DefaultMaxBodySize` (32 MiB) — so a single oversized payload cannot exhaust
  process memory (CWE-400). An oversized request is answered with
  `413 Request Entity Too Large`. Adjust or disable it per component:

    ```go
    http := gocamel.NewHTTPComponent()
    http.SetMaxBodySize(256 << 20) // 256 MiB; <= 0 disables the limit
    ```

- Route errors are **not** returned to the client: the handler answers with a
  generic `500 internal server error` and logs the detail, which may carry SQL
  text, file paths or driver messages.

#### Shutdown

The consumer goroutine is tracked by an internal `WaitGroup` and `Stop()`
waits for `http.Server.Shutdown` to complete (bounded by a 30-second
internal deadline) before returning. `Stop()` is idempotent.

---

### Net (TCP/UDP)

Raw TCP and UDP sockets, built on the standard library — the Go equivalent of
Apache Camel's `netty`/`mina` components. The consumer listens and feeds
inbound messages into the route; the producer dials and sends the message
body.

```go
// Consumer (server): request-reply over TCP
builder.From("net:tcp://localhost:9090")

// Consumer (server): fire-and-forget UDP
builder.From("net:udp://localhost:9090?sync=false")

// Producer (client)
builder.To("net:tcp://localhost:9090")
```

| Option       | Type   | Default  | Description                                    |
|--------------|--------|----------|------------------------------------------------|
| `sync`       | bool   | `true`   | Request-reply: the consumer writes the route's response back to the peer; the producer waits for a reply |
| `textline`   | bool   | `true`   | Newline-delimited messages (TCP only). `false` = one message per read |
| `bufferSize` | int    | `8192`   | Maximum message/datagram size in bytes         |
| `timeout`    | int    | `30000`  | Producer dial/reply timeout in ms (0 = none)   |
| `keepAlive`  | bool   | `false`  | TCP keepalive on connections                   |
| `readTimeout`| int    | `60000`  | Consumer read deadline in ms (0 = none)        |

**URI forms** — both `net:tcp://host:port` and `net:tcp:host:port` are
accepted; the same applies to `udp`. An omitted host defaults to `localhost`.
Port `0` asks the OS for an ephemeral port: read the actual bound address
from `NetConsumer.Address()`.

**Framing.** TCP is a byte stream, so the component needs a message boundary.
With `textline=true` (default) each newline-terminated line is one message
(the trailing `\r\n` or `\n` is stripped). With `textline=false` each read
is one message. UDP datagrams need no framing: each datagram is one message.

```go
// Request-reply echo server
builder.From("net:tcp://localhost:9090").
    ProcessFunc(func(e *gocamel.Exchange) error {
        body, _ := e.GetIn().GetBody().([]byte)
        e.GetOut().SetBody([]byte("echo: " + string(body)))
        return nil
    })
```

**Headers.** The consumer sets `CamelNetRemoteAddress` and
`CamelNetLocalAddress` (host:port) on the In message.

#### Hardening

- Message reads are bounded by `bufferSize`. A textline message longer than
  the limit is a protocol error: the connection is dropped rather than
  buffering unbounded data (CWE-400). UDP datagrams larger than the limit
  are truncated.
- The producer's reply read in sync mode is bounded by `bufferSize` and by
  the `timeout` read deadline, so a silent peer cannot hang the route
  forever.
- The consumer sets a `readTimeout` (default 60 s) on each connection so a
  slowloris-style client cannot pin a goroutine open indefinitely; set it to
  `0` to disable.
- Panics in route processors are contained (`ProcessSafely`): a misbehaving
  route fails its exchange, never the consumer.

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

| Option           | Type   | Default   | Description             |
|------------------|--------|-----------|-------------------------|
| `username`       | string | `""`      | IMAP username           |
| `password`       | string | `""`      | IMAP password           |
| `folderName`     | string | `"INBOX"` | Folder to poll          |
| `unseen`         | bool   | `true`    | Only unread messages    |
| `idle`           | bool   | `false`   | Use IMAP IDLE mode      |
| `delete`         | bool   | `false`   | Delete after processing |
| `fetchSize`      | int    | `-1`      | Messages per poll       |
| `pollDelay`      | int    | `60000`   | Poll interval (ms)      |
| `maxMessageSize` | int    | `0`       | Max message size in bytes (0 = unlimited) |

---

### POP3/POP3S (Receive)

Receive emails via POP3.

```go
builder.From("pop3s://pop.gmail.com:995?username=user&password=pass")
```

The POP3 consumer honours the same `maxMessageSize` option as IMAP (default
`0` = unlimited) to bound the size of a downloaded message.

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

| Option                | Type   | Default      | Description                                    |
|-----------------------|--------|--------------|------------------------------------------------|
| `query`               | string | required     | SQL query string                               |
| `dataSourceRef`       | string | host path    | Datasource name                                |
| `outputType`          | string | `SelectList` | `SelectList` or `SelectOne`                    |
| `batch`               | bool   | `false`      | Batch execution mode                           |
| `transacted`          | bool   | `false`      | Wrap in transaction                            |
| `allowHeaderOverride` | bool   | `false`      | Allow `CamelSqlQuery` to replace the statement |

**Query Parameters:**

Provide via `CamelSqlParameters` header or body as `[]any`.

> [!caution] The query is trusted configuration
> The statement is **never** interpolated with Exchange data — substituting
> `${header.X}` into the SQL string would defeat parameterised queries and
> enable SQL injection. Always bind dynamic values through
> `CamelSqlParameters` (positional `?` placeholders) or a `[]any` body.
>
> The `CamelSqlQuery` header replaces the statement **wholesale**, so since
> v0.2 it is ignored unless the endpoint opts in with
> `allowHeaderOverride=true`. Enable it only where headers cannot be
> influenced by untrusted input: any component that maps external metadata
> onto headers would otherwise hand a caller full control of the statement.
>
> ```go
> // Statement fixed by configuration (default, recommended)
> builder.To("sql://appdb?query=SELECT+*+FROM+users+WHERE+id=?")
>
> // Statement supplied per message — only with trusted headers
> builder.To("sql://appdb?allowHeaderOverride=true&query=SELECT+1")
> ```


**Output Headers:**

- `CamelSqlRowCount` - Rows returned/affected
- `CamelSqlColumnNames` - Column names (SELECT)

**Result Body:**

| Case                    | Out.Body Type             |
|-------------------------|---------------------------|
| `SELECT` + `SelectList` | `[]map[string]any`        |
| `SELECT` + `SelectOne`  | `map[string]any` or `nil` |
| `INSERT/UPDATE/DELETE`  | `int64` (affected rows)   |

**Statement classification:**

Whether a statement is run as a query (result set) or an update (affected
rows) is decided from its leading keyword, ignoring leading comments and
parentheses. `SELECT`, `WITH` (CTE), `VALUES`, `TABLE`, `SHOW`, `EXPLAIN`,
`DESCRIBE` and `PRAGMA` return rows, as does any statement carrying a
`RETURNING` clause:

```sql
WITH recent AS (SELECT * FROM orders WHERE ts > ?)
SELECT id, total FROM recent          -- result set
INSERT INTO users(name) VALUES (?) RETURNING id   -- result set
```

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

| Option                | Type   | Default      | Description                                            |
|-----------------------|--------|--------------|--------------------------------------------------------|
| `procedure`           | string | required     | Stored procedure name                                  |
| `dataSourceRef`       | string | host path    | Datasource name                                        |
| `outputType`          | string | `SelectList` | `SelectList` or `SelectOne`                            |
| `transacted`          | bool   | `false`      | Execute in transaction                                 |
| `noop`                | bool   | `false`      | Test mode (no execution)                               |
| `allowHeaderOverride` | bool   | `false`      | Allow `CamelSqlStoredProcedureName` to replace the name |

> [!caution] Procedure name is a trusted identifier
> The procedure name is spliced into the `CALL` statement and is **never**
> interpolated with exchange data. The `CamelSqlStoredProcedureName` header is
> ignored unless the endpoint opts in with `allowHeaderOverride=true`; when it
> is enabled, the header value is validated against a strict identifier pattern
> (`[A-Za-z_][A-Za-z0-9_.]*`). Only enable the override where headers cannot be
> influenced by untrusted input.

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

> [!caution] Security
> Header overrides let a message choose **which binary is executed**. They are
> disabled by default; only enable `allowHeaderOverride=true` when message
> headers cannot be influenced by untrusted input (e.g. headers coming from an
> HTTP or mail consumer).
>
> The **executable** and the **working directory** are validated against shell
> metacharacters and path traversal. A header-supplied executable must
> additionally be a **bare command name** (no path) — it is resolved via `PATH`,
> never an absolute path — so an attacker cannot smuggle `/bin/sh` plus
> arbitrary arguments. **Arguments are not validated**: commands run
> through `execve` without a shell, so `|`, `&`, `$`, `<` and `>` carry no
> special meaning and reach the child process verbatim — which is the intent.
> Rejecting them blocked legitimate values (a JSON document, a password
> containing `$`, a relative path) without preventing any injection. NUL and
> other control characters are still rejected.


**Output:**

- `Out.Body` = command stdout (also mirrored on `In` for backward
  compatibility, so an InOut route sees the command output)
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
| Net        | Network    | ✅        | ✅        | `net:tcp://host:port`     |
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
