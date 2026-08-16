+++
title = "Référence des Composants"
weight = 12
+++
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

| Option                  | Type     | Défaut     | Description                                                                           |
|-------------------------|----------|------------|---------------------------------------------------------------------------------------|
| `delete`                | bool     | `false`    | Supprimer après traitement                                                            |
| `noop`                  | bool     | `false`    | Ne pas déplacer/supprimer le fichier                                                  |
| `include`               | string   | `""`       | Motif d'inclusion de fichiers                                                         |
| `exclude`               | string   | `""`       | Motif d'exclusion de fichiers                                                         |
| `preMove`               | string   | `""`       | Déplacer le fichier avant traitement                                                  |
| `move`                  | string   | `""`       | Déplacer le fichier après traitement                                                  |
| `moveFailed`            | string   | `""`       | Déplacer le fichier en cas d'échec                                                    |
| `readLock`              | string   | `changed`  | Stratégie de verrouillage (`none`, `changed`, `rename`, `markerFile`)                 |
| `readLockTimeout`       | Duration | `10s`      | Délai maximal d'attente pour acquérir le verrou                                       |
| `readLockCheckInterval` | Duration | `100ms`    | Intervalle entre les vérifications du verrou                                          |
| `readLockMinLength`     | int64    | `0`        | Taille minimale du fichier en octets                                                  |
| `readLockMinAge`        | Duration | `0`        | Âge minimal du fichier                                                                |
| `fileExist`             | string   | `Override` | Comportement si le fichier destination existe (`Override`, `Append`, `Fail`, `Ignore`) |

#### En-têtes

##### Définis par le Consumer
- `CamelFileName` : Nom relatif du fichier
- `CamelFilePath` : Chemin absolu ou complet du fichier
- `CamelFileLength` : Taille du fichier en octets
- `CamelFileLastModified` : Date et heure de dernière modification (`time.Time`)

##### Consommés par le Producer
- `CamelFileName` : Surcharge le nom du fichier lorsque l'URI cible pointe vers un répertoire

#### Verrou de lecture (Read Lock)

GoCamel fournit plusieurs stratégies de verrou de lecture pour s'assurer que les fichiers sont complètement écrits et exclusifs avant traitement :

- `readLock=changed` : Le consumer échantillonne la taille et la date de modification sur `readLockCheckInterval` et lit dès que deux échantillons consécutifs coïncident dans la limite de `readLockTimeout`.
- `readLock=rename` : Renomme temporairement le fichier sous un nom exclusif (`.camelExclusiveReadLock`) pendant le traitement.
- `readLock=markerFile` : Crée un fichier marqueur `.camelLock` pendant le traitement et le supprime une fois terminé.
- `readLock=none` : Désactive toute vérification de verrou.

> [!tip] Préférez un passage de relais atomique
> Le verrou de lecture `changed` est une heuristique : un écrivain qui s'interrompt plus
> longtemps que l'intervalle d'échantillonnage en cours d'écriture peut encore
> être observé comme stable. Pour un passage de relais garanti, faites écrire
> le producteur sous un nom temporaire puis effectuez un `rename` vers le
> répertoire surveillé — le rename est atomique.


---

### FTP / FTPS

Transfert de fichiers via le protocole FTP.

```go
// Consumer
builder.From("ftp://host:21/incoming?username=admin&readLock=changed")

// Producer
builder.To("ftp://host:21/outgoing?binary=true")
```

**Variables d'environnement :**

- `FTP_USERNAME` - Nom d'utilisateur
- `FTP_PASSWORD` - Mot de passe

| Option                  | Type     | Défaut | Description                                                        |
|-------------------------|----------|--------|--------------------------------------------------------------------|
| `username`              | string   | `""`   | Nom d'utilisateur FTP                                              |
| `password`              | string   | `""`   | Mot de passe FTP                                                   |
| `binary`                | bool     | `true` | Mode de transfert binaire                                          |
| `passiveMode`           | bool     | `true` | Utiliser le mode passif                                            |
| `maxMessageSize`        | int      | `0`    | Taille max d'un fichier pollé en octets (0 = illimité)             |
| `readLock`              | string   | `none` | Stratégie de verrou (`none`, `changed`, `rename`, `markerFile`)    |
| `readLockTimeout`       | Duration | `10s`  | Délai maximal d'attente pour acquérir le verrou                    |
| `readLockCheckInterval` | Duration | `1s`   | Intervalle entre les vérifications du verrou                       |
| `readLockMinLength`     | int64    | `0`    | Taille minimale du fichier en octets                               |
| `readLockMinAge`        | Duration | `0`    | Âge minimal du fichier                                             |

