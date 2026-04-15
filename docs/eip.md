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
