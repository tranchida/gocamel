# Changelog

Toutes les modifications notables de ce projet seront documentées dans ce fichier.

Le format est basé sur [Keep a Changelog](https://keepachangelog.com/fr/1.0.0/),
et ce projet adhère à [Semantic Versioning](https://semver.org/lang/fr/spec/v2.0.0.html).

## [Unreleased]

### Ajouté

- **`Exchange.DeepCopy()` / `Message.DeepCopy()`** — dupliquent récursivement les types conteneurs mutables
  courants (`[]byte`, `[]any`, `map[string]any`, `[]string`) des bodies, headers et propriétés ; utilisés par le
  `WireTap` et le `Multicast` parallèle pour que les branches concurrentes ne partagent jamais un body mutable
- **Cycle de vie d'exchange cohérent** — tous les consumers (direct, seda, chan, timer, cron, http, file, ftp,
  sftp, smb, mail, redis, nats, telegram) déclenchent désormais les callbacks `Synchronization`
  (`OnComplete`/`OnFailure`) à la fin du traitement ; `ErrStopRouting` est normalisé en succès
- **Option Exec `allowHeaderOverride`** — les surcharges par headers `CamelExecCommand*` sont désormais
  **opt-in** (désactivées par défaut) pour empêcher des headers non fiables de choisir le binaire exécuté

### Corrigé

- **Perte de messages de l'Idempotent Consumer** — la clé d'idempotence est retirée du dépôt quand l'exchange
  échoue en aval : les relivraisons sont traitées au lieu d'être silencieusement perdues (at-least-once)
- **Garde consumer du composant `chan`** — les endpoints sont mis en cache par nom de canal ; auparavant chaque
  `CreateEndpoint` retournait une nouvelle instance, permettant à plusieurs consumers de se disputer le même canal
- **Empoisonnement du lazy producer** — un échec transitoire à la création/démarrage d'un producer `To()`
  n'empoisonne plus la route définitivement ; l'initialisation est retentée au message suivant
- **Backoff exponentiel du DeadLetterChannel** — le délai croît dès la première relivraison
  (base × multiplicateur^tentative) ; l'attente respecte désormais l'annulation du contexte
- **Sondes half-open du Circuit Breaker** — une seule requête d'essai à la fois sonde le service aval ; les
  requêtes concurrentes partent en fallback au lieu de submerger le service en cours de rétablissement
- **Sémantique de routage du Resequencer** — les messages bufferisés/dupliqués/obsolètes arrêtent la route
  d'origine (`ErrStopRouting`) : les étapes placées après `End()` ne voient plus d'exchanges hors ordre ; les
  erreurs du drain de timeout sont loggées au lieu d'être avalées
- **Visibilité des compensations Saga** — les compensations en échec sont désormais loggées (index et erreur
  d'origine) au lieu d'être silencieusement ignorées
- **Nettoyage des ressources de `Route.Stop()`** — tous les producers et processors stoppables sont arrêtés même
  si l'un d'eux échoue ; les erreurs sont collectées et retournées combinées
- **Propagation headers-only du Pipeline** — un message `Out` ne portant que des headers (sans body) se propage
  entre les étapes sans effacer le body courant
- **Identité des endpoints `direct`** — les paramètres de query ne créent plus d'endpoints distincts
  (`direct:a` et `direct:a?x=1` désignent le même endpoint)
- **Arrêt & efficacité du WireTap** — les taps réutilisent le cache de producers partagé au lieu de créer un
  producer par message, et `Route.Stop()` attend les taps en vol au lieu de les perdre
- **Durcissement du consumer HTTP** — ajout d'un `ReadHeaderTimeout` de 10 s (slowloris) et d'un fallback `%v`
  pour les bodies de réponse non-string au lieu d'une réponse vide
- **Validation d'URI SEDA/chan** — les tailles de file négatives et les nombres de consumers non positifs
  retombent sur les valeurs par défaut avec un avertissement au lieu de paniquer ou d'être acceptés en silence

## [1.1.0] - 2026-05-24

### Ajouté

- **EIP Loop** — `.Loop(count)`, `.LoopSimple(expr)` et `.LoopFunc(fn)` permettent de répéter un segment de route de façon statique ou dynamique, en suivant l'index via les propriétés `CamelLoopIndex`, `CamelLoopSize` et `CamelLoopComplete`
- **EIP Saga** — Orchestrateur de transactions `.Saga()` qui coordonne les actions distribuées et leurs compensations avec un ordre d'annulation LIFO (via `.Action()` ou `.ActionFunc()`)
- **EIP Transform** — `.Transform(fn)` et `.TransformSimple(expr)` pour effectuer des transformations propres et dédiées du corps des messages
- **EIP Message History** — `.EnableMessageHistory()` retrace le parcours chronologique des messages à travers les nœuds de la route, inspectable via `gocamel.GetMessageHistory(exchange)`
- **Composant NATS** — `nats://subject` éditeur et abonné haute performance avec configuration des serveurs NATS
- **Composant Redis** — `redis://addr` prend en charge les commandes SET, GET, DEL, PUBLISH et les abonnements aux canaux Pub/Sub
- **Dépôt Idempotent Redis** — `NewRedisIdempotentRepository` implémentation distribuée de dépôt d'idempotence avec préfixage et TTL configurables
- **Composant Go Channel** — `chan:channelName` messagerie intra-processus asynchrone, rapide et thread-safe utilisant des canaux Go natifs
- **Optimisations du Moteur** — `ProducerCache` global et thread-safe dans le `CamelContext` et cache d'AST thread-safe pour les expressions Simple Language afin de maximiser le débit de traitement

### Ajouté

- **EIP Routing Slip** — `.RoutingSlip(expression)` route un exchange à travers une séquence dynamique d'endpoints ;
  supporte les chaînes séparées par des virgules, les expressions Simple Language et les fonctions Go
- **EIP Delay** — `.Delay(duration)` met l'exchange en pause pour une durée fixe ; `.DelayFunc(fn)` calcule la durée par
  exchange ; tous deux respectent l'annulation du contexte et échouent proprement si la fonction dynamique est nil
- **EIP Content Enricher** — `.Enrich(uri, stratégie)` récupère des données depuis un endpoint externe et les fusionne
  dans l'exchange via une `AggregationStrategy` ; passer `nil` pour remplacer directement le corps
- **EIP Throttle** — `.Throttle(maxRequests, période)` limite le débit ; les exchanges excédentaires sont retardés (
  jamais perdus) via un compteur à fenêtre fixe ; les limites invalides échouent rapidement et les exchanges en attente
  respectent l'annulation du contexte
- **EIP Message Filter** — `.Filter(expression)` supprime les exchanges ne correspondant pas à un prédicat Go ou à une
  expression Simple Language
- **`Exchange.PropagateOutToIn()`** — helper extrait qui copie le corps et les en-têtes de `Out` vers `In` ; utilisé en
  interne par `To`, `ToD` et `WireTap`
- **Améliorations Aggregator** — verrouillage par clé de corrélation via `sync.Map` (remplace le mutex global),
  `SetCompletionTimeout(d)` et `SetCompletionPredicate(fn)` comme stratégies de complétion
- **Durcissement DeadLetterChannel** — la relivraison rejoue désormais le processeur en échec avant de poursuivre la
  route, utilise des IDs d'exchange stables pour le suivi des tentatives, et nettoie l'état de retry après une
  relivraison réussie sans dépendre des callbacks `Exchange.Done()` des consumers
- Support transactionnel via **Unit of Work** et **Synchronization**
    - Nouvelle méthode `.Transacted()` dans le DSL RouteBuilder
    - Traitement transactionnel dans les composants `file`, `ftp`, `sftp`, `smb` et `mail` (post-traitement déclenché à
      la fin de la route)
- Documentation complète en anglais
- Structure de documentation bilingue (EN/FR)
- Support du thème MkDocs Material
- Nouveau fichier `security.go` avec utilitaires de validation de sécurité
    - `ValidatePath()` - valide les chemins de fichiers contre les traversées
    - `ValidatePathInDir()` - garantit que les chemins restent dans les répertoires autorisés
    - `IsSafePath()` - vérifie si un chemin est sûr
    - `SanitizeInput()` - supprime les caractères dangereux des entrées utilisateur

### Sécurité

- Corrigé les vulnérabilités de traversée de répertoire dans les composants FTP, SFTP et SMB
- Corrigé la vulnérabilité d'injection de commandes dans le composant Exec
- Corrigé les vulnérabilités d'injection SQL dans les composants SQL et SQL-Stored
- Ajout de l'assainissement des entrées pour les URI des composants
- Tous les composants valident désormais les chemins de fichiers avant les opérations

### Modifié

- **Traductions** : Tous les commentaires de code français traduits en anglais (650+ commentaires)

### Ajout

- Composant SQL (producer) pour exécuter des requêtes `SELECT`/`INSERT`/`UPDATE`/`DELETE` via `database/sql`, avec
  support des paramètres positionnels, `outputType` (`SelectList`/`SelectOne`), mode batch et transactions
- Composant Cron pour le scheduling avancé
- Nouveaux EIP : Stop, ToD (To Dynamic)
- Headers/Properties manipulation
- Management REST API
- Component XSLT pour transformations XML
- Component XSD pour validation XML

### Modifié

- Amélioration des performances du Multicast en mode parallèle
- Refactoring de l'architecture des endpoints

### Corrigé

- Gestion des erreurs dans le composant SFTP
- Leak de goroutines dans Timer

---

## [0.5.0] - 2026-03-15

### Ajout

- Composant OpenAI pour intégration LLM
- Composant Telegram pour bots
- EIP SetHeaders/SetHeadersFunc
- RemoveHeaders/RemoveProperties avec patterns wildcards

### Modifié

- Migration vers Go 1.21
- Optimisation de la mémoire dans Exchange pooling

---

## [0.4.0] - 2026-02-01

### Ajout

- Composant Exe pour exécution de commandes
- Composant Timer pour scheduling simple
- Composant Cron (beta)
- Support des Query Params dans les URIs Camel

### Modifié

- Réécriture complète du composant File
- Meilleure gestion des fichiers verrouillés

---

## [0.3.0] - 2026-01-10

### Ajout

- EIP Multicast avec support du parallélisme
- EIP Split avec propriétés CamelSplit*
- Composant SMB pour partages Windows
- Composant Direct pour routage synchrone in-memory

### Corrigé

- Race condition dans le composant HTTP

---

## [0.2.0] - 2025-12-01

### Ajout

- EIP Aggregate avec AggregationStrategy
- MemoryAggregationRepository
- SQLAggregationRepository (SQLite)
- Composant SFTP avec auth SSH key

### Modifié

- Breaking change: signature de SplitEIP

---

## [0.1.0] - 2025-11-01

### Ajout

- Core: CamelContext, Exchange, Message
- DSL RouteBuilder fluent
- Composants: HTTP, File, FTP
- EIP: Split (basique)
- Logging intégré

---

## Historique

```
0.1.0 ────────────── 1.1.0 ────────────── unreleased
  │                    │                      │
  └────────────────────┴──────────────────────┴──────▶ main
```
