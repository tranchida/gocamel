+++
title = "Concepts Fondamentaux"
weight = 10
+++
## Message

L'unité fondamentale d'échange contenant:

- **Body**: Le contenu du message (n'importe quel type)
- **Headers**: Métadonnées clé-valeur (map[string]any)

Les messages fournissent des accesseurs typés pour plus de commodité :

```go
body, _ := msg.GetBodyAsString()
count, _ := msg.GetHeaderAsInt("X-Count")
```

## Exchange

Conteneur pour les messages traversant une route:

- **In**: Message d'entrée (du consumer)
- **Out**: Message de sortie (vers le producer)
- **Properties**: Métadonnées liées à l'exchange
- **Context**: Contexte Go pour l'annulation

Les Exchanges relaient également les accesseurs typés vers le message **In** :

```go
body, _ := exchange.GetBodyAsString()
```

> [!warning] Thread-safety
> `Exchange` **n'est pas** sûr en utilisation concurrente. Les messages
> `In`/`Out` et la map `Properties` n'ont aucun verrouillage interne. Les
> EIPs qui font du fan-out vers plusieurs goroutines (`Multicast`,
> `Splitter`, `RecipientList` en mode parallèle) appellent
> `exchange.Copy()` une fois par branche **dans la goroutine appelante**
> avant de lancer les workers — l'exchange parent est alors read-only le
> temps du fan-out et redevient mutable une fois `wg.Wait()` retourné. Si
> vous écrivez un EIP personnalisé qui fan-out, suivez le même schéma.


> [!note] Copy vs DeepCopy
> `Copy()` duplique les **maps** de headers et de propriétés mais partage le
> body (et les valeurs de headers) **par référence**. Quand la copie s'exécute
> en parallèle de l'original — taps du `WireTap`, branches du `Multicast`
> parallèle — utilisez `DeepCopy()` : il duplique récursivement les types
> conteneurs mutables courants (`[]byte`, `[]any`, `map[string]any`,
> `[]string`). Les valeurs des autres types restent partagées ; traitez-les
> comme immuables entre goroutines.


## Processeur (Processor)

Une interface pour implémenter une logique personnalisée. Vous pouvez utiliser des instances directes, des closures ou
des références du registre.

```go
type MyProcessor struct {}
func (p *MyProcessor) Process(exchange *gocamel.Exchange) error {
    // logique personnalisée
    return nil
}

// Dans le RouteBuilder
builder.Process(&MyProcessor{})
builder.ProcessFunc(func(e *gocamel.Exchange) error { ... })
builder.ProcessRef("monBeanNomme")
```

### Récupération des panics

Un processeur qui panique **fait échouer son exchange, pas le processus**. Le
panic est récupéré, sa pile d'appels journalisée, puis converti en une erreur
enveloppant `gocamel.ErrProcessorPanic` — une erreur de route ordinaire, que
l'`ErrorHandler` configuré (redélivraison, dead letter) peut traiter :

```go
if errors.Is(err, gocamel.ErrProcessorPanic) {
    // l'exchange a échoué sur un panic plutôt que sur une erreur retournée
}
```

Cela vaut aussi bien pour le pipeline synchrone de la route que pour toutes les
goroutines détenues par le framework : workers SEDA et chan, branches
parallèles de Multicast et Recipient List, envois WireTap, timeouts de
complétion d'Aggregator, et les consumers de composants. Sans cela, une
écriture dans une map nil au sein d'un seul processeur emportait l'application
entière.

Si vous écrivez un `Consumer` personnalisé qui invoque un processeur depuis une
goroutine que vous gérez, appelez `gocamel.ProcessSafely` plutôt que `Process`
directement :

```go
err := gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange))
```

## Registre (Registry)

Un magasin clé-valeur central pour les objets nommés (Beans, Processeurs, Composants).

```go
context.GetComponentRegistry().Bind("monProcesseur", &MyProcessor{})
```

## Route

Une chaîne de processeurs qui traite un message:

```go
route := context.CreateRouteBuilder().
    From("direct:start").
    Process(processor).
    To("direct:end").
    Build()
```

## Endpoint

Ressource adressable par URI:

```
component://path?param=value
```

Exemples:

- `file:///tmp/data`
- `ftp://host:21/incoming`
- `http://localhost:8080/api`

## Context (CamelContext)

Conteneur d'exécution gérant:

- Cycle de vie des routes (démarrage/arrêt)
- Registre des composants
- Résolution des endpoints
- Gestion du pool de threads

```go
context := gocamel.NewCamelContext()
context.AddRoute(route)
context.Start()
context.Stop()
// Start après Stop est supporté : Stop installe un nouveau contexte racine.
```

### Arrêt propre (Graceful Shutdown)

`Stop()` n'a pas de délai limite : si le `Stop()` d'un consumer bloque (par
exemple un shutdown HTTP qui attend une connexion keep-alive inactive),
`context.Stop()` bloque avec lui. Après le teardown, il remplace le contexte
racine annulé pour qu'un `Start()` ultérieur puisse s'exécuter.
En production, préférez
`GracefulStop(timeout)` qui annule d'abord le contexte parent (pour que les
consumers qui observent `ctx.Done()` sortent immédiatement de leur boucle de
travail), puis attend jusqu'à `timeout` que toutes les routes terminent leur
arrêt. Les routes encore actives à l'échéance sont abandonnées et l'appel
retourne une erreur décrivant la situation ; le contexte racine est recréé
lorsque le teardown en arrière-plan se termine.

