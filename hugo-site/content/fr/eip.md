+++
title = "EIP (Enterprise Integration Patterns)"
weight = 13
+++
Les patterns d'intégration enterprise implémentés dans GoCamel.

## Split

Divise un message en plusieurs parties traitées individuellement.

```go
builder.From("direct:start").
    Split(func(e *gocamel.Exchange) (any, error) {
        body := e.GetIn().GetBody().(string)
        return strings.Split(body, ","), nil
    }).
    Log("Partie: ${body}").
    To("direct:process").
    End()
```

**Traitement parallèle :**

```go
builder.From("file:/data/in").
    Split(lines).ParallelProcessing(true).MaxConcurrency(20). // 10 par défaut
        To("http://api/items").
    End()
```

**Propriétés de l'Exchange:**

- `CamelSplitIndex` — Index actuel (0-based)
- `CamelSplitSize` — Nombre total de parties
- `CamelSplitComplete` — Dernière partie ?

> [!note] Ces propriétés sont limitées à la branche
> Elles décrivent une partie, pas le message entier, et sont **retirées au
> moment où le résultat agrégé est publié sur l'exchange parent**. Un
> processeur placé après le Split ne voit donc pas un `CamelSplitComplete=true`
> résiduel de la dernière partie, et un Split imbriqué n'écrase pas la
> comptabilité de celui qui l'englobe. La même règle s'applique à
> `CamelMulticast*`, `CamelRecipientList*` et `CamelRoutingSlip*`.


> [!warning] Un corps `[]byte` n'est pas une collection
> Découper un corps `[]byte` produit une **seule partie**, comme Apache Camel
> qui ne découpe pas les `byte[]`. Le traiter comme une liste produisait un
> exchange par octet — une charge utile de 10 Ko devenait 10 000 exchanges.
> Pour découper délibérément du contenu binaire, découpez sur une valeur
> décodée :
>
> ```go
> Split(func(e *gocamel.Exchange) (any, error) {
>     return strings.Split(string(e.GetBody().([]byte)), "\n"), nil
> })
> ```


**Avec agrégation :**

```go
type StringJoinStrategy struct{}

func (s *StringJoinStrategy) Aggregate(oldEx, newEx *gocamel.Exchange) *gocamel.Exchange {
    if oldEx == nil {
        return newEx
    }
    old := oldEx.GetIn().GetBody().(string)
    new := newEx.GetIn().GetBody().(string)
    oldEx.GetIn().SetBody(old + "," + new)
    return oldEx
}

strategy := &StringJoinStrategy{}

builder.From("direct:start").
    Split(splitter).Strategy(strategy).
        To("direct:process").
    End()
```

---

## Aggregate

Combine plusieurs messages en un seul.

Au moins une condition de complétion est obligatoire : `SetCompletionSize`,
`SetCompletionTimeout` ou `SetCompletionPredicate`. Sans cela, `Process`
retourne une erreur au lieu de bufferiser indéfiniment.

```go
strategy := &MyAggregationStrategy{}
repo := gocamel.NewMemoryAggregationRepository()

builder.From("direct:start").
    Aggregate(gocamel.NewAggregator(correlationExpr, strategy, repo).
        SetCompletionSize(3)). // Completer après 3 messages
    Log("Agrégation terminée: ${body}")
```

**Stratégie personnalisée:**

```go
type MyAggregationStrategy struct{}

func (s *MyAggregationStrategy) Aggregate(
    oldExchange, 
    newExchange *gocamel.Exchange
) *gocamel.Exchange {
    // Logique de fusion
    return oldExchange
}
```

**Conditions de complétion :**

| Méthode                      | Description                                    |
|------------------------------|------------------------------------------------|
| `SetCompletionSize(n)`       | Compléter après n messages                     |
| `SetCompletionTimeout(ms)`   | Compléter après un délai (timeout)             |
| `SetCompletionPredicate(fn)` | Compléter quand le prédicat retourne vrai      |

**Options de stockage :**

```go
// En mémoire (par défaut)
repo := gocamel.NewMemoryAggregationRepository()

// Persistance SQLite
repo := gocamel.NewSQLAggregationRepository(db, "schema")
```

Au démarrage d'une route avec un timeout de complétion, les groupes déjà
présents dans le dépôt reçoivent à nouveau un `CompletionTimeout` complet : un
groupe laissé en attente par une route arrêtée, ou persisté en SQL lors d'un
redémarrage du processus, expire au lieu d'attendre indéfiniment sa taille ou
son prédicat. Un dépôt personnalisé en bénéficie en implémentant
`ListableAggregationRepository` (`Keys`).

---

## Multicast

Envoie une copie à plusieurs destinations.

```go
builder.From("direct:start").
    Multicast().
        Pipeline().
            Log("Branche 1: ${body}").
            To("direct:out1").
        End().
        Pipeline().
            Log("Branche 2: ${body}").
            To("direct:out2").
        End().
    End()
```

**Options:**

- `ParallelProcessing()` — Exécute les branches en parallèle
- `AggregationStrategy` — Fusionne les résultats

**Propriétés:**

- `CamelMulticastIndex`
- `CamelMulticastSize`
- `CamelMulticastComplete`

---

## Wire Tap

Envoie une copie de l'exchange à un endpoint secondaire de manière asynchrone (fire-and-forget), sans bloquer la route principale. La copie est indépendante : les modifications sur l'exchange tapé n'affectent pas la route principale et vice-versa.

```go
builder.From("direct:start").
    WireTap("direct:audit"). // Copie asynchrone vers audit
    To("direct:result")
```

**Fonctionnement :**

1. Crée une **copie profonde** (deep copy) de l'exchange.
2. Lance un goroutine qui envoie la copie à l'endpoint spécifié.
3. Retourne immédiatement — la route principale continue sans attendre.
4. L'exchange tapé utilise un contexte indépendant (`context.Background()`) : il n'est pas annulé quand la route principale se termine.
5. Les erreurs lors du tap sont loggées mais ne sont pas propagées à la route principale.

**Notes :**

