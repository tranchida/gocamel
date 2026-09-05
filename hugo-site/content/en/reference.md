+++
title = "API Reference"
weight = 30
+++
## CamelContext

```go
func NewCamelContext() *CamelContext
```

### Methods

| Method                                           | Description          |
|--------------------------------------------------|----------------------|
| `AddRoute(route *Route)`                         | Register a route     |
| `AddComponent(name string, component Component)` | Register a component |
| `CreateEndpoint(uri string) (Endpoint, error)`   | Create endpoint      |
| `Start()`                                        | Start all routes (safe after `Stop()`) |
| `Stop()`                                         | Stop all routes; installs a fresh root context |
| `CreateRouteBuilder() *RouteBuilder`             | Create route builder |

## RouteBuilder

### Source

| Method                           | Description         |
|----------------------------------|---------------------|
| `From(uri string) *RouteBuilder` | Set source endpoint |

### Processing

| Method                               | Description             |
|--------------------------------------|-------------------------|
| `To(uri string) *RouteBuilder`       | Send to endpoint        |
| `ToD(uri string) *RouteBuilder`      | Dynamic destination     |
| `Process(p Processor) *RouteBuilder` | Custom processor        |
| `ProcessFunc(fn) *RouteBuilder`      | Function processor      |
| `ProcessRef(name) *RouteBuilder`     | Reference from registry |
| `Log(msg string) *RouteBuilder`      | Log static message      |
| `LogSimple(expr) *RouteBuilder`      | Log dynamic expression  |

## Message / Exchange Accessors

Common methods available on `Message` and proxied on `Exchange` (accessing the `In` message):

| Method                 | Return           | Description       |
|------------------------|------------------|-------------------|
| `GetBodyAsString()`    | `(string, bool)` | Body as string    |
| `GetBodyAsInt()`       | `(int, bool)`    | Body as integer   |
| `GetBodyAsBool()`      | `(bool, bool)`   | Body as boolean   |
| `GetHeaderAsString(k)` | `(string, bool)` | Header as string  |
| `GetHeaderAsInt(k)`    | `(int, bool)`    | Header as integer |
| `GetHeaderAsBool(k)`   | `(bool, bool)`   | Header as boolean |

## Registry

Accessible via `ctx.GetComponentRegistry()`:

| Method              | Description                             |
|---------------------|-----------------------------------------|
| `Bind(name, value)` | Register a bean, component or processor |
| `Lookup(name)`      | Retrieve an object by name              |
| `Remove(name)`      | Remove an object from registry          |

| Method                                         | Description        |
|------------------------------------------------|--------------------|
| `Choice() *ChoiceBuilder`                      | Content router     |
| `Split(fn) *SplitBuilder`                      | Message splitter   |
| `Aggregate(a) *RouteBuilder`                   | Message aggregator |
| `Multicast() *MulticastBuilder`                | Multi-destination  |
| `Enrich(uri, strategy) *RouteBuilder`          | Content enricher via producer |
| `PollEnrich(uri, strategy, timeout) *RouteBuilder` | On-demand content enricher via consumer |
| `Validate(predicate) *RouteBuilder`            | Validate message against predicate |
| `ValidateFunc(fn) *RouteBuilder`               | Validate message against function predicate |

### Headers

| Method                          | Description          |
|---------------------------------|----------------------|
| `SetHeader(k, v) *RouteBuilder` | Set header           |
| `SetHeaders(m) *RouteBuilder`   | Set multiple headers |
| `RemoveHeader(n) *RouteBuilder` | Remove header        |

### Body

| Method                              | Description     |
|-------------------------------------|-----------------|
| `SetBody(any) *RouteBuilder`        | Set body        |
| `SimpleSetBody(expr) *RouteBuilder` | Expression body |

### Data Formats

| Method                                | Description                                                                      |
|---------------------------------------|----------------------------------------------------------------------------------|
| `Marshal(df) *RouteBuilder`           | Marshal body using a data format                                                 |
| `Unmarshal(df) *RouteBuilder`         | Unmarshal body using a data format                                               |
| `MarshalJson(opts...) *RouteBuilder`  | Marshal structured body as a JSON string                                         |
| `UnmarshalJson(opts...) *RouteBuilder`| Unmarshal JSON body into typed struct, map or slice                              |
| `MarshalXml(opts...) *RouteBuilder`   | Marshal structured body as an XML string                                         |
| `UnmarshalXml(opts...) *RouteBuilder` | Unmarshal XML body into typed struct or generic XML document                     |
| `MarshalCSV(opts...) *RouteBuilder`   | Write a structured body as a CSV string                                          |
| `UnmarshalCSV(opts...) *RouteBuilder` | Read a CSV body as `[][]string` or `[]map[string]string` with `CSVUseMaps(true)` |
| `MarshalZip(opts...) *RouteBuilder`   | Build a ZIP archive from the body (single entry or `map[string][]byte`/`ZipFiles`) |
| `UnmarshalZip(opts...) *RouteBuilder` | Extract a ZIP archive (single entry yields `[]byte`, multi-entry yields `map[string][]byte`) |
| `MarshalTarGz(opts...) *RouteBuilder` | Build a gzipped tar archive from the body                                         |
| `UnmarshalTarGz(opts...) *RouteBuilder`| Extract a gzipped tar archive                                                     |

Main CSV options: `CSVDelimiter`, `CSVHeader`, `CSVSkipHeaderRecord`, `CSVCaptureHeaderRecord`, `CSVUseMaps`, `CSVTrim`,
`CSVIgnoreEmptyLines`.
Main JSON options: `JSONPrettyPrint`, `JSONIndent`, `JSONUnmarshalType`, `JSONUseNumber`.
Main XML options: `XMLPrettyPrint`, `XMLIndent`, `XMLUnmarshalType`, `XMLHeader`.
Main ZIP options: `ZipPassword` (WinZip AES-256 on write; WinZip AES and legacy ZipCrypto on read),
`ZipEntryName`, `ZipFlat`, `ZipComment`, `ZipModTime`, `ZipMaxEntrySize` (zip-bomb guard, default 512 MiB per entry).
Main tar.gz options: `TarGzEntryName`, `TarGzFlat`, `TarGzCompressionLevel`, `TarGzMaxEntrySize`.

### Builder

| Method                    | Description  |
|---------------------------|--------------|
| `SetID(id) *RouteBuilder` | Set route ID |
| `Build() *Route`          | Build route  |
