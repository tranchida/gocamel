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
    "log"

    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    // period et delay du timer sont des entiers en millisecondes (défaut 1000)
    route, err := ctx.CreateRouteBuilder().
        From("timer:tick?period=5000").
        SetBody("Hello GoCamel!").
        LogSimple("${body}").
        Build()
    if err != nil {
        log.Fatal(err)
    }

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

## Intégration OpenAI

```go title="openai_example.go"
package main

import (
    "context"
    "fmt"

    "github.com/tranchida/gocamel"
    "github.com/tranchida/gocamel/components/openai"
)

func main() {
    ctx := gocamel.NewCamelContext()
    ctx.AddComponent("openai", openai.NewOpenAIComponent())

    // Utilisation manuelle du producer (lit OPENAI_API_KEY depuis l'environnement)
    endpoint, _ := ctx.CreateEndpoint("openai:chat?model=gpt-3.5-turbo")
    producer, _ := endpoint.CreateProducer()
    _ = producer.Start(context.Background())
    defer producer.Stop()

    exchange := gocamel.NewExchange(context.Background())
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
    "log"

    "github.com/tranchida/gocamel"
    "github.com/tranchida/gocamel/components/file"
)

func main() {
    ctx := gocamel.NewCamelContext()
    // file est un module externe (fsnotify) et doit être enregistré
    ctx.AddComponent("file", file.NewFileComponent())

    // Déplacer les fichiers de input vers output
    route, err := ctx.CreateRouteBuilder().
        From("file://input?delete=true").
        LogSimple("Traitement: ${header.CamelFileName}").
        To("file://output").
        Build()
    if err != nil {
        log.Fatal(err)
    }

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

### Fichier vers Fichier avec transformation

```go
// nécessite : ctx.AddComponent("file", file.NewFileComponent())
route, err := ctx.CreateRouteBuilder().
    From("file://input?include=*.txt&delete=true").
    ProcessFunc(func(e *gocamel.Exchange) error {
        content := e.GetIn().GetBody().(string)
        // Transformer le contenu
        e.GetOut().SetBody(strings.ToUpper(content))
        return nil
    }).
    SimpleSetHeader("CamelFileName", "${header.CamelFileName}.processed").
    To("file://output").
    Build()
if err != nil {
    log.Fatal(err)
}
```

---

## Endpoints HTTP

### Serveur Echo HTTP

```go title="http_echo.go"
package main