- Idéal pour l'audit, la journalisation ou la surveillance sans impact sur les performances.
- `Route.Stop()` attend que tous les taps en cours soient terminés avant de s'arrêter (aucun message n'est perdu lors du shutdown).
- Disponible également dans les contextes `Multicast`, `Pipeline`, `Split` et `Loop`.

---

## Pipeline

Chaîne séquentielle de processors.

```go
builder.From("direct:start").
    Pipeline().
        Log("Étape 1").
        Transform(transformer).
        Log("Étape 2").
        To("direct:end").
    End()
```

---

## Choice

Content-Based Router - route les messages vers différentes destinations selon des conditions sur le contenu.

```go
builder.From("direct:start").
    Choice().
        When("${header.priority == 'high'}").
            SimpleSetBody("🔴 Priority: ${body}").
            To("direct:urgent").
        When("${header.priority == 'medium'}").
            SimpleSetBody("🟡 Priority: ${body}").
            To("direct:normal").
        When("${body['count'] > 100}").
            To("direct:large-batch").
        Otherwise().
            To("direct:default").
    EndChoice()
```

`EndChoice()` accroche le Choice au **container courant** (la route, ou un
`Split` / `Loop` / `Multicast` englobant, pour que chaque partie soit évaluée).

**Expressions supportées :**

| Expression                 | Description         | Exemple                            |
|----------------------------|---------------------|------------------------------------|
| `${header.name}`           | Valeur d'en-tête    | `${header.Content-Type == 'json'}` |
| `${body}`                  | Corps du message    | `${body == 'active'}`              |
| `${body['key']}`           | Accès par clé map   | `${body['status'] == 'pending'}`   |
| `${body[0]}`               | Accès par index     | `${body[0] > 50}`                  |
| `${exchangeProperty.prop}` | Propriété Exchange  | `${exchangeProperty.userId != ''}` |
| `${date:now}`              | Date/heure actuelle | —                                  |
| `${random(100)}`           | Nombre aléatoire    | —                                  |
| `${uuid}`                  | UUID unique         | —                                  |

**Opérateurs de comparaison :**

- `==` — Égal à
- `!=` — Différent de
- `>` — Supérieur à
- `<` — Inférieur à
- `>=` — Supérieur ou égal
- `<=` — Inférieur ou égal

**Méthodes disponibles dans When/Otherwise :**

```go
// Définir le corps/header
When(expression).SetBody(value)
When(expression).SetHeader(key, value)
When(expression).SimpleSetBody("template ${body}")
When(expression).SimpleSetHeader(key, "template ${header.name}")

// Chainer des processors
When(expression).
    Log("Message").
    SetHeader("X-Processed", "true").
    To("direct:output")
```

> [!tip] Ordre d'évaluation

Les clauses `When` sont évaluées dans l'ordre. La première condition vraie déclenche son processor, les autres sont
ignorées. Si aucune condition ne correspond et qu'`Otherwise` est présent, il est exécuté.

---

## Filter

Filtre les messages selon une condition. Les exchanges qui ne correspondent pas sont silencieusement supprimés (
`ErrStopRouting`). La suppression ne termine que la route courante : si cette route a été appelée
via `direct:`, l'appelant continue (comme pour un Aggregator incomplet ou un Resequencer).
Utilisez `Stop()` pour arrêter l'exchange partout.

```go
// Expression Simple Language (comparaison à l'intérieur de ${...})
builder.From("direct:start").
    Filter("${header.type == 'order'}").
    To("direct:commandes").
    Build()

// Prédicat Go
builder.From("direct:start").
    Filter(func(e *gocamel.Exchange) (bool, error) {
        body, ok := e.GetBodyAsString()
        return ok && body != "", nil
    }).
    To("direct:nonVide").
    Build()
```

---

## Throttle

Limite le débit de traitement à N messages maximum par fenêtre de temps. Les exchanges excédentaires sont **retardés** (
jamais perdus) jusqu'à ce que la fenêtre se réinitialise.

```go
// Maximum 100 messages par seconde
builder.From("direct:start").
    Throttle(100, time.Second).
    To("direct:aval").
    Build()

// Maximum 10 messages par 500ms (fenêtre plus fine)
builder.From("direct:start").
    Throttle(10, 500*time.Millisecond).
    To("direct:api").
    Build()
```

**Notes :**

- Utilise un compteur à fenêtre fixe — pour un lissage plus fin, réduire la durée de la fenêtre
- Bloquant : le goroutine attend que la fenêtre autorise le passage
- `maxRequests` et `timePeriod` doivent être supérieurs à zéro ; les valeurs invalides retournent une erreur au lieu de
  bloquer indéfiniment
- Si le contexte de l'exchange est annulé pendant l'attente de la fenêtre suivante, `Process` retourne une erreur

---

## Sample

Ne laisse passer qu'un échantillon des exchanges. Contrairement à Throttle, qui retarde les exchanges, Sample **écarte**
silencieusement les autres, quel que soit leur contenu.

```go
// Au plus un message par seconde (le premier de chaque période passe)
builder.From("direct:capteur").
    Sample(time.Second).
    To("direct:tableauDeBord").
    Build()

// Un message sur 100 (le premier passe)
builder.From("direct:capteur").
    SampleEvery(100).
    To("direct:tableauDeBord").
    Build()
```

**Notes :**

- Les exchanges écartés s'arrêtent avec `ErrStopRouting` : ils ne sont pas traités comme des échecs
- Aucune goroutine en arrière-plan : seul un mutex protège l'horodatage ou le compteur
- `Sample` conserve le premier exchange de chaque période, pas le dernier
- Une période ou un compteur non positif est signalé par `Route.Validate()` ; `Process` retourne une erreur au lieu de
  tout laisser passer

---

## Delay

Met le traitement de l'exchange en pause pour une durée fixe ou calculée dynamiquement. Le sleep respecte l'annulation
du contexte.

