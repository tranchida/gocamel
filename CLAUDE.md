# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## AI Agent Configuration

**Preferred AI Agent:** Claude via Ollama

**Launch Command:**
```bash
ollama launch claude --model glm-5.1:cloud
```

**Description:** Claude Code is configured to work with the GoCamel project using the GLM-5.1 model (Cloud provider). This ensures consistent and high-quality code generation across all sessions.

## Commands

```bash
# Run all tests
go test ./...

# Run a single test
go test -run TestName ./...

# Run tests with verbose output
go test -v ./...

# Build the module
go build ./...

# Run an example
go run examples/http-echo/http_echo.go
```

## Architecture

GoCamel is a Go implementation of Apache Camel's Enterprise Integration Patterns (EIP). The core abstractions mirror Camel's design:

### Core Interfaces (`endpoint.go`)
- **`Component`** — factory that creates `Endpoint`s from a URI string; registered against a URI scheme (e.g. `"ftp"`, `"http"`)
- **`Endpoint`** — a configured connection point; creates `Consumer`s (source) or `Producer`s (sink)
- **`Consumer`** — reads from an external system and drives messages into a `Route`
- **`Producer`** — writes an `Exchange` to an external system
- **`Processor`** — any step in a route that transforms or inspects an `Exchange`

### Runtime (`context.go`, `route.go`, `route_builder.go`)
- **`CamelContext`** — top-level container; holds the `ComponentRegistry`, owns all `Route`s, and manages their lifecycle (`Start`/`Stop`)
- **`Route`** — a pipeline of `Processor`s fed by one `Consumer`. Built via `RouteBuilder` for a fluent DSL
- **`RouteBuilder`** — DSL wrapper: `.From(uri).ProcessFunc(...).To(uri).Build()`. Delegates to the underlying `Route`
- `ErrStopRouting` — sentinel error that halts route processing without propagating as a failure (used by `Aggregator`)

### Message Model (`exchange.go`, `message.go`)
- **`Exchange`** — the unit of work: `In` message (input), `Out` message (output), and a `Properties` bag. Consumers create a new `Exchange` per event; `To(uri)` copies `Out` → `In` before sending
- **`Message`** — body (`any`) + headers (`map[string]any`). Pre-defined header keys are `Camel*` constants (e.g. `CamelFileName`, `CamelHttpMethod`)

### Component Registry (`registry.go`)
- **`ComponentRegistry`** — thread-safe map from scheme name to `Component`. `CamelContext.AddComponent("ftp", NewFTPComponent())` registers a component; URI parsing splits on the first `:` to look up the right factory

### Configuration (`config.go`)
- `GetConfigValue(u *url.URL, key string)` — unified config lookup used by all components: checks env vars first (`KEY`, then `SCHEME_KEY`), then URI query params, then URL userinfo. Sensitive credentials should use env vars.

### EIP: Split (`splitter.go`)
- **`Splitter`** implements `Processor`. Splits a message into parts using an expression function; each part is processed sequentially through nested processors
- `SplitDefinition` — fluent sub-DSL returned by `.Split(expr)`. Supports `.AggregationStrategy()`, all standard DSL methods, and `.End()` to close the block
- Exchange properties set on each part: `CamelSplitIndex` (0-based), `CamelSplitSize`, `CamelSplitComplete` (bool)

### EIP: Aggregate (`aggregator.go`, `aggregation_strategy.go`, `aggregation_repository.go`)
- **`Aggregator`** implements `Processor`. Collects exchanges under a correlation key until `CompletionSize` is reached, then lets the aggregated exchange continue; incomplete groups return `ErrStopRouting`
- **`AggregationStrategy`** — interface (`Aggregate(old, new *Exchange) *Exchange`) for merge logic
- **`AggregationRepository`** — interface for persistence; implementations: `MemoryAggregationRepository` (in-process) and `SqlAggregationRepository` (SQLite via `go-sqlite3`)

### EIP: Multicast (`multicast.go`, `pipeline.go`)
- **`Multicast`** implements `Processor`. Sends a copy of the exchange to multiple branches (sequential or parallel)
- `MulticastDefinition` — fluent sub-DSL returned by `.Multicast()`. Supports `.AggregationStrategy()`, `.ParallelProcessing()`, `.Pipeline()` (sub-branch), and `.End()`
- `PipelineDefinition` — groups processors within a multicast branch; `.End()` returns to `MulticastDefinition`
- Exchange properties set on each branch: `CamelMulticastIndex` (0-based), `CamelMulticastSize`, `CamelMulticastComplete` (bool)

### EIP: Stop
- `.Stop()` — halts routing for the current exchange by returning `ErrStopRouting` (not treated as a failure)

### EIP: ToD (To Dynamic)
- `.ToD(uriTemplate)` — sends to an endpoint whose URI is resolved at runtime using `${header.x}`, `${property.x}`, or `${body}` interpolation (`Interpolate()` in `utils.go`)

### Header/Property EIPs
- `.SetHeader(key, value)` / `.SetHeaders(map)` / `.SetHeadersFunc(fn)` — set Out message headers
- `.RemoveHeader(name)` / `.RemoveHeaders(pattern, excludePatterns...)` — remove In message headers; `*` wildcard supported with optional exclusions
- `.SetProperty(key, value)` / `.SetPropertyFunc(key, fn)` — set exchange properties
- `.RemoveProperty(key)` / `.RemoveProperties(pattern, excludePatterns...)` — remove exchange properties; `*` wildcard with optional exclusions
### Available Components

