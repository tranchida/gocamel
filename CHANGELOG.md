# Changelog

All notable changes to this project are documented in this file.

## [Unreleased]

Fixes from the 2026-09 security and code review.

### Security

- **ToD option injection**: `ToD` now resolves templates with the new `InterpolateURI`, which escapes exchange values by position (query-escaped in the query string, `?`/`#` escaped in the path). A header such as `x&allowHeaderOverride=true` could previously enable header overrides on an `exec:` or `sql:` endpoint and lead to command execution. Whole-URI placeholders (`ToD("${header.uri}")`) are still inserted verbatim and must be trusted.
- **HTTP header isolation**: the consumer no longer emits internal `Camel*` headers in responses (`CamelHttpQuery` leaked query-string secrets, `CamelFileName` internal paths) and drops inbound `Camel*` request headers; the producer never sends `Camel*` headers upstream.
- **HTTP credential relay**: the producer no longer forwards a client's unchanged `Authorization`/`Cookie` received by the HTTP consumer. New `HTTPComponent.SetForwardInboundCredentials(true)` opt-in for pass-through proxies.
- **Template `json` XSS**: the function now uses `encoding/json` (HTML-safe escaping); non-string values used to be rendered raw into HTML.
- **Bounded producer cache**: the shared `ProducerCache` is an LRU bounded by `DefaultProducerCacheSize` (1000), configurable with `CamelContext.SetProducerCacheSize`. In-use and pinned (`GetProducer`) producers are never evicted.
- **TCP consumer**: new `maxConnections` option (default 1024), and a transient `Accept` error (e.g. `EMFILE`) no longer stops the consumer permanently.

### Fixed

- **SEDA/chan context**: consumers process exchanges under their own context instead of the sender's (already cancelled when an HTTP request enqueued the message and returned).
- **SEDA/chan shutdown**: `Stop` finishes the queued exchanges, bounded by the new `shutdownTimeout` option (default 30 s), instead of discarding them. SEDA warns when a queue is referenced again with different options.
- **HTTP producer body**: `io.Reader` bodies are sent; unsupported body types return an error instead of an empty request. Repeated response headers (`Set-Cookie`) are kept as `[]string`, and the consumer emits `[]string` headers as repeated headers.
- **Dynamic Router**: each step's reply now becomes the input of the next step (it was discarded).
- **ErrorHandler contract**: documented that `nil` means "retry the step"; retries are bounded by `MaxErrorHandlerRetries` (error wraps `ErrTooManyRetries`). New `DeadLetterHandled(true)` option.
- **File producer**: `Override`/`Fail`/`Ignore` writes go through a hidden temporary file published by rename/hard link, so consumers never read a partial file; `Close` errors are reported.
- **Template cache**: compiled templates are keyed by path and delimiters.
- ToD, RecipientList, RoutingSlip, DynamicRouter and WireTap reset `Out` consistently after propagating it; `ProducerCache.Stop` joins errors with `errors.Join`.

### Documentation

- `ValidatePath`, `ValidateRemotePath` and `IsSafePath` document that absolute paths are accepted (use `ValidatePathInDir` / `ConfineFileName` for header-derived paths).

## [v0.13.1] - 2026-09-12

Patch release for ZIP/tar.gz integrity and archive limits.

### Fixed

- **WinZip AES reader**: HMAC is computed over the bytes actually read (`p[:n]`) and verified when the payload is consumed, including `io.EOF` and empty entries (AE-2 has no CRC).
- **ZIP/tar marshal**: reject absolute entry names (`/etc/passwd`, Windows volumes), matching unmarshal zip-slip rules.
- **ZIP/tar unmarshal**: cap the number of entries (default 4096) and the total decompressed size (default 1 GiB) in addition to the existing 512 MiB per-entry limit.

### Notes

- Published components and observability modules now require core **v0.13.1**.

## [v0.13.0] - 2026-09-05

Feature release adding archive data formats and the STOMP/ActiveMQ component.

### Added

- **STOMP component** (`components/stomp`, new module): produce and consume messages with any STOMP 1.0/1.1/1.2 compliant broker (ActiveMQ, ActiveMQ Artemis, RabbitMQ, Apollo) via the `go-stomp` client.
  - URI-based destinations (`stomp://queue/orders?host=localhost:61613`), consumer ack modes `auto`/`client`/`client-individual` with retry-before-ack semantics, `forwardHeaders` allowlist and `allowHeaderOverride` opt-in for dynamic destinations.
  - SSL/TLS support: `ssl=true` with `sslCAFile`, `sslCertFile`/`sslKeyFile` (mutual TLS), `sslServerName` and a warned `sslInsecureSkipVerify` opt-in; TLS options validated at endpoint creation with traversal-safe paths.
  - Sensitive credential headers (`Authorization`, `Cookie`, ...) are never forwarded to the broker.
  - Tests run in-process against the embedded go-stomp server over plain TCP, TLS and mutual TLS, all passing with `-race`.