```go
// Délai fixe
builder.From("direct:start").
    Delay(500 * time.Millisecond).
    To("direct:suivant").
    Build()

// Délai dynamique depuis un en-tête
builder.From("direct:start").
    DelayFunc(func(e *gocamel.Exchange) (time.Duration, error) {
        ms, ok := e.GetHeaderAsInt("X-Delay-Ms")
        if !ok {
            return 0, nil  // pas de délai si l'en-tête est absent
        }
        return time.Duration(ms) * time.Millisecond, nil
    }).
    To("direct:suivant").
    Build()
```

**Notes :**

- Bloquant : le goroutine dort pendant la durée calculée
- Si le contexte de l'exchange est annulé pendant le sleep, `Process` retourne une erreur
- Retourner `0` depuis `DelayFunc` pour ignorer le délai pour un exchange donné
- Passer `nil` à `DelayFunc` fait échouer la construction de la route ; `NewDelayerFunc(nil)` reste constructible mais
  retourne une erreur de traitement au lieu de paniquer

---

## Content Enricher

Enrichit l'exchange courant avec des données provenant d'une source externe.

```go
// Avec stratégie d'agrégation
type FusionStrategy struct{}

func (s *FusionStrategy) Aggregate(original, enriched *gocamel.Exchange) *gocamel.Exchange {
    origBody, _ := original.GetBodyAsString()
    enrichBody, _ := enriched.GetBodyAsString()
    original.GetIn().SetBody(origBody + " | " + enrichBody)
    return original
}

builder.From("direct:start").
    Enrich("http://api.example.com/data", &FusionStrategy{}).
    To("direct:suivant").
    Build()

// Sans stratégie — remplace le corps par la réponse d'enrichissement
builder.From("direct:start").
    Enrich("direct:lookup", nil).
    To("direct:suivant").
    Build()
```

**Fonctionnement :**

1. Une copie de l'exchange courant est envoyée à l'URI d'enrichissement
2. La réponse est récupérée
3. `AggregationStrategy.Aggregate(original, réponse)` fusionne les deux
4. Sans stratégie, le corps de la réponse remplace le corps original

---

## PollEnrich

Consommation ponctuelle à la demande depuis un endpoint consommateur (avec timeout et stratégie d'agrégation).

Les endpoints pris en charge sont `seda`, `chan` et les implémentations personnalisées de `PollingEndpoint`. Chaque appel reçoit au plus un échange et laisse la file ouverte avec ses autres messages. Un endpoint sans ce contrat, ou une file ayant déjà un consommateur ou une réception ponctuelle en cours, renvoie une erreur avant toute consommation. Un timeout nul est non bloquant ; un timeout négatif attend un message ou l’annulation du contexte. Un timeout positif laisse le message initial inchangé si aucun message n’arrive.

```go
// Consommation depuis une file d'attente avec timeout de 2 secondes
builder.From("direct:start").
    PollEnrich("seda:pending-orders", &FusionStrategy{}, 2*time.Second).
    To("direct:process")

// Consommation non-bloquante (timeout 0) remplaçant le corps
builder.From("direct:start").
    PollEnrichWithTimeout("chan:updates", 0).
    To("direct:process")
```

---

## Message Validator

Valide les messages en cours de transit selon un prédicat, interrompant l'échange avec `ErrValidationFailed` si la condition n'est pas remplie.

```go
// Validation via Simple Language
builder.From("direct:start").
    Validate("${header.priority == 'high'}").
    To("direct:urgent")

// Validation via JSONPath
builder.From("direct:start").
    Validate(gocamel.JsonPath("$.user.active")).
    To("direct:active-users")

// Validation via fonction personnalisée
builder.From("direct:start").
    ValidateFunc(func(e *gocamel.Exchange) bool {
        body, _ := e.GetBodyAsString()
        return len(body) > 0
    }).
    To("direct:output")
```

---

## Idempotent Consumer

Empêche le traitement de messages en double.

```go
repo := gocamel.NewMemoryIdempotentRepository()

builder.From("direct:start").
    IdempotentConsumer("${header.MessageId}", repo).
    To("direct:suivant").
    Build()
```

**Fonctionnement :**

1. Évalue l'expression pour obtenir une clé unique.
2. Vérifie si la clé existe déjà dans le dépôt (repository).
3. Si elle n'est pas présente, ajoute la clé immédiatement (bloquant les doublons
   concurrents) et continue le traitement.
4. Si elle est déjà présente, arrête le routage (`ErrStopRouting`).
5. Si l'exchange **échoue** ensuite en aval, la clé est retirée du dépôt (via une
   `Synchronization` de l'exchange) : une relivraison du même message sera donc traitée
   au lieu d'être perdue — sémantique at-least-once.

**Dépôts (Repositories) :**

- `NewMemoryIdempotentRepository()` : Stockage en mémoire vive (par défaut).
- `NewRedisIdempotentRepository()` : Stockage distribué via Redis.
- Des dépôts basés sur SQL peuvent être implémentés via l'interface `IdempotentRepository`.

---

## Circuit Breaker

Protège les routes contre les pannes en cascade lors de l'appel de services externes instables.

```go
builder.From("direct:start").
    CircuitBreaker().
        FailureThreshold(5).           // Ouvre après 5 échecs
        OpenTimeout(30 * time.Second).  // Reste ouvert pendant 30s
        SuccessThreshold(2).           // Ferme après 2 succès en état Half-Open
        Process(unstableProcessor).
        OnFallback().
            Log("Circuit ouvert ou échec, appel du fallback").
            To("direct:fallback").
        End().
    End().
    To("direct:resultat").
    Build()
```

**États :**

- **Closed (Fermé)** : Les requêtes passent normalement. Les échecs sont comptabilisés.
- **Open (Ouvert)** : Les requêtes échouent immédiatement ou passent par le fallback.
- **Half-Open (Semi-ouvert)** : Après le `OpenTimeout`, **une seule requête d'essai à la fois** sonde le service
  aval ; les requêtes concurrentes sont redirigées vers le fallback comme si le circuit était encore ouvert.
  Quand `SuccessThreshold` sondes réussissent, le circuit se referme. Si une sonde échoue, il s'ouvre à nouveau.

`ErrStopRouting` (Filter, agrégation incomplète, Stop EIP) n'est **pas** un
échec : il n'incrémente pas le compteur et n'ouvre pas le circuit.

