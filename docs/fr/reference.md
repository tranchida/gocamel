# Référence API

## CamelContext

```go
func NewCamelContext() *CamelContext
```

### Méthodes

| Méthode | Description |
|---------|-------------|
| `AddRoute(route *Route)` | Enregistrer une route |
| `AddComponent(name, component)` | Enregistrer un composant |
| `CreateEndpoint(uri)` | Créer un endpoint |
| `Start()` | Démarrer toutes les routes |
| `Stop()` | Arrêter toutes les routes |
| `CreateRouteBuilder()` | Créer un route builder |

## RouteBuilder

### Source

| Méthode | Description |
|---------|-------------|
| `From(uri)` | Définir l'endpoint source |

### Traitement

| Méthode | Description |
|---------|-------------|
| `To(uri)` | Envoyer vers endpoint |
| `ToD(uri)` | Destination dynamique |
| `Process(p)` | Processeur personnalisé |
| `ProcessFunc(fn)` | Processeur fonction |
| `ProcessRef(name)` | Référence depuis le registre |
| `Log(msg)` | Log message statique |
| `LogSimple(expr)` | Log expression dynamique |

## Accesseurs Message / Exchange

Méthodes communes sur `Message` et relayées sur `Exchange` (accès au message `In`) :

| Méthode | Retour | Description |
|---------|--------|-------------|
| `GetBodyAsString()` | `(string, bool)` | Corps en string |
| `GetBodyAsInt()` | `(int, bool)` | Corps en entier |
| `GetBodyAsBool()` | `(bool, bool)` | Corps en booléen |
| `GetHeaderAsString(k)` | `(string, bool)` | En-tête en string |
| `GetHeaderAsInt(k)` | `(int, bool)` | En-tête en entier |
| `GetHeaderAsBool(k)` | `(bool, bool)` | En-tête en booléen |

## Registre (Registry)

Accessible via `ctx.GetComponentRegistry()` :

| Méthode | Description |
|---------|-------------|
| `Bind(name, value)` | Enregistrer un bean, composant ou processeur |
| `Lookup(name)` | Récupérer un objet par nom |
| `Remove(name)` | Supprimer un objet du registre |

| Méthode | Description |
|---------|-------------|
| `Choice()` | Routeur par contenu |
| `Split(fn)` | Séparateur de messages |
| `Aggregate(a)` | Agrégateur de messages |
| `Multicast()` | Multi-destinations |

### Headers

| Méthode | Description |
|---------|-------------|
| `SetHeader(k, v)` | Définir un en-tête |
| `SetHeaders(m)` | Définir plusieurs en-têtes |
| `RemoveHeader(n)` | Supprimer un en-tête |

### Body

| Méthode | Description |
|---------|-------------|
| `SetBody(any)` | Définir le corps |
| `SimpleSetBody(expr)` | Corps par expression |

### Builder

| Méthode | Description |
|---------|-------------|
| `SetID(id)` | Définir l'ID de la route |
| `Build()` | Construire la route |
