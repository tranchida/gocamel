# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
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
0.1.0 ─────────────────── unreleased
  │                            │
  └────────────────────────────┴──────▶ main
```