---

## Load Balancer

Répartit les messages sur plusieurs endpoints.

```go
builder.From("direct:start").
    LoadBalance().RoundRobin().
        To("direct:serveur1").
        To("direct:serveur2").
        To("direct:serveur3").
    End().
    Build()
```

**Stratégies :**

- `RoundRobin()` : Distribution circulaire (par défaut).
- `Random()` : Distribution aléatoire.
- `Custom(strategy)` : Fournir une implémentation personnalisée de l'interface `LoadBalancerStrategy`.

---

## SEDA

Staged Event-Driven Architecture. Découple les producteurs et les consommateurs via une file d'attente interne (Go
channel).

```go
// Consommateur avec workers concurrents
builder.From("seda:traitement?concurrentConsumers=5&size=1000").
    Log("Traitement asynchrone : ${body}").
    To("direct:suivant")

// Producteur
builder.To("seda:traitement")
```

**Fonctionnement :**

1. Le producteur envoie une **copie** de l'exchange dans une file d'attente interne.
2. Le producteur rend la main immédiatement (asynchrone).
3. Un ou plusieurs workers lisent la file et traitent l'exchange de manière indépendante.

**Paramètres :**

- `size` : La capacité de la file d'attente (défaut 1000).
- `concurrentConsumers` : Le nombre de goroutines esclaves traitant la file (défaut 1).
- `shutdownTimeout` : Durée pendant laquelle `Stop` continue de traiter les exchanges
  en file, en ms (défaut 30000 ; `0` les abandonne immédiatement).

Les valeurs invalides (`size` négatif, `concurrentConsumers` non positif) retombent sur
les valeurs par défaut avec un avertissement. Les paramètres sont appliqués à la
**première création** de l'endpoint ; les références ultérieures au même nom de file
réutilisent l'endpoint existant, et une référence avec des options différentes émet un
avertissement.

**Contexte.** Les workers traitent chaque exchange sous le contexte du consumer, et non
celui de l'émetteur. Une route comme `From("http://...").To("seda:travail")` rend la
main dès que le message est en file, ce qui annule le contexte de la requête HTTP ; la
partie asynchrone n'est pas affectée et ses appels HTTP, délais et requêtes SQL
s'exécutent normalement.

**Arrêt.** `Stop` refuse d'abord les nouveaux envois (et débloque les producteurs en
attente sur une file pleine), puis laisse les workers terminer les exchanges déjà en
file, pendant au plus `shutdownTimeout`. Il annule ensuite le contexte du consumer ; les
exchanges restant en file sont complétés en échec. `CamelContext.GracefulStop` annule
le contexte d'emblée et n'attend donc pas la file.

---

## Resequencer

Remet dans l'ordre les messages arrivés de manière désordonnée en se basant sur un numéro de séquence ou un horodatage.

```go
builder.From("direct:start").
    Resequence("${header.seq}", 1). // Commence à la séquence 1
        Timeout(500 * time.Millisecond).
        Log("Traitement dans l'ordre : ${header.seq}").
        To("direct:suivant").
    End()
```

**Fonctionnement :**

1. Évalue l'expression pour obtenir le numéro de séquence.
2. Si la séquence est celle attendue, elle est traitée immédiatement.
3. S'il s'agit d'une séquence future, elle est mise en tampon et triée.
4. Si un trou (gap) persiste plus longtemps que le `Timeout`, le resequenceur saute la séquence manquante et continue
   avec la suivante disponible.

**Notes :**

- Les messages sont délivrés en aval via le **pipeline interne** du resequenceur (les étapes
  entre `Resequence(...)` et `End()`). L'exchange original s'arrête là (`ErrStopRouting`) :
  les étapes placées *après* `End()` ne voient jamais de messages hors ordre.
- La livraison est sérialisée pour garantir l'ordre ; les étapes en aval ne doivent pas
  rerouter vers le même resequenceur.
- Les numéros de séquence dupliqués ou obsolètes sont ignorés (avec un avertissement dans
  les logs).

---

## Claim Check

Réduit la taille des messages en stockant le contenu (payload) dans un dépôt externe et en ne faisant circuler qu'une
référence (une clé).

```go
repo := gocamel.NewMemoryClaimCheckRepository()

builder.From("direct:start").
    // Stocke le corps et le remplace par une clé
    ClaimCheck(gocamel.ClaimCheckPush, "mon-ticket", repo).
    To("direct:autre-systeme"). // Seule la clé "mon-ticket" est envoyée
    // Restaure le corps original
    ClaimCheck(gocamel.ClaimCheckPop, "mon-ticket", repo).
    To("direct:suivant")
```

**Opérations :**

- `ClaimCheckPush` : Stocke le corps et le remplace par une clé. Si aucune clé n'est fournie, elle est générée.
- `ClaimCheckPop` : Récupère les données par clé, restaure le corps et les supprime du dépôt.
- `ClaimCheckGet` : Récupère les données par clé et restaure le corps (les garde dans le dépôt).
- `ClaimCheckDiscard` : Supprime les données du dépôt sans les restaurer.

---

## Stop

Arrête le routage actuel sans erreur.

```go
builder.From("direct:start").
    ProcessFunc(func(e *gocamel.Exchange) error {
        if shouldStop(e) {
            e.SetProperty("stopped", true)
            return nil
        }
        return nil
    }).
    Stop(). // Arrête ici si condition remplie
    Log("Jamais atteint si stop")
```

Contrairement à `Filter`, `Stop()` termine l'exchange entier (il positionne la propriété
`CamelRouteStop`) : une route qui a appelé celle-ci via `direct:` s'arrête aussi, et son
`Send` renvoie `ErrStopRouting`.

---

## ToD (Dynamic To)

URI résolue dynamiquement à l'exécution.

```go
builder.From("direct:start").
    SetHeader("CamelFileName", "data.txt").
    ToD("file://output/${header.CamelFileName}")
```

**Expressions supportées:**

- `${header.<name>}` — Valeur d'en-tête
- `${property.<name>}` — Valeur de propriété
- `${body}` — Corps du message

