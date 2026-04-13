# GEMINI.md - GoCamel Project Context

## Project Overview
**GoCamel** is a lightweight Enterprise Integration Framework for Go, inspired by Apache Camel. It implements various Enterprise Integration Patterns (EIP) and provides a pluggable architecture for connecting different systems through "routes".

### Core Architecture
- **`CamelContext`**: The main container that manages the lifecycle of routes and components.
- **`Route`**: A sequence of processors starting from a source (Consumer) to one or more destinations (Producers).
- **`Exchange` & `Message`**: The data model. An `Exchange` carries an `In` message, an optional `Out` message, and a map of properties.
- **`Component`**: A factory for `Endpoint`s, registered by URI scheme (e.g., `http:`, `file:`, `ftp:`).
- **`Endpoint`**: Represents an external system URI; creates `Consumer`s (to read) or `Producer`s (to write).
- **`RouteBuilder`**: A fluent Go DSL for defining routes: `.From(uri).ProcessFunc(fn).To(uri).Build()`.

### Key Components
| Scheme | Description |
|--------|-------------|
| `http` | HTTP server (Consumer) and client (Producer) |
| `file` | Local file polling and writing |
| `ftp`/`sftp` | FTP and SFTP file transfer |
| `smb` | Windows share (Samba) integration |
| `telegram` | Bot integration (polling/webhooks and sending) |
| `openai` | AI chat completion integration (Producer only) |
| `quartz` | Advanced scheduling with Cron or intervals |
| `xslt`/`xsd` | XML transformation and validation |
| `exec` | Local system command execution |

## Building and Running

### Prerequisites
- Go 1.23.0 or higher.

### Key Commands
- **Build the project**: `go build ./...`
- **Run all tests**: `go test ./...`
- **Run a specific test**: `go test -v -run TestName ./...`
- **Run examples**: `go run examples/<example-dir>/main.go` (e.g., `go run examples/http-echo/http_echo.go`)

### Configuration
Sensitive parameters (API keys, passwords) can be provided via:
1. **Environment Variables** (Recommended): e.g., `OPENAI_API_KEY`, `FTP_PASSWORD`.
2. **URI Query Parameters**: e.g., `telegram:bots?authorizationToken=XYZ`.
3. **URI Userinfo**: e.g., `ftp://user:pass@host/path`.

## Development Conventions

### Code Style
- **Naming**: Follow standard Go naming conventions. Use PascalCase for exported symbols.
- **Header Constants**: Use predefined `Camel*` constants in `exchange.go` for common message headers (e.g., `CamelFileName`, `CamelHttpMethod`).
- **Config Lookup**: Always use `GetConfigValue(u, key)` from `config.go` in component implementations to ensure consistent lookup across env vars, query params, and URL userinfo.

### Implementing New Components
1. Create `<name>_component.go`.
2. Implement `Component`, `Endpoint`, and `Consumer`/`Producer` interfaces.
3. For file-based consumers, utilize `file_filter.go` for name filtering.
4. Add tests in `<name>_component_test.go`.

### Error Handling
- Use `ErrStopRouting` as a sentinel error to halt route processing without logging it as a failure (used in EIPs like Aggregator).

### Management API
GoCamel includes a REST management server (JMX-like) available via `NewManagementServer(context)`. It exposes:
- `GET /api/context`: Context status.
- `GET /api/routes`: List of routes.
- `POST /api/routes/{id}/start|stop`: Control individual routes.