- **ZIP data format** (stdlib only): `MarshalZip`/`UnmarshalZip` DSL methods. Marshal writes standard deflate archives or WinZip AE-2 AES-256 encrypted entries (`ZipPassword`); Unmarshal reads plain, ZipCrypto (legacy `zip -P`) and WinZip AES archives. Single-entry archives unmarshal to raw content, multi-entry to `map[string][]byte`.
- **tar.gz data format** (stdlib only): `MarshalTarGz`/`UnmarshalTarGz` DSL methods with configurable gzip level and the same body conventions as ZIP.
- Security for both data formats: zip-slip/tar-slip entry names rejected on marshal and unmarshal, per-entry decompression size limits (512 MiB default) against zip bombs, constant-time password checks.

### Notes

- Published components and observability modules now require core **v0.13.0**.

## [v0.12.2] - 2026-09-05

Patch release for the Go 1.27 toolchain and lint configuration.

### Fixed

- Updated Staticcheck to 2026.2.1 and gosec to v2.29.0, both compatible with Go 1.27.
- Removed the MongoDB test's deprecation diagnostic while preserving its no-I/O regression scenario.
- Applied gofmt to all previously unformatted Go files.

### Notes

- This release contains no new runtime API or behavior changes compared with v0.12.1.

## [v0.12.0] - 2026-09-05

Reliability release addressing 18 code-review findings across routing, message delivery, concurrency and component lifecycle.

### Changed

- **Go 1.27.0** is the minimum and default version for all modules, examples and CI.
- Published components and observability modules now require core **v0.12.0**.
- **PollEnrich** requires `PollingEndpoint`, supported by `seda`, `chan` and custom implementations. It receives at most one message without draining or stopping the queue. Unsupported endpoints fail before consuming; zero timeout is non-blocking.
- **Kafka** retries failed processing before fetching another record, and retries failed commits without reprocessing successful records. Permanently failing deliveries block the consumer until handled or stopped.
- Parallel **Split, Multicast and RecipientList** aggregate serially in input order after all workers finish.

### Fixed

- Propagate pending output body and headers between route and nested pipeline steps, resetting `Out` so removed headers cannot reappear. `GetResponse()` combines the final input with pending output.
- Complete branch callbacks on success and failure, preserving idempotency rollback; recover aggregation strategy panics as `ErrProcessorPanic`.
- Copy mutable split parts before parallel processing and protect message-history cache reads.
- Release lifecycle locks before starting/stopping consumers, allowing status queries from processors without deadlocks.
- **Kafka:** resolve topics per message to avoid conflicting writer/message topic configuration; prevent cumulative commits from acknowledging failed deliveries.
- **MongoDB:** retain the input document's `_id` when `save` fails, preserving retry identity.
- **FTP:** reserve the connection exclusively for complete transfers and coordinate closure with concurrent sends.
- **Mail:** react to IMAP IDLE mailbox notifications, search existing messages and re-enter IDLE after processing.
- **Cron:** prevent new executions and join active jobs on stop or pause.
- **Net:** interrupt established connections on exchange cancellation and bound writes with the configured timeout.
- **Redis:** release consumer registrations on stop, recreate clients on restart and preserve clients still owned by other producers/consumers.
- **File:** preserve trailing-slash directory intent for destinations that do not yet exist.
- **Resequence:** keep the DSL usable after an invalid expression, reporting errors at build time.
- **Prometheus:** reuse compatible collectors when multiple routes independently register metrics.

### Validation

- Added 23 regression tests, including local protocol simulations and parallel branch cases.
- Build, vet and race-detector tests cover all 19 workspace modules; external production services are not required.

## [v0.11.0] - 2026-09-03

Hardening, robustness, and performance improvements across core EIPs and components following an in-depth code audit.

### Fixed

- **core (`ContentEnricher`)** — reuse started producers via `CamelContext.GetProducer(uri)` from the shared producer cache instead of creating, starting, and tearing down an endpoint and producer on every single exchange.
- **core (`TryProcessor`)** — exclude `ErrStopRouting` from catch clause pattern matching so that routing control signals (filters, stops) are not misclassified as caught exceptions; wrap try and catch execution in `safeProcess` to catch panics gracefully and ensure `DoFinally` always runs via deferred cleanup.
- **core (`DynamicRouter`, `RoutingSlip`, `RecipientList`)** — route debug logs through `LogDebug` instead of direct `slog.Debug` calls to honor custom loggers configured via `SetLogger`.
- **core (`RoundRobinStrategy`)** — change counter to `uint64` with `atomic.AddUint64` to prevent integer overflow and eliminate gosec G115 security warning.
- **components/openai** — enforce strict validation for `baseURL` requiring an absolute HTTP/HTTPS URL to align with Anthropic and Ollama component security standards.
- **components/sftp** — translate all remaining French error and log messages to English per project naming standards.