**Échappement.** Les valeurs de l'exchange sont des données, pas de la syntaxe
d'URI. Chaque valeur est échappée selon la position de son placeholder dans le
template (`InterpolateURI`) :

- dans la query string, elle est échappée comme valeur de requête : un header
  comme `a&allowHeaderOverride=true` reste dans son option et ne peut pas en
  activer une autre ;
- dans le chemin, `?` et `#` sont échappés : une valeur ne peut pas ouvrir une
  query string.

Un placeholder qui représente l'URI entière ou son schéma (`ToD("${header.dest}")`)
est inséré tel quel : la route laisse explicitement les données de l'exchange
choisir l'endpoint, ces données doivent donc provenir d'une source de confiance.

Chaque URI distincte conserve un producer démarré dans le cache de producers du
contexte, borné à `DefaultProducerCacheSize` (1000) producers. Au-delà, les
producers inactifs les moins récemment utilisés sont arrêtés. Modifiez la borne
avec `CamelContext.SetProducerCacheSize(n)` (`n <= 0` = illimité).

---

## Recipient List

Envoie un message à plusieurs destinataires calculés dynamiquement à l'exécution.

```go
// Destinataires depuis un en-tête
builder.From("direct:start").
    SetHeader("recipients", "direct:a,direct:b,direct:c").
    RecipientList("${header.recipients}")

// Appeler les destinataires en parallèle, 5 au plus à la fois (10 par défaut)
builder.From("direct:start").
    RecipientList("${header.recipients}").ParallelProcessing().MaxConcurrency(5).End()
```

---

## Dynamic Router

Route un exchange à travers une séquence d'endpoints résolus dynamiquement en appelant une fonction de contrôle après
chaque étape. Le routage s'arrête quand la fonction retourne une chaîne vide ou `nil`.

```go
// Utilisation d'une fonction Go
builder.From("direct:start").
    DynamicRouter(func(e *gocamel.Exchange) (string, error) {
        // Décide de la prochaine étape selon l'état/contenu
        val, _ := e.GetProperty("step")
        count, _ := val.(int)
        e.SetProperty("step", count + 1)
        
        if count == 0 { return "direct:a", nil }
        if count == 1 { return "direct:b", nil }
        return "", nil // Fin du routage
    })

// Utilisation du Simple Language
builder.From("direct:start").
    DynamicRouter("${header.nextEndpoint}")
```

**Fonctionnement :**

1. Appelle la fonction ou l'expression de contrôle.
2. Si le résultat n'est pas vide, envoie l'exchange à cet URI.
3. Propage la sortie vers l'entrée et recommence à l'étape 1.

---

## Routing Slip

Route un exchange à travers une séquence dynamique d'endpoints. Contrairement à
Recipient List, chaque étape reçoit la sortie de l'étape précédente.

```go
// Routing slip depuis un en-tête
builder.From("direct:start").
    RoutingSlip("${header.routingSlip}")

// Fonction Go
builder.From("direct:start").
    RoutingSlip(func(e *gocamel.Exchange) ([]string, error) {
        return []string{"direct:valider", "direct:transformer", "direct:envoyer"}, nil
    })
```

**Propriétés Exchange pendant Routing Slip :**

| Propriété                  | Type | Description                        |
|----------------------------|------|------------------------------------|
| `CamelRoutingSlipIndex`    | int  | Index de l'étape courante (base 0) |
| `CamelRoutingSlipSize`     | int  | Nombre total d'étapes              |
| `CamelRoutingSlipComplete` | bool | Indique la dernière étape          |

---

## Headers & Properties

### Set/Remove Headers

```go
// Définir
builder.SetHeader("X-Correlation-ID", uuid.New().String())
builder.SetHeaders(map[string]any{
    "X-Client-Version": "1.0",
    "X-Request-Time": time.Now(),
})

// Supprimer
builder.RemoveHeader("X-Temp-Data")
builder.RemoveHeaders("X-Debug*", "X-Debug-Rare") // Wildcard
```

### Set/Remove Properties

```go
// Définir
builder.SetProperty("correlationId", "abc-123")
builder.SetPropertyFunc("status", func(e *gocamel.Exchange) (any, error) {
    return e.GetIn().GetHeader("status"), nil
})

// Supprimer
builder.RemoveProperty("temp-token")
builder.RemoveProperties("cache-*")
```

---

## Gestion des erreurs

### Do-Try-Catch-Finally

Gestion structurée des erreurs au sein d'une route.

```go
builder.From("direct:start").
    DoTry().
        To("http://service-instable").
    DoCatch("connection refused").
        Log("Le service est hors ligne, application du mode dégradé").
        To("direct:recovery").
    DoCatch(""). // Rattrape toutes les autres erreurs
        Log("Erreur inattendue : ${exchangeProperty.CamelExceptionCaught}").
        To("direct:error-handler").
    DoFinally().
        Log("Traitement terminé (succès ou échec)").
    End().
    To("direct:suivant")
```

**Fonctionnement :**

1. Exécute les processeurs du bloc `DoTry`.
2. Si une erreur survient, recherche un bloc `DoCatch` correspondant (une chaîne vide intercepte tout).
3. Si une correspondance est trouvée, l'erreur est considérée comme "gérée" et le routage continue après le bloc
   `DoTry` (sauf si le bloc catch échoue lui-même).
4. Le bloc `DoFinally` est **toujours** exécuté, qu'il y ait eu une erreur ou non.

### Error handler de route et Dead Letter Channel

Un `ErrorHandler` de route reçoit chaque erreur renvoyée par une étape. Sa
valeur de retour pilote la route :

- `nil` demande à la route de **rejouer l'étape en échec** (redelivery). Cela
  ne veut pas dire « ignorer l'erreur et continuer » : la route abandonne après
  `MaxErrorHandlerRetries` (10000) rejeux consécutifs de la même étape et
  renvoie une erreur qui enveloppe `ErrTooManyRetries`.
- une erreur qui enveloppe `ErrStopRouting` arrête le routage sans mettre
  l'exchange en échec.
- toute autre erreur met l'exchange en échec.

