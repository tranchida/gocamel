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
| `preMove` | string | `""` | Move file before processing |
| `move` | string | `""` | Move file after processing |
| `moveFailed` | string | `""` | Move file on failure |

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

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `username` | string | `""` | FTP username |
| `password` | string | `""` | FTP password |
| `binary` | bool | `true` | Binary transfer mode |
| `passiveMode` | bool | `true` | Use passive mode |

---

### SFTP

Secure file transfer via SSH.

```go
builder.From("sftp://host:22/data?username=scott")
```

**Authentication Methods:**
- Password: via `password` parameter or `SFTP_PASSWORD` env var
- Key-based: via `privateKeyFile` parameter or `SFTP_PRIVATE_KEY_FILE`

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `username` | string | `""` | SSH username |
| `password` | string | `""` | SSH password |
| `privateKeyFile` | string | `""` | Path to private key |
| `privateKeyPassphrase` | string | `""` | Private key passphrase |

---

### SMB

Windows/Samba share access.

```go
builder.From("smb://server/share/folder?username=user")
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `username` | string | `""` | Domain username |
| `password` | string | `""` | Domain password |
| `share` | string | required | Share name |

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

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `httpMethod` | string | `GET` | HTTP method for producer |
| `bridgeEndpoint` | bool | `false` | Bridge consumer endpoint |

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

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `authorizationToken` | string | env var | Bot API token |

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

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `model` | string | `gpt-3.5-turbo` | Model to use |
| `authorizationToken` | string | env var | API key |

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

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `cron` | string | `""` | 6-field cron expression |
| `trigger.repeatInterval` | int | `""` | Interval in ms (simple trigger) |
| `trigger.repeatCount` | int | `-1` | Max repetitions |
| `triggerStartDelay` | int | `500` | Initial delay in ms |
| `stateful` | bool | `false` | Prevent concurrent execution |

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

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `username` | string | `""` | SMTP username |
| `password` | string | `""` | SMTP password |
| `to` | string | `""` | Recipient(s) |
| `subject` | string | `""` | Email subject |
| `contentType` | string | `"text/plain"` | MIME type |

---

### IMAP/IMAPS (Receive)

Receive emails via IMAP with IDLE support.

```go
builder.From("imaps://imap.gmail.com:993?folderName=INBOX&idle=true")
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `username` | string | `""` | IMAP username |
| `password` | string | `""` | IMAP password |
| `folderName` | string | `"INBOX"` | Folder to poll |
| `unseen` | bool | `true` | Only unread messages |
| `idle` | bool | `false` | Use IMAP IDLE mode |
| `delete` | bool | `false` | Delete after processing |
| `fetchSize` | int | `-1` | Messages per poll |
| `pollDelay` | int | `60000` | Poll interval (ms) |

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

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `query` | string | required | SQL query string |
| `dataSourceRef` | string | host path | Datasource name |
| `outputType` | string | `SelectList` | `SelectList` or `SelectOne` |
| `batch` | bool | `false` | Batch execution mode |
| `transacted` | bool | `false` | Wrap in transaction |

**Query Parameters:**

Provide via `CamelSqlParameters` header or body as `[]any`.

**Output Headers:**
- `CamelSqlRowCount` - Rows returned/affected
- `CamelSqlColumnNames` - Column names (SELECT)

**Result Body:**

| Case | Out.Body Type |
|------|---------------|
| `SELECT` + `SelectList` | `[]map[string]any` |
| `SELECT` + `SelectOne` | `map[string]any` or `nil` |
| `INSERT/UPDATE/DELETE` | `int64` (affected rows) |

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
| `dataSourceRef` | string | host path | Datasource name |
| `outputType` | string | `SelectList` | `SelectList` or `SelectOne` |
| `transacted` | bool | `false` | Execute in transaction |
| `noop` | bool | `false` | Test mode (no execution) |

**Parameter Directions:**

| Direction | Description |
|-----------|-------------|
| `ParamDirectionIn` | Input only |
| `ParamDirectionOut` | Output only |
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

## Transformation Components

### XSLT

XML transformation using XSL stylesheets.

```go
builder.To("xslt:file://transform.xsl")
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `transformerFactory` | string | `""` | Custom transformer class |

---

### XSD

XML Schema validation.

```go
builder.To("xsd:file://schema.xsd")
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
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

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `contentCache` | bool | `false` | Cache template in memory |
| `allowTemplateFromHeader` | bool | `false` | Allow `CamelTemplatePath` header override |
| `startDelimiter` | string | `{{` | Template start delimiter |
| `endDelimiter` | string | `}}` | Template end delimiter |

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

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `args` | string | `""` | Command arguments |
| `workingDir` | string | `""` | Working directory |
| `timeout` | int | `0` | Timeout in ms (0=no timeout) |

**Output:**
- `Out.Body` = command stdout
- Header `CamelExecExitCode` = exit code

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

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `delay` | Duration | varies | Poll interval |
| `include` | string | `""` | Include pattern |
| `exclude` | string | `""` | Exclude pattern |

---

## Component Summary Table

| Component | Category | Consumer | Producer | URI Pattern |
|-----------|----------|----------|----------|-------------|
| Direct | Core | ✅ | ✅ | `direct:name` |
| Timer | Core | ✅ | ❌ | `timer:name` |
| File | File | ✅ | ✅ | `file://path` |
| FTP | File | ✅ | ✅ | `ftp://host/path` |
| SFTP | File | ✅ | ✅ | `sftp://host/path` |
| SMB | File | ✅ | ✅ | `smb://host/share` |
| HTTP | Network | ✅ | ✅ | `http://host:port/path` |
| Telegram | Messaging | ✅ | ✅ | `telegram:bots` |
| OpenAI | AI | ❌ | ✅ | `openai:chat` |
| Cron | Scheduling | ✅ | ❌ | `cron://group/job` |
| SMTP | Mail | ❌ | ✅ | `smtp://host:port` |
| IMAP | Mail | ✅ | ❌ | `imap://host:port` |
| POP3 | Mail | ✅ | ❌ | `pop3://host:port` |
| SQL | Database | ❌ | ✅ | `sql://datasource` |
| SQL-Stored | Database | ❌ | ✅ | `sql-stored://datasource` |
| XSLT | Transform | ❌ | ✅ | `xslt:template` |
| XSD | Transform | ❌ | ✅ | `xsd:schema` |
| Template | Transform | ❌ | ✅ | `template:template` |
| Exec | Execution | ❌ | ✅ | `exec:command` |
