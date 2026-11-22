# Composant Mail - GoCamel

Le composant Mail supporte l'envoi et la réception d'emails via plusieurs protocoles.

## Description

GoCamel Mail est un composant complet pour la gestion des emails via:

- **SMTP/SMTPS** (Producer uniquement): envoi d'emails
- **IMAP/IMAPS** (Consumer): réception avec support IDLE
- **POP3/POP3S** (Consumer): réception de messages

## Protocoles supportés

| Protocole | Port standard | Rôle     | Description                     |
|-----------|---------------|----------|---------------------------------|
| SMTP      | 587           | Producer | Envoi avec STARTTLS             |
| SMTPS     | 465           | Producer | Envoi avec TLS natif            |
| IMAP      | 143           | Consumer | Réception avec possibilité IDLE |
| IMAPS     | 993           | Consumer | Réception sécurisée avec IDLE   |
| POP3      | 110           | Consumer | Réception simple                |
| POP3S     | 995           | Consumer | Réception sécurisée             |

## Exemples d'URI

### Envoi SMTP

```go
// SMTP simple (port 587)
.To("smtp://smtp.gmail.com:587?to=dest@example.com&subject=Test")

// SMTPS (port 465) avec authentification
.To("smtps://smtp.gmail.com:465?username=user@gmail.com&password=pass&to=dest@example.com&subject=Hello")
```

### Réception IMAP

```go
// IMAPS avec IDLE (notifications push temps réel)
.From("imaps://imap.gmail.com:993?folderName=INBOX&username=user@gmail.com&password=pass&idle=true&unseen=true")

// IMAP simple sans IDLE
.From("imap://imap.gmail.com:143?folderName=INBOX&username=user@gmail.com&password=pass&unseen=true")
```

### Réception POP3

```go
// POP3S avec suppression après traitement
.From("pop3s://pop.gmail.com:995?username=user@gmail.com&password=pass&delete=true&fetchSize=10")

// POP3 simple sans suppression
.From("pop3://pop.example.com:110?username=user&password=pass&delete=false")
```

## Options disponibles

### Options communes (Consumer & Producer)

| Option              | Type   | Défaut | Description                                       |
|---------------------|--------|--------|---------------------------------------------------|
| `username`          | string | ""     | Nom d'utilisateur pour l'authentification         |
| `password`          | string | ""     | Mot de passe pour l'authentification              |
| `connectionTimeout` | int    | 30000  | Timeout de connexion en ms                        |
| `pollDelay`         | int    | 60000  | Délai entre les polls en ms (Consumer uniquement) |

### Options Producer (envoi)

| Option        | Type   | Défaut       | Description                         |
|---------------|--------|--------------|-------------------------------------|
| `from`        | string | ""           | Adresse expéditeur                  |
| `to`          | string | ""           | Destinataire(s) principal(aux)      |
| `cc`          | string | ""           | Destinataire(s) en copie            |
| `bcc`         | string | ""           | Destinataire(s) en copie cachée     |
| `subject`     | string | ""           | Sujet du message                    |
| `contentType` | string | "text/plain" | Type MIME (text/plain ou text/html) |
| `allowHeaderOverride` | bool | false  | Autorise la surcharge par en-têtes (défaut: false) |
| `requireStartTLS`     | bool | false  | Refuse l'envoi sans STARTTLS (défaut: false)       |

### Options Consumer (réception)

| Option       | Type   | Défaut  | Description                                        |
|--------------|--------|---------|----------------------------------------------------|
| `folderName` | string | "INBOX" | Dossier IMAP à consulter                           |
| `unseen`     | bool   | true    | Ne traiter que les messages non lus                |
| `delete`     | bool   | false   | Supprimer les messages après traitement            |
| `idle`       | bool   | false   | Activer IMAP IDLE (notifications push)             |
| `fetchSize`  | int    | -1      | Nombre max de messages par poll (-1 = illimité)    |
| `peek`       | bool   | true    | Récupérer avec `BODY.PEEK[]` et marquer lu après succès |
| `maxMessageSize` | int64 | 33554432 | Taille max d'un message en octets (32 Mio si <= 0) |

## Exemple complet

```go
package main

import (
    "context"
    "fmt"
    "log"
    "net/url"
    "os"
    "time"

    "github.com/tranchida/gocamel"
    "github.com/tranchida/gocamel/components/mail"
)

func main() {
    ctx := gocamel.NewCamelContext()

    // Enregistrer le composant mail pour tous les protocoles
    mailComponent := mail.NewMailComponent()
    mailComponent.SetDefaultFrom("notification@example.com")
    ctx.AddComponent("smtp", mailComponent)
    ctx.AddComponent("smtps", mailComponent)
    ctx.AddComponent("imap", mailComponent)
    ctx.AddComponent("imaps", mailComponent)

    // Le mot de passe vient de l'environnement (pas d'interpolation ${env:...} :
    // il faut lire la variable en Go et construire l'URI)
    password := os.Getenv("GMAIL_PASSWORD")

    // Route 1: Envoi d'email
    sendRoute, err := ctx.CreateRouteBuilder().
        From("direct:send-email").
        ProcessFunc(func(exchange *gocamel.Exchange) error {
            // Configurer l'email via les headers
            exchange.SetHeader("Subject", "Rapport quotidien")
            exchange.SetHeader("To", "manager@example.com")
            return nil
        }).
        To("smtps://smtp.gmail.com:465?username=user@gmail.com&password=" + url.QueryEscape(password)).
        Build()
    if err != nil {
        log.Fatal(err)
    }

    // Route 2: Réception d'emails IMAP
    receiveRoute, err := ctx.CreateRouteBuilder().
        From("imaps://imap.gmail.com:993?folderName=INBOX&username=user@gmail.com&password=" + url.QueryEscape(password) + "&idle=true&unseen=true").
        ProcessFunc(func(exchange *gocamel.Exchange) error {
            subject, _ := exchange.GetIn().GetHeader("Subject")
            from, _ := exchange.GetIn().GetHeader("From")
            body := exchange.GetIn().GetBody()

            fmt.Printf("Nouvel email reçu de: %v\n", from)
            fmt.Printf("Sujet: %v\n", subject)
            fmt.Printf("Corps: %v\n", body)

            return nil
        }).
        To("direct:processed").
        Build()
    if err != nil {
        log.Fatal(err)
    }

    ctx.AddRoute(sendRoute)
    ctx.AddRoute(receiveRoute)

    if err := ctx.Start(); err != nil {
        log.Fatalf("Erreur démarrage: %v", err)
    }
    defer ctx.Stop()

    // Simuler l'envoi d'un email
    producerCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    exchange := gocamel.NewExchange(producerCtx)
    exchange.SetBody([]byte("Bonjour, voici le rapport demandé."))
    exchange.SetHeader("Subject", "Rapport quotidien")

    // Envoyer via le endpoint direct
    // (nécessite un Producer pour le endpoint direct)

    select {}
}
```

## Headers spéciaux

### Headers entrants (Consumer)

| Header         | Description                    |
|----------------|--------------------------------|
| `From`         | Expéditeur du message          |
| `To`           | Destinataire(s) principal(aux) |
| `Cc`           | Destinataire(s) en copie       |
| `Subject`      | Sujet du message               |
| `Date`         | Date du message                |
| `Message-ID`   | ID unique du message           |
| `Reply-To`     | Adresse de réponse alternative |
| `Content-Type` | Type MIME du message           |
| `Size`         | Taille du corps en octets      |

### Headers pour pièces jointes

| Header                      | Description                                   |
|-----------------------------|-----------------------------------------------|
| `CamelMailAttachment_<nom>` | Contenu d'une pièce jointe                    |
| `CamelMailBodyHTML`         | Version HTML du message (dans les propriétés) |

### Headers de contrôle (Producer vers Consumer)

| Header            | Description                            |
|-------------------|----------------------------------------|
| `CamelMailDelete` | `true` pour supprimer après traitement |
| `CamelMailMoveTo` | Déplacer vers ce dossier               |
| `CamelMailCopyTo` | Copier vers ce dossier                 |

## Fonctionnalités avancées

### IMAP IDLE

La fonctionnalité IDLE permet de recevoir des notifications en temps réel quand de nouveaux messages arrivent, au lieu
de faire du polling périodique.

```go
.From("imaps://imap.gmail.com:993?folderName=INBOX&username=user&password=pass&idle=true")
```

Si IDLE échoue (serveur non supporté, erreur réseau), le consommateur bascule automatiquement vers le polling classique.

### SMTP Retry avec Exponential Backoff

L'envoi SMTP implémente un mécanisme de retry automatique:

- 5 tentatives maximum
- Délais: 1s, 2s, 4s, 8s (max)
- Les erreurs d'authentification ou d'adresse invalide ne sont pas retentées

### Parsing MIME avancé

Le composant supporte le parsing avancé des messages MIME:

- `multipart/alternative`: Extraction simultanée des versions text/plain et text/html
- `multipart/mixed`: Support des pièces jointes
- Encodings automatiques (base64, quoted-printable)

## Sécurité

Il est **fortement recommandé** de ne pas écrire les identifiants en clair
dans l'URI (les URI peuvent fuiter dans les logs d'erreur de connexion). Il
n'existe pas de syntaxe `${env:...}` : lisez la variable d'environnement en Go
et construisez l'URI avec :

```go
// ✅ Recommandé : le mot de passe vient de l'environnement
pw := os.Getenv("GMAIL_PASSWORD")
builder.To("smtps://smtp.gmail.com:465?username=user@gmail.com&password=" + url.QueryEscape(pw))

// ❌ Déconseillé (mot de passe visible)
builder.To("smtps://smtp.gmail.com:465?username=user@gmail.com&password=monmotdepasse")
```

Note : les composants qui passent par `gocamel.GetConfigValue` (par ex. FTP,
SFTP, Telegram, OpenAI) récupèrent aussi automatiquement une variable
d'environnement préfixée par le scheme (`FTP_PASSWORD`, ...) ou portant le nom
exact de la clé — voir la page Configuration de la documentation.

## Voir aussi

- [Documentation GoCamel](../..)
- [Exemples HTTP](../http-echo/)
- [Composant Timer](../timer/)
