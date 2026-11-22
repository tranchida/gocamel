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
> crash reports and proxy access logs. When `GetConfigValue` resolves any of the
> 18 unified credential keys (`password`, `pass`, `passwd`, `secret`, `token`,
> `passcode`, `apikey`, `api_key`, `apitoken`, `authorizationtoken`,
> `accesstoken`, `access_token`, `accesskey`, `access_key`, `clientsecret`,
> `client_secret`, `privatekey`, `private_key`) from
> a query parameter, it emits a `WARN` log recommending env vars (and `RedactURI`
> masks all 18 keys across errors and logs). The
> warning carries only the scheme and the key name — never the value — and
> is deduped per `(scheme, key)` pairing so a polling consumer cannot flood
> the logs.
>
> Use environment variables in production. The userinfo form
> (`user:pass@host`) is treated as the conventional URI way of carrying
> credentials and is **not** warned about — you opted into it explicitly.

Note: `sql` and `sql-stored` read all endpoint options strictly from URI query
parameters and ignore environment variables so process env vars cannot alter
SQL queries, procedures, or `allowHeaderOverride`.


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

// ✅ GOOD — nothing in the URI: GetConfigValue picks up the
// FTP_USERNAME / FTP_PASSWORD environment variables automatically
// (there is no ${env:...} interpolation syntax)
endpoint := "ftp://host"
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
import "github.com/tranchida/gocamel/components/ftp"

ctx.AddComponent("ftp", ftp.NewFTPComponent())
ctx.AddComponent("http", gocamel.NewHTTPComponent())
ctx.AddComponent("timer", gocamel.NewTimerComponent())
ctx.AddComponent("direct", gocamel.NewDirectComponent())
```

### Scheduled Components

```go
import "github.com/tranchida/gocamel/components/cron"

ctx.AddComponent("cron", cron.NewCronComponent())
```

### Messaging Components

```go
import "github.com/tranchida/gocamel/components/telegram"

ctx.AddComponent("telegram", telegram.NewTelegramComponent())
```

### AI Components

```go
import "github.com/tranchida/gocamel/components/openai"

ctx.AddComponent("openai", openai.NewOpenAIComponent())
```

### Database Components

```go
import "database/sql"
import _ "modernc.org/sqlite"

db, _ := sql.Open("sqlite", "./app.db")

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
route, err := context.CreateRouteBuilder().
    From("timer:tick").
    SetID("timer-route-1").
    To("direct:output").
    Build()
if err != nil {
    log.Fatal(err)
}
```

### Common Options

```go
// Polling delay (requires: ctx.AddComponent("file", file.NewFileComponent()))
builder.From("file://input?delay=10s&delete=true")

// HTTP method
builder.To("http://api?httpMethod=POST")
```

## Environment Variable Mapping

| Component | Variable                                                  | Description       |
|-----------|-----------------------------------------------------------|-------------------|
| FTP       | `FTP_USERNAME`, `FTP_PASSWORD`                            | FTP credentials   |
| SFTP      | `SFTP_USERNAME`, `SFTP_PASSWORD`, `SFTP_PRIVATE_KEY_FILE` | SSH credentials   |
| STOMP     | `STOMP_LOGIN`, `STOMP_PASSCODE`, `STOMP_USERNAME`, `STOMP_PASSWORD` | Broker credentials |
| Telegram  | `TELEGRAM_AUTHORIZATIONTOKEN`                             | Bot token         |
| OpenAI    | `OPENAI_AUTHORIZATIONTOKEN`, `OPENAI_API_KEY`             | API keys          |
| Anthropic | `ANTHROPIC_AUTHORIZATIONTOKEN`, `ANTHROPIC_API_KEY`       | API keys          |
| Jev       | `JEV_AUTHORIZATIONTOKEN`, `OPENROUTER_API_KEY`            | API keys          |
| Ollama    | `OLLAMA_HOST`, `OLLAMA_API_KEY` (Ollama Cloud only)       | Host and cloud key |
| Mail      | URI query parameters (`username`, `password`)             | Email credentials |

## Best Practices

1. **Never commit secrets** — Use env vars or secret managers
2. **Use different configs per environment** — Dev/staging/prod
3. **Document required env vars** — In README or deployment docs
4. **Validate config at startup** — Fail fast on missing config