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
| `Log(msg string) *RouteBuilder` | Log message |

### EIP

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
