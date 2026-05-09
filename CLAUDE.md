# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

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

GoCamel is a Go implementation of Apache Camel's Enterprise Integration Patterns (EIP). The module path is `gitlab.com/tranchida/gocamel`. All EIP processors and components live in the root package.

### Core Interfaces (`endpoint.go`)
- **`Component`** — factory that creates `Endpoint`s from a URI string; registered against a URI scheme (e.g. `"ftp"`, `"http"`)
- **`Endpoint`** — a configured connection point; creates `Consumer`s (source) or `Producer`s (sink)
- **`Consumer`** — reads from an external system and drives messages into a `Route`
- **`Producer`** — writes an `Exchange` to an external system
- **`Processor`** — any step in a route that transforms or inspects an `Exchange`; `ProcessorFunc` adapts a plain function
- **`ProcessorContainer`** — implemented by `Route`, `Splitter`, `Multicast`, `Pipeline`; allows the RouteBuilder to target nested containers

### Runtime (`context.go`, `route.go`, `route_builder.go`)
- **`CamelContext`** — top-level container; holds the `ComponentRegistry`, owns all `Route`s, and manages their lifecycle (`Start`/`Stop`)
- **`Route`** — a pipeline of `Processor`s fed by one `Consumer`. `.Transacted()` ensures consumer acknowledgement is tied to exchange outcome
- **`RouteBuilder`** — fluent DSL: `.From(uri).ProcessFunc(...).To(uri).Build()`. DSL construction errors accumulate and surface at `.Build()`
- **`ErrStopRouting`** — sentinel error that halts route processing without propagating as a failure

### Message Model (`exchange.go`, `message.go`)
- **`Exchange`** — unit of work: `In` (input), `Out` (output), `Properties` bag, `Error`, and `Context` (`context.Context`). `To(uri)` copies `Out` → `In` before forwarding
- **`Message`** — body (`any`) + headers (`map[string]any`). Pre-defined header keys are `Camel*` constants in `message.go`
- `Exchange.GetResponse()` returns `Out` if body is set, otherwise falls back to `In` (used by `HTTPConsumer` for request-reply)

### Component Registry (`registry.go`)
- **`ComponentRegistry`** — thread-safe map from URI scheme to `Component`. URI parsing splits on the first `:` to pick the factory
- `GetConfigValue(u *url.URL, key string)` in `config.go` — unified config lookup: env vars first (`KEY`, then `SCHEME_KEY`), then URI query params, then URL userinfo

### EIPs

| EIP | File | DSL entry point |
|-----|------|-----------------|
| Split | `splitter.go` | `.Split(exprFn)` → `SplitDefinition` → `.End()` |
| Aggregate | `aggregator.go` | `.Aggregate(aggregator)` |
| Multicast | `multicast.go`, `pipeline.go` | `.Multicast()` → `MulticastDefinition` → `.Pipeline()` → `.End()` |
| Choice (CBR) | `choice.go` | `.Choice().When(expr)…Otherwise().EndChoice()` |
| WireTap | `wiretap.go` | `.WireTap(uri)` — async fire-and-forget copy |
| Recipient List | `recipient_list.go` | `.RecipientList(expr)` → `.ParallelProcessing()` → `.End()` |
| Message Filter | `filter.go` | `.Filter(expr)` — Go predicate or Simple Language string |
| Content Enricher | `enricher.go` | `.Enrich(uri, strategy)` — nil strategy replaces body |
| Throttle | `throttle.go` | `.Throttle(maxRequests, timePeriod)` — fixed-window rate limiter, delays excess |
| Delay | `delay.go` | `.Delay(duration)` / `.DelayFunc(fn)` — sleeps for fixed or dynamic duration, context-aware |
| Stop | `route_builder.go` | `.Stop()` — returns `ErrStopRouting` |
| ToD | `route_builder.go` | `.ToD(template)` — `${header.x}`, `${property.x}`, `${body}` interpolation via `utils.go:Interpolate()` |

