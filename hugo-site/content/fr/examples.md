+++
title = "Exemples"
weight = 22
+++
## Présentation

Collection d'exemples pratiques démontrant les fonctionnalités de GoCamel.

## Hello World

```go title="hello.go"
package main

import (
    "gitlab.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    route := ctx.CreateRouteBuilder().
        From("timer:tick?period=5s").
        SetBody("Hello GoCamel!").
        Log("${body}").
        Build()

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

## Intégration OpenAI

```go title="openai_example.go"
package main

import (
    "fmt"
    "gitlab.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()
    ctx.AddComponent("openai", gocamel.NewOpenAIComponent())

    // Utilisation manuelle du producer
    endpoint, _ := ctx.CreateEndpoint("openai:chat?model=gpt-3.5-turbo")
    producer, _ := endpoint.CreateProducer()

    exchange := gocamel.NewExchange(nil)
    exchange.GetIn().SetBody("Explique l'intégration en Go")

    producer.Send(exchange)
    fmt.Println(exchange.GetOut().GetBody())
}
```

---

## Traitement de fichiers

### Fichier vers Fichier

```go
package main

import (
    "gitlab.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    // Déplacer les fichiers de input vers output
    route := ctx.CreateRouteBuilder().
        From("file://input?delete=true").
        Log("Traitement: ${header.CamelFileName}").
        To("file://output").
        Build()

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

### Fichier vers Fichier avec transformation

```go
route := ctx.CreateRouteBuilder().
    From("file://input?include=*.txt&delete=true").
    ProcessFunc(func(e *gocamel.Exchange) error {
        content := e.GetIn().GetBody().(string)
        // Transformer le contenu
        e.GetOut().SetBody(strings.ToUpper(content))
        return nil
    }).
    SetHeader("CamelFileName", "${header.CamelFileName}.processed").
    To("file://output").
    Build()
```

---

## Endpoints HTTP

### Serveur Echo HTTP

```go title="http_echo.go"
package main

import (
    "gitlab.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()
    ctx.AddComponent("http", gocamel.NewHTTPComponent())

    route := ctx.CreateRouteBuilder().
        From("http://localhost:8080/hello").
        SetBody("Hello ${header.name}!").
        Build()

    ctx.AddRoute(route)

    // API de gestion
    mgmt := gocamel.NewManagementServer(ctx)
    mgmt.Start("127.0.0.1:8081")

    ctx.Start()
    select {}
}
```

### Consommateur HTTP avec traitement

```go
route := ctx.CreateRouteBuilder().
    From("http://localhost:8080/api/process").
    Choice().
        When("${header.Content-Type} == 'application/json'").
            ProcessFunc(func(e *gocamel.Exchange) error {
                // Traiter le JSON
                e.GetOut().SetBody(`{"status":"ok"}`)
                e.GetOut().SetHeader("Content-Type", "application/json")
                return nil
            }).
        Otherwise().
            SetBody("Type de contenu non supporté").
    EndChoice().
    Build()
```

---

## Intégration FTP

### Téléchargement FTP

```go
package main

import (
    "gitlab.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()
    ctx.AddComponent("ftp", gocamel.NewFTPComponent())

    // Télécharger depuis FTP
    route := ctx.CreateRouteBuilder().
        From("ftp://ftp.example.com/incoming?delete=true&passiveMode=true").
        Log("Téléchargé: ${header.CamelFileName}").
        To("file://downloads").
        Build()

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

### SFTP avec authentification par clé

```go
route := ctx.CreateRouteBuilder().
    From("sftp://secure.example.com/data?username=admin").
    SetHeader("CamelFileName", "${header.CamelFileName}.secure").
    To("file://secure-downloads").
    Build()
```

---

## Routage basé sur le contenu

### Routage par choix (Choice)

```go title="choice_routing.go"
package main

import (
    "gitlab.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    route := ctx.CreateRouteBuilder().
        From("direct:start").
        Choice().
            When("${header.priority == 'high'}").
                SimpleSetBody("🚨 HIGH PRIORITY: ${body}").
                SetHeader("X-Urgent", "true").
                To("direct:urgent-queue").
            When("${header.priority == 'medium'}").
                SimpleSetBody("⚠️ MEDIUM: ${body}").
                To("direct:normal-queue").
            When("${body['count'] > 100}").
                SimpleSetBody("📦 LARGE BATCH: ${body['count']} items").
                To("direct:batch-queue").
            Otherwise().
                SimpleSetBody("📄 LOW: ${body}").
                To("direct:low-queue").
        EndChoice().
        Build()

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

---

## Langage Simple

### Transformation avec expressions Simple

```go title="simple_transformation.go"
package main

import (
    "gitlab.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    route := ctx.CreateRouteBuilder().
        From("direct:start").
        SimpleSetBody("📨 Message: ${body}").
        SimpleSetHeader("X-Timestamp", "${date:now}").
        SimpleSetHeader("X-Request-ID", "${uuid}").
        Log("Traitement à ${header.X-Timestamp}").
        To("direct:output").
        Build()

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

### Traitement JSON avec Simple Language

```go title="json_processing.go"
package main

import (
    "gitlab.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    // Body attendu: {"user": {"name": "John", "email": "john@example.com"}}
    route := ctx.CreateRouteBuilder().
        From("direct:api").
        Choice().
            When("${header.Content-Type == 'application/json'}").
                SimpleSetHeader("X-User-Name", "${body['user']['name']}").
                SimpleSetHeader("X-User-Email", "${body['user']['email']}").
                SimpleSetBody("👤 User ${body['user']['name']} registered").
                Log("📝 Enregistrement utilisateur: ${body}").
                To("direct:process-json").
            When("${header.Content-Type == 'text/plain'}").
                SimpleSetBody("📝 Message reçu: ${body}").
                To("direct:process-text").
            Otherwise().
                SimpleSetBody("❌ Type non supporté: ${body}").
                To("direct:error").
        EndChoice().
        Build()

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

### Null-safe et Collections

```go title="null_safe_collection.go"
package main

import (
    "gitlab.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    // Body: [{"name": "A"}, {"name": "B"}, {"name": "C"}]
    route := ctx.CreateRouteBuilder().
        From("direct:start").
        Log("Premier: ${body[0]['name']}").
        Log("Dernier: ${body[last]['name']}").
        Log("User (null-safe): ${body?.user?.name}").
        SimpleSetHeader("X-First-Item", "${body[0]['name']}").
        SimpleSetHeader("X-Last-Item", "${body[last]['name']}").
        To("direct:output").
        Build()

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

---

## Découpage de messages

### Traitement CSV

```go
route := ctx.CreateRouteBuilder().
    From("file://csv-input").
    Split(func(e *gocamel.Exchange) (any, error) {
        body := e.GetIn().GetBody().(string)
        // Découper le CSV en lignes
        return strings.Split(body, "\n"), nil
    }).
    Log("Traitement ligne ${in.header.CamelSplitIndex}/${in.header.CamelSplitSize}").
    ProcessFunc(func(e *gocamel.Exchange) error {
        // Traiter chaque ligne
        return nil
    }).
    To("direct:processed").
    End(). // Fin du split
    Log("Toutes les lignes traitées").
    Build()
```

---

## Agrégation

### Split & Aggregate

```go title="split_aggregate.go"
package main

import (
    "strings"
    "gitlab.com/tranchida/gocamel"
)

type StringConcatStrategy struct{}

func (s *StringConcatStrategy) Aggregate(
    oldExchange,
    newExchange *gocamel.Exchange,
) *gocamel.Exchange {
    if oldExchange == nil {
        return newExchange
    }
    oldBody := oldExchange.GetOut().GetBody().(string)
    newBody := newExchange.GetOut().GetBody().(string)
    oldExchange.GetOut().SetBody(oldBody + newBody)
    return oldExchange
}

func main() {
    ctx := gocamel.NewCamelContext()

    strategy := &StringConcatStrategy{}
    repo := gocamel.NewMemoryAggregationRepository()

    route := ctx.CreateRouteBuilder().
        From("direct:start").
        Split(func(e *gocamel.Exchange) (any, error) {
            body := e.GetIn().GetBody().(string)
            return strings.Split(body, ","), nil
        }).
        To("direct:transform").
        End().
        Aggregate(gocamel.NewAggregator(
            func(e *gocamel.Exchange) string { return "group" },
            strategy,
            repo,
        ).SetCompletionSize(3)).
        Build()

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

### Agrégation de commandes

```go
type OrderAggregationStrategy struct{}

func (s *OrderAggregationStrategy) Aggregate(
    oldEx, newEx *gocamel.Exchange,
) *gocamel.Exchange {
    if oldEx == nil {
        return newEx
    }

    // Combiner les commandes avec le même ID
    old := oldEx.GetIn().GetBody()
    new := newEx.GetIn().GetBody()

    combined := fmt.Sprintf("%s + %s", old, new)
    oldEx.GetIn().SetBody(combined)
    return oldEx
}

// Utilisation
strategy := &OrderAggregationStrategy{}
repo := gocamel.NewMemoryAggregationRepository()

aggregator := gocamel.NewAggregator("${header.orderId}", strategy, repo).
    SetCompletionSize(3).
    SetCompletionTimeout(30000)

route := ctx.CreateRouteBuilder().
    From("direct:line-items").
    Aggregate(aggregator).
        Log("Commande complète: ${body}").
        To("direct:fulfillment").
        End().
    Build()
```

---

## Multicast

### Traitement parallèle

```go
route := ctx.CreateRouteBuilder().
    From("direct:incoming").
    Multicast().ParallelProcessing().
        To("direct:archive").
        To("direct:audit").
        To("direct:cache").
    End().
    Log("Multicast terminé").
    Build()
```

---

## Tâches planifiées

### Timer

```go title="timer.go"
package main

import (
    "gitlab.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    // Exécuter toutes les 5 secondes
    route := ctx.CreateRouteBuilder().
        From("timer:tick?period=5s").
        Log("Tick à ${date:now}").
        Build()

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

### Expression Cron

```go
ctx.AddComponent("cron", gocamel.NewCronComponent())

// Exécuter chaque minute
route := ctx.CreateRouteBuilder().
    From("cron://group/minutely?cron=0+*+*+*+*+*").
    Log("Déclencheur cron à ${header.fireTime}").
    Build()
```

---

## Intégration de bases de données

### Requête SQL

```go
import (
    "database/sql"
    _ "modernc.org/sqlite"
    "gitlab.com/tranchida/gocamel"
)

func main() {
    db, _ := sql.Open("sqlite", "app.db")
    defer db.Close()

    ctx := gocamel.NewCamelContext()

    sqlComp := gocamel.NewSQLComponent()
    sqlComp.RegisterDataSource("appdb", db)
    ctx.AddComponent("sql", sqlComp)

    route := ctx.CreateRouteBuilder().
        From("timer:poll?period=60s").
        To("sql://appdb?query=SELECT+*+FROM+events+WHERE+processed=false").
        Split().
            Log("Événement: ${body}").
            To("direct:process-event").
        End().
        Build()

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

### Insertion SQL

```go
route := ctx.CreateRouteBuilder().
    From("direct:new-user").
    SetHeader("CamelSqlParameters", []any{
        "${header.name}",
        "${header.email}",
    }).
    To("sql://appdb?query=INSERT+INTO+users(name,email)+VALUES(?,?)").
    Log("Utilisateur créé, lignes affectées: ${header.CamelSqlRowCount}").
    Build()
```

---

## Bot Telegram

```go title="telegram_bot.go"
package main

import (
    "gitlab.com/tranchida/gocamel"
)

func main() {
    // Définir la variable d'environnement TELEGRAM_AUTHORIZATIONTOKEN
    ctx := gocamel.NewCamelContext()
    ctx.AddComponent("telegram", gocamel.NewTelegramComponent())

    route := ctx.CreateRouteBuilder().
        From("telegram:bots").
        Log("Message de ${header.chatId}: ${body}").
        SetBody("Echo: ${body}").
        To("telegram:bots").
        Build()

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

---

## Traitement de templates

Génération de messages formatés avec templates Go natifs (inspiré du composant Velocity d'Apache Camel).

```go title="template_email.go"
package main

import (
    "gitlab.com/tranchida/gocamel"
)

// templates/email.tmpl:
// Bonjour {{.Headers.name}},
//
// {{.Body}}
//
// Envoyé le {{now | formatDate "2006-01-02"}}

func main() {
    ctx := gocamel.NewCamelContext()
    ctx.AddComponent("template", gocamel.NewTemplateComponent())

    route := ctx.CreateRouteBuilder().
        From("direct:notify").
        SetHeader("name", "Alice").
        SetBody("Votre commande est prête !").
        To("template:templates/email.tmpl").
        Log("Email généré: ${body}").
        Build()

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

### Avec cache du template

```go
To("template:templates/email.tmpl?contentCache=true")
```

### Fonctions disponibles dans les templates

| Fonction     | Description         | Exemple   |
|--------------|---------------------|-----------|
| `upper`      | Majuscules          | `{{.Body  | upper}}` |
| `lower`      | Minuscules          | `{{.Body  | lower}}` |
| `trim`       | Supprime espaces    | `{{.Body  | trim}}` |
| `now`        | Date actuelle       | `{{now}}` |
| `formatDate` | Formate date        | `{{now    | formatDate "2006-01-02"}}` |
| `toString`   | Convertit en string | `{{.Body  | toString}}` |
| `safeHTML`   | HTML non échappé    | `{{.Body  | safeHTML}}` |

```

---

## Gestion des erreurs

```go
route := ctx.CreateRouteBuilder().
    From("direct:process").
    DoTry().
        ProcessFunc(func(e *gocamel.Exchange) error {
            // Opération risquée
            return riskyOperation(e)
        }).
    DoCatch(Exception.class).
        Log("Erreur: ${exception.message}").
        To("direct:error-handler").
    EndDoTry().
    Build()
```

---

## Application complète

### Processeur de fichiers avec toutes les fonctionnalités

```go
package main

import (
    "gitlab.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    // Route de traitement principale
    mainRoute := ctx.CreateRouteBuilder().
        SetID("file-processor").
        From("file://input?include=*.json&delete=true").
        Log("Traitement: ${header.CamelFileName}").

        // Parser le JSON
        ProcessFunc(func(e *gocamel.Exchange) error {
            // Parser le JSON ici
            return nil
        }).

        // Router selon le type
        Choice().
            When("${body.type} == 'order'").
                To("direct:process-order").
            When("${body.type} == 'refund'").
                To("direct:process-refund").
            Otherwise().
                To("direct:unknown").
        EndChoice().

        Build()

    // Traitement des commandes
    orderRoute := ctx.CreateRouteBuilder().
        SetID("order-processor").
        From("direct:process-order").
        Log("Traitement commande: ${body.id}").
        To("file://output/orders").
        Build()

    // Traitement des remboursements
    refundRoute := ctx.CreateRouteBuilder().
        SetID("refund-processor").
        From("direct:process-refund").
        Log("Traitement remboursement: ${body.id}").
        To("file://output/refunds").
        Build()

    // Ajouter les routes
    ctx.AddRoute(mainRoute)
    ctx.AddRoute(orderRoute)
    ctx.AddRoute(refundRoute)

    // Gestion
    mgmt := gocamel.NewManagementServer(ctx)
    mgmt.Start("127.0.0.1:8081")

    ctx.Start()
    select {}
}
```