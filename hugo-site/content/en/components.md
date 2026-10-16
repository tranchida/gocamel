+++
title = "Components Reference"
weight = 12
+++
## Overview

Complete reference of all available GoCamel components. Components provide connectivity to various systems and services.

## Core Components

### Direct

In-memory synchronous routing between routes in the same context.

```go
// Consumer: receives from direct endpoint
// Producer: sends to direct endpoint
builder.From("direct:start").To("direct:process")
```

Endpoints are identified by **name only** — query parameters are ignored for identity, so
`direct:start` and `direct:start?x=1` resolve to the same endpoint. Only one consumer per
endpoint is allowed.

---

### Timer

Simple periodic triggering.

```go
builder.From("timer:tick?period=5s")
```

| Option        | Type     | Default | Description                        |
|---------------|----------|---------|------------------------------------|
| `period`      | Duration | `1s`    | Period between triggers            |
| `repeatCount` | int      | `0`     | Number of repetitions (0=infinite) |
| `fixedRate`   | bool     | `false` | Fixed-rate vs fixed-delay mode     |

---

### Mock

Producer-only test endpoint (registered by default) that records every exchange sent to it.
Combine it with `Route.AdviceWith` to replace real endpoints in a built route before `Start()`.

```go
route, _ := ctx.CreateRouteBuilder().
    From("direct:in").
    To("kafka:orders").
    Build()

// Swap the real endpoint for a mock before Start.
_ = route.AdviceWith(map[string]string{"kafka:orders": "mock:orders"})

mock, _ := ctx.GetMockEndpoint("mock:orders")
mock.ExpectedMessageCount(2)
_ = ctx.Start()
// ... send two exchanges to direct:in ...
if err := mock.Wait(time.Second); err != nil {
    t.Fatal(err) // "expected at least 2 messages but received 1 ..."
}
body := mock.Body(0)
```

| Method                        | Description                                                           |
|-------------------------------|-----------------------------------------------------------------------|
| `MessageCount()`              | Number of exchanges received                                          |
| `Message(i)`                  | Snapshot of the i-th exchange (arrival order), `nil` if out of range  |
| `Body(i)` / `Header(i, name)` | In body / header of the i-th exchange                                 |
| `ExpectedMessageCount(n)`     | Expect **at least** `n` exchanges (more is accepted)                  |
| `Wait(timeout)`               | Block until the expectation is met; error with counts on timeout      |
| `Reset()`                     | Clear received exchanges and the expectation                          |

- Endpoints are identified by **name only** and cached per context: `mock:out` in a route and
  `GetMockEndpoint("out")` are the same endpoint.
- Each exchange is recorded as a `DeepCopy` taken at send time (original exchange ID kept), so
  later mutation of the live exchange does not change the snapshot.
- Mock endpoints have no consumer: `From("mock:x")` fails.

`AdviceWith` substitutes exact URIs of static `To()` producers, including multicast `To(a, b)`
and Choice / LoadBalancer / Saga branches. It does **not** substitute the `From()` endpoint,
`ToD` templates or other dynamic sends (RecipientList, RoutingSlip, DynamicRouter, WireTap,
Enrich, DeadLetterChannel). Unknown URIs are ignored; substitution is a single non-transitive
pass. It returns an error while the route is started (it is allowed again after `Stop()`).

---

## File Transfer Components

### File

Local file system operations.

```go
// Consumer (read)
builder.From("file://input?delete=true")

// Producer (write)
builder.To("file://output")
```

| Option                  | Type     | Default    | Description                                                                   |
|-------------------------|----------|------------|-------------------------------------------------------------------------------|
| `delete`                | bool     | `false`    | Delete after processing                                                       |
| `noop`                  | bool     | `false`    | Do not move/delete file                                                       |
| `include`               | string   | `""`       | Include file pattern                                                          |
| `exclude`               | string   | `""`       | Exclude file pattern                                                          |
| `preMove`               | string   | `""`       | Move file before processing                                                   |
| `move`                  | string   | `""`       | Move file after processing                                                    |
| `moveFailed`            | string   | `""`       | Move file on failure                                                          |
| `readLock`              | string   | `changed`  | Read lock strategy (`none`, `changed`, `rename`, `markerFile`)                |
| `readLockTimeout`       | Duration | `10s`      | Timeout waiting to acquire read lock                                          |
| `readLockCheckInterval` | Duration | `100ms`    | Interval between read lock checks                                             |
| `readLockMinLength`     | int64    | `0`        | Minimum file size in bytes                                                    |
| `readLockMinAge`        | Duration | `0`        | Minimum file age                                                              |
| `maxSeenEntries`        | int      | `100000`   | Files remembered to skip duplicate events; oldest evicted first (`0` = unlimited) |
| `fileExist`             | string   | `Override` | Behavior if destination file exists (`Override`, `Append`, `Fail`, `Ignore`) |

#### Headers

##### Set on Consumer Exchanges
- `CamelFileName`: The relative file name
- `CamelFilePath`: The full path of the file
- `CamelFileLength`: The file size in bytes
- `CamelFileLastModified`: Last modification timestamp (`time.Time`)

##### Consumed by Producer
- `CamelFileName`: Overrides destination file name when the producer URI points to a directory. Must be a **relative** path: empty names, absolute paths, Windows volume names, and `..` components that escape the endpoint directory are rejected.

The consumer skips symbolic links and refuses files larger than 32 MiB
(`DefaultMaxBodySize`).

#### Read lock

GoCamel provides multiple read lock strategies to ensure files are fully written and exclusive before processing:

- `readLock=changed`: The consumer samples file size and modification time over `readLockCheckInterval` and reads once two consecutive samples match within `readLockTimeout`.
- `readLock=rename`: Renames the file to an exclusive temporary name (`.camelExclusiveReadLock`) during processing.
- `readLock=markerFile`: Creates a `.camelLock` marker file while processing and deletes it upon completion.
- `readLock=none`: Disables read lock verification.

> [!tip] Prefer an atomic handoff
> The `changed` read lock is a heuristic: a writer that stalls for longer than the
> sampling interval mid-write can still be observed as stable. For a
> guaranteed handoff, have the producer write to a temporary name and
> `rename` it into the watched directory — rename is atomic.


---

### FTP / FTPS

File transfer via FTP protocol.

```go
// Consumer
builder.From("ftp://host:21/incoming?username=admin&readLock=changed")

// Producer
builder.To("ftp://host:21/outgoing?binary=true")
```

**Environment Variables:**

- `FTP_USERNAME` - Username
- `FTP_PASSWORD` - Password

| Option                  | Type     | Default | Description                                                        |
|-------------------------|----------|---------|--------------------------------------------------------------------|
| `username`              | string   | `""`    | FTP username                                                       |
| `password`              | string   | `""`    | FTP password                                                       |
| `binary`                | bool     | `true`  | Binary transfer mode                                               |
| `passiveMode`           | bool     | `true`  | Use passive mode                                                   |
| `maxMessageSize`        | int      | `0`     | Max bytes a polled file may hold (0 = unlimited)                   |
| `readLock`              | string   | `none`  | Read lock strategy (`none`, `changed`, `rename`, `markerFile`)     |
| `readLockTimeout`       | Duration | `10s`   | Timeout waiting to acquire read lock                               |
| `readLockCheckInterval` | Duration | `1s`    | Interval between read lock checks                                  |
| `readLockMinLength`     | int64    | `0`     | Minimum file size in bytes                                         |
| `readLockMinAge`        | Duration | `0`     | Minimum file age                                                   |

With `fileExist=Append`, the producer downloads the existing remote file and
uploads the concatenation. A partial download fails the send instead of
silently re-uploading truncated content.

When the URI path is a directory, `CamelFileName` is joined with
`JoinRemotePath`: absolute names and directory traversal are rejected.

---

### SFTP

Secure file transfer via SSH.

```go
builder.From("sftp://host:22/data?username=scott&readLock=changed&readLockTimeout=15s")
```

**Authentication Methods:**

- Password: via `password` parameter or `SFTP_PASSWORD` env var
- Key-based: via `privateKeyFile` parameter or `SFTP_PRIVATE_KEY_FILE`

| Option                  | Type     | Default | Description                                                        |
|-------------------------|----------|---------|--------------------------------------------------------------------|
| `username`              | string   | `""`    | SSH username                                                       |
| `password`              | string   | `""`    | SSH password                                                       |
| `privateKeyFile`        | string   | `""`    | Path to private key                                                |
| `privateKeyPassphrase`  | string   | `""`    | Private key passphrase                                             |
| `maxMessageSize`        | int      | `0`     | Max bytes a polled file may hold (0 = unlimited)                   |
| `readLock`              | string   | `none`  | Read lock strategy (`none`, `changed`, `rename`, `markerFile`)     |
| `readLockTimeout`       | Duration | `10s`   | Timeout waiting to acquire read lock                               |
| `readLockCheckInterval` | Duration | `1s`    | Interval between read lock checks                                  |
| `readLockMinLength`     | int64    | `0`     | Minimum file size in bytes                                         |
| `readLockMinAge`        | Duration | `0`     | Minimum file age                                                   |