```go
// Borne l'arrêt à 30 secondes.
if err := context.GracefulStop(30 * time.Second); err != nil {
    log.Printf("arrêt incomplet : %v", err)
}
```

## Validation des routes

`Route.Validate() error` parcourt le pipeline au moment du build et signale
les problèmes qui n'apparaîtraient sinon qu'au premier message correspondant :

- l'erreur de construction accumulée par `From()` (ex. un scheme de composant
  inconnu),
- les erreurs de construction enregistrées sur les builders imbriqués `Split` /
  `Loop` / `Multicast` / `Pipeline` / `DoTry` (recopiées sur le parent à `End()`),
- l'absence d'endpoint source,
- les erreurs de validation des processors implémentant l'interface
  `Validator` (notamment `ChoiceProcessor`, dont les expressions Simple des
  clauses `When` sont re-parsées pour faire remonter les erreurs de syntaxe,
  et `Aggregator`, qui exige au moins une condition de complétion).

`Start()` **n'appelle pas** `Validate()` automatiquement — c'est un opt-in
destiné aux tests et à la CI pour échouer tôt sur des routes mal formées :

```go
route, err := context.CreateRouteBuilder().
    From("direct:start").
    Choice().
        When("${header.priority == 'high'}").To("direct:urgent").
        Otherwise().To("direct:normal").
    EndChoice().
    Build()
if err != nil {
    log.Fatal(err)
}
if err := route.Validate(); err != nil {
    log.Fatalf("route invalide : %v", err) // attraperait ici une expression When malformée
}
```

Les processors personnalisés peuvent se brancher sur le même mécanisme en
implémentant l'interface `Validator` :

```go
type Validator interface {
    Validate() error
}
```

## Monitoring (Observabilité)

GoCamel intègre nativement le support pour Prometheus et OpenTelemetry pour vous permettre de surveiller vos pipelines
en production.

### Prometheus (Métriques)

Vous pouvez exposer des métriques pour chaque route avec l'intercepteur du module
`gitlab.com/tranchida/gocamel/observability/prometheus` :
`.Intercept(gocamelprom.Metrics())`. Les métriques suivantes
sont collectées automatiquement :

- `gocamel_exchanges_total` : Nombre total de messages (avec labels `route_id` et `status`).
- `gocamel_exchange_duration_seconds` : Temps de traitement des messages.
- `gocamel_exchanges_inflight` : Nombre de messages en cours de traitement.

`Metrics()` enregistre les familles de métriques sur le registre Prometheus par
défaut. Les applications qui possèdent leur propre registre (collecteurs
personnalisés, push gateway) ou qui souhaitent des enregistrements isolés dans
les tests peuvent utiliser `.Intercept(gocamelprom.MetricsWith(reg))` avec
n'importe quel `prometheus.Registerer`.

Un `ErrStopRouting` enveloppé (wrapped) renvoyé par la route est un signal de
contrôle, pas un échec : il est compté comme `status="success"`, en cohérence
avec la sémantique du routeur principal.

### OpenTelemetry (Tracing)

Activez le traçage distribué avec l'intercepteur du module
`gitlab.com/tranchida/gocamel/observability/otel` :
`.Intercept(gocamelotel.Tracing())`. GoCamel créera un **Span** pour chaque exécution de route,
incluant :

- L'ID de la route et de l'échange.
- La propagation automatique du contexte de trace.
- L'enregistrement des erreurs si le traitement échoue.

```go
route, _ := context.CreateRouteBuilder().
    From("timer:tick?period=5000").
    Intercept(gocamelprom.Metrics()).
    Intercept(gocamelotel.Tracing()).
    To("http://api.service.com").
    Build()
```

## Component

Usine pour créer des endpoints d'un type spécifique:

```go
context.AddComponent("ftp", gocamel.NewFTPComponent())
context.AddComponent("http", gocamel.NewHTTPComponent())
```

## Unit of Work & Transactions

GoCamel supporte un modèle transactionnel basé sur le pattern **Unit of Work**. Cela garantit que la source du message (
par exemple, un fichier, un email ou un enregistrement de base de données) n'est marquée comme "consommée" qu'une fois
que toute la route a été traitée avec succès.

- **Synchronization**: Vous pouvez enregistrer des callbacks (`OnComplete`, `OnFailure`) sur un `Exchange` via
  `AddSynchronization`. **Tous** les consumers qui créent des exchanges (direct, seda, chan, timer, cron, http,
  file, ftp, sftp, smb, mail, redis, nats, telegram, ...) déclenchent ces callbacks à la fin du traitement de la
  route. Le signal de contrôle `ErrStopRouting` (Stop EIP, doublons idempotents, bufferisation d'agrégation) compte
  comme un **succès**, pas comme un échec.
- **Route Transactionnelle**: Marquez une route comme transactionnelle en utilisant `.Transacted()` dans le DSL.
- **Composants Transactionnels**: Les composants tels que `file`, `ftp`, `sftp`, `smb` et `mail` supportent ce modèle en
  retardant la suppression ou le déplacement du fichier source jusqu'à la fin de la route.

```go
context.CreateRouteBuilder().
    From("file:///data/in?move=.done&moveFailed=.error").
    Transacted(). // Active le comportement transactionnel
    To("http://api.service.com").
    Build()
```