| Scheme | File | Role |
|--------|------|------|
| `http` | `http_component.go` | HTTP server (Consumer) + HTTP client (Producer) |
| `file` | `file_component.go` | Local filesystem watch (Consumer) + write (Producer) |
| `ftp` | `ftp_component.go` | FTP polling (Consumer) + upload (Producer) |
| `sftp` | `sftp_component.go` | SFTP polling (Consumer) + upload (Producer) |
| `smb` | `smb_component.go` | SMB/Samba share (Consumer & Producer) |
| `telegram` | `telegram_component.go` | Bot polling (Consumer) + send message (Producer) |
| `openai` | `openai_component.go` | Chat completion (Producer only) |
| `xslt` | `xslt_component.go` | XSLT transformation (Producer only) |
| `xsd` | `xsd_component.go` | XSD schema validation (Producer only) |
| `quartz` | `quartz_component.go` | Cron/interval-based scheduler (Consumer only) |
| `exec` | `exec_component.go` | Execute system commands (Producer only) |
| `direct` | `direct_component.go` | Synchronous in-memory routing between routes (Consumer & Producer) |
| `timer` | `timer_component.go` | Simple periodic timer (Consumer only) |
| `mail` | `mail_component.go` | Email send/receive via SMTP/IMAP/POP3 (Consumer & Producer) |
| `sql` | `sql_component.go` | SQL queries execution (Producer only) |
| `sql-stored` | `sql_stored_component.go` | SQL stored procedures with IN/OUT/INOUT params (Producer only) |

### Composant Mail

Le composant mail supporte l'envoi et la réception d'emails via plusieurs protocoles.

**Protocoles supportés:**
- SMTP (port 587) / SMTPS (port 465) - Envoi
- IMAP (port 143) / IMAPS (port 993) - Réception avec IDLE
- POP3 (port 110) / POP3S (port 995) - Réception

**Exemples d'URI:**
```
// Envoi SMTP
smtp://smtp.gmail.com:587?username=user@gmail.com&password=pass&to=dest@example.com

// Réception IMAP avec IDLE
imaps://imap.gmail.com:993?folderName=INBOX&username=user@gmail.com&password=pass&idle=true

// Réception POP3
pop3://pop.gmail.com:995?username=user@gmail.com&password=pass&delete=false
```

**Options communes:**
- `username`, `password` - Credentials
- `folderName` - Dossier IMAP (défaut: INBOX)
- `unseen` - Messages non lus seulement (défaut: true)
- `idle` - Utiliser IMAP IDLE pour notifications push
- `delete` - Supprimer après traitement
- `pollDelay` - Délai entre polls en ms (défaut: 60000)
- `fetchSize` - Nombre max de messages par poll

**Headers spéciaux:**
- `From`, `To`, `Cc`, `Subject` - En-têtes standards
- `Reply-To` - Adresse de réponse
- `CamelMailBodyHTML` - Propriété contenant version HTML
- `CamelMailAttachment_<nom>` - Pièces jointes

### Exchange Properties API (`exchange.go`)
- `SetProperty(key, value)`, `GetProperty(key)`, `GetPropertyOrDefault(key, default)`, `HasProperty(key)`
- Typed accessors: `GetPropertyAsString`, `GetPropertyAsInt`, `GetPropertyAsBool`, `GetPropertyAsFloat`, `GetPropertyAsTime`, `GetPropertyAsDuration`, `GetPropertyAsMap`, `GetPropertyAsSlice`
- `RemoveProperty(key)`, `RemoveProperties(pattern, excludePatterns...)` — `*` wildcard with exclusions
- `GetProperties()`, `SetProperties(map)`, `ClearProperties()`

### Management API (`management.go`)
- `ManagementServer` exposes a REST API: `GET /api/context`, `GET /api/routes`, `POST /api/routes/{id}/start|stop`

### Timer component notes
- URI params: `period` (ms, default 1000), `delay` (ms, default 1000), `repeatCount` (0=unlimited), `fixedRate` (bool)
- Exchange properties set on each fire: `CamelTimerName`, `CamelTimerFiredTime`, `CamelTimerPeriod`, `CamelTimerCounter`
- Simpler predecessor to Quartz; use Quartz for cron expressions or sub-second intervals

### Exec component notes
- URI params: `args` (space-separated), `workingDir`, `timeout` (ms), `outFile` (redirect stdout to file), `useStderrOnEmpty` (bool)
- Headers on output: `CamelExecExitValue`, `CamelExecStdout`, `CamelExecStderr`
- Headers for runtime override: `CamelExecCommandExecutable`, `CamelExecCommandArgs`, `CamelExecCommandWorkingDir`, `CamelExecCommandTimeout`

### Quartz component notes
- CronTrigger (`cron=` param): uses robfig/cron with 6-field seconds-inclusive expressions
- SimpleTrigger (`trigger.repeatInterval=` param): uses `time.Ticker` — supports sub-second intervals (robfig/cron `@every` rounds up to 1s, so it is intentionally bypassed)
- All consumers on the same `QuartzComponent` share one `*cron.Cron` scheduler
- Headers set on each exchange: `fireTime`, `scheduledFireTime`, `nextFireTime`, `previousFireTime`, `triggerName`, `triggerGroup`, `refireCount`

### InOut exchange pattern
`Exchange.GetResponse()` returns `Out` if its body is set, otherwise falls back to `In`. Used by `HTTPConsumer` so processors can reply without explicitly writing to `Out`.

### Adding a New Component
1. Implement `Component`, `Endpoint`, `Consumer` (if applicable), and `Producer` (if applicable) in a `<name>_component.go` file
2. Use `GetConfigValue(u, "key")` for any credentials or options
3. For file-like consumers, check `file_filter.go` for `matchFileName` with `include`/`exclude` query params (already used by `file`, `ftp`, `sftp`)
4. Register in user code: `camelCtx.AddComponent("scheme", NewMyComponent())`
5. Add tests in `<name>_component_test.go`
