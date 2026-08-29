<div align="center">

# GoCamel

**Framework d'Intégration d'Entreprise pour Go**

[![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go)](https://golang.org/)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

🌐 **Langue / Language**: [**🇺🇸 English**](./README.md) | 🇫🇷 Français

</div>

---

GoCamel est une bibliothèque d'intégration d'entreprise inspirée d'[Apache Camel](https://camel.apache.org/), écrite en
Go. Elle fournit un puissant DSL pour créer des routes d'intégration connectant différents systèmes et services.

## ✨ Fonctionnalités

- 🔀 **Architecture Route & Endpoint** - DSL fluide pour construire des flux
- 📬 **Gestion des Messages** - Corps et en-têtes avec accesseurs typés (`GetBodyAsString`, `GetHeaderAsInt`, etc.)
- 🔄 **Contexte Camel** - Gestion du cycle de vie des routes et composants
- 📦 **Registre & Beans** - Enregistrez et recherchez des processeurs et beans personnalisés par nom
- ⚙️ **Processeurs** - Implémentation de logique personnalisée via l'interface `Processor` et `ProcessRef`
- ⚖️ **Routes Transactionnelles** - Pattern Unit of Work pour un traitement fiable
- 🧩 **Enterprise Integration Patterns** - Split, Aggregate, Multicast, Choice, Filter, Recipient List, Routing Slip, Wire Tap,
  Stop, ToD, Loop, Saga, Transform, Enrich, PollEnrich, Validate, Message History
- 📝 **Simple Language & JSONPath** - Expressions dynamiques (`${body}`, `${header.name}`, `${jsonpath:$.path}`, fonctions)
- 🔌 **Composants Multiples** - HTTP, File, FTP, SFTP, SMB, Mail, SQL, Telegram, OpenAI, Cron, Redis, NATS, Go Channel (chan), TCP/UDP (net), etc.
- 🛠️ **API REST de Management** - Monitoring et contrôle inspiré de JMX
- 🔒 **Utilitaires de Sécurité** - Protection contre les traversées de répertoire, prévention des injections SQL,
  assainissement des entrées

## 📦 Installation

Le module core (moteur de routage, EIPs, Simple Language et tous les
composants sans dépendance externe : direct, seda, chan, timer, http, exec,
template, sql) n'a **aucune dépendance externe à l'exécution** et se compile sans
CGO (les tests utilisent le driver pure-Go `modernc.org/sqlite`) :

```bash
go get gitlab.com/tranchida/gocamel
```

Les composants qui s'appuient sur des bibliothèques tierces vivent dans leurs
propres modules Go — vous ne récupérez que les dépendances de ce que vous
utilisez réellement :

```bash
go get gitlab.com/tranchida/gocamel/components/kafka     # segmentio/kafka-go
go get gitlab.com/tranchida/gocamel/components/grpc      # google.golang.org/grpc
go get gitlab.com/tranchida/gocamel/components/redis    # go-redis
go get gitlab.com/tranchida/gocamel/components/nats     # nats.go
go get gitlab.com/tranchida/gocamel/components/mongodb  # mongo-driver
go get gitlab.com/tranchida/gocamel/components/mail     # SMTP/IMAP/POP3
go get gitlab.com/tranchida/gocamel/components/ftp
go get gitlab.com/tranchida/gocamel/components/sftp
go get gitlab.com/tranchida/gocamel/components/smb
go get gitlab.com/tranchida/gocamel/components/telegram
go get gitlab.com/tranchida/gocamel/components/openai
go get gitlab.com/tranchida/gocamel/components/anthropic
go get gitlab.com/tranchida/gocamel/components/ollama
go get gitlab.com/tranchida/gocamel/components/cron     # robfig/cron
go get gitlab.com/tranchida/gocamel/components/file     # watcher fsnotify
go get gitlab.com/tranchida/gocamel/observability/prometheus
go get gitlab.com/tranchida/gocamel/observability/otel
```

```go
import (
	"gitlab.com/tranchida/gocamel"
	"gitlab.com/tranchida/gocamel/components/redis"
)

ctx := gocamel.NewCamelContext()
ctx.AddComponent("redis", redis.NewRedisComponent())
```

Les métriques et le tracing sont des intercepteurs de route fournis par les
modules d'observabilité :

```go
import (
	gocamelotel "gitlab.com/tranchida/gocamel/observability/otel"
	gocamelprom "gitlab.com/tranchida/gocamel/observability/prometheus"
)

ctx.CreateRouteBuilder().
	From("timer:tick?period=5s").
	Intercept(gocamelprom.Metrics()).
	Intercept(gocamelotel.Tracing()).
	...
```

## 🚀 Démarrage Rapide

```go
package main

import (
    "gitlab.com/tranchida/gocamel"
)

func main() {
    context := gocamel.NewCamelContext()
    
    route := context.CreateRouteBuilder().
        From("timer:tick?period=5s").
        SetBody("Hello World").
        Log("${body}").
        To("direct:output").
        Build()
    
    context.AddRoute(route)
    context.Start()
    select {}
}
```

## 📝 Exemples Simple Language

```go
// Expressions dynamiques
builder.From("direct:start").
    SimpleSetBody("Bonjour ${body} à ${date:now}").
    SimpleSetHeader("X-Request-ID", "${uuid}").
    To("direct:output")

// Routage basé sur le contenu
builder.From("direct:start").
    Choice().
        When("${header.priority == 'high'}").
            To("direct:urgent").
        When("${body['count'] > 100}").
            To("direct:large-batch").
        Otherwise().
            To("direct:normal").
    EndChoice()
```

## 🔌 Composants Disponibles

| Composant      | Description                    |
|----------------|--------------------------------|
| **HTTP**       | Serveur et client HTTP         |
| **Net**        | Sockets TCP/UDP bruts          |
| **Kafka**      | Messagerie Apache Kafka        |
| **gRPC**       | Client et serveur gRPC         |
| **File**       | Opérations système de fichiers |
| **FTP/SFTP**   | Transfert via FTP/SSH          |
| **SMB**        | Partages Windows/Samba         |
| **Direct**     | Routage synchrone en mémoire   |
| **NATS**       | Messagerie événementielle asynchrone |
| **Redis**      | Commandes & Messagerie Pub/Sub |
| **Go Channel** | Intégration de canal Go natif  |
| **Timer/Cron** | Déclencheurs planifiés         |
| **Telegram**   | Intégration Bot                |
| **OpenAI**     | ChatGPT/GPT-4 & fournisseurs compatibles OpenAI (OpenRouter, Groq, ...) |
| **Anthropic**  | API Messages Claude (native)  |
| **Ollama**     | LLMs locaux (chat/generate/embed, API native) |
| **Mail**       | Email SMTP/IMAP/POP3           |
| **SQL**        | Requêtes base de données       |
| **MongoDB**    | Opérations CRUD MongoDB        |
| **XSLT/XSD**   | Transformation/validation XML  |
| **Template**   | Templates Go natifs            |
| **Exec**       | Exécution commandes système    |

## 🧾 Data Formats

| Data Format | Description                                                       |
|-------------|-------------------------------------------------------------------|
| **JSON**    | Marshal/unmarshal JSON avec `MarshalJson()` / `UnmarshalJson()` ou `Marshal(NewJSONDataFormat(...))` |
| **XML**     | Marshal/unmarshal XML avec `MarshalXml()` / `UnmarshalXml()` ou `Marshal(NewXMLDataFormat(...))` |
| **CSV**     | Marshal/unmarshal CSV avec `MarshalCSV()` / `UnmarshalCSV()` ou `Marshal(NewCSVDataFormat(...))` |
| **RSS**     | Marshal/unmarshal XML RSS avec `Marshal(NewRSSDataFormat())` et `Unmarshal(NewRSSDataFormat())` |

## 🔒 Sécurité

GoCamel inclut des utilitaires de sécurité intégrés pour se protéger contre les vulnérabilités courantes :

### Fonctionnalités de Sécurité

- **Protection contre les Traversées de Répertoire** - Prévient les attaques de traversée de répertoire (ex.
  `../etc/passwd`)
- **Prévention des Injections SQL** - La requête est une configuration de confiance, jamais interpolée avec les données du message ; les valeurs dynamiques sont liées en paramètres positionnels. Les requêtes superposées sont rejetées, les guillemets simples/doubles, backticks et commentaires restent autorisés
- **Assainissement des Entrées** - Supprime les octets nuls et caractères de contrôle des entrées utilisateur
- **Validation des Chemins** - Garantit que les chemins restent dans les répertoires autorisés, en résolvant les liens symboliques pour qu'un lien placé dans le répertoire de base ne puisse pas s'en échapper
- **Aucune Réflexion d'Identifiants** - Le consommateur HTTP ne renvoie jamais les en-têtes de la requête entrante (`Cookie`, `Authorization`, ...) dans sa réponse
- **Lectures de Corps Bornées** - Les corps de requête et de réponse HTTP sont limités en taille (32 Mio par défaut) pour prévenir l'épuisement mémoire
- **Confinement des Panics** - Un processeur qui panique fait échouer son exchange, pas le processus entier

### Utilitaires de Sécurité (`security.go`)

```go
// Valider un chemin de fichier
err := gocamel.ValidatePath("/data/file.txt")
if err != nil {
    // Le chemin contient des motifs de traversée ou des octets nuls
}

// S'assurer que le chemin est dans un répertoire spécifique
err = gocamel.ValidatePathInDir("/data/output.txt", "/data")

// Vérifier si le chemin est sûr
isSafe := gocamel.IsSafePath("/data/file.txt", false)

// Assainir une entrée utilisateur
sanitized := gocamel.SanitizeInput(userInput)
```

### Bonnes Pratiques

- Toujours valider les chemins de fichiers provenant des entrées utilisateur
- Utiliser `ValidatePathInDir()` lors de l'utilisation d'uploads ou d'opérations de fichiers
- Ne jamais exécuter des commandes externes non assainies
- Utiliser le binding de paramètres pour les requêtes SQL au lieu de la concaténation de strings
- Installer un middleware sur le consommateur HTTP (`SetMiddleware`) avant de l'exposer à un réseau non fiable — il est livré sans authentification

## ⚠️ Notes de Migration

Deux comportements par défaut ont changé, pour la sécurité et l'alignement sur
Apache Camel. Les deux sont désactivables si l'ancien comportement est requis.

**L'en-tête `CamelSqlQuery` ne remplace plus la requête configurée.**
Il remplaçait la requête SQL dans son intégralité : tout composant reportant
des métadonnées externes sur les en-têtes pouvait donc donner à un appelant le
contrôle total de la requête. Il est désormais ignoré sauf opt-in explicite :

```go
// Avant : l'en-tête remplaçait silencieusement la requête
builder.To("sql://appdb?query=SELECT+1")

// Après : opt-in explicite, et uniquement avec des en-têtes de confiance
builder.To("sql://appdb?allowHeaderOverride=true&query=SELECT+1")
```

**Le producteur HTTP considère un statut ≥ 300 comme une erreur**, comme
Apache Camel. L'erreur enveloppe `gocamel.ErrHTTPStatus` ; le corps et
`CamelHttpResponseCode` restent disponibles sur `Out` :

```go
// Rétablir le comportement permissif précédent
http := gocamel.NewHTTPComponent()
http.SetThrowExceptionOnFailure(false)
```

**MongoDB `update` refuse un filtre vide**, y compris une clé `"filter"`
absente et un document vide (`{}` / `bson.M{}`). Comme `remove`, cela
mettrait à jour tous les documents de la collection. L'opération est rejetée
sauf opt-in explicite :

```go
// Opt-in explicite pour autoriser la mise à jour de toute la collection
builder.To("mongodb://myconn?collection=items&operation=update&allowUpdateAll=true")
```

Deux autres changements ne nécessitent probablement aucune action : découper un
corps `[]byte` produit désormais une seule part au lieu d'un exchange par
octet, et le consommateur HTTP ne renvoie plus les en-têtes entrants inchangés
dans sa réponse.

## 📚 Documentation

- [**Site de Documentation en Ligne**](https://gocamel.tranchida.cloud/fr/)
- [Exemples](examples/)
- [Architecture](https://gocamel.tranchida.cloud/fr/architecture/)
- [Référence API](https://gocamel.tranchida.cloud/fr/reference/)

## 🤝 Contribution

Les contributions sont les bienvenues ! Voir les [GitLab Issues](https://gitlab.com/tranchida/gocamel/-/issues).

## ✍️ Auteur

**Giampaolo Tranchida** - [giampaolo@tranchida.ch](mailto:giampaolo@tranchida.ch)

## 📄 Licence

Licence Apache Version 2.0 - voir le fichier [LICENSE](LICENSE) pour les détails.

---

<div align="center">
  <sub>Fait avec ❤️ par [Giampaolo Tranchida](mailto:giampaolo@tranchida.ch) et l'équipe GoCamel</sub>
</div>