`DeadLetterChannel` réessaie jusqu'à `MaxRedeliveries`, envoie ensuite
l'exchange vers l'endpoint dead letter et, par défaut, renvoie quand même
l'erreur d'origine au consumer (le consumer HTTP répond 500). Avec
`DeadLetterHandled(true)`, un exchange envoyé en dead letter est considéré comme
traité, comme dans Apache Camel :

```go
builder.From("http://0.0.0.0:8080/commandes").
    DeadLetterChannel("seda:dead", gocamel.MaxRedeliveries(3), gocamel.DeadLetterHandled(true)).
    To("direct:traitement")
```

### OnException

`OnException` déclare des gestionnaires de route sélectionnés **par type
d'erreur**, comme le `onException` d'Apache Camel. Chaque clause liste une ou
plusieurs cibles :

- une **valeur d'erreur** (une sentinelle comme `io.EOF`) correspond via
  `errors.Is` ;
- un **pointeur non nil vers un type d'erreur ou d'interface** correspond via
  `errors.As` : `gocamel.ErrorType[*MonErreur]()` (équivalent à
  `new(*MonErreur)`), `&MonErreurValeur{}` pour une erreur à récepteur valeur,
  `gocamel.ErrorType[net.Error]()`. Un `&MonErreur{}` dont la méthode `Error`
  a un récepteur pointeur est une valeur d'erreur et ne correspond qu'à
  elle-même.

Une cible absente, `nil` ou non supportée fait échouer `Build()`.

```go
builder.
    OnException(gocamel.ErrorType[*ValidationError]()).
        Handled(true).
        SetBody("commande invalide").
    End().
    OnException(io.ErrUnexpectedEOF, syscall.ECONNRESET).
        MaximumRedeliveries(3).
        RedeliveryDelay(100 * time.Millisecond).
        To("direct:reprise").
    End().
    From("direct:commandes").
    To("http://backend/commandes")
```

**Fonctionnement :**

1. Quand une étape de premier niveau échoue, les clauses sont essayées dans
   l'**ordre de déclaration** et la **première** qui correspond l'emporte :
   déclarez d'abord les clauses spécifiques.
2. Une clause qui correspond **remplace** l'`ErrorHandler` de la route pour
   cette erreur. L'étape en échec est rejouée jusqu'à `MaximumRedeliveries`
   fois (0 par défaut), en attendant `RedeliveryDelay` (1 s par défaut)
   multiplié par `BackOffMultiplier` (2 par défaut, 1 pour un délai fixe) à
   chaque tentative, plafonné par `MaximumRedeliveryDelay` (60 s par défaut).
   L'attente s'interrompt si le contexte de l'exchange est annulé.
3. Si l'étape échoue encore, les processeurs de la clause (`Process`,
   `ProcessFunc`, `To`, `SetBody`, `SetHeader`, `Log`) s'exécutent sur le
   message reçu par l'étape, avec les propriétés `CamelExceptionCaught`,
   `CamelRedeliveryCounter` et `CamelFailureRouteId`.
4. Ensuite :
   - `Handled(true)` : le routage **s'arrête** (aucune étape suivante) et
     **aucune erreur n'atteint le consumer ni l'émetteur**, qui reçoit
     l'exchange tel que laissé par le gestionnaire. La route renvoie une erreur
     qui enveloppe `ErrStopRouting` et l'erreur d'origine.
   - `Continued(true)` : le routage **continue avec l'étape suivante** comme si
     l'erreur n'avait pas eu lieu ; cette étape reçoit la sortie du
     gestionnaire. `Continued` l'emporte sur `Handled`.
   - aucun des deux (par défaut) : l'erreur d'origine est propagée.
5. Si un processeur du gestionnaire échoue, son erreur (qui enveloppe l'erreur
   d'origine) est renvoyée à la place.

Les erreurs sans clause correspondante gardent le comportement
`ErrorHandler`/`DeadLetterChannel`. Un exchange déjà envoyé en dead letter
(`IsDeadLettered`), par exemple par une route appelée via `direct:` qui propage
l'erreur d'origine, ne déclenche jamais de clause : son échec a déjà été pris
en charge.

**Limites :** les clauses sont propres à la route (pas encore d'`OnException`
global au contexte), ne s'appliquent qu'aux erreurs des étapes de premier
niveau — un rejeu relance toute l'étape, par exemple un bloc `Split` entier —
et n'ont pas de prédicat `OnWhen`.

---

## Process

Exécute un processeur personnalisé.

```go
// Fonction simple
builder.ProcessFunc(func(e *gocamel.Exchange) error {
    body := e.GetIn().GetBody().(string)
    e.GetOut().SetBody(process(body))
    return nil
})

// Struct implémentant Processor
builder.Process(&MyCustomProcessor{config: cfg})
```

---

## Loop

Répète un segment de route un nombre de fois statique ou dynamique.

```go
// Nombre d'itérations fixe (statique)
builder.From("direct:start").
    Loop(3).
        ProcessFunc(func(e *gocamel.Exchange) error {
            // Évalué 3 fois
            idx, _ := e.GetPropertyAsInt(gocamel.CamelLoopIndex)
            size, _ := e.GetPropertyAsInt(gocamel.CamelLoopSize)
            fmt.Printf("Traitement de l'itération %d sur %d\n", idx, size)
            return nil
        }).
    End().
    To("direct:output")

// Nombre d'itérations dynamique via une expression Simple Language
builder.From("direct:start").
    LoopSimple("${header.LoopCount}").
        To("direct:process-item").
    End()

// Nombre d'itérations dynamique via une fonction Go
builder.From("direct:start").
    LoopFunc(func(e *gocamel.Exchange) (int, error) {
        val, _ := e.GetHeaderAsInt("X-Max-Retries")
        return val, nil
    }).
        To("direct:retry-attempt").
    End()
```

**Propriétés de l'Exchange pendant la boucle :**

| Propriété | Type | Description |
|---|---|---|
| `CamelLoopIndex` | int | Index de l'itération actuelle (commence à 0) |
| `CamelLoopSize` | int | Nombre total d'itérations de la boucle |
| `CamelLoopComplete` | bool | Booléen indiquant s'il s'agit de la dernière itération |

---

## Saga

Le pattern Saga fournit un mécanisme pour coordonner une série de transactions locales à travers des services distribués sans verrouiller les bases de données. Il définit un ensemble d'actions et leurs actions de compensation correspondantes (annulations).

Si une action dans le bloc Saga échoue ou retourne une erreur, l'orchestrateur de la Saga déclenche automatiquement les étapes de compensation pour toutes les actions terminées, dans l'ordre inverse (LIFO - Last In, First Out). Les panics d'action sont récupérés et traités comme des échecs (les compensations s'exécutent quand même). Un `ErrStopRouting` provenant d'une action arrête la saga **sans** compensation — c'est un signal de contrôle, pas un échec.

