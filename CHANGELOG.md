# Changelog

All notable changes to this project are documented in this file.

## [Unreleased]

### Security

- **file producer** — writes using `CamelFileName` are confined to the endpoint
  directory (`ValidatePathInDir`). Absolute names and `../` traversal are rejected.
- **timer / HTTP consumers** — route panics are recovered via `ProcessSafely` so
  they fail the exchange instead of the process (timer) or skipping the error handler (HTTP).

### Fixed

- `SimpleSetBody` / `SimpleSetHeader` now attach to the current EIP container
  (Split, Loop, …) instead of always the top-level route.
- `RouteBuilder.Build()` surfaces `From()` endpoint resolution errors.
- `TimerConsumer.Stop()` waits for the firing goroutine to exit.
- `CamelContext.Start()` is rejected while a timed-out `GracefulStop` is still tearing down.
- CI runs `go test -race` on every module.
- `exec` / `template` path checks use component-walking traversal detection
  (filenames like `report..v2` are accepted). Template header overrides are
  confined to the endpoint directory. Exec `outFile` is validated before read.
- `RemoveRoute` / `RemoveRouteByID` / `RemoveAllRoutes` stop the route and
  release its ID. They now return `error`.
- SEDA / chan `Stop` unblocks producers waiting on a full queue and drains
  leftover exchanges.
- `Route.Validate()` aggregates errors with `errors.Join`.

### Components

- **New `ollama` component** — native integration with a local Ollama
  server via the official `github.com/ollama/ollama/api` client. Dedicated
  module `gitlab.com/tranchida/gocamel/components/ollama`. Three modes
  selected by URI path:
  - `ollama:chat` — multi-turn chat (`/api/chat`)
  - `ollama:generate` — one-shot generation (`/api/generate`)
  - `ollama:embed` — embeddings (`/api/embed`)
  Supports `keepAlive`, `think` (reasoning effort), `format` (structured
  output) options, and per-message override headers (`OllamaModel`,
  `OllamaSystem`). Response headers expose token counts and timing.
  Producer only; model lifecycle operations (Pull/List/Delete) are out
  of scope.
- **New `anthropic` component** — native integration with the Anthropic
  Messages API (Claude). Dedicated module
  `gitlab.com/tranchida/gocamel/components/anthropic` built on the official
  `anthropic-sdk-go`. Supports a separate `system` parameter, per-message
  override headers (`AnthropicSystem`, `AnthropicModel`, `AnthropicMaxTokens`,
  `AnthropicTemperature`), response headers for token usage and stop reason,
  and an optional `baseURL` (validated `http(s)`) for proxies. Producer only.
