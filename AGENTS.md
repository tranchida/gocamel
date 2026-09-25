# AGENT.md / AGENTS.md — GoCamel Development & Go Best Practices Guide

Guidance for AI coding agents and LLMs working in the **GoCamel** repository. Assumes no prior knowledge of the project.

---

## 1. Project Overview & Core Architecture

**GoCamel** is an enterprise integration framework for Go, inspired by [Apache Camel](https://camel.apache.org/). It provides a fluent DSL for building integration routes that connect heterogeneous systems, plus implementations of the classic Enterprise Integration Patterns (EIPs).

- **Module path**: `gitlab.com/tranchida/gocamel`
- **Language**: Go 1.27 (see `go.mod`, `go.work`)
- **License**: Apache 2.0
- **Language / Naming Policy**: All code, comments, docstrings, variable names, error messages, logs, and commit messages **MUST be written in English**. (User-facing docs are bilingual EN/FR).

### Core Concepts (package `gocamel`, repo root)

- `CamelContext` (`context.go`): Container for routes, components, and registry; manages lifecycle (`Start`/`Stop`).
- `Route` / `RouteBuilder` (`route.go`, `route_builder*.go`): A route is a pipeline of processors; `RouteBuilder` is the fluent DSL (`From("timer:tick?period=5s").SetBody(...).To(...)`).
- `Exchange` (`exchange.go`): In-flight message container holding `In` and `Out` `Message`s, properties, and `Camel*` header constants.
- `Message` (`message.go`): Payload body (`any`) + headers (`map[string]any`) with typed accessors (`GetBodyAsString`, `GetHeaderAsInt`, etc.).
- `Processor` (`route.go`): Single-method interface `Process(*Exchange) error`. Everything in a route pipeline implements `Processor`.
- `Component` / `Endpoint` / `Producer` / `Consumer` (`endpoint.go`):
  - `Component`: Factory for endpoints (`CreateEndpoint(uri string)`).
  - `Endpoint`: Factory for producers (sinks) and consumers (sources).
  - `Producer`: Sends exchanges to external targets (`Process(*Exchange) error`).
  - `Consumer`: Sources exchanges and pushes them into the route pipeline (`Start(ctx)` / `Stop()`).
- `ComponentRegistry` (`registry.go`): Name-to-object registry for beans and components (`ProcessRef`).
- `ProcessSafely` (`route.go`): Safely executes processors, catching panics and converting them into `ErrProcessorPanic` errors.

---

## 2. Workspace & Multi-Module Layout (`go.work`)

GoCamel is structured as a **multi-module Go workspace** (`go.work`). Each module must build, vet, and test independently:

```text
gocamel/
├── go.work / go.work.sum
├── go.mod / go.sum                 # Root core module (gitlab.com/tranchida/gocamel)
├── *.go / *_test.go               # Core routing engine, EIPs, Simple language, DataFormats, stdlib components
├── components/                    # Standalone optional component modules
│   ├── cron/                      # Cron component (robfig/cron)
│   ├── file/                      # File component (fsnotify)
│   ├── ftp/                       # FTP component (jlaffaye/ftp)
│   ├── grpc/                      # gRPC component (google.golang.org/grpc)
│   ├── kafka/                     # Kafka component (segmentio/kafka-go)
│   ├── mail/                      # Mail component (emersion/go-smtp, go-imap)
│   ├── mongodb/                   # MongoDB component (go.mongodb.org/mongo-driver)
│   ├── nats/                      # NATS component (nats-io/nats.go)
│   ├── openai/                    # OpenAI component (sashabaranov/go-openai)
│   ├── anthropic/                 # Anthropic component (anthropic-sdk-go, Claude Messages API)
│   ├── ollama/                    # Ollama component (ollama/api, local models)
│   ├── redis/                     # Redis component (redis/go-redis)
│   ├── sftp/                      # SFTP component (pkg/sftp)
│   ├── smb/                       # SMB component (hirochachacha/go-smb2)
│   ├── stomp/                     # STOMP component (go-stomp, ActiveMQ/JMS)
│   └── telegram/                  # Telegram component (go-telegram-bot-api)
├── observability/                 # Metrics & Tracing modules
│   ├── prometheus/                # Prometheus metrics interceptor
│   └── otel/                      # OpenTelemetry tracing interceptor
└── hugo-site/                     # Bilingual documentation website (Hugo + Relearn theme)
```

### ⚠️ Strict Dependency Rules

1. **Zero Third-Party Dependencies in Core**: The root module (`.`) is strictly stdlib-only.
   - Allowed exceptions in root `go.mod`: `github.com/stretchr/testify` and `modernc.org/sqlite` (pure-Go SQLite driver used in tests, **no CGO required**).
   - **NEVER** add third-party libraries to the root `go.mod` for optional components or formats.
2. **Component Isolation**: Any component requiring an external package **must** live in `components/<name>/` with its own independent `go.mod` referencing `gitlab.com/tranchida/gocamel`.
3. **Stdlib-only Components**: Reside directly in the core root module: `direct`, `seda`, `chan`, `timer`, `http`, `exec`, `template`, `sql`, `sql-stored`, `net` (TCP/UDP).

---

## 3. Go Coding Idioms & Project Standards

### Flat Package Layout in Core
- The core module uses a flat structure in package `gocamel`.
- One concept per file: `<feature>.go` with tests in sibling `<feature>_test.go` (e.g. `aggregator.go` + `aggregator_test.go`).
- Do not create unnecessary subpackages in core; keep the public API cohesive and discoverable.

### Naming Conventions
- Standard Go conventions: camelCase for unexported identifiers, PascalCase for exported identifiers.
- Well-known Camel headers and exchange properties use the `Camel*` prefix constant format:
  ```go
  const (
      CamelFileName         = "CamelFileName"
      CamelHttpResponseCode = "CamelHttpResponseCode"
      CamelRedisCommand     = "CamelRedisCommand"
  )
  ```
- Factory functions use `New<Type>()` (e.g., `NewCamelContext()`, `NewRouteBuilder()`).

### Fluent DSL & Error Accumulation
- DSL methods on `*RouteBuilder` must be chainable and return `*RouteBuilder`.
- **Never panic** during route building. If an invalid argument or malformed expression is passed to a DSL method, record the error in `rb.errors` and return `rb`.
- Surfaced errors at `Build()` / `Validate()` time:
  ```go
  // Validate checks the route pipeline and all processor validations
  func (r *Route) Validate() error {
      var errs []error
      for _, p := range r.processors {
          if v, ok := p.(Validator); ok {
              if err := v.Validate(); err != nil {
                  errs = append(errs, err)
              }
          }
      }
      return errors.Join(errs...)
  }
  ```

---

## 4. Concurrency, Goroutines & Panic Safety

### 1. Panic Containment with `ProcessSafely`
Processors execute arbitrary user-defined code. When invoked from library-owned background goroutines (SEDA workers, async Multicast branches, Splitter parallel threads, WireTap, polling consumers, Timers), an unrecovered panic would crash the entire host process.

- **Rule**: Always call `ProcessSafely(processor, exchange)` from background goroutines.
- `ProcessSafely` recovers panics, logs the stack trace, and returns an error wrapping `ErrProcessorPanic`.
- The route's `ErrorHandler` can then process the error gracefully.

```go
// In consumer or background worker:
if err := ProcessSafely(c.processor, exchange); err != nil {
    LogError("processing failed", "error", err)
}
```

### 2. Exchange Cloning for Concurrent Branches
The `Exchange` struct is mutable. Sharing the same `Exchange` instance across multiple concurrent goroutines causes data races on headers, properties, and messages.

- **Rule**: When routing an exchange across concurrent paths (e.g., `Multicast` with parallel processing, `Splitter` with worker pools, `WireTap`), **always clone the exchange** using `exchange.Copy()` or `exchange.CopyWithoutProperties()`.

```go
// Safe parallel dispatch:
copyExchange := exchange.Copy()
go func(ex *Exchange) {
    _ = ProcessSafely(branchProcessor, ex)
}(copyExchange)
```

### 3. Mutex Discipline & Documentation
- Document which fields are guarded by which mutexes:
  ```go
  type CamelContext struct {
      routesLock    sync.RWMutex // Protects routes slice and routeCounter
      routes        []*Route
      startLock     sync.RWMutex // Protects started lifecycle state
      started       bool
  }
  ```
- Use `sync.RWMutex` for read-heavy state and `sync.Mutex` for exclusive mutations.
- Minimize critical section duration; never perform blocking I/O or call external processors while holding a lock.

### 4. Idempotent Resource Teardown with `sync.Once`
When an endpoint's client/connection is shared between a Producer and a Consumer, both might attempt to close it upon `Stop()`.
- **Rule**: Guard client closure with `sync.Once` to prevent double-close panics or connection races:
  ```go
  func (e *RedisEndpoint) closeClient() error {
      var err error
      e.closeOnce.Do(func() {
          err = e.client.Close()
      })
      return err
  }
  ```

### 5. Goroutine Lifecycle & Leak Prevention
- Always respect `ctx.Done()` in all loops, channel selections, and polling consumers.
- Use `sync.WaitGroup` to wait for all background workers during `Stop()`.
- Use a dedicated `done` channel or context cancellation to trigger worker exit.

---

## 5. Error Handling Guidelines

### Wrapped Errors
- Always wrap underlying errors with descriptive context:
  ```go
  return fmt.Errorf("failed to connect to redis host %s: %w", addr, err)
  ```

### Sentinel Errors
- Check sentinel errors using `errors.Is(err, Target)`:
  - `ErrStopRouting`: Signals normal routing cessation (e.g., Filter rejected message, Aggregation completed) without marking the exchange as failed.
  - `ErrProcessorPanic`: Identifies a caught panic from user code.
  - `ErrHTTPStatus`: Identifies HTTP responses with status code ≥ 300.

### Error Propagation
- Do not swallow errors silently. Allow errors to propagate so that route-level error handlers (`ErrorHandler`, `DeadLetterChannel`, `OnException`) can apply retry policies and fallbacks.

---

## 6. Security & Hardening Rules (Critical)

Security is a primary concern in GoCamel (`security.go`). Follow these non-negotiable rules:

### 1. Path Traversal Protection
Whenever file paths, directory names, or file names are derived from message headers (such as `CamelFileName`) or user-supplied URI parameters:
- **Rule**: Validate paths using `gocamel.ValidatePath()`, `gocamel.ValidatePathInDir()`, `gocamel.ValidateRemotePath()`, or `gocamel.IsSafePath()`.
- Never trust raw path strings. Reject paths escaping the root (`..`), containing embedded null bytes (`\x00`), or escaping via symbolic links.

### 2. SQL Injection Prevention
- SQL queries in endpoints are trusted configuration.
- **Rule**: Dynamic values must strictly use parameter binding with `?` placeholders.
- The `CamelSqlQuery` header override is ignored by default and is only permitted if the endpoint explicitly opts in with `allowHeaderOverride=true`.

### 3. Input Sanitization
- External strings used in file systems or headers must be sanitized via `gocamel.SanitizeInput(s)` to strip null bytes and unwanted control characters.

### 4. HTTP & Resource Limits
- **Header Isolation**: The HTTP consumer must never echo inbound security headers (`Cookie`, `Authorization`, etc.) into outbound responses.
- **Bounded Buffers**: HTTP request/response bodies are size-bounded (default 32 MiB) to prevent memory exhaustion (DoS).
- **Exec Safety**: The `exec` component is strictly for trusted configuration. Never concatenate unvalidated user headers into shell commands.

---

## 7. Component & Endpoint Implementation Pattern

When implementing or modifying a GoCamel component, adhere to the 4-pillar contract:

```go
// 1. Component
type MyComponent struct{}
func (c *MyComponent) CreateEndpoint(uri string) (gocamel.Endpoint, error) {
    u, err := gocamel.ParseURI(uri)
    if err != nil {
        return nil, fmt.Errorf("invalid URI: %w", err)
    }
    timeout := gocamel.GetConfigDuration(u, "timeout", 5*time.Second)
    return &MyEndpoint{uri: uri, timeout: timeout}, nil
}

// 2. Endpoint
type MyEndpoint struct {
    uri     string
    timeout time.Duration
}
func (e *MyEndpoint) URI() string { return e.uri }
func (e *MyEndpoint) CreateProducer() (gocamel.Producer, error) { return &MyProducer{endpoint: e}, nil }
func (e *MyEndpoint) CreateConsumer(p gocamel.Processor) (gocamel.Consumer, error) {
    return &MyConsumer{endpoint: e, processor: p}, nil
}

// 3. Producer
type MyProducer struct{ endpoint *MyEndpoint }
func (p *MyProducer) Start(ctx context.Context) error { return nil }
func (p *MyProducer) Stop() error { return nil }
func (p *MyProducer) Process(exchange *gocamel.Exchange) error {
    // Process exchange and populate In/Out message
    return nil
}

// 4. Consumer
type MyConsumer struct {
    endpoint  *MyEndpoint
    processor gocamel.Processor
    cancel    context.CancelFunc
    wg        sync.WaitGroup
}
func (c *MyConsumer) Start(ctx context.Context) error {
    ctx, c.cancel = context.WithCancel(ctx)
    c.wg.Add(1)
    go c.run(ctx)
    return nil
}
func (c *MyConsumer) Stop() error {
    if c.cancel != nil { c.cancel() }
    c.wg.Wait()
    return nil
}
func (c *MyConsumer) run(ctx context.Context) {
    defer c.wg.Done()
    // Polling / listening loop respecting ctx.Done() and using gocamel.ProcessSafely
}
```

---

## 8. Testing Standards & Guidelines

- **Testing Library**: Use `github.com/stretchr/testify` (`assert` and `require`).
  - Use `require.*` for setup preconditions (e.g. `require.NoError(t, err)`).
  - Use `assert.*` for actual outcome assertions.
- **In-Memory First**:
  - Core module tests must run in memory using `direct`, `seda`, `chan`, `timer`, or stdlib test servers (`httptest.Server`, `net.Pipe`).
  - Component tests must avoid external live infrastructure where possible (e.g. use `miniredis` for Redis tests, ephemeral temporary directories for File/SFTP tests).
- **Race Detector**: All tests **must pass with the race detector enabled**:
  ```bash
  go test -race ./...
  ```
- **Asynchronous Testing**:
  - Do not use arbitrary `time.Sleep` to wait for async messages.
  - Use `assert.Eventually(t, func() bool { ... }, timeout, interval)` or channel receives with a `select` + `time.After` timeout.
- **Cleanup**: Always clean up resources using `t.Cleanup()` or `defer` (stop contexts, close servers, delete temp files).

---

## 9. Build, Test & CI Commands

Always run commands from the specific module directory you modified:

```bash
# Core module (repo root) — CGO-free (pure-Go modernc.org/sqlite)
go build ./...
go vet ./...
go test -race ./...

# Specific component module, e.g. components/redis
cd components/redis
go build ./...
go vet ./...
go test -race ./...

# CI Parity: build, vet, and test every module in the workspace
for mod in $(find . -name go.mod -not -path '*/.*'); do
  dir=$(dirname "$mod")
  echo "==> Testing module in $dir"
  (cd "$dir" && go build ./... && go vet ./... && go test -race ./...)
done

# Linting (matches GitLab CI non-blocking jobs)
staticcheck ./...
gosec -exclude-dir=hugo-site ./...
```

---

## 10. Documentation & Commit Conventions

- **Conventional Commits**: Use semantic commit prefixes: `feat:`, `fix:`, `fix(security):`, `docs:`, `test:`, `refactor:`, `perf:`, `chore:`.
- **Bilingual Documentation Policy**:
  - All user-facing documentation is maintained in both **English** and **French**.
  - When adding or changing documented behavior, update both:
    1. Root READMEs: `README.md` and `README.fr.md`.
    2. Hugo site: `hugo-site/content/en/` and `hugo-site/content/fr/`.
- **Code Intelligence**: The repository is indexed with **CodeGraph** (`.codegraph/`). Use `codegraph_explore` to inspect symbol definitions, call hierarchies, and blast radius before modifying core interfaces.
