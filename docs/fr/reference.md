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
| `Log(msg)` | Logger un message |

### EIP

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
