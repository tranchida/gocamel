# Référence des Composants

## Vue d'ensemble

Référence complète de tous les composants GoCamel disponibles. Les composants fournissent la connectivité à divers systèmes et services.

## Composants Principaux

### Direct

Routage synchrone en mémoire entre routes dans le même contexte.

```go
// Consumer : reçoit depuis un endpoint direct
// Producer : envoie vers un endpoint direct
builder.From("direct:start").To("direct:process")
```

Les endpoints sont identifiés par **leur nom uniquement** — les paramètres de requête sont ignorés pour l'identité, donc
`direct:start` et `direct:start?x=1` correspondent au même endpoint. Un seul consumer par
endpoint est autorisé.

---

### Timer

Déclenchement périodique simple.

```go
builder.From("timer:tick?period=5s")
```

| Option        | Type     | Défaut | Description                          |
|---------------|----------|--------|--------------------------------------|
| `period`      | Duration | `1s`   | Période entre les déclenchements     |
| `repeatCount` | int      | `0`    | Nombre de répétitions (0=infini)     |
| `fixedRate`   | bool     | `false`| Mode à fréquence fixe vs délai fixe  |

---

## Composants de Transfert de Fichiers

### File

Opérations sur le système de fichiers local.

```go
// Consumer (lecture)
builder.From("file://input?delete=true")

// Producer (écriture)
builder.To("file://output")
```

| Option       | Type   | Défaut  | Description                       |
|--------------|--------|---------|-----------------------------------|
| `delete`     | bool   | `false` | Supprimer après traitement        |
| `noop`       | bool   | `false` | Ne pas déplacer/supprimer le fichier |
| `include`    | string | `""`    | Motif d'inclusion de fichiers     |
| `exclude`    | string | `""`    | Motif d'exclusion de fichiers     |
| `preMove`    | string | `""`    | Déplacer le fichier avant traitement |
| `move`       | string | `""`    | Déplacer le fichier après traitement |
| `moveFailed` | string | `""`    | Déplacer le fichier en cas d'échec |

---

### FTP / FTPS

Transfert de fichiers via le protocole FTP.

```go
// Consumer
builder.From("ftp://host:21/incoming?username=admin")

// Producer
builder.To("ftp://host:21/outgoing?binary=true")
```

**Variables d'environnement :**

- `FTP_USERNAME` - Nom d'utilisateur
- `FTP_PASSWORD` - Mot de passe

| Option        | Type   | Défaut | Description           |
|---------------|--------|--------|-----------------------|
| `username`    | string | `""`   | Nom d'utilisateur FTP |
| `password`    | string | `""`   | Mot de passe FTP      |
| `binary`      | bool   | `true` | Mode de transfert binaire |
| `passiveMode` | bool   | `true` | Utiliser le mode passif |

---

### SFTP

Transfert de fichiers sécurisé via SSH.

```go
builder.From("sftp://host:22/data?username=scott")
```

**Méthodes d'authentification :**

- Par mot de passe : via le paramètre `password` ou la variable d'environnement `SFTP_PASSWORD`
- Par clé : via le paramètre `privateKeyFile` ou `SFTP_PRIVATE_KEY_FILE`

| Option                 | Type   | Défaut | Description              |
|------------------------|--------|--------|--------------------------|
| `username`             | string | `""`   | Nom d'utilisateur SSH    |
| `password`             | string | `""`   | Mot de passe SSH         |
| `privateKeyFile`       | string | `""`   | Chemin vers la clé privée |
| `privateKeyPassphrase` | string | `""`   | Phrase secrète de la clé privée |

---

### SMB

Accès aux partages Windows/Samba.

```go
builder.From("smb://server/share/folder?username=user")
```

| Option     | Type   | Défaut    | Description           |
|------------|--------|-----------|-----------------------|
| `username` | string | `""`      | Nom d'utilisateur du domaine |
| `password` | string | `""`      | Mot de passe du domaine |
| `share`    | string | requis    | Nom du partage        |

---

## Composants Réseau

### HTTP

Support serveur et client HTTP.

```go
// Consumer (serveur HTTP)
builder.From("http://localhost:8080/api")

// Producer (client HTTP)
builder.To("http://example.com/webhook")

// Avec options
builder.To("http://api.example.com/data?httpMethod=POST")
```

