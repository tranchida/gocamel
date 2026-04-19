# Simple Language

Le **Simple Language** est le moteur d'expressions de GoCamel, inspiré du Simple Language d'Apache Camel. Il permet d'évaluer des expressions dynamiques à l'exécution pour accéder aux données des échanges, manipuler les en-têtes, et effectuer des routages basés sur le contenu.

## Vue d'ensemble

Le Simple Language utilise la syntaxe `${expression}` pour encapsuler les expressions qui seront évaluées.

```go
// Exemple simple
template := gocamel.ParseSimpleTemplate("Bonjour ${body}")
result, _ := template.Evaluate(exchange) // "Bonjour John"
```

## Variables de référence

| Variable | Syntaxe | Description | Exemple |
|----------|---------|-------------|---------|
| **body** | `${body}` | Corps du message | `${body}` |
| **header** | `${header.nom}` | En-tête HTTP/propriété | `${header.Content-Type}` |
| **exchangeProperty** | `${exchangeProperty.nom}` | Propriété de l'Exchange | `${exchangeProperty.userId}` |
| **variable** | `${variable.nom}` | Variable personnalisée | `${variable.counter}` |

## Accès aux données

### Notation par point

Pour accéder aux champs des maps et des structs via la notation par point :

```go
// Accès direct
${body}
${header.X-Request-ID}
${exchangeProperty.sessionId}

// Accès à une propriété du body (si le body est une map ou struct)
${body.user.name}
${body.address.city}
```

### Notation par crochets

Pour accéder aux éléments via des clés dynamiques ou des index numériques :

```go
// Accès à une clé de map avec espaces ou caractères spéciaux
${body['key with spaces']}
${body["email"]}

// Accès à un élément de slice/array par index
${body[0]}
${body[users][0][name]}

// Index spécial 'last' pour le dernier élément
${body[last]}
${body[last-1]}

// Accès chaîné mixte
${body['users'][0]['roles'][last]}
```

### Opérateur null-safe (?.)

L'opérateur `?.` permet d'accéder en toute sécurité aux propriétés sans risquer de panic sur une valeur nil :

```go
// Si body ou user est nil, retourne nil sans panic
${body?.user?.profile?.name}

// Null-safe sur les headers
${header?.X-Optional-Header}

// Null-safe sur les propriétés
${exchangeProperty?.optional?.value}
```

!!! tip "Bonnes pratiques"
    Utilisez l'opérateur `?.` lorsque vous n'êtes pas certain que les données parentes existent. Cela évite les erreurs de runtime.

## Fonctions intégrées

| Fonction | Description | Exemple |
|----------|-------------|---------|
| `${date:now}` | Date/heure actuelle (RFC3339) | `${date:now}` |
| `${date:now:FORMAT}` | Date/heure avec format personnalisé | `${date:now:2006-01-02}` |
| `${random(MAX)}` | Nombre aléatoire entre 0 et MAX-1 | `${random(100)}` |
| `${uuid}` | Génère un UUID v4 | `${uuid}` |

### Exemples de dates

```go
// Format Go standard
${date:now:2006-01-02}                    // 2026-01-15
${date:now:January 2, 2006}                // January 15, 2026
${date:now:2006-01-02 15:04:05}           // 2026-01-15 14:30:45
${date:now:Mon, 02 Jan 2006 15:04:05 MST} // Format RFC1123
```

## Comparaisons

Le Simple Language supporte les opérateurs de comparaison pour les conditions :

| Opérateur | Description | Exemple |
|-----------|-------------|---------|
| `==` | Égal à | `${body == 'active'}` |
| `!=` | Différent de | `${header.count != 0}` |
| `>` | Supérieur à | `${header.priority > 5}` |
| `<` | Inférieur à | `${header.count < 100}` |
| `>=` | Supérieur ou égal | `${header.count >= 10}` |
| `<=` | Inférieur ou égal | `${header.count <= 50}` |

