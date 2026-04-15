# Installation

## Via Go Modules (recommandé)

```bash
go get github.com/tranchida/gocamel
```

Ajoutez à votre `go.mod`:

```gomod
require github.com/tranchida/gocamel v0.0.0
```

## Via go.work (développement local)

```bash
# Clonez le repo
git clone https://github.com/tranchida/gocamel.git
cd gocamel

# Dans votre projet
go work init
go work use ../gocamel
go work edit -replace github.com/tranchida/gocamel=../gocamel
```

## Configuration de l'environnement

### Variables d'environnement communes

| Variable | Usage | Composant |
|----------|-------|-----------|
| `OPENAI_API_KEY` | Clé API OpenAI | OpenAI |
| `TELEGRAM_AUTHORIZATIONTOKEN` | Token bot Telegram | Telegram |
| `FTP_USERNAME` / `FTP_PASSWORD` | Auth FTP | FTP |
| `SFTP_USERNAME` / `SFTP_PASSWORD` | Auth SFTP | SFTP |

### Configuration dans le code

```go
cfg := gocamel.NewConfig()
val := cfg.Get("ma.variable", "valeur par défaut")
```

## Vérifier l'installation

```go title="verify.go"
package main

import (
    "fmt"
    "github.com/tranchida/gocamel"
)

func main() {
    ctx := gocamel.NewCamelContext()
    fmt.Println("✅ GoCamel installé avec succès!")
    
    // Vérifier les composants disponibles
    components := []string{"http", "file", "timer", "direct"}
    for _, name := range components {
        if _, err := ctx.CreateEndpoint(name + ":test"); err == nil {
            fmt.Printf("  ✓ Composant '%s' disponible\n", name)
        }
    }
}
```

## Dépannage

### Compilation échoue

??? bug "Erreur de dépendance"

    ```
    go: github.com/tranchida/gocamel: module not found
    ```

    **Solution:**
    ```bash
    go mod tidy
    go get github.com/tranchida/gocamel@latest
    ```

### Composant non trouvé

??? bug `"unknown component"`

    Assurez-vous d'avoir ajouté le composant:
    ```go
    ctx.AddComponent("telegram", gocamel.NewTelegramComponent())
    ```

### Timeout sur HTTPS

??? tip "Proxy d'entreprise"

    ```go
    os.Setenv("HTTP_PROXY", "http://proxy.entreprise:8080")
    os.Setenv("HTTPS_PROXY", "http://proxy.entreprise:8080")
    ```
