<div align="center">

# GoCamel

**Framework d'Intégration d'Entreprise pour Go**

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go)](https://golang.org/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

🌐 **Langue / Language**: [**🇺🇸 English**](./README.md) | 🇫🇷 Français

</div>

---

GoCamel est une bibliothèque d'intégration d'entreprise inspirée d'[Apache Camel](https://camel.apache.org/), écrite en Go. Elle fournit un puissant DSL pour créer des routes d'intégration connectant différents systèmes et services.

## ✨ Fonctionnalités

- 🔀 **Architecture Route & Endpoint** - DSL fluide pour construire des flux
- 📬 **Gestion des Messages** - Corps et en-têtes avec sécurité de type
- 🔄 **Contexte Camel** - Gestion du cycle de vie des routes et composants
- 🧩 **Patterns d'Intégration** - Split, Aggregate, Multicast, Choice, Stop, ToD
- 📝 **Simple Language** - Expressions dynamiques (`${body}`, `${header.name}`, fonctions)
- 🔌 **Composants Multiples** - HTTP, File, FTP, SFTP, SMB, Mail, SQL, Telegram, OpenAI, Cron, etc.
- 🛠️ **API REST de Management** - Monitoring et contrôle inspiré de JMX

## 📦 Installation

```bash
go get github.com/tranchida/gocamel
```

## 🚀 Démarrage Rapide

```go
package main

import (
    "github.com/tranchida/gocamel"
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

| Composant | Description |
|-----------|-------------|
| **HTTP** | Serveur et client HTTP |
| **File** | Opérations système de fichiers |
| **FTP/SFTP** | Transfert via FTP/SSH |
| **SMB** | Partages Windows/Samba |
| **Direct** | Routage synchrone en mémoire |
| **Timer/Cron** | Déclencheurs planifiés |
| **Telegram** | Intégration Bot |
| **OpenAI** | API ChatGPT/GPT-4 |
| **Mail** | Email SMTP/IMAP/POP3 |
| **SQL** | Requêtes base de données |
| **XSLT/XSD** | Transformation/validation XML |
| **Template** | Templates Go natifs |
| **Exec** | Exécution commandes système |

## 📚 Documentation

- [Documentation Complète](docs/)
- [Exemples](examples/)
- [Architecture](docs/architecture.md)
- [Référence API](docs/reference.md)

## 🤝 Contribution

Les contributions sont les bienvenues ! Voir les [Issues GitHub](https://github.com/tranchida/gocamel/issues).

## 📄 Licence

Licence MIT - voir le fichier [LICENSE](LICENSE) pour les détails.

---

<div align="center">
  <sub>Fait avec ❤️ par l'équipe GoCamel</sub>
</div>
