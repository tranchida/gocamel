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
endpoint est autorisé, et `DirectProducer.Send` exige que le `DirectConsumer` cible soit démarré (`Start()`).

---

### Timer

Déclenchement périodique simple.

```go
builder.From("timer:tick?period=5000")
```

| Option        | Type     | Défaut | Description                                       |
|---------------|----------|--------|---------------------------------------------------|
| `period`      | int (ms) | `1000` | Période entre les déclenchements, en millisecondes |
| `delay`       | int (ms) | `1000` | Délai initial avant le premier déclenchement, en ms |
| `repeatCount` | int      | `0`    | Nombre de répétitions (0=infini)                  |
| `fixedRate`   | bool     | `false`| Mode à fréquence fixe vs délai fixe               |

`period` et `delay` sont de simples entiers en millisecondes, pas des
durées Go : `period=5000` déclenche toutes les 5 secondes. Une valeur comme
`period=5s` échoue à l'analyse et l'option garde silencieusement sa valeur par défaut.

---

### Mock

Endpoint de test, producer uniquement (enregistré par défaut), qui enregistre chaque échange reçu.
À combiner avec `Route.AdviceWith` pour remplacer les endpoints réels d'une route construite avant `Start()`.

```go
route, _ := ctx.CreateRouteBuilder().
    From("direct:in").
    To("kafka:orders").
    Build()

// Remplacer l'endpoint réel par un mock avant Start.
_ = route.AdviceWith(map[string]string{"kafka:orders": "mock:orders"})

mock, _ := ctx.GetMockEndpoint("mock:orders")
mock.ExpectedMessageCount(2)
_ = ctx.Start()
// ... envoyer deux échanges vers direct:in ...
if err := mock.Wait(time.Second); err != nil {
    t.Fatal(err) // "expected at least 2 messages but received 1 ..."
}
body := mock.Body(0)
```

