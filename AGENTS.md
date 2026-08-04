# AGENTS.md — GoCamel

Guidance for AI coding agents working in this repository. Assumes no prior knowledge of the project.

## Project overview

**GoCamel** is an enterprise integration framework for Go, inspired by
[Apache Camel](https://camel.apache.org/). It provides a fluent DSL for building
integration routes that connect systems and services, plus implementations of the
classic Enterprise Integration Patterns (EIPs).

- Module path: `gitlab.com/tranchida/gocamel`
- Language: Go 1.25 (see `go.mod`, `go.work`)
- License: Apache 2.0
- Hosted on GitLab; CI is GitLab CI (`.gitlab-ci.yml`)
- All code comments and documentation are written in **English** — keep it that way.
  User-facing docs exist in both English and French (see "Documentation" below).

### Core concepts (all in package `gocamel`, repo root)

- `CamelContext` (`context.go`) — container for routes, components and the registry;
  manages lifecycle (`Start`/`Stop`).
- `Route` / `RouteBuilder` (`route.go`, `route_builder*.go`) — a route is a pipeline of
  processors; `RouteBuilder` is the fluent DSL (`From("timer:tick?period=5s").SetBody(...).To(...)`).
  DSL construction errors are accumulated and surfaced at `Build()` time.
- `Exchange` (`exchange.go`) — in-flight container with `In`/`Out` messages, properties,
  and the well-known `Camel*` header constants (e.g. `CamelFileName`, `CamelHttpResponseCode`).
- `Message` (`message.go`) — body (`any`) + headers, with typed accessors
  (`GetBodyAsString`, `GetHeaderAsInt`, ...).
- `Processor` (`route.go`) — the single-method interface `Process(*Exchange) error` that
  everything in a route implements. `ProcessorFunc` adapts plain functions.
- `Component` / `Endpoint` / `Producer` / `Consumer` (`endpoint.go`) — a Component creates
  Endpoints from URIs; an Endpoint creates Producers (sinks) and Consumers (sources).
- `ComponentRegistry` (`registry.go`) — name → object registry; also used for bean lookup
  (`ProcessRef`).
- `ProcessSafely` (`route.go`) — wraps processor invocation so a panic in user code fails
  the exchange (error wrapping `ErrProcessorPanic`) instead of crashing the process.
  Consumers that run user code on their own goroutine must use it.

## Repository layout

This is a **multi-module Go workspace** (`go.work`). Each directory with a `go.mod` is an
independent module and must build, vet and test on its own:

- `.` (root module `gitlab.com/tranchida/gocamel`) — the core: routing engine, EIPs,
  Simple language, data formats, and the **stdlib-only components**: `direct`, `seda`,
  `chan`, `timer`, `http`, `exec`, `template`, `sql`, `sql-stored`, `net` (raw TCP/UDP
  sockets).
- `components/*` — components with third-party dependencies, one module each:
  `cron`, `file`, `ftp`, `mail`, `mongodb`, `nats`, `openai`, `redis`, `sftp`, `smb`,
  `telegram`. Users pull in only what they need. Keep it that way: **never add a
  third-party dependency to the core module for an optional component** — put it in
  `components/<name>/` with its own `go.mod` instead.
- `observability/prometheus`, `observability/otel` — metrics and tracing, exposed as
  route interceptors (`Intercept(gocamelprom.Metrics())`).
- `hugo-site/` — Hugo documentation site (Relearn theme), deployed to
  https://gocamel.tranchida.cloud/. See `hugo-site/` for build instructions.

The core module is intentionally near-dependency-free. Its only direct requirements are
`github.com/stretchr/testify` (tests) and `github.com/mattn/go-sqlite3` (the `sql`
component, which requires **CGO** to build/test).

### EIPs implemented (root package)

Aggregator, Splitter, Multicast, Choice, Filter, Recipient List, Routing Slip, Wire Tap,
Loop, Stop, ToD, Saga, Transform, Enricher, Resequencer, Throttle, Delay, Circuit Breaker,
Idempotent Consumer, Claim Check, Load Balancer, Dynamic Router, Message History, Dead
Letter Channel / error handlers, plus repositories (memory / SQL / Redis-backed) for
aggregation and idempotency state.

### Simple language

Camel's Simple expression language is implemented across `simple_language*.go`
(`${body}`, `${header.name}`, comparisons, functions like `${date:now}`, `${uuid}`).
Predicates in the DSL (`When(...)`, completion conditions, etc.) are Simple expressions.

## Build and test commands

Always run from the directory of the module you changed. There is no Makefile.

```bash
# Core module (repo root) — needs CGO for go-sqlite3
go build ./...
go vet ./...
go test ./...

# Any other module, e.g. components/redis
cd components/redis && go build ./... && go vet ./... && go test ./...

# CI parity: build/vet/test EVERY module in the workspace (what .gitlab-ci.yml does)
for mod in $(find . -name go.mod -not -path '*/.*'); do
  dir=$(dirname "$mod")
  (cd "$dir" && go build ./... && go vet ./... && go test ./...)
done
```

Run a single test: `go test -run TestName ./...` from the module directory.

Do not use `go work sync` casually or add `replace` directives; the workspace file already
wires the local modules together.

## Code style guidelines

- Standard Go idioms: `gofmt`-formatted, effective-Go naming. Comments/doc comments in English.
- Flat package layout in the root: one concept per file (`aggregator.go`,
  `circuit_breaker.go`, ...), with its tests in the sibling `*_test.go` file.
  New EIPs/components follow the same naming: `<name>.go` + `<name>_test.go`.
- Public API is Camel-flavoured: fluent builder methods returning `*RouteBuilder`,
  `Camel*` header/property constants, interfaces `Component`/`Endpoint`/`Producer`/`Consumer`/`Processor`.
- Errors: return wrapped errors (`fmt.Errorf("...: %w", err)`); sentinel errors such as
  `ErrStopRouting`, `ErrProcessorPanic`, `ErrHTTPStatus` are checked with `errors.Is`.
- Concurrency: contexts/structs document which mutex guards which field; consumers and
  async EIPs run their own goroutines and must honour `context.Context` cancellation and
  call `ProcessSafely` when invoking user processors.
- Route validation: processors may implement the optional `Validator` interface so
  misconfiguration surfaces at `Build()`/`Validate()` time, not on the first message.
- Optional behaviour on builders uses `SetX(...)` chainable methods
  (e.g. `SetCompletionSize`, `SetCompletionTimeout`).

## Testing instructions

- Tests use [`stretchr/testify`](https://github.com/stretchr/testify) (`assert` / `require`)
  and live in `*_test.go` next to the code, in the same package.
- Most tests are pure unit/integration tests over in-memory components (`direct`, `seda`,
  `chan`, `timer`) — no external services needed for the core module.
- Component tests avoid real infrastructure where possible: the Redis component tests spin
  up an in-memory [`miniredis`](https://github.com/alicebob/miniredis) server. Prefer this
  style (fakes/in-memory servers) over tests that need real Redis/NATS/MongoDB/etc.
- Core module tests require CGO (go-sqlite3). On macOS/Linux with a C toolchain this works
  out of the box.
- CI (GitLab) runs per module: `go build`, `go vet`, `go test -coverprofile=...`, aggregates
  coverage (no enforced threshold), and runs a **non-blocking** lint job with
  `staticcheck` (pinned 2025.1.1) and `gosec` (pinned v2.22.4, tests excluded). Lint
  failures don't gate merges today, but don't add new findings.

## Security considerations

Security is a first-class concern in this codebase (`security.go`, plus review-driven
hardening). When touching I/O, HTTP, SQL or file paths, preserve these guarantees:

- **Path traversal protection** — `ValidatePath`, `ValidatePathInDir`, `ValidateRemotePath`,
  `IsSafePath` must be applied to any path derived from message/user data; symlink escapes
  are resolved.
- **SQL** — queries are trusted configuration, never interpolated with exchange data;
  dynamic values go through positional `?` parameter binding. The `CamelSqlQuery` header is
  ignored unless the endpoint opts in with `allowHeaderOverride=true`. Keep it that way.
- **HTTP** — the consumer never reflects inbound request headers (`Cookie`,
  `Authorization`, ...) into responses; request/response bodies are size-bounded
  (32 MiB default); the producer treats status ≥ 300 as an error wrapping `ErrHTTPStatus`
  (opt-out via `SetThrowExceptionOnFailure(false)`).
- **Panic containment** — a panicking processor fails its exchange, never the process
  (use `ProcessSafely` from consumer goroutines).
- **Input sanitization** — `SanitizeInput` strips null bytes/control characters.
- Never execute unsanitized external commands; the `exec` component is for trusted config only.
- The HTTP consumer ships with no authentication — routes exposed to untrusted networks are
  expected to add middleware via `SetMiddleware`.

## Documentation and CI/deployment

- Docs are built with **Hugo** + the [Relearn](https://github.com/McShelby/hugo-theme-relearn)
  theme, sources in `hugo-site/` (`hugo.toml`, content in `hugo-site/content/en/` and
  `hugo-site/content/fr/`).
- Documentation is bilingual: `README.md`/`README.fr.md`, `hugo-site/content/en/`,
  `hugo-site/content/fr/`. **When you change documented behaviour, update both languages.**
- The live site is self-hosted on a VPS with Caddy at
  https://gocamel.tranchida.cloud/. Build and deploy with `hugo-site/deploy.sh`.
- The `convert_docs.py` script in `hugo-site/` converts MkDocs Markdown to Hugo format
  (admonitions → GitHub callouts, front matter, weights).
- There is no binary release process — this is a library; users `go get` the modules.

## Commit conventions

Recent history uses conventional-commit-style messages (`fix:`, `fix(security):`,
`docs:`, ...). Follow that style.

## Code intelligence tooling

This repository is indexed by **CodeGraph** (see `.codegraph/`). Prefer `codegraph_explore`
for exploring flows, understanding architecture, and assessing blast radius before
editing exported symbols. The index auto-syncs via a file watcher.