```go
// Comparaisons numériques
${header.count > 100}
${exchangeProperty.total >= 1000}

// Comparaisons de chaînes
${body == 'hello'}
${header.Content-Type != 'application/json'}

// Combinaison avec accès par crochets
${body['status'] == 'active'}
${body[0] > 50}
```

!!! note "Comparaison mixte"
    Les comparaisons entre nombres et chaînes sont supportées. GoCamel tente d'abord une comparaison numérique, puis une comparaison lexicographique si les valeurs ne sont pas numériques.

## Utilisation dans les routes

### SimpleSetBody

Définit le corps du message à partir d'une expression Simple :

```go
builder.From("direct:start").
    SimpleSetBody("Received: ${body} at ${date:now}").
    To("direct:output")
```

### SimpleSetHeader

Définit un en-tête à partir d'une expression Simple :

```go
builder.From("direct:start").
    SimpleSetHeader("X-Request-ID", "${uuid}").
    SimpleSetHeader("X-Timestamp", "${date:now:RFC3339}").
    SimpleSetHeader("X-User", "${exchangeProperty.userId}").
    To("direct:output")
```

### Log

Les expressions Simple peuvent être utilisées dans les logs :

```go
builder.From("direct:start").
    Log("Processing message from ${header.X-Client-ID}: ${body}").
    To("direct:output")
```

### ToD (To Dynamic)

L'URI est résolue dynamiquement avec interpolation :

```go
builder.From("direct:start").
    SetHeader("CamelFileName", "${exchangeProperty.fileName}").
    ToD("file://output/${header.CamelFileName}")
```

## Pattern Choice (Content-Based Router)

Le Simple Language est utilisé dans le pattern Choice pour le routage basé sur le contenu :

```go
builder.From("direct:start").
    Choice().
        When("${header.priority == 'high'}").
            SimpleSetBody("HIGH: ${body}").
            To("direct:high-priority").
        When("${header.priority == 'medium'}").
            SimpleSetBody("MEDIUM: ${body}").
            To("direct:medium-priority").
        When("${body['count'] > 100}").
            To("direct:large-batch").
        Otherwise().
            To("direct:default").
    EndChoice()
```

### Chaining dans Choice

```go
builder.Choice().
    When("${header.Content-Type == 'application/json'}").
        SimpleSetHeader("X-Processor", "JSON").
        SimpleSetBody("Processing JSON: ${body[0]['name']}").
        To("direct:process-json").
    When("${header.Content-Type == 'text/xml'}").
        SetHeader("X-Processor", "XML").
        To("direct:process-xml").
    Otherwise().
        SetHeader("X-Processor", "UNKNOWN").
        To("direct:process-generic").
EndChoice()
```

## API Programmatique

### ParseSimpleTemplate

Pour utiliser le Simple Language en dehors des routes :

```go
// Créer un template
template, err := gocamel.ParseSimpleTemplate("Hello ${body}")
if err != nil {
    log.Fatal(err)
}

// Évaluer le template
result, err := template.Evaluate(exchange)
fmt.Println(result) // "Hello John"

// Évaluer comme chaîne
str, _ := template.EvaluateAsString(exchange)

// Évaluer comme booléen (pour les conditions)
boolean, _ := template.EvaluateAsBool(exchange)
```

### Création d'expressions personnalisées

```go
// ExpressionFunc
expr := gocamel.ExpressionFunc(func(e *gocamel.Exchange) (interface{}, error) {
    return e.GetIn().GetBody(), nil
})

// Utilisation
template, _ := gocamel.ParseSimpleTemplate("${uuid}")
result, _ := template.Evaluate(exchange)
```

## Exemples complets

### Exemple 1: Transformation de message

