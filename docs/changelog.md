# Changelog

Toutes les modifications notables de ce projet.

Le format est basé sur [Keep a Changelog](https://keepachangelog.com/fr/1.0.0/),
et ce projet adhère à [Semantic Versioning](https://semver.org/lang/fr/spec/v2.0.0.html).

## [Unreleased]

### Ajout
- Composant Cron pour le scheduling avancé
- Nouveaux EIP : Stop, ToD (To Dynamic)
- Headers/Properties manipulation
- Management REST API
- Component XSLT pour transformations XML
- Component XSD pour validation XML

### Modifié
- Amélioration des performances du Multicast en mode parallèle
- Refactoring de l'architecture des endpoints

### Corrigé
- Gestion des erreurs dans le composant SFTP
- Leak de goroutines dans Timer

---

## [0.5.0] - 2026-03-15

### Ajout
- Composant OpenAI pour intégration LLM
- Composant Telegram pour bots
- EIP SetHeaders/SetHeadersFunc
- RemoveHeaders/RemoveProperties avec patterns wildcards

### Modifié
- Migration vers Go 1.21
- Optimisation de la mémoire dans Exchange pooling

---

## [0.4.0] - 2026-02-01

### Ajout
- Composant Exe pour exécution de commandes
- Composant Timer pour scheduling simple
- Composant Cron (beta)
- Support des Query Params dans les URIs Camel

### Modifié
- Réécriture complète du composant File
- Meilleure gestion des fichiers verrouillés

---

## [0.3.0] - 2026-01-10

### Ajout
- EIP Multicast avec support du parallélisme
- EIP Split avec propriétés CamelSplit*
- Composant SMB pour partages Windows
- Composant Direct pour routage synchrone in-memory

### Corrigé
- Race condition dans le composant HTTP

---

## [0.2.0] - 2025-12-01

### Ajout
- EIP Aggregate avec AggregationStrategy
- MemoryAggregationRepository
- SQLAggregationRepository (SQLite)
- Composant SFTP avec auth SSH key

### Modifié
- Breaking change: signature de SplitEIP

---

## [0.1.0] - 2025-11-01

### Ajout
- Core: CamelContext, Exchange, Message
- DSL RouteBuilder fluent
- Composants: HTTP, File, FTP
- EIP: Split (basique)
- Logging intégré

---

## Historique

```
0.1.0 ── 0.2.0 ── 0.3.0 ── 0.4.0 ── 0.5.0 ── unreleased
  │         │         │         │         │
  └─────────┴─────────┴─────────┴─────────┴──────▶ main
```