---

### SFTP

Transfert de fichiers sécurisé via SSH.

```go
builder.From("sftp://host:22/data?username=scott&readLock=changed&readLockTimeout=15s")
```

**Méthodes d'authentification :**

- Par mot de passe : via le paramètre `password` ou la variable d'environnement `SFTP_PASSWORD`
- Par clé : via le paramètre `privateKeyFile` ou `SFTP_PRIVATE_KEY_FILE`

| Option                  | Type     | Défaut | Description                                                        |
|-------------------------|----------|--------|--------------------------------------------------------------------|
| `username`              | string   | `""`   | Nom d'utilisateur SSH                                              |
| `password`              | string   | `""`   | Mot de passe SSH                                                   |
| `privateKeyFile`        | string   | `""`   | Chemin vers la clé privée                                          |
| `privateKeyPassphrase`  | string   | `""`   | Phrase secrète de la clé privée                                    |
| `maxMessageSize`        | int      | `0`    | Taille max d'un fichier pollé en octets (0 = illimité)             |
| `readLock`              | string   | `none` | Stratégie de verrou (`none`, `changed`, `rename`, `markerFile`)    |
| `readLockTimeout`       | Duration | `10s`  | Délai maximal d'attente pour acquérir le verrou                    |
| `readLockCheckInterval` | Duration | `1s`   | Intervalle entre les vérifications du verrou                       |
| `readLockMinLength`     | int64    | `0`    | Taille minimale du fichier en octets                               |
| `readLockMinAge`        | Duration | `0`    | Âge minimal du fichier                                             |

---

### SMB

Accès aux partages Windows/Samba.

```go
builder.From("smb://server/share/folder?username=user&readLock=changed")
```

| Option                  | Type     | Défaut    | Description                                                        |
|-------------------------|----------|-----------|--------------------------------------------------------------------|
| `username`              | string   | `""`      | Nom d'utilisateur du domaine                                       |
| `password`              | string   | `""`      | Mot de passe du domaine                                            |
| `share`                 | string   | requis    | Nom du partage                                                     |
| `maxMessageSize`        | int      | `0`       | Taille max d'un fichier pollé en octets (0 = illimité)             |
| `readLock`              | string   | `none`    | Stratégie de verrou (`none`, `changed`, `rename`, `markerFile`)    |
| `readLockTimeout`       | Duration | `10s`     | Délai maximal d'attente pour acquérir le verrou                    |
| `readLockCheckInterval` | Duration | `1s`      | Intervalle entre les vérifications du verrou                       |
| `readLockMinLength`     | int64    | `0`       | Taille minimale du fichier en octets                               |
| `readLockMinAge`        | Duration | `0`       | Âge minimal du fichier                                             |

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

#### Headers de réponse

Le corps de la réponse provient de `Out` (ou de `In` si aucun `Out` n'a été
défini, selon la sémantique InOut de Camel). Les corps `[]byte` et `string`
sont écrits tels quels ; tout autre corps non nil est écrit en utilisant sa
représentation `%v`.

Comme le consumer copie tous les headers de la requête entrante sur l'exchange,
une route qui ne définit pas `Out` répondrait avec les headers de la requête
elle-même. Deux filtres l'en empêchent :

- **Les headers entrants inchangés ne sont pas renvoyés.** Un header qui
  revient identique à celui reçu est écarté : `Cookie` et `Authorization` ne
  sont donc jamais réfléchis vers le client. Un header posé ou modifié par la
  route est toujours émis.
- **Les headers hop-by-hop et de cadrage ne sont jamais émis** (`Connection`,
  `Keep-Alive`, `Transfer-Encoding`, `Content-Length`, `Host`, ...). Renvoyer
  le `Content-Length` de la requête corrompait le cadrage de la réponse.

Le même filtre s'applique au producer : un pont `From(http://...)` →
`To(http://...)` ne transmet donc pas le `Content-Length` ni le `Host` entrants
au service amont.

