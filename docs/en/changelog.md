# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **`Exchange.DeepCopy()` / `Message.DeepCopy()`** — recursively duplicate the common mutable container types
  (`[]byte`, `[]any`, `map[string]any`, `[]string`) in bodies, headers, and properties; used by `WireTap` and
  parallel `Multicast` so concurrent branches never share a mutable body
- **Consistent exchange lifecycle** — every consumer (direct, seda, chan, timer, cron, http, file, ftp, sftp, smb,
  mail, redis, nats, telegram) now fires `Synchronization` callbacks (`OnComplete`/`OnFailure`) when route
  processing finishes; `ErrStopRouting` is normalized to success
- **Exec `allowHeaderOverride` option** — per-message `CamelExecCommand*` header overrides are now **opt-in**
  (disabled by default) to prevent untrusted headers from choosing the executed binary

### Fixed

- **Idempotent Consumer message loss** — the idempotency key is now removed from the repository when the exchange
  fails downstream, so redeliveries are processed instead of silently dropped (at-least-once semantics)
- **`chan` component consumer guard** — endpoints are now cached by channel name; previously each `CreateEndpoint`
  call returned a new instance, allowing multiple consumers to silently compete for the same channel
- **Lazy producer poisoning** — a transient failure while creating/starting a `To()` producer no longer poisons the
  route permanently; initialization is retried on the next exchange
- **DeadLetterChannel exponential backoff** — the delay now grows from the first redelivery
  (base × multiplier^attempt); the redelivery wait also respects context cancellation instead of sleeping blindly
- **Circuit Breaker half-open probing** — only one trial request probes the downstream at a time; concurrent
  requests are diverted to the fallback instead of stampeding the recovering service
- **Resequencer routing semantics** — buffered/duplicate/outdated messages now stop the original route
  (`ErrStopRouting`) so steps placed after `End()` never observe out-of-order exchanges; timeout-drain errors are
  logged instead of swallowed
- **Saga compensation visibility** — compensation failures are now logged (with step index and original error)
  instead of being silently ignored
- **`Route.Stop()` resource cleanup** — all producers and stoppable processors are stopped even when one of them
  fails; errors are collected and returned combined
- **Pipeline headers-only propagation** — an `Out` message carrying only headers (no body) now propagates between
  pipeline steps without erasing the current body
- **`direct` endpoint identity** — query parameters no longer create distinct endpoints (`direct:a` and
  `direct:a?x=1` now resolve to the same endpoint)
- **WireTap shutdown & efficiency** — taps reuse the shared producer cache instead of creating a producer per
  message, and `Route.Stop()` waits for in-flight taps instead of dropping them
- **HTTP consumer hardening** — added a 10-second `ReadHeaderTimeout` (slowloris) and a `%v` fallback for
  non-string response bodies instead of returning an empty response
- **SEDA/chan URI validation** — negative queue sizes and non-positive consumer counts fall back to defaults with
  a warning instead of panicking or being silently accepted

## [1.1.0] - 2026-05-24

### Added

- **Loop EIP** — `.Loop(count)`, `.LoopSimple(expr)`, and `.LoopFunc(fn)` iterate a route segment statically or dynamically, tracking index via `CamelLoopIndex`, `CamelLoopSize`, and `CamelLoopComplete` properties
- **Saga EIP** — `.Saga()` transaction orchestrator that coordinates distributed actions and compensations with LIFO rollback ordering (using `.Action()` or `.ActionFunc()`)
- **Transform EIP** — `.Transform(fn)` and `.TransformSimple(expr)` for clean, dedicated message body transformations
- **Message History EIP** — `.EnableMessageHistory()` tracks the chronological trajectory of exchanges across route nodes, inspectable via `gocamel.GetMessageHistory(exchange)`
- **NATS Component** — `nats://subject` high-performance publisher and consumer with NATS server connection configuration
- **Redis Component** — `redis://addr` supports SET, GET, DEL, PUBLISH commands and Pub/Sub channel subscriptions
- **Redis Idempotent Repository** — `NewRedisIdempotentRepository` distributed idempotent repository implementation with customizable TTL and prefixing
- **Go Channel Component** — `chan:channelName` fast, thread-safe asynchronous intra-process messaging utilizing native Go channels
- **Core Engine Optimizations** — Thread-safe global `ProducerCache` in `CamelContext` and thread-safe AST Cache for Simple Language expressions to maximize processing throughput