| Option           | Type   | Défaut  | Description                    |
|------------------|--------|---------|--------------------------------|
| `httpMethod`     | string | `GET`   | Méthode HTTP pour le producer  |
| `bridgeEndpoint` | bool   | `false` | Bridge l'endpoint consumer     |

#### Authentification, TLS, limitation de débit (middleware consumer)

Le consumer HTTP est livré **sans authentification**. Ne l'exposez à un réseau non fiable
que derrière un middleware qui applique le modèle de sécurité dont vous avez besoin
(vérification de token, mTLS, OAuth, allowlist d'IP, rate limit, ...). Installez le
middleware sur le `HTTPComponent` avant le démarrage de la route :

```go
http := gocamel.NewHTTPComponent()
http.SetMiddleware(func(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.Header.Get("Authorization") != "Bearer "+os.Getenv("API_TOKEN") {
            http.Error(w, "non autorisé", http.StatusUnauthorized)
            return
        }
        next.ServeHTTP(w, r)
    })
})
ctx.AddComponent("http", http)
```

Le middleware enveloppe chaque handler installé par un consumer `http://` dans ce
contexte.

#### Sanitisation des headers sortants

Le producer HTTP (`To`) et le chemin de réponse du consumer rejettent les noms et
valeurs de header contenant `\r` ou `\n` (CRLF) pour empêcher le HTTP response
splitting (CWE-113). Un exchange qui tente de définir un tel header reçoit une
erreur de la part de `Send()`.

#### Durcissement & réponses

- Le serveur embarqué définit un `ReadHeaderTimeout` de 10 secondes pour limiter
  les attaques de type slowloris.
- Le corps de la réponse provient de `Out` (ou de `In` si aucun `Out` n'a été défini,
  selon la sémantique InOut de Camel). Les corps `[]byte` et `string` sont écrits
  tels quels ; tout autre corps non nil est écrit en utilisant sa représentation
  `%v`.

#### Arrêt

La goroutine du consumer est suivie par un `WaitGroup` interne et `Stop()`
attend que `http.Server.Shutdown` se termine (borné par un délai interne de
30 secondes) avant de retourner. `Stop()` est idempotent.

---

## Composants de Messagerie

### Telegram

Intégration de l'API Telegram Bot pour la réception et l'envoi de messages.

```go
// Requis : définir la variable d'environnement TELEGRAM_AUTHORIZATIONTOKEN
ctx.AddComponent("telegram", gocamel.NewTelegramComponent())

// Consumer (mode webhook/polling)
builder.From("telegram:bots").Log("${body}")

// Producer (envoi de messages)
builder.To("telegram:bots")
```

**Variables d'environnement :**

- `TELEGRAM_AUTHORIZATIONTOKEN` - Token de l'API Bot

| Option               | Type   | Défaut | Description        |
|----------------------|--------|--------|--------------------|
| `authorizationToken` | string | env var | Token de l'API Bot |

---

### NATS

Messagerie asynchrone orientée événements avec NATS. Publisher et subscriber haute performance avec le client Go NATS.

```go
ctx.AddComponent("nats", gocamel.NewNATSComponent())

// Consumer : subscription asynchrone réactive
builder.From("nats://orders?servers=nats://localhost:4222").
    ProcessFunc(func(e *gocamel.Exchange) error {
        // La charge utile NATS est reçue comme un slice d'octets
        payload := e.GetIn().GetBody().([]byte)
        fmt.Printf("Commande reçue : %s\n", string(payload))
        return nil
    })

// Producer : publier vers un sujet
builder.To("nats://orders?servers=nats://localhost:4222")

// Surcharger dynamiquement le sujet via un header
exchange.In.SetHeader(gocamel.CamelNATSSubject, "urgent-orders")
```

> [!WARNING]
> Lors de l'écriture des URIs, utilisez toujours des doubles barres obliques (ex. `nats://orders`) au lieu de `nats:orders`, sinon `url.Parse` traite l'URI comme une URI opaque, ce qui contamine la chaîne du sujet avec les paramètres de requête.

| Option   | Type   | Défaut                  | Description                                    |
|----------|--------|-------------------------|------------------------------------------------|
| `servers` | string | `nats://127.0.0.1:4222` | Adresses des serveurs NATS (séparées par virgules) |
| `subject` | string | parsé depuis le chemin  | Sujet NATS pour la subscription/publication    |

**Headers d'Exchange :**

- `CamelNATSSubject` - Sujet du message reçu ou sujet cible pour la publication.

---

### Redis

Envoi de commandes (SET, GET, DEL, PUBLISH) ou abonnement aux canaux Redis Pub/Sub.

```go
ctx.AddComponent("redis", gocamel.NewRedisComponent())

// Consumer : s'abonner à un canal Pub/Sub
builder.From("redis://localhost:6379?subscribe=true&channel=mychannel").
    Log("Message Pub/Sub reçu : ${body}")

// Producer : exécuter des commandes
builder.To("redis://localhost:6379?command=SET&key=mykey")
```

**Commandes supportées :**

- `SET` : Enregistre le corps de l'exchange comme valeur de `key`.
- `GET` : Récupère la valeur de `key` et l'écrit dans `Out.Body`.
- `DEL` : Supprime `key` et écrit le nombre de clés affectées dans `Out.Body`.
- `PUBLISH` : Publie le corps de l'exchange vers le canal/clé spécifié.

**Headers d'Exchange (surcharge des paramètres URI) :**

- `CamelRedisCommand` - La commande Redis à exécuter (ex. `"GET"`).
- `CamelRedisKey` - La clé Redis cible.
- `CamelRedisChannel` - Canal Pub/Sub cible pour la publication ou canal source pour l'abonnement.

| Option     | Type   | Défaut  | Description                          |
|------------|--------|---------|--------------------------------------|
| `command`  | string | `SET`   | Commande Redis pour le producer      |
| `key`      | string | `""`    | Clé Redis sur laquelle opérer        |
| `channel`  | string | `""`    | Canal Redis Pub/Sub                  |
| `subscribe`| bool   | `false` | Si true, agit comme un consumer Pub/Sub |

---

### Dépôt Idempotent Redis

Le Dépôt Idempotent Redis fournit une implémentation distribuée de l'interface `IdempotentRepository`, permettant à plusieurs instances GoCamel dans un cluster de se coordonner et d'éviter le traitement de messages dupliqués.

```go
import (
    "time"
    "github.com/redis/go-redis/v9"
)

rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
// Préfixe de clé "idempotent:" avec TTL d'expiration de 24 heures
repo := gocamel.NewRedisIdempotentRepository(rdb, "idempotent:", 24*time.Hour)

builder.From("direct:start").
    IdempotentConsumer("${header.MessageId}", repo).
    To("direct:process")
```

---

### Go Channel

Composant d'intégration intra-processus natif et ultra-rapide utilisant les canaux Go natifs. Il permet de découpler les segments de route de manière asynchrone au sein du même processus.

```go
ctx.AddComponent("chan", gocamel.NewChanComponent())

// Consumer : traite les exchanges de manière asynchrone depuis le canal
builder.From("chan:orders?bufferSize=500").
    To("direct:process-order")

// Producer : écrit une copie de l'exchange dans le canal de manière asynchrone
builder.To("chan:orders")
```

| Option       | Type | Défaut | Description                              |
|--------------|------|--------|------------------------------------------|
| `bufferSize` | int  | `100`  | Capacité du canal interne bufferisé      |

**Notes :**

- Les endpoints sont mis en cache **par nom de canal** : chaque producer et consumer
  utilisant le même nom partage un endpoint, et **un seul consumer** par canal est
  autorisé (un second `From("chan:orders")` échoue au démarrage).
- Les options URI sont appliquées lorsque l'endpoint est **créé pour la première
  fois** ; les références ultérieures avec des options différentes émettent un
  avertissement et réutilisent l'endpoint existant.

---

## Composants IA

### OpenAI

Intégration de l'API OpenAI pour ChatGPT/GPT-4.

```go
ctx.AddComponent("openai", gocamel.NewOpenAIComponent())

// Producer uniquement - envoyer une requête de chat completion
endpoint, _ := ctx.CreateEndpoint("openai:chat?model=gpt-4")
producer, _ := endpoint.CreateProducer()

exchange := gocamel.NewExchange(context.Background())
exchange.GetIn().SetBody("Bonjour, comment allez-vous ?")
producer.Send(exchange)

fmt.Println(exchange.GetOut().GetBody()) // Réponse de l'IA
```

**Variables d'environnement :**

- `OPENAI_AUTHORIZATIONTOKEN` ou `OPENAI_API_KEY` - Clé API

| Option               | Type   | Défaut          | Description     |
|----------------------|--------|-----------------|-----------------|
| `model`              | string | `gpt-3.5-turbo` | Modèle à utiliser |
| `authorizationToken` | string | env var         | Clé API         |

---

## Composants d'Ordonnancement

### Cron

Ordonnancement avancé avec expressions cron ou intervalles simples.

```go
ctx.AddComponent("cron", gocamel.NewCronComponent())

// Déclencheur cron (6 champs, incluant les secondes)
builder.From("cron://group/job?cron=0+*+*+*+*+*")

// Déclencheur à intervalle simple
builder.From("cron://poller?trigger.repeatInterval=5000")
```

**Format d'expression cron (6 champs) :**

```
second minute hour day month dayOfWeek
0 * * * * *  # Chaque minute
0 */5 * * * * # Toutes les 5 minutes
cron=0 0 12 * * *  # Chaque jour à midi
```

| Option                   | Type   | Défaut | Description                          |
|--------------------------|--------|--------|--------------------------------------|
| `cron`                   | string | `""`   | Expression cron à 6 champs           |
| `trigger.repeatInterval` | int    | `""`   | Intervalle en ms (déclencheur simple) |
| `trigger.repeatCount`    | int    | `-1`   | Nombre maximum de répétitions        |
| `triggerStartDelay`      | int    | `500`  | Délai initial en ms                  |
| `stateful`               | bool   | `false`| Empêcher l'exécution concurrente     |

**Headers d'Exchange :**

- `fireTime` - Heure d'exécution du déclencheur
- `nextFireTime` - Prochaine heure planifiée
- `triggerName` - Identifiant du déclencheur

---

## Composants de Messagerie Électronique

### SMTP/SMTPS (Envoi)

Envoi d'emails via SMTP.

```go
builder.To("smtps://smtp.gmail.com:465?to=recipient@example.com&subject=Bonjour")
```

| Option        | Type   | Défaut         | Description    |
|---------------|--------|----------------|----------------|
| `username`    | string | `""`           | Nom d'utilisateur SMTP |
| `password`    | string | `""`           | Mot de passe SMTP |
| `to`          | string | `""`           | Destinataire(s) |
| `subject`     | string | `""`           | Sujet de l'email |
| `contentType` | string | `"text/plain"` | Type MIME      |

---

### IMAP/IMAPS (Réception)

Réception d'emails via IMAP avec support IDLE.

```go
builder.From("imaps://imap.gmail.com:993?folderName=INBOX&idle=true")
```

| Option       | Type   | Défaut   | Description               |
|--------------|--------|----------|---------------------------|
| `username`   | string | `""`     | Nom d'utilisateur IMAP    |
| `password`   | string | `""`     | Mot de passe IMAP         |
| `folderName` | string | `"INBOX"`| Dossier à surveiller      |
| `unseen`     | bool   | `true`   | Messages non lus uniquement |
| `idle`       | bool   | `false`  | Utiliser le mode IMAP IDLE |
| `delete`     | bool   | `false`  | Supprimer après traitement |
| `fetchSize`  | int    | `-1`     | Messages par interrogation |
| `pollDelay`  | int    | `60000`  | Intervalle d'interrogation (ms) |

---

### POP3/POP3S (Réception)

Réception d'emails via POP3.

```go
builder.From("pop3s://pop.gmail.com:995?username=user&password=pass")
```

---

## Composants de Base de Données

### SQL

Exécution de requêtes SQL via `database/sql`.

```go
import (
    "database/sql"
    _ "github.com/mattn/go-sqlite3"
)

db, _ := sql.Open("sqlite3", "./app.db")

sqlComp := gocamel.NewSQLComponent()
sqlComp.RegisterDataSource("appdb", db)
ctx.AddComponent("sql", sqlComp)

// SELECT -> Out.Body = []map[string]any
builder.From("direct:list").
    To("sql://appdb?query=SELECT+id,name+FROM+users").
    Log("${body}")

// SELECT une seule ligne -> Out.Body = map[string]any
builder.From("direct:one").
    SetHeader(gocamel.SqlParameters, []any{42}).
    To("sql://appdb?query=SELECT+*+FROM+users+WHERE+id=?&outputType=SelectOne")

// INSERT/UPDATE/DELETE -> Out.Body = lignes affectées (int64)
builder.From("direct:insert").
    SetHeader(gocamel.SqlParameters, []any{"alice", "alice@example.com"}).
    To("sql://appdb?query=INSERT+INTO+users(name,email)+VALUES(?,?)")
```

**Format d'URI :**

```
sql://<datasourceName>?query=<SQL>
sql://logical?dataSourceRef=<datasourceName>&query=<SQL>
```

| Option          | Type   | Défaut       | Description                   |
|-----------------|--------|--------------|-------------------------------|
| `query`         | string | requis       | Chaîne de requête SQL         |
| `dataSourceRef` | string | chemin hôte  | Nom de la datasource          |
| `outputType`    | string | `SelectList` | `SelectList` ou `SelectOne`   |
| `batch`         | bool   | `false`      | Mode d'exécution par lot      |
| `transacted`    | bool   | `false`      | Encapsuler dans une transaction |

**Paramètres de requête :**

Fournis via le header `CamelSqlParameters` ou le corps comme `[]any`.

**Headers de sortie :**

- `CamelSqlRowCount` - Lignes retournées/affectées
- `CamelSqlColumnNames` - Noms des colonnes (SELECT)

**Corps du résultat :**

| Cas                     | Type de Out.Body           |
|-------------------------|----------------------------|
| `SELECT` + `SelectList` | `[]map[string]any`         |
| `SELECT` + `SelectOne`  | `map[string]any` ou `nil`  |
| `INSERT/UPDATE/DELETE`  | `int64` (lignes affectées) |

---

### SQL-Stored

Exécution de procédures stockées avec support des paramètres IN, OUT et INOUT.

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

**Format d'URI :**

```
sql-stored://datasourceName?procedure=NAME
sql-stored://logical?dataSourceRef=dsName&procedure=NAME
```

| Option          | Type   | Défaut       | Description                   |
|-----------------|--------|--------------|-------------------------------|
| `procedure`     | string | requis       | Nom de la procédure stockée   |
| `dataSourceRef` | string | chemin hôte  | Nom de la datasource          |
| `outputType`    | string | `SelectList` | `SelectList` ou `SelectOne`   |
| `transacted`    | bool   | `false`      | Exécuter dans une transaction |
| `noop`          | bool   | `false`      | Mode test (pas d'exécution)   |

**Directions des paramètres :**

| Direction             | Description           |
|-----------------------|-----------------------|
| `ParamDirectionIn`    | Entrée uniquement     |
| `ParamDirectionOut`   | Sortie uniquement     |
| `ParamDirectionInOut` | Entrée et sortie      |

Exemple :

```go
params := []gocamel.StoredProcedureParam{
    {Name: "inParam", Direction: gocamel.ParamDirectionIn, Value: "input"},
    {Name: "outParam", Direction: gocamel.ParamDirectionOut},
    {Name: "inOutParam", Direction: gocamel.ParamDirectionInOut, Value: 123},
}
```

---

### MongoDB

Intégration MongoDB pour les opérations CRUD. Producer uniquement.

#### Format d'URI

```
mongodb://connectionName?database=mydb&collection=mycoll&operation=find
```

#### Options

| Option          | Type   | Requis | Description                                                              |
|-----------------|--------|--------|--------------------------------------------------------------------------|
| `database`      | string | Oui    | Nom de la base de données                                                |
| `collection`    | string | Oui    | Nom de la collection                                                     |
| `operation`     | string | Oui    | Opération : find, findOne, insert, insertOne, save, update, remove, count |
| `connectionRef` | string | Non    | Référence de connexion enregistrée                                      |
| `allowDeleteAll` | bool   | Non    | Autorise `remove` avec un filtre vide (défaut : `false`)                 |

Par sécurité, `remove` refuse les filtres nuls ou vides. Définissez
`allowDeleteAll=true` explicitement uniquement si la suppression de toute la
collection est intentionnelle.

#### Headers d'entrée

| Header                   | Mode  | Description                                |
|--------------------------|-------|--------------------------------------------|
| `CamelMongoDbDatabase`   | R/W   | Nom de la base de données                  |
| `CamelMongoDbCollection` | R/W   | Nom de la collection                       |
| `CamelMongoDbOperation`  | R/W   | Opération à exécuter                       |
| `CamelMongoDbCriteria`   | Écriture | Filtre/critère (map[string]any ou JSON) |
| `CamelMongoDbLimit`      | Écriture | Limite de résultats                        |
| `CamelMongoDbSkip`       | Écriture | Sauter N documents                         |
| `CamelMongoDbSort`       | Écriture | Ordre de tri (json: {"field": 1})          |

#### Headers de sortie

| Header                    | Description                       |
|---------------------------|-----------------------------------|
| `CamelMongoDbResultTotal` | Total des documents trouvés/affectés |
| `CamelMongoDbOid`         | ObjectID du document inséré       |

#### Exemple

```go
import (
    "context"
    "go.mongodb.org/mongo-driver/mongo"
    "go.mongodb.org/mongo-driver/mongo/options"
)

// Créer le composant
mongoComp := gocamel.NewMongoDBComponent()

// Se connecter
client, _ := mongo.Connect(context.Background(), options.Client().ApplyURI("mongodb://localhost:27017"))
conn := gocamel.CreateMongoDBConnection(client, "mongodb://localhost:27017", "mydb")
mongoComp.RegisterConnection("myconn", conn)

builder.WithComponent("mongodb", mongoComp)

// Insertion
builder.From("timer:tick?period=5s").
    SetBody(map[string]any{"name": "test", "value": 123}).
    To("mongodb://myconn?database=mydb&collection=items&operation=insert")

// Requête avec filtre
builder.From("direct:search").
    SetHeader(gocamel.CamelMongoDbCriteria, map[string]any{"status": "active"}).
    SetHeader(gocamel.CamelMongoDbLimit, 10).
    To("mongodb://myconn?database=mydb&collection=items&operation=find")
```

---

## Composants de Transformation

### XSLT

Transformation XML via feuille de style XSL.

```go
builder.To("xslt:file://transform.xsl")
```

| Option               | Type   | Défaut | Description                    |
|----------------------|--------|--------|--------------------------------|
| `transformerFactory` | string | `""`   | Classe de transformateur personnalisée |

---

### XSD

Validation de schéma XML.

```go
builder.To("xsd:file://schema.xsd")
```

| Option           | Type   | Défaut   | Description            |
|------------------|--------|----------|------------------------|
| `schemaResource` | string | requis   | Chemin du schéma XSD   |

---

### Template

Traitement de templates Go (inspiré d'Apache Camel Velocity).

```go
// Template de base
builder.To("template:templates/email.tmpl")

// Avec cache
builder.To("template:templates/item.tmpl?contentCache=true")

// Template dynamique depuis un header
builder.To("template:default.tmpl?allowTemplateFromHeader=true")
```

| Option                    | Type   | Défaut  | Description                                    |
|---------------------------|--------|---------|------------------------------------------------|
| `contentCache`            | bool   | `false` | Mettre en cache le template en mémoire         |
| `allowTemplateFromHeader` | bool   | `false` | Autoriser la surcharge via le header `CamelTemplatePath` |
| `startDelimiter`          | string | `{{`    | Délimiteur de début                            |
| `endDelimiter`            | string | `}}`    | Délimiteur de fin                              |

**Variables de template :**

```
{{.Body}}              # Corps du message
{{.Headers.name}}      # Valeur de header
{{.Exchange.ID}}       # ID de l'Exchange
{{.Exchange.Created}}  # Horodatage de création
```

**Fonctions de template :**

```go
{{.Body | upper}}
{{.Body | lower}}
{{.Body | trim}}
{{now | formatDate "2006-01-02 15:04:05"}}
{{"hello" | contains "ell"}}
```

---

## Composants d'Exécution

### Exec

Exécuter des commandes système.

```go
builder.To("exec:ls -la")
```

| Option                | Type   | Défaut  | Description                                          |
|-----------------------|--------|---------|------------------------------------------------------|
| `args`                | string | `""`    | Arguments de la commande                             |
| `workingDir`          | string | `""`    | Répertoire de travail                                |
| `timeout`             | int    | `0`     | Délai d'attente en ms (0=pas de délai)               |
| `outFile`             | string | `""`    | Lire le résultat depuis ce fichier au lieu de stdout |
| `useStderrOnEmpty`    | bool   | `false` | Utiliser stderr comme corps quand stdout est vide    |
| `allowHeaderOverride` | bool   | `false` | Autoriser les headers `CamelExecCommand*` à surcharger l'URI |

**Surcharges par message (opt-in) :**

Quand `allowHeaderOverride=true`, ces headers surchargent les paramètres URI pour un seul message :

| Header                       | Surcharge         |
|------------------------------|-------------------|
| `CamelExecCommandExecutable` | Exécutable        |
| `CamelExecCommandArgs`       | Arguments         |
| `CamelExecCommandWorkingDir` | Répertoire de travail |
| `CamelExecCommandTimeout`    | Délai d'attente (ms) |

!!! danger "Sécurité"
    Les surcharges de header permettent à un message de choisir **quel binaire est exécuté**. Elles sont
    désactivées par défaut ; n'activez `allowHeaderOverride=true` que lorsque les headers de message
    ne peuvent pas être influencés par une entrée non fiable (ex. headers provenant d'un consumer
    HTTP ou mail). Les exécutables, arguments et répertoires de travail sont toujours validés
    contre les métacaractères shell et le path traversal.

**Sortie :**

- `Out.Body` = stdout de la commande
- Header `CamelExecExitValue` = code de sortie
- Headers `CamelExecStdout` / `CamelExecStderr` = sortie brute de la commande

---

## Configuration des Composants

### Authentification

Les identifiants peuvent être fournis via des variables d'environnement :

```go
// FTP avec variable d'environnement
builder.From("ftp://host?username=${env:FTP_USER}")

// Ou paramètre
builder.From("ftp://host?username=admin&password=${env:FTP_PASS}")
```

### Options communes

De nombreux composants partagent des options d'interrogation communes :

```go
// Interrogation de fichiers
builder.From("file://data?delay=10s&delete=true")

// Interrogation FTP
builder.From("ftp://host/incoming?delay=30s&include=*.xml")
```

| Option    | Type     | Défaut | Description           |
|-----------|----------|--------|-----------------------|
| `delay`   | Duration | varie  | Intervalle d'interrogation |
| `include` | string   | `""`   | Motif d'inclusion     |
| `exclude` | string   | `""`   | Motif d'exclusion     |

---

## Tableau Récapitulatif des Composants

| Composant  | Catégorie    | Consumer | Producer | Motif d'URI              |
|------------|--------------|----------|----------|--------------------------|
| Direct     | Core         | ✅        | ✅        | `direct:name`            |
| Timer      | Core         | ✅        | ❌        | `timer:name`             |
| File       | Fichier      | ✅        | ✅        | `file://path`            |
| FTP        | Fichier      | ✅        | ✅        | `ftp://host/path`        |
| SFTP       | Fichier      | ✅        | ✅        | `sftp://host/path`       |
| SMB        | Fichier      | ✅        | ✅        | `smb://host/share`       |
| HTTP       | Réseau       | ✅        | ✅        | `http://host:port/path`  |
| Telegram   | Messagerie   | ✅        | ✅        | `telegram:bots`          |
| OpenAI     | IA           | ❌        | ✅        | `openai:chat`            |
| Cron       | Ordonnancement | ✅      | ❌        | `cron://group/job`       |
| SMTP       | Mail         | ❌        | ✅        | `smtp://host:port`       |
| IMAP       | Mail         | ✅        | ❌        | `imap://host:port`       |
| POP3       | Mail         | ✅        | ❌        | `pop3://host:port`       |
| SQL        | Base de données | ❌     | ✅        | `sql://datasource`       |
| SQL-Stored | Base de données | ❌     | ✅        | `sql-stored://datasource`|
| XSLT       | Transformation | ❌     | ✅        | `xslt:template`          |
| XSD        | Transformation | ❌     | ✅        | `xsd:schema`             |
| Template   | Transformation | ❌     | ✅        | `template:template`      |
| Exec       | Exécution    | ❌        | ✅        | `exec:command`           |
| NATS       | Messagerie   | ✅        | ✅        | `nats://subject`         |
| Redis      | Messagerie   | ✅        | ✅        | `redis://host:port`      |
| Go Channel | Core         | ✅        | ✅        | `chan:channelName`       |