As with FTP, `fileExist=Append` concatenates onto the existing remote file and
aborts the send if reading it fails partway. `CamelFileName` is confined the
same way as FTP (`JoinRemotePath`).

---

### SMB

Windows/Samba share access.

```go
builder.From("smb://server/share/folder?username=user&readLock=changed")
```

| Option                  | Type     | Default  | Description                                                        |
|-------------------------|----------|----------|--------------------------------------------------------------------|
| `username`              | string   | `""`     | Domain username                                                    |
| `password`              | string   | `""`     | Domain password                                                    |
| `share`                 | string   | required | Share name                                                         |
| `maxMessageSize`        | int      | `0`      | Max bytes a polled file may hold (0 = unlimited)                   |
| `readLock`              | string   | `none`   | Read lock strategy (`none`, `changed`, `rename`, `markerFile`)     |
| `readLockTimeout`       | Duration | `10s`    | Timeout waiting to acquire read lock                               |
| `readLockCheckInterval` | Duration | `1s`     | Interval between read lock checks                                  |
| `readLockMinLength`     | int64    | `0`      | Minimum file size in bytes                                         |
| `readLockMinAge`        | Duration | `0`      | Minimum file age                                                   |

The same `fileExist=Append` semantics as FTP/SFTP apply. `CamelFileName` is
confined with `JoinRemotePath` (absolute names and `..` are rejected; the
producer does not use `filepath.Join`, which would discard the share path
when the header is absolute).

---

## Network Components

### HTTP

HTTP server and client support.

```go
// Consumer (HTTP server) — plaintext listen only
builder.From("http://localhost:8080/api")

// Producer (HTTP client)
builder.To("http://example.com/webhook")
```

The `https` scheme is **producer-only**. `From("https://...")` fails when creating
the consumer: the listener is plaintext HTTP. Terminate TLS in front of an
`http://` consumer (reverse proxy, `HTTPComponent.SetMiddleware`, or similar).

#### Headers set on the consumer

| Header            | Source                         |
|-------------------|--------------------------------|
| `CamelHttpMethod` | Request method (`GET`, `POST`, …) |
| `CamelHttpPath`   | Request URL path               |
| `CamelHttpQuery`  | Raw query string               |
| `CamelHttpUrl`    | `url.URL.String()` of the request |

The producer sends **POST** unless `CamelHttpMethod` is set on the exchange
(a `From(http).To(http)` proxy therefore forwards the inbound method).

Inbound request headers are also copied onto the exchange (see response
headers below).

#### Authentication, TLS, rate limiting (consumer middleware)

The HTTP consumer ships with **no authentication**. Expose it to an untrusted
network only behind a middleware that enforces the security model you need
(token check, mTLS, OAuth, IP allowlist, rate limit, ...). Install the
middleware on the `HTTPComponent` before the route starts:

```go
http := gocamel.NewHTTPComponent()
http.SetMiddleware(func(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.Header.Get("Authorization") != "Bearer "+os.Getenv("API_TOKEN") {
            http.Error(w, "unauthorized", http.StatusUnauthorized)
            return
        }
        next.ServeHTTP(w, r)
    })
})
ctx.AddComponent("http", http)
```

The middleware wraps every handler installed by an `http://` consumer in this
context.

#### Outbound header sanitisation

Both the HTTP producer (`To`) and the consumer response path reject header
names and values containing `\r` or `\n` (CRLF) to prevent HTTP response
splitting (CWE-113). An exchange that tries to set such a header gets an
error back from `Send()`.

#### Response headers

The response body is taken from `Out` (or `In` when no `Out` was set, per Camel
InOut semantics). `[]byte` and `string` bodies are written as-is; any other
non-nil body is written using its `%v` string representation.

Because the consumer copies every inbound request header onto the exchange, a
route that does not set `Out` would answer with the request's own headers. Two
filters prevent that:

- **Unchanged inbound headers are not echoed.** A header that comes back
  identical to the one received is dropped, so `Cookie` and `Authorization`
  are never reflected to the client. A header the route sets or modifies is
  always sent.
- **Hop-by-hop and framing headers are never emitted** (`Connection`,
  `Keep-Alive`, `Transfer-Encoding`, `Content-Length`, `Host`, ...). Echoing
  the request's `Content-Length` corrupted the response framing.
- **Internal `Camel*` headers never cross the HTTP boundary.** They are not
  emitted in the response (`CamelHttpQuery` used to echo query-string secrets
  and `CamelFileName` internal paths), and a client cannot set them on the
  exchange: inbound request headers starting with `Camel` are dropped.

A header holding a `[]string` is sent as a repeated header (e.g. several
`Set-Cookie`). Symmetrically, the producer stores a repeated response header
as a `[]string` on `Out`; single-valued headers remain a `string`.

The same hop-by-hop filter applies to the producer, so a `From(http://...)`
→ `To(http://...)` bridge does not forward the inbound `Content-Length` or
`Host` to the upstream. Response-only headers (`Set-Cookie`, `Server`,
`WWW-Authenticate`, `Location`, `ETag`, `Status-Code`...) are not sent either:
in `To("http://a").To("http://b")` the response headers of `a` are on the
message, and used to reach `b`, leaking `a`'s session cookie to another
service.

The producer never sends internal `Camel*` headers upstream. It forwards the
other headers, with one exception for credentials: the `Authorization` and
`Cookie` values a client sent to the HTTP consumer are **not** relayed
unchanged to the upstream, so a `From(http://...)` → `To(http://...)` bridge
does not leak a client's credentials to a third party. A credential the route
sets itself (e.g. a service token) is always sent. For a deliberate
authenticating pass-through proxy, opt in:

```go
http := gocamel.NewHTTPComponent()
http.SetForwardInboundCredentials(true)
```

To strip `Authorization` and `Cookie` from every outbound request produced by
the component, including the ones the route sets:

```go
http.SetSkipAuthorizationHeaders(true)
```

**Request body.** The producer sends `[]byte`, `string` and `io.Reader`
bodies; a `nil` body sends no content. Any other type (a map, a struct) is
rejected with an error instead of being silently sent as an empty body:
marshal it first (e.g. `MarshalJSON`).

#### Response status

A route sets the status code with the `CamelHttpResponseCode` header on the
response message (the legacy `Status-Code` header is still honoured). Values
outside 100–599 are ignored:

```go
builder.From("http://localhost:8080/api").
    ProcessFunc(func(e *gocamel.Exchange) error {
        e.GetOut().SetHeader(gocamel.CamelHttpResponseCode, 201)
        e.GetOut().SetHeader("Content-Type", "application/json")
        e.GetOut().SetBody(`{"created":true}`)
        return nil
    })
```

#### Producer error handling

An upstream response with status **≥ 300 is an error** (the Apache Camel
default). The error wraps `gocamel.ErrHTTPStatus`, and the response body and
headers are still published on `Out` so the route can inspect them:

```go
if errors.Is(err, gocamel.ErrHTTPStatus) {
    code, _ := exchange.GetOut().GetHeader(gocamel.CamelHttpResponseCode)
    // code is an int; the body holds the upstream error payload
}
```

To treat every response as a success and branch on the status yourself:

```go
http := gocamel.NewHTTPComponent()
http.SetThrowExceptionOnFailure(false)
```

The producer also sets `CamelHttpResponseCode` (int) and
`CamelHttpResponseText` (e.g. `"500 Internal Server Error"`) on `Out`.

#### Hardening

- The embedded server sets a 10-second `ReadHeaderTimeout` (slow header
  attacks), a 60-second `ReadTimeout` (slow body attacks) and a 60-second
  `IdleTimeout` (idle keep-alive connections).
- Request and response bodies are read through a size limit —
  `DefaultMaxBodySize` (32 MiB) — so a single oversized payload cannot exhaust
  process memory (CWE-400). An oversized request is answered with
  `413 Request Entity Too Large`. Adjust or disable it per component:

    ```go
    http := gocamel.NewHTTPComponent()
    http.SetMaxBodySize(256 << 20) // 256 MiB; <= 0 disables the limit
    ```

- Route errors are **not** returned to the client: the handler answers with a
  generic `500 internal server error` and logs the detail, which may carry SQL
  text, file paths or driver messages.

#### Shutdown

The consumer goroutine is tracked by an internal `WaitGroup` and `Stop()`
waits for `http.Server.Shutdown` to complete (bounded by a 30-second
internal deadline) before returning. `Stop()` is idempotent.

---

### Net (TCP/UDP)

Raw TCP and UDP sockets, built on the standard library — the Go equivalent of
Apache Camel's `netty`/`mina` components. The consumer listens and feeds
inbound messages into the route; the producer dials and sends the message
body.

```go
// Consumer (server): request-reply over TCP
builder.From("net:tcp://localhost:9090")

// Consumer (server): fire-and-forget UDP
builder.From("net:udp://localhost:9090?sync=false")

// Producer (client)
builder.To("net:tcp://localhost:9090")
```