| Méthode                       | Description                                                                 |
|-------------------------------|-----------------------------------------------------------------------------|
| `MessageCount()`              | Nombre d'échanges reçus                                                     |
| `Message(i)`                  | Instantané du i-ème échange (ordre d'arrivée), `nil` hors limites           |
| `Body(i)` / `Header(i, name)` | Corps / en-tête In du i-ème échange                                         |
| `ExpectedMessageCount(n)`     | Attendre **au moins** `n` échanges (davantage est accepté)                  |
| `Wait(timeout)`               | Bloque jusqu'à ce que l'attente soit satisfaite ; erreur avec les compteurs à l'expiration |
| `Reset()`                     | Efface les échanges reçus et l'attente                                      |

- Les endpoints sont identifiés par **leur nom uniquement** et mis en cache par contexte :
  `mock:out` dans une route et `GetMockEndpoint("out")` désignent le même endpoint.
- Chaque échange est enregistré sous forme de `DeepCopy` prise à l'envoi (ID d'échange
  d'origine conservé) : une modification ultérieure de l'échange vivant ne change pas l'instantané.
- Les endpoints mock n'ont pas de consumer : `From("mock:x")` échoue.

`AdviceWith` substitue les URI exactes des producers statiques `To()`, y compris le multicast
`To(a, b)` et les branches Choice / LoadBalancer / Saga. Il ne substitue **pas** l'endpoint
`From()`, les templates `ToD` ni les autres envois dynamiques (RecipientList, RoutingSlip,
DynamicRouter, WireTap, Enrich, DeadLetterChannel). Les URI inconnues sont ignorées ; la
substitution est une passe unique non transitive. Il renvoie une erreur tant que la route est
démarrée (il est de nouveau autorisé après `Stop()`).

---

## Composants de Transfert de Fichiers

### File

Opérations sur le système de fichiers local. Vit dans son propre module
(`github.com/tranchida/gocamel/components/file`, dépend de `fsnotify`) et doit
être enregistré explicitement :

```go
import "github.com/tranchida/gocamel/components/file"

ctx.AddComponent("file", file.NewFileComponent())
```

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
| `move`                  | string   | `""`       | Répertoire où déplacer le fichier après traitement                                    |
| `moveFailed`            | string   | `""`       | Répertoire où déplacer le fichier en cas d'échec                                      |
| `readLock`              | string   | `changed`  | Stratégie de verrouillage (`none`, `changed`, `rename`, `markerFile`)                 |
| `readLockTimeout`       | Duration | `10s`      | Délai maximal d'attente pour acquérir le verrou                                       |
| `readLockCheckInterval` | Duration | `100ms`    | Intervalle entre les vérifications du verrou                                          |
| `readLockMinLength`     | int64    | `0`        | Taille minimale du fichier en octets                                                  |
| `readLockMinAge`        | Duration | `0`        | Âge minimal du fichier                                                                |
| `maxSeenEntries`        | int      | `100000`   | Fichiers mémorisés pour ignorer les événements en double ; les plus anciens sont évincés en premier (`0` = illimité) |
| `fileExist`             | string   | `Override` | Comportement si le fichier destination existe (`Override`, `Append`, `Fail`, `Ignore`) |

Un répertoire `move` / `moveFailed` relatif est résolu à côté du fichier
consommé : avec `From("file:///data/in?move=.done")`, `/data/in/a.txt` aboutit
dans `/data/in/.done/a.txt`. Un répertoire absolu est utilisé tel quel. Les
fichiers présents dans ces répertoires ne sont jamais reconsommés, y compris
avec `recursive=true`.

Les fichiers déjà présents au démarrage de la route sont consommés en
arrière-plan : `Start()` n'attend pas la fin de l'arriéré.

#### En-têtes

##### Définis par le Consumer
- `CamelFileName` : Nom relatif du fichier
- `CamelFilePath` : Chemin absolu ou complet du fichier
- `CamelFileLength` : Taille du fichier en octets
- `CamelFileLastModified` : Date et heure de dernière modification (`time.Time`)

##### Consommés par le Producer
- `CamelFileName` : Nom du fichier de destination dans le répertoire cible. **Obligatoire** lorsque l'URI du producer pointe vers un répertoire (y compris se terminant par `/`). Doit être un chemin **relatif** (`ConfineFileName`) : nom vide, chemin absolu, volume Windows et composants `..` qui sortent du répertoire de l'endpoint sont rejetés.

Le consumer ignore les liens symboliques (avec pré-vérification `Lstat` et `O_NOFOLLOW` sous Unix contre le TOCTOU) et refuse les fichiers de plus de 32 Mio
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
| `maxMessageSize`        | int      | `33554432` | Taille max d'un fichier pollé ou écrit en octets (`DefaultMaxBodySize`, 32 Mio si omis ou `<= 0`) |
| `readLock`              | string   | `none` | Stratégie de verrou (`none`, `changed`, `rename`, `markerFile`)    |
| `readLockTimeout`       | Duration | `10s`  | Délai maximal d'attente pour acquérir le verrou                    |
| `readLockCheckInterval` | Duration | `1s`   | Intervalle entre les vérifications du verrou                       |
| `readLockMinLength`     | int64    | `0`    | Taille minimale du fichier en octets                               |
| `readLockMinAge`        | Duration | `0`    | Âge minimal du fichier                                             |

Avec `fileExist=Append`, le producer utilise la commande FTP native `APPE` (`Append`).
`CamelFileName` et les chemins distants sont validés avec `ValidateRemotePath` /
`JoinRemotePath` : les caractères CR/LF, les noms absolus et la traversée `..`
sont rejetés. Les sockets de handshake et de données sont bornées par des délais
et suivies pour un arrêt propre, et les hôtes IPv6 reçoivent un port par défaut.

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
| `maxMessageSize`        | int      | `33554432` | Taille max d'un fichier pollé ou écrit en octets (`DefaultMaxBodySize`, 32 Mio si omis ou `<= 0`) |
| `readLock`              | string   | `none` | Stratégie de verrou (`none`, `changed`, `rename`, `markerFile`)    |
| `readLockTimeout`       | Duration | `10s`  | Délai maximal d'attente pour acquérir le verrou                    |
| `readLockCheckInterval` | Duration | `1s`   | Intervalle entre les vérifications du verrou                       |
| `readLockMinLength`     | int64    | `0`    | Taille minimale du fichier en octets                               |
| `readLockMinAge`        | Duration | `0`    | Âge minimal du fichier                                             |

`fileExist=Append` ouvre le fichier distant avec `O_APPEND` atomique et se
place en fin de fichier, `Fail`/`Ignore` utilisent `O_EXCL` atomique, et les
erreurs de `file.Close()` sont propagées. `CamelFileName` est confiné en chemins
POSIX avec `JoinRemotePath` (CR/LF, noms absolus et `..` rejetés).
`readLock=rename` utilise `Rename`, et les déplacements basculent de
`PosixRename` vers `Rename`.

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
| `maxMessageSize`        | int      | `33554432` | Taille max d'un fichier pollé ou écrit en octets (`DefaultMaxBodySize`, 32 Mio si omis ou `<= 0`) |
| `readLock`              | string   | `none`    | Stratégie de verrou (`none`, `changed`, `rename`, `markerFile`)    |
| `readLockTimeout`       | Duration | `10s`     | Délai maximal d'attente pour acquérir le verrou                    |
| `readLockCheckInterval` | Duration | `1s`      | Intervalle entre les vérifications du verrou                       |
| `readLockMinLength`     | int64    | `0`       | Taille minimale du fichier en octets                               |
| `readLockMinAge`        | Duration | `0`       | Âge minimal du fichier                                             |

`fileExist=Append` utilise `O_APPEND` atomique, `Fail`/`Ignore` utilisent
`O_EXCL` atomique, les erreurs de `file.Close()` sont propagées et le consumer
ignore les liens symboliques. `CamelFileName` est confiné en chemins POSIX via
`JoinRemotePath` (sûr sous Unix comme sous Windows ; noms absolus et `..`
rejetés).

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

Plusieurs routes peuvent écouter sur le même `host:port` avec des chemins
différents : les consumers d'un même `HTTPComponent` partagent le listener, qui
est fermé à l'arrêt du dernier. Servir deux fois le même chemin sur un port
échoue au démarrage.

```go
builder.From("http://0.0.0.0:8080/orders")
builder.From("http://0.0.0.0:8080/health")
```

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
contexte. Il s'exécute pour les requêtes routées vers un consumer ; un chemin
qu'aucun consumer ne sert répond 404 sans passer par lui.

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
  route est toujours émis. `Content-Type` fait exception : il décrit le corps
  de la réponse et est toujours émis, si bien qu'une route qui répond du JSON à
  une requête JSON garde son `application/json` (et une route qui ne le pose
  pas renvoie celui de la requête).
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
au service amont. Les headers propres aux réponses (`Set-Cookie`, `Server`,
`WWW-Authenticate`, `Location`, `ETag`, `Status-Code`...) ne sont pas envoyés
non plus : dans `To("http://a").To("http://b")`, les headers de réponse de `a`
restent sur le message et parvenaient à `b`, ce qui transmettait le cookie de
session de `a` à un autre service.

Le producer n'envoie jamais les headers internes `Camel*` au service amont et
retire tout en-tête sortant `Accept-Encoding` afin que `http.Transport` gère la
négociation gzip et la décompression transparente automatiquement. Il
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
  d'en-têtes lents), un `ReadTimeout` de 60 secondes (attaques de corps lents),
  un `WriteTimeout` / échéance d'écriture par réponse de 60 secondes
  (`httpWriteTimeout`), et un `IdleTimeout` de 60 secondes (connexions
  keep-alive inactives).
- `NewHTTPComponent` installe par défaut une politique `CheckRedirect` qui
  refuse les redirections de déclassement HTTPS→HTTP et supprime les en-têtes
  sensibles (`Authorization`, `Cookie`, `Cookie2`, `Proxy-Authorization`,
  `X-Api-Key`, `Api-Key`, `X-Auth-Token`) lors des redirections cross-origin ;
  personnalisez ou restaurez cette politique avec `HTTPComponent.SetCheckRedirect`.
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
| `textline`   | bool   | `true`  | Messages délimités par saut de ligne (TCP uniquement) : requêtes et réponses se terminent par `\n`. `false` = un message par lecture |
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
de ligne est un message (le `\r\n` ou `\n` final est retiré). Le producer
ajoute un `\n` à une requête qui n'en a pas et lit sa réponse jusqu'au `\n`
suivant ; le consumer termine chaque réponse par `\n` de la même façon, de
sorte qu'un producer et un consumer GoCamel dialoguent avec les options par
défaut. Avec `textline=false`, chaque lecture est un message. Les datagrammes UDP n'ont
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
import "github.com/tranchida/gocamel/components/telegram"

// Requis : définir la variable d'environnement TELEGRAM_AUTHORIZATIONTOKEN
ctx.AddComponent("telegram", telegram.NewTelegramComponent())

// Consumer (mode webhook/polling)
builder.From("telegram:bots").LogSimple("${body}")

// Producer (envoi de messages)
builder.To("telegram:bots")
```

**Variables d'environnement :**

- `TELEGRAM_AUTHORIZATIONTOKEN` - Token de l'API Bot

| Option               | Type   | Défaut | Description        |
|----------------------|--------|--------|--------------------|
| `authorizationToken` | string | env var | Token de l'API Bot |

Les jetons de bot sont validés contre les espaces, caractères de contrôle et délimiteurs d'URL, et masqués dans les erreurs du client HTTP. Le client HTTP utilise un timeout borné de `75s`, le consumer interroge l'API avec une boucle `GetUpdates` sensible au contexte et ignore sans panic les messages sans `Chat`.

---

### Webex

Intégration de bot Cisco Webex avec réception sortante via WebSocket Mercury (sans webhook ni URL publique) et envoi de messages via l'API REST (`POST /v1/messages`).

```go
import "github.com/tranchida/gocamel/components/webex"

// Requis (recommandé) : définir la variable d'environnement WEBEX_ACCESS_TOKEN
ctx.AddComponent("webex", webex.NewWebexComponent())

// Bot conversationnel répondant en Markdown dans la même room (et le même thread)
builder.From("webex:bots?allowedDomains=company.com").
    To("openai:chat?model=gpt-4o").
    To("webex:bots?allowHeaderOverride=true")

// Envoi direct vers une room fixe ou un utilisateur
builder.To("webex:bots?roomId=Y2lzY29zcGFyazovL...&format=markdown")
builder.To("webex:bots?toPersonEmail=alice@company.com&format=text")
```

**Variables d'environnement (priorité du token, de la plus haute à la plus basse) :**

- `WEBEX_ACCESS_TOKEN` — Token du bot Webex
- `WEBEX_ACCESSTOKEN` — Alias de repli
- `WEBEX_AUTHORIZATIONTOKEN` — Alias de repli (prioritaire sur les paramètres URI `?accessToken=` / `?authorizationToken=`)

Toutes les options non-secrètes sont lues strictement depuis la query string de l'URI.

| Option | Portée | Type | Défaut | Description |
|--------|--------|------|--------|-------------|
| `accessToken` | C / P | string | env var | Token du bot (alias : `authorizationToken` ; masqué par `RedactURI`) |
| `roomId` | P | string | `""` | ID de room cible par défaut (ID REST régional ou UUID brut Mercury) |
| `toPersonEmail` | P | string | `""` | Email du destinataire pour les messages directs |
| `toPersonId` | P | string | `""` | ID personne du destinataire pour les messages directs |
| `format` | P | string | `markdown` | Format du corps : `markdown` ou `text` |
| `baseURL` | P | string | `https://webexapis.com/v1` | URL racine de l'API REST Webex (proxies / tests) |
| `timeout` | P | duration | `15s` | Timeout par requête HTTP |
| `allowHeaderOverride` | P | bool | `false` | Opt-in : autorise les headers `CamelWebex*` à surcharger destination, `parentId` et `format` |
| `allowedDomains` | C | string | `""` | Domaines email expéditeurs autorisés séparés par des virgules (vide = tout le monde) |
| `allowedEmails` | C | string | `""` | Adresses email expéditeurs autorisées séparées par des virgules (vide = tout le monde) |
| `ignoreSelfMessages` | C | bool | `true` | Ignore les messages émis par le bot lui-même (anti-boucle) |
| `ignoreEmptyMessages` | C | bool | `true` | Ignore les messages sans texte (`false` pour recevoir les messages fichiers sans texte) |
| `maxConcurrency` | C | int | `10` | Nombre maximal de workers parallèles pour les messages entrants |
| `shutdownTimeout` | C | duration | `10s` | Timeout de `Disconnect()` lors de `Stop()` |
| `pingInterval` | C | duration | `15s` | Intervalle de ping WebSocket Mercury |
| `pongTimeout` | C | duration | `14s` | Timeout de pong Mercury (doit être `< pingInterval`) |
| `reconnectBackoffMax` | C | duration | `32s` | Backoff maximal de reconnexion Mercury |
| `maxReconnectAttempts` | C | int | `10` | Nombre maximal de tentatives consécutives de reconnexion |

**En-têtes :**

- **Entrants (Consumer) :** `CamelWebexRoomId`, `CamelWebexMessageId`, `CamelWebexParentId`, `CamelWebexPersonId`, `CamelWebexPersonEmail`, `CamelWebexRoomType`, `CamelWebexHtml`, `CamelWebexCreated`, `CamelWebexFiles`, `CamelWebexMentionedPeople`, `CamelWebexMentionedGroups` (posés uniquement s'ils ne sont pas vides).
- **Surcharges sortantes (Producer, nécessite `allowHeaderOverride=true`) :** `CamelWebexRoomId` > `CamelWebexToPersonEmail` > `CamelWebexToPersonId`, ainsi que `CamelWebexParentId` (rooms uniquement) et `CamelWebexFormat`. Lorsque Webex renvoie l'ID du message créé, `CamelWebexMessageId` est posé sur `Out` sans altérer `In.Body`.

Les UUID bruts Mercury de room sont résolus automatiquement vers leur véritable ID REST régional par pagination `GET /rooms` avec un cache mémoire borné (10 000 entrées max, sans jamais coder en dur le cluster `us`), et les `parentId` bruts sont encodés avec le même cluster régional. Les liens de pagination `Link` sont vérifiés sur la même origine que `baseURL`, les redirections 3xx sont bloquées côté Producer, les réponses sont bornées à `DefaultMaxBodySize`, et les tokens sont masqués dans les URIs, journaux et erreurs.

---

### NATS

Messagerie asynchrone orientée événements avec NATS. Publisher et subscriber haute performance avec le client Go NATS.

```go
import "github.com/tranchida/gocamel/components/nats"

ctx.AddComponent("nats", nats.NewNATSComponent())

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
exchange.In.SetHeader(nats.CamelNATSSubject, "urgent-orders")
```

> [!WARNING]
> Lors de l'écriture des URIs, utilisez toujours des doubles barres obliques (ex. `nats://orders`) au lieu de `nats:orders`, sinon `url.Parse` traite l'URI comme une URI opaque, ce qui contamine la chaîne du sujet avec les paramètres de requête.

| Option   | Type   | Défaut                  | Description                                    |
|----------|--------|-------------------------|------------------------------------------------|
| `servers` | string | `nats://127.0.0.1:4222` | Adresses des serveurs NATS (séparées par virgules) |
| `subject` | string | parsé depuis le chemin  | Sujet NATS pour la subscription/publication    |

**Headers d'Exchange :**

- `CamelNATSSubject` - Sujet du message reçu ou sujet cible pour la publication.

Les identifiants intégrés dans `servers` (tels que `user:pass@` ou `token@`) sont masqués dans `URI()` et les journaux. Les connexions sont établies sans verrou avec prise en charge du contexte tout en préservant la reconnexion automatique après le démarrage.

### Kafka

Diffusion d'événements distribuée à haut débit avec Apache Kafka via `segmentio/kafka-go` (pur Go).

```go
import "github.com/tranchida/gocamel/components/kafka"

ctx.AddComponent("kafka", kafka.NewKafkaComponent())

// Consommateur avec consumer group
builder.From("kafka:orders?brokers=localhost:9092&groupId=billing-service").
    LogSimple("Message reçu : ${body}")

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
| `maxRedeliveries` | int | `10` | Consommateur : nombre de redélivraisons d'un message dont la route échoue avant de le journaliser et de l'acquitter malgré tout (`-1` = réessai sans fin, `0` = acquittement dès le premier échec) |
| `redeliveryDelay` | int | `100` | Consommateur : délai de la première redélivraison en ms, doublé à chaque tentative |
| `maxRedeliveryDelay` | int | `30000` | Consommateur : plafond du backoff de redélivraison en ms |

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
>
> Les headers de record entrants nommés `Camel*` (insensible à la casse) sont
> ignorés par le consommateur : quiconque peut écrire dans le topic ne doit
> pas piloter des headers internes comme `CamelFileName` ou `CamelHttpMethod`.

Quand un `groupId` est configuré, chaque message traité avec succès est
committé auprès du groupe de consommateurs. Un échec de commit est journalisé
(avec topic/partition/offset) plutôt qu'ignoré silencieusement : le message
sera redélivré après un rééquilibrage ou un redémarrage, et la ligne de journal
est le seul signal de ce traitement dupliqué.

---

### STOMP

Production et consommation de messages avec tout broker compatible STOMP 1.0/1.1/1.2 — ActiveMQ, ActiveMQ Artemis, RabbitMQ, Apollo — via le client `go-stomp`. Idéal pour les destinations JMS de type file (`/queue/...`) et topic (`/topic/...`).

```go
import "github.com/tranchida/gocamel/components/stomp"

ctx.AddComponent("stomp", stomp.NewStompComponent())

// Consommateur : souscription à une file ActiveMQ
builder.From("stomp://queue/orders?host=localhost:61613&ack=client-individual").
    LogSimple("Message reçu : ${body}")

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
| `login` | string | `""` | Identifiant envoyé dans la trame CONNECT STOMP (se replie sur `username` et l'userinfo de l'URI) |
| `passcode` | string | `""` | Mot de passe envoyé dans la trame CONNECT STOMP (se replie sur `password` et l'userinfo de l'URI ; préférez `STOMP_PASSCODE`) |
| `ack` | string | `auto` | Mode d'accusé de réception : `auto`, `client`, `client-individual` (consommateur uniquement) |
| `allowHeaderOverride` | bool | `false` | Autorise `CamelStompDestination` à remplacer l'URI en production |
| `forwardHeaders` | string | `""` | Liste de noms de headers (séparés par des virgules) propagés comme headers de trame STOMP ; si définie, SEULS ces headers sont propagés |
| `maxRedeliveries` | int | `10` | Consommateur : nombre de redélivraisons d'un message dont la route échoue avant de le journaliser et de l'acquitter malgré tout (`-1` = réessai sans fin, `0` = acquittement dès le premier échec) |
| `redeliveryDelay` | int | `100` | Consommateur : délai de la première redélivraison en ms, doublé à chaque tentative |
| `maxRedeliveryDelay` | int | `30000` | Consommateur : plafond du backoff de redélivraison en ms |
| `connectTimeout` | durée | `30s` | Délai maximal de la connexion TCP et des handshakes TLS et STOMP. Le consommateur se reconnecte et se réabonne (backoff de 100 ms à 5 s) en cas de perte de connexion au broker ou de frame ERROR |
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
>
> Les headers de trame entrants nommés `Camel*` (insensible à la casse) sont
> ignorés par le consommateur : un client qui envoie vers la destination ne
> doit pas piloter des headers internes comme `CamelFileName` ou
> `CamelHttpMethod`.
>
> 19 headers de protocole STOMP 1.0/1.1/1.2 réservés (tels que `destination`,
> `content-length`, `receipt`, `ack`, `id`, `transaction`) et les octets `\x00`
> de cadrage dans les destinations, hôtes, identifiants ou headers sont bloqués
> pour empêcher l'injection de trames.

Le consommateur utilise une connexion dédiée au broker et vide proprement les
acquittements en attente lors de l'arrêt. Avec `ack=client` ou `ack=client-individual`, un message n'est accusé reçu
qu'après que la route l'ait traité avec succès ; un échec de traitement est
retenté (voir `maxRedeliveries`) avant tout accusé de réception, le broker redélivre donc le message si
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
import "github.com/tranchida/gocamel/components/grpc"

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
| `insecure` | bool | `true` | Utiliser un transport texte en clair sans TLS ; à `false`, TLS 1.2+ est utilisé (`certFile`+`keyFile` requis côté consommateur ; CA système ou `caCert` côté producteur) |
| `caCert` | string | `""` | Chemin d'un certificat CA au format PEM approuvé pour la vérification du serveur quand `insecure=false` (producteur) |
| `certFile` | string | `""` | Chemin d'un certificat TLS serveur PEM requis par `GRPCConsumer` quand `insecure=false` (exige `keyFile`, validé par `IsSafePath`) |
| `keyFile` | string | `""` | Chemin d'une clé privée TLS serveur PEM requise par `GRPCConsumer` quand `insecure=false` (exige `certFile`, validé par `IsSafePath`) |
| `timeout` | Duration | `5s` | Délai maximal par appel RPC appliqué par `GRPCProducer` |
| `forwardHeaders` | string | `""` | Liste de noms de headers (séparés par des virgules) propagés comme métadonnées RPC sortantes ; si définie, SEULS ces headers sont propagés |
| `allowHeaderOverride` | bool | `false` | Autorise `CamelGrpcMethod` à remplacer la méthode configurée |

**Headers d'Exchange :**
- `CamelGrpcMethod` - Méthode gRPC invoquée.
- `CamelGrpcStatusCode` - Code d'état gRPC (`0` pour OK ; côté consommateur, les valeurs `> 0` fixent le code d'erreur gRPC retourné).
- `CamelGrpcStatusDescription` - Message descriptif du statut gRPC (assaini dans les réponses d'erreur du consommateur).

> ⚠️ Sécurité : `GRPCConsumer` rejette `insecure=false` sans `certFile` et
> `keyFile`, lit la réponse finale via `exchange.GetResponse()` et assainit les
> détails d'erreur internes. `GRPCProducer` supprime les en-têtes `Camel*` et
> les identifiants sensibles (`Authorization`, `Cookie`, `Proxy-Authorization`,
> `Set-Cookie`, `WWW-Authenticate`) des métadonnées RPC sortantes. Définissez
> `forwardHeaders` pour une liste explicite si vous souhaitez un contrôle plus
> strict.

---

### Redis

Envoi de commandes (SET, GET, DEL, PUBLISH) ou abonnement aux canaux Redis Pub/Sub.

```go
import "github.com/tranchida/gocamel/components/redis"

ctx.AddComponent("redis", redis.NewRedisComponent())

// Consumer : s'abonner à un canal Pub/Sub
builder.From("redis://localhost:6379?subscribe=true&channel=mychannel").
    LogSimple("Message Pub/Sub reçu : ${body}")

// Producer : exécuter des commandes
builder.To("redis://localhost:6379?command=SET&key=mykey")
```

**Commandes supportées :**

- `SET` : Enregistre le corps de l'exchange (`string`, `[]byte` ou scalaire formaté) comme valeur de `key` (binaire sûr pour `[]byte`).
- `GET` : Récupère la valeur de `key` et l'écrit dans `Out.Body`. En cas d'absence (`redis.Nil`), `In.Body` et `Out.Body` sont mis à `nil`.
- `DEL` : Supprime `key` et écrit le nombre de clés affectées dans `Out.Body`.
- `PUBLISH` : Publie le corps de l'exchange (binaire sûr pour `[]byte`) vers le canal/clé spécifié.

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

    goredis "github.com/redis/go-redis/v9"
    "github.com/tranchida/gocamel/components/redis"
)

rdb := goredis.NewClient(&goredis.Options{Addr: "localhost:6379"})
// Préfixe de clé "idempotent:" (non vide) avec TTL d'expiration de 24 heures ; Clear échappe les caractères glob
repo := redis.NewRedisIdempotentRepository(rdb, "idempotent:", 24*time.Hour)

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
import "github.com/tranchida/gocamel/components/openai"

ctx.AddComponent("openai", openai.NewOpenAIComponent())

// Producer uniquement - envoyer une requête de chat completion (lit OPENAI_API_KEY automatiquement)
endpoint, _ := ctx.CreateEndpoint("openai:chat?model=gpt-4")
producer, _ := endpoint.CreateProducer()
_ = producer.Start(context.Background())
defer producer.Stop()

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
        "&apiKey=" + url.QueryEscape(os.Getenv("OPENROUTER_API_KEY")))
