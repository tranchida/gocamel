# Configuration

## Variables d'environnement

Configuration sensible via l'environnement système:

```bash
# OpenAI
export OPENAI_API_KEY="sk-..."

# Telegram
export TELEGRAM_AUTHORIZATIONTOKEN="..."

# FTP/SFTP
export FTP_USERNAME="user"
export FTP_PASSWORD="secret"
export SFTP_USERNAME="user"
export SFTP_PASSWORD="secret"

# SMB
export SMB_USERNAME="domain\\user"
export SMB_PASSWORD="secret"
```

## Configuration du contexte

```go
ctx := gocamel.NewCamelContext()

// Configuration custom
cfg := ctx.Config()
```

## Sécurité

**Ne jamais hardcoder les credentials:**

```go
// ❌ MAUVAIS
endpoint := "ftp://user:secret@host"

// ✅ BON
endpoint := "ftp://host?username=${env:FTP_USER}"
```

## Proxy

```go
os.Setenv("HTTP_PROXY", "http://proxy.company:8080")
os.Setenv("HTTPS_PROXY", "http://proxy.company:8080")
os.Setenv("NO_PROXY", "localhost,127.0.0.1")
```
