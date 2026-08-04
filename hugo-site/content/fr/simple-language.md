+++
title = "Simple Language"
weight = 14
+++
## Vue d'Ensemble

Simple Language est un langage d'expressions dynamiques inspiré d'Apache Camel. Il permet d'insérer des
placeholders d'expressions dans des chaînes pour le routage dynamique, la transformation de messages et le
traitement conditionnel.

## Syntaxe

Les expressions sont encadrées par `${...}` :

```go
"Bonjour ${body}"                  // Corps du message
"Priorité: ${header.priority}"     // Valeur d'en-tête
"ID: ${exchangeProperty.id}"       // Propriété d'échange
```

## Variables Intégrées

| Variable                   | Description         | Exemple                             |
|----------------------------|---------------------|-------------------------------------|
| `${body}`                  | Corps du message    | `${body}`                           |
| `${header.name}`           | Valeur d'en-tête    | `${header.Content-Type}`            |
| `${exchangeProperty.name}` | Propriété d'échange | `${exchangeProperty.correlationId}` |

## Fonctions Intégrées

| Fonction                        | Description               | Exemple                           |
|---------------------------------|---------------------------|-----------------------------------|
| `${date:now:format}`            | Horodatage actuel         | `${date:now:2006-01-02}`          |
| `${date:command+offset:format}` | Date avec arithmétique    | `${date:now+24h:2006-01-02}`      |
| `${date:header.name:format}`    | Date depuis un header     | `${date:header.birthDate:2006}`   |
| `${date:property.name:format}`  | Date depuis une propriété | `${date:property.expiry:RFC3339}` |
| `${date:file:format}`           | Date de modif. du fichier | `${date:file:2006-01-02}`         |
| `${random(max)}`                | Nombre aléatoire          | `${random(100)}`                  |
| `${uuid}`                       | Génération UUID           | `${uuid}`                         |
| `${env:VAR}`                    | Variable d'environnement  | `${env:USER}`                     |

**Décalages Arithmétiques :**

- `s` : Secondes (ex : `+30s`)
- `m` : Minutes (ex : `-5m`)
- `h` : Heures (ex : `+12h`)
- `d` : Jours (ex : `-7d`)

---

## File Language

File Language est une extension de Simple Language dédiée aux opérations liées aux fichiers. Elle fournit des fonctions
pour extraire des parties du nom de fichier, du chemin, de la taille, etc.

| Fonction                 | Description                                                  | Exemple                               |
|--------------------------|--------------------------------------------------------------|---------------------------------------|
| `${file:name}`           | Le nom du fichier (incluant le chemin)                       | `data/reports/monthly.json`           |
| `${file:name.noext}`     | Le nom du fichier mais sans l'extension                      | `data/reports/monthly`                |
| `${file:name.ext}`       | L'extension du fichier                                       | `json`                                |
| `${file:ext}`            | Alias pour `${file:name.ext}`                                | `json`                                |
| `${file:onlyname}`       | Le nom du fichier (sans le chemin)                           | `monthly.json`                        |
| `${file:onlyname.noext}` | Le nom du fichier (sans le chemin) et sans l'extension       | `monthly`                             |
| `${file:path}`           | Le chemin complet du fichier                                 | `/abs/path/data/reports/monthly.json` |
| `${file:size}`           | La longueur (taille) du fichier                              | `1234`                                |
| `${file:modified}`       | La date de dernière modification du fichier (format RFC3339) | `2023-05-10T10:00:00Z`               |

## Opérateurs de Comparaison

| Opérateur | Description         | Exemple                  |
|-----------|---------------------|--------------------------|
| `==`      | Égal                | `${body} == 'active'`    |
| `!=`      | Différent           | `${body} != 'inactive'`  |
| `>`       | Supérieur à         | `${header.count} > 10`   |
| `>=`      | Supérieur ou égal   | `${header.count} >= 10`  |
| `<`       | Inférieur à         | `${header.count} < 100`  |
| `<=`      | Inférieur ou égal   | `${header.count} <= 100` |
| `&&`      | ET logique          | `a > 5 && b == 'x'`      |
| `||`      | OU logique          | `a > 5 || b == 'y'`      |

