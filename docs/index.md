---
title: Accueil
hide:
  - navigation
  - toc
---

<style>
.md-typeset h1 {
    font-size: 3em;
    font-weight: bold;
    background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
    -webkit-background-clip: text;
    -webkit-text-fill-color: transparent;
    background-clip: text;
}
</style>

<h1 style="text-align: center; margin-top: 2em; margin-bottom: 0.5em;">
🐪 GoCamel
</h1>

<p style="text-align: center; font-size: 1.3em; color: var(--md-default-fg-color--light); margin-bottom: 2em;">
Framework d'intégration d'entreprise inspiré d'Apache Camel, écrit en Go
</p>

<div style="text-align: center; margin-bottom: 3em;">
<a href="quickstart/" class="md-button md-button--primary">🚀 Quick Start</a>
<a href="installation/" class="md-button">📦 Installation</a>
<a href="https://github.com/tranchida/gocamel" class="md-button">
    <span class="twemoji"><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M12 2A10 10 0 0 0 2 12c0 4.42 2.87 8.17 6.84 9.5.5.08.66-.23.66-.5v-1.69c-2.77.6-3.36-1.34-3.36-1.34-.46-1.16-1.11-1.47-1.11-1.47-.91-.62.07-.6.07-.6 1 .07 1.53 1.03 1.53 1.03.87 1.52 2.34 1.07 2.91.83.09-.65.35-1.09.63-1.34-2.22-.25-4.55-1.11-4.55-4.92 0-1.11.38-2 1.03-2.71-.1-.25-.45-1.29.1-2.64 0 0 .84-.27 2.75 1.02.79-.22 1.65-.33 2.5-.33.85 0 1.71.11 2.5.33 1.91-1.29 2.75-1.02 2.75-1.02.55 1.35.2 2.39.1 2.64.65.71 1.03 1.6 1.03 2.71 0 3.82-2.34 4.66-4.57 4.91.36.31.69.92.69 1.85V21c0 .27.16.59.67.5C19.14 20.16 22 16.42 22 12A10 10 0 0 0 12 2z"/></svg></span> GitHub
</a>
</div>

<div class="grid cards" markdown>

-   🚀 **DSL Fluent**

    ---

    Définissez vos routes d'intégration avec une API Go idiomatique et expressive

    ````go
    builder.From("direct:start").
        Split(splitter).
        Log("Traité: ${body}").
        To("direct:output")
    ````

-   🔌 **Composants Riches**

    ---

    HTTP, FTP, SFTP, SMB, File, Telegram, OpenAI, Cron, et plus encore

-   🧩 **EIP Natifs**

    ---

    Split, Aggregate, Multicast, Pipeline, Routing dynamique — patterns enterprise intégrés

-   ⚡ **Performance Go**

    ---

    Concurrency goroutines, faible empreinte mémoire, compilation native

</div>

## Installation rapide

<div class="tabbed-set" data-tabs="1:2">
<input checked="checked" id="__tabbed_1_1" name="__tabbed_1" type="radio"/><label for="__tabbed_1_1">Go Modules</label><div class="tabbed-content">

```bash
go get github.com/tranchida/gocamel
```

</div>
<input id="__tabbed_1_2" name="__tabbed_1" type="radio"/><label for="__tabbed_1_2">Go Workspace</label><div class="tabbed-content">

```bash
go work use .
go work edit -replace github.com/tranchida/gocamel=./
```

</div>
</div>

## Exemple complet

```go title="hello.go"
package main

import (
    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()
    
    route := ctx.CreateRouteBuilder().
        From("timer:tick?period=5s").
        SetBody("Hello GoCamel!").
        Log("${body}").
        To("http://localhost:8080/webhook").
        Build()
    
    ctx.AddRoute(route)
    ctx.Start()
    select {}
}
```

<div style="display: flex; justify-content: center; gap: 20px; flex-wrap: wrap; margin-top: 3em; padding: 2em; background: var(--md-code-bg-color); border-radius: 8px;">

<div style="text-align: center;">
    <div style="font-size: 2em; font-weight: bold; color: var(--md-primary-fg-color);">15+</div>
    <div style="font-size: 0.9em; color: var(--md-default-fg-color--light);">Composants</div>
</div>

<div style="text-align: center;">
    <div style="font-size: 2em; font-weight: bold; color: var(--md-primary-fg-color);">10+</div>
    <div style="font-size: 0.9em; color: var(--md-default-fg-color--light);">EIP Patterns</div>
</div>

<div style="text-align: center;">
    <div style="font-size: 2em; font-weight: bold; color: var(--md-primary-fg-color);">Go 1.21+</div>
    <div style="font-size: 0.9em; color: var(--md-default-fg-color--light);">Minimum Version</div>
</div>

</div>

---

<p style="text-align: center; color: var(--md-default-fg-color--light);">
🐪 GoCamel — Propulsé par la communauté Go
</p>
