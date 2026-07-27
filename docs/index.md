# GoCamel Documentation

**Enterprise Integration Framework for Go**

---

Welcome to GoCamel, a Go implementation of Apache Camel's Enterprise Integration Patterns. GoCamel provides a powerful
DSL for creating integration routes to connect different systems and services.

## Quick Links

- [📧 Installation](en/installation.md)
- [🚀 Quick Start](en/quickstart.md)
- [📚 Core Concepts](en/concepts.md)
- [🔌 Components](en/components.md)
- [✨ Features](en/features.md)
- [📖 API Reference](en/reference.md)

## Features

- **Route & Endpoint Architecture** - Fluent DSL for building integration flows
- **Message Management** - Body and headers with type safety
- **Enterprise Integration Patterns** - Split, Aggregate, Multicast, Choice, and more
- **Simple Language** - Dynamic expressions with `${body}`, `${header.name}`, and functions
- **Multiple Components** - HTTP, File, FTP, SFTP, SMB, Mail, SQL, Telegram, OpenAI, Cron, and more
- **Security Built-in** - Path traversal protection, SQL injection prevention, input sanitization

## Security Notice

GoCamel includes built-in security utilities to protect against common vulnerabilities:

- [🔐 Path traversal protection](en/features.md#security)
- [🔐 SQL injection prevention](en/features.md#security)
- [🔐 Input sanitization](en/features.md#security)
- [🔐 Safe file operations](en/features.md#security)
- [🔐 No credential reflection in HTTP responses](en/features.md#security)
- [🔐 Bounded HTTP body reads](en/features.md#security)
- [🔐 Panic containment](en/features.md#security)

---

🌐 **Languages**: 🇺🇸 **English** (current) | [🇫🇷 Français](fr/)
