# GoCamel

GoCamel est une bibliothèque d'intégration d'entreprise inspirée d'Apache Camel, écrite en Go. Elle permet de créer des routes d'intégration pour connecter différents systèmes et services.

## Installation

```bash
go get github.com/tranchida/gocamel
```

## Fonctionnalités

- Architecture basée sur les routes et les endpoints
- Gestion des messages avec corps et en-têtes
- Contexte Camel pour la gestion du cycle de vie
- Pattern Builder pour la création de routes
- Fonctions de logging intégrées
- Gestion centralisée des identifiants (fichiers, query params, variables d'environnement)

## Exemples d'utilisation

### Exemple FTP (Téléchargement & Envoi)

Il est recommandé de passer les identifiants via les variables d'environnement pour plus de sécurité (ex: `FTP_USERNAME`, `FTP_PASSWORD`, ou directement `username` et `password`).

```go
package main

import (
    "github.com/tranchida/gocamel"
)

func main() {
    context := gocamel.NewCamelContext()
    context.AddComponent("ftp", gocamel.NewFTPComponent())

    // Écoute les nouveaux fichiers sur le serveur FTP (Consumer)
    // et les renvoie vers un autre répertoire (Producer)
    route := context.CreateRouteBuilder().
        From("ftp://localhost:21/incoming?delay=10s&delete=true").
        Log("Nouveau fichier téléchargé").
        LogHeaders("Métadonnées du fichier FTP").
        SetHeader(gocamel.CamelFileName, "processed_file.txt").
        ProcessFunc(func(exchange *gocamel.Exchange) error {
            // Traitement custom ici...
            return nil
        }).
        Build()

    // Ajout d'un Producer manuel à l'exécution si besoin:
    // endpoint, _ := context.CreateEndpoint("ftp://localhost:21/outgoing")
    // producer, _ := endpoint.CreateProducer()
    // producer.Send(exchange)

    context.AddRoute(route)
    context.Start()
    select {}
}
```

### Exemple Telegram

```go
package main

import (
    "github.com/tranchida/gocamel"
)

func main() {
    // Définir la variable d'environnement TELEGRAM_AUTHORIZATIONTOKEN="votre_token_bot"
    context := gocamel.NewCamelContext()
    context.AddComponent("telegram", gocamel.NewTelegramComponent())

    route := context.CreateRouteBuilder().
        From("telegram:bots").
        Log("Message Telegram reçu !").
        LogBody("Texte du message :").
        Build()

    context.AddRoute(route)
    context.Start()
    select {}
}
```

### Exemple OpenAI

L'envoi de requêtes s'effectue via un Producteur.

```go
package main

import (
    "fmt"
    "context"
    "github.com/tranchida/gocamel"
)

func main() {
    // Définir la variable d'environnement OPENAI_AUTHORIZATIONTOKEN ou OPENAI_API_KEY
    camelCtx := gocamel.NewCamelContext()
    camelCtx.AddComponent("openai", gocamel.NewOpenAIComponent())

    // Dans une route ou manuellement:
    endpoint, _ := camelCtx.CreateEndpoint("openai:chat?model=gpt-3.5-turbo")
    producer, _ := endpoint.CreateProducer()

    exchange := gocamel.NewExchange(context.Background())
    exchange.GetIn().SetBody("Bonjour, comment ça va ?")

    producer.Send(exchange)
    fmt.Println("Réponse OpenAI :", exchange.GetOut().GetBody())
}
```

## Structure du projet

```
gocamel/
├── context.go         # Gestion du contexte Camel
├── exchange.go        # Structure d'échange de messages
├── message.go         # Structure de message
├── route.go           # Gestion des routes
├── route_builder.go   # Pattern Builder pour les routes
├── registry.go        # Registre des composants
├── config.go          # Utilitaires de gestion des configurations environnementales
├── uri_utils.go       # Utilitaires de parsing d'URI
├── http_component.go  # Composant HTTP
├── file_component.go  # Composant File
├── ftp_component.go   # Composant FTP
├── sftp_component.go  # Composant SFTP
├── smb_component.go   # Composant SMB (Samba / Windows Share)
├── telegram_component.go # Composant Telegram Bot
└── openai_component.go   # Composant OpenAI
```

## Composants disponibles

- **HTTP** (`http://...`) : Serveur (Consumer) et Client (Producer).
- **File** (`file://...`) : Lecture et écriture de fichiers locaux.
- **FTP** (`ftp://...`) : Serveur FTP (Consumer & Producer).
- **SFTP** (`sftp://...`) : Serveur SFTP avec authentification SSH (Consumer & Producer).
- **SMB** (`smb://...`) : Partages réseau Windows/Samba (Consumer & Producer).
- **Telegram** (`telegram:...`) : Bot Telegram (Consumer webhook/polling & Producer).
- **OpenAI** (`openai:...`) : Chat Completion (Producer uniquement).

## Configuration

La plupart des paramètres sensibles (tokens, mots de passe) peuvent être passés de trois façons :
1. Dans l'URI directement (ex: `ftp://user:pass@host/path`).
2. En paramètre de requête (ex: `telegram:bots?authorizationToken=XXX`).
3. En variable d'environnement (ex: `FTP_PASSWORD=xxx`, `TELEGRAM_AUTHORIZATIONTOKEN=xxx` ou `OPENAI_API_KEY=xxx`). C'est la méthode recommandée.

## Licence

MIT

## Monitoring et Management REST (JMX-like)

GoCamel inclut une interface REST permettant de monitorer et de contrôler le cycle de vie des routes (démarrage/arrêt), inspirée du management JMX d'Apache Camel.

```go
package main

import (
    "fmt"
    "github.com/tranchida/gocamel"
)

func main() {
    context := gocamel.NewCamelContext()

    route := context.CreateRouteBuilder().
        From("http://localhost:8080/hello").
        SetID("route-http-1").
        SetBody("Hello World").
        Build()

    context.AddRoute(route)
    context.Start()

    // Démarrer l'interface de management sur le port 8081
    mgmt := gocamel.NewManagementServer(context)
    mgmt.Start(":8081")

    select {}
}
```

### Endpoints de Management

- **État du contexte** : `GET /api/context`
  ```json
  {"started":true,"totalRoutes":1,"startedRoutes":1}
  ```

- **Lister les routes** : `GET /api/routes`
  ```json
  [{"id":"route-http-1","description":"","group":"","started":true}]
  ```

- **Arrêter une route** : `POST /api/routes/{id}/stop`
  ```bash
  curl -X POST http://localhost:8081/api/routes/route-http-1/stop
  ```

- **Démarrer une route** : `POST /api/routes/{id}/start`
  ```bash
  curl -X POST http://localhost:8081/api/routes/route-http-1/start
  ```
