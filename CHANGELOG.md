# Changelog

All notable changes to this project are documented in this file.

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

[v0.3.0]: https://gitlab.com/tranchida/gocamel/-/tags/v0.3.0
[v0.2.0]: https://gitlab.com/tranchida/gocamel/-/tags/v0.2.0
