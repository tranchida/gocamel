# Simple Language | Simple Language

<p align="center">
  Dynamic Expression Language for GoCamel | Langage d'Expressions Dynamiques pour GoCamel
</p>

---

# 🇺🇸 English

## Overview

Simple Language is a dynamic expression language inspired by Apache Camel. It allows you to embed expression placeholders in strings, enabling dynamic routing, message transformation, and conditional processing.

## Syntax

Expressions are enclosed in `${}`:

```go
"Hello ${body}"                    // Body content
"Priority: ${header.priority}"     // Header value
"ID: ${exchangeProperty.id}"       // Exchange property
```

## Built-in Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `${body}` | Message body | `${body}` |
| `${header.name}` | Header value | `${header.Content-Type}` |
| `${exchangeProperty.name}` | Exchange property | `${exchangeProperty.correlationId}` |

## Built-in Functions

| Function | Description | Example |
|----------|-------------|---------|
| `${date:now}` | Current timestamp | `${date:now:yyyy-MM-dd}` |
| `${random(max)}` | Random number | `${random(100)}` |
| `${uuid}` | UUID generation | `${uuid}` |
| `${env:VAR}` | Environment variable | `${env:USER}` |

## Operators

### Comparison Operators

```go
${body == 'active'}           // Equality
${body != 'inactive'}          // Inequality
${header.count > 10}          // Greater than
${header.count >= 10}         // Greater than or equal
${header.count < 100}         // Less than
${header.count <= 100}        // Less than or equal
```

### Null-safe Navigation

```go
${body?.field?.subfield}       // Null-safe access
```

### Bracket Notation

```go
${body['key']}                 // Map access
${body[0]}                     // Array/slice index
${body['user']['name']}        // Nested access
```

## Usage Examples

### In Choice EIP

```go
builder.From("direct:input").
    Choice().
        When("${header.priority == 'high'}").
            To("direct:urgent").
        When("${header.count > 100}").
            To("direct:large-batch").
        Otherwise().
            To("direct:normal").
    EndChoice()
```

### In Headers and Body

```go
builder.From("direct:start").
    SimpleSetHeader("X-Request-ID", "${uuid}").
    SimpleSetBody("Processed at ${date:now} for user ${header.username}").
    To("direct:output")
```

### In Logging

```go
builder.From("direct:start").
    Log("Processing order ${header.orderId} with value ${body?.total}")
```

### In ToD (Dynamic URI)

```go
builder.From("direct:start").
    SetHeader("filename", "report.txt").
    ToD("file://output/${header.filename}")
```

## Complex Expressions

### Null-safe Chain

```go
// Safe navigation through potentially nil structures
"User name: ${body?.user?.profile?.name}"
```

### Combined Conditions

```go
builder.From("direct:start").
    Choice().
        When("${header.type == 'A' && body.status == 'active'}").
            To("direct:processA").
        When("${header.priority > 5 || random(10) > 7}").
            To("direct:random-priority").
    EndChoice()
```

---

# 🇫🇷 Français

## Vue d'Ensemble

Simple Language est un langage d'expressions dynamiques inspiré d'Apache Camel. Il vous permet d'insérer des placeholders d'expressions dans des chaînes, permettant le routage dynamique, la transformation de messages et le traitement conditionnel.

## Syntaxe

Les expressions sont entourées de `${}`:

```go
"Bonjour ${body}"                  // Contenu du body
"Priorité: ${header.priority}"     // Valeur d'en-tête
"ID: ${exchangeProperty.id}"       // Propriété d'échange
```

## Variables Intégrées

| Variable | Description | Exemple |
|----------|-------------|---------|
| `${body}` | Corps du message | `${body}` |
| `${header.name}` | Valeur d'en-tête | `${header.Content-Type}` |
| `${exchangeProperty.name}` | Propriété d'échange | `${exchangeProperty.correlationId}` |

## Fonctions Intégrées

| Fonction | Description | Exemple |
|----------|-------------|---------|
| `${date:now}` | Timestamp actuel | `${date:now:yyyy-MM-dd}` |
| `${random(max)}` | Nombre aléatoire | `${random(100)}` |
| `${uuid}` | Génération UUID | `${uuid}` |
| `${env:VAR}` | Variable d'environnement | `${env:USER}` |

## Opérateurs

### Opérateurs de Comparaison

```go
${body == 'active'}           // Égalité
${body != 'inactive'}          // Inégalité
${header.count > 10}          // Supérieur à
${header.count >= 10}         // Supérieur ou égal
${header.count < 100}         // Inférieur à
${header.count <= 100}        // Inférieur ou égal
```

### Navigation Null-safe

```go
${body?.field?.subfield}       // Accès null-safe
```

### Notation par Crochets

```go
${body['key']}                 // Accès Map
${body[0]}                     // Index tableau/tranche
${body['user']['name']}        // Accès imbriqué
```

## Exemples d'Utilisation

### Dans Choice EIP

```go
builder.From("direct:input").
    Choice().
        When("${header.priority == 'high'}").
            To("direct:urgent").
        When("${header.count > 100}").
            To("direct:large-batch").
        Otherwise().
            To("direct:normal").
    EndChoice()
```

### Dans Headers et Body

```go
builder.From("direct:start").
    SimpleSetHeader("X-Request-ID", "${uuid}").
    SimpleSetBody("Traité à ${date:now} pour l'utilisateur ${header.username}").
    To("direct:output")
```

### Dans Logging

```go
builder.From("direct:start").
    Log("Traitement commande ${header.orderId} avec valeur ${body?.total}")
```

### Dans ToD (URI Dynamique)

```go
builder.From("direct:start").
    SetHeader("filename", "report.txt").
    ToD("file://output/${header.filename}")
```

## Expressions Complexes

### Chaîne Null-safe

```go
// Navigation sécurisée à travers structures potentiellement nil
"Nom utilisateur: ${body?.user?.profile?.name}"
```

### Conditions Combinées

```go
builder.From("direct:start").
    Choice().
        When("${header.type == 'A' && body.status == 'active'}").
            To("direct:processA").
        When("${header.priority > 5 || random(10) > 7}").
            To("direct:random-priority").
    EndChoice()
```

---

## 📚 Complete Function Reference | Référence Complète des Fonctions

### Date Formatting | Formatage des Dates

```go
${date:now:yyyy-MM-dd HH:mm:ss}
${date:now:ISO8601}
```

### String Functions (if available) | Fonctions de Chaîne (si disponibles)

```go
${body.toUpperCase()}
${body.substring(0, 5)}
${header.name.trim()}
```

### Math Functions | Fonctions Mathématiques

```go
${random(100)}        // 0-99
${random(1, 10)}      // 1-10 range
```

---

*For more examples, see | Pour plus d'exemples, voir: [examples.md](examples.md)*
