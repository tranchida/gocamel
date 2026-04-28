# API Reference

## CamelContext

```go
func NewCamelContext() *CamelContext
```

### Methods

| Method | Description |
|--------|-------------|
| `AddRoute(route *Route)` | Register a route |
| `AddComponent(name string, component Component)` | Register a component |
| `CreateEndpoint(uri string) (Endpoint, error)` | Create endpoint |
| `Start()` | Start all routes |
| `Stop()` | Stop all routes |
| `CreateRouteBuilder() *RouteBuilder` | Create route builder |

## RouteBuilder

### Source

| Method | Description |
|--------|-------------|
| `From(uri string) *RouteBuilder` | Set source endpoint |

### Processing

| Method | Description |
|--------|-------------|
| `To(uri string) *RouteBuilder` | Send to endpoint |
| `ToD(uri string) *RouteBuilder` | Dynamic destination |
| `Process(p Processor) *RouteBuilder` | Custom processor |
| `ProcessFunc(fn) *RouteBuilder` | Function processor |
| `ProcessRef(name) *RouteBuilder` | Reference from registry |
| `Log(msg string) *RouteBuilder` | Log static message |
| `LogSimple(expr) *RouteBuilder` | Log dynamic expression |

## Message / Exchange Accessors

Common methods available on `Message` and proxied on `Exchange` (accessing the `In` message):

| Method | Return | Description |
|--------|--------|-------------|
| `GetBodyAsString()` | `(string, bool)` | Body as string |
| `GetBodyAsInt()` | `(int, bool)` | Body as integer |
| `GetBodyAsBool()` | `(bool, bool)` | Body as boolean |
| `GetHeaderAsString(k)` | `(string, bool)` | Header as string |
| `GetHeaderAsInt(k)` | `(int, bool)` | Header as integer |
| `GetHeaderAsBool(k)` | `(bool, bool)` | Header as boolean |

## Registry

Accessible via `ctx.GetComponentRegistry()`:

| Method | Description |
|--------|-------------|
| `Bind(name, value)` | Register a bean, component or processor |
| `Lookup(name)` | Retrieve an object by name |
| `Remove(name)` | Remove an object from registry |

| Method | Description |
|--------|-------------|
| `Choice() *ChoiceBuilder` | Content router |
| `Split(fn) *SplitBuilder` | Message splitter |
| `Aggregate(a) *RouteBuilder` | Message aggregator |
| `Multicast() *MulticastBuilder` | Multi-destination |

### Headers

| Method | Description |
|--------|-------------|
| `SetHeader(k, v) *RouteBuilder` | Set header |
| `SetHeaders(m) *RouteBuilder` | Set multiple headers |
| `RemoveHeader(n) *RouteBuilder` | Remove header |

### Body

| Method | Description |
|--------|-------------|
| `SetBody(any) *RouteBuilder` | Set body |
| `SimpleSetBody(expr) *RouteBuilder` | Expression body |

### Builder

| Method | Description |
|--------|-------------|
| `SetID(id) *RouteBuilder` | Set route ID |
| `Build() *Route` | Build route |
