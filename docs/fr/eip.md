# EIP (Enterprise Integration Patterns)

Les patterns d'intégration enterprise implémentés dans GoCamel.

## Split

Divise un message en plusieurs parties traitées individuellement.

```go go
builder.From("direct:start").
    Split(func(e *gocamel.Exchange) (any, error) {
        body := e.GetIn().GetBody().(string)
        return strings.Split(body, ","), nil
    }).
    Log("Partie: ${body}").
    To("direct:process").
    End()
```

**Propriétés de l'Exchange:**

- `CamelSplitIndex` — Index actuel (0-based)
- `CamelSplitSize` — Nombre total de parties
- `CamelSplitComplete` — Dernière partie ?

---

## Aggregate

Combine plusieurs messages en un seul.

```go go
strategy := &MyAggregationStrategy{}
repo := gocamel.NewMemoryAggregationRepository()

builder.From("direct:start").
    Aggregate(gocamel.NewAggregator(correlationExpr, strategy, repo).
        SetCompletionSize(3)). // Completer après 3 messages
    Log("Agrégation terminée: ${body}")
```

**Stratégie personnalisée:**

```go go
type MyAggregationStrategy struct{}

func (s *MyAggregationStrategy) Aggregate(
    oldExchange, 
    newExchange *gocamel.Exchange
) *gocamel.Exchange {
    // Logique de fusion
    return oldExchange
}
```

---

## Multicast

Envoie une copie à plusieurs destinations.

```go go
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

## Pipeline

Chaîne séquentielle de processors.

```go go
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

```go go
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

```go go
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

!!! tip "Ordre d'évaluation"
Les clauses `When` sont évaluées dans l'ordre. La première condition vraie déclenche son processor, les autres sont
ignorées. Si aucune condition ne correspond et qu'`Otherwise` est présent, il est exécuté.

---

## Filter

Filtre les messages selon une condition. Les exchanges qui ne correspondent pas sont silencieusement supprimés (
`ErrStopRouting`).

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
3. Si elle n'est pas présente, ajoute la clé et continue le traitement.
4. Si elle est déjà présente, arrête le routage (`ErrStopRouting`).

**Dépôts (Repositories) :**

- `NewMemoryIdempotentRepository()` : Stockage en mémoire vive (par défaut).
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
- **Half-Open (Semi-ouvert)** : Après le `OpenTimeout`, autorise quelques requêtes pour tester si le service est
  rétabli. Si le `SuccessThreshold` est atteint, il se referme. En cas d'échec, il s'ouvre à nouveau.

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
    To("direct: suivant")

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

```go go
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

---

## ToD (Dynamic To)

URI résolue dynamiquement à l'exécution.

```go go
builder.From("direct:start").
    SetHeader("CamelFileName", "data.txt").
    ToD("file://output/${header.CamelFileName}")
```

**Expressions supportées:**

- `${header.<name>}` — Valeur d'en-tête
- `${property.<name>}` — Valeur de propriété
- `${body}` — Corps du message

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

```go go
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

```go go
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

---

## Process

Exécute un processeur personnalisé.

```go go
// Fonction simple
builder.ProcessFunc(func(e *gocamel.Exchange) error {
    body := e.GetIn().GetBody().(string)
    e.GetOut().SetBody(process(body))
    return nil
})

// Struct implémentant Processor
builder.Process(&MyCustomProcessor{config: cfg})
```