- `openai` — new `baseURL` URI option lets the producer target any
  OpenAI-compatible endpoint (OpenRouter, Groq, Together, Mistral, vLLM, LM
  Studio, Ollama's OpenAI compatibility layer, ...). When unset, the SDK
  defaults to `https://api.openai.com/v1`.

## [v0.7.0] - 2026-08-16

Security hardening release: header-override opt-in across all external
components, credential redaction in endpoint URIs, and outbound header
filtering for Kafka and gRPC.

### ⚠️ Breaking changes

All changes below are **secure-by-default behavioural changes**. Routes that
relied on the previous permissive behaviour must add `allowHeaderOverride=true`
to the endpoint URI to restore it. Only opt in when exchange headers can never
be influenced by untrusted input.

- `mongodb` — `CamelMongoDbDatabase`, `CamelMongoDbCollection`,
  `CamelMongoDbOperation` and `CamelMongoDbCriteria` headers are now honored
  only with `allowHeaderOverride=true`.
- `kafka` — `CamelKafkaTopic`, `CamelKafkaPartitionKey` and `CamelKafkaKey`
  headers are now honored only with `allowHeaderOverride=true`.
- `redis` — `CamelRedisCommand`, `CamelRedisKey` and `CamelRedisChannel`
  headers are now honored only with `allowHeaderOverride=true`.
- `nats` — `CamelNATSSubject` header is now honored only with
  `allowHeaderOverride=true`; overridden subjects containing spaces are
  rejected.
- `grpc` — `CamelGrpcMethod` header is now honored only with
  `allowHeaderOverride=true` and must match `Service/Method` form.
- `telegram` — `CamelTelegramChatId` header is now honored only with
  `allowHeaderOverride=true`.
- `mail` — `From`, `To`, `Cc`, `Bcc`, `Subject` and `Content-Type` exchange
  headers are now honored only with `allowHeaderOverride=true`; inbound
  IMAP/POP3 MIME headers are isolated under the `CamelMailHeader_` prefix to
  prevent header confusion with downstream producers.
- `cron` — invalid cron expressions and timezones now fail fast at
  `CreateEndpoint` instead of failing silently at schedule time.
- `grpc` — `insecure=false` now requires working TLS (previously it silently
  configured no transport credentials and broke at runtime).

### Core (`gitlab.com/tranchida/gocamel`)

- **New exported helper `RedactURI`** — masks userinfo passwords and sensitive
  query parameters (`password`, `token`, `apiKey`, `authorizationToken`, ...)
  with `xxxxx` so endpoint URIs can be logged or introspected safely.
- Removed the unused `sanitizer.pathTraversal` regex — traversal detection
  lives in `containsTraversal`.
- Added unit test coverage for `security.go` (path validation, symlink
  containment, SQL stacked-statement parser, input sanitization).

### Components

- `openai`, `telegram`, `mail` — `Endpoint.URI()` now returns a redacted URI
  via `RedactURI`; API keys, bot tokens and passwords no longer leak into
  logs, error messages or management introspection. Authentication still reads
  the original parsed URL.
- `kafka`, `grpc` — outbound header propagation (Kafka headers, gRPC metadata)
  now blocks sensitive headers by default (`Authorization`, `Cookie`,
  `Proxy-Authorization`, `Set-Cookie`, `WWW-Authenticate`, case-insensitive).
  New `forwardHeaders=a,b` URI option provides an explicit allowlist;
  sensitive headers remain blocked even when listed.
- `grpc` — the `insecure=false` branch now works: TLS 1.2+ with system root
  CAs and `ServerName` derived from the target. New `caCert` URI option loads
  a custom PEM CA (rejected in combination with `insecure=true`); certificate
  file errors now fail at `CreateEndpoint` instead of at runtime.
- `mail` — new `requireStartTLS` URI option refuses to send credentials to
  SMTP servers that do not announce STARTTLS, with connection deadlines
  against stalled servers.

## [v0.6.0] - 2026-08-15

JSONPath expressions, PollEnrich, and Validate EIPs.

### Core (`gitlab.com/tranchida/gocamel`)

- **JSONPath Expression** — Added pure-Go `EvaluateJSONPath`, `JsonPath(...)`, `JsonPathHeader(...)`, and Simple Language function `${jsonpath:...}` for query navigation across JSON bodies, headers, maps, and structs without third-party dependencies.
- **PollEnrich EIP** — Added `PollEnrich` and `PollEnrichWithTimeout` processors allowing on-demand polling consumption of messages from external endpoints with timeout and aggregation strategies.
- **Validate EIP** — Added `Validate` and `ValidateFunc` processors validating messages in-flight and failing with `ErrValidationFailed` when predicates evaluate to false.

## [v0.5.0] - 2026-08-15

New Kafka and gRPC components, alongside JSON and XML DataFormats.

### Core (`gitlab.com/tranchida/gocamel`)

- **JSON DataFormat** — Added `JSONDataFormat` (`MarshalJson`, `UnmarshalJson`), supporting pretty-printing, typed target unmarshalling (`JSONUnmarshalType`), generic map/slice unmarshalling, and `json.Number`.
- **XML DataFormat** — Added `XMLDataFormat` (`MarshalXml`, `UnmarshalXml`), supporting XML declaration headers, indentation, typed struct unmarshalling, and DoS mitigation rejecting recursive `<!DOCTYPE>` entity declarations.

### Components

- `kafka` (`gitlab.com/tranchida/gocamel/components/kafka`) — New Apache Kafka component using `segmentio/kafka-go`, supporting producers with dynamic partition key / topic headers, consumers with consumer groups / partition assignments, and header propagation.
- `grpc` (`gitlab.com/tranchida/gocamel/components/grpc`) — New gRPC component using `google.golang.org/grpc`, supporting client producers with status code / metadata mapping, and server consumers with dynamic RPC dispatch.

## [v0.4.0] - 2026-08-15

Read lock mechanism across file/remote file components and route builder enhancements.

### Core (`gitlab.com/tranchida/gocamel`)

- **Read lock support** — Added `ReadLockStrategy` (`none`, `changed`, `rename`, `markerFile`) and common polling options (`readLockTimeout`, `readLockCheckInterval` / `readLockInterval`, `readLockMinLength`, `readLockMinAge`, `readLockMarkerFile`).
- **New exported helper** — `WaitForStable` to poll file metadata until stable and satisfying minimum constraints.
- **Route builder** — Wire nested child processors correctly and standardize file headers.

### Components

- `file`, `ftp`, `sftp`, `smb` — Full support for `readLock` strategies (`changed`, `rename`, `markerFile`, `none`) with automatic lock management and synchronization cleanup.

## [v0.3.0] - 2026-08-13

Security hardening and bug fixes across the core and several components.

### Core (`gitlab.com/tranchida/gocamel`)

- **SQL injection fixes** — `sql-stored` now honours the
  `CamelSqlStoredProcedureName` header only with `allowHeaderOverride=true`
  (default false), no longer interpolates the procedure name, and validates it
  against a strict identifier pattern before splicing it into the `CALL`
  statement. `SQLAggregationRepository` validates its table name at
  construction.
- **HTTP** — added `ReadTimeout`/`IdleTimeout` (60 s) to the consumer server,
  a canonical-key reflection guard for response headers, and the opt-in
  `SetSkipAuthorizationHeaders` producer option (strips `Authorization`/`Cookie`).
- **Net** — new `readTimeout` consumer option (default 60 s) against slowloris.
- **Exec** — a header-supplied executable must be a bare command name (no path),
  closing arbitrary command execution via `allowHeaderOverride`.
- **EIP fixes** — Routing Slip and Load Balancer no longer leak producers;
  the circuit breaker recovers panics so the half-open state always resets;
  `ChanProducer` deep-copies exchanges (data race); nested aggregators (inside
  Split/Multicast/Choice/Loop) get their completion timeouts wired and stopped;
  the Resequencer gained a `Stop()` method; the Delayer no longer leaks timers.
- **Simple language** — `random(0)` no longer panics; `padstart`/`padend` are
  bounded by `MaxSimpleInputBytes`; map access no longer panics on non-string
  key types; logical `&&`/`||` splitting respects quoted literals; `%` uses
  `math.Mod` for floats.
- **New exported helpers** — `ReadLimited` and `ParseInt64Option`.
- **Nil-context safety** — route lazy producers, the Idempotent Consumer and
  the Claim Check all fall back to `context.Background()` instead of passing a
  nil context to producers/repositories.

### Components

- `mail` — CRLF-stripped SMTP headers (header injection fix); the IMAP consumer
  closes stale clients on reconnect and supports `maxMessageSize` (with an
  `RFC822Size` pre-check); POP3 supports `maxMessageSize`.
- `ftp`, `sftp`, `smb` — new `maxMessageSize` consumer option; `smb` producer
  now guards its persistent connection with a mutex.
- `redis` — the shared client is closed exactly once (`sync.Once`).
- `telegram` — `Stop()` calls `StopReceivingUpdates()` and waits for the
  polling goroutine.

### Notes

- The `sql-stored` `CamelSqlStoredProcedureName` header and the `exec`
  header-supplied executable changed behaviour: both now require opt-in and
  stricter validation.

## [v0.2.0] - 2026-08-03

First release. GoCamel is a lightweight Apache Camel-inspired integration
framework for Go, organized as a Go workspace of independent modules.

### Core (`gitlab.com/tranchida/gocamel`)

- Zero external runtime dependencies.
- Routing engine: routes, pipeline, multicast, recipient list, split,
  aggregate, choice, filter, loop, saga, transform, resequencer, circuit
  breaker, wiretap, claim check, idempotent consumer, throttler, dynamic
  router, routing slip, message history, load balancer (round-robin/random),
  try/catch error handling and the Stop EIP.
- Simple Language expression evaluator (property/header/body access,
  string functions, math, date, base64, random, uuid) with a ReDoS guard.
- Stdlib-only components: direct, seda, chan, timer, http, exec, template,
  sql (with SQL aggregation/claim-check/idempotent repositories).
- Security helpers (`ValidatePath`, `ValidatePathInDir`, `SanitizeInput`,
  `IsSafePath`) reused across components.
- gosec and staticcheck clean across the whole workspace.

### Components (`gitlab.com/tranchida/gocamel/components/<name>`)

- `cron` — scheduled jobs.
- `file` — filesystem producer/consumer with move/delete post-processing.
- `ftp` — FTP producer/consumer.
- `mail` — SMTP/SMTPS producer, IMAP/IMAPS and POP3/POP3S consumers.
- `mongodb` — MongoDB component.
- `nats` — NATS producer/consumer.
- `openai` — OpenAI integration.
- `redis` — Redis component.
- `sftp` — SFTP with strict host key checking by default.
- `smb` — SMB shares.
- `telegram` — Telegram bot integration.

### Observability

- `observability/otel` — OpenTelemetry tracing interceptor.
- `observability/prometheus` — Prometheus metrics interceptor.

### Notes

- The examples module always builds against the in-repo modules and is not
  a published library.
- Pre-v1: breaking API changes are allowed between minor versions.

[v0.7.0]: https://gitlab.com/tranchida/gocamel/-/tags/v0.7.0
[v0.6.0]: https://gitlab.com/tranchida/gocamel/-/tags/v0.6.0
[v0.5.0]: https://gitlab.com/tranchida/gocamel/-/tags/v0.5.0
[v0.4.0]: https://gitlab.com/tranchida/gocamel/-/tags/v0.4.0
[v0.3.0]: https://gitlab.com/tranchida/gocamel/-/tags/v0.3.0
[v0.2.0]: https://gitlab.com/tranchida/gocamel/-/tags/v0.2.0