| Option       | Type   | Default  | Description                                    |
|--------------|--------|----------|------------------------------------------------|
| `sync`       | bool   | `true`   | Request-reply: the consumer writes the route's response back to the peer; the producer waits for a reply |
| `textline`   | bool   | `true`   | Newline-delimited messages (TCP only): requests and replies are terminated by `\n`. `false` = one message per read |
| `bufferSize` | int    | `8192`   | Maximum message/datagram size in bytes         |
| `timeout`    | int    | `30000`  | Producer dial/reply timeout in ms (0 = none)   |
| `keepAlive`  | bool   | `false`  | TCP keepalive on connections                   |
| `readTimeout`| int    | `60000`  | Consumer read deadline in ms (0 = none)        |
| `maxConnections` | int | `1024` | Maximum concurrent TCP connections of the consumer (0 = unlimited); extra connections are closed immediately |

**URI forms** — both `net:tcp://host:port` and `net:tcp:host:port` are
accepted; the same applies to `udp`. An omitted host defaults to `localhost`.
Port `0` asks the OS for an ephemeral port: read the actual bound address
from `NetConsumer.Address()`.

**Framing.** TCP is a byte stream, so the component needs a message boundary.
With `textline=true` (default) each newline-terminated line is one message
(the trailing `\r\n` or `\n` is stripped). The producer appends a `\n` to a
request that lacks one and reads its reply up to the next `\n`; the consumer
terminates each reply with `\n` the same way, so a GoCamel producer and
consumer interoperate with the default options. With `textline=false` each
read is one message. UDP datagrams need no framing: each datagram is one message.

```go
// Request-reply echo server
builder.From("net:tcp://localhost:9090").
    ProcessFunc(func(e *gocamel.Exchange) error {
        body, _ := e.GetIn().GetBody().([]byte)
        e.GetOut().SetBody([]byte("echo: " + string(body)))
        return nil
    })
```

**Headers.** The consumer sets `CamelNetRemoteAddress` and
`CamelNetLocalAddress` (host:port) on the In message.

#### Hardening

- Message reads are bounded by `bufferSize`. A textline message longer than
  the limit is a protocol error: the connection is dropped rather than
  buffering unbounded data (CWE-400). UDP datagrams larger than the limit
  are truncated.
- The producer's reply read in sync mode is bounded by `bufferSize` and by
  the `timeout` read deadline, so a silent peer cannot hang the route
  forever.
- The consumer sets a `readTimeout` (default 60 s) on each connection so a
  slowloris-style client cannot pin a goroutine open indefinitely; set it to
  `0` to disable.
- `maxConnections` (default 1024) bounds the concurrent TCP connections, so a
  flood of clients cannot exhaust the process's file descriptors.
- A transient `Accept` error (such as `EMFILE`, too many open files) no longer
  stops the consumer: it logs a warning and retries with a backoff capped at
  one second, like `net/http.Server`.
- Panics in route processors are contained (`ProcessSafely`): a misbehaving
  route fails its exchange, never the consumer.

---

## Messaging Components

### Telegram

Telegram Bot API integration for receiving and sending messages.

```go
// Required: Set TELEGRAM_AUTHORIZATIONTOKEN env variable
ctx.AddComponent("telegram", gocamel.NewTelegramComponent())

// Consumer (webhook/polling mode)
builder.From("telegram:bots").Log("${body}")

// Producer (send messages)
builder.To("telegram:bots")
```

**Environment Variables:**

- `TELEGRAM_AUTHORIZATIONTOKEN` - Bot API token

| Option               | Type   | Default | Description   |
|----------------------|--------|---------|---------------|
| `authorizationToken` | string | env var | Bot API token |

---

### NATS

Asynchronous event-driven messaging with NATS. High performance publisher and subscriber with Go NATS client.

```go
ctx.AddComponent("nats", gocamel.NewNATSComponent())

// Consumer: reactive asynchronous subscription
builder.From("nats://orders?servers=nats://localhost:4222").
    ProcessFunc(func(e *gocamel.Exchange) error {
        // NATS payload is received as a byte slice
        payload := e.GetIn().GetBody().([]byte)
        fmt.Printf("Received order: %s\n", string(payload))
        return nil
    })

// Producer: publish to subject
builder.To("nats://orders?servers=nats://localhost:4222")

// Dynamically override subject in header
exchange.In.SetHeader(gocamel.CamelNATSSubject, "urgent-orders")
```

> [!WARNING]
> When writing URIs, always use double slashes (e.g. `nats://orders`) instead of `nats:orders`, otherwise `url.Parse` treats it as an Opaque URI, which contaminates the subject string with query parameters.

| Option | Type | Default | Description |
|---|---|---|---|
| `servers` | string | `nats://127.0.0.1:4222` | NATS server addresses (comma-separated) |
| `subject` | string | parsed from path | NATS subject to subscribe/publish to |

**Exchange Headers:**

- `CamelNATSSubject` - Subject of the received message or target subject for publishing.

### Kafka

High-throughput distributed event streaming with Apache Kafka via `segmentio/kafka-go` (pure Go).

```go
import "github.com/tranchida/gocamel/components/kafka"

ctx.AddComponent("kafka", kafka.NewKafkaComponent())

// Consumer with consumer group
builder.From("kafka:orders?brokers=localhost:9092&groupId=billing-service").
    Log("Received message: ${body}")

// Producer with dynamic partition key (requires allowHeaderOverride=true)
builder.From("direct:publish").
    SetHeader("CamelKafkaPartitionKey", "key-123").
    To("kafka:orders?brokers=localhost:9092&allowHeaderOverride=true")
```

| Option | Type | Default | Description |
|---|---|---|---|
| `brokers` | string | `localhost:9092` | Comma-separated list of Kafka broker addresses |
| `groupId` | string | `""` | Kafka consumer group ID |
| `partition` | int | `-1` | Target partition (when not using consumer group) |
| `clientId` | string | `""` | Client identifier |
| `allowHeaderOverride` | bool | `false` | Allow `CamelKafkaTopic`, `CamelKafkaPartitionKey` and `CamelKafkaKey` to override the URI when producing |
| `forwardHeaders` | string | `""` | Comma-separated allowlist of exchange headers forwarded as Kafka record headers; when set, ONLY those headers are propagated |
| `maxRedeliveries` | int | `10` | Consumer: redeliveries of a message whose route failed before it is logged and acknowledged anyway (`-1` = retry forever, `0` = acknowledge on the first failure) |
| `redeliveryDelay` | int | `100` | Consumer: first redelivery delay in ms, doubled on each attempt |
| `maxRedeliveryDelay` | int | `30000` | Consumer: cap of the redelivery backoff in ms |

**Headers:**
- `CamelKafkaTopic` - Overrides (with `allowHeaderOverride=true`) or reads the target topic.
- `CamelKafkaPartitionKey` / `CamelKafkaKey` - Partition key for Kafka partitioning (honored with `allowHeaderOverride=true`).
- `CamelKafkaPartition` - Partition index.
- `CamelKafkaOffset` - Message offset.
- `CamelKafkaTimestamp` - Message timestamp.

> ⚠️ Security: the topic and key configured in the URI are trusted
> configuration. Header overrides are ignored unless the endpoint opts in with
> `allowHeaderOverride=true`. Enable it only where headers cannot be
> influenced by untrusted input.
>
> Outbound exchange headers are filtered before they reach the broker:
> `Authorization`, `Cookie`, `Proxy-Authorization`, `Set-Cookie` and
> `WWW-Authenticate` (case-insensitive) are never forwarded, so a producer
> sitting downstream of the HTTP consumer does not leak client credentials.
> Set `forwardHeaders` for an explicit allowlist when you want stricter
> control.
>
> Inbound record headers named `Camel*` (case-insensitive) are dropped by the
> consumer: whoever can write to the topic must not drive internal headers
> such as `CamelFileName` or `CamelHttpMethod`.

When a `groupId` is configured, each successfully processed message is
committed to the consumer group. A failed commit is logged (with
topic/partition/offset) rather than silently swallowed: the message will be
redelivered after a rebalance or restart, and the log line is the only signal
of that duplicated processing.

---

### STOMP

Produce and consume messages with any STOMP 1.0/1.1/1.2 compliant broker — ActiveMQ, ActiveMQ Artemis, RabbitMQ, Apollo — via the `go-stomp` client. Ideal for JMS-style queue (`/queue/...`) and topic (`/topic/...`) destinations.

```go
import "github.com/tranchida/gocamel/components/stomp"

ctx.AddComponent("stomp", stomp.NewStompComponent())

// Consumer: subscribe to an ActiveMQ queue
builder.From("stomp://queue/orders?host=localhost:61613&ack=client-individual").
    Log("Received message: ${body}")

// Consumer on a topic
builder.From("stomp://topic/prices?host=localhost:61613")

// Producer
builder.From("direct:publish").
    To("stomp://queue/orders?host=localhost:61613")

// Dynamic destination (requires allowHeaderOverride=true)
exchange.In.SetHeader("CamelStompDestination", "/queue/urgent-orders")
```

