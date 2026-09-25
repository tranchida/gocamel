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
- `CamelFileName` : Surcharge le nom du fichier lorsque l'URI cible pointe vers un répertoire. Doit être un chemin **relatif** : nom vide, chemin absolu, volume Windows et composants `..` qui sortent du répertoire de l'endpoint sont rejetés.

Le consumer ignore les liens symboliques et refuse les fichiers de plus de 32 Mio
(`DefaultMaxBodySize`).

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

Avec `fileExist=Append`, le producer télécharge le fichier distant existant
puis renvoie l'envoi de la concaténation. Un téléchargement partiel fait
échouer l'envoi plutôt que de renvoyer silencieusement un contenu tronqué.

Lorsque le chemin de l'URI est un répertoire, `CamelFileName` est joint avec
`JoinRemotePath` : noms absolus et traversée de répertoires sont rejetés.

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

Comme pour FTP, `fileExist=Append` concatène au fichier distant existant et
fait échouer l'envoi si sa lecture échoue en cours de route. `CamelFileName`
est confiné comme pour FTP (`JoinRemotePath`).

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

La sémantique de `fileExist=Append` est identique à celle de FTP/SFTP.
`CamelFileName` est confiné avec `JoinRemotePath` (noms absolus et `..`
rejetés ; le producer n'utilise pas `filepath.Join`, qui jetterait le chemin
du partage si le header était absolu).

---

## Composants Réseau

### HTTP

Support serveur et client HTTP.

```go
// Consumer (serveur HTTP) — écoute en clair uniquement
builder.From("http://localhost:8080/api")

// Producer (client HTTP)
builder.To("http://example.com/webhook")
```

Le scheme `https` est **producer uniquement**. `From("https://...")` échoue à
la création du consumer : le listener est du HTTP en clair. Terminez le TLS
devant un consumer `http://` (reverse proxy, `HTTPComponent.SetMiddleware`, etc.).

#### Headers posés par le consumer

| Header            | Source                              |
|-------------------|-------------------------------------|
| `CamelHttpMethod` | Méthode de la requête (`GET`, `POST`, …) |
| `CamelHttpPath`   | Chemin de l'URL                     |
| `CamelHttpQuery`  | Query string brute                  |
| `CamelHttpUrl`    | `url.URL.String()` de la requête    |

Le producer envoie en **POST** sauf si `CamelHttpMethod` est posé sur
l'exchange (un pont `From(http).To(http)` transmet donc la méthode entrante).

Les headers de la requête entrante sont aussi copiés sur l'exchange (voir
headers de réponse ci-dessous).

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
- **Les headers internes `Camel*` ne franchissent jamais la frontière HTTP.**
  Ils ne sont pas émis dans la réponse (`CamelHttpQuery` renvoyait les secrets
  de la query string et `CamelFileName` des chemins internes), et un client ne
  peut pas les poser sur l'exchange : les headers de requête commençant par
  `Camel` sont ignorés.

Un header contenant un `[]string` est émis comme header répété (par exemple
plusieurs `Set-Cookie`). Symétriquement, le producer stocke un header de
réponse répété sous forme de `[]string` sur `Out` ; les headers à valeur
unique restent des `string`.

Le même filtre s'applique au producer : un pont `From(http://...)` →
`To(http://...)` ne transmet donc pas le `Content-Length` ni le `Host` entrants
au service amont.

Le producer n'envoie jamais les headers internes `Camel*` au service amont. Il
transmet les autres headers, avec une exception pour les identifiants : les
valeurs `Authorization` et `Cookie` envoyées par un client au consumer HTTP ne
sont **pas** relayées telles quelles vers l'amont, afin qu'un pont
`From(http://...)` → `To(http://...)` ne fasse pas fuiter les identifiants d'un
client vers un tiers. Un identifiant posé par la route elle-même (par exemple
un jeton de service) est toujours envoyé. Pour un proxy qui doit volontairement
relayer l'authentification du client, activez l'option :

```go
http := gocamel.NewHTTPComponent()
http.SetForwardInboundCredentials(true)
```

Pour retirer `Authorization` et `Cookie` de toutes les requêtes sortantes du
composant, y compris celles posées par la route :

```go
http.SetSkipAuthorizationHeaders(true)
```

