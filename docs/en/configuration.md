# Configuration

## Overview

GoCamel supports multiple configuration strategies for credentials, endpoints, and component settings.

## Credential Sources

### Environment Variables (Recommended)

Set sensitive data via environment variables:

```bash
export FTP_USERNAME="myuser"
export FTP_PASSWORD="secret"
export TELEGRAM_AUTHORIZATIONTOKEN="bot-token"
export OPENAI_API_KEY="api-key"
```

Access in routes:

```go
builder.From("ftp://host?username=${env:FTP_USERNAME}")
```

### Query Parameters

Pass credentials as URI parameters:

```go
builder.From("ftp://host?username=admin&password=secret")
```

**Security Note:** Avoid hardcoding passwords. Use env vars.

### Direct in URI

```go
builder.From("ftp://user:pass@host:21/path")
```

## Component Registration

### Standard Components

```go
ctx.AddComponent("ftp", gocamel.NewFTPComponent())
ctx.AddComponent("http", gocamel.NewHTTPComponent())
ctx.AddComponent("timer", gocamel.NewTimerComponent())
ctx.AddComponent("direct", gocamel.NewDirectComponent())
```

### Scheduled Components

```go
ctx.AddComponent("cron", gocamel.NewCronComponent())
```

### Messaging Components

```go
ctx.AddComponent("telegram", gocamel.NewTelegramComponent())
```

### AI Components

```go
ctx.AddComponent("openai", gocamel.NewOpenAIComponent())
```

### Database Components

```go
import "database/sql"
import _ "github.com/mattn/go-sqlite3"

db, _ := sql.Open("sqlite3", "./app.db")

sqlComp := gocamel.NewSQLComponent()
sqlComp.RegisterDataSource("appdb", db)
ctx.AddComponent("sql", sqlComp)
```

## Data Source Configuration

### SQL Component

```go
// Multiple datasources
sqlComp.RegisterDataSource("primary", db1)
sqlComp.RegisterDataSource("secondary", db2)

// Default datasource
sqlComp.SetDefaultDataSource(db)
```

### SQL-Stored Component

```go
sqlStored := gocamel.NewSQLStoredComponent()
sqlStored.RegisterDataSource("mydb", db)
ctx.AddComponent("sql-stored", sqlStored)
```

## Route Configuration

### Setting Route ID

```go
route := context.CreateRouteBuilder().
    From("timer:tick").
    SetID("timer-route-1").
    To("direct:output").
    Build()
```

### Common Options

```go
// Polling delay
builder.From("file://input?delay=10s&delete=true")

// HTTP method
builder.To("http://api?httpMethod=POST")
```

## Environment Variable Mapping

| Component | Variable | Description |
|-----------|----------|-------------|
| FTP | `FTP_USERNAME`, `FTP_PASSWORD` | FTP credentials |
| SFTP | `SFTP_USERNAME`, `SFTP_PASSWORD`, `SFTP_PRIVATE_KEY_FILE` | SSH credentials |
| Telegram | `TELEGRAM_AUTHORIZATIONTOKEN` | Bot token |
| OpenAI | `OPENAI_AUTHORIZATIONTOKEN`, `OPENAI_API_KEY` | API keys |
| Mail | `MAIL_USERNAME`, `MAIL_PASSWORD` | Email credentials |

## Best Practices

1. **Never commit secrets** — Use env vars or secret managers
2. **Use different configs per environment** — Dev/staging/prod
3. **Document required env vars** — In README or deployment docs
4. **Validate config at startup** — Fail fast on missing config

