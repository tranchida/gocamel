+++
title = "Fonctionnalités"
weight = 1
+++
## Capacités Principales

### Framework d'Intégration d'Entreprise

GoCamel est un framework d'intégration léger et natif à Go, inspiré d'Apache Camel. Il fournit :

- **Architecture Modulaire** — Conception basée sur les composants
- **Sécurité des Types** — Sécurité de typage complète en Go
- **Concurrence** — Construit sur les goroutines de Go
- **Zéro Dépendance Externe** — Pilotes de base de données optionnels uniquement

### Modèle de Route et d'Endpoint

```go
context := gocamel.NewCamelContext()

route := context.CreateRouteBuilder().
    From("direct:input").      // Endpoint consommateur
    Process(myProcessor).      // Processeur personnalisé
    To("direct:output").       // Endpoint producteur
    Build()

context.AddRoute(route)
context.Start()
```

### Modèle de Message

```mermaid
flowchart TB
    subgraph Exchange["Exchange"]
        direction TB
        subgraph Properties["Propriétés"]
            P["correlationId: 'abc-123'<br/>routeId: 'route-1'"]
        end
        subgraph In["In (Message entrant)"]
            I["<b>Headers :</b> map[string]any<br/>Content-Type: 'application/json'<br/><br/><b>Body :</b> any<br/>{'name': 'John', 'age': 30}"]
        end
        subgraph Out["Out (Message sortant)"]
            O["<b>Headers :</b> map[string]any<br/>X-Processed: 'true'<br/><br/><b>Body :</b> any<br/>{'name': 'John', 'age': 31}"]
        end
    end
```

### Simple Language

Langage d'expressions dynamique pour le routage et les transformations :

```go
// Accès aux headers
SimpleSetHeader("X-ID", "${uuid}")

// Manipulation du body
SimpleSetBody("Bonjour ${body}")

// Routage conditionnel
When("${header.priority} == 'high'")

// Accès null-safe
When("${body?.user?.name} != ''")

// Date/heure
When("${date:now:yyyy-MM-dd}")
```

### Écosystème de Composants

#### Noyau (Intégré)

- **Direct** — Routage en mémoire
- **Timer** — Exécution planifiée

#### Sécurité

- **Validation des Chemins** — Protection contre les attaques de traversée de répertoire
- **Prévention des Injections SQL** — Liaison de paramètres ; la requête est une
  configuration de confiance, jamais interpolée avec les données du message
- **Assainissement des Entrées** — Suppression des octets nuls et caractères de contrôle
- **Opérations de Chemins Sécurisées** — Vérification des limites pour les
  opérations sur fichiers, avec résolution des liens symboliques pour qu'un lien
  placé dans le répertoire de base ne puisse pas s'en échapper
- **Aucune Réflexion d'Identifiants** — le consumer HTTP ne renvoie jamais les
  headers de la requête entrante dans sa réponse
- **Lectures de Corps Bornées** — les corps de requête et de réponse HTTP sont
  limités en taille (32 Mio par défaut) pour prévenir l'épuisement mémoire
- **Confinement des Panics** — un processeur qui panique fait échouer son
  exchange, pas le processus

#### Transfert de Fichiers

- **File** — Système de fichiers local
- **FTP/FTPS** — File Transfer Protocol
- **SFTP** — Transfert de fichiers SSH
- **SMB** — Partages Windows

#### Réseau

- **HTTP** — Serveur/client HTTP
- **Net** — Sockets TCP/UDP brutes
- **gRPC** — Client/serveur RPC haute performance

#### Messagerie

- **Kafka** — Streaming d'événements distribué Apache Kafka
- **NATS** — Messagerie d'événements haute performance
- **STOMP** — Messagerie ActiveMQ/JMS via STOMP
- **Redis** — Commandes et Pub/Sub
- **Telegram** — Bot API
- **Mail** — SMTP/IMAP/POP3
- **Go Channel** — Canaux bufferisés en mémoire

#### IA

- **OpenAI** — Modèles GPT

#### Base de Données

- **SQL** — Exécution de requêtes
- **SQL-Stored** — Procédures stockées

#### Planification

- **Cron** — Expressions cron

#### Transformation

- **XSLT** — Transformation XML
- **XSD** — Validation de schéma
- **Template** — Templates Go
- **Exec** — Exécution de commandes

### Enterprise Integration Patterns