### Notes

- Fully backward-compatible; recommended upgrade for all users.

## [v0.10.1] - 2026-09-02

Packaging fix: the LICENSE files shipped in v0.10.0 were four words away from
the canonical Apache-2.0 text, so pkg.go.dev's license scanner
(`google/licensecheck`) matched only 6.4% of the file, classified every module
as `License: UNKNOWN` and hid the documentation behind license restrictions.

### Fixed

- **packaging** — every `LICENSE` (root and all nested modules) now contains
  the canonical Apache-2.0 text verbatim; the appendix keeps the copyright
  attribution. All files verify at 100% coverage with `google/licensecheck`.
- pkg.go.dev displays the Apache-2.0 badge and renders the package
  documentation again.

### Notes

- No API or behavioural changes; safe upgrade from v0.10.0.

## [v0.10.0] - 2026-08-31

Metadata and packaging release: per-module license files for pkg.go.dev
license detection, and a refreshed workspace checksum file.

### Fixed

- **packaging** — a copy of the Apache-2.0 `LICENSE` now ships inside every
  published module (`components/*`, `observability/*`). Each nested module is
  distributed as its own module zip, so the root LICENSE was never included
  and pkg.go.dev showed the submodules without a detected license.
- `go.work.sum` refreshed (`go work sync` leftover from the v0.8.0 module
  additions).

### Notes

- No API or behavioural changes; safe upgrade from v0.9.0.

## [v0.9.0] - 2026-08-29

Follow-up hardening after v0.8.0: context restart, nested DSL errors, HTTP
consumer headers, and remaining path/filter holes.

### Security

- **mongodb** — `update` now rejects empty filters as documents too (`{}`,
  empty `bson.M` / `bson.D`), not only a missing `"filter"` key.
  `allowUpdateAll=true` remains the opt-in for a mass update.
- **ftp / sftp / smb** — `CamelFileName` is joined with `JoinRemotePath`:
  absolute names and `..` traversal are rejected (SMB no longer uses
  `filepath.Join`, which discarded the share path for an absolute header).
- **file** — the consumer skips symbolic links and refuses files larger than
  32 MiB (`DefaultMaxBodySize`). Producer opens use `O_NOFOLLOW` on Unix.

### Fixed

- **CamelContext** — `Stop` / `GracefulStop` install a fresh root context, so
  `Start()` after `Stop()` works. `ProducerCache` refuses `Get` while stopping.
  `RemoveRoute` takes `startLock` so it cannot race `Start`.
- **HTTP consumer** — sets `CamelHttpMethod`, `CamelHttpPath`, `CamelHttpQuery`
  and `CamelHttpUrl`. A `From(http).To(http)` proxy forwards the inbound method
  instead of always POSTing. `https://` consumers are rejected (plaintext
  listen only; terminate TLS in front). In-flight handlers are joined on Stop.
- **DSL** — nested `Split` / `Loop` / `Multicast` / `Pipeline` / `DoTry`
  construction errors are copied to the parent at `End()`. `Choice.EndChoice()`
  attaches to the current container so Choice inside Split runs per part.
  Nested `RecipientList` with an unsupported expression type fails at `Build`.
- **Circuit breaker** — `ErrStopRouting` is not counted as a failure.
- **Saga** — actions run through `ProcessSafely`; `ErrStopRouting` does not
  trigger compensation.
- **Aggregator** — at least one completion condition (size, timeout, or
  predicate) is required.
- **PollEnrich** — Simple URI parse/eval errors are returned instead of
  falling through with the unresolved template.
- **net** — `wg.Add` for accepted connections happens under the mutex.
- **cron** — the simple-trigger goroutine is joined on `Stop`.
- **openai / anthropic** — HTTP client timeout is 60 seconds.

### Notes

- `Start()` after `Stop()` is supported. `https` remains producer-only for
  the HTTP component.

## [v0.8.0] - 2026-08-29

Code review hardening release: empty-filter MongoDB updates require opt-in,
orphan cron jobs and shared-connection double closes are fixed, and the
prometheus interceptor gained an injectable registry.

### Security

- **mongodb** — `update` with an empty (missing) filter is now rejected by
  default, matching the existing `remove` behaviour. A mass `UpdateMany` over
  the whole collection requires the new `allowUpdateAll=true` endpoint option.
