+++
title = "Référence API"
weight = 30
+++
## CamelContext

```go
func NewCamelContext() *CamelContext
```

### Méthodes

| Méthode                         | Description                |
|---------------------------------|----------------------------|
| `AddRoute(route *Route)`        | Enregistrer une route      |
| `AddComponent(name, component)` | Enregistrer un composant   |
| `CreateEndpoint(uri)`           | Créer un endpoint          |
| `Start()`                       | Démarrer toutes les routes (valide après `Stop()`) |
| `Stop()`                        | Arrêter toutes les routes ; installe un nouveau contexte racine |
| `CreateRouteBuilder()`          | Créer un route builder     |

## RouteBuilder

### Source

| Méthode     | Description               |
|-------------|---------------------------|
| `From(uri)` | Définir l'endpoint source |

### Traitement

| Méthode            | Description                  |
|--------------------|------------------------------|
| `To(uri)`          | Envoyer vers endpoint        |
| `ToD(uri)`         | Destination dynamique        |
| `Process(p)`       | Processeur personnalisé      |
| `ProcessFunc(fn)`  | Processeur fonction          |
| `ProcessRef(name)` | Référence depuis le registre |
| `Log(msg)`         | Log message statique         |
| `LogSimple(expr)`  | Log expression dynamique     |

## Accesseurs Message / Exchange

Méthodes communes sur `Message` et relayées sur `Exchange` (accès au message `In`) :

| Méthode                | Retour           | Description        |
|------------------------|------------------|--------------------|
| `GetBodyAsString()`    | `(string, bool)` | Corps en string    |
| `GetBodyAsInt()`       | `(int, bool)`    | Corps en entier    |
| `GetBodyAsBool()`      | `(bool, bool)`   | Corps en booléen   |
| `GetHeaderAsString(k)` | `(string, bool)` | En-tête en string  |
| `GetHeaderAsInt(k)`    | `(int, bool)`    | En-tête en entier  |
| `GetHeaderAsBool(k)`   | `(bool, bool)`   | En-tête en booléen |

## Registre (Registry)

Accessible via `ctx.GetComponentRegistry()` :

| Méthode             | Description                                  |
|---------------------|----------------------------------------------|
| `Bind(name, value)` | Enregistrer un bean, composant ou processeur |
| `Lookup(name)`      | Récupérer un objet par nom                   |
| `Remove(name)`      | Supprimer un objet du registre               |

| Méthode                                | Description            |
|----------------------------------------|------------------------|
| `Choice()`                             | Routeur par contenu    |
| `Split(fn)`                            | Séparateur de messages |
| `Aggregate(a)`                         | Agrégateur de messages |
| `Multicast()`                          | Multi-destinations     |
| `Enrich(uri, strategy)`                | Enrichisseur de contenu via producteur |
| `PollEnrich(uri, strategy, timeout)`   | Enrichisseur à la demande via consommateur |
| `Validate(predicate)`                  | Valider le message selon un prédicat |
| `ValidateFunc(fn)`                     | Valider le message avec une fonction prédicat |

### Headers

| Méthode           | Description                |
|-------------------|----------------------------|
| `SetHeader(k, v)` | Définir un en-tête         |
| `SetHeaders(m)`   | Définir plusieurs en-têtes |
| `RemoveHeader(n)` | Supprimer un en-tête       |

### Body

| Méthode               | Description          |
|-----------------------|----------------------|
| `SetBody(any)`        | Définir le corps     |
| `SimpleSetBody(expr)` | Corps par expression |

### Data Formats

| Méthode                 | Description                                                                        |
|-------------------------|------------------------------------------------------------------------------------|
| `Marshal(df)`           | Sérialiser le corps avec un data format                                            |
| `Unmarshal(df)`         | Désérialiser le corps avec un data format                                          |
| `MarshalJson(opts...)`  | Sérialiser un corps structuré en chaîne JSON                                       |
| `UnmarshalJson(opts...)`| Désérialiser un corps JSON vers une structure typée, une map ou une slice          |
| `MarshalXml(opts...)`   | Sérialiser un corps structuré en chaîne XML                                        |
| `UnmarshalXml(opts...)` | Désérialiser un corps XML vers une structure typée ou un document XML générique    |
| `MarshalCSV(opts...)`   | Écrire un corps structuré en chaîne CSV                                            |
| `UnmarshalCSV(opts...)` | Lire un corps CSV en `[][]string` ou `[]map[string]string` avec `CSVUseMaps(true)` |

Options CSV principales : `CSVDelimiter`, `CSVHeader`, `CSVSkipHeaderRecord`, `CSVCaptureHeaderRecord`, `CSVUseMaps`,
`CSVTrim`, `CSVIgnoreEmptyLines`.
Options JSON principales : `JSONPrettyPrint`, `JSONIndent`, `JSONUnmarshalType`, `JSONUseNumber`.
Options XML principales : `XMLPrettyPrint`, `XMLIndent`, `XMLUnmarshalType`, `XMLHeader`.

### Builder

| Méthode     | Description              |
|-------------|--------------------------|
| `SetID(id)` | Définir l'ID de la route |
| `Build()`   | Construire la route      |
