<div align="center">

# GoCamel

**Framework d'Intégration d'Entreprise pour Go**

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go)](https://golang.org/)
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
- 🧩 **Patterns d'Intégration** - Split, Aggregate, Multicast, Choice, Recipient List, Routing Slip, Wire Tap, Stop, ToD, Loop, Saga, Transform, Message History
- 📝 **Simple Language** - Expressions dynamiques (`${body}`, `${header.name}`, fonctions)
- 🔌 **Composants Multiples** - HTTP, File, FTP, SFTP, SMB, Mail, SQL, Telegram, OpenAI, Cron, Redis, NATS, Go Channel (chan), etc.
- 🛠️ **API REST de Management** - Monitoring et contrôle inspiré de JMX
- 🔒 **Utilitaires de Sécurité** - Protection contre les traversées de répertoire, prévention des injections SQL,
  assainissement des entrées

## 📦 Installation

Le module core (moteur de routage, EIPs, Simple Language et tous les
composants sans dépendance externe : direct, seda, chan, timer, http, exec,
template, sql) n'a **aucune dépendance externe** :

```bash
go get gitlab.com/tranchida/gocamel
```

Les composants qui s'appuient sur des bibliothèques tierces vivent dans leurs
propres modules Go — vous ne récupérez que les dépendances de ce que vous
utilisez réellement :

```bash
go get gitlab.com/tranchida/gocamel/components/redis    # go-redis
go get gitlab.com/tranchida/gocamel/components/nats     # nats.go
go get gitlab.com/tranchida/gocamel/components/mongodb  # mongo-driver
go get gitlab.com/tranchida/gocamel/components/mail     # SMTP/IMAP/POP3
go get gitlab.com/tranchida/gocamel/components/ftp
go get gitlab.com/tranchida/gocamel/components/sftp
go get gitlab.com/tranchida/gocamel/components/smb
go get gitlab.com/tranchida/gocamel/components/telegram
go get gitlab.com/tranchida/gocamel/components/openai
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
| **File**       | Opérations système de fichiers |
| **FTP/SFTP**   | Transfert via FTP/SSH          |
| **SMB**        | Partages Windows/Samba         |
| **Direct**     | Routage synchrone en mémoire   |
| **NATS**       | Messagerie événementielle asynchrone |
| **Redis**      | Commandes & Messagerie Pub/Sub |
| **Go Channel** | Intégration de canal Go natif  |
| **Timer/Cron** | Déclencheurs planifiés         |
| **Telegram**   | Intégration Bot                |
| **OpenAI**     | API ChatGPT/GPT-4              |
| **Mail**       | Email SMTP/IMAP/POP3           |
| **SQL**        | Requêtes base de données       |
| **XSLT/XSD**   | Transformation/validation XML  |
| **Template**   | Templates Go natifs            |
| **Exec**       | Exécution commandes système    |

## 🧾 Data Formats

| Data Format | Description                                                       |
|-------------|-------------------------------------------------------------------|
| **RSS**     | Marshal/unmarshal XML RSS avec `Marshal(NewRSSDataFormat())` et `Unmarshal(NewRSSDataFormat())` |

## 🔒 Sécurité

GoCamel inclut des utilitaires de sécurité intégrés pour se protéger contre les vulnérabilités courantes :

### Fonctionnalités de Sécurité

- **Protection contre les Traversées de Répertoire** - Prévient les attaques de traversée de répertoire (ex.
  `../etc/passwd`)
- **Prévention des Injections SQL** - Validation des entrées pour les requêtes SQL
- **Assainissement des Entrées** - Supprime les octets nuls et caractères de contrôle des entrées utilisateur
- **Validation des Chemins** - Garantit que les chemins restent dans les répertoires autorisés

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

## 📚 Documentation

- [**Site de Documentation en Ligne**](https://tranchida.gitlab.io/gocamel/fr/)
- [Exemples](examples/)
- [Architecture](https://tranchida.gitlab.io/gocamel/fr/architecture/)
- [Référence API](https://tranchida.gitlab.io/gocamel/fr/reference/)

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
