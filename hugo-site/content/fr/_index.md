+++
title = "Documentation GoCamel"
weight = 0
[build]
  list = "always"
+++

**Dernière version : v0.16.0 · Go 1.27.0 ou ultérieur.** [Notes de version](releases/v0.16.0/)


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

- [🔐 Protection contre les traversées de répertoire](features.md#securite)
- [🔐 Prévention des injections SQL](features.md#securite)
- [🔐 Assainissement des entrées](features.md#securite)
- [🔐 Opérations de fichiers sécurisées](features.md#securite)
- [🔐 Aucune réflexion d'identifiants dans les réponses HTTP](features.md#securite)
- [🔐 Lectures de corps HTTP bornées](features.md#securite)
- [🔐 Confinement des panics](features.md#securite)

---

🌐 [🇺🇸 English Version](../en/)
