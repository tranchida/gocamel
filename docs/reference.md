# Référence API

## Types principaux

### CamelContext

```go
type CamelContext struct {
    // champs internes
}

// Méthodes
func NewCamelContext() *CamelContext
func (c *CamelContext) Start() error
func (c *CamelContext) Stop() error
func (c *CamelContext) AddRoute(route Route) error
func (c *CamelContext) CreateRouteBuilder() *RouteBuilder
func (c *CamelContext) CreateEndpoint(uri string) (Endpoint, error)
func (c *CamelContext) AddComponent(name string, component Component) error
func (c *CamelContext) GetComponent(name string) (Component, error)
```

### Exchange

```go
type Exchange struct {
    In       *Message
    Out      *Message
    Properties map[string]any
    Exception  error
}

// Méthodes
func NewExchange(ctx context.Context) *Exchange
func (e *Exchange) GetIn() *Message
func (e *Exchange) GetOut() *Message
func (e *Exchange) SetProperty(key string, value any)
func (e *Exchange) GetProperty(key string) (any, bool)
```

### Message

```go
type Message struct {
    Body   any
    Headers map[string]any
}

// Méthodes
func (m *Message) GetBody() any
func (m *Message) SetBody(body any)
func (m *Message) GetHeader(name string) (any, bool)
func (m *Message) SetHeader(name string, value any)
```

### RouteBuilder

```go
type RouteBuilder struct{}

// Méthodes DSL
func (b *RouteBuilder) From(uri string) *RouteBuilder
func (b *RouteBuilder) To(uri string) *RouteBuilder
func (b *RouteBuilder) Log(message string) *RouteBuilder
func (b *RouteBuilder) SetBody(body any) *RouteBuilder
func (b *RouteBuilder) SetHeader(name string, value any) *RouteBuilder
func (b *RouteBuilder) ProcessFunc(fn ProcessorFn) *RouteBuilder
func (b *RouteBuilder) Split(splitter SplitterFn) *RouteBuilder
func (b *RouteBuilder) Aggregate(aggregator *Aggregator) *RouteBuilder
func (b *RouteBuilder) Multicast() *RouteBuilder
func (b *RouteBuilder) Pipeline() *RouteBuilder
func (b *RouteBuilder) Stop() *RouteBuilder
func (b *RouteBuilder) Build() Route
```

## Interfaces

```go
// Composant
type Component interface {
    CreateEndpoint(uri string) (Endpoint, error)
}

// Endpoint
type Endpoint interface {
    CreateConsumer(processor Processor) (Consumer, error)
    CreateProducer() (Producer, error)
}

// Processor
type Processor interface {
    Process(exchange *Exchange) error
}
type ProcessorFn func(exchange *Exchange) error

// Consumer
type Consumer interface {
    Start() error
    Stop() error
}

// Producer  
type Producer interface {
    Send(exchange *Exchange) error
    Start() error
    Stop() error
}

// AggregationStrategy
type AggregationStrategy interface {
    Aggregate(oldExchange, newExchange *Exchange) *Exchange
}

// Splitter
type SplitterFn func(exchange *Exchange) (any, error)
```

## Constants

```go
// Headers standard Camel
const (
    CamelFileName     = "CamelFileName"
    CamelFilePath     = "CamelFilePath"
    CamelFileLength   = "CamelFileLength"
    CamelFileLastMod  = "CamelFileLastModified"
    // ...
)

// Properties EIP
const (
    CamelSplitIndex     = "CamelSplitIndex"
    CamelSplitSize      = "CamelSplitSize"
    CamelSplitComplete  = "CamelSplitComplete"
    CamelMulticastIndex = "CamelMulticastIndex"
    CamelMulticastSize  = "CamelMulticastSize"
)
```
