# ADR-001 : Découpage de gocamel en core + modules composants

**Statut :** Accepté — implémenté (option B)
**Date :** 2026-07-02 (implémentation le même jour)
**Décideur :** Giampaolo Tranchida

> **Note d'implémentation.** L'option B a été réalisée en une seule passe
> (phases 0 à 2 fusionnées, aucune version n'ayant jamais été publiée).
> Écarts par rapport au texte ci-dessous :
>
> - `RouteInterceptor` reçoit la `*Route` en plus de `next`, pour que les
>   intercepteurs accèdent à l'ID de route (labels Prometheus, nom de span).
> - Helpers core exportés pour les composants externes : `LogDebug/Info/Warn/Error`,
>   `MatchFileName`, `ValidateRemotePath`, `ParseConnectTimeout`, `CompleteExchange`.
> - `file` (fsnotify) est parti intégralement dans `components/file` (pas de
>   variante polling dans le core).
> - `MarshalCSV`/`UnmarshalCSV` restent dans le DSL (stdlib, API à options) ;
>   seules les méthodes RSS dédiées ont été retirées au profit de
>   `Marshal(NewRSSDataFormat())`.
> - Les tests sql restent sur `mattn/go-sqlite3` (cgo, test-only, invisible
>   pour les consommateurs) — bascule éventuelle vers `modernc.org/sqlite`
>   laissée en suivi.
> - Chaque go.mod de composant porte un `replace ../..` temporaire, à retirer
>   par le script de release au premier tag du core (voir Actions).

## Contexte

gocamel est aujourd'hui un unique package Go plat (`gitlab.com/tranchida/gocamel`) : 152 fichiers, ~30 700 lignes, un seul `go.mod`. Ce `go.mod` requiert directement une vingtaine de bibliothèques lourdes : go-redis, nats.go, mongo-driver, pkg/sftp + x/crypto, jlaffaye/ftp, go-smb2, telegram-bot-api, go-openai, go-imap/go-pop3/go-message, prometheus/client_golang, opentelemetry, fsnotify, robfig/cron, mattn/go-sqlite3 (cgo, en test uniquement).

Un package plat a deux conséquences pour le consommateur, mesurées sur un `main` minimal qui n'utilise que `direct` + `http` (protocole de mesure en annexe) :

| Métrique (consommateur minimal) | Valeur actuelle |
|---|---|
| `require` indirects dans son `go.mod` | **47** |
| `go.sum` | **194 lignes / 79 modules** |
| Taille du binaire | **22,8 Mo** |
| Contenu du binaire | mongo, telegram, nats, openai, smb2, sftp, imap, prometheus, cron, fsnotify… tous **liés** (vérifié via `go tool nm`) |

Le second point est le plus surprenant : comme tout est dans un seul package, le linker ne peut rien élaguer — les initialisations au niveau package (ex. les compteurs `promauto` de `prometheus.go`) rendent tout le graphe atteignable. Un « hello world » d'intégration embarque le driver MongoDB et le client Telegram.

Forces en présence :

- **Aucun tag de version n'a encore été publié** : les changements de chemins d'import sont quasi gratuits aujourd'hui, et le resteront de moins en moins.
- Le pattern d'enregistrement est déjà sain : **aucun composant n'est enregistré par défaut**, pas de `func init()`, l'utilisateur appelle explicitement `ctx.AddComponent("redis", NewRedisComponent(...))`. Le découpage ne demande donc pas de refonte du mécanisme de composants.
- Mainteneur solo : le coût de maintenance de la solution compte autant que sa pureté.

## Inventaire : core vs composants à dépendances

### Core (compile avec la stdlib seule — validé expérimentalement)

Une copie du repo réduite à ces fichiers (73 fichiers non-test) **compile avec un `go.mod` sans aucun `require`**, moyennant deux stubs décrits dans « Points de couplage » :

| Groupe | Fichiers |
|---|---|
| Moteur | `context.go`, `route.go`, `route_builder*.go` (5), `exchange.go`, `message.go`, `pipeline.go`, `endpoint.go`, `registry.go`, `config.go`, `logger.go`, `utils.go`, `security.go`, `management.go`, `polling_options.go`, `dsl_processors.go` |
| EIPs (zéro dépendance) | `aggregator`, `aggregation_strategy`, `aggregation_repository` (+ `memory_*`), `choice`, `circuit_breaker`, `claim_check` (+ repos), `delay`, `dynamic_router`, `enricher`, `error_handler`, `filter`, `idempotent_consumer` (+ `memory_idempotent_repository`), `load_balancer`, `loop`, `message_history`, `multicast`, `recipient_list`, `resequencer`, `routing_slip`, `saga`, `splitter`, `throttle`, `transform`, `try_catch`, `wiretap` |
| Simple Language | `simple_language*.go` (8 fichiers) |
| DataFormats stdlib | `dataformat.go`, `csv_dataformat.go`, `rss_dataformat.go` (encoding/xml — **pas** de dépendance externe) |
| Composants stdlib-only | `direct`, `seda`, `chan`, `timer`, `http`, `exec`, `template`, `sql` / `sql_stored` (+ `sql_stored_parameters`, `sql_aggregation_repository` — `database/sql`, le driver est fourni par l'utilisateur), `file_filter` |

Nuances par rapport à l'hypothèse de départ :

- `file_component.go` n'est **pas** stdlib-only : il importe `fsnotify` (dépendance légère, seule dépendance transitive `x/sys`).
- `sql_component.go` et famille sont bien stdlib-only ; `mattn/go-sqlite3` (cgo) n'apparaît que dans les `_test.go`. Les dépendances de test ne se propagent pas aux consommateurs (élagage Go ≥ 1.17, vérifié : sqlite3 est absent du `go.mod` du consommateur minimal), mais elles imposent cgo pour `go test ./...` du repo lui-même.
- `rss_dataformat.go` est stdlib-only (`encoding/xml`) : le RSS n'est pas un problème de dépendances, seulement d'altitude d'API (voir plus bas).

### Composants / fichiers à dépendances externes (20 fichiers non-test)

| Fichier(s) | Dépendance directe | Poids indicatif du graphe |
|---|---|---|
| `redis_component.go`, `redis_idempotent_repository.go` | go-redis/v9 | léger |
| `nats_component.go` | nats.go | moyen (nkeys, nuid…) |
| `mongodb_component.go` | mongo-driver | **lourd** (scram, snappy, compress…) |
| `mail_component.go`, `mail_endpoint.go`, `mail_message.go`, `mail_producer.go`, `mail_mime.go`, `mail_consumer_imap.go`, `mail_consumer_pop3.go` | go-imap/v2, go-pop3, go-message (producer = `net/smtp` stdlib, mais la famille est cohésive) | moyen |
| `ftp_component.go` | jlaffaye/ftp | léger |
| `sftp_component.go` | pkg/sftp + x/crypto | moyen |
| `smb_component.go` | go-smb2 | léger |
| `telegram_component.go` | telegram-bot-api/v5 | léger |
| `openai_component.go` | go-openai | léger |
| `cron_component.go` | robfig/cron/v3 | léger |
| `file_component.go` | fsnotify | léger |
| `prometheus.go` | client_golang | **lourd** (protobuf, procfs…) |
| `otel.go` | otel + otel/trace | moyen |

### Points de couplage inverse (à traiter avant tout découpage)

1. **`route.go` → prometheus/otel** : `Route.Process` (route.go:153-166) appelle `metricsProcess` qui manipule directement les variables package `exchangesTotal` / `exchangesDuration` / `exchangesInFlight` définies dans `prometheus.go`, et `traceProcess` défini dans `otel.go`. C'est le seul couplage du moteur vers des dépendances lourdes. Les deux fonctions sont déjà de purs wrappers autour de `doProcess` → conversion naturelle en **chaîne d'intercepteurs injectables** (voir Impact API).
2. **`RouteBuilder.MarshalRSS()` / `UnmarshalRSS()`** (+ répliques sur `SplitDefinition`, `MulticastDefinition`, `PipelineDefinition`, `Route`) : pas de problème de dépendance (RSS = stdlib), mais le DSL générique `Marshal(dataFormat)` existe déjà (route_builder.go:279) — les méthodes dédiées par format sont une dette d'API qui ne passera pas à l'échelle (un futur `MarshalAvro` tirerait une dépendance dans le DSL core).
3. **`error_handler.go`** : le dead-letter channel crée son endpoint via `context.CreateEndpoint(uri)` → passe par le `Registry`, **aucun couplage de package**. Rien à faire.
4. **`RedisIdempotentRepository`** : implémentation Redis d'une interface core (`IdempotentRepository`). L'interface reste dans le core, l'implémentation part avec le composant redis. Même logique pour d'éventuels repos d'agrégation externes.
5. **`examples/`** fait partie du module : `examples/monitoring` (promhttp) et `examples/mail` contribuent au `go.mod` de la bibliothèque.
6. Anomalie mineure : `nats.go` et `go-redis` sont marqués `// indirect` dans le `go.mod` actuel alors qu'ils sont importés directement — un `go mod tidy` n'a pas suivi.

## Options considérées

### Option A — Sous-packages dans un module unique

Racine = core ; `components/redis`, `components/nats`, … = sous-packages du même module.

| Dimension | Évaluation |
|---|---|
| Complexité | **Faible** (déplacement de fichiers + renommage de package) |
| Versioning | Un seul tag pour tout — simple mais couplé |
| CI / outillage | Inchangé (`go test ./...`) |
| Expérience consommateur | Très bonne (voir mesures), avec résidus |

**Mesuré** (module publié sur un proxy local, consommateur n'important que le core) — c'est le point où l'intuition commune se trompe : grâce à l'élagage du graphe de modules (Go ≥ 1.17), le `go.mod` du consommateur reste **propre** (1 require, 0 indirect) et son `go.sum` tombe à **2 lignes**, car les requires du module gocamel ne s'imposent au consommateur que si les packages correspondants sont importés. Binaire : **7,9 Mo**.

**Résidus mesurés, néanmoins :**

- `go list -m all` du consommateur liste **43 modules**, dont mongo, telegram, openai — bruit pour les SBOM et scanners de conformité qui travaillent sur le graphe de modules plutôt que sur les imports.
- **Contraintes MVS** : les versions requises par gocamel s'imposent aux dépendances *partagées*. Test concret : un consommateur qui veut go-redis v9.7.0 (pour son propre usage, sans toucher au composant redis) obtient `go: removed gitlab.com/tranchida/gocamel` — les deux sont incompatibles alors que le code redis de gocamel n'est jamais compilé.
- `go get -u ./...` et les bots type dependabot/renovate proposent des mises à jour de dépendances que le consommateur n'utilise pas.
- Une CVE dans mongo-driver oblige à tagger une nouvelle version de **toute** la bibliothèque.

**Pros :** gain massif immédiat, zéro outillage nouveau, prépare mécaniquement l'option B.
**Cons :** les 4 résidus ci-dessus ; cadence de release unique pour core et composants.

### Option B — Multi-modules dans le même repo (style OpenTelemetry contrib, go-cloud)

Racine = module core **sans aucun require** ; chaque composant lourd a son propre `go.mod` qui requiert le core + sa bibliothèque.

| Dimension | Évaluation |
|---|---|
| Complexité | **Moyenne** (tags par module, compatibilité croisée core↔composants) |
| Versioning | Tags préfixés par module (`components/redis/v0.1.0`) ; le core peut atteindre v1 pendant que les composants itèrent en v0 |
| CI / outillage | Boucle sur les `go.mod` (ou matrix) ; `go.work` commité pour le confort des contributeurs |
| Expérience consommateur | **Optimale** : le graphe de modules du consommateur ne contient que ce qu'il requiert réellement |

**Mesuré** (module core seul) : `go.mod` consommateur 1 require / 0 indirect, `go.sum` 2 lignes, `go list -m all` = **2 modules**, binaire 7,9 Mo, aucune contrainte MVS parasite.

**Pros :** isolation totale (SBOM, MVS, mises à jour, CVE par composant) ; releases indépendantes ; le `go.mod` vide du core est un argument d'adoption vérifiable en un coup d'œil.
**Cons :** discipline de tags (un oubli = composant qui pointe sur un vieux core) ; PRs touchant core + composant nécessitent deux releases ordonnées ; `go.work` requis pour le dev local ; friction connue des monorepos multi-modules Go (le fix classique : script de release qui tagge tous les modules ensemble).

### Option C — Repos séparés

| Dimension | Évaluation |
|---|---|
| Complexité | **Élevée** (n repos, n CI, synchronisation) |
| Versioning | Totalement indépendant |
| Expérience consommateur | Identique à B |

**Rejeté** : pour un mainteneur solo, C n'apporte rien de plus que B côté consommateur (l'isolation par module est identique) et coûte beaucoup plus cher : drift entre repos, découverte des composants dégradée, refactorings cross-cutting en plusieurs MRs, duplication de CI. C ne se justifie que si des composants acquièrent des mainteneurs ou des cadences propres.

## Analyse des compromis

Le tableau qui résume tout (consommateur minimal `direct` + `http`) :

| Métrique | Aujourd'hui (plat) | A (sous-packages) | B (multi-modules) |
|---|---|---|---|
| `go.mod` : requires indirects | 47 | 0 | 0 |
| `go.sum` | 194 lignes / 79 modules | 2 lignes | 2 lignes |
| `go list -m all` (SBOM/audit) | 79 modules | **43 modules** | **2 modules** |
| Contraintes MVS parasites | oui | **oui** (prouvé avec go-redis v9.7.0) | non |
| Binaire | 22,8 Mo | 7,9 Mo | 7,9 Mo (−65 %) |
| Release/CVE par composant | non | non | oui |
| Coût outillage | — | ~0 | modéré (tags + go.work + CI en boucle) |

A capture ~90 % du bénéfice utilisateur pour ~10 % de l'effort. B élimine les résidus (SBOM, MVS, releases couplées) pour un coût d'outillage récurrent mais borné. Et surtout : **A est le préfixe exact de B** — la même arborescence, sans les `go.mod`. Il n'y a donc pas à choisir entre les deux, mais à décider jusqu'où aller, et quand.

## Décision

**Option B, atteinte en deux temps : réorganiser d'abord en sous-packages (A) dans une release, puis promouvoir les répertoires composants en modules (B) dans la suivante.** Profiter de l'absence de tags publiés pour faire la rupture de chemins d'import maintenant, sans package de compatibilité.

### Arborescence cible

```
gocamel/                          module gitlab.com/tranchida/gocamel  (0 require)
├── *.go                          moteur, EIPs, Simple Language, dataformats stdlib,
│                                 composants direct/seda/chan/timer/http/exec/template/sql
├── components/
│   ├── redis/     go.mod         + RedisIdempotentRepository
│   ├── nats/      go.mod
│   ├── mongodb/   go.mod
│   ├── mail/      go.mod         famille complète (smtp/imap/pop3/mime)
│   ├── ftp/       go.mod
│   ├── sftp/      go.mod
│   ├── smb/       go.mod
│   ├── telegram/  go.mod
│   ├── openai/    go.mod
│   ├── cron/      go.mod         (robfig/cron)
│   └── file/      go.mod         (fsnotify)
├── observability/
│   ├── prometheus/ go.mod
│   └── otel/       go.mod
├── examples/      go.mod         (sort du module core dès la phase 0)
└── go.work                       commité, pour le dev local et la CI
```

Convention : `package redis`, importé `gocamelredis "gitlab.com/tranchida/gocamel/components/redis"` en cas de collision. Les constructeurs existants (`NewRedisComponent`, …) sont conservés tels quels — la migration d'un utilisateur est un simple changement d'import, l'appel `ctx.AddComponent("redis", redis.NewRedisComponent(...))` reste identique.

Cas discutable — `file` : seule dépendance fsnotify (quasi stdlib). Alternative : garder `file` dans le core en mode polling (`polling_options.go` existe déjà) et proposer le watcher fsnotify comme option dans `components/file`. Non retenu pour l'instant (deux implémentations à maintenir) ; à réévaluer si des utilisateurs réclament `file` sans dépendance.

### Impact API

1. **Observabilité → intercepteurs injectables.** Le core gagne :

   ```go
   type RouteInterceptor func(next func(*Exchange) error) func(*Exchange) error

   func (r *Route) Intercept(i RouteInterceptor) *Route
   ```

   `Route.Process` déroule la chaîne autour de `doProcess`. `metricsProcess` et `traceProcess` étant déjà des wrappers de cette forme exacte, la réécriture est mécanique :

   ```go
   import gocamelprom "gitlab.com/tranchida/gocamel/observability/prometheus"

   route.Intercept(gocamelprom.Metrics())   // remplace route.EnableMetrics()
   route.Intercept(gocamelotel.Tracing())   // remplace route.EnableTracing()
   ```

   Les champs `metrics`/`tracing` de `Route` et les méthodes `EnableMetrics`/`EnableTracing` disparaissent du core (dépréciées en phase 0, supprimées en phase 1 — elles ne peuvent pas survivre au découpage puisque leur implémentation part dans d'autres modules).

2. **RSS** : conserver `RSSDataFormat` dans le core (stdlib), déprécier les méthodes DSL dédiées `MarshalRSS`/`UnmarshalRSS` (RouteBuilder, Route, Split/Multicast/Pipeline definitions) au profit du `Marshal(NewRSSDataFormat())` générique déjà présent. Motif : ne plus jamais ajouter de méthode par format au DSL.

3. **`RedisIdempotentRepository`** → `components/redis`. L'interface `IdempotentRepository` et l'implémentation mémoire restent core.

4. **Erreur handler, Registry, AddComponent** : aucun changement — le découplage par URI/scheme fait déjà le travail.

5. **Tests sql** : remplacer `mattn/go-sqlite3` (cgo) par `modernc.org/sqlite` (pur Go) dans les `_test.go`, ou déplacer ces tests dans un module de test dédié. Sans cela, `go test ./...` du core exige cgo alors que le code livré est stdlib-only.

### Plan de migration

**Phase 0 — préparation, dans le module actuel, non-cassante**
- Introduire `Route.Intercept` ; réécrire `prometheus.go`/`otel.go` comme intercepteurs ; `EnableMetrics`/`EnableTracing` délèguent et sont marquées `// Deprecated:`.
- Déprécier `MarshalRSS`/`UnmarshalRSS` au profit de `Marshal(DataFormat)`.
- `examples/go.mod` séparé ; `go mod tidy` (corrige les `// indirect` erronés de nats/redis).
- Tests sql → driver pur Go.

**Phase 1 — réorganisation (= option A), une rupture unique**
- Déplacer les 20 fichiers composants + leurs tests vers `components/<x>/` et `observability/<x>/` (toujours un seul module) ; supprimer les méthodes dépréciées.
- Tag `v0.<n>.0` + table de migration des imports dans le changelog. Pas de package de compatibilité : aucun tag n'ayant jamais été publié, le coût pour l'écosystème est nul — c'est maintenant ou jamais.

**Phase 2 — promotion en modules (= option B)**
- Un `go.mod` par répertoire composant (require core + la lib) ; `go.work` commité.
- CI : job Go qui boucle sur les `go.mod` (`find -name go.mod`) pour build/test/vet — la `.gitlab-ci.yml` actuelle ne fait que publier mkdocs, tout est à créer de toute façon.
- Script/Make target de release qui tagge core puis chaque module (`components/redis/v0.1.0`, …) pour éviter le drift.

**Phase 3 — stabilisation**
- Quand l'API du moteur est jugée stable : core `v1.0.0` ; les composants suivent à leur rythme.

Chaque phase livre de la valeur seule ; on peut s'arrêter après la phase 1 (option A) si la discipline multi-modules s'avère trop coûteuse, sans rien perdre du travail accompli.

## Conséquences

**Plus facile :**
- Adoption : `go.mod` du core vide, binaires 3× plus petits, plus aucun faux positif d'audit venant de composants inutilisés.
- Ajout de nouveaux composants : plus de débat « cette dépendance a-t-elle sa place ? » — chaque composant paie ses propres dépendances.
- Gestion des CVE : un patch de mongo-driver ne concerne que `components/mongodb`.

**Plus difficile :**
- Releases : tags multiples ordonnés (core avant composants) ; nécessite le script de release dès la phase 2.
- Contributions cross-module (changement d'API core utilisé par un composant) : deux releases coordonnées.
- Documentation : chaque page composant doit indiquer son chemin d'import et sa version.

**À revisiter :**
- Le sort de `file`/fsnotify (core polling vs module).
- Passage du core en v1 (phase 3) — critère : plus de changement cassant de `Route`/`Exchange`/`RouteBuilder` pendant N mois.
- Option C si un composant acquiert sa propre communauté de mainteneurs.

## Actions

1. [x] Valider cet ADR (et le choix « rupture sans compat »).
2. [x] Intercepteurs (`Route.Intercept`) + suppression `EnableMetrics`/`EnableTracing`/`MarshalRSS` + `examples/go.mod` + `go mod tidy`.
3. [x] Déplacement des fichiers vers `components/` et `observability/` (git mv, historique préservé).
4. [x] `go.mod` par composant, `go.work` commité, job CI Go bouclant sur les modules.
5. [x] README(.fr) et `docs/{en,fr}/{concepts,reference}.md` mis à jour (imports par module, intercepteurs).
6. [ ] Premier tag du core (`v0.1.0`), puis retirer les `replace ../..` des go.mod composants, pinner `require gitlab.com/tranchida/gocamel v0.1.0` et tagger chaque module (`components/redis/v0.1.0`, …) — à scripter.
7. [ ] (Optionnel) Basculer les tests sql vers `modernc.org/sqlite` pour supprimer l'exigence cgo de `go test`.

## Annexe — protocole de mesure

Consommateur minimal :

```go
package main

import (
	"fmt"
	gocamel "gitlab.com/tranchida/gocamel"
)

func main() {
	ctx := gocamel.NewCamelContext()
	ctx.AddComponent("direct", gocamel.NewDirectComponent())
	ctx.AddComponent("http", gocamel.NewHTTPComponent())
	fmt.Println(ctx)
}
```

- **Aujourd'hui** : `replace` vers le repo actuel, `go mod tidy` + `go build`. Résultat : 47 requires indirects, go.sum 194 lignes / 79 modules, binaire 22 820 962 octets ; `go tool nm` confirme la présence des symboles mongo/telegram/nats/openai/smb2/sftp/imap/prometheus.
- **Core seul (≈ option B)** : copie du repo sans les 20 fichiers à dépendances, plus un stub no-op pour les 3 métriques et `traceProcess` (≈ 15 lignes). `go mod tidy` produit un go.mod **sans aucun require** ; le consommateur build avec go.sum vide et un binaire de 7 923 059 octets.
- **Option A** : module simulé (core à la racine + sous-packages important chacun sa dépendance lourde), publié en `v0.1.0` sur un proxy `file://`. Le consommateur qui n'importe que la racine obtient go.mod 1 require / 0 indirect et go.sum 2 lignes ; mais `go list -m all` remonte 43 modules, et `go get github.com/redis/go-redis/v9@v9.7.0` répond `go: removed gitlab.com/tranchida/gocamel` (conflit MVS avec le require v9.19.0 du module, pourtant jamais compilé).
