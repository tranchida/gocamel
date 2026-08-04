+++
title = "Configuration"
weight = 21
+++
## Aperçu

GoCamel supporte plusieurs stratégies de configuration pour les credentials, les endpoints et les paramètres des composants.

## Sources de credentials

`gocamel.GetConfigValue(url, key)` est le point d'entrée unique utilisé par
tous les composants pour résoudre une valeur de configuration. Il consulte
quatre sources dans l'ordre suivant — la première valeur non vide gagne :

1. **Variable d'environnement exacte.** La clé est utilisée telle quelle.
   Exemple : `GetConfigValue(_, "OPENAI_API_KEY")` lit
   `os.Getenv("OPENAI_API_KEY")`.
2. **Variable d'environnement préfixée par le scheme.** Construite en
   majuscules : `scheme + "_" + key`. Pour une URI `ftp://` et la clé
   `password`, la résolution interroge `os.Getenv("FTP_PASSWORD")`.
3. **Paramètre de query string.** Exemple : `ftp://host?password=secret`.
4. **Userinfo de l'URI**, uniquement pour les clés bien connues
   `username`/`user` et `password`/`pass`. Exemple :
   `ftp://user:pass@host`.

### Variables d'environnement (Recommandé)

Configuration sensible via l'environnement système :

```bash
export FTP_USERNAME="myuser"
export FTP_PASSWORD="secret"
export TELEGRAM_AUTHORIZATIONTOKEN="bot-token"
export OPENAI_API_KEY="api-key"
```

Les routes référencent ensuite l'option directement — la variable
d'environnement est récupérée automatiquement :

```go
builder.From("ftp://host?username=myuser")  // FTP_PASSWORD vient de l'environnement
```

### Paramètres de query string

Passez les options en tant que paramètres de query string :

```go
builder.From("ftp://host?username=admin&password=secret")
```

> [!caution] Credentials dans les query strings : signalés au runtime
> Les URI fuitent régulièrement dans les logs d'erreur de connexion, les
> pistes d'audit, les rapports de crash et les access logs de proxy.
> Quand `GetConfigValue` résout une clé reconnue comme credential
> (`password`, `pass`, `secret`, `token`, `apikey`, `api_key`, `accesskey`,
> `access_key`, `clientsecret`, `privatekey`) depuis un paramètre de
> query, un log `WARN` est émis avec une recommandation d'utiliser les
> variables d'environnement. Le warning n'inclut que le scheme et le nom
> de la clé — **jamais la valeur** — et il est dédupé par couple
> `(scheme, key)` pour qu'un consumer en polling ne sature pas les logs.
>
> Utilisez les variables d'environnement en production. La forme userinfo
> (`user:pass@host`) est considérée comme la manière conventionnelle de
> porter des credentials dans une URI et n'est **pas** signalée — vous y
> avez explicitement consenti.


### Directement dans l'URI (forme userinfo)

```go
builder.From("ftp://user:pass@host:21/path")
```

## Configuration du contexte

```go
ctx := gocamel.NewCamelContext()

// Configuration custom
cfg := ctx.Config()
```

## Sécurité

**Ne jamais hardcoder les credentials :**

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

## Enregistrement des composants

### Composants standards

```go
ctx.AddComponent("ftp", gocamel.NewFTPComponent())
ctx.AddComponent("http", gocamel.NewHTTPComponent())
ctx.AddComponent("timer", gocamel.NewTimerComponent())
ctx.AddComponent("direct", gocamel.NewDirectComponent())
```

### Composants planifiés

```go
ctx.AddComponent("cron", gocamel.NewCronComponent())
```

### Composants de messagerie

```go
ctx.AddComponent("telegram", gocamel.NewTelegramComponent())
```

### Composants IA

```go
ctx.AddComponent("openai", gocamel.NewOpenAIComponent())
```

### Composants de base de données

```go
import "database/sql"
import _ "github.com/mattn/go-sqlite3"

db, _ := sql.Open("sqlite3", "./app.db")

sqlComp := gocamel.NewSQLComponent()
sqlComp.RegisterDataSource("appdb", db)
ctx.AddComponent("sql", sqlComp)
```

## Configuration des sources de données

### Composant SQL

```go
// Plusieurs sources de données
sqlComp.RegisterDataSource("primary", db1)
sqlComp.RegisterDataSource("secondary", db2)

// Source de données par défaut
sqlComp.SetDefaultDataSource(db)
```

### Composant SQL-Stored

```go
sqlStored := gocamel.NewSQLStoredComponent()
sqlStored.RegisterDataSource("mydb", db)
ctx.AddComponent("sql-stored", sqlStored)
```

## Configuration des routes

### Définir l'ID de route

```go
route := context.CreateRouteBuilder().
    From("timer:tick").
    SetID("timer-route-1").
    To("direct:output").
    Build()
```

### Options courantes

```go
// Délai de polling
builder.From("file://input?delay=10s&delete=true")

// Méthode HTTP
builder.To("http://api?httpMethod=POST")
```

## Correspondance des variables d'environnement

| Composant | Variable                                                  | Description       |
|-----------|-----------------------------------------------------------|-------------------|
| FTP       | `FTP_USERNAME`, `FTP_PASSWORD`                            | Credentials FTP   |
| SFTP      | `SFTP_USERNAME`, `SFTP_PASSWORD`, `SFTP_PRIVATE_KEY_FILE` | Credentials SSH   |
| Telegram  | `TELEGRAM_AUTHORIZATIONTOKEN`                             | Token du bot      |
| OpenAI    | `OPENAI_AUTHORIZATIONTOKEN`, `OPENAI_API_KEY`             | Clés API          |
| Mail      | `MAIL_USERNAME`, `MAIL_PASSWORD`                          | Credentials email |

## Bonnes pratiques

1. **Ne jamais committer de secrets** — Utilisez les variables d'environnement ou un gestionnaire de secrets
2. **Utiliser des configurations différentes par environnement** — Dev/staging/prod
3. **Documenter les variables d'environnement requises** — Dans le README ou la documentation de déploiement
4. **Valider la configuration au démarrage** — Échouer rapidement si config manquante