```

**Variables d'environnement :**

- `OPENAI_AUTHORIZATIONTOKEN` ou `OPENAI_API_KEY` - Clé API

| Option               | Type   | Défaut               | Description                                                     |
|----------------------|--------|----------------------|-----------------------------------------------------------------|
| `model`              | string | `gpt-3.5-turbo`      | Modèle à utiliser                                               |
| `authorizationToken` | string | variable d'env       | Clé API (alias : `apiKey`)                                      |
| `baseURL`            | string | `https://api.openai.com/v1` | URL de base compatible OpenAI personnalisée (OpenRouter, Groq, ...). Doit être `http(s)` avec hôte. |

Le timeout HTTP du SDK est de **60 secondes**, refuse les redirections 3xx susceptibles de faire fuiter les en-têtes d'autorisation et borne les corps de réponse à `DefaultMaxBodySize` (32 Mio). Les corps `nil` sont rejetés. Bornez davantage les appels longs avec `exchange.Context`.

### Anthropic

Intégration native de l'API Messages d'Anthropic (Claude). Contrairement au
composant OpenAI, c'est un connecteur dédié à la forme native de l'API
Claude — paramètre `system` séparé, blocs de contenu, en-têtes de cache de
prompt et raisons d'arrêt spécifiques.

```go
import "github.com/tranchida/gocamel/components/anthropic"

ctx.AddComponent("anthropic", anthropic.NewAnthropicComponent())

// Producer uniquement - envoyer une requête Messages (lit ANTHROPIC_API_KEY automatiquement)
endpoint, _ := ctx.CreateEndpoint(
    "anthropic:messages?model=claude-sonnet-4-5" +
        "&maxTokens=1024" +
        "&system=Tu+es+un+assistant+concis")
producer, _ := endpoint.CreateProducer()
_ = producer.Start(context.Background())
defer producer.Stop()

exchange := gocamel.NewExchange(context.Background())
exchange.GetIn().SetBody("Explique EIP en une phrase.")
producer.Send(exchange)

fmt.Println(exchange.GetOut().GetBody()) // Réponse de Claude
```