| Pattern     | Description                  | Statut |
|-------------|------------------------------|--------|
| Choice      | Routeur basé sur le contenu  | ✅      |
| Split       | Découpage de message         | ✅      |
| Aggregate   | Agrégateur de messages       | ✅      |
| Multicast   | Destinations multiples       | ✅      |
| Filter      | Filtrage conditionnel        | ✅      |
| Transform   | Transformation de message    | ✅      |
| Enrich      | Enrichisseur de contenu      | ✅      |
| PollEnrich  | Enrichisseur à la demande    | ✅      |
| Validate    | Validateur de message        | ✅      |
| ToD         | Endpoint dynamique           | ✅      |
| Stop        | Arrêt du routage             | ✅      |
| Pipeline    | Branches séquentielles       | ✅      |
| SetHeader   | Manipulation de headers      | ✅      |
| SetProperty | Manipulation de propriétés   | ✅      |

### API REST de Management

Surveiller et contrôler les routes via HTTP :

```go
mgmt := gocamel.NewManagementServer(context).
    SetAuthToken(os.Getenv("MGMT_TOKEN")) // optionnel : exige "Authorization: Bearer <token>"
if err := mgmt.Start("127.0.0.1:8081"); err != nil { // renvoie les erreurs de bind
    log.Fatal(err)
}
defer mgmt.Stop()
```

**Endpoints :**

- `GET /api/context` — État du contexte et nombre de routes
- `GET /api/routes` — Lister les routes
- `POST /api/routes/{id}/start` — Démarrer une route (`409` tant que le contexte est arrêté)
- `POST /api/routes/{id}/stop` — Arrêter une route

**Sécurité :** l'API peut arrêter n'importe quelle route, chaque requête est donc contrôlée :

- l'en-tête `Host` doit être une adresse IP, `localhost` ou un nom passé à
  `SetAllowedHosts(...)` : cela bloque le DNS rebinding, où une page web fait
  pointer son propre domaine vers `127.0.0.1` pour atteindre un serveur local ;
- les `POST` cross-origin d'un navigateur sont rejetés
  (`http.CrossOriginProtection`), ce qui bloque le CSRF ;
- avec `SetAuthToken`, une requête sans le jeton bearer reçoit `401`.

Sans jeton, l'API n'est pas authentifiée : liez-la à une adresse loopback ou
placez-la derrière un reverse proxy qui authentifie. Les erreurs sont
journalisées ; les réponses ne contiennent qu'un message générique.

### Configuration

Plusieurs sources d'identification :

1. **Directement dans l'URI** — `ftp://user:pass@host`
2. **Paramètres de requête** — `?username=user&password=pass`
3. **Variables d'environnement** — `FTP_PASSWORD` (recommandé)

```go
builder.From("ftp://host?username=${env:FTP_USER}")
```

### Support de Test

```go
import "testing"

func TestRoute(t *testing.T) {
    ctx := gocamel.NewCamelContext()

    // Construire la route de test
    route := ctx.CreateRouteBuilder().
        From("direct:test").
        SetBody("traité").
        To("direct:result").
        Build()

    ctx.AddRoute(route)
    ctx.Start()
    defer ctx.Stop()

    // Envoyer le message de test
    exchange := gocamel.NewExchange(context.Background())
    exchange.GetIn().SetBody("entrée")

    endpoint, _ := ctx.CreateEndpoint("direct:test")
    producer, _ := endpoint.CreateProducer()
    producer.Send(exchange)

    // Vérifier
    result := exchange.GetOut().GetBody()
    if result != "traité" {
        t.Fail()
    }
}
```

### Performance

- **Allocations minimales** — Pooling d'objets
- **Efficacité des goroutines** — Threads légers
- **Pas de réflexion** — Opérations type-safe
- **Initialisation paresseuse** — Chargement à la demande

### Extensibilité

```go
// Composant personnalisé
type CustomComponent struct{}

func (c *CustomComponent) CreateEndpoint(uri string) (gocamel.Endpoint, error) {
    return &CustomEndpoint{}, nil
}

type CustomEndpoint struct{}

func (e *CustomEndpoint) CreateProducer() (gocamel.Producer, error) {
    return &CustomProducer{}, nil
}

func (e *CustomEndpoint) CreateConsumer(p gocamel.Processor) (gocamel.Consumer, error) {
    return &CustomConsumer{}, nil
}

// Enregistrer
ctx.AddComponent("custom", &CustomComponent{})
```

### Bonnes Pratiques

- Utiliser les **variables d'environnement** pour les identifiants
- Définir des **IDs de route** pour la gestion
- Implémenter la **gestion des erreurs** dans les processeurs
- Utiliser un **stockage approprié** pour les agrégations
- Configurer des **timeouts** pour les systèmes externes
- Activer l'**API REST** pour la surveillance en production