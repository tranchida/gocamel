# Démarrage Rapide

## Hello World

```go
package main

import (
    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()
    
    route := ctx.CreateRouteBuilder().
        From("timer:tick?period=5s").
        SetBody("Hello World").
        Log("${body}").
        Build()
    
    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

## Traitement de Fichiers

```go
route := ctx.CreateRouteBuilder().
    From("file://input?delete=true").
    Log("Traitement: ${body}").
    To("file://output").
    Build()
```

## Endpoint HTTP

```go
route := ctx.CreateRouteBuilder().
    From("http://localhost:8080/hello").
    SetBody("Bonjour ${header.name}!").
    Build()
```
