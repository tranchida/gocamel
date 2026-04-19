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

## Mail

### Envoi/Réception d'emails

Le composant Mail supporte SMTP/SMTPS (envoi) et IMAP/IMAPS/POP3/POP3S (réception).

```go
// Envoi SMTP
builder.To("smtps://smtp.gmail.com:465?to=dest@example.com")

// Réception IMAP avec IDLE
builder.From("imaps://imap.gmail.com:993?folderName=INBOX&idle=true")

// Réception POP3
builder.From("pop3s://pop.gmail.com:995?username=user&password=pass")
```

| Option | Type | Défaut | Description |
|--------|------|--------|-------------|
| `username` | string | "" | Credentials d'authentification |
| `password` | string | "" | Credentials d'authentification |
| `to` | string | "" | Destinataire(s) (Producer) |
| `subject` | string | "" | Sujet du message (Producer) |
| `contentType` | string | "text/plain" | Type MIME (Producer) |
| `folderName` | string | "INBOX" | Dossier IMAP (Consumer) |
| `unseen` | bool | true | Messages non lus seulement |
| `idle` | bool | false | IMAP IDLE pour notifications push |
| `delete` | bool | false | Supprimer après traitement |
| `fetchSize` | int | -1 | Messages max par poll |
| `pollDelay` | int | 60000 | Délai entre polls (ms) |

Voir l'exemple complet dans [examples/mail/](../examples/mail/).

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

## Base de données

### SQL

Exécution de requêtes SQL (`SELECT`, `INSERT`, `UPDATE`, `DELETE`) via `database/sql`.
Le composant est **Producer uniquement** : l'utilisateur enregistre ses `*sql.DB`
sur le composant, puis les référence par nom dans l'URI.

```go
import (
    "database/sql"
    _ "github.com/mattn/go-sqlite3"
)

db, _ := sql.Open("sqlite3", "./app.db")

sqlComp := gocamel.NewSQLComponent()
sqlComp.RegisterDataSource("appdb", db)
// ou : sqlComp.SetDefaultDataSource(db)
ctx.AddComponent("sql", sqlComp)

// SELECT -> Out.Body = []map[string]any
builder.From("direct:list").
    To("sql://appdb?query=SELECT+id,name+FROM+users").
    Log("${body}")

// SELECT avec une seule ligne -> Out.Body = map[string]any
builder.From("direct:one").
    SetHeader(gocamel.SqlParameters, []any{42}).
    To("sql://appdb?query=SELECT+*+FROM+users+WHERE+id=?&outputType=SelectOne")

// INSERT/UPDATE/DELETE -> Out.Body = lignes affectées (int64)
builder.From("direct:insert").
    SetHeader(gocamel.SqlParameters, []any{"alice", "alice@example.com"}).
    To("sql://appdb?query=INSERT+INTO+users(name,email)+VALUES(?,?)&transacted=true")
```

**Format URI**

```
sql://<datasourceName>?query=<SQL>
sql://logical?dataSourceRef=<datasourceName>&query=<SQL>
```

| Option | Type | Défaut | Description |
|--------|------|--------|-------------|
| `query` | string | — | Requête SQL (obligatoire) — peut contenir `${header.x}`, `${body}`, `${property.x}` |
| `dataSourceRef` | string | host URI | Nom d'une datasource enregistrée via `RegisterDataSource` |
| `outputType` | string | `SelectList` | `SelectList` (`[]map[string]any`) ou `SelectOne` (`map[string]any`) |
| `batch` | bool | `false` | Mode batch : body = `[][]any`, une exécution par jeu de paramètres dans une transaction |
| `transacted` | bool | `false` | Englobe la requête dans une transaction (`BEGIN`/`COMMIT`/`ROLLBACK`) |

**Paramètres de la requête**

Les paramètres positionnels (`?`) sont fournis via, par ordre de priorité :
1. le header `CamelSqlParameters` (`[]any`)
2. le body s'il s'agit d'un `[]any`

**Headers**

| Header | Sens | Description |
|--------|------|-------------|
| `CamelSqlQuery` | In | Surcharge la requête configurée dans l'URI |
| `CamelSqlParameters` | In | Paramètres positionnels (`[]any`) |
| `CamelSqlRowCount` | Out | Nombre de lignes retournées (SELECT) ou affectées (INSERT/UPDATE/DELETE) |
| `CamelSqlColumnNames` | Out | Noms des colonnes retournées pour un SELECT (`[]string`) |

**Body résultat**

| Cas | Type du `Out.Body` |
|-----|---------------------|
| `SELECT` + `outputType=SelectList` (défaut) | `[]map[string]any` |
| `SELECT` + `outputType=SelectOne` | `map[string]any` (ou `nil` si aucun résultat) |
| `INSERT` / `UPDATE` / `DELETE` | `int64` (lignes affectées) |
| `batch=true` | `int64` (total des lignes affectées) |

---

### SQL-Stored

Exécution de procédures stockées SQL avec support des paramètres IN, OUT et INOUT.

```go
import (
    "database/sql"
    _ "github.com/mattn/go-sqlite3"
)

db, _ := sql.Open("mysql", "user:pass@/mydb")

sqlStored := gocamel.NewSQLStoredComponent()
sqlStored.RegisterDataSource("mydb", db)
ctx.AddComponent("sql-stored", sqlStored)

// Appel simple avec paramètres IN
builder.From("direct:call").
    SetBody([]gocamel.StoredProcedureParam{
        {Name: "userId", Direction: gocamel.ParamDirectionIn, Value: 42},
    }).
    To("sql-stored://mydb?procedure=GET_USER_BY_ID").
    Log("${body}")
```

**Format URI**

```
sql-stored://datasourceName?procedure=NAME
sql-stored://logical?dataSourceRef=dsName&procedure=NAME
```

| Option | Type | Défaut | Description |
|--------|------|--------|-------------|
| `procedure` | string | — | Nom de la procédure stockée (obligatoire) |
| `dataSourceRef` | string | host URI | Nom de la datasource |
| `outputType` | string | `SelectList` | `SelectList` (défaut) ou `SelectOne` |
| `transacted` | bool | `false` | Exécution dans une transaction |
| `noop` | bool | `false` | Mode test : ne pas exécuter |

**Paramètres**

Les paramètres sont fournis via le body comme `[]StoredProcedureParam` :

```go
params := []gocamel.StoredProcedureParam{
    {Name: "inParam", Direction: gocamel.ParamDirectionIn, Value: "value"},
    {Name: "outParam", Direction: gocamel.ParamDirectionOut},
    {Name: "inOutParam", Direction: gocamel.ParamDirectionInOut, Value: 123},
}
exchange.GetIn().SetBody(params)
```

| Direction | Description |
|-----------|-------------|
| `ParamDirectionIn` | Entrée seule |
| `ParamDirectionOut` | Sortie seule |
| `ParamDirectionInOut` | Entrée et sortie |

**Headers**

| Header | Sens | Description |
|--------|------|-------------|
| `CamelSqlStoredProcedureName` | In | Surcharge le nom de la procédure |
| `CamelSqlRowCount` | Out | Nombre de lignes retournées |

**Body résultat**

- Avec résultat : `[]map[string]interface{}` ou `map[string]interface{}`
- Output params seulement : `map[string]interface{}`
- Sans résultat : `nil`

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
