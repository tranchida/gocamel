# API Reference | Référence API

---

# 🇺🇸 English

## CamelContext

### Constructor

```go
func NewCamelContext() *CamelContext
```

Creates a new Camel context with default settings.

### Methods

| Method | Description |
|--------|-------------|
| `AddRoute(route *Route)` | Register a route |
| `AddComponent(name string, component Component)` | Register a component |
| `CreateEndpoint(uri string) (Endpoint, error)` | Create endpoint from URI |
| `Start()` | Start all routes |
| `Stop()` | Stop all routes |
| `CreateRouteBuilder() *RouteBuilder` | Create a new route builder |

## RouteBuilder

### Source Methods

| Method | Description |
|--------|-------------|
| `From(uri string) *RouteBuilder` | Set the source endpoint |

### Processing Methods

| Method | Description |
|--------|-------------|
| `Process(p Processor) *RouteBuilder` | Add a custom processor |
| `ProcessFunc(fn func(*Exchange) error) *RouteBuilder` | Add function processor |
| `To(uri string) *RouteBuilder` | Add destination endpoint |
| `ToD(uri string) *RouteBuilder` | Add dynamic destination |

### EIP Methods

| Method | Description |
|--------|-------------|
| `Choice() *ChoiceBuilder` | Start Choice pattern |
| `Split(expr func(*Exchange) (any, error)) *SplitBuilder` | Start Split pattern |
| `Aggregate(aggregator *Aggregator) *RouteBuilder` | Add Aggregate pattern |
| `Multicast() *MulticastBuilder` | Start Multicast pattern |
| `Stop() *RouteBuilder` | Stop routing |

### Header/Property Methods

| Method | Description |
|--------|-------------|
| `SetHeader(key string, value any) *RouteBuilder` | Set a header |
| `SetHeaders(headers map[string]any) *RouteBuilder` | Set multiple headers |
| `SetHeadersFunc(fn func(*Exchange) (map[string]any, error)) *RouteBuilder` | Set headers from function |
| `RemoveHeader(name string) *RouteBuilder` | Remove a header |
| `RemoveHeaders(pattern string, exclude ...string) *RouteBuilder` | Remove headers by pattern |
| `SetProperty(key string, value any) *RouteBuilder` | Set an exchange property |
| `SetPropertyFunc(key string, fn func(*Exchange) (any, error)) *RouteBuilder` | Set property from function |
| `RemoveProperty(key string) *RouteBuilder` | Remove a property |
| `RemoveProperties(pattern string, exclude ...string) *RouteBuilder` | Remove properties by pattern |

### Message Methods

| Method | Description |
|--------|-------------|
| `SetBody(body any) *RouteBuilder` | Set the message body |
| `SimpleSetBody(expr string) *RouteBuilder` | Set body from expression |
| `Log(message string) *RouteBuilder` | Log a message |
| `LogBody(prefix string) *RouteBuilder` | Log the body with prefix |
| `LogHeaders(prefix string) *RouteBuilder` | Log headers with prefix |

### Builder Methods

| Method | Description |
|--------|-------------|
| `SetID(id string) *RouteBuilder` | Set route ID |
| `Build() *Route` | Build the route |

## Message

### Constructor

```go
func NewMessage() *Message
```

### Methods

| Method | Description |
|--------|-------------|
| `GetBody() any` | Get message body |
| `SetBody(body any)` | Set message body |
| `GetHeader(key string) (any, bool)` | Get header value |
| `SetHeader(key string, value any)` | Set header value |
| `GetHeaders() map[string]any` | Get all headers |
| `SetHeaders(headers map[string]any)` | Set all headers |

## Exchange

### Constructor

```go
func NewExchange(ctx context.Context) *Exchange
```

### Methods

| Method | Description |
|--------|-------------|
| `GetIn() *Message` | Get input message |
| `SetIn(msg *Message)` | Set input message |
| `GetOut() *Message` | Get output message |
| `SetOut(msg *Message)` | Set output message |
| `GetProperty(key string) (any, bool)` | Get exchange property |
| `SetProperty(key string, value any)` | Set exchange property |
| `GetContext() context.Context` | Get Go context |

---

# 🇫🇷 Français

## CamelContext

### Constructeur

```go
func NewCamelContext() *CamelContext
```

Crée un nouveau contexte Camel avec les paramètres par défaut.

### Méthodes

