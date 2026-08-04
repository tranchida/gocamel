+++
title = "Configuration"
weight = 21
+++
## Overview

GoCamel supports multiple configuration strategies for credentials, endpoints, and component settings.

## Credential Sources

`gocamel.GetConfigValue(url, key)` is the single entry point that every
component uses to resolve a configuration value. It checks four sources in
the following order — the first non-empty result wins:

1. **Exact-name environment variable.** The key is used verbatim.
   Example: `GetConfigValue(_, "OPENAI_API_KEY")` reads
   `os.Getenv("OPENAI_API_KEY")`.
2. **Scheme-prefixed environment variable.** Built by uppercasing
   `scheme + "_" + key`. For an `ftp://` URI and key `password`, the lookup
   is `os.Getenv("FTP_PASSWORD")`.
3. **URI query parameter.** Example: `ftp://host?password=secret`.
4. **URI userinfo**, for the well-known keys `username`/`user` and
   `password`/`pass` only. Example: `ftp://user:pass@host`.

### Environment Variables (Recommended)

Set sensitive data via environment variables:

```bash
export FTP_USERNAME="myuser"
export FTP_PASSWORD="secret"
export TELEGRAM_AUTHORIZATIONTOKEN="bot-token"
export OPENAI_API_KEY="api-key"
```

Routes then reference the option directly — the env var is picked up
automatically:

```go
builder.From("ftp://host?username=myuser")  // FTP_PASSWORD comes from env
```

### Query Parameters

Pass options as URI query parameters:

```go
builder.From("ftp://host?username=admin&password=secret")
```

> [!caution] Credentials in URI queries are flagged at runtime
> URI strings routinely leak into connection-error logs, audit trails,
> crash reports and proxy access logs. When `GetConfigValue` resolves a
> credential-like key (`password`, `pass`, `secret`, `token`, `apikey`,
> `api_key`, `accesskey`, `access_key`, `clientsecret`, `privatekey`) from
> a query parameter, it emits a `WARN` log recommending env vars. The
> warning carries only the scheme and the key name — never the value — and
> is deduped per `(scheme, key)` pairing so a polling consumer cannot flood
> the logs.
>
> Use environment variables in production. The userinfo form
> (`user:pass@host`) is treated as the conventional URI way of carrying
> credentials and is **not** warned about — you opted into it explicitly.


### Direct in URI (userinfo form)

```go
builder.From("ftp://user:pass@host:21/path")
```

## Context Configuration

```go
ctx := gocamel.NewCamelContext()

// Custom configuration
cfg := ctx.Config()
```

## Security

**Never hardcode credentials:**

```go
// ❌ BAD
endpoint := "ftp://user:secret@host"

// ✅ GOOD
endpoint := "ftp://host?username=${env:FTP_USER}"
```

## Proxy

```go
os.Setenv("HTTP_PROXY", "http://proxy.company:8080")
os.Setenv("HTTPS_PROXY", "http://proxy.company:8080")
os.Setenv("NO_PROXY", "localhost,127.0.0.1")
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

| Component | Variable                                                  | Description       |
|-----------|-----------------------------------------------------------|-------------------|
| FTP       | `FTP_USERNAME`, `FTP_PASSWORD`                            | FTP credentials   |
| SFTP      | `SFTP_USERNAME`, `SFTP_PASSWORD`, `SFTP_PRIVATE_KEY_FILE` | SSH credentials   |
| Telegram  | `TELEGRAM_AUTHORIZATIONTOKEN`                             | Bot token         |
| OpenAI    | `OPENAI_AUTHORIZATIONTOKEN`, `OPENAI_API_KEY`             | API keys          |
| Mail      | `MAIL_USERNAME`, `MAIL_PASSWORD`                          | Email credentials |

## Best Practices

1. **Never commit secrets** — Use env vars or secret managers
2. **Use different configs per environment** — Dev/staging/prod
3. **Document required env vars** — In README or deployment docs
4. **Validate config at startup** — Fail fast on missing config