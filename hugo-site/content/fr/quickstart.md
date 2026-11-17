+++
title = "Démarrage Rapide"
weight = 2
+++
## Hello World

```go
package main

import (
    "log"

    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()

    // period et delay du timer sont des entiers en millisecondes (défaut 1000)
    route, err := ctx.CreateRouteBuilder().
        From("timer:tick?period=5000").
        SetBody("Hello World").
        LogSimple("${body}").
        Build()
    if err != nil {
        log.Fatal(err)
    }

    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

## Traitement de Fichiers

Le composant `file` vit dans son propre module (il dépend de `fsnotify`), il
doit donc être enregistré explicitement :

```go
import "github.com/tranchida/gocamel/components/file"

ctx.AddComponent("file", file.NewFileComponent())

route, err := ctx.CreateRouteBuilder().
    From("file://input?delete=true").
    LogSimple("Traitement: ${header.CamelFileName}").
    To("file://output").
    Build()
if err != nil {
    log.Fatal(err)
}
```

## Endpoint HTTP

```go
route, err := ctx.CreateRouteBuilder().
    From("http://localhost:8080/hello").
    SimpleSetBody("Bonjour ${header.name}!").
    Build()
if err != nil {
    log.Fatal(err)
}
```