**Split** sets `CamelSplitIndex`, `CamelSplitSize`, `CamelSplitComplete` on each part exchange.  
**Multicast** sets `CamelMulticastIndex`, `CamelMulticastSize`, `CamelMulticastComplete`.  
**RecipientList** expression can be: comma-separated URI string, Simple Language expression (`${header.recipients}`), or `func(*Exchange) ([]string, error)`.  
**Choice** `.When(expr)` uses Simple Language expressions evaluated as bool.

### Simple Language (`simple_language.go`)
Expressions like `${body}`, `${header.foo}`, `${exchangeProperty.bar}`, `${date:now:yyyy-MM-dd}`. Used in `.SimpleSetBody()`, `.SimpleSetHeader()`, `.LogSimple()`, `.RecipientList()`, `.ToD()`, and `ChoiceBuilder.When()`.

### Error Handling (`error_handler.go`)
- **`ErrorHandler`** interface — `HandleError(exchange, err) error`
- **`NoErrorHandler`** — default; propagates errors immediately
- **`DeadLetterChannel`** — retries up to `MaxRedeliveries`, then routes failed exchange to a dead letter URI. Options: `MaxRedeliveries(n)`, `RedeliveryDelay(d)`, `ExponentialBackoff(multiplier)`
- DSL: `.ErrorHandler(handler)` or `.DeadLetterChannel(uri, opts...)`

### Observability (`prometheus.go`, `otel.go`, `logger.go`)
- **Prometheus** — `.EnableMetrics()` on a route instruments it with `prometheus/client_golang`. Metrics exposed via `ManagementServer` or custom handler
- **OpenTelemetry** — `.EnableTracing()` wraps route processing in an OTEL span (tracer name: `gitlab.com/tranchida/gocamel`)
- **Logger** — structured logging via `logInfo`/`logError` helpers in `logger.go`

### Management API (`management.go`)
`ManagementServer` exposes a REST API: `GET /api/context`, `GET /api/routes`, `POST /api/routes/{id}/start|stop`

### Available Components

| Scheme | File | Role |
|--------|------|------|
| `http`/`https` | `http_component.go` | HTTP server (Consumer) + HTTP client (Producer) |
| `file` | `file_component.go` | Local filesystem watch (Consumer) + write (Producer) |
| `ftp` | `ftp_component.go` | FTP polling (Consumer) + upload (Producer) |
| `sftp` | `sftp_component.go` | SFTP polling (Consumer) + upload (Producer) |
| `smb` | `smb_component.go` | SMB/Samba share (Consumer & Producer) |
| `telegram` | `telegram_component.go` | Bot polling (Consumer) + send message (Producer) |
| `openai` | `openai_component.go` | Chat completion (Producer only) |
| `xslt` | `xslt_component.go` | XSLT transformation (Producer only) |
| `xsd` | `xsd_component.go` | XSD schema validation (Producer only) |
| `quartz` | `quartz_component.go` | Cron/interval scheduler (Consumer only); 6-field seconds-inclusive cron via `robfig/cron` |
| `cron` | `cron_component.go` | Cron scheduler (Consumer only) |
| `exec` | `exec_component.go` | Execute system commands (Producer only) |
| `direct` | `direct_component.go` | Synchronous in-memory routing between routes (Consumer & Producer) |
| `timer` | `timer_component.go` | Simple periodic timer (Consumer only) |
| `mail`/`smtp`/`imap`/`pop3` | `mail_component.go` | Email send/receive via SMTP/IMAP/POP3 (Consumer & Producer) |
| `sql` | `sql_component.go` | SQL queries (Producer only) |
| `sql-stored` | `sql_stored_component.go` | SQL stored procedures with IN/OUT/INOUT params (Producer only) |
| `template` | `template_component.go` | Go template rendering (Producer only) |
| `mongodb` | `mongodb_component.go` | MongoDB operations (Producer only) |

**File-like consumers** (`file`, `ftp`, `sftp`) share `file_filter.go:matchFileName` for `include`/`exclude` URI query params and `polling_options.go` for common polling configuration.