import (
    "log"

    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()
    ctx.AddComponent("http", gocamel.NewHTTPComponent())

    route, err := ctx.CreateRouteBuilder().
        From("http://localhost:8080/hello").
        SimpleSetBody("Hello ${header.name}!").
        Build()
    if err != nil {
        log.Fatal(err)
    }

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
route, err := ctx.CreateRouteBuilder().
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
if err != nil {
    log.Fatal(err)
}
```

---

## Intégration FTP

### Téléchargement FTP

```go
package main

import (
    "log"

    "github.com/tranchida/gocamel"
    "github.com/tranchida/gocamel/components/file"
    "github.com/tranchida/gocamel/components/ftp"
)

func main() {
    ctx := gocamel.NewCamelContext()
    ctx.AddComponent("ftp", ftp.NewFTPComponent())
    ctx.AddComponent("file", file.NewFileComponent())

    // Télécharger depuis FTP — FTP_USERNAME / FTP_PASSWORD sont lues depuis l'environnement
    route, err := ctx.CreateRouteBuilder().
        From("ftp://ftp.example.com/incoming?delete=true&passiveMode=true").
        LogSimple("Téléchargé: ${header.CamelFileName}").
        To("file://downloads").
        Build()
    if err != nil {
        log.Fatal(err)
    }

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

### SFTP avec authentification par clé

```go
// nécessite : ctx.AddComponent("sftp", sftp.NewSFTPComponent())
//             ctx.AddComponent("file", file.NewFileComponent())
route, err := ctx.CreateRouteBuilder().
    From("sftp://secure.example.com/data?username=admin").
    SimpleSetHeader("CamelFileName", "${header.CamelFileName}.secure").
    To("file://secure-downloads").
    Build()
if err != nil {
    log.Fatal(err)
}
```

---

## Routage basé sur le contenu

### Routage par choix (Choice)

```go title="choice_routing.go"
package main

import (
    "log"

    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    route, err := ctx.CreateRouteBuilder().
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
    if err != nil {
        log.Fatal(err)
    }

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
    "log"

    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    route, err := ctx.CreateRouteBuilder().
        From("direct:start").
        SimpleSetBody("📨 Message: ${body}").
        SimpleSetHeader("X-Timestamp", "${date:now}").
        SimpleSetHeader("X-Request-ID", "${uuid}").
        LogSimple("Traitement à ${header.X-Timestamp}").
        To("direct:output").
        Build()
    if err != nil {
        log.Fatal(err)
    }
    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

### Traitement JSON avec Simple Language

```go title="json_processing.go"
package main

import (
    "log"

    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    // Body attendu: {"user": {"name": "John", "email": "john@example.com"}}
    route, err := ctx.CreateRouteBuilder().
        From("direct:api").
        Choice().
            When("${header.Content-Type == 'application/json'}").
                SimpleSetHeader("X-User-Name", "${body['user']['name']}").
                SimpleSetHeader("X-User-Email", "${body['user']['email']}").
                SimpleSetBody("👤 User ${body['user']['name']} registered").
                LogSimple("📝 Enregistrement utilisateur: ${body}").
                To("direct:process-json").
            When("${header.Content-Type == 'text/plain'}").
                SimpleSetBody("📝 Message reçu: ${body}").
                To("direct:process-text").
            Otherwise().
                SimpleSetBody("❌ Type non supporté: ${body}").
                To("direct:error").
        EndChoice().
        Build()
    if err != nil {
        log.Fatal(err)
    }
    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

### Null-safe et Collections

```go title="null_safe_collection.go"
package main

import (
    "log"

    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    // Body: [{"name": "A"}, {"name": "B"}, {"name": "C"}]
    route, err := ctx.CreateRouteBuilder().
        From("direct:start").
        LogSimple("Premier: ${body[0]['name']}").
        LogSimple("Dernier: ${body[last]['name']}").
        LogSimple("User (null-safe): ${body?.user?.name}").
        SimpleSetHeader("X-First-Item", "${body[0]['name']}").
        SimpleSetHeader("X-Last-Item", "${body[last]['name']}").
        To("direct:output").
        Build()
    if err != nil {
        log.Fatal(err)
    }
    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

---

## Découpage de messages

### Traitement CSV

```go
// nécessite : ctx.AddComponent("file", file.NewFileComponent())
route, err := ctx.CreateRouteBuilder().
    From("file://csv-input").
    Split(func(e *gocamel.Exchange) (any, error) {
        body := e.GetIn().GetBody().(string)
        // Découper le CSV en lignes
        return strings.Split(body, "\n"), nil
    }).
    // CamelSplitIndex/CamelSplitSize sont des propriétés de l'exchange, pas des en-têtes
    LogSimple("Traitement ligne ${exchangeProperty.CamelSplitIndex}/${exchangeProperty.CamelSplitSize}").
    ProcessFunc(func(e *gocamel.Exchange) error {
        // Traiter chaque ligne
        return nil
    }).
    To("direct:processed").
    End(). // Fin du split
    Log("Toutes les lignes traitées").
    Build()
if err != nil {
    log.Fatal(err)
}
```

---

## Agrégation

### Split & Aggregate

```go title="split_aggregate.go"
package main

import (
    "log"
    "strings"

    "github.com/tranchida/gocamel"
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

    route, err := ctx.CreateRouteBuilder().
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
    if err != nil {
        log.Fatal(err)
    }

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

aggregator := gocamel.NewAggregator(
    func(e *gocamel.Exchange) string {
        orderId, _ := e.GetHeaderAsString("orderId")
        return orderId
    },
    strategy,
    repo,
).
    SetCompletionSize(3).
    SetCompletionTimeout(30 * time.Second)

route, err := ctx.CreateRouteBuilder().
    From("direct:line-items").
    Aggregate(aggregator).
    LogSimple("Commande complète: ${body}").
    To("direct:fulfillment").
    Build()
if err != nil {
    log.Fatal(err)
}
```

---

## Multicast

### Traitement parallèle

```go
route, err := ctx.CreateRouteBuilder().
    From("direct:incoming").
    Multicast().ParallelProcessing().
        To("direct:archive").
        To("direct:audit").
        To("direct:cache").
    End().
    Log("Multicast terminé").
    Build()
if err != nil {
    log.Fatal(err)
}
```

---

## Tâches planifiées

### Timer

```go title="timer.go"
package main

import (
    "log"

    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    // Exécuter toutes les 5 secondes (la période du timer est en millisecondes)
    route, err := ctx.CreateRouteBuilder().
        From("timer:tick?period=5000").
        LogSimple("Tick à ${date:now}").
        Build()
    if err != nil {
        log.Fatal(err)
    }

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

### Expression Cron

```go
import "github.com/tranchida/gocamel/components/cron"

ctx.AddComponent("cron", cron.NewCronComponent())

// Exécuter chaque minute
route, err := ctx.CreateRouteBuilder().
    From("cron://group/minutely?cron=0+*+*+*+*+*").
    LogSimple("Déclencheur cron à ${header.fireTime}").
    Build()
if err != nil {
    log.Fatal(err)
}
```

---

## Intégration de bases de données

### Requête SQL

```go
import (
    "database/sql"
    "log"

    _ "modernc.org/sqlite"
    "github.com/tranchida/gocamel"
)

func main() {
    db, _ := sql.Open("sqlite", "app.db")
    defer db.Close()

    ctx := gocamel.NewCamelContext()

    sqlComp := gocamel.NewSQLComponent()
    sqlComp.RegisterDataSource("appdb", db)
    ctx.AddComponent("sql", sqlComp)

    route, err := ctx.CreateRouteBuilder().
        From("timer:poll?period=60000"). // période en ms : chaque minute
        To("sql://appdb?query=SELECT+*+FROM+events+WHERE+processed=false").
        Split(func(e *gocamel.Exchange) (any, error) {
            // Out.Body est []map[string]any (SelectList)
            return e.GetIn().GetBody(), nil
        }).
            LogSimple("Événement: ${body}").
            To("direct:process-event").
        End().
        Build()
    if err != nil {
        log.Fatal(err)
    }

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

### Insertion SQL

```go
route, err := ctx.CreateRouteBuilder().
    From("direct:new-user").
    ProcessFunc(func(e *gocamel.Exchange) error {
        // Lier les valeurs du message en paramètres positionnels (jamais d'interpolation)
        name, _ := e.GetHeaderAsString("name")
        email, _ := e.GetHeaderAsString("email")
        e.GetIn().SetHeader(gocamel.SqlParameters, []any{name, email})
        return nil
    }).
    To("sql://appdb?query=INSERT+INTO+users(name,email)+VALUES(?,?)").
    LogSimple("Utilisateur créé, lignes affectées: ${header.CamelSqlRowCount}").
    Build()
if err != nil {
    log.Fatal(err)
}
```

---

## Bot Telegram

```go title="telegram_bot.go"
package main

import (
    "log"

    "github.com/tranchida/gocamel"
    "github.com/tranchida/gocamel/components/telegram"
)

func main() {
    // Définir la variable d'environnement TELEGRAM_AUTHORIZATIONTOKEN
    ctx := gocamel.NewCamelContext()
    ctx.AddComponent("telegram", telegram.NewTelegramComponent())

    route, err := ctx.CreateRouteBuilder().
        From("telegram:bots").
        LogSimple("Message de ${header.chatId}: ${body}").
        SimpleSetBody("Echo: ${body}").
        To("telegram:bots").
        Build()
    if err != nil {
        log.Fatal(err)
    }

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
    "log"

    "github.com/tranchida/gocamel"
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

    route, err := ctx.CreateRouteBuilder().
        From("direct:notify").
        SetHeader("name", "Alice").
        SetBody("Votre commande est prête !").
        To("template:templates/email.tmpl").
        LogSimple("Email généré: ${body}").
        Build()
    if err != nil {
        log.Fatal(err)
    }

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
| `json`       | JSON sûr en HTML    | `{{.Body  | toString | json}}` |
| `safeHTML`   | HTML non échappé    | `{{.Body  | safeHTML}}` |

---

## Gestion des erreurs

```go
route, err := ctx.CreateRouteBuilder().
    From("direct:process").
    DoTry().
        ProcessFunc(func(e *gocamel.Exchange) error {
            // Opération risquée
            return riskyOperation(e)
        }).
    DoCatch("connection refused"). // correspondance sur le texte de l'erreur
        Log("Service injoignable, application du mode dégradé").
        To("direct:recovery").
    DoCatch(""). // rattrape toutes les autres erreurs
        LogSimple("Erreur: ${exchangeProperty.CamelExceptionCaught}").
        To("direct:error-handler").
    End().
    Build()
if err != nil {
    log.Fatal(err)
}
```

---

## Application complète

### Processeur de fichiers avec toutes les fonctionnalités

```go
package main

import (
    "log"

    "github.com/tranchida/gocamel"
    "github.com/tranchida/gocamel/components/file"
)

func main() {
    ctx := gocamel.NewCamelContext()
    ctx.AddComponent("file", file.NewFileComponent())

    // Route de traitement principale
    mainRoute, err := ctx.CreateRouteBuilder().
        SetID("file-processor").
        From("file://input?include=*.json&delete=true").
        LogSimple("Traitement: ${header.CamelFileName}").

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
    if err != nil {
        log.Fatal(err)
    }

    // Traitement des commandes
    orderRoute, err := ctx.CreateRouteBuilder().
        SetID("order-processor").
        From("direct:process-order").
        LogSimple("Traitement commande: ${body.id}").
        To("file://output/orders").
        Build()
    if err != nil {
        log.Fatal(err)
    }

    // Traitement des remboursements
    refundRoute, err := ctx.CreateRouteBuilder().
        SetID("refund-processor").
        From("direct:process-refund").
        LogSimple("Traitement remboursement: ${body.id}").
        To("file://output/refunds").
        Build()
    if err != nil {
        log.Fatal(err)
    }

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