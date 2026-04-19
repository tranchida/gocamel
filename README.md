<div align="center">

# GoCamel

**Enterprise Integration Framework for Go**

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go)](https://golang.org/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

🌐 **Language / Langue**: 🇺🇸 English | [**🇫🇷 Français**](./README.fr.md)

</div>

---

GoCamel is an enterprise integration library inspired by [Apache Camel](https://camel.apache.org/), written in Go. It provides a powerful DSL for creating integration routes to connect different systems and services.

## ✨ Features

- 🔀 **Route & Endpoint Architecture** - Fluent DSL for building integration flows
- 📬 **Message Management** - Body and headers with type safety
- 🔄 **Camel Context** - Lifecycle management for routes and components
- 🧩 **Enterprise Integration Patterns** - Split, Aggregate, Multicast, Choice, Stop, ToD
- 📝 **Simple Language** - Dynamic expressions (`${body}`, `${header.name}`, functions)
- 🔌 **Multiple Components** - HTTP, File, FTP, SFTP, SMB, Mail, SQL, Telegram, OpenAI, Cron, etc.
- 🛠️ **REST Management API** - JMX-like monitoring and control
- 🔒 **Security Utilities** - Path traversal protection, SQL injection prevention, input sanitization

## 📦 Installation

```bash
go get github.com/tranchida/gocamel
```

## 🚀 Quick Start

```go
package main

import (
    "github.com/tranchida/gocamel"
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

| Component | Description |
|-----------|-------------|
| **HTTP** | HTTP server and client |
| **File** | Local file system operations |
| **FTP/SFTP** | File transfer via FTP/SSH |
| **SMB** | Windows/Samba shares |
| **Direct** | In-memory synchronous routing |
| **Timer/Cron** | Scheduled triggers |
| **Telegram** | Bot integration |
| **OpenAI** | ChatGPT/GPT-4 API |
| **Mail** | SMTP/IMAP/POP3 email |
| **SQL** | Database queries |
| **XSLT/XSD** | XML transformation/validation |
| **Template** | Go template processing |
| **Exec** | System command execution |

## 🔒 Security

GoCamel includes built-in security utilities to protect against common vulnerabilities:

### Security Features

- **Path Traversal Protection** - Prevents directory traversal attacks (e.g., `../etc/passwd`)
- **SQL Injection Prevention** - Input validation for SQL queries
- **Input Sanitization** - Removes null bytes and control characters from user input
- **Path Validation** - Ensures file paths stay within allowed directories

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

## 📚 Documentation

- [Full Documentation](docs/)
- [Examples](examples/)
- [Architecture](docs/architecture.md)
- [API Reference](docs/reference.md)

## 🤝 Contributing

Contributions are welcome! Please see [GitHub Issues](https://github.com/tranchida/gocamel/issues).

## 📄 License

MIT License - see [LICENSE](LICENSE) file for details.

---

<div align="center">
  <sub>Built with ❤️ by the GoCamel team</sub>
</div>
