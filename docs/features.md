# Fonctionnalités

## Vue d'ensemble

GoCamel est un framework d'intégration d'entreprise qui simplifie la connexion entre différents systèmes et services. Inspiré d'Apache Camel, il apporte la puissance des patterns d'intégration enterprise (EIP) à l'écosystème Go.

## Fonctionnalités clés

### 🛤️ Routage puissant

- **DSL Fluent** — API Go idiomatique pour définir des routes complexes
- **Pattern Builder** — Construction chaînée d'endpoints et de processors
- **Routing dynamique** — ToD (To Dynamic) pour URIs résolues à l'exécution
- **Content-based routing** — Routage basé sur le contenu des messages

### 🔌 Composants intégrés

| Catégorie | Composants |
|-----------|------------|
| **Protocoles** | HTTP, FTP, SFTP, SMB, File |
| **Messaging** | Direct (in-memory), Telegram |
| **Scheduling** | Timer, Cron |
| **IA/LLM** | OpenAI Chat Completion |
| **Transformation** | XSLT, XSD validation, Template |
| **Exécution** | Exec (commandes système) |

### 🧩 EIP (Enterprise Integration Patterns)

- **Splitter** — Divise un message en parties
- **Aggregator** — Combine plusieurs messages en un seul
- **Multicast** — Envoie à plusieurs destinations
- **Pipeline** — Chaîne séquentielle de processors
- **Choice** — Routage basé sur le contenu (Content-Based Router)
- **Stop** — Arrête le routage conditionnellement
- **Headers/Properties** — Manipulation des métadonnées

### 💬 Simple Language

Moteur d'expressions inspiré d'Apache Camel pour manipuler les données d'échange :

- **Variables** : `${body}`, `${header.name}`, `${exchangeProperty.prop}`
- **Notations** : Point (`body.field`), Crochets (`body['key']`), Null-safe (`body?.field`)
- **Fonctions** : `${date:now}`, `${random(MAX)}`, `${uuid}`
- **Comparaisons** : `==`, `!=`, `>`, `<`, `>=`, `<=`
- **Intégration** : `SimpleSetBody()`, `SimpleSetHeader()`, `Choice().When()`

### 📊 Management & Monitoring

- **API REST** — Interface JMX-like pour controler les routes
- **Logging intégré** — Tracing des messages
- **Metrics** — État du contexte et des routes
- **Graceful shutdown** — Arrêt propre des routes actives

### 🔐 Sécurité

- **Variables d'environnement** — Configuration sensible externalisée
- **No hardcoded credentials** — Tokens et mots de passe via env vars
- **URI-safe** — Paramètres sensibles encodés

## Roadmap

### ✅ Disponible

- [x] Core: Exchange, Message, Context
- [x] EIP: Split, Aggregate, Multicast, Choice, Pipeline
- [x] Composants HTTP, File, FTP, SFTP, SMB
- [x] Composants Telegram, OpenAI
- [x] Scheduling: Timer, Cron
- [x] REST Management API
- [x] Simple Language (expressions dynamiques)
- [x] Composant Template (Velocity-like)

### 🚧 En cours

- [ ] Composant JMS (ActiveMQ/Artemis)
- [ ] Composant Kafka
- [ ] Composant AWS S3
- [ ] Composant Google Cloud Storage
- [ ] Retry policies avancées
- [ ] Circuit Breaker pattern

### 📋 Envisagé

- [ ] Dead Letter Channel
- [ ] Message Queue (in-memory)
- [ ] Streaming (WebSocket)
- [ ] Metrics Prometheus
- [ ] Distributed tracing (OpenTelemetry)

## Comparison

| Feature | GoCamel | Apache Camel | Go-Native libs |
|---------|---------|--------------|----------------|
| DSL Fluent | ✅ | ✅ | ❌ |
| EIP Patterns | ✅ | ✅ | ❌/Partiel |
| Simple Language | ✅ | ✅ | ❌ |
| Performance | ⚡ Go | ☕ JVM | ⚡ Go |
| Embarqué | ✅ Oui | ❌ Lourd | ✅ |
| Mémoire | Basse | Haute | Basse |

