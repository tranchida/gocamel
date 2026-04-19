# Composant Template GoCamel

## Vue d'ensemble

Le composant `template` permet de transformer le corps d'un message en utilisant les templates Go natifs (`html/template`), inspiré du composant Velocity d'Apache Camel.

## URI Format

```
template:templatePath[?options]
```

- `templatePath` : chemin vers le fichier template (relatif ou absolu)

### Exemples :

```
template:templates/email.tmpl
template:/home/user/templates/notification.tmpl
template:templates/item.tmpl?contentCache=true
```

## Options disponibles

| Option | Type | Défaut | Description |
|--------|------|--------|-------------|
| `contentCache` | booléen | `false` | Mettre en cache le template en mémoire pour éviter les lectures répétées du fichier |
| `allowTemplateFromHeader` | booléen | `false` | Permet de changer le template dynamiquement via le header `CamelTemplatePath` |
| `encoding` | string | `UTF-8` | Encodage du fichier template |
| `startDelimiter` | string | `{{` | Délimiteur de début pour les expressions Go template |
| `endDelimiter` | string | `}}` | Délimiteur de fin pour les expressions Go template |

## Headers

### Input

| Header | Description |
|--------|-------------|
| `CamelTemplatePath` | (Optionnel) Chemin du template à utiliser si `allowTemplateFromHeader=true` |
| `CamelTemplateEncoding` | (Optionnel) Encodage du template |

## Structure des données du template

Dans les templates, les données suivantes sont disponibles :

```
{{.Body}}              # Corps du message (toujours converti en string)
{{.Headers.name}}      # Accès aux headers (ex: Header "name")
{{.Exchange.ID}}       # ID unique de l'échange
{{.Exchange.Created}}  # Date de création de l'échange
{{.Exchange.Properties}} # Propriétés de l'échange
{{.In.Body}}          # Alias du corps
{{.In.Headers}}       # Tous les headers
```

## Fonctions disponibles

### Fonctions de chaîne

- `upper` - Convertit en majuscules
- `lower` - Convertit en minuscules
- `title` - Met en majuscule la première lettre de chaque mot
- `trim` - Supprime les espaces blancs
- `join` - Joint un slice avec un séparateur
- `split` - Sépare une chaîne par un séparateur
- `contains` - Vérifie si une chaîne en contient une autre
- `hasPrefix` / `hasSuffix` - Vérifie préfixe/suffixe
- `replace old new s` - Remplace toutes les occurrences
- `replaceN old new n s` - Remplace n occurrences

### Fonctions de date

- `now` - Date/heure actuelle
- `formatDate layout t` - Formate une date (layout Go)
- `formatTime layout t` - Alias de formatDate

Exemple : `{{now | formatDate "2006-01-02 15:04:05"}}`

### Fonctions de conversion

- `toString` - Convertit en chaîne
- `toInt` - Convertit en entier
- `toFloat` - Convertit en float64

### Fonctions HTML/JSON

- `json` - Échappe une valeur pour l'insérer dans du JSON
- `safeHTML` - Marque du contenu comme HTML sûr (non échappé)

## Exemple d'utilisation

### Template simple

Fichier `templates/greeting.tmpl` :

```
Bonjour {{.Headers.name}},

Vous avez reçu : {{.Body}}

--
Message généré le {{now | formatDate "2006-01-02"}}
```

Code Go :

```go
camelCtx := gocamel.NewCamelContext()
camelCtx.AddComponent("template", gocamel.NewTemplateComponent())

builder := camelCtx.CreateRouteBuilder()
builder.From("direct:greet").
    SetHeader("name", gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
        e.SetHeader("name", "Alice")
        return nil
    })).
    SetBody("Votre commande est prête").
    To("template:templates/greeting.tmpl")
builder.Build()
```

### Template avec cache

```go
// Le template sera chargé une seule fois en mémoire
builder.To("template:templates/notification.tmpl?contentCache=true")
```

### Template dynamique via header

```go
// Permet de changer le template à la volée
builder.To("template:templates/default.tmpl?allowTemplateFromHeader=true")

// Plus tard dans le traitement :
exchange.SetHeader("CamelTemplatePath", "templates/custom.tmpl")
```

### Template JSON

Fichier `templates/response.json.tmpl` :

```json
{
  "timestamp": "{{now | formatDate "2006-01-02T15:04:05Z07:00"}}",
  "message": {{.Body | toString | safeHTML}},
  "user": "{{.Headers.user}}",
  "status": "{{.Headers.status | upper}}"
}
```

## Comparaison avec Apache Camel Velocity

| Fonction | Camel Velocity | GoCamel Template |
|----------|---------------|------------------|
| Syntaxe | `${body}` | `{{.Body}}` |
| Headers | `${header.name}` | `{{.Headers.name}}` |
| Fonctions intégrées | Oui | Oui (voir liste) |
| Custom functions | Non | Non |
| Caching | Oui | Via `contentCache` |
| Template dynamique | Oui | Via header |

## Notes

- Le composant utilise `html/template` de Go, donc le contenu est automatiquement échappé en HTML sauf si marqué avec `safeHTML`
- Le corps est toujours converti en `string` avant d'être passé au template
- Les `[]byte` sont automatiquement convertis en `string` pour éviter l'affichage de représentations mémoire