### Added

- **Routing Slip EIP** — `.RoutingSlip(expression)` routes an exchange through a dynamic sequence of endpoints; supports
  comma-separated strings, Simple Language expressions, and Go functions
- **Delay EIP** — `.Delay(duration)` pauses an exchange for a fixed duration; `.DelayFunc(fn)` computes the delay per
  exchange; both respect context cancellation and now fail cleanly when the dynamic delay function is nil
- **Content Enricher EIP** — `.Enrich(uri, strategy)` fetches data from an external endpoint and merges it into the
  current exchange via an `AggregationStrategy`; pass `nil` to replace the body directly
- **Throttle EIP** — `.Throttle(maxRequests, timePeriod)` rate-limits exchange processing; excess exchanges are
  delayed (not dropped) using a fixed-window counter; invalid limits now fail fast and queued exchanges respect context
  cancellation
- **Message Filter EIP** — `.Filter(expression)` drops exchanges that do not match a Go predicate or a Simple Language
  expression
- **`Exchange.PropagateOutToIn()`** — extracted helper that copies `Out` body and headers into `In`; used internally by
  `To`, `ToD`, and `WireTap`
- **Aggregator improvements** — per-correlation-key locking via `sync.Map` (replaces the previous global mutex),
  `SetCompletionTimeout(d)`, and `SetCompletionPredicate(fn)` completion strategies
- **`DeadLetterChannel` redelivery hardening** — redelivery now replays the failed processor before continuing the
  route, uses stable exchange IDs for retry tracking, and cleans retry state after successful redelivery without relying
  on consumer `Exchange.Done()` callbacks
- Transactional support via **Unit of Work** and **Synchronization**
    - New `.Transacted()` method in RouteBuilder DSL
    - Transactional processing in `file`, `ftp`, `sftp`, `smb`, and `mail` components (post-processing triggered at the
      end of the route)
- Comprehensive English documentation
- Bilingual documentation structure (EN/FR)
- MkDocs Material theme support
- New `security.go` file with security validation utilities
    - `ValidatePath()` - validates file paths for directory traversal
    - `ValidatePathInDir()` - ensures paths stay within allowed directories
    - `IsSafePath()` - checks if a path is safe
    - `SanitizeInput()` - removes dangerous characters from user input

### Security

- Fixed path traversal vulnerabilities in FTP, SFTP, and SMB components
- Fixed command injection vulnerability in Exec component
- Fixed SQL injection vulnerabilities in SQL and SQL-Stored components
- Added input sanitization for component URIs
- All components now validate file paths before operations

### Changed

- **Translations**: All French code comments translated to English (650+ comments)

## [0.1.0] - 2026-04-19

### Added

- Initial release of GoCamel
- Core EIP patterns: Choice, Split, Aggregate, Multicast, Filter, Transform, ToD, Stop
- Core components: Direct, Timer, File
- Network components: HTTP
- File transfer: FTP (FTPS), SFTP, SMB
- Messaging: Telegram Bot API
- AI: OpenAI GPT integration
- Mail: SMTP/SMTPS (send), IMAP/IMAPS, POP3/POP3S (receive)
- Database: SQL and SQL-Stored components
- Scheduling: Cron component with cron expressions
- Transformation: XSLT, XSD, Template (Go templates), Exec
- Simple Language: Expression language for routing and transformation
- REST Management API: JMX-like HTTP endpoints
- Memory and SQLite aggregation repositories

---

## History

```
0.1.0 ────────────── 1.1.0 ────────────── unreleased
  │                    │                      │
  └────────────────────┴──────────────────────┴──────▶ main
```