**Quartz** `SimpleTrigger` uses `time.Ticker` directly (not `@every`) to support sub-second intervals. Headers set per exchange: `fireTime`, `scheduledFireTime`, `nextFireTime`, `previousFireTime`, `triggerName`, `triggerGroup`, `refireCount`.

**Exec** headers on output: `CamelExecExitValue`, `CamelExecStdout`, `CamelExecStderr`. Runtime override headers: `CamelExecCommandExecutable`, `CamelExecCommandArgs`, `CamelExecCommandWorkingDir`, `CamelExecCommandTimeout`.

**Mail** common URI params: `username`, `password`, `folderName`, `unseen`, `idle` (IMAP IDLE push), `delete`, `pollDelay` (ms), `fetchSize`. Special headers: `From`, `To`, `Cc`, `Subject`, `Reply-To`, `CamelMailBodyHTML`, `CamelMailAttachment_<name>`.

### Adding a New Component
1. Implement `Component`, `Endpoint`, `Consumer` (if applicable), and `Producer` (if applicable) in `<name>_component.go`
2. Use `GetConfigValue(u, "key")` for credentials or options
3. For file-like consumers, reuse `file_filter.go` and `polling_options.go`
4. Validate paths/inputs using helpers in `security.go` (`validateRemotePath`, `validateSQLQuery`)
5. Register in user code: `camelCtx.AddComponent("scheme", NewMyComponent())`
6. Add tests in `<name>_component_test.go`

<!-- gitnexus:start -->
# GitNexus — Code Intelligence

This project is indexed by GitNexus as **gocamel** (3736 symbols, 9251 relationships, 300 execution flows). Use the GitNexus MCP tools to understand code, assess impact, and navigate safely.

> If any GitNexus tool warns the index is stale, run `npx gitnexus analyze` in terminal first.

## Always Do

- **MUST run impact analysis before editing any symbol.** Before modifying a function, class, or method, run `gitnexus_impact({target: "symbolName", direction: "upstream"})` and report the blast radius (direct callers, affected processes, risk level) to the user.
- **MUST run `gitnexus_detect_changes()` before committing** to verify your changes only affect expected symbols and execution flows.
- **MUST warn the user** if impact analysis returns HIGH or CRITICAL risk before proceeding with edits.
- When exploring unfamiliar code, use `gitnexus_query({query: "concept"})` to find execution flows instead of grepping. It returns process-grouped results ranked by relevance.
- When you need full context on a specific symbol — callers, callees, which execution flows it participates in — use `gitnexus_context({name: "symbolName"})`.

## Never Do

- NEVER edit a function, class, or method without first running `gitnexus_impact` on it.
- NEVER ignore HIGH or CRITICAL risk warnings from impact analysis.
- NEVER rename symbols with find-and-replace — use `gitnexus_rename` which understands the call graph.
- NEVER commit changes without running `gitnexus_detect_changes()` to check affected scope.

## Resources

| Resource | Use for |
|----------|---------|
| `gitnexus://repo/gocamel/context` | Codebase overview, check index freshness |
| `gitnexus://repo/gocamel/clusters` | All functional areas |
| `gitnexus://repo/gocamel/processes` | All execution flows |
| `gitnexus://repo/gocamel/process/{name}` | Step-by-step execution trace |

## CLI

| Task | Read this skill file |
|------|---------------------|
| Understand architecture / "How does X work?" | `.claude/skills/gitnexus/gitnexus-exploring/SKILL.md` |
| Blast radius / "What breaks if I change X?" | `.claude/skills/gitnexus/gitnexus-impact-analysis/SKILL.md` |
| Trace bugs / "Why is X failing?" | `.claude/skills/gitnexus/gitnexus-debugging/SKILL.md` |
| Rename / extract / split / refactor | `.claude/skills/gitnexus/gitnexus-refactoring/SKILL.md` |
| Tools, resources, schema reference | `.claude/skills/gitnexus/gitnexus-guide/SKILL.md` |
| Index, status, clean, wiki CLI commands | `.claude/skills/gitnexus/gitnexus-cli/SKILL.md` |

<!-- gitnexus:end -->
