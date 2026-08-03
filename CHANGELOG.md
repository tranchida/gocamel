# Changelog

All notable changes to this project are documented in this file.

## [v0.1.0] - 2026-08-03

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

[v0.1.0]: https://gitlab.com/tranchida/gocamel/-/tags/v0.1.0