## Navigation Null-safe

```go
${body?.field?.subfield}       // Accès sécurisé
```

## Notation par Crochets

```go
${body['key']}                 // Accès Map
${body[0]}                     // Index de tableau
${body['user']['name']}        // Accès imbriqué
```

## Exemples d'Utilisation

### Dans Choice EIP

```go
builder.From("direct:input").
    Choice().
        When("${header.priority} == 'high'").
            To("direct:urgent").
        When("${header.count} > 100").
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
"Nom utilisateur: ${body?.user?.profile?.name}"
```

### Conditions Combinées

```go
builder.From("direct:start").
    Choice().
        When("${header.type} == 'A' && ${body.status} == 'active'").
            To("direct:processA").
        When("${header.priority} > 5 || ${random(10)} > 7").
            To("direct:random-priority").
    EndChoice()
```

## Formatage des Dates

```go
${date:now:yyyy-MM-dd HH:mm:ss}
${date:now:ISO8601}
```

## Référence Complète

### Fonctions de Chaîne (si disponibles)

```go
${body.toUpperCase()}
${body.substring(0, 5)}
${header.name.trim()}
```

### Fonctions Mathématiques

Opérations mathématiques via la fonction `math(...)`. Supporte les opérateurs de base (+, -, *, /, %) et les
parenthèses.

| Fonction                   | Description              | Exemple |
|----------------------------|--------------------------|---------|
| `${math(body * 2)}`        | Multiplie le corps par 2 | `20`    |
| `${math(header.val + 10)}` | Ajoute 10 au header      | `15`    |
| `${math((body + 2) * 3)}`  | Expression complexe      | `36`    |

### Fonctions Numériques

Utilitaires numériques pour les arrondis et transformations. Utilisables seuls ou dans `math()`.

| Fonction                                    | Description                       | Exemple                |
|---------------------------------------------|-----------------------------------|------------------------|
| `${round(val)}`                             | Arrondi à l'entier le plus proche | `${round(5.6)}` -> `6` |
| `${floor(val)}`                             | Arrondi vers le bas               | `${floor(5.6)}` -> `5` |
| `${ceil(val)}`                              | Arrondi vers le haut              | `${ceil(5.1)}` -> `6`  |
| `${abs(val)}`                               | Valeur absolue                    | `${abs(-10)}` -> `10`  |
| `${sqrt(val)}`                              | Racine carrée                     | `${sqrt(16)}` -> `4`   |
| `${sin(val)}`, `${cos(val)}`, `${tan(val)}` | Fonctions trigonométriques        | `${sin(0)}` -> `0`     |

---

## Fonctions Base64

Encodage et décodage de données en Base64.

| Fonction                  | Description                   | Exemple                        |
|---------------------------|-------------------------------|--------------------------------|
| `${base64:encode:target}` | Encode la cible en Base64     | `${base64:encode:body}`        |
| `${base64:decode:target}` | Décode la cible depuis Base64 | `${base64:decode:header.auth}` |

---

## Limite de Taille des Entrées

`gocamel.MaxSimpleInputBytes` est une variable de package (par défaut **1 MiB**) qui borne le coût des dizaines de
regex compilées que Simple Language utilise. Elle protège contre les dénis de service de type ReDoS via des
bodies ou headers contrôlés par un attaquant :

- `ParseSimpleTemplate` rejette les expressions plus grandes que la limite et retourne une erreur.
- `gocamel.Interpolate` retourne l'entrée inchangée si elle dépasse la limite, avec un log `WARN`.

Si vos routes interpolent légitimement de gros templates, augmentez la valeur explicitement au démarrage :

```go
import "gitlab.com/tranchida/gocamel"

func init() {
    gocamel.MaxSimpleInputBytes = 8 << 20 // 8 MiB
}
```