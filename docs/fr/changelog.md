# Changelog

Toutes les modifications notables de ce projet seront documentées dans ce fichier.

Le format est basé sur [Keep a Changelog](https://keepachangelog.com/fr/1.0.0/),
et ce projet adhère à [Semantic Versioning](https://semver.org/lang/fr/spec/v2.0.0.html).

## [Unreleased]

### Ajouté
- **EIP Delay** — `.Delay(duration)` met l'exchange en pause pour une durée fixe ; `.DelayFunc(fn)` calcule la durée par exchange ; tous deux respectent l'annulation du contexte et échouent proprement si la fonction dynamique est nil
- **EIP Content Enricher** — `.Enrich(uri, stratégie)` récupère des données depuis un endpoint externe et les fusionne dans l'exchange via une `AggregationStrategy` ; passer `nil` pour remplacer directement le corps
- **EIP Throttle** — `.Throttle(maxRequests, période)` limite le débit ; les exchanges excédentaires sont retardés (jamais perdus) via un compteur à fenêtre fixe ; les limites invalides échouent rapidement et les exchanges en attente respectent l'annulation du contexte
- **EIP Message Filter** — `.Filter(expression)` supprime les exchanges ne correspondant pas à un prédicat Go ou à une expression Simple Language
- **`Exchange.PropagateOutToIn()`** — helper extrait qui copie le corps et les en-têtes de `Out` vers `In` ; utilisé en interne par `To`, `ToD` et `WireTap`
- **Améliorations Aggregator** — verrouillage par clé de corrélation via `sync.Map` (remplace le mutex global), `SetCompletionTimeout(d)` et `SetCompletionPredicate(fn)` comme stratégies de complétion
- **Durcissement DeadLetterChannel** — la relivraison rejoue désormais le processeur en échec avant de poursuivre la route, utilise des IDs d'exchange stables pour le suivi des tentatives, et nettoie l'état de retry après une relivraison réussie sans dépendre des callbacks `Exchange.Done()` des consumers
- Support transactionnel via **Unit of Work** et **Synchronization**
  - Nouvelle méthode `.Transacted()` dans le DSL RouteBuilder
  - Traitement transactionnel dans les composants `file`, `ftp`, `sftp`, `smb` et `mail` (post-traitement déclenché à la fin de la route)
- Documentation complète en anglais
- Structure de documentation bilingue (EN/FR)
- Support du thème MkDocs Material
- Nouveau fichier `security.go` avec utilitaires de validation de sécurité
  - `ValidatePath()` - valide les chemins de fichiers contre les traversées
  - `ValidatePathInDir()` - garantit que les chemins restent dans les répertoires autorisés
  - `IsSafePath()` - vérifie si un chemin est sûr
  - `SanitizeInput()` - supprime les caractères dangereux des entrées utilisateur

### Sécurité
- Corrigé les vulnérabilités de traversée de répertoire dans les composants FTP, SFTP et SMB
- Corrigé la vulnérabilité d'injection de commandes dans le composant Exec
- Corrigé les vulnérabilités d'injection SQL dans les composants SQL et SQL-Stored
- Ajout de l'assainissement des entrées pour les URI des composants
- Tous les composants valident désormais les chemins de fichiers avant les opérations

### Modifié
- **Traductions** : Tous les commentaires de code français traduits en anglais (650+ commentaires)

### Ajout
- Composant SQL (producer) pour exécuter des requêtes `SELECT`/`INSERT`/`UPDATE`/`DELETE` via `database/sql`, avec support des paramètres positionnels, `outputType` (`SelectList`/`SelectOne`), mode batch et transactions
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
unreleased
     │
     ├──────▶ main
```