**Variables d'environnement :**

- `ANTHROPIC_API_KEY` - Clé API (repli quand `apiKey`/`authorizationToken` sont omis ; le chargement automatique d'environnement du SDK tel que `ANTHROPIC_BASE_URL` est désactivé via `option.WithoutEnvironmentDefaults()`)

| Option               | Type   | Défaut      | Description                                                    |
|----------------------|--------|-------------|----------------------------------------------------------------|
| `apiKey`             | string | variable d'env | Clé API (alias : `authorizationToken`). Masquée par `RedactURI`. |
| `model`              | string | requis      | Nom du modèle, ex. `claude-sonnet-4-5`                         |
| `maxTokens`          | int    | `1024`      | Nombre maximum de tokens à générer                             |
| `system`             | string | `""`        | Prompt système                                                |
| `temperature`         | float  | non défini  | Température d'échantillonnage (nombre fini dans `0.0-1.0`)    |
| `baseURL`            | string | API Anthropic | URL de base personnalisée (proxies, passerelles). Doit être `http(s)` avec hôte. |
| `allowHeaderOverride` | bool  | `false`     | Autorise les en-têtes de surcharge `Anthropic*` ci-dessous à remplacer la configuration de l'URI |

Le timeout HTTP du SDK est de **60 secondes**, refuse les redirections 3xx, borne les réponses à `DefaultMaxBodySize` (32 Mio) et rejette les corps `nil` ainsi que les réponses SDK nulles.

**En-têtes de surcharge par message** (message In, pris en compte uniquement avec `allowHeaderOverride=true`) :

| En-tête                   | Type   | Description                                |
|---------------------------|--------|--------------------------------------------|
| `AnthropicSystem`         | string | Surcharge le `system` de l'URI             |
| `AnthropicModel`          | string | Surcharge le `model` de l'URI               |
| `AnthropicMaxTokens`     | string | Surcharge le `maxTokens` de l'URI           |
| `AnthropicTemperature`    | string | Surcharge le `temperature` de l'URI         |

> ⚠️ Sécurité : le prompt système, le modèle, le budget de tokens et la
> température configurés dans l'URI sont de la configuration de confiance. Les
> en-têtes de surcharge sont ignorés sauf si l'endpoint l'autorise avec
> `allowHeaderOverride=true`. Ne l'activez que lorsque les en-têtes ne peuvent
> pas être influencés par une entrée non fiable : un émetteur pourrait sinon
> remplacer le prompt système (injection de prompt) ou choisir un modèle et un
> budget de tokens plus coûteux.

**En-têtes Out** (définis sur la réponse) :

| En-tête                       | Type  | Description                   |
|-------------------------------|-------|-------------------------------|
| `AnthropicUsageInputTokens`  | int64 | Tokens d'entrée facturés      |
| `AnthropicUsageOutputTokens`  | int64 | Tokens de sortie facturés     |
| `AnthropicStopReason`        | string | `end_turn`, `max_tokens`, ... |
| `AnthropicModel`              | string | Modèle ayant produit la réponse |

### Jev (API Decisions d'OpenRouter)

Producer pour le modèle de décision [TypeSafe Jev](https://openrouter.ai/typesafe)
via l'API Decisions d'OpenRouter. Jev lit le langage naturel comme un LLM mais
renvoie des **jugements typés** (probabilités, choix, scores) au lieu de texte
généré ; les seuils de décision restent dans votre route. Le module n'a aucune
dépendance tierce.

```go
ctx.AddComponent("jev", jev.NewJevComponent())

questions := `{"is_ironic":{"type":"noul","instructions":"Ce texte est-il ironique ?",
  "criteria":{"true":"ironique","false":"sincère"}}}`

route, _ := ctx.CreateRouteBuilder().
    From("direct:in").
    To("jev:decisions?questions=" + url.QueryEscape(questions)).
    ProcessFunc(func(e *gocamel.Exchange) error {
        answers := e.GetIn().GetBody().(map[string]jev.Answer)
        fmt.Printf("ironique : %.0f%%\n", answers["is_ironic"].Noul*100)
        return nil
    }).
    Build()
ctx.AddRoute(route)
```

Le corps de l'exchange est l'*état* à juger (string, `[]byte` ou toute valeur
sérialisable en JSON). Le corps Out est un `map[string]jev.Answer` indexé par
identifiant de question. `Answer.Type` vaut `noul` (renseigne `Noul`), `choice`
(`Choice`, `Probabilities`, `Confidence`) ou `score` (`Value`, `Legend`, `Confidence`).

**Variables d'environnement :**

- `OPENROUTER_API_KEY` - clé API (recommandé plutôt que de l'inclure dans l'URI)

| Option               | Type     | Défaut               | Description |
|----------------------|----------|----------------------|-------------|
| `apiKey`             | string   | variable d'env       | Clé API (alias : `authorizationToken`). Masquée par `RedactURI`. |
| `questions`          | JSON     | requis*              | Objet associant identifiants de questions et questions Jev, envoyé tel quel |
| `model`              | string   | `typesafe/jev-1.13`  | Modèle Jev |
| `baseURL`            | string   | `https://openrouter.ai/api/alpha` | Racine de l'API (proxies, tests). Doit être `http(s)` avec hôte. |
| `timeout`            | duration | `30s`                | Délai maximal par requête |
| `allowHeaderOverride` | bool    | `false`              | Autorise les en-têtes `Jev*` à remplacer la configuration de l'URI |

\* Facultatif avec `allowHeaderOverride=true` et l'en-tête `JevQuestions`.

Les corps `nil` sont rejetés, les redirections 3xx sont refusées et les corps de réponse HTTP sont plafonnés à `DefaultMaxBodySize` (32 Mio).

**En-têtes de surcharge** (message In, pris en compte uniquement avec `allowHeaderOverride=true`) :
`JevQuestions` (chaîne JSON ou map) et `JevModel`.

> ⚠️ Sécurité : les questions et le modèle sont une configuration de confiance.
> N'activez la surcharge que si les en-têtes ne peuvent pas être influencés par
> une entrée non fiable.

**En-têtes Out :** `JevModel` (modèle utilisé), `JevElapsed` (`time.Duration`, durée de l'appel).

### Ollama

Intégration native avec un serveur [Ollama](https://ollama.com) local via son
client Go officiel (`github.com/ollama/ollama/api`). Trois modes sont
sélectionnés par le chemin de l'URI (`ollama:mode` ou `ollama://mode`) :

- `ollama:chat` / `ollama://chat` (défaut) — API de chat multi-tours (`/api/chat`)
- `ollama:generate` / `ollama://generate` — génération one-shot (`/api/generate`)
- `ollama:embed` / `ollama://embed` — embeddings (`/api/embed`)

```go
import "github.com/tranchida/gocamel/components/ollama"

ctx.AddComponent("ollama", ollama.NewOllamaComponent())

// Discuter avec un modèle local
endpoint, _ := ctx.CreateEndpoint(
    "ollama:chat?model=llama3.1&host=http://localhost:11434")
producer, _ := endpoint.CreateProducer()
_ = producer.Start(context.Background())
defer producer.Stop()

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
| `host`       | string  | `http://localhost:11434`  | URL de base du serveur Ollama (doit être `http(s)` avec hôte) |
| `system`     | string  | `""`                      | Prompt système (mode generate uniquement)                   |
| `keepAlive`  | durée   | non défini                | Rétention du modèle en mémoire (ex. `5m`, `30s`)             |
| `think`      | string  | non défini                | Effort de raisonnement : `true`/`false`/`low`/`medium`/`high`/`max` (validé à la création) |
| `format`     | string  | non défini                | Format de sortie structuré (ex. `json`)                      |
| `allowHeaderOverride` | bool | `false`                | Autorise les en-têtes `OllamaModel` et `OllamaSystem` à remplacer la configuration de l'URI |

**En-têtes de surcharge par message** (message In, pris en compte uniquement avec `allowHeaderOverride=true`) :

| En-tête         | Type   | Description                                  |
|-----------------|--------|----------------------------------------------|
| `OllamaModel`   | string | Surcharge le `model` de l'URI                  |
| `OllamaSystem`  | string | Surcharge le `system` de l'URI (chat/generate) |

> ⚠️ Sécurité : le modèle et le prompt système configurés dans l'URI sont de la
> configuration de confiance. Les en-têtes de surcharge sont ignorés sauf si
> l'endpoint l'autorise avec `allowHeaderOverride=true`. Ne l'activez que
> lorsque les en-têtes ne peuvent pas être influencés par une entrée non
> fiable : un émetteur pourrait sinon remplacer le prompt système (injection de
> prompt) ou choisir un autre modèle.

**En-têtes Out** (définis sur la réponse) :

| En-tête                  | Type  | Description (chat/generate)         |
|-------------------------|-------|------------------------------------|
| `OllamaModel`           | string | Modèle ayant produit la réponse   |
| `OllamaDoneReason`      | string | `stop`, `length`, ...              |
| `OllamaTotalDuration`  | int64  | Durée totale (ns)                  |
| `OllamaPromptEvalCount` | int    | Tokens d'entrée évalués           |
| `OllamaEvalCount`       | int    | Tokens de sortie générés          |

**Ollama Cloud :** définissez la variable d'environnement `OLLAMA_API_KEY` et
pointez `host` vers `https://ollama.com`. Les requêtes sont alors authentifiées
par un jeton Bearer. La clé n'est lue que depuis l'environnement (jamais depuis
l'URI) et n'est envoyée qu'à `ollama.com` et à ses sous-domaines, en HTTPS
(`http://ollama.com` échoue au `Start`). Tout autre hôte, par exemple un serveur
Ollama local ou du réseau local, utilise le client standard et ne reçoit jamais
la clé, même si la variable est définie. Tous les clients HTTP Ollama refusent
les redirections 3xx et plafonnent les réponses à 32 Mio ; les messages d'erreur
cloud sont limités à 512 octets et les corps `nil` sont rejetés.

```go
// export OLLAMA_API_KEY=...
endpoint, _ := ctx.CreateEndpoint(
    "ollama:chat?model=gpt-oss:120b&host=https://ollama.com")
```

> Le composant Ollama **n'expose pas** les opérations de cycle de vie des
> modèles (Pull/List/Delete). Utilisez la CLI `ollama` ou l'API HTTP
> directement pour la gestion des modèles ; le producer GoCamel est limité
> à l'inférence.

---

## Composants d'Ordonnancement

### Cron

Ordonnancement avancé avec expressions cron ou intervalles simples.

```go
import "github.com/tranchida/gocamel/components/cron"

ctx.AddComponent("cron", cron.NewCronComponent())

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
| `cron`                   | string | `""`   | Expression cron à 6 champs (les préfixes `TZ=`/`CRON_TZ=` sans espace sont rejetés) |
| `trigger.timeZone`       | string | `""`   | Fuseau horaire IANA de l'expression cron (sans espace) |
| `trigger.repeatInterval` | int    | `""`   | Intervalle en ms (déclencheur simple) |
| `trigger.repeatCount`    | int    | `-1`   | Nombre maximum de répétitions (`-1` = infini, `0` = retour immédiat sans planification) |
| `triggerStartDelay`      | int    | `500`  | Délai initial en ms                  |
| `deleteJob`              | bool   | `true` | Supprimer le job lors de `Stop()` (`false` le conserve) |
| `pauseJob`               | bool   | `false`| Mettre en pause la planification au démarrage |
| `stateful`               | bool   | `false`| Empêcher l'exécution concurrente     |

**Headers d'Exchange :**

- `fireTime` - Heure d'exécution du déclencheur
- `nextFireTime` - Prochaine heure planifiée
- `triggerName` - Identifiant du déclencheur

À l'appel de `Stop()`, un job dont le `triggerStartDelay` n'est pas encore
écoulé n'est jamais enregistré sur le scheduler partagé : la demande d'arrêt
gagne la course avec l'enregistrement différé, aucun job orphelin ne peut donc
rester à se déclencher indéfiniment. Chaque consommateur suit son scheduler
`*cron.Cron` propriétaire et `Stop()` arrête proprement les schedulers inactifs.

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
| `allowHeaderOverride` | bool | `false`  | Autorise les en-têtes `From`/`To`/`Cc`/`Bcc`/`Subject`/`Content-Type` à surcharger l'URI |
| `requireStartTLS`     | bool | `false`  | Refuse l'envoi si le serveur n'annonce pas `STARTTLS` |

Les adresses sont validées strictement avec `net/mail` pour empêcher toute injection ESMTP. Une fois le message accepté par le serveur (`250 OK` après `DATA`), un échec de `QUIT` est journalisé sans relancer de réessai pour éviter les doublons.

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
| `peek`           | bool   | `true`   | Récupérer avec `BODY.PEEK[]` et ne marquer `\Seen` qu'après succès |
| `delete`         | bool   | `false`  | Supprimer après traitement (`UIDExpunge`) |
| `fetchSize`      | int    | `-1`     | Messages par interrogation |
| `pollDelay`      | int    | `60000`  | Intervalle d'interrogation (ms ; une valeur non positive retombe sur `60000` ms / `60s`) |
| `maxMessageSize` | int    | `33554432` | Taille max d'un message en octets (`DefaultMaxBodySize`, 32 Mio si `<= 0`) |

IMAP récupère les messages via `BODY.PEEK[]` (préservant `\Seen` en cas d'échec de la route) et applique une lecture en flux bornée à `maxMessageSize`. Le décodage MIME prend en charge les encodages non UTF-8 via `go-message/charset` et assainit les noms de pièces jointes dans `Content-Disposition`.

---

### POP3/POP3S (Réception)

Réception d'emails via POP3.

```go
builder.From("pop3s://pop.gmail.com:995?username=user&password=pass")
```

Le consumer POP3 accepte la même option `maxMessageSize` qu'IMAP (défaut
`DefaultMaxBodySize`, 32 Mio si `<= 0`), vérifie la taille via `LIST` avant de
télécharger le message brut RFC 5322 par `RetrRaw`/MIME et envoie `QUIT`
proprement après `DELE` pour valider les suppressions.

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
    LogSimple("${body}")

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
| `query`               | string | requis       | Chaîne de requête SQL (query URI uniquement ; variables d'env ignorées) |
| `dataSourceRef`       | string | chemin hôte  | Nom de la datasource (query URI uniquement)         |
| `outputType`          | string | `SelectList` | `SelectList` ou `SelectOne` (query URI uniquement)  |
| `batch`               | bool   | `false`      | Mode d'exécution par lot (query URI uniquement)     |
| `transacted`          | bool   | `false`      | Encapsuler dans une transaction avec `defer tx.Rollback()` et mise à jour de `Out` après `tx.Commit()` (query URI uniquement) |
| `allowHeaderOverride` | bool   | `false`      | Autoriser `CamelSqlQuery` à remplacer la requête (query URI uniquement) |

**Paramètres de requête :**

Fournis via le header `CamelSqlParameters` ou le corps comme `[]any`.

> [!caution] La requête est une configuration de confiance
> Les options SQL (`query`, `dataSourceRef`, `outputType`, `batch`,
> `transacted`, `allowHeaderOverride`) sont lues **strictement depuis la query
> string de l'URI** et ignorent les variables d'environnement.
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
>
> Une instruction suivie d'une autre (`SELECT 1; DROP TABLE t`) est rejetée
> selon quatre dialectes lexicaux SQL (guillemets simples, guillemets doubles,
> backticks et chaînes dollar-quoted PostgreSQL, plus commentaires ligne/bloc).
> Selon la base, `\'` échappe ou non une apostrophe (MySQL : oui ; SQLite,
> PostgreSQL : non) ; un `;` hors littéral dans l'une ou l'autre lecture est
> donc rejeté : échappez les apostrophes avec `''`, accepté partout.


**Headers de sortie :**

- `CamelSqlRowCount` - Lignes retournées/affectées
- `CamelSqlColumnNames` - Noms des colonnes (SELECT)

**Corps du résultat :**

| Cas                     | Type de Out.Body           |
|-------------------------|----------------------------|
| `SELECT` + `SelectList` | `[]map[string]any`         |
| `SELECT` + `SelectOne`  | `map[string]any` ou `nil` (efface `In.Body` et `Out.Body` à `nil` si 0 ligne correspond) |
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

Exécution de procédures stockées avec une slice ordonnée `[]gocamel.StoredProcedureParam` (paramètres `IN`).

```go
import (
    "database/sql"
    _ "modernc.org/sqlite"
)

db, _ := sql.Open("mysql", "user:pass@/mydb")

sqlStored := gocamel.NewSQLStoredComponent()
sqlStored.RegisterDataSource("mydb", db)
ctx.AddComponent("sql-stored", sqlStored)

// Appel simple avec paramètres IN ordonnés
builder.From("direct:call").
    SetBody([]gocamel.StoredProcedureParam{
        {Name: "userId", Direction: gocamel.ParamDirectionIn, Value: 42},
    }).
    To("sql-stored://mydb?procedure=GET_USER_BY_ID").
    LogSimple("${body}")
```

**Format d'URI :**

```
sql-stored://datasourceName?procedure=NAME
sql-stored://logical?dataSourceRef=dsName&procedure=NAME
```

| Option                | Type   | Défaut       | Description                                                  |
|-----------------------|--------|--------------|--------------------------------------------------------------|
| `procedure`           | string | requis       | Nom de la procédure stockée (query URI uniquement ; variables d'env ignorées) |
| `dataSourceRef`       | string | chemin hôte  | Nom de la datasource (query URI uniquement)                  |
| `outputType`          | string | `SelectList` | `SelectList` ou `SelectOne` (query URI uniquement)           |
| `transacted`          | bool   | `false`      | Exécuter dans une transaction (query URI uniquement)         |
| `noop`                | bool   | `false`      | Mode test (pas d'exécution ; query URI uniquement)           |
| `allowHeaderOverride` | bool   | `false`      | Autoriser `CamelSqlStoredProcedureName` à remplacer le nom (query URI uniquement) |

> [!caution] Le nom de procédure est un identifiant de confiance
> Comme `sql`, `sql-stored` lit toutes les options d'endpoint strictement
> depuis la query string de l'URI (en ignorant les variables d'environnement).
> Le nom de procédure est inséré dans la requête `CALL` et n'est **jamais**
> interpolé avec les données de l'exchange. Le header
> `CamelSqlStoredProcedureName` est ignoré sauf si l'endpoint opte avec
> `allowHeaderOverride=true` ; dans ce cas, la valeur est validée contre un
> motif d'identifiant strict (`[A-Za-z_][A-Za-z0-9_.]*`). N'activez la
> surcharge que là où les headers ne peuvent pas être influencés par une entrée
> non fiable.

**Règles et directions des paramètres :**

Passez les paramètres dans l'ordre positionnel déterministe via `[]gocamel.StoredProcedureParam`. Les corps `map[string]any` non ordonnés sont **rejetés** pour éviter toute transposition aléatoire des arguments.

| Direction             | Statut                                                                   |
|-----------------------|--------------------------------------------------------------------------|
| `ParamDirectionIn`    | Supporté (paramètre positionnel d'entrée)                                |
| `ParamDirectionOut`   | Rejeté (`database/sql` ne fournit pas de récupération générique `sql.Out`) |
| `ParamDirectionInOut` | Rejeté (`database/sql` ne fournit pas de récupération générique `sql.Out`) |

Exemple :

```go
params := []gocamel.StoredProcedureParam{
    {Name: "tenantId", Direction: gocamel.ParamDirectionIn, Value: "acme"},
    {Name: "userId", Direction: gocamel.ParamDirectionIn, Value: 42},
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
| `maxResults` | int | Non | Nombre maximal de documents renvoyés par un `find` (défaut : `10000`, `0` = illimité). Au-delà, le résultat est tronqué, un avertissement est journalisé et `CamelMongoDbResultsTruncated` vaut `true` |

Par sécurité, `remove` refuse les filtres nuls ou vides. Définissez
`allowDeleteAll=true` explicitement uniquement si la suppression de toute la
collection est intentionnelle. De même, `update` refuse un filtre absent ou
vide (`nil`, `{}`, `bson.M` / `bson.D` / `bson.Raw` vides, chaîne vide, vérifié via `bson.Marshal`) : chacun
mettrait à jour tous les documents. Définissez `allowUpdateAll=true`
explicitement uniquement si c'est intentionnel.
Avec `allowHeaderOverride=true`, `CamelMongoDbCriteria` prime sur `body["filter"]` pour `update`/`remove`. Sur `findOne`, `mongo.ErrNoDocuments` remet `In.Body` et `Out.Body` à `nil` et fixe `CamelMongoDbResultTotal` à `0`.

> ⚠️ Sécurité : la base, la collection et l'opération configurées dans l'URI
> constituent une configuration de confiance. Les headers
> `CamelMongoDbDatabase`, `CamelMongoDbCollection`, `CamelMongoDbOperation` et
> `CamelMongoDbCriteria` sont ignorés sauf si l'endpoint active explicitement
> `allowHeaderOverride=true` (booléen strict). Ne l'activez que si les headers ne peuvent pas
> être influencés par une entrée non fiable.

#### Headers d'entrée

| Header                   | Mode  | Description                                |
|--------------------------|-------|--------------------------------------------|
| `CamelMongoDbDatabase`   | R/W   | Nom de la base de données                  |
| `CamelMongoDbCollection` | R/W   | Nom de la collection                       |
| `CamelMongoDbOperation`  | R/W   | Opération à exécuter                       |
| `CamelMongoDbCriteria`   | Écriture | Filtre/critère (`map[string]any`, `bson.M`, `bson.D` ou JSON ; prime sur `body["filter"]`) |
| `CamelMongoDbLimit`      | Écriture | Limite de résultats                        |
| `CamelMongoDbSkip`       | Écriture | Sauter N documents                         |
| `CamelMongoDbSort`       | Écriture | Ordre de tri (JSON, `map[string]any`, `bson.M`, `[]bson.M` ou `bson.D`) |

#### Headers de sortie

| Header                    | Description                       |
|---------------------------|-----------------------------------|
| `CamelMongoDbResultTotal` | Total des documents trouvés/affectés |
| `CamelMongoDbOid`         | ObjectID du document inséré       |
| `CamelMongoDbResultsTruncated` | `true` quand un `find` s'est arrêté à `maxResults` |

#### Exemple

```go
import (
    "context"

    "go.mongodb.org/mongo-driver/mongo"
    "go.mongodb.org/mongo-driver/mongo/options"
    "github.com/tranchida/gocamel/components/mongodb"
)

// Créer le composant
mongoComp := mongodb.NewMongoDBComponent()

// Se connecter
client, _ := mongo.Connect(context.Background(), options.Client().ApplyURI("mongodb://localhost:27017"))
conn := mongodb.CreateMongoDBConnection(client, "mongodb://localhost:27017", "mydb")
mongoComp.RegisterConnection("myconn", conn)

ctx.AddComponent("mongodb", mongoComp)

// Insertion (la période du timer est en millisecondes)
builder.From("timer:tick?period=5000").
    SetBody(map[string]any{"name": "test", "value": 123}).
    To("mongodb://myconn?database=mydb&collection=items&operation=insert")

// Requête avec filtre
builder.From("direct:search").
    SetHeader(mongodb.CamelMongoDbCriteria, map[string]any{"status": "active"}).
    SetHeader(mongodb.CamelMongoDbLimit, 10).
    To("mongodb://myconn?database=mydb&collection=items&operation=find")
```

---

## Composants de Transformation

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
| `contentCache`            | bool   | `false` | Mettre en cache le template compilé en mémoire (plafonné à 1024 entrées ; précompilé au `Start()`) |
| `allowTemplateFromHeader` | bool   | `false` | Autoriser la surcharge via le header `CamelTemplatePath` (confinée au répertoire du template de l'endpoint via `ValidatePathInDir`) |
| `encoding`                | string | `UTF-8` | Encodage du template (seul UTF-8 est pris en charge ; tout autre valeur échoue à la création de l'endpoint) |
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
| `allowedExecutables`  | string | `""`    | Noms de commandes simples, séparés par des virgules, qu'un header `CamelExecCommandExecutable` peut choisir en plus de l'exécutable de l'URI |
| `maxOutputSize`       | int    | `33554432` | Octets conservés de stdout et de stderr (32 Mio, `0` = illimité) ; le reste est ignoré |

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
> L'**exécutable**, le **répertoire de travail** (`workingDir` / `CamelExecCommandWorkingDir`)
> et **`outFile`** sont validés contre les
> métacaractères shell, le path traversal et les évasions par liens symboliques. Un exécutable fourni par header
> doit en outre être un **nom de commande simple** (sans chemin, résolu via
> `PATH`), et soit l'exécutable de l'URI, soit l'un de ceux listés dans
> `allowedExecutables` ; tout autre est rejeté. Un nom simple ne suffisait pas :
> `sh` ou `python3` avec des arguments de header comme `-c ...` exécutent du
> code arbitraire, ne listez donc jamais un shell ni un interpréteur. Les **arguments ne le sont
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
- Header `CamelExecOutputTruncated` = `true` quand stdout ou stderr a dépassé
  `maxOutputSize` et a été tronqué (la commande s'exécute tout de même jusqu'au bout)

---

## Configuration des Composants

### Authentification

Les identifiants sont résolus par `gocamel.GetConfigValue`, utilisé par tous
les composants. Pour une URI comme `ftp://host` et la clé `password`, il
consulte dans l'ordre : la variable d'environnement exacte `password` (les
clés en majuscules comme un nom de clé API fonctionnent telles quelles), puis
la variable préfixée par le scheme `FTP_PASSWORD`, puis le paramètre de query
`password`, puis l'userinfo de l'URI (`user:pass@host`). La première valeur
non vide gagne : exporter `FTP_USERNAME` / `FTP_PASSWORD` suffit donc — rien
n'apparaît dans l'URI :

```go
// Les identifiants viennent des variables d'env FTP_USERNAME / FTP_PASSWORD
builder.From("ftp://host/incoming")

// Ou passés explicitement en paramètres de query (moins sûr : les URI
// fuient dans les logs d'erreur de connexion, et un WARN est émis pour les
// clés reconnues comme credentials)
builder.From("ftp://host/incoming?username=admin&password=s3cret")
```

Il n'y a pas d'interpolation `${env:...}` : un `${env:FTP_USER}` littéral
dans une URI serait envoyé au serveur comme nom d'utilisateur.

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
| Webex      | Messagerie   | ✅        | ✅        | `webex:bots`             |
| OpenAI     | IA           | ❌        | ✅        | `openai:chat`            |
| Anthropic  | IA           | ❌        | ✅        | `anthropic:messages`     |
| Ollama     | IA           | ❌        | ✅        | `ollama:chat`/`generate`/`embed` |
| Jev        | IA           | ❌        | ✅        | `jev:decisions`          |
| Cron       | Ordonnancement | ✅      | ❌        | `cron://group/job`       |
| SMTP       | Mail         | ❌        | ✅        | `smtp://host:port`       |
| IMAP       | Mail         | ✅        | ❌        | `imap://host:port`       |
| POP3       | Mail         | ✅        | ❌        | `pop3://host:port`       |
| SQL        | Base de données | ❌     | ✅        | `sql://datasource`       |
| SQL-Stored | Base de données | ❌     | ✅        | `sql-stored://datasource`|
| MongoDB    | Base de données | ❌     | ✅        | `mongodb:connectionName` |
| Template   | Transformation | ❌     | ✅        | `template:template`      |
| Exec       | Exécution    | ❌        | ✅        | `exec:command`           |
| NATS       | Messagerie   | ✅        | ✅        | `nats://subject`         |
| Redis      | Messagerie   | ✅        | ✅        | `redis://host:port`      |
| Go Channel | Core         | ✅        | ✅        | `chan:channelName`       |

## Détails de livraison et de cycle de vie

- **Direct :** `DirectProducer.Send` exige que le `DirectConsumer` cible soit démarré (`Start()`), et les transitions `Start`/`Stop` sont sérialisées.
- **Kafka / STOMP :** un traitement en échec est réessayé avec un nouvel échange avant la lecture du record (Kafka) ou du message (STOMP) suivant, avec un backoff exponentiel de `redeliveryDelay` jusqu’à `maxRedeliveryDelay`. Après `maxRedeliveries` redélivraisons (`10` par défaut, environ 80 s avec les délais par défaut), le consommateur abandonne le message : il le journalise en erreur puis le commite/acquitte pour ne pas bloquer indéfiniment la partition ou l’abonnement. Utilisez un `DeadLetterChannel` pour conserver ces messages, ou `maxRedeliveries=-1` pour réessayer jusqu’au succès de la route (le consommateur reste alors bloqué sur un message en échec permanent). Un message que la route a envoyé dans un `DeadLetterChannel` est commité/acquitté immédiatement et jamais redélivré, même avec `DeadLetterHandled(false)` : le redélivrer l’enverrait en double dans la DLQ à chaque tentative. Les producteurs Kafka utilisent l'équilibreur `&kafka.Hash{}` et valident les bornes de partition (`[0, math.MaxInt32)`). Les commits d'offset Kafka utilisent des contextes sûrs à l'arrêt et réessaient sans répéter un traitement réussi ; les erreurs de lecture réessaient jusqu'à la fermeture du reader. Le topic Kafka résolu est porté par chaque message ; les topics configurés et surchargés par en-tête fonctionnent ensemble.
- **FTP :** chaque producteur réserve sa connexion exclusivement pendant tout le transfert. Les envois concurrents et `Stop` ne peuvent pas entrelacer les commandes FTP ; un envoi en attente de réservation peut être annulé.
- **MongoDB :** `save` préserve le `_id` du document entrant, même si le remplacement échoue ; le retry cible ainsi le même document. `findOne` met `In.Body` et `Out.Body` à `nil` (`CamelMongoDbResultTotal=0`) lorsqu'aucun document ne correspond.
- **Mail :** IMAP IDLE réagit aux notifications de boîte en terminant IDLE, en recherchant les messages puis en reprenant IDLE. Une première recherche traite les messages déjà présents via `BODY.PEEK[]` et la suppression utilise `UIDExpunge` ; POP3 lit via `RetrRaw` et envoie `QUIT` après `DELE`.
- **Cron :** `Stop` empêche de nouvelles exécutions et attend les jobs actifs avant de retourner, y compris lors d’une pause ; les schedulers inactifs s'arrêtent proprement et chaque entrée suit son scheduler `*cron.Cron` propriétaire.
- **Redis :** les producteurs et consommateurs actifs partagent leurs clients, fermés au départ du dernier utilisateur. Arrêter un producteur laisse les autres utilisables ; les consommateurs arrêtés libèrent leur enregistrement pour permettre le redémarrage de la route.
- **File :** une URI terminée par `/` désigne un répertoire même s’il n’existe pas encore ; `CamelFileName` est obligatoire pour écrire dans un répertoire et confiné à l'intérieur.
- **Net :** le timeout configuré borne les écritures établies ainsi que la lecture des réponses, et les tampons de lecture TCP entrants sont copiés par message (sans aliasing de slice). Annuler l’échange ferme sa connexion et interrompt ces opérations.
- **Prometheus :** des appels répétés à `Metrics()` ou `MetricsWith(registry)` réutilisent les collecteurs compatibles déjà enregistrés, ce qui permet des intercepteurs distincts sur plusieurs routes. Les définitions incompatibles échouent toujours à l’enregistrement.
- **Webex :** la livraison Mercury WebSocket est **au plus une fois (at-most-once)** (Webex acquitte les trames au niveau protocole avant le déchiffrement et l'exécution de la route), et **un seul consommateur actif doit tourner par jeton de bot** car l'enregistrement WDM partage un nom d'appareil unique. Les messages entrants sont traités en parallèle jusqu'à `maxConcurrency` avec confinement des panics (`ProcessSafely`) et déconnexion `Disconnect()` propre avant annulation du contexte des workers. Préférez un `To("webex:bots?allowHeaderOverride=true")` statique à `ToD` afin de conserver le cache régional de rooms par Producer ; pour rediriger un échange Webex entrant vers un DM via `CamelWebexToPersonEmail`, supprimez d'abord `CamelWebexRoomId` ou utilisez un endpoint dédié.