```go
builder.From("direct:saga-route").
    Saga().
        // Action 1: Réserver Hôtel. Compensation 1: Annuler Hôtel.
        Action("direct:reserve-hotel", "direct:cancel-hotel").
        // Action 2: Réserver Vol. Compensation 2: Annuler Vol.
        Action("direct:reserve-flight", "direct:cancel-flight").
    End().
    To("direct:saga-completed")
```

Vous pouvez également utiliser des fonctions Go personnalisées pour les actions et les compensations directement avec `ActionFunc` :

```go
builder.From("direct:saga-route").
    Saga().
        ActionFunc(
            func(e *gocamel.Exchange) error {
                // Réserver l'hébergement
                return nil
            },
            func(e *gocamel.Exchange) error {
                // Annuler/compenser la réservation
                return nil
            },
        ).
    End()
```

Une compensation `nil` dans `ActionFunc` signifie que l'étape n'a rien à annuler.

### État persistant et reprise

Chaque saga s'exécute sous un identifiant lu dans l'en-tête `CamelSagaId` (un UUID est généré et placé dans l'en-tête s'il est absent). Après chaque action dont l'étape a une compensation, la saga **enregistre un point de reprise** dans un `SagaRepository` : la liste des compensations en attente et un instantané du message In (corps et en-têtes). Elle enregistre de nouveau après chaque compensation, et supprime l'état une fois la saga terminée ou entièrement compensée. Une compensation en échec reste dans le dépôt pour qu'une reprise ultérieure la retente.

```go
db, _ := sql.Open("sqlite", "sagas.db") // n'importe quel driver database/sql
repo := gocamel.NewSQLSagaRepository(db, gocamel.SQLSagaOptions{}) // table gocamel_sagas
_ = repo.InitDB(context.Background())

builder.From("direct:saga-route").
    Saga().
        Repository(repo).              // défaut : en mémoire (perdu à l'arrêt du processus)
        Timeout(5 * time.Second).      // limite pour chaque action et compensation
        RecoverIncomplete(true).       // reprise optionnelle au CamelContext.Start
        Action("direct:reserve-hotel", "direct:cancel-hotel").
        Action("direct:reserve-flight", "direct:cancel-flight").
    End()
```

| Option | Défaut | Effet |
|---|---|---|
| `Repository(repo)` | `MemorySagaRepository` | Où l'état est persisté. `SQLSagaRepository` (module racine, `database/sql` uniquement — le driver est fourni par l'appelant) survit à un crash du processus. |
| `Timeout(d)` | `0` (aucun) | Limite chaque action et chaque compensation. Une action qui la dépasse échoue avec `ErrSagaStepTimeout` (qui enveloppe `context.DeadlineExceeded`) et les étapes **terminées** sont compensées ; l'étape expirée ne l'est pas, comme toute étape en échec. L'étape s'exécute sur une copie de l'échange dont le `Context` porte l'échéance : une étape qui ignore son contexte continue en arrière-plan après l'expiration et son résultat est ignoré. |
| `RecoverIncomplete(true)` | désactivé | Au `CamelContext.Start`, une fois toutes les routes démarrées, les sagas incomplètes trouvées dans le dépôt sont compensées de la dernière étape à la première. Désactivé, chaque saga incomplète est seulement journalisée en avertissement, avec la manière d'activer la reprise. |

La reprise envoie chaque compensation à son URI via le cache de producteurs du contexte, avec un échange reconstruit à partir du message du dernier point de reprise et de l'en-tête `CamelSagaId`. `SagaProcessor.Recover(ctx, camelCtx)` la déclenche à la demande. Les sagas encore en cours dans le processus courant ne sont jamais reprises.

{{% notice warning %}}
**Seules les compensations déclarées par URI d'endpoint survivent à un redémarrage du processus.** Une compensation passée à `ActionFunc` est une fonction Go : elle ne peut pas être sérialisée, donc après un redémarrage rien ne permet de la reconstruire. Ces étapes sont tout de même persistées (avec `InProcessCompensation`), et lorsque la reprise en rencontre une, elle signale `ErrSagaCompensationNotRecoverable` dans une ligne de log ERROR qui nomme la saga et l'étape — elle **ne l'ignore jamais silencieusement** — et la conserve dans le dépôt, au statut `COMPENSATING`, pour un traitement manuel. Les autres compensations par URI de cette saga s'exécutent quand même. Déclarez les étapes qui doivent pouvoir être reprises avec `Action(actionURI, compensationURI)`.
{{% /notice %}}

