# Composants

Vue complète des composants disponibles dans GoCamel.

## Core Components

### Direct

Routage synchrone en mémoire entre routes du même contexte.

```go
builder.From("direct:start").To("direct:process")
```

| Option | Type | Défaut | Description |
|--------|------|--------|-------------|
| `reuseChannel` | bool | `true` | Réutiliser le même channel |

---

### Timer

Déclenchement périodique simple.

```go
builder.From("timer:tick?period=5s")
```

| Option | Type | Défaut | Description |
|--------|------|--------|-------------|
| `period` | Duration | `1s` | Période entre déclenchements |
| `repeatCount` | int | `0` | Nombre de répétitions (0=infini) |
| `fixedRate` | bool | `false` | Mode fixed-rate vs fixed-delay |

---

## File Transfer

### File

Lecture/écriture de fichiers locaux.

```go
// Consumer (lecture)
builder.From("file://input?delete=true")

// Producer (écriture)
builder.To("file://output")
```

| Option | Type | Défaut | Description |
|--------|------|--------|-------------|
| `delete` | bool | `false` | Supprimer après traitement |
| `noop` | bool | `false` | Ne pas déplacer/supprimer |
| `include` | string | `""` | Pattern fichiers inclus |
| `exclude` | string | `""` | Pattern fichiers exclus |

---

### FTP / FTPS

Transfert de fichiers via FTP.

```go
builder.From("ftp://host:21/incoming?username=${env:FTP_USER}")
```

---

### SFTP

Transfert sécurisé via SSH.

```go
builder.From("sftp://host:22/data?username=scott")
```

---

### SMB

Partages Windows/Samba.

```go
builder.From("smb://server/share/folder")
```

---

## Network

### HTTP

Serveur et client HTTP.

```go
// Consumer (serveur)
builder.From("http://localhost:8080/api")

// Producer (client)
builder.To("http://example.com/webhook")
```

---

## Messaging

### Telegram

Bot Telegram pour recevoir/envoyer des messages.

```go
ctx.AddComponent("telegram", gocamel.NewTelegramComponent())
builder.From("telegram:bots").Log("${body}")
```

---

## AI / LLM

### OpenAI

Intégration ChatGPT / GPT-4.

```go
ctx.AddComponent("openai", gocamel.NewOpenAIComponent())

endpoint, _ := ctx.CreateEndpoint("openai:chat?model=gpt-4")
producer, _ := endpoint.CreateProducer()
```

| Option | Type | Défaut | Description |
|--------|------|--------|-------------|
| `model` | string | `gpt-3.5-turbo` | Modèle OpenAI |

---

## Scheduling

### Cron

Scheduling avancé avec expressions cron.

```go
ctx.AddComponent("cron", gocamel.NewCronComponent())

// Cron:
builder.From("cron://group/job?cron=0+*+*+*+*+*")

// Simple interval:
builder.From("cron://poller?trigger.repeatInterval=5000")
```

---

## Transformation

### XSLT

Transformation XML via feuille de style.

```go
builder.To("xslt:file://transform.xsl")
```

---

### Template

Transformation via templates Go natifs (inspiré du composant Velocity d'Apache Camel).

```go
// Template basique
builder.To("template:templates/email.tmpl")

// Avec caching
builder.To("template:templates/item.tmpl?contentCache=true")

// Template dynamique
builder.To("template:default.tmpl?allowTemplateFromHeader=true")
```

| Option | Type | Défaut | Description |
|--------|------|--------|-------------|
| `contentCache` | bool | `false` | Cache le template en mémoire |
| `allowTemplateFromHeader` | bool | `false` | Permet override via header `CamelTemplatePath` |
| `startDelimiter` | string | `{{` | Délimiteur de début |
| `endDelimiter` | string | `}}` | Délimiteur de fin |

**Variables disponibles dans le template :**

```
{{.Body}}              # Corps du message
{{.Headers.name}}      # Valeur d'un header
{{.Exchange.ID}}       # ID de l'échange
{{.Exchange.Created}}  # Date de création
```

**Fonctions disponibles :**

```
{{.Body | upper}}, {{.Body | lower}}, {{.Body | trim}}
{{now | formatDate "2006-01-02 15:04:05"}}
{{"hello" | contains "ell"}}
```

---

### XSD

Validation XML via schéma XSD.

```go
builder.To("xsd:file://schema.xsd")
```

---

## Execution

### Exec

Exécution de commandes système.

```go
builder.To("exec:ls -la")
```

---

## Configuration des composants

### Authentification

```go
// Via variables d'environnement
builder.From("ftp://host?username=${env:FTP_USER}")
```

### Options communes

```go
// File: polling options
builder.From("file://data?delay=10s&delete=true")

// HTTP: méthode
builder.To("http://api?httpMethod=POST")
```
