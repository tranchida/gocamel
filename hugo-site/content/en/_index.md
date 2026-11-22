+++
title = "GoCamel Documentation"
weight = 0
[build]
  list = "always"
+++

**Latest release: v1.0.0 · Go 1.27.0 or later.** [Release notes](releases/v1.0.0/)


**Enterprise Integration Framework for Go**

---

Welcome to the GoCamel documentation.

## Quick Links

- [Installation](installation.md)
- [Quick Start](quickstart.md)
- [Core Concepts](concepts.md)
- [Components](components.md)
- [Features](features.md)
- [API Reference](reference.md)

## Features

- **Route & Endpoint Architecture** — Fluent DSL for building integration flows
- **Message Management** — Typed body and headers
- **Enterprise Integration Patterns** — Split, Aggregate, Multicast, Choice, and more
- **Simple Language** — Dynamic expressions with `${body}`, `${header.name}` and functions
- **Multiple Components** — HTTP, File, FTP, SFTP, SMB, Mail, SQL, Telegram, OpenAI, Cron, and more
- **Built-in Security** — Path traversal protection, SQL injection prevention, input sanitization

## Security Notice

GoCamel includes built-in security utilities (`security.go`) to protect against common vulnerabilities:

- [🔐 Path traversal protection](features.md#security)
- [🔐 SQL injection prevention](features.md#security)
- [🔐 Input sanitization](features.md#security)
- [🔐 Safe file operations](features.md#security)
- [🔐 No credential reflection in HTTP responses](features.md#security)
- [🔐 Bounded HTTP body reads](features.md#security)
- [🔐 Panic containment](features.md#security)

---

🌐 [🇫🇷 Version Française](../fr/)