| Méthode | Description |
|---------|-------------|
| `AddRoute(route *Route)` | Enregistrer une route |
| `AddComponent(name string, component Component)` | Enregistrer un composant |
| `CreateEndpoint(uri string) (Endpoint, error)` | Créer un endpoint depuis une URI |
| `Start()` | Démarrer toutes les routes |
| `Stop()` | Arrêter toutes les routes |
| `CreateRouteBuilder() *RouteBuilder` | Créer un nouveau route builder |

## RouteBuilder

### Méthodes Source

| Méthode | Description |
|---------|-------------|
| `From(uri string) *RouteBuilder` | Définir l'endpoint source |

### Méthodes de Traitement

| Méthode | Description |
|---------|-------------|
| `Process(p Processor) *RouteBuilder` | Ajouter un processeur personnalisé |
| `ProcessFunc(fn func(*Exchange) error) *RouteBuilder` | Ajouter un processeur fonction |
| `To(uri string) *RouteBuilder` | Ajouter un endpoint destination |
| `ToD(uri string) *RouteBuilder` | Ajouter une destination dynamique |

### Méthodes EIP

| Méthode | Description |
|---------|-------------|
| `Choice() *ChoiceBuilder` | Démarrer le pattern Choice |
| `Split(expr func(*Exchange) (any, error)) *SplitBuilder` | Démarrer le pattern Split |
| `Aggregate(aggregator *Aggregator) *RouteBuilder` | Ajouter le pattern Aggregate |
| `Multicast() *MulticastBuilder` | Démarrer le pattern Multicast |
| `Stop() *RouteBuilder` | Arrêter le routage |

### Méthodes Headers/Properties

| Méthode | Description |
|---------|-------------|
| `SetHeader(key string, value any) *RouteBuilder` | Définir un en-tête |
| `SetHeaders(headers map[string]any) *RouteBuilder` | Définir plusieurs en-têtes |
| `SetHeadersFunc(fn func(*Exchange) (map[string]any, error)) *RouteBuilder` | Définir en-têtes par fonction |
| `RemoveHeader(name string) *RouteBuilder` | Supprimer un en-tête |
| `RemoveHeaders(pattern string, exclude ...string) *RouteBuilder` | Supprimer en-têtes par pattern |
| `SetProperty(key string, value any) *RouteBuilder` | Définir une propriété d'échange |
| `SetPropertyFunc(key string, fn func(*Exchange) (any, error)) *RouteBuilder` | Définir propriété par fonction |
| `RemoveProperty(key string) *RouteBuilder` | Supprimer une propriété |
| `RemoveProperties(pattern string, exclude ...string) *RouteBuilder` | Supprimer propriétés par pattern |

### Méthodes de Message

| Méthode | Description |
|---------|-------------|
| `SetBody(body any) *RouteBuilder` | Définir le corps du message |
| `SimpleSetBody(expr string) *RouteBuilder` | Définir le corps depuis expression |
| `Log(message string) *RouteBuilder` | Logger un message |
| `LogBody(prefix string) *RouteBuilder` | Logger le corps avec préfixe |
| `LogHeaders(prefix string) *RouteBuilder` | Logger les en-têtes avec préfixe |

### Méthodes de Builder

| Méthode | Description |
|---------|-------------|
| `SetID(id string) *RouteBuilder` | Définir l'ID de la route |
| `Build() *Route` | Construire la route |

## Message

### Constructeur

```go
func NewMessage() *Message
```

### Méthodes

| Méthode | Description |
|---------|-------------|
| `GetBody() any` | Récupérer le corps du message |
| `SetBody(body any)` | Définir le corps du message |
| `GetHeader(key string) (any, bool)` | Récupérer la valeur d'en-tête |
| `SetHeader(key string, value any)` | Définir la valeur d'en-tête |
| `GetHeaders() map[string]any` | Récupérer tous les en-têtes |
| `SetHeaders(headers map[string]any)` | Définir tous les en-têtes |

## Exchange

### Constructeur

```go
func NewExchange(ctx context.Context) *Exchange
```

### Méthodes

| Méthode | Description |
|---------|-------------|
| `GetIn() *Message` | Récupérer le message d'entrée |
| `SetIn(msg *Message)` | Définir le message d'entrée |
| `GetOut() *Message` | Récupérer le message de sortie |
| `SetOut(msg *Message)` | Définir le message de sortie |
| `GetProperty(key string) (any, bool)` | Récupérer une propriété d'échange |
| `SetProperty(key string, value any)` | Définir une propriété d'échange |
| `GetContext() context.Context` | Récupérer le contexte Go |