**Garanties de livraison.** Le point de reprise est écrit après le retour de l'action et avant le début de l'action suivante. Un crash entre une action et son point de reprise perd cette étape (sa compensation ne s'exécute pas à la reprise) ; un crash après une compensation et avant la mise à jour de l'état ré-exécute cette compensation. La compensation à la reprise est donc **au moins une fois** pour les étapes enregistrées : les compensations doivent être idempotentes. Si un point de reprise ne peut pas être enregistré, la saga échoue et compense ses étapes terminées, y compris celle qui vient de se terminer.

**Format de persistance.** `SQLSagaRepository` stocke l'état en JSON (`saga_id`, `status`, `state`, `updated_at` en nanosecondes Unix) : le corps et les valeurs d'en-têtes doivent être sérialisables en JSON et reviennent sous forme de types JSON (nombres en `float64`, `[]byte` en chaîne base64). `ListIncomplete` renvoie au plus `DefaultSagaListLimit` (1000) sagas, les plus anciennes d'abord ; les suivantes sont reprises à un démarrage ultérieur, une fois les premières résolues.

**Limites.** Une saga imbriquée dans l'action d'une autre saga reçoit son propre identifiant (l'en-tête de la saga englobante est restauré ensuite) ; les sagas ne sont ni propagées ni jointes entre routes. La reprise ne coordonne pas plusieurs processus partageant une même base : activez `RecoverIncomplete` sur une seule instance, sinon un processus peut compenser une saga qu'un autre exécute encore.

---

## Transformer

Transforme le contenu du message.

```go
// Définir le corps directement
builder.From("direct:start").
    SetBody("Hello World")

// Définir le corps via une fonction
builder.From("direct:start").
    SetBodyFunc(func(e *gocamel.Exchange) (any, error) {
        input := e.GetIn().GetBody().(string)
        return strings.ToUpper(input), nil
    })

// Transformer via Simple Language
builder.From("direct:start").
    SimpleSetBody("Processed: ${body} at ${date:now}")
```

---

## Transform

Un EIP dédié à la transformation de message qui modifie dynamiquement le corps (body) du message à l'aide d'une fonction Go ou d'une expression Simple Language.

```go
// Transformation via une fonction Go personnalisée
builder.From("direct:start").
    Transform(func(e *gocamel.Exchange) (any, error) {
        input, _ := e.GetBodyAsString()
        return "Transformed: " + input, nil
    }).
    To("direct:output")

// Transformation via une expression Simple Language
builder.From("direct:start").
    TransformSimple("Modified: ${body} and type is ${header.Type}").
    To("direct:output")
```

---

## Message History

L'EIP Message History permet de retracer le parcours chronologique d'un message lorsqu'il traverse les différents nœuds et processeurs de la route.

Pour activer le suivi d'une route, appelez `.EnableMessageHistory()` sur votre route builder. Vous pouvez ensuite récupérer et inspecter l'historique des messages à l'aide de `gocamel.GetMessageHistory(exchange)`.

```go
// 1. Activer le suivi dans la définition de la route
builder.From("direct:start").
    EnableMessageHistory().
    SetBody("Hello GoCamel").
    TransformSimple("Modified: ${body}").
    To("direct:output")

// 2. Récupérer l'historique dans un processeur en aval
builder.From("direct:output").
    ProcessFunc(func(e *gocamel.Exchange) error {
        history := gocamel.GetMessageHistory(e)
        for i, entry := range history {
            fmt.Printf("[%d] Route : %s | Processor : %T | Time : %s\n", 
                i, entry.RouteID, entry.Processor, entry.Timestamp.Format(time.RFC3339))
        }
        return nil
    })
```

---

## Récapitulatif des patterns EIP

| Pattern             | Catégorie      | Description                                  |
|---------------------|----------------|----------------------------------------------|
| Choice              | Routage        | Routage basé sur le contenu                  |
| Filter              | Routage        | Filtrage conditionnel                        |
| Idempotent Consumer | Routage        | Empêche les messages en double               |
| Circuit Breaker     | Routage        | Pattern de résilience pour les pannes        |
| Load Balancer       | Routage        | Répartition de charge entre endpoints        |
| Dynamic Router      | Routage        | Routage dynamique basé sur décision          |
| Resequencer         | Routage        | Réordonnancer les messages par séquence      |
| Claim Check         | Routage        | Stocker le payload et passer une référence   |
| Throttle            | Routage        | Limitation de débit                          |
| Delay               | Routage        | Pause avant la prochaine étape               |
| Sample              | Routage        | Garder un échantillon, écarter le reste      |
| Multicast           | Routage        | Destinations multiples                       |
| Recipient List      | Routage        | Routage dynamique vers plusieurs destinataires|
| Routing Slip        | Routage        | Routage séquentiel dynamique                 |
| Wire Tap            | Routage        | Copie asynchrone fire-and-forget             |
| Split               | Transformation | Découpage de message                         |
| Aggregate           | Transformation | Agrégation de messages                       |
| Content Enricher    | Transformation | Enrichissement avec données externes         |
| Transform           | Transformation | Transformation de contenu                    |
| ToD                 | Endpoint       | Endpoint dynamique                           |
| Stop                | Contrôle       | Arrêter le routage                           |
| DoTry               | Contrôle       | Gestion d'erreurs Try-Catch-Finally          |
| SetHeader           | En-têtes       | Manipulation d'en-têtes                      |
| SetProperty         | Propriétés     | Propriétés d'exchange                        |
| Loop                | Contrôle       | Itération avec compteur statique/dynamique   |
| Saga                | Contrôle       | Pattern transactionnel Saga                  |
| Message History     | Diagnostics    | Suivi du parcours de traitement des messages |

## Fin des branches et agrégation

Split, Multicast et RecipientList terminent chaque branche traitée, en succès comme en échec, notamment ses callbacks de rollback d’idempotence. Avant de lancer les workers parallèles, ils copient les corps mutables pris en charge, y compris chaque élément du Split. Les types mutables personnalisés exigent toujours une copie applicative ou un usage immuable. L’agrégation commence après la fin des workers ; la stratégie est appelée séquentiellement dans l’ordre d’entrée et ses panics deviennent des erreurs `ErrProcessorPanic`. Le traitement séquentiel s’arrête à la première branche en échec. Un Split ou une RecipientList en parallèle traite au plus `MaxConcurrency(n)` branches à la fois (défaut `DefaultMaxConcurrency`, 10) : leur nombre de branches dépend du contenu du message, et sans borne chaque élément lançait sa propre goroutine. Un Multicast parallèle lance toutes ses branches à la fois, leur nombre étant fixé par la route.

Une expression `Resequence` invalide accumule une erreur de construction tout en laissant la chaîne DSL utilisable ; `Timeout`, `Capacity` et `End` ne paniquent pas à cause de cette erreur antérieure.