| Option | Type | Default | Description |
|---|---|---|---|
| `host` | string | `localhost:61613` | Broker address `host:port` (ActiveMQ STOMP connector) |
| `destination` | string | parsed from path | STOMP destination (`/queue/...`, `/topic/...`) |
| `login` | string | `""` | Login sent in the STOMP CONNECT frame |
| `passcode` | string | `""` | Passcode sent in the STOMP CONNECT frame (prefer the `STOMP_PASSCODE` env var) |
| `ack` | string | `auto` | Acknowledgement mode: `auto`, `client`, `client-individual` (consumer only) |
| `allowHeaderOverride` | bool | `false` | Allow `CamelStompDestination` to override the URI when producing |
| `forwardHeaders` | string | `""` | Comma-separated allowlist of exchange headers forwarded as STOMP frame headers; when set, ONLY those headers are propagated |
| `maxRedeliveries` | int | `10` | Consumer: redeliveries of a message whose route failed before it is logged and acknowledged anyway (`-1` = retry forever, `0` = acknowledge on the first failure) |
| `redeliveryDelay` | int | `100` | Consumer: first redelivery delay in ms, doubled on each attempt |
| `maxRedeliveryDelay` | int | `30000` | Consumer: cap of the redelivery backoff in ms |
| `connectTimeout` | duration | `30s` | Timeout of the TCP connect plus the TLS and STOMP handshakes. The consumer reconnects and resubscribes (backoff 100 ms up to 5 s) when the broker connection is lost or the broker sends an ERROR frame |
| `ssl` | bool | `false` | Connect over TLS |
| `sslCAFile` | string | system roots | PEM file of the certificate authority verifying the broker certificate |
| `sslCertFile` | string | `""` | PEM file of the client certificate for mutual TLS (requires `sslKeyFile`) |
| `sslKeyFile` | string | `""` | PEM file of the client private key for mutual TLS (requires `sslCertFile`) |
| `sslServerName` | string | broker host | Server name used for SNI and certificate verification |
| `sslInsecureSkipVerify` | bool | `false` | Skip broker certificate verification (controlled environments only) |

**Headers:**
- `CamelStompDestination` - Destination the message was received from, or target destination when producing (honored with `allowHeaderOverride=true`).
- `CamelStompContentType` - MIME content type sent with the message or received from the broker.

> ⚠️ Security: the destination configured in the URI is trusted
> configuration. Header overrides are ignored unless the endpoint opts in with
> `allowHeaderOverride=true`. Enable it only where headers cannot be
> influenced by untrusted input.
>
> Outbound exchange headers are filtered before they reach the broker:
> `Authorization`, `Cookie`, `Proxy-Authorization`, `Set-Cookie` and
> `WWW-Authenticate` (case-insensitive) are never forwarded, so a producer
> sitting downstream of the HTTP consumer does not leak client credentials.
> Set `forwardHeaders` for an explicit allowlist when you want stricter
> control.
>
> Inbound frame headers named `Camel*` (case-insensitive) are dropped by the
> consumer: a client sending to the destination must not drive internal
> headers such as `CamelFileName` or `CamelHttpMethod`.

With `ack=client` or `ack=client-individual`, a message is acknowledged only
after the route processed it successfully; a processing failure is retried
(see `maxRedeliveries`) before any acknowledgement is sent, so the broker
redelivers the message if the connection drops in the meantime. With `ack=auto` (default), the broker
considers a message delivered as soon as it is sent.

**SSL/TLS:** set `ssl=true` to connect to the broker over TLS (e.g. the
ActiveMQ STOMP+SSL connector on port 61614). The server certificate is
verified against the system roots by default, or against the CA file
provided via `sslCAFile`. Mutual TLS uses `sslCertFile`/`sslKeyFile`. The
TLS configuration is validated at endpoint creation, so a missing CA file
or an invalid key pair fails fast.

```go
// TLS with a private CA
builder.From("stomp://queue/orders?host=activemq:61614&ssl=true&sslCAFile=/etc/gocamel/ca.pem")

// Mutual TLS
builder.To("stomp://queue/orders?host=activemq:61614&ssl=true&sslCAFile=/etc/gocamel/ca.pem&sslCertFile=/etc/gocamel/client.pem&sslKeyFile=/etc/gocamel/client.key")
```

> [!WARNING]
> `sslInsecureSkipVerify=true` disables server certificate verification and
> exposes the connection to man-in-the-middle attacks. Use it only in
> controlled environments (local development, test harnesses).

---

### gRPC

High-performance RPC client and server integration powered by `google.golang.org/grpc`.

```go
import "github.com/tranchida/gocamel/components/grpc"

ctx.AddComponent("grpc", grpc.NewGRPCComponent())

// Server Consumer: routes RPC calls through GoCamel
builder.From("grpc://0.0.0.0:50051/helloworld.Greeter/SayHello").
    SetBody("Hello from GoCamel gRPC Server")

// Client Producer: invokes remote RPC methods
builder.From("direct:invoke").
    To("grpc://localhost:50051/helloworld.Greeter/SayHello?insecure=true")
```

| Option | Type | Default | Description |
|---|---|---|---|
| `service` | string | parsed from path | Full gRPC service name |
| `method` | string | parsed from path | Method name to call or expose |
| `insecure` | bool | `true` | Use plaintext transport without TLS; when `false`, TLS 1.2+ is used with the system CA pool |
| `caCert` | string | `""` | Path to a PEM CA certificate trusted for server verification when `insecure=false` (e.g. self-signed server) |
| `forwardHeaders` | string | `""` | Comma-separated allowlist of exchange headers forwarded as outgoing RPC metadata; when set, ONLY those headers are propagated |

**Headers:**
- `CamelGrpcMethod` - Method name invoked.
- `CamelGrpcStatusCode` - gRPC status code (e.g. 0 for OK).
- `CamelGrpcStatusDescription` - gRPC client status description message.

> ⚠️ Security: outbound exchange headers are filtered before they are sent as
> RPC metadata: `Authorization`, `Cookie`, `Proxy-Authorization`, `Set-Cookie`
> and `WWW-Authenticate` (case-insensitive) are never forwarded, so a producer
> sitting downstream of the HTTP consumer does not leak client credentials.
> Set `forwardHeaders` for an explicit allowlist when you want stricter
> control.

---

### Redis

Send commands (SET, GET, DEL, PUBLISH) or subscribe to Redis Pub/Sub channels.

```go
ctx.AddComponent("redis", gocamel.NewRedisComponent())

// Consumer: Subscribe to Pub/Sub channel
builder.From("redis://localhost:6379?subscribe=true&channel=mychannel").
    Log("Received Pub/Sub message: ${body}")

// Producer: Execute commands
builder.To("redis://localhost:6379?command=SET&key=mykey")
```

**Supported Commands:**

- `SET`: Saves the exchange body as the value of `key`.
- `GET`: Retrieves the value of `key` and writes it to `Out.Body`.
- `DEL`: Deletes `key` and writes the number of affected keys to `Out.Body`.
- `PUBLISH`: Publishes the exchange body to the specified channel/key.

**Exchange Headers (Override URI parameters):**

- `CamelRedisCommand` - The Redis command to execute (e.g. `"GET"`).
- `CamelRedisKey` - The target Redis key.
- `CamelRedisChannel` - Target Pub/Sub channel for publish or source channel for subscribe.

| Option | Type | Default | Description |
|---|---|---|---|
| `command` | string | `SET` | Redis command for producer |
| `key` | string | `""` | Redis key to operate on |
| `channel` | string | `""` | Redis Pub/Sub channel |
| `subscribe` | bool | `false` | If true, acts as a Pub/Sub consumer |

---

### Redis Idempotent Repository

The Redis Idempotent Repository provides a distributed implementation of the `IdempotentRepository` interface, allowing multiple GoCamel instances in a cluster to coordinate and avoid processing duplicate messages.

```go
import (
    "time"
    "github.com/redis/go-redis/v9"
)

rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
// Key prefix "idempotent:" with 24-hour expiration TTL
repo := gocamel.NewRedisIdempotentRepository(rdb, "idempotent:", 24*time.Hour)

builder.From("direct:start").
    IdempotentConsumer("${header.MessageId}", repo).
    To("direct:process")
```

---

### Go Channel

A native, ultra-fast intra-process integration component utilizing native Go channels. It enables decoupling route segments asynchronously inside the same process.

```go
ctx.AddComponent("chan", gocamel.NewChanComponent())

// Consumer: processes exchanges asynchronously from channel
builder.From("chan:orders?bufferSize=500").
    To("direct:process-order")

// Producer: writes a copy of the exchange to the channel asynchronously
builder.To("chan:orders")
```