Le producer transmet par défaut tous les autres headers entrants, y compris
`Cookie` et `Authorization` — le comportement normal d'un client HTTP générique
qui appelle un service amont authentifié. Dans un scénario de pont/proxy où cela
fuirait les identifiants d'un client vers un amont tiers, supprimez-les
explicitement :

```go
http := gocamel.NewHTTPComponent()
http.SetSkipAuthorizationHeaders(true)
```

Une fois activée, `Authorization` et `Cookie` sont retirés de toute requête
sortante produite par ce composant.

#### Statut de la réponse

Une route fixe le code de statut via le header `CamelHttpResponseCode` sur le
message de réponse (l'ancien header `Status-Code` reste accepté). Les valeurs
hors de 100–599 sont ignorées :

```go
builder.From("http://localhost:8080/api").
    ProcessFunc(func(e *gocamel.Exchange) error {
        e.GetOut().SetHeader(gocamel.CamelHttpResponseCode, 201)
        e.GetOut().SetHeader("Content-Type", "application/json")
        e.GetOut().SetBody(`{"created":true}`)
        return nil
    })
```

#### Gestion des erreurs du producer

Une réponse amont de statut **≥ 300 est une erreur** (comportement par défaut
d'Apache Camel). L'erreur enveloppe `gocamel.ErrHTTPStatus`, et le corps ainsi
que les headers de la réponse restent publiés sur `Out` pour inspection :

```go
if errors.Is(err, gocamel.ErrHTTPStatus) {
    code, _ := exchange.GetOut().GetHeader(gocamel.CamelHttpResponseCode)
    // code est un int ; le corps contient la charge utile d'erreur amont
}
```

Pour considérer toute réponse comme un succès et traiter le statut soi-même :

```go
http := gocamel.NewHTTPComponent()
http.SetThrowExceptionOnFailure(false)
```

Le producer renseigne également `CamelHttpResponseCode` (int) et
`CamelHttpResponseText` (ex. `"500 Internal Server Error"`) sur `Out`.

#### Durcissement

- Le serveur embarqué définit un `ReadHeaderTimeout` de 10 secondes (attaques
  d'en-têtes lents), un `ReadTimeout` de 60 secondes (attaques de corps lents)
  et un `IdleTimeout` de 60 secondes (connexions keep-alive inactives).
- Les corps de requête et de réponse sont lus au travers d'une limite de taille
  — `DefaultMaxBodySize` (32 Mio) — afin qu'une charge utile surdimensionnée ne
  puisse pas épuiser la mémoire du processus (CWE-400). Une requête trop
  volumineuse reçoit un `413 Request Entity Too Large`. Ajustez ou désactivez
  la limite par composant :

    ```go
    http := gocamel.NewHTTPComponent()
    http.SetMaxBodySize(256 << 20) // 256 Mio ; <= 0 désactive la limite
    ```

- Les erreurs de route ne sont **pas** renvoyées au client : le handler répond
  par un `500 internal server error` générique et journalise le détail, qui
  peut contenir du texte SQL, des chemins de fichiers ou des messages de
  driver.

#### Arrêt

La goroutine du consumer est suivie par un `WaitGroup` interne et `Stop()`
attend que `http.Server.Shutdown` se termine (borné par un délai interne de
30 secondes) avant de retourner. `Stop()` est idempotent.

---

### Net (TCP/UDP)

Sockets TCP et UDP bruts, construits sur la bibliothèque standard — l'équivalent
Go des composants `netty`/`mina` d'Apache Camel. Le consumer écoute et alimente
la route avec les messages entrants ; le producer se connecte et envoie le
corps du message.

```go
// Consumer (serveur) : requête-réponse sur TCP
builder.From("net:tcp://localhost:9090")

// Consumer (serveur) : UDP sans accusé de réception
builder.From("net:udp://localhost:9090?sync=false")

// Producer (client)
builder.To("net:tcp://localhost:9090")
```

| Option       | Type   | Défaut  | Description                                      |
|--------------|--------|---------|--------------------------------------------------|
| `sync`       | bool   | `true`  | Requête-réponse : le consumer renvoie la réponse de la route au pair ; le producer attend une réponse |
| `textline`   | bool   | `true`  | Messages délimités par saut de ligne (TCP uniquement). `false` = un message par lecture |
| `bufferSize` | int    | `8192`  | Taille maximale d'un message/datagramme en octets |
| `timeout`    | int    | `30000` | Délai d'attente de connexion/réponse du producer en ms (0 = aucun) |
| `keepAlive`  | bool   | `false` | Keepalive TCP sur les connexions                 |
| `readTimeout`| int    | `60000` | Délai de lecture du consumer en ms (0 = aucun)    |

**Formes d'URI** — `net:tcp://hôte:port` et `net:tcp:hôte:port` sont acceptées ;
idem pour `udp`. Un hôte omis prend la valeur `localhost`. Le port `0` demande
un port éphémère à l'OS : lisez l'adresse réellement liée via
`NetConsumer.Address()`.

**Cadrage.** TCP est un flux d'octets : le composant a donc besoin d'une limite
de message. Avec `textline=true` (défaut), chaque ligne terminée par un saut
de ligne est un message (le `\r\n` ou `\n` final est retiré). Avec
`textline=false`, chaque lecture est un message. Les datagrammes UDP n'ont
pas besoin de cadrage : chaque datagramme est un message.

```go
// Serveur écho requête-réponse
builder.From("net:tcp://localhost:9090").
    ProcessFunc(func(e *gocamel.Exchange) error {
        body, _ := e.GetIn().GetBody().([]byte)
        e.GetOut().SetBody([]byte("echo: " + string(body)))
        return nil
    })
```

**Headers.** Le consumer définit `CamelNetRemoteAddress` et
`CamelNetLocalAddress` (hôte:port) sur le message In.

#### Durcissement

- Les lectures de messages sont bornées par `bufferSize`. Un message textline
  plus long que la limite est une erreur de protocole : la connexion est
  fermée plutôt que de mettre en tampon des données illimitées (CWE-400).
  Les datagrammes UDP plus grands que la limite sont tronqués.
- La lecture de la réponse du producer en mode sync est bornée par
  `bufferSize` et par le délai `timeout`, afin qu'un pair muet ne puisse pas
  bloquer la route indéfiniment.
- Le consumer fixe un `readTimeout` (défaut 60 s) sur chaque connexion afin
  qu'un client de type slowloris ne puisse pas monopoliser une goroutine
  indéfiniment ; mettez-le à `0` pour le désactiver.
- Les paniques dans les processeurs de route sont contenues
  (`ProcessSafely`) : une route défectueuse échoue son exchange, jamais le
  consumer.

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

### Kafka

Diffusion d'événements distribuée à haut débit avec Apache Kafka via `segmentio/kafka-go` (pur Go).

```go
import "gitlab.com/tranchida/gocamel/components/kafka"

ctx.AddComponent("kafka", kafka.NewKafkaComponent())

// Consommateur avec consumer group
builder.From("kafka:orders?brokers=localhost:9092&groupId=billing-service").
    Log("Message reçu : ${body}")

// Producteur avec partition key dynamique (nécessite allowHeaderOverride=true)
builder.From("direct:publish").
    SetHeader("CamelKafkaPartitionKey", "key-123").
    To("kafka:orders?brokers=localhost:9092&allowHeaderOverride=true")
```

| Option | Type | Défaut | Description |
|---|---|---|---|
| `brokers` | string | `localhost:9092` | Adresses des brokers Kafka séparées par des virgules |
| `groupId` | string | `""` | Identifiant du consumer group Kafka |
| `partition` | int | `-1` | Partition cible (hors consumer group) |
| `clientId` | string | `""` | Identifiant client |
| `allowHeaderOverride` | bool | `false` | Autorise `CamelKafkaTopic`, `CamelKafkaPartitionKey` et `CamelKafkaKey` à remplacer l'URI en production |
| `forwardHeaders` | string | `""` | Liste de noms de headers (séparés par des virgules) propagés comme headers de record Kafka ; si définie, SEULS ces headers sont propagés |

**Headers d'Exchange :**
- `CamelKafkaTopic` - Surcharge (avec `allowHeaderOverride=true`) ou lecture du topic cible.
- `CamelKafkaPartitionKey` / `CamelKafkaKey` - Clé de partitionnement Kafka (honorée avec `allowHeaderOverride=true`).
- `CamelKafkaPartition` - Index de la partition.
- `CamelKafkaOffset` - Offset du message.
- `CamelKafkaTimestamp` - Horodatage du message.

> ⚠️ Sécurité : le topic et la clé configurés dans l'URI constituent une
> configuration de confiance. Les surcharges par headers sont ignorées sauf si
> l'endpoint active explicitement `allowHeaderOverride=true`. Ne l'activez que
> si les headers ne peuvent pas être influencés par une entrée non fiable.
>
> Les headers sortants de l'exchange sont filtrés avant d'atteindre le broker :
> `Authorization`, `Cookie`, `Proxy-Authorization`, `Set-Cookie` et
> `WWW-Authenticate` (insensible à la casse) ne sont jamais propagés, afin
> qu'un producteur en aval du consommateur HTTP ne fuite pas les identifiants
> du client. Définissez `forwardHeaders` pour une liste explicite si vous
> souhaitez un contrôle plus strict.

---

### gRPC

Intégration client et serveur RPC haute performance via `google.golang.org/grpc`.

```go
import "gitlab.com/tranchida/gocamel/components/grpc"

ctx.AddComponent("grpc", grpc.NewGRPCComponent())

// Serveur Consommateur : route les appels RPC entrants dans GoCamel
builder.From("grpc://0.0.0.0:50051/helloworld.Greeter/SayHello").
    SetBody("Réponse depuis le serveur gRPC GoCamel")

// Client Producteur : invoque des méthodes RPC distantes
builder.From("direct:invoke").
    To("grpc://localhost:50051/helloworld.Greeter/SayHello?insecure=true")
```

| Option | Type | Défaut | Description |
|---|---|---|---|
| `service` | string | parsé depuis le chemin | Nom complet du service gRPC |
| `method` | string | parsé depuis le chemin | Nom de la méthode RPC |
| `insecure` | bool | `true` | Utiliser un transport texte en clair sans TLS ; à `false`, TLS 1.2+ est utilisé avec le pool de CA système |
| `caCert` | string | `""` | Chemin d'un certificat CA au format PEM approuvé pour la vérification du serveur quand `insecure=false` (ex. serveur auto-signé) |
| `forwardHeaders` | string | `""` | Liste de noms de headers (séparés par des virgules) propagés comme métadonnées RPC sortantes ; si définie, SEULS ces headers sont propagés |

**Headers d'Exchange :**
- `CamelGrpcMethod` - Méthode gRPC invoquée.
- `CamelGrpcStatusCode` - Code d'état gRPC (ex: 0 pour OK).
- `CamelGrpcStatusDescription` - Message descriptif du statut gRPC.

> ⚠️ Sécurité : les headers sortants de l'exchange sont filtrés avant d'être
> envoyés comme métadonnées RPC : `Authorization`, `Cookie`,
> `Proxy-Authorization`, `Set-Cookie` et `WWW-Authenticate` (insensible à la
> casse) ne sont jamais propagés, afin qu'un producteur en aval du
> consommateur HTTP ne fuite pas les identifiants du client. Définissez
> `forwardHeaders` pour une liste explicite si vous souhaitez un contrôle plus
> strict.

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

| Option           | Type   | Défaut   | Description               |
|------------------|--------|----------|---------------------------|
| `username`       | string | `""`     | Nom d'utilisateur IMAP    |
| `password`       | string | `""`     | Mot de passe IMAP         |
| `folderName`     | string | `"INBOX"`| Dossier à surveiller      |
| `unseen`         | bool   | `true`   | Messages non lus uniquement |
| `idle`           | bool   | `false`  | Utiliser le mode IMAP IDLE |
| `delete`         | bool   | `false`  | Supprimer après traitement |
| `fetchSize`      | int    | `-1`     | Messages par interrogation |
| `pollDelay`      | int    | `60000`  | Intervalle d'interrogation (ms) |
| `maxMessageSize` | int    | `0`      | Taille max d'un message en octets (0 = illimité) |

---

### POP3/POP3S (Réception)

Réception d'emails via POP3.

```go
builder.From("pop3s://pop.gmail.com:995?username=user&password=pass")
```

Le consumer POP3 accepte la même option `maxMessageSize` qu'IMAP (défaut `0` =
illimité) pour borner la taille d'un message téléchargé.

---

## Composants de Base de Données

### SQL

Exécution de requêtes SQL via `database/sql`.

```go
import (
    "database/sql"
    _ "modernc.org/sqlite"
)

db, _ := sql.Open("sqlite", "./app.db")

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

| Option                | Type   | Défaut       | Description                                         |
|-----------------------|--------|--------------|-----------------------------------------------------|
| `query`               | string | requis       | Chaîne de requête SQL                               |
| `dataSourceRef`       | string | chemin hôte  | Nom de la datasource                                |
| `outputType`          | string | `SelectList` | `SelectList` ou `SelectOne`                         |
| `batch`               | bool   | `false`      | Mode d'exécution par lot                            |
| `transacted`          | bool   | `false`      | Encapsuler dans une transaction                     |
| `allowHeaderOverride` | bool   | `false`      | Autoriser `CamelSqlQuery` à remplacer la requête    |

**Paramètres de requête :**

Fournis via le header `CamelSqlParameters` ou le corps comme `[]any`.

> [!caution] La requête est une configuration de confiance
> La requête n'est **jamais** interpolée avec les données de l'Exchange :
> substituer `${header.X}` dans la chaîne SQL annulerait le bénéfice des
> requêtes paramétrées et ouvrirait la porte à l'injection SQL. Liez toujours
> les valeurs dynamiques via `CamelSqlParameters` (placeholders positionnels
> `?`) ou un corps `[]any`.
>
> Le header `CamelSqlQuery` remplace la requête **dans son intégralité** :
> depuis la v0.2 il est donc ignoré sauf si l'endpoint l'active avec
> `allowHeaderOverride=true`. Ne l'activez que là où les headers ne peuvent
> pas être influencés par une entrée non fiable — sinon tout composant
> reportant des métadonnées externes sur les headers donne à l'appelant le
> contrôle total de la requête.
>
> ```go
> // Requête fixée par la configuration (défaut, recommandé)
> builder.To("sql://appdb?query=SELECT+*+FROM+users+WHERE+id=?")
>
> // Requête fournie par message — uniquement avec des headers de confiance
> builder.To("sql://appdb?allowHeaderOverride=true&query=SELECT+1")
> ```


**Headers de sortie :**

- `CamelSqlRowCount` - Lignes retournées/affectées
- `CamelSqlColumnNames` - Noms des colonnes (SELECT)

**Corps du résultat :**

| Cas                     | Type de Out.Body           |
|-------------------------|----------------------------|
| `SELECT` + `SelectList` | `[]map[string]any`         |
| `SELECT` + `SelectOne`  | `map[string]any` ou `nil`  |
| `INSERT/UPDATE/DELETE`  | `int64` (lignes affectées) |

**Classification des requêtes :**

Le choix entre exécution en lecture (jeu de résultats) et en écriture (lignes
affectées) se fait sur le premier mot-clé, en ignorant les commentaires et
parenthèses de tête. `SELECT`, `WITH` (CTE), `VALUES`, `TABLE`, `SHOW`,
`EXPLAIN`, `DESCRIBE` et `PRAGMA` renvoient des lignes, tout comme une requête
portant une clause `RETURNING` :

```sql
WITH recent AS (SELECT * FROM orders WHERE ts > ?)
SELECT id, total FROM recent          -- jeu de résultats
INSERT INTO users(name) VALUES (?) RETURNING id   -- jeu de résultats
```

---

### SQL-Stored

Exécution de procédures stockées avec support des paramètres IN, OUT et INOUT.

```go
import (
    "database/sql"
    _ "modernc.org/sqlite"
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

| Option                | Type   | Défaut       | Description                                                  |
|-----------------------|--------|--------------|--------------------------------------------------------------|
| `procedure`           | string | requis       | Nom de la procédure stockée                                  |
| `dataSourceRef`       | string | chemin hôte  | Nom de la datasource                                         |
| `outputType`          | string | `SelectList` | `SelectList` ou `SelectOne`                                  |
| `transacted`          | bool   | `false`      | Exécuter dans une transaction                                |
| `noop`                | bool   | `false`      | Mode test (pas d'exécution)                                  |
| `allowHeaderOverride` | bool   | `false`      | Autoriser `CamelSqlStoredProcedureName` à remplacer le nom   |

> [!caution] Le nom de procédure est un identifiant de confiance
> Le nom de procédure est inséré dans la requête `CALL` et n'est **jamais**
> interpolé avec les données de l'exchange. Le header
> `CamelSqlStoredProcedureName` est ignoré sauf si l'endpoint opte avec
> `allowHeaderOverride=true` ; dans ce cas, la valeur est validée contre un
> motif d'identifiant strict (`[A-Za-z_][A-Za-z0-9_.]*`). N'activez la
> surcharge que là où les headers ne peuvent pas être influencés par une entrée
> non fiable.

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
| `allowHeaderOverride` | bool | Non | Autorise `CamelMongoDbDatabase`, `CamelMongoDbCollection`, `CamelMongoDbOperation` et `CamelMongoDbCriteria` à remplacer la configuration de l'URI (défaut : `false`) |

Par sécurité, `remove` refuse les filtres nuls ou vides. Définissez
`allowDeleteAll=true` explicitement uniquement si la suppression de toute la
collection est intentionnelle.

> ⚠️ Sécurité : la base, la collection et l'opération configurées dans l'URI
> constituent une configuration de confiance. Les headers
> `CamelMongoDbDatabase`, `CamelMongoDbCollection`, `CamelMongoDbOperation` et
> `CamelMongoDbCriteria` sont ignorés sauf si l'endpoint active explicitement
> `allowHeaderOverride=true`. Ne l'activez que si les headers ne peuvent pas
> être influencés par une entrée non fiable.

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

> [!caution] Sécurité
> Les surcharges de header permettent à un message de choisir **quel binaire est exécuté**. Elles sont
> désactivées par défaut ; n'activez `allowHeaderOverride=true` que lorsque les headers de message
> ne peuvent pas être influencés par une entrée non fiable (ex. headers provenant d'un consumer
> HTTP ou mail).
>
> L'**exécutable** et le **répertoire de travail** sont validés contre les
> métacaractères shell et le path traversal. Un exécutable fourni par header
> doit en outre être un **nom de commande simple** (sans chemin) — il est résolu
> via `PATH`, jamais par un chemin absolu — afin qu'un attaquant ne puisse pas
> injecter `/bin/sh` avec des arguments arbitraires. Les **arguments ne le sont
> pas** : les commandes sont lancées via `execve`, sans shell, donc `|`, `&`,
> `$`, `<` et `>` n'ont aucune signification particulière et parviennent
> verbatim au processus fils — c'est précisément l'intention. Les rejeter
> bloquait des valeurs légitimes (un document JSON, un mot de passe contenant
> `$`, un chemin relatif) sans empêcher la moindre injection. Le caractère NUL
> et les autres caractères de contrôle restent rejetés.


**Sortie :**

- `Out.Body` = stdout de la commande (également recopié sur `In` pour la
  rétrocompatibilité, afin qu'une route InOut voie la sortie de la commande)
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
| Net        | Réseau     | ✅        | ✅        | `net:tcp://host:port`     |
| Kafka      | Messagerie | ✅        | ✅        | `kafka:topic`             |
| gRPC       | Réseau     | ✅        | ✅        | `grpc://host:port/Service/Method` |
| Telegram   | Messagerie   | ✅        | ✅        | `telegram:bots`          |
| OpenAI     | IA           | ❌        | ✅        | `openai:chat`            |
| Cron       | Ordonnancement | ✅      | ❌        | `cron://group/job`       |
| SMTP       | Mail         | ❌        | ✅        | `smtp://host:port`       |
| IMAP       | Mail         | ✅        | ❌        | `imap://host:port`       |
| POP3       | Mail         | ✅        | ❌        | `pop3://host:port`       |
| SQL        | Base de données | ❌     | ✅        | `sql://datasource`       |
| SQL-Stored | Base de données | ❌     | ✅        | `sql-stored://datasource`|
| MongoDB    | Base de données | ❌     | ✅        | `mongodb:connectionName` |
| XSLT       | Transformation | ❌     | ✅        | `xslt:template`          |
| XSD        | Transformation | ❌     | ✅        | `xsd:schema`             |
| Template   | Transformation | ❌     | ✅        | `template:template`      |
| Exec       | Exécution    | ❌        | ✅        | `exec:command`           |
| NATS       | Messagerie   | ✅        | ✅        | `nats://subject`         |
| Redis      | Messagerie   | ✅        | ✅        | `redis://host:port`      |
| Go Channel | Core         | ✅        | ✅        | `chan:channelName`       |