```go
package main

import (
    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()
    
    route := ctx.CreateRouteBuilder().
        From(" direct:start").
        SetProperty("startTime", time.Now()).
        SimpleSetBody("Request: ${body}").
        SimpleSetHeader("X-Trace-ID", "${uuid}").
        SimpleSetHeader("X-Started-At", "${exchangeProperty.startTime}").
        Log("Processing ${header.X-Trace-ID}").
        To("direct:output").
        Build()
    
    ctx.AddRoute(route)
}
```

### Exemple 2: Routage basé sur le contenu

```go
route := ctx.CreateRouteBuilder().
    From("direct:start").
    Choice().
        When("${header.status == 'error'}").
            SimpleSetBody("❌ Error: ${body}").
            To("direct:error-handler").
        When("${body['priority'] >= 5}").
            SimpleSetHeader("X-Urgent", "true").
            To("direct:urgent").
        When("${body['users'][0]['role'] == 'admin'}").
            To("direct:admin-queue").
        Otherwise().
            To("direct:normal-queue").
    EndChoice().
    Build()
```

### Exemple 3: Accès aux données JSON

```go
// Body: {"user": {"name": "John", "email": "john@example.com"}}
builder.From("direct:json").
    SimpleSetHeader("X-User-Name", "${body['user']['name']}").
    SimpleSetHeader("X-User-Email", "${body['user']['email']}").
    Log("User ${body['user']['name']} registered").
    To("direct:register")
```

### Exemple 4: Traitement de collections

```go
// Body: [{"name": "A"}, {"name": "B"}, {"name": "C"}]
builder.From("direct:collection").
    Log("First item: ${body[0]['name']}").
    Log("Last item: ${body[last]['name']}").
    Log("Second to last: ${body[last-1]['name']}").
    To("direct:process")
```

## Comparaison avec Apache Camel

| Fonctionnalité | GoCamel | Apache Camel |
|----------------|---------|--------------|
| `${body}` | ✅ | ✅ |
| `${header.name}` | ✅ | ✅ |
| `${exchangeProperty.name}` | ✅ | ✅ |
| Notation par crochets | ✅ | ✅ |
| Opérateur null-safe | ✅ (`?.`) | ✅ (`?.`) |
| `${date:now}` | ✅ | ✅ |
| `${random()}` | ✅ | ✅ |
| `${uuid}` | ✅ | ✅ |
| Comparaisons | ✅ | ✅ |
| `${variable}` | ✅ | ✅ |

## Erreurs communes

```go
// ❌ Erreur: clé non trouvée retourne nil, pas une erreur
${body.nonexistent} // retourne nil

// ✅ Solution: utiliser l'opérateur null-safe
${body?.nonexistent} // retourne nil sans panic

// ❌ Erreur: index hors limites
${body[100]} // retourne nil

// ✅ Solution: vérifier la taille avant
${exchangeProperty.size > 100}

// ❌ Erreur: nom d'en-tête avec tiret
${header.X-Client-ID} // ❌

// ✅ Solution: utiliser les crochets
${header['X-Client-ID']} // ✅
${header.X-Client-ID}    // ✅ aussi supporté
```

## Référence rapide

### Syntaxe de base

```
${body}                          Corps du message
${body.field}                    Propriété du body (map/struct)
${body['key']}                   Accès par clé
${body[index]}                   Accès par index
${body?.field}                   Accès null-safe
${header.name}                   En-tête
${header['X-Custom']}            En-tête avec caractères spéciaux
${exchangeProperty.prop}         Propriété de l'Exchange
${date:now}                      Date/heure actuelle
${date:now:FORMAT}               Date/heure formatée
${random(MAX)}                   Nombre aléatoire 0..MAX-1
${uuid}                          UUID v4
```

### Opérateurs de comparaison

```
==  Égal à
!=  Différent de
>   Supérieur à
<   Inférieur à
>=  Supérieur ou égal
<=  Inférieur ou égal
```

### Fonctions RouteBuilder

```go
SimpleSetBody(expression string)
SimpleSetHeader(headerName, expression string)
Choice().When(expression).(...).EndChoice()
```
