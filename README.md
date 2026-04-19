# GoCamel

GoCamel est une bibliothèque d'intégration d'entreprise inspirée d'Apache Camel, écrite en Go. Elle permet de créer des routes d'intégration pour connecter différents systèmes et services.

## Installation

```bash
go get github.com/tranchida/gocamel
```

## Fonctionnalités

- Architecture basée sur les routes et les endpoints
- Gestion des messages avec corps et en-têtes
- Contexte Camel pour la gestion du cycle de vie
- Pattern Builder pour la création de routes
- Support des EIP (Split, Aggregate, Multicast, Stop, ToD, SetHeader, SetHeaders, SetHeadersFunc, RemoveHeader, RemoveHeaders, SetProperty, SetPropertyFunc, RemoveProperty, RemoveProperties, **Choice**)
- **Simple Language** pour les expressions dynamiques (${body}, ${header.name}, fonctions, comparaisons)
- Fonctions de logging intégrées
- Gestion centralisée des identifiants (fichiers, query params, variables d'environnement)

## Simple Language

Le Simple Language permet d'utiliser des expressions dynamiques dans les routes GoCamel, inspiré d'Apache Camel.

### Capacités principales

- **Accès aux données** : `${body}`, `${header.name}`, `${exchangeProperty.prop}`
- **Functions intégrées** : `${date:now}`, `${random(100)}`, `${uuid}`
- **Comparaisons** : `${header.count > 10}`, `${body == 'active'}`
- **Accès null-safe** : `${body?.field?.subfield}`
- **Notation par crochets** : `${body['key']}`, `${body[0]}`

### Exemples d'utilisation

**Définir le corps et les en-têtes avec des expressions :**

```go
builder.From("direct:start").
    SimpleSetBody("Hello ${body} at ${date:now}").
    SimpleSetHeader("X-Request-ID", "${uuid}").
    To("direct:output")
```

**Routage basé sur le contenu (Choice) :**

```go
builder.From("direct:start").
    Choice().
        When("${header.priority == 'high'}").
            SimpleSetBody("🚨 HIGH: ${body}").
            To("direct:urgent").
        When("${body['count'] > 100}").
            To("direct:large-batch").
        Otherwise().
            To("direct:normal").
    EndChoice()
```

**Accès aux données complexes :**

```go
// Map : {"user": {"name": "John", "email": "john@example.com"}}
builder.From("direct:json").
    SimpleSetHeader("X-Name", "${body['user']['name']}").
    Log("Processing user: ${body?.user?.name}").
    To("direct:process")
```

Voir la [documentation complète](docs/simple-language.md) pour plus de détails.

## EIP (Enterprise Integration Patterns)

### Split EIP

Le Split EIP permet de diviser un message en plusieurs parties et de les traiter individuellement.

```go
builder.From("direct:start").
    Split(func(e *gocamel.Exchange) (any, error) {
        // Divise une chaîne par virgules
        body := e.GetIn().GetBody().(string)
        return strings.Split(body, ","), nil
    }).
    Log("Traitement de la partie : ${body}").
    To("direct:process-part").
    End(). // Fin du bloc Split
    Log("Traitement terminé pour tous les morceaux")
```

**Options du Split :**
- **`AggregationStrategy`** : Permet de combiner les résultats de chaque partie splitée pour mettre à jour le message original.
- **Propriétés de l'Exchange** : Chaque partie dispose de propriétés spécifiques :
    - `CamelSplitIndex` : Index actuel (0-based)
    - `CamelSplitSize` : Nombre total de parties
    - `CamelSplitComplete` : Booléen indiquant s'il s'agit de la dernière partie

### Aggregate EIP

L'Aggregator permet de combiner plusieurs messages en un seul selon une clé de corrélation.

```go
strategy := &MyAggregationStrategy{}
repo := gocamel.NewMemoryAggregationRepository()

builder.From("direct:start").
    Aggregate(gocamel.NewAggregator(correlationExpr, strategy, repo).
        SetCompletionSize(3)).
    Log("Message agrégé : ${body}")
```

### Multicast EIP

Le Multicast EIP envoie une copie du message à plusieurs destinations ou branches de processeurs.

```go
builder.From("direct:start").
    Multicast().
        Pipeline().
            Log("Branche 1 : ${body}").
            To("direct:out1").
        End().
        Pipeline().
            Log("Branche 2 : ${body}").
            To("direct:out2").
        End().
    End(). // Fin du bloc Multicast
    Log("Traitement terminé pour toutes les branches")
```

**Options du Multicast :**
- **`AggregationStrategy`** : Permet de combiner les résultats de chaque branche.
- **`ParallelProcessing`** : Active le traitement parallèle des branches (goroutines).
- **Propriétés de l'Exchange** : Chaque branche dispose de propriétés spécifiques :
    - `CamelMulticastIndex` : Index actuel (0-based)
    - `CamelMulticastSize` : Nombre total de branches
    - `CamelMulticastComplete` : Booléen indiquant s'il s'agit de la dernière branche

### Stop EIP

Le Stop EIP arrête le routage du message courant sans que cela soit considéré comme un échec.

```go
builder.From("direct:start").
    ProcessFunc(func(e *gocamel.Exchange) error {
        if e.GetIn().GetBody() == "stop" {
            return nil // condition à vérifier
        }
        return nil
    }).
    Stop().
    Log("Ce log ne sera jamais atteint")
```

### ToD (To Dynamic) EIP

Le ToD EIP permet d'envoyer un message vers un endpoint dont l'URI est résolue dynamiquement à l'exécution. Les expressions `${header.x}`, `${property.x}` et `${body}` sont interpolées dans le template d'URI.

```go
builder.From("direct:start").
    SetHeader(gocamel.CamelFileName, "output.txt").
    ToD("file://output/${header.CamelFileName}")
```

### Header/Property EIPs

GoCamel propose un ensemble d'EIP pour manipuler les en-têtes et les propriétés de l'Exchange :

**En-têtes :**
- `SetHeader(key, value)` / `SetHeaders(map)` / `SetHeadersFunc(fn)` : Définit les en-têtes du message de sortie.
- `RemoveHeader(name)` / `RemoveHeaders(pattern, excludePatterns...)` : Supprime des en-têtes du message d'entrée. Le pattern `*` est supporté comme joker, avec des patterns d'exclusion optionnels.

```go
builder.From("direct:start").
    SetHeaders(map[string]any{"X-Custom": "value", "X-Trace": "123"}).
    RemoveHeaders("X-Debug*", "X-DebugKeep").
    To("direct:out")
```

**Propriétés :**
- `SetProperty(key, value)` / `SetPropertyFunc(key, fn)` : Définit une propriété sur l'Exchange.
- `RemoveProperty(key)` / `RemoveProperties(pattern, excludePatterns...)` : Supprime des propriétés. Le pattern `*` est supporté comme joker, avec des patterns d'exclusion optionnels.

```go
builder.From("direct:start").
    SetProperty("correlationId", "abc-123").
    SetPropertyFunc("timestamp", func(e *gocamel.Exchange) (any, error) {
        return time.Now().UnixMilli(), nil
    }).
    RemoveProperties("temp*").
    To("direct:out")
```

## Exemples d'utilisation

### Exemple FTP (Téléchargement & Envoi)

Il est recommandé de passer les identifiants via les variables d'environnement pour plus de sécurité (ex: `FTP_USERNAME`, `FTP_PASSWORD`, ou directement `username` et `password`).

```go
package main

import (
    "github.com/tranchida/gocamel"
)

func main() {
    context := gocamel.NewCamelContext()
    context.AddComponent("ftp", gocamel.NewFTPComponent())

    // Écoute les nouveaux fichiers sur le serveur FTP (Consumer)
    // et les renvoie vers un autre répertoire (Producer)
    route := context.CreateRouteBuilder().
        From("ftp://localhost:21/incoming?delay=10s&delete=true").
        Log("Nouveau fichier téléchargé").
        LogHeaders("Métadonnées du fichier FTP").
        SetHeader(gocamel.CamelFileName, "processed_file.txt").
        ProcessFunc(func(exchange *gocamel.Exchange) error {
            // Traitement custom ici...
            return nil
        }).
        Build()

    // Ajout d'un Producer manuel à l'exécution si besoin:
    // endpoint, _ := context.CreateEndpoint("ftp://localhost:21/outgoing")
    // producer, _ := endpoint.CreateProducer()
    // producer.Send(exchange)

    context.AddRoute(route)
    context.Start()
    select {}
}
```

### Exemple Telegram

```go
package main

import (
    "github.com/tranchida/gocamel"
)

func main() {
    // Définir la variable d'environnement TELEGRAM_AUTHORIZATIONTOKEN="votre_token_bot"
    context := gocamel.NewCamelContext()
    context.AddComponent("telegram", gocamel.NewTelegramComponent())

    route := context.CreateRouteBuilder().
        From("telegram:bots").
        Log("Message Telegram reçu !").
        LogBody("Texte du message :").
        Build()

    context.AddRoute(route)
    context.Start()
    select {}
}
```

### Exemple OpenAI

L'envoi de requêtes s'effectue via un Producteur.

```go
package main

import (
    "fmt"
    "context"
    "github.com/tranchida/gocamel"
)

func main() {
    // Définir la variable d'environnement OPENAI_AUTHORIZATIONTOKEN ou OPENAI_API_KEY
    camelCtx := gocamel.NewCamelContext()
    camelCtx.AddComponent("openai", gocamel.NewOpenAIComponent())

    // Dans une route ou manuellement:
    endpoint, _ := camelCtx.CreateEndpoint("openai:chat?model=gpt-3.5-turbo")
    producer, _ := endpoint.CreateProducer()

    exchange := gocamel.NewExchange(context.Background())
    exchange.GetIn().SetBody("Bonjour, comment ça va ?")

    producer.Send(exchange)
    fmt.Println("Réponse OpenAI :", exchange.GetOut().GetBody())
}
```

### Exemple Quartz

```go
package main

import (
    "fmt"
    "github.com/tranchida/gocamel"
)

func main() {
    context := gocamel.NewCamelContext()
    context.AddComponent("quartz", gocamel.NewQuartzComponent())

    // CronTrigger : toutes les minutes (expression 6 champs, secondes incluses)
    route1 := context.CreateRouteBuilder().
        From("quartz://monGroupe/minutely?cron=0+*+*+*+*+*").
        ProcessFunc(func(exchange *gocamel.Exchange) error {
            fmt.Println("Déclenchement cron :", exchange.GetIn().Headers[gocamel.QuartzFireTime])
            return nil
        }).
        Build()

    // SimpleTrigger : toutes les 5 secondes (intervalles sub-secondes supportés)
    route2 := context.CreateRouteBuilder().
        From("quartz://poller?trigger.repeatInterval=5000&triggerStartDelay=0").
        ProcessFunc(func(exchange *gocamel.Exchange) error {
            fmt.Println("Tick toutes les 5s")
            return nil
        }).
        Build()

    context.AddRoute(route1)
    context.AddRoute(route2)
    context.Start()
    select {}
}
```

**Paramètres URI Quartz :**

| Paramètre | Description | Défaut |
|-----------|-------------|--------|
| `cron` | Expression cron 6 champs (espaces encodés en `+`) | — |
| `trigger.repeatInterval` | Intervalle en ms (SimpleTrigger) | — |
| `trigger.repeatCount` | Nombre max de déclenchements (`-1` = infini) | `-1` |
| `trigger.timeZone` | Timezone IANA (ex: `Europe/Paris`) — CronTrigger uniquement | — |
| `triggerStartDelay` | Délai en ms avant le premier déclenchement | `500` |
| `deleteJob` | Supprimer le job à l'arrêt | `true` |
| `pauseJob` | Mettre en pause au lieu de supprimer à l'arrêt | `false` |
| `stateful` | Empêcher les exécutions concurrentes (CronTrigger) | `false` |

**En-têtes posés sur chaque Exchange :**
`fireTime`, `scheduledFireTime`, `nextFireTime`, `previousFireTime`, `triggerName`, `triggerGroup`, `refireCount`

## Structure du projet

```
gocamel/
├── context.go              # Gestion du contexte Camel
├── exchange.go             # Structure d'échange de messages
├── message.go              # Structure de message
├── route.go                # Gestion des routes
├── route_builder.go        # Pattern Builder pour les routes (DSL)
├── registry.go             # Registre des composants
├── config.go               # Utilitaires de gestion des configurations environnementales
├── utils.go                # Utilitaires (interpolation, pattern-to-regex)
├── aggregator.go           # Implémentation de l'EIP Aggregate
├── splitter.go             # Implémentation de l'EIP Split
├── multicast.go            # Implémentation de l'EIP Multicast
├── pipeline.go             # Pipeline séquentiel (utilisé par Multicast)
├── aggregation_strategy.go # Interface pour les stratégies d'agrégation
├── aggregation_repository.go # Interface pour le stockage de l'agrégation
├── memory_aggregation_repository.go # Stockage d'agrégation en mémoire
├── sql_aggregation_repository.go   # Stockage d'agrégation SQLite
├── polling_options.go      # Options de polling partagées (FTP, SFTP, SMB)
├── file_filter.go          # Filtrage de noms de fichiers (include/exclude)
├── management.go           # API REST de management (JMX-like)
├── http_component.go       # Composant HTTP
├── file_component.go       # Composant File
├── ftp_component.go        # Composant FTP
├── sftp_component.go       # Composant SFTP
├── smb_component.go        # Composant SMB (Samba / Windows Share)
├── direct_component.go     # Composant Direct (routage en mémoire)
├── timer_component.go      # Composant Timer (minuterie simple)
├── telegram_component.go   # Composant Telegram Bot
├── openai_component.go    # Composant OpenAI
├── exec_component.go      # Composant Exec (exécution de commandes)
├── xslt_component.go      # Composant XSLT
├── xsd_component.go       # Composant XSD
└── quartz_component.go    # Composant Quartz (scheduler)
```

## Composants disponibles

- **HTTP** (`http://...`) : Serveur (Consumer) et Client (Producer).
- **File** (`file://...`) : Lecture et écriture de fichiers locaux.
- **FTP** (`ftp://...`) : Serveur FTP (Consumer & Producer).
- **SFTP** (`sftp://...`) : Serveur SFTP avec authentification SSH (Consumer & Producer).
- **SMB** (`smb://...`) : Partages réseau Windows/Samba (Consumer & Producer).
- **Direct** (`direct:...`) : Routage synchrone en mémoire entre routes (Consumer & Producer).
- **Timer** (`timer:...`) : Minuterie périodique simple (Consumer uniquement).
- **Telegram** (`telegram:...`) : Bot Telegram (Consumer webhook/polling & Producer).
- **OpenAI** (`openai:...`) : Chat Completion (Producer uniquement).
- **Exec** (`exec:...`) : Exécution de commandes système (Producer uniquement).
- **XSLT** (`xslt:...`) : Transformation XML via une feuille de style (Producer uniquement).
- **XSD** (`xsd:...`) : Validation de schéma XML (Producer uniquement).
- **Quartz** (`quartz://...`) : Déclenchement planifié par expression cron ou intervalle fixe (Consumer uniquement).

## Configuration

La plupart des paramètres sensibles (tokens, mots de passe) peuvent être passés de trois façons :
1. Dans l'URI directement (ex: `ftp://user:pass@host/path`).
2. En paramètre de requête (ex: `telegram:bots?authorizationToken=XXX`).
3. En variable d'environnement (ex: `FTP_PASSWORD=xxx`, `TELEGRAM_AUTHORIZATIONTOKEN=xxx` ou `OPENAI_API_KEY=xxx`). C'est la méthode recommandée.

## Licence

MIT

## Monitoring et Management REST (JMX-like)

GoCamel inclut une interface REST permettant de monitorer et de contrôler le cycle de vie des routes (démarrage/arrêt), inspirée du management JMX d'Apache Camel.

```go
package main

import (
    "github.com/tranchida/gocamel"
)

func main() {
    context := gocamel.NewCamelContext()

    route := context.CreateRouteBuilder().
        From("http://localhost:8080/hello").
        SetID("route-http-1").
        SetBody("Hello World").
        Build()

    context.AddRoute(route)
    context.Start()

    // Démarrer l'interface de management sur le port 8081
    mgmt := gocamel.NewManagementServer(context)
    mgmt.Start(":8081")

    select {}
}
```

### Endpoints de Management

- **État du contexte** : `GET /api/context`
  ```json
  {"started":true,"totalRoutes":1,"startedRoutes":1}
  ```

- **Lister les routes** : `GET /api/routes`
  ```json
  [{"id":"route-http-1","description":"","group":"","started":true}]
  ```

- **Arrêter une route** : `POST /api/routes/{id}/stop`
  ```bash
  curl -X POST http://localhost:8081/api/routes/route-http-1/stop
  ```

- **Démarrer une route** : `POST /api/routes/{id}/start`
  ```bash
  curl -X POST http://localhost:8081/api/routes/route-http-1/start
  ```
