---
hide:
  - navigation
---

# Documentation GoCamel

**Framework d'Intégration d'Entreprise pour Go**

---

Bienvenue dans la documentation GoCamel.

## Liens Rapides

- [Installation](installation.md)
- [Démarrage Rapide](quickstart.md)
- [Concepts Fondamentaux](concepts.md)
- [Composants](components.md)
- [Fonctionnalités](features.md)
- [Référence API](reference.md)

## Fonctionnalités

- **Architecture de Routes et d'Endpoints** - DSL fluide pour construire des flux d'intégration
- **Gestion des Messages** - Body et headers avec typage
- **Enterprise Integration Patterns** - Split, Aggregate, Multicast, Choice, et plus
- **Simple Language** - Expressions dynamiques avec `${body}`, `${header.name}` et fonctions
- **Composants Multiples** - HTTP, File, FTP, SFTP, SMB, Mail, SQL, Telegram, OpenAI, Cron, et plus
- **Sécurité Intégrée** - Protection path traversal, prévention injection SQL, assainissement des entrées

## Note de Sécurité

GoCamel inclut des utilitaires de sécurité intégrés (`security.go`) pour se protéger contre les vulnérabilités
courantes :

- [🔐 Protection contre les traversées de répertoire](features.md#fonctionnalites-de-securite)
- [🔐 Prévention des injections SQL](features.md#fonctionnalites-de-securite)
- [🔐 Assainissement des entrées](features.md#fonctionnalites-de-securite)
- [🔐 Opérations de fichiers sécurisées](features.md#fonctionnalites-de-securite)

---

🌐 [🇺🇸 English Version](../en/)