**Corps de la requête.** Le producer envoie les corps `[]byte`, `string` et
`io.Reader` ; un corps `nil` n'envoie aucun contenu. Tout autre type (map,
struct) est rejeté avec une erreur au lieu d'être envoyé silencieusement comme
un corps vide : sérialisez-le d'abord (par exemple `MarshalJSON`).

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
| `maxConnections` | int | `1024` | Nombre maximal de connexions TCP simultanées du consumer (0 = illimité) ; les connexions en trop sont fermées immédiatement |

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
- `maxConnections` (défaut 1024) borne le nombre de connexions TCP
  simultanées, afin qu'un afflux de clients ne puisse pas épuiser les
  descripteurs de fichiers du processus.
- Une erreur transitoire d'`Accept` (comme `EMFILE`, trop de fichiers ouverts)
  n'arrête plus le consumer : il journalise un avertissement et réessaie avec
  un délai croissant plafonné à une seconde, comme `net/http.Server`.
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

Quand un `groupId` est configuré, chaque message traité avec succès est
committé auprès du groupe de consommateurs. Un échec de commit est journalisé
(avec topic/partition/offset) plutôt qu'ignoré silencieusement : le message
sera redélivré après un rééquilibrage ou un redémarrage, et la ligne de journal
est le seul signal de ce traitement dupliqué.

---

### STOMP

Production et consommation de messages avec tout broker compatible STOMP 1.0/1.1/1.2 — ActiveMQ, ActiveMQ Artemis, RabbitMQ, Apollo — via le client `go-stomp`. Idéal pour les destinations JMS de type file (`/queue/...`) et topic (`/topic/...`).

```go
import "gitlab.com/tranchida/gocamel/components/stomp"

ctx.AddComponent("stomp", stomp.NewStompComponent())

// Consommateur : souscription à une file ActiveMQ
builder.From("stomp://queue/orders?host=localhost:61613&ack=client-individual").
    Log("Message reçu : ${body}")

// Consommateur sur un topic
builder.From("stomp://topic/prices?host=localhost:61613")

// Producteur
builder.From("direct:publish").
    To("stomp://queue/orders?host=localhost:61613")

// Destination dynamique (nécessite allowHeaderOverride=true)
exchange.In.SetHeader("CamelStompDestination", "/queue/urgent-orders")
```

