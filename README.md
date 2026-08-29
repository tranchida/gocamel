<div align="center">

# GoCamel

**Enterprise Integration Framework for Go**

[![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go)](https://golang.org/)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

🌐 **Language / Langue**: 🇺🇸 English | [**🇫🇷 Français**](./README.fr.md)

</div>

---

GoCamel is an enterprise integration library inspired by [Apache Camel](https://camel.apache.org/), written in Go. It
provides a powerful DSL for creating integration routes to connect different systems and services.

## ✨ Features

- 🔀 **Route & Endpoint Architecture** - Fluent DSL for building integration flows
- 📬 **Message Management** - Body and headers with typed accessors (`GetBodyAsString`, `GetHeaderAsInt`, etc.)
- 🔄 **Camel Context** - Lifecycle management for routes and components
- 📦 **Registry & Beans** - Register and lookup custom processors and beans by name
- ⚙️ **Processors** - Custom logic implementation with `Processor` interface and `ProcessRef`
- ⚖️ **Transactional Routes** - Unit of Work pattern for reliable message processing
- 🧩 **Enterprise Integration Patterns** - Split, Aggregate, Multicast, Choice, Filter, Recipient List, Routing Slip, Wire Tap,
  Stop, ToD, Loop, Saga, Transform, Enrich, PollEnrich, Validate, Message History
- 📝 **Simple Language & JSONPath** - Dynamic expressions (`${body}`, `${header.name}`, `${jsonpath:$.path}`, functions)
- 🔌 **Multiple Components** - HTTP, File, FTP, SFTP, SMB, Mail, SQL, Telegram, OpenAI, Cron, Redis, NATS, Go Channel (chan), TCP/UDP (net), etc.
- 🛠️ **REST Management API** - JMX-like monitoring and control
- 🔒 **Security Utilities** - Path traversal protection, SQL injection prevention, input sanitization

## 📦 Installation

The core module (routing engine, EIPs, Simple Language and all stdlib-only
components: direct, seda, chan, timer, http, exec, template, sql) has **zero
external runtime dependencies** and builds without CGO (tests use the pure-Go
`modernc.org/sqlite` driver):

```bash
go get gitlab.com/tranchida/gocamel
```

Components that rely on third-party libraries live in their own Go modules —
you only pull the dependencies of what you actually use:

```bash
go get gitlab.com/tranchida/gocamel/components/kafka     # segmentio/kafka-go
go get gitlab.com/tranchida/gocamel/components/grpc      # google.golang.org/grpc
go get gitlab.com/tranchida/gocamel/components/redis    # go-redis
go get gitlab.com/tranchida/gocamel/components/nats     # nats.go
go get gitlab.com/tranchida/gocamel/components/mongodb  # mongo-driver
go get gitlab.com/tranchida/gocamel/components/mail     # SMTP/IMAP/POP3
go get gitlab.com/tranchida/gocamel/components/ftp
go get gitlab.com/tranchida/gocamel/components/sftp
go get gitlab.com/tranchida/gocamel/components/smb
go get gitlab.com/tranchida/gocamel/components/telegram
go get gitlab.com/tranchida/gocamel/components/openai
go get gitlab.com/tranchida/gocamel/components/anthropic
go get gitlab.com/tranchida/gocamel/components/ollama
go get gitlab.com/tranchida/gocamel/components/cron     # robfig/cron
go get gitlab.com/tranchida/gocamel/components/file     # fsnotify watcher
go get gitlab.com/tranchida/gocamel/observability/prometheus
go get gitlab.com/tranchida/gocamel/observability/otel
```

```go
import (
	"gitlab.com/tranchida/gocamel"
	"gitlab.com/tranchida/gocamel/components/redis"
)

ctx := gocamel.NewCamelContext()
ctx.AddComponent("redis", redis.NewRedisComponent())
```

Metrics and tracing are route interceptors provided by the observability
modules:

```go
import (
	gocamelotel "gitlab.com/tranchida/gocamel/observability/otel"
	gocamelprom "gitlab.com/tranchida/gocamel/observability/prometheus"
)

ctx.CreateRouteBuilder().
	From("timer:tick?period=5s").
	Intercept(gocamelprom.Metrics()).
	Intercept(gocamelotel.Tracing()).
	...
```

## 🚀 Quick Start

```go
package main

import (
	"gitlab.com/tranchida/gocamel"
)

func main() {
	context := gocamel.NewCamelContext()

	route := context.CreateRouteBuilder().
		From("timer:tick?period=5s").
		SetBody("Hello World").
		Log("${body}").
		To("direct:output").
		Build()

	context.AddRoute(route)
	context.Start()
	select {}
}
```

## 📝 Simple Language Examples

```go
// Dynamic expressions
builder.From("direct:start").
SimpleSetBody("Hello ${body} at ${date:now}").
SimpleSetHeader("X-Request-ID", "${uuid}").
To("direct:output")

// Content-based routing
builder.From("direct:start").
Choice().
When("${header.priority == 'high'}").
To("direct:urgent").
When("${body['count'] > 100}").
To("direct:large-batch").
Otherwise().
To("direct:normal").
EndChoice()
```

## 🔌 Available Components

| Component      | Description                   |
|----------------|-------------------------------|
| **HTTP**       | HTTP server and client        |
| **Net**        | Raw TCP/UDP sockets           |
| **Kafka**      | Apache Kafka messaging        |
| **gRPC**       | gRPC client and server        |
| **File**       | Local file system operations  |
| **FTP/SFTP**   | File transfer via FTP/SSH     |
| **SMB**        | Windows/Samba shares          |
| **Direct**     | In-memory synchronous routing |
| **NATS**       | Asynchronous event messaging  |
| **Redis**      | Commands & Pub/Sub messaging  |
| **Go Channel** | Native Go channel integration |
| **Timer/Cron** | Scheduled triggers            |
| **Telegram**   | Bot integration               |
| **OpenAI**     | ChatGPT/GPT-4 & OpenAI-compatible providers (OpenRouter, Groq, ...) |
| **Anthropic**  | Claude Messages API (native)  |
| **Ollama**     | Local LLMs (chat/generate/embed, native API) |
| **Mail**       | SMTP/IMAP/POP3 email          |
| **SQL**        | Database queries              |
| **MongoDB**    | MongoDB CRUD operations       |
| **XSLT/XSD**   | XML transformation/validation |
| **Template**   | Go template processing        |
| **Exec**       | System command execution      |

## 🧾 Data Formats

| Data Format | Description                                                        |
|-------------|--------------------------------------------------------------------|
| **JSON**    | Marshal/unmarshal JSON with `MarshalJson()` / `UnmarshalJson()` or `Marshal(NewJSONDataFormat(...))` |
| **XML**     | Marshal/unmarshal XML with `MarshalXml()` / `UnmarshalXml()` or `Marshal(NewXMLDataFormat(...))` |
| **CSV**     | Marshal/unmarshal CSV with `MarshalCSV()` / `UnmarshalCSV()` or `Marshal(NewCSVDataFormat(...))` |
| **RSS**     | Marshal/unmarshal RSS XML with `Marshal(NewRSSDataFormat())` and `Unmarshal(NewRSSDataFormat())` |

## 🔒 Security

GoCamel includes built-in security utilities to protect against common vulnerabilities:

### Security Features

- **Path Traversal Protection** - Prevents directory traversal attacks (e.g., `../etc/passwd`)
- **SQL Injection Prevention** - The query is trusted configuration and is never interpolated with message data; dynamic values are bound as positional parameters. Stacked statements are rejected while single/double quotes, backticks and comments are supported
- **Input Sanitization** - Removes null bytes and control characters from user input
- **Path Validation** - Ensures file paths stay within allowed directories, resolving symlinks so a link inside the base directory cannot escape it
- **No Credential Reflection** - The HTTP consumer never echoes inbound request headers (`Cookie`, `Authorization`, ...) back in its response
- **Bounded Body Reads** - HTTP request and response bodies are size-limited (32 MiB by default) to prevent memory exhaustion
- **Panic Containment** - A panicking processor fails its exchange rather than the whole process

### Security Utilities (`security.go`)

```go
// Validate a file path
err := gocamel.ValidatePath("/data/file.txt")
if err != nil {
// Path contains traversal patterns or null bytes
}

// Ensure path is within specific directory
err = gocamel.ValidatePathInDir("/data/output.txt", "/data")

// Check if path is safe
isSafe := gocamel.IsSafePath("/data/file.txt", false)

// Sanitize user input
sanitized := gocamel.SanitizeInput(userInput)
```

### Best Practices

- Always validate file paths from user input
- Use `ValidatePathInDir()` when working with uploads or file operations
- Never execute unsanitized external commands
- Use parameter binding for SQL queries instead of string concatenation
- Put a middleware on the HTTP consumer (`SetMiddleware`) before exposing it to an untrusted network — it ships with no authentication

## ⚠️ Upgrade Notes

Two defaults changed for security and Apache Camel alignment. Both are
opt-out if the previous behaviour is required.

**The `CamelSqlQuery` header no longer overrides the configured statement.**
The header replaced the SQL statement wholesale, so any component mapping
external metadata onto headers could hand a caller full control of the query.
It is now ignored unless the endpoint opts in:

```go
// Before: the header silently replaced the statement
builder.To("sql://appdb?query=SELECT+1")

// After: opt in explicitly, and only with trusted headers
builder.To("sql://appdb?allowHeaderOverride=true&query=SELECT+1")
```

**The HTTP producer treats a response status ≥ 300 as an error**, matching
Apache Camel. The error wraps `gocamel.ErrHTTPStatus`; the body and
`CamelHttpResponseCode` are still available on `Out`:

```go
// Restore the previous permissive behaviour
http := gocamel.NewHTTPComponent()
http.SetThrowExceptionOnFailure(false)
```

**MongoDB `update` rejects an empty filter.** Like `remove` before it, an
`update` whose body carries no `"filter"` key would mass-update every document
in the collection. It is now rejected unless the endpoint opts in:

```go
// Opt in explicitly to allow updating the whole collection
builder.To("mongodb://myconn?collection=items&operation=update&allowUpdateAll=true")
```

Two further behaviour changes are unlikely to need action: splitting a
`[]byte` body now yields a single part instead of one exchange per byte, and
the HTTP consumer no longer echoes unchanged inbound request headers in its
response.

## 📚 Documentation

- [**Live Documentation Website**](https://tranchida.gitlab.io/gocamel/)
- [Examples](examples/)
- [Architecture](https://tranchida.gitlab.io/gocamel/en/architecture/)
- [API Reference](https://tranchida.gitlab.io/gocamel/en/reference/)

## 🤝 Contributing

Contributions are welcome! Please see [GitLab Issues](https://gitlab.com/tranchida/gocamel/-/issues).

## ✍️ Author

**Giampaolo Tranchida** - [giampaolo@tranchida.ch](mailto:giampaolo@tranchida.ch)

## 📄 License

Apache License, Version 2.0 - see [LICENSE](LICENSE) file for details.

---

<div align="center">
  <sub>Built with ❤️ by [Giampaolo Tranchida](mailto:giampaolo@tranchida.ch) and the GoCamel team</sub>
</div>