- **file producer** — writes using `CamelFileName` are confined to the endpoint
  directory (`ValidatePathInDir`). Absolute names and `../` traversal are rejected.
- **timer / HTTP consumers** — route panics are recovered via `ProcessSafely` so
  they fail the exchange instead of the process (timer) or skipping the error handler (HTTP).

### Fixed

- **cron** — `Stop()` racing the delayed job registration could leave a job
  registered on the shared component scheduler that fires forever with no
  owner left to remove it. `Stop()` now joins the registration goroutine and
  a `stopped` flag (decided under the same mutex) wins the race.
- **nats** — the producer and consumer of one endpoint share the connection;
  each `Stop()` closed it under the other's feet (use-after-close). The close
  is now centralised in `closeConn()`.
- **ftp** — the producer no longer `ChangeDir`s before `Stor`. With a
  persistent connection (`disconnect=false`), the stateful `ChangeDir` made a
  relative path resolve against the *previous* file's directory, silently
  writing to the wrong remote directory. `Stor` now receives the full path.
- **ftp / sftp / smb** — in `fileExist=Append` mode, a partial read of the
  existing remote file aborted silently and the truncated content was
  re-uploaded (data corruption). A read error now fails the send.
- **mail (POP3)** — the `disconnect=false` option was dead code: the consumer
  disconnected after each poll unconditionally. It now keeps the connection
  between polls like the IMAP consumer, unless `disconnect=true`.
- **kafka** — consumer group commit errors were swallowed silently; a failed
  commit is now logged with topic/partition/offset so the coming redelivery is
  diagnosable.
- **observability** — the Prometheus and OpenTelemetry interceptors classified
  a *wrapped* `ErrStopRouting` as a failure (counter inflation, spurious span
  error events). Both now use `errors.Is`, matching the core.
- All error messages and logs in `sftp`, `smb`, `openai` and `mail` are now in
  English, per the repository language policy.
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
- Circuit breaker: idle failure window (OpenTimeout) resets the count;
  fallback panics are recovered.
- Resequencer buffer is bounded (default 10000); downstream drain uses
  ProcessSafely.
- Loop EIP rejects iteration counts above `MaxLoopIterations` (default 1e6).
- Logger is swapped atomically; template/log/otel use `exchange.ID` instead
  of the pointer address. `CamelTemplateLocale` added (typo alias kept).
- Direct producer recovers panics. Duplicate route IDs are logged.
- Dead-letter exponential backoff is capped at 24h. Error wrapping uses `%w`.
- `GetHeaders` / `GetProperties` return copies. `Route.SetID` updates `usedRouteIDs`.
- Stdlib components (`direct`, `timer`, `http`, `seda`, …) are registered by default.
- Split supports `ParallelProcessing`. Throttle is a token bucket.
- Resequencer delivers without holding the buffer mutex (re-entrant enqueue).
- File consumer scans existing files on start and watches `Write` as well as `Create`.

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

### Observability

- **prometheus** — new `MetricsWith(reg prometheus.Registerer)` variant
  registers the metric families on a caller-supplied registry instead of the
  process-wide default, for embedders that own their registry and for test
  isolation. `Metrics()` keeps using the default registry.
- **Simple language** — the parse cache is bounded by the new
  `MaxSimpleTemplateCacheEntries` (default 10000). Applications feeding
  unbounded dynamic expressions through the Simple language no longer leak one
  cache entry per distinct expression; trusted route configuration (inserted
  first) keeps hitting the cache.

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

[v0.12.2]: https://gitlab.com/tranchida/gocamel/-/tags/v0.12.2
[v0.12.0]: https://gitlab.com/tranchida/gocamel/-/tags/v0.12.0
[v0.11.0]: https://gitlab.com/tranchida/gocamel/-/tags/v0.11.0
[v0.10.1]: https://gitlab.com/tranchida/gocamel/-/tags/v0.10.1
[v0.10.0]: https://gitlab.com/tranchida/gocamel/-/tags/v0.10.0
[v0.9.0]: https://gitlab.com/tranchida/gocamel/-/tags/v0.9.0
[v0.8.0]: https://gitlab.com/tranchida/gocamel/-/tags/v0.8.0
[v0.7.0]: https://gitlab.com/tranchida/gocamel/-/tags/v0.7.0
[v0.6.0]: https://gitlab.com/tranchida/gocamel/-/tags/v0.6.0
[v0.5.0]: https://gitlab.com/tranchida/gocamel/-/tags/v0.5.0
[v0.4.0]: https://gitlab.com/tranchida/gocamel/-/tags/v0.4.0
[v0.3.0]: https://gitlab.com/tranchida/gocamel/-/tags/v0.3.0
[v0.2.0]: https://gitlab.com/tranchida/gocamel/-/tags/v0.2.0