| Option | Type | Défaut | Description |
|---|---|---|---|
| `host` | string | `localhost:61613` | Adresse du broker `host:port` (connecteur STOMP ActiveMQ) |
| `destination` | string | analysée depuis le chemin | Destination STOMP (`/queue/...`, `/topic/...`) |
| `login` | string | `""` | Identifiant envoyé dans la trame CONNECT STOMP |
| `passcode` | string | `""` | Mot de passe envoyé dans la trame CONNECT STOMP (préférez la variable d'environnement `STOMP_PASSCODE`) |
| `ack` | string | `auto` | Mode d'accusé de réception : `auto`, `client`, `client-individual` (consommateur uniquement) |
| `allowHeaderOverride` | bool | `false` | Autorise `CamelStompDestination` à remplacer l'URI en production |
| `forwardHeaders` | string | `""` | Liste de noms de headers (séparés par des virgules) propagés comme headers de trame STOMP ; si définie, SEULS ces headers sont propagés |
| `ssl` | bool | `false` | Connexion au broker via TLS |
| `sslCAFile` | string | racines système | Fichier PEM de l'autorité de certification vérifiant le certificat du broker |
| `sslCertFile` | string | `""` | Fichier PEM du certificat client pour le TLS mutuel (nécessite `sslKeyFile`) |
| `sslKeyFile` | string | `""` | Fichier PEM de la clé privée client pour le TLS mutuel (nécessite `sslCertFile`) |
| `sslServerName` | string | hôte du broker | Nom du serveur utilisé pour le SNI et la vérification du certificat |
| `sslInsecureSkipVerify` | bool | `false` | Désactive la vérification du certificat du broker (environnements contrôlés uniquement) |

**Headers d'Exchange :**
- `CamelStompDestination` - Destination d'origine du message reçu, ou destination cible en production (honorée avec `allowHeaderOverride=true`).
- `CamelStompContentType` - Type MIME envoyé avec le message ou reçu du broker.

> ⚠️ Sécurité : la destination configurée dans l'URI constitue une
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

Avec `ack=client` ou `ack=client-individual`, un message n'est accusé reçu
qu'après que la route l'ait traité avec succès ; un échec de traitement est
retenté avant tout accusé de réception, le broker redélivre donc le message si
la connexion tombe entre-temps. Avec `ack=auto` (défaut), le broker considère
le message comme délivré dès son envoi.

**SSL/TLS :** activez `ssl=true` pour vous connecter au broker via TLS (par
exemple le connecteur STOMP+SSL d'ActiveMQ sur le port 61614). Le certificat
du serveur est vérifié contre les racines système par défaut, ou contre le
fichier CA fourni via `sslCAFile`. Le TLS mutuel utilise
`sslCertFile`/`sslKeyFile`. La configuration TLS est validée à la création de
l'endpoint : un fichier CA manquant ou une paire de clés invalide échoue
immédiatement.

```go
// TLS avec une CA privée
builder.From("stomp://queue/orders?host=activemq:61614&ssl=true&sslCAFile=/etc/gocamel/ca.pem")

// TLS mutuel
builder.To("stomp://queue/orders?host=activemq:61614&ssl=true&sslCAFile=/etc/gocamel/ca.pem&sslCertFile=/etc/gocamel/client.pem&sslKeyFile=/etc/gocamel/client.key")
```

> [!WARNING]
> `sslInsecureSkipVerify=true` désactive la vérification du certificat du
> serveur et expose la connexion à des attaques de l'homme du milieu. Ne
> l'utilisez que dans des environnements contrôlés (développement local,
> bancs de test).

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
| `shutdownTimeout` | int | `30000` | Durée pendant laquelle `Stop` continue de traiter les exchanges en file, en ms (0 = les abandonner) |

**Notes :**

- Les endpoints sont mis en cache **par nom de canal** : chaque producer et consumer
  utilisant le même nom partage un endpoint, et **un seul consumer** par canal est
  autorisé (un second `From("chan:orders")` échoue au démarrage).
- Les options URI sont appliquées lorsque l'endpoint est **créé pour la première
  fois** ; les références ultérieures avec des options différentes émettent un
  avertissement et réutilisent l'endpoint existant.
- Le consumer traite chaque exchange sous **son propre contexte**, et non celui de
  l'émetteur : une requête HTTP qui a mis un message en file puis s'est terminée
  n'annule pas la partie asynchrone.
- `Stop` refuse les nouveaux envois, puis termine les exchanges déjà en file pendant au
  plus `shutdownTimeout` avant d'annuler le reste (voir SEDA dans la page EIP).

---

## Composants IA

### OpenAI

Intégration de l'API OpenAI pour ChatGPT/GPT-4. Tout endpoint compatible
OpenAI est pris en charge via l'option `baseURL` (OpenRouter, Groq, Together,
Mistral, vLLM, LM Studio, couche de compatibilité OpenAI d'Ollama, ...).

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

Utiliser un fournisseur compatible OpenAI :

```go
// Exemple OpenRouter : router vers openai/gpt-4o via la passerelle OpenRouter
endpoint, _ := ctx.CreateEndpoint(
    "openai:chat?model=openai/gpt-4o" +
        "&baseURL=https://openrouter.ai/api/v1" +
        "&apiKey=$OPENROUTER_API_KEY")
```

**Variables d'environnement :**

- `OPENAI_AUTHORIZATIONTOKEN` ou `OPENAI_API_KEY` - Clé API

| Option               | Type   | Défaut               | Description                                                     |
|----------------------|--------|----------------------|-----------------------------------------------------------------|
| `model`              | string | `gpt-3.5-turbo`      | Modèle à utiliser                                               |
| `authorizationToken` | string | variable d'env       | Clé API (alias : `apiKey`)                                      |
| `baseURL`            | string | `https://api.openai.com/v1` | URL de base compatible OpenAI personnalisée (OpenRouter, Groq, ...) |

Le timeout HTTP du SDK est de **60 secondes**. Bornez davantage les appels
longs avec `exchange.Context`.

### Anthropic

Intégration native de l'API Messages d'Anthropic (Claude). Contrairement au
composant OpenAI, c'est un connecteur dédié à la forme native de l'API
Claude — paramètre `system` séparé, blocs de contenu, en-têtes de cache de
prompt et raisons d'arrêt spécifiques.

```go
ctx.AddComponent("anthropic", gocamel.NewAnthropicComponent())

// Producer uniquement - envoyer une requête Messages
endpoint, _ := ctx.CreateEndpoint(
    "anthropic:messages?model=claude-sonnet-4-5" +
        "&apiKey=$ANTHROPIC_API_KEY" +
        "&maxTokens=1024" +
        "&system=Tu es un assistant concis")
producer, _ := endpoint.CreateProducer()

exchange := gocamel.NewExchange(context.Background())
exchange.GetIn().SetBody("Explique EIP en une phrase.")
producer.Send(exchange)

fmt.Println(exchange.GetOut().GetBody()) // Réponse de Claude
```

**Variables d'environnement :**

- `ANTHROPIC_API_KEY` - Clé API (recommandé plutôt que l'intégrer dans l'URI)

| Option               | Type   | Défaut      | Description                                                    |
|----------------------|--------|-------------|----------------------------------------------------------------|
| `apiKey`             | string | variable d'env | Clé API (alias : `authorizationToken`). Masquée par `RedactURI`. |
| `model`              | string | requis      | Nom du modèle, ex. `claude-sonnet-4-5`                         |
| `maxTokens`          | int    | `1024`      | Nombre maximum de tokens à générer                             |
| `system`             | string | `""`        | Prompt système                                                |
| `temperature`         | float  | non défini  | Température d'échantillonnage (0.0-1.0)                       |
| `baseURL`            | string | API Anthropic | URL de base personnalisée (proxies, passerelles). Doit être `http(s)`. |

Le timeout HTTP du SDK est de **60 secondes**.

**En-têtes de surcharge par message** (message In) :

| En-tête                   | Type   | Description                                |
|---------------------------|--------|--------------------------------------------|
| `AnthropicSystem`         | string | Surcharge le `system` de l'URI             |
| `AnthropicModel`          | string | Surcharge le `model` de l'URI               |
| `AnthropicMaxTokens`     | string | Surcharge le `maxTokens` de l'URI           |
| `AnthropicTemperature`    | string | Surcharge le `temperature` de l'URI         |

**En-têtes Out** (définis sur la réponse) :

| En-tête                       | Type  | Description                   |
|-------------------------------|-------|-------------------------------|
| `AnthropicUsageInputTokens`  | int64 | Tokens d'entrée facturés      |
| `AnthropicUsageOutputTokens`  | int64 | Tokens de sortie facturés     |
| `AnthropicStopReason`        | string | `end_turn`, `max_tokens`, ... |
| `AnthropicModel`              | string | Modèle ayant produit la réponse |

### Ollama

Intégration native avec un serveur [Ollama](https://ollama.com) local via son
client Go officiel (`github.com/ollama/ollama/api`). Trois modes sont
sélectionnés par le chemin de l'URI :

- `ollama:chat` (défaut) — API de chat multi-tours (`/api/chat`)
- `ollama:generate` — génération one-shot (`/api/generate`)
- `ollama:embed` — embeddings (`/api/embed`)

```go
ctx.AddComponent("ollama", gocamel.NewOllamaComponent())

// Discuter avec un modèle local
endpoint, _ := ctx.CreateEndpoint(
    "ollama:chat?model=llama3.1&host=http://localhost:11434")
producer, _ := endpoint.CreateProducer()

exchange := gocamel.NewExchange(context.Background())
exchange.GetIn().SetBody("Résume EIP en une phrase.")
producer.Send(exchange)

fmt.Println(exchange.GetOut().GetBody()) // Réponse du modèle local
```

Générer des embeddings localement :

```go
endpoint, _ := ctx.CreateEndpoint(
    "ollama:embed?model=nomic-embed-text&host=http://localhost:11434")
// exchange.GetOut().GetBody() retourne []float32
```

**Variables d'environnement :**

- `OLLAMA_HOST` - hôte par défaut lorsque l'option URI `host` est omise

| Option       | Type    | Défaut                    | Description                                                  |
|--------------|---------|---------------------------|--------------------------------------------------------------|
| `model`      | string  | requis (chat/generate)    | Nom du modèle                                                |
| `host`       | string  | `http://localhost:11434`  | URL de base du serveur Ollama (doit être `http(s)`)           |
| `system`     | string  | `""`                      | Prompt système (mode generate uniquement)                   |
| `keepAlive`  | durée   | non défini                | Rétention du modèle en mémoire (ex. `5m`, `30s`)             |
| `think`      | string  | non défini                | Effort de raisonnement : `true`/`false`/`low`/`medium`/`high`/`max` |
| `format`     | string  | non défini                | Format de sortie structuré (ex. `json`)                      |

**En-têtes de surcharge par message** (message In) :

| En-tête         | Type   | Description                                  |
|-----------------|--------|----------------------------------------------|
| `OllamaModel`   | string | Surcharge le `model` de l'URI                  |
| `OllamaSystem`  | string | Surcharge le `system` de l'URI (chat/generate) |

**En-têtes Out** (définis sur la réponse) :

| En-tête                  | Type  | Description (chat/generate)         |
|-------------------------|-------|------------------------------------|
| `OllamaModel`           | string | Modèle ayant produit la réponse   |
| `OllamaDoneReason`      | string | `stop`, `length`, ...              |
| `OllamaTotalDuration`  | int64  | Durée totale (ns)                  |
| `OllamaPromptEvalCount` | int    | Tokens d'entrée évalués           |
| `OllamaEvalCount`       | int    | Tokens de sortie générés          |

> Le composant Ollama **n'expose pas** les opérations de cycle de vie des
> modèles (Pull/List/Delete). Utilisez la CLI `ollama` ou l'API HTTP
> directement pour la gestion des modèles ; le producer GoCamel est limité
> à l'inférence.

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

À l'appel de `Stop()`, un job dont le `triggerStartDelay` n'est pas encore
écoulé n'est jamais enregistré sur le scheduler partagé : la demande d'arrêt
gagne la course avec l'enregistrement différé, aucun job orphelin ne peut donc
rester à se déclencher indéfiniment.

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

Avec `disconnect=false` (défaut), la connexion POP3 est conservée entre les
polls, comme pour le consumer IMAP. Définissez `disconnect=true` pour vous
ré-authentifier à chaque poll (utile avec les serveurs qui coupent les
connexions inactives).

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
| `allowUpdateAll` | bool   | Non    | Autorise `update` avec un filtre vide, c'est-à-dire une mise à jour massive de toute la collection (défaut : `false`) |
| `allowHeaderOverride` | bool | Non | Autorise `CamelMongoDbDatabase`, `CamelMongoDbCollection`, `CamelMongoDbOperation` et `CamelMongoDbCriteria` à remplacer la configuration de l'URI (défaut : `false`) |

Par sécurité, `remove` refuse les filtres nuls ou vides. Définissez
`allowDeleteAll=true` explicitement uniquement si la suppression de toute la
collection est intentionnelle. De même, `update` refuse un filtre absent ou
vide (`nil`, `{}`, `bson.M` / `bson.D` vides, chaîne vide) : chacun
mettrait à jour tous les documents. Définissez `allowUpdateAll=true`
explicitement uniquement si c'est intentionnel.

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
{"order": {{json .Body}}, "meta": {{json .Headers.meta}}}
```

`json` encode n'importe quelle valeur (chaîne, nombre, map, slice) avec
`encoding/json`. Sa sortie échappe `<`, `>` et `&` : elle est donc sûre en HTML
comme dans une charge utile JSON. Dans un bloc `<script>`, écrivez plutôt
`{{.Body}}` : le moteur de templates y encode déjà les valeurs en JavaScript.
Le cache des templates compilés (`contentCache`) est indexé par chemin **et**
par délimiteurs.

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
| Anthropic  | IA           | ❌        | ✅        | `anthropic:messages`     |
| Ollama     | IA           | ❌        | ✅        | `ollama:chat`/`generate`/`embed` |
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

## Détails de livraison et de cycle de vie

- **Kafka :** le topic résolu est porté par chaque message ; les topics configurés et surchargés par en-tête fonctionnent ensemble. Un traitement en échec est réessayé avec un nouvel échange avant la lecture suivante. Un commit en échec est réessayé sans refaire le traitement réussi. Les tentatives sont espacées de 100 ms et cessent à l’annulation. Un échec permanent bloque ce consommateur jusqu’à sa résolution par la route ou son arrêt, afin de préserver les offsets cumulatifs.
- **FTP :** chaque producteur réserve sa connexion exclusivement pendant tout le transfert. Les envois concurrents et `Stop` ne peuvent pas entrelacer les commandes FTP ; un envoi en attente de réservation peut être annulé.
- **MongoDB :** `save` préserve le `_id` du document entrant, même si le remplacement échoue ; le retry cible ainsi le même document.
- **Mail :** IMAP IDLE réagit aux notifications de boîte en terminant IDLE, en recherchant les messages puis en reprenant IDLE. Une première recherche traite les messages déjà présents.
- **Cron :** `Stop` empêche de nouvelles exécutions et attend les jobs actifs avant de retourner, y compris lors d’une pause.
- **Redis :** les producteurs et consommateurs actifs partagent leurs clients, fermés au départ du dernier utilisateur. Arrêter un producteur laisse les autres utilisables ; les consommateurs arrêtés libèrent leur enregistrement pour permettre le redémarrage de la route.
- **File :** une URI terminée par `/` désigne un répertoire même s’il n’existe pas encore ; `CamelFileName` sélectionne le fichier à l’intérieur.
- **Net :** le timeout configuré borne les écritures établies ainsi que la lecture des réponses. Annuler l’échange ferme sa connexion et interrompt ces opérations.
- **Prometheus :** des appels répétés à `Metrics()` ou `MetricsWith(registry)` réutilisent les collecteurs compatibles déjà enregistrés, ce qui permet des intercepteurs distincts sur plusieurs routes. Les définitions incompatibles échouent toujours à l’enregistrement.