| Option | Type | Default | Description |
|---|---|---|---|
| `bufferSize` | int | `100` | Capacity of the internal buffered channel |
| `shutdownTimeout` | int | `30000` | How long `Stop` keeps processing the queued exchanges, in ms (0 = discard them) |

**Notes:**

- Endpoints are cached **by channel name**: every producer and consumer using the same
  name shares one endpoint, and only **one consumer** per channel is allowed (a second
  `From("chan:orders")` fails at startup).
- URI options are applied when the endpoint is **first created**; later references with
  different options log a warning and reuse the existing endpoint.
- The consumer processes each exchange under **its own context**, not the sender's: an
  HTTP request that enqueued a message and returned does not cancel the asynchronous leg.
- `Stop` rejects new sends, then finishes the exchanges already queued for up to
  `shutdownTimeout` before cancelling the rest (see SEDA in the EIP page).

---

## AI Components

### OpenAI

OpenAI API integration for ChatGPT/GPT-4. Any OpenAI-compatible endpoint is
supported via the `baseURL` option (OpenRouter, Groq, Together, Mistral,
vLLM, LM Studio, Ollama's OpenAI compatibility layer, ...).

```go
ctx.AddComponent("openai", gocamel.NewOpenAIComponent())

// Producer only - send chat completion request
endpoint, _ := ctx.CreateEndpoint("openai:chat?model=gpt-4")
producer, _ := endpoint.CreateProducer()

exchange := gocamel.NewExchange(context.Background())
exchange.GetIn().SetBody("Hello, how are you?")
producer.Send(exchange)

fmt.Println(exchange.GetOut().GetBody()) // AI response
```

Use a custom OpenAI-compatible provider:

```go
// OpenRouter example: route to openai/gpt-4o via OpenRouter's gateway
endpoint, _ := ctx.CreateEndpoint(
    "openai:chat?model=openai/gpt-4o" +
        "&baseURL=https://openrouter.ai/api/v1" +
        "&apiKey=$OPENROUTER_API_KEY")
```

**Environment Variables:**

- `OPENAI_AUTHORIZATIONTOKEN` or `OPENAI_API_KEY` - API key

| Option               | Type   | Default              | Description                                              |
|----------------------|--------|----------------------|----------------------------------------------------------|
| `model`              | string | `gpt-3.5-turbo`      | Model to use                                             |
| `authorizationToken` | string | env var              | API key (alias: `apiKey`)                               |
| `baseURL`            | string | `https://api.openai.com/v1` | Custom OpenAI-compatible base URL (OpenRouter, Groq, ...) |

The SDK HTTP client timeout is **60 seconds**. Bound long-running calls further
with `exchange.Context`.

### Anthropic

Native integration with the Anthropic Messages API (Claude). Unlike the
OpenAI component, this is a dedicated connector for Claude's native API
shape — separate `system` parameter, content blocks, prompt caching
headers, and stop reasons.

```go
ctx.AddComponent("anthropic", gocamel.NewAnthropicComponent())

// Producer only - send a Messages request
endpoint, _ := ctx.CreateEndpoint(
    "anthropic:messages?model=claude-sonnet-4-5" +
        "&apiKey=$ANTHROPIC_API_KEY" +
        "&maxTokens=1024" +
        "&system=You are a concise assistant")
producer, _ := endpoint.CreateProducer()

exchange := gocamel.NewExchange(context.Background())
exchange.GetIn().SetBody("Explain EIP in one sentence.")
producer.Send(exchange)

fmt.Println(exchange.GetOut().GetBody()) // Claude response
```

**Environment Variables:**

- `ANTHROPIC_API_KEY` - API key (recommended over embedding it in the URI)

| Option               | Type   | Default  | Description                                              |
|----------------------|--------|----------|----------------------------------------------------------|
| `apiKey`             | string | env var  | API key (alias: `authorizationToken`). Masked by `RedactURI`. |
| `model`              | string | required | Model name, e.g. `claude-sonnet-4-5`                     |
| `maxTokens`          | int    | `1024`   | Maximum tokens to generate                               |
| `system`             | string | `""`     | System prompt                                            |
| `temperature`         | float  | unset    | Sampling temperature (0.0-1.0)                           |
| `baseURL`            | string | Anthropic API | Override API base URL (proxies, gateways). Must be `http(s)`. |
| `allowHeaderOverride` | bool  | `false`  | Allow the `Anthropic*` override headers below to replace the URI settings |

The SDK HTTP client timeout is **60 seconds**.

**Per-message override headers** (In message, honored only with `allowHeaderOverride=true`):

| Header                  | Type   | Description                              |
|-------------------------|--------|------------------------------------------|
| `AnthropicSystem`       | string | Overrides the URI `system`               |
| `AnthropicModel`        | string | Overrides the URI `model`                |
| `AnthropicMaxTokens`   | string | Overrides the URI `maxTokens`            |
| `AnthropicTemperature`  | string | Overrides the URI `temperature`         |

> ⚠️ Security: the system prompt, model, token budget and temperature
> configured in the URI are trusted configuration. The override headers are
> ignored unless the endpoint opts in with `allowHeaderOverride=true`. Enable it
> only where headers cannot be influenced by untrusted input: a sender could
> otherwise replace the system prompt (prompt injection) or pick a costlier
> model and token budget.

**Out headers** (set on the response):

| Header                      | Type  | Description                |
|-----------------------------|-------|----------------------------|
| `AnthropicUsageInputTokens` | int64 | Input tokens billed        |
| `AnthropicUsageOutputTokens` | int64 | Output tokens billed       |
| `AnthropicStopReason`       | string | `end_turn`, `max_tokens`, ... |
| `AnthropicModel`            | string | Model that produced the response |

### Jev (OpenRouter Decisions API)

Producer for the [TypeSafe Jev](https://openrouter.ai/typesafe) decision model
through the OpenRouter Decisions API. Jev reads natural language like an LLM
but returns **typed judgments** (probabilities, choices, scores) instead of
generated text; the thresholds stay in your route. The module has no
third-party dependency.

```go
ctx.AddComponent("jev", jev.NewJevComponent())

questions := `{"is_ironic":{"type":"noul","instructions":"Is this text ironic?",
  "criteria":{"true":"ironic","false":"sincere"}}}`

route, _ := ctx.CreateRouteBuilder().
    From("direct:in").
    To("jev:decisions?questions=" + url.QueryEscape(questions)).
    ProcessFunc(func(e *gocamel.Exchange) error {
        answers := e.GetIn().GetBody().(map[string]jev.Answer)
        fmt.Printf("ironic: %.0f%%\n", answers["is_ironic"].Noul*100)
        return nil
    }).
    Build()
ctx.AddRoute(route)
```

The exchange body is the *state* to judge (string, `[]byte`, or any
JSON-serializable value). The Out body is a `map[string]jev.Answer` keyed by
question id. `Answer.Type` is `noul` (fills `Noul`), `choice` (`Choice`,
`Probabilities`, `Confidence`) or `score` (`Value`, `Legend`, `Confidence`).

**Environment Variables:**

- `OPENROUTER_API_KEY` - API key (recommended over embedding it in the URI)

| Option               | Type     | Default              | Description |
|----------------------|----------|----------------------|-------------|
| `apiKey`             | string   | env var              | API key (alias: `authorizationToken`). Masked by `RedactURI`. |
| `questions`          | JSON     | required*            | Object mapping question ids to Jev questions, sent as-is |
| `model`              | string   | `typesafe/jev-1.13`  | Jev model |
| `baseURL`            | string   | `https://openrouter.ai/api/alpha` | API root (proxies, tests). Must be `http(s)`. |
| `timeout`            | duration | `30s`                | Per-request timeout |
| `allowHeaderOverride` | bool    | `false`              | Allow the `Jev*` override headers to replace the URI settings |

\* Optional when `allowHeaderOverride=true` and the `JevQuestions` header is sent.

**Override headers** (In message, honored only with `allowHeaderOverride=true`):
`JevQuestions` (JSON string or map) and `JevModel`.

> ⚠️ Security: questions and model are trusted configuration. Enable the
> override only where headers cannot be influenced by untrusted input.

**Out headers:** `JevModel` (model used), `JevElapsed` (`time.Duration`, API round-trip).

### Ollama

Native integration with a local [Ollama](https://ollama.com) server via its
Go client (`github.com/ollama/ollama/api`). Three modes are selected by the
URI path:

- `ollama:chat` (default) — multi-turn chat API (`/api/chat`)
- `ollama:generate` — one-shot generation (`/api/generate`)
- `ollama:embed` — embeddings (`/api/embed`)

```go
ctx.AddComponent("ollama", gocamel.NewOllamaComponent())

// Chat with a local model
endpoint, _ := ctx.CreateEndpoint(
    "ollama:chat?model=llama3.1&host=http://localhost:11434")
producer, _ := endpoint.CreateProducer()

exchange := gocamel.NewExchange(context.Background())
exchange.GetIn().SetBody("Summarize EIP in one sentence.")
producer.Send(exchange)

fmt.Println(exchange.GetOut().GetBody()) // Local model response
```

Generate embeddings locally:

```go
endpoint, _ := ctx.CreateEndpoint(
    "ollama:embed?model=nomic-embed-text&host=http://localhost:11434")
// exchange.GetOut().GetBody() returns []float32
```

**Environment Variables:**

- `OLLAMA_HOST` - default host when the `host` URI option is omitted

| Option       | Type     | Default                   | Description                                              |
|--------------|----------|---------------------------|----------------------------------------------------------|
| `model`      | string   | required (chat/generate)  | Model name                                                |
| `host`       | string   | `http://localhost:11434`  | Ollama server base URL (must be `http(s)`)               |
| `system`     | string   | `""`                      | System prompt (generate mode only)                       |
| `keepAlive`  | duration | unset                     | Model retention in memory (e.g. `5m`, `30s`)            |
| `think`      | string   | unset                     | Reasoning effort: `true`/`false`/`low`/`medium`/`high`/`max` |
| `format`     | string   | unset                     | Structured output format (e.g. `json`)                   |
| `allowHeaderOverride` | bool | `false`                | Allow the `OllamaModel` and `OllamaSystem` headers to replace the URI settings |

**Per-message override headers** (In message, honored only with `allowHeaderOverride=true`):

| Header          | Type   | Description                          |
|-----------------|--------|--------------------------------------|
| `OllamaModel`   | string | Overrides the URI `model`            |
| `OllamaSystem`  | string | Overrides the URI `system` (chat/generate) |

> ⚠️ Security: the model and system prompt configured in the URI are trusted
> configuration. The override headers are ignored unless the endpoint opts in
> with `allowHeaderOverride=true`. Enable it only where headers cannot be
> influenced by untrusted input: a sender could otherwise replace the system
> prompt (prompt injection) or pick another model.

**Out headers** (set on the response):

| Header                  | Type  | Description (chat/generate)        |
|-------------------------|-------|-----------------------------------|
| `OllamaModel`           | string | Model that produced the response |
| `OllamaDoneReason`      | string | `stop`, `length`, ...             |
| `OllamaTotalDuration`  | int64  | Total duration (ns)               |
| `OllamaPromptEvalCount` | int    | Input tokens evaluated            |
| `OllamaEvalCount`       | int    | Output tokens generated           |

> The Ollama component does **not** expose model lifecycle operations
> (Pull/List/Delete). Use the `ollama` CLI or HTTP API directly for model
> management; the GoCamel producer is scoped to inference.

---

## Scheduling Components

### Cron

Advanced scheduling with cron expressions or simple intervals.

```go
ctx.AddComponent("cron", gocamel.NewCronComponent())

// Cron trigger (6 fields, including seconds)
builder.From("cron://group/job?cron=0+*+*+*+*+*")

// Simple interval trigger
builder.From("cron://poller?trigger.repeatInterval=5000")
```

**Cron Expression Format (6 fields):**

```
second minute hour day month dayOfWeek
0 * * * * *  # Every minute
0 */5 * * * * # Every 5 minutes
cron=0 0 12 * * *  # Every day at noon
```

| Option                   | Type   | Default | Description                     |
|--------------------------|--------|---------|---------------------------------|
| `cron`                   | string | `""`    | 6-field cron expression         |
| `trigger.repeatInterval` | int    | `""`    | Interval in ms (simple trigger) |
| `trigger.repeatCount`    | int    | `-1`    | Max repetitions                 |
| `triggerStartDelay`      | int    | `500`   | Initial delay in ms             |
| `stateful`               | bool   | `false` | Prevent concurrent execution    |

**Exchange Headers:**

- `fireTime` - Trigger execution time
- `nextFireTime` - Next scheduled time
- `triggerName` - Trigger identifier

On `Stop()`, a job whose `triggerStartDelay` has not elapsed yet is never
registered on the shared scheduler: the stop request wins the race with the
delayed registration, so no orphan job is left firing forever.

---

## Mail Components

### SMTP/SMTPS (Send)

Send emails via SMTP.

```go
builder.To("smtps://smtp.gmail.com:465?to=recipient@example.com&subject=Hello")
```

| Option        | Type   | Default        | Description   |
|---------------|--------|----------------|---------------|
| `username`    | string | `""`           | SMTP username |
| `password`    | string | `""`           | SMTP password |
| `to`          | string | `""`           | Recipient(s)  |
| `subject`     | string | `""`           | Email subject |
| `contentType` | string | `"text/plain"` | MIME type     |

---

### IMAP/IMAPS (Receive)

Receive emails via IMAP with IDLE support.

```go
builder.From("imaps://imap.gmail.com:993?folderName=INBOX&idle=true")
```

| Option           | Type   | Default   | Description             |
|------------------|--------|-----------|-------------------------|
| `username`       | string | `""`      | IMAP username           |
| `password`       | string | `""`      | IMAP password           |
| `folderName`     | string | `"INBOX"` | Folder to poll          |
| `unseen`         | bool   | `true`    | Only unread messages    |
| `idle`           | bool   | `false`   | Use IMAP IDLE mode      |
| `delete`         | bool   | `false`   | Delete after processing |
| `fetchSize`      | int    | `-1`      | Messages per poll       |
| `pollDelay`      | int    | `60000`   | Poll interval (ms)      |
| `maxMessageSize` | int    | `0`       | Max message size in bytes (0 = unlimited) |

---

### POP3/POP3S (Receive)

Receive emails via POP3.

```go
builder.From("pop3s://pop.gmail.com:995?username=user&password=pass")
```

The POP3 consumer honours the same `maxMessageSize` option as IMAP (default
`0` = unlimited) to bound the size of a downloaded message.

With `disconnect=false` (default), the POP3 connection is kept between polls,
like the IMAP consumer. Set `disconnect=true` to re-authenticate for each poll
(useful with servers that drop idle connections).

---

## Database Components

### SQL

SQL query execution via `database/sql`.

```go
import (
    "database/sql"
    _ "modernc.org/sqlite"
)

db, _ := sql.Open("sqlite", "./app.db")

sqlComp := gocamel.NewSQLComponent()
sqlComp.RegisterDataSource("appdb", db)
ctx.AddComponent("sql", sqlComp)

// SELECT -> Out.Body = []map[string]any
builder.From("direct:list").
    To("sql://appdb?query=SELECT+id,name+FROM+users").
    Log("${body}")

// SELECT single row -> Out.Body = map[string]any
builder.From("direct:one").
    SetHeader(gocamel.SqlParameters, []any{42}).
    To("sql://appdb?query=SELECT+*+FROM+users+WHERE+id=?&outputType=SelectOne")

// INSERT/UPDATE/DELETE -> Out.Body = affected rows (int64)
builder.From("direct:insert").
    SetHeader(gocamel.SqlParameters, []any{"alice", "alice@example.com"}).
    To("sql://appdb?query=INSERT+INTO+users(name,email)+VALUES(?,?)")
```

**URI Format:**

```
sql://<datasourceName>?query=<SQL>
sql://logical?dataSourceRef=<datasourceName>&query=<SQL>
```

| Option                | Type   | Default      | Description                                    |
|-----------------------|--------|--------------|------------------------------------------------|
| `query`               | string | required     | SQL query string                               |
| `dataSourceRef`       | string | host path    | Datasource name                                |
| `outputType`          | string | `SelectList` | `SelectList` or `SelectOne`                    |
| `batch`               | bool   | `false`      | Batch execution mode                           |
| `transacted`          | bool   | `false`      | Wrap in transaction                            |
| `allowHeaderOverride` | bool   | `false`      | Allow `CamelSqlQuery` to replace the statement |

**Query Parameters:**

Provide via `CamelSqlParameters` header or body as `[]any`.

> [!caution] The query is trusted configuration
> The statement is **never** interpolated with Exchange data — substituting
> `${header.X}` into the SQL string would defeat parameterised queries and
> enable SQL injection. Always bind dynamic values through
> `CamelSqlParameters` (positional `?` placeholders) or a `[]any` body.
>
> The `CamelSqlQuery` header replaces the statement **wholesale**, so since
> v0.2 it is ignored unless the endpoint opts in with
> `allowHeaderOverride=true`. Enable it only where headers cannot be
> influenced by untrusted input: any component that maps external metadata
> onto headers would otherwise hand a caller full control of the statement.
>
> ```go
> // Statement fixed by configuration (default, recommended)
> builder.To("sql://appdb?query=SELECT+*+FROM+users+WHERE+id=?")
>
> // Statement supplied per message — only with trusted headers
> builder.To("sql://appdb?allowHeaderOverride=true&query=SELECT+1")
> ```
>
> A statement followed by another one (`SELECT 1; DROP TABLE t`) is rejected.
> Whether `\'` escapes a quote depends on the database (MySQL: yes; SQLite,
> PostgreSQL: no), so a `;` outside literals under either reading is
> rejected: escape quotes as `''`, which every database accepts.


**Output Headers:**

- `CamelSqlRowCount` - Rows returned/affected
- `CamelSqlColumnNames` - Column names (SELECT)

**Result Body:**

| Case                    | Out.Body Type             |
|-------------------------|---------------------------|
| `SELECT` + `SelectList` | `[]map[string]any`        |
| `SELECT` + `SelectOne`  | `map[string]any` or `nil` |
| `INSERT/UPDATE/DELETE`  | `int64` (affected rows)   |

**Statement classification:**

Whether a statement is run as a query (result set) or an update (affected
rows) is decided from its leading keyword, ignoring leading comments and
parentheses. `SELECT`, `WITH` (CTE), `VALUES`, `TABLE`, `SHOW`, `EXPLAIN`,
`DESCRIBE` and `PRAGMA` return rows, as does any statement carrying a
`RETURNING` clause:

```sql
WITH recent AS (SELECT * FROM orders WHERE ts > ?)
SELECT id, total FROM recent          -- result set
INSERT INTO users(name) VALUES (?) RETURNING id   -- result set
```

---

### SQL-Stored

Stored procedure execution with IN, OUT, and INOUT parameter support.

```go
import (
    "database/sql"
    _ "modernc.org/sqlite"
)

db, _ := sql.Open("mysql", "user:pass@/mydb")

sqlStored := gocamel.NewSQLStoredComponent()
sqlStored.RegisterDataSource("mydb", db)
ctx.AddComponent("sql-stored", sqlStored)

// Simple call with IN parameters
builder.From("direct:call").
    SetBody([]gocamel.StoredProcedureParam{
        {Name: "userId", Direction: gocamel.ParamDirectionIn, Value: 42},
    }).
    To("sql-stored://mydb?procedure=GET_USER_BY_ID").
    Log("${body}")
```

**URI Format:**

```
sql-stored://datasourceName?procedure=NAME
sql-stored://logical?dataSourceRef=dsName&procedure=NAME
```

| Option                | Type   | Default      | Description                                            |
|-----------------------|--------|--------------|--------------------------------------------------------|
| `procedure`           | string | required     | Stored procedure name                                  |
| `dataSourceRef`       | string | host path    | Datasource name                                        |
| `outputType`          | string | `SelectList` | `SelectList` or `SelectOne`                            |
| `transacted`          | bool   | `false`      | Execute in transaction                                 |
| `noop`                | bool   | `false`      | Test mode (no execution)                               |
| `allowHeaderOverride` | bool   | `false`      | Allow `CamelSqlStoredProcedureName` to replace the name |

> [!caution] Procedure name is a trusted identifier
> The procedure name is spliced into the `CALL` statement and is **never**
> interpolated with exchange data. The `CamelSqlStoredProcedureName` header is
> ignored unless the endpoint opts in with `allowHeaderOverride=true`; when it
> is enabled, the header value is validated against a strict identifier pattern
> (`[A-Za-z_][A-Za-z0-9_.]*`). Only enable the override where headers cannot be
> influenced by untrusted input.

**Parameter Directions:**

| Direction             | Description           |
|-----------------------|-----------------------|
| `ParamDirectionIn`    | Input only            |
| `ParamDirectionOut`   | Output only           |
| `ParamDirectionInOut` | Both input and output |

Example:

```go
params := []gocamel.StoredProcedureParam{
    {Name: "inParam", Direction: gocamel.ParamDirectionIn, Value: "input"},
    {Name: "outParam", Direction: gocamel.ParamDirectionOut},
    {Name: "inOutParam", Direction: gocamel.ParamDirectionInOut, Value: 123},
}
```

---

### MongoDB

MongoDB integration for CRUD operations. Producer-only.

#### URI Format

```
mongodb://connectionName?database=mydb&collection=mycoll&operation=find
```

#### Options

| Option          | Type   | Required | Description                                                              |
|-----------------|--------|----------|--------------------------------------------------------------------------|
| `database`      | string | Yes      | Database name                                                            |
| `collection`    | string | Yes      | Collection name                                                          |
| `operation`     | string | Yes      | Operation: find, findOne, insert, insertOne, save, update, remove, count |
| `connectionRef` | string | No       | Registered connection reference                                          |
| `allowDeleteAll` | bool   | No       | Allow `remove` with an empty filter (default: `false`)                    |
| `allowUpdateAll` | bool   | No       | Allow `update` with an empty filter, i.e. mass-update the whole collection (default: `false`) |
| `allowHeaderOverride` | bool | No    | Allow `CamelMongoDbDatabase`, `CamelMongoDbCollection`, `CamelMongoDbOperation` and `CamelMongoDbCriteria` to override the URI configuration (default: `false`) |
| `maxResults` | int | No | Maximum documents a `find` returns (default: `10000`, `0` = unlimited). Beyond it the result is truncated, a warning is logged and `CamelMongoDbResultsTruncated` is `true` |

For safety, `remove` rejects nil and empty filters. Set `allowDeleteAll=true`
explicitly only when deleting the entire collection is intentional.
Likewise, `update` rejects a missing or empty filter (`nil`, `{}`, empty
`bson.M` / `bson.D`, empty string): any of those would mass-update every
document. Set `allowUpdateAll=true` explicitly only when that is intentional.

> ⚠️ Security: the database, collection and operation configured in the URI
> are trusted configuration. The `CamelMongoDbDatabase`,
> `CamelMongoDbCollection`, `CamelMongoDbOperation` and `CamelMongoDbCriteria`
> headers are ignored unless the endpoint opts in with
> `allowHeaderOverride=true`. Enable it only where headers cannot be
> influenced by untrusted input.

#### Input Headers

| Header                   | Mode  | Description                              |
|--------------------------|-------|------------------------------------------|
| `CamelMongoDbDatabase`   | R/W   | Database name                            |
| `CamelMongoDbCollection` | R/W   | Collection name                          |
| `CamelMongoDbOperation`  | R/W   | Operation to execute                     |
| `CamelMongoDbCriteria`   | Write | Filter/criteria (map[string]any or JSON) |
| `CamelMongoDbLimit`      | Write | Result limit                             |
| `CamelMongoDbSkip`       | Write | Skip N documents                         |
| `CamelMongoDbSort`       | Write | Sort order (json: {"field": 1})          |

#### Output Headers

| Header                    | Description                    |
|---------------------------|--------------------------------|
| `CamelMongoDbResultTotal` | Total documents found/affected |
| `CamelMongoDbOid`         | ObjectID of inserted document  |
| `CamelMongoDbResultsTruncated` | `true` when a `find` stopped at `maxResults` |

#### Example

```go
import (
    "context"
    "go.mongodb.org/mongo-driver/mongo"
    "go.mongodb.org/mongo-driver/mongo/options"
)

// Create component
mongoComp := gocamel.NewMongoDBComponent()

// Connect
client, _ := mongo.Connect(context.Background(), options.Client().ApplyURI("mongodb://localhost:27017"))
conn := gocamel.CreateMongoDBConnection(client, "mongodb://localhost:27017", "mydb")
mongoComp.RegisterConnection("myconn", conn)

builder.WithComponent("mongodb", mongoComp)

// Insert
builder.From("timer:tick?period=5s").
    SetBody(map[string]any{"name": "test", "value": 123}).
    To("mongodb://myconn?database=mydb&collection=items&operation=insert")

// Query with filter
builder.From("direct:search").
    SetHeader(gocamel.CamelMongoDbCriteria, map[string]any{"status": "active"}).
    SetHeader(gocamel.CamelMongoDbLimit, 10).
    To("mongodb://myconn?database=mydb&collection=items&operation=find")
```

---

## Transformation Components

### XSLT

XML transformation using XSL stylesheets.

```go
builder.To("xslt:file://transform.xsl")
```

| Option               | Type   | Default | Description              |
|----------------------|--------|---------|--------------------------|
| `transformerFactory` | string | `""`    | Custom transformer class |

---

### XSD

XML Schema validation.

```go
builder.To("xsd:file://schema.xsd")
```

| Option           | Type   | Default  | Description     |
|------------------|--------|----------|-----------------|
| `schemaResource` | string | required | XSD schema path |

---

### Template

Go template processing (inspired by Apache Camel Velocity).

```go
// Basic template
builder.To("template:templates/email.tmpl")

// With caching
builder.To("template:templates/item.tmpl?contentCache=true")

// Dynamic template from header
builder.To("template:default.tmpl?allowTemplateFromHeader=true")
```

| Option                    | Type   | Default | Description                               |
|---------------------------|--------|---------|-------------------------------------------|
| `contentCache`            | bool   | `false` | Cache template in memory                  |
| `allowTemplateFromHeader` | bool   | `false` | Allow `CamelTemplatePath` header override |
| `startDelimiter`          | string | `{{`    | Template start delimiter                  |
| `endDelimiter`            | string | `}}`    | Template end delimiter                    |

**Template Variables:**

```
{{.Body}}              # Message body
{{.Headers.name}}      # Header value
{{.Exchange.ID}}         # Exchange ID
{{.Exchange.Created}}    # Creation timestamp
```

**Template Functions:**

```go
{{.Body | upper}}
{{.Body | lower}}
{{.Body | trim}}
{{now | formatDate "2006-01-02 15:04:05"}}
{{"hello" | contains "ell"}}
{"order": {{json .Body}}, "meta": {{json .Headers.meta}}}
```

`json` encodes any value (string, number, map, slice) with `encoding/json`.
Its output escapes `<`, `>` and `&`, so it is safe in HTML as well as in JSON
payloads. Inside a `<script>` block, write `{{.Body}}` instead: the template
engine already encodes values as JavaScript there. The compiled-template cache
(`contentCache`) is keyed by path **and** delimiters.

---

## Execution Components

### Exec

Execute system commands.

```go
builder.To("exec:ls -la")
```

| Option                | Type   | Default | Description                                          |
|-----------------------|--------|---------|------------------------------------------------------|
| `args`                | string | `""`    | Command arguments                                    |
| `workingDir`          | string | `""`    | Working directory                                    |
| `timeout`             | int    | `0`     | Timeout in ms (0=no timeout)                         |
| `outFile`             | string | `""`    | Read the result from this file instead of stdout     |
| `useStderrOnEmpty`    | bool   | `false` | Use stderr as body when stdout is empty              |
| `allowHeaderOverride` | bool   | `false` | Allow `CamelExecCommand*` headers to override the URI |
| `allowedExecutables`  | string | `""`    | Comma-separated bare command names a `CamelExecCommandExecutable` header may select, besides the URI executable |
| `maxOutputSize`       | int    | `33554432` | Bytes kept from stdout and from stderr (32 MiB, `0` = unlimited); the rest is discarded |

**Per-message overrides (opt-in):**

When `allowHeaderOverride=true`, these headers override the URI settings for a single message:

| Header                       | Overrides         |
|------------------------------|-------------------|
| `CamelExecCommandExecutable` | Executable        |
| `CamelExecCommandArgs`       | Arguments         |
| `CamelExecCommandWorkingDir` | Working directory |
| `CamelExecCommandTimeout`    | Timeout (ms)      |

> [!caution] Security
> Header overrides let a message choose **which binary is executed**. They are
> disabled by default; only enable `allowHeaderOverride=true` when message
> headers cannot be influenced by untrusted input (e.g. headers coming from an
> HTTP or mail consumer).
>
> The **executable** and the **working directory** are validated against shell
> metacharacters and path traversal. A header-supplied executable must
> additionally be a **bare command name** (no path, resolved via `PATH`) and
> either the URI executable or one listed in `allowedExecutables`; any other
> is rejected. A bare name alone was not enough: `sh` or `python3` with
> header arguments such as `-c ...` run arbitrary code, so never list a shell
> or an interpreter. **Arguments are not validated**: commands run
> through `execve` without a shell, so `|`, `&`, `$`, `<` and `>` carry no
> special meaning and reach the child process verbatim — which is the intent.
> Rejecting them blocked legitimate values (a JSON document, a password
> containing `$`, a relative path) without preventing any injection. NUL and
> other control characters are still rejected.


**Output:**

- `Out.Body` = command stdout (also mirrored on `In` for backward
  compatibility, so an InOut route sees the command output)
- Header `CamelExecExitValue` = exit code
- Headers `CamelExecStdout` / `CamelExecStderr` = raw command output
- Header `CamelExecOutputTruncated` = `true` when stdout or stderr exceeded
  `maxOutputSize` and was truncated (the command still runs to completion)

---

## Component Configuration

### Authentication

Credentials can be provided via environment variables:

```go
// FTP with env var
builder.From("ftp://host?username=${env:FTP_USER}")

// Or parameter  
builder.From("ftp://host?username=admin&password=${env:FTP_PASS}")
```

### Common Options

Many components share common polling options:

```go
// File polling
builder.From("file://data?delay=10s&delete=true")

// FTP polling
builder.From("ftp://host/incoming?delay=30s&include=*.xml")
```

| Option    | Type     | Default | Description     |
|-----------|----------|---------|-----------------|
| `delay`   | Duration | varies  | Poll interval   |
| `include` | string   | `""`    | Include pattern |
| `exclude` | string   | `""`    | Exclude pattern |

---

## Component Summary Table

| Component  | Category   | Consumer | Producer | URI Pattern               |
|------------|------------|----------|----------|---------------------------|
| Direct     | Core       | ✅        | ✅        | `direct:name`             |
| Timer      | Core       | ✅        | ❌        | `timer:name`              |
| File       | File       | ✅        | ✅        | `file://path`             |
| FTP        | File       | ✅        | ✅        | `ftp://host/path`         |
| SFTP       | File       | ✅        | ✅        | `sftp://host/path`        |
| SMB        | File       | ✅        | ✅        | `smb://host/share`        |
| HTTP       | Network    | ✅        | ✅        | `http://host:port/path`   |
| Net        | Network    | ✅        | ✅        | `net:tcp://host:port`     |
| Kafka      | Messaging  | ✅        | ✅        | `kafka:topic`             |
| gRPC       | Network    | ✅        | ✅        | `grpc://host:port/Service/Method` |
| Telegram   | Messaging  | ✅        | ✅        | `telegram:bots`           |
| OpenAI     | AI         | ❌        | ✅        | `openai:chat`             |
| Anthropic  | AI         | ❌        | ✅        | `anthropic:messages`       |
| Ollama     | AI         | ❌        | ✅        | `ollama:chat`/`generate`/`embed` |
| Jev        | AI         | ❌        | ✅        | `jev:decisions`           |
| Cron       | Scheduling | ✅        | ❌        | `cron://group/job`        |
| SMTP       | Mail       | ❌        | ✅        | `smtp://host:port`        |
| IMAP       | Mail       | ✅        | ❌        | `imap://host:port`        |
| POP3       | Mail       | ✅        | ❌        | `pop3://host:port`        |
| SQL        | Database   | ❌        | ✅        | `sql://datasource`        |
| SQL-Stored | Database   | ❌        | ✅        | `sql-stored://datasource` |
| MongoDB    | Database   | ❌        | ✅        | `mongodb:connectionName`  |
| XSLT       | Transform  | ❌        | ✅        | `xslt:template`           |
| XSD        | Transform  | ❌        | ✅        | `xsd:schema`              |
| Template   | Transform  | ❌        | ✅        | `template:template`       |
| Exec       | Execution  | ❌        | ✅        | `exec:command`            |
| NATS       | Messaging  | ✅        | ✅        | `nats://subject`          |
| Redis      | Messaging  | ✅        | ✅        | `redis://host:port`       |
| Go Channel | Core       | ✅        | ✅        | `chan:channelName`        |

## Delivery and lifecycle details

- **Kafka / STOMP:** failed processing is retried with a fresh exchange before fetching the next record (Kafka) or message (STOMP), with an exponential backoff from `redeliveryDelay` up to `maxRedeliveryDelay`. After `maxRedeliveries` redeliveries (default `10`, about 80 s with the default delays) the consumer gives up on the message: it logs it at error level and commits/acknowledges it so the partition or subscription is not blocked forever. Use a `DeadLetterChannel` to keep such messages, or set `maxRedeliveries=-1` to retry until the route succeeds (the consumer then stays blocked on a permanently failing message). A message the route sent to a `DeadLetterChannel` is committed/acknowledged right away and never redelivered, even with `DeadLetterHandled(false)`: redelivering it would dead-letter a duplicate on every attempt. Kafka commit failures retry the commit every 100 ms without repeating successful processing. Kafka fetch errors are logged and retried every 100 ms; the consumer only ends when its reader is closed by the route stop (an error mentioning `closed` or `EOF` used to end it while the route still appeared started). The resolved Kafka topic is set on each message, so configured and header-overridden topics work together.
- **FTP:** each producer leases its connection exclusively for the complete transfer. Concurrent sends and `Stop` cannot interleave FTP commands; a sender waiting for the lease can cancel.
- **MongoDB:** `save` preserves the input document’s `_id`, including when replacement fails, so retry keeps targeting the same document.
- **Mail:** IMAP IDLE reacts to mailbox notifications by ending IDLE, searching for messages and re-entering IDLE. Existing messages are searched before the first IDLE.
- **Cron:** `Stop` prevents new executions and waits for active jobs before returning, including when pausing a job.
- **Redis:** clients are shared by active producers and consumers and closed when the last owner stops. Stopping one producer leaves other owners usable; stopped route consumers release their registration so the route can restart.
- **File:** a URI ending in `/` denotes a directory even before it exists; `CamelFileName` selects the file inside it.
- **Net:** the configured timeout bounds established writes as well as reply reads. Cancelling the exchange closes its connection and interrupts either operation.
- **Prometheus:** repeated `Metrics()` or `MetricsWith(registry)` calls reuse compatible registered collectors, allowing separate interceptors on multiple routes. Incompatible collector definitions still fail registration.
