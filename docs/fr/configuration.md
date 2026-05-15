# Configuration

## Précédence de résolution

`gocamel.GetConfigValue(url, key)` est le point d'entrée unique utilisé par
tous les composants pour résoudre une valeur de configuration. Il consulte
quatre sources dans l'ordre suivant — la première valeur non vide gagne :

1. **Variable d'environnement exacte.** La clé est utilisée telle quelle.
   Exemple : `GetConfigValue(_, "OPENAI_API_KEY")` lit
   `os.Getenv("OPENAI_API_KEY")`.
2. **Variable d'environnement préfixée par le scheme.** Construite en
   majuscules : `scheme + "_" + key`. Pour une URI `ftp://` et la clé
   `password`, la résolution interroge `os.Getenv("FTP_PASSWORD")`.
3. **Paramètre de query string.** Exemple : `ftp://host?password=secret`.
4. **Userinfo de l'URI**, uniquement pour les clés bien connues
   `username`/`user` et `password`/`pass`. Exemple :
   `ftp://user:pass@host`.

## Variables d'environnement

Configuration sensible via l'environnement système:

```bash
# OpenAI
export OPENAI_API_KEY="sk-..."

# Telegram
export TELEGRAM_AUTHORIZATIONTOKEN="..."

# FTP/SFTP
export FTP_USERNAME="user"
export FTP_PASSWORD="secret"
export SFTP_USERNAME="user"
export SFTP_PASSWORD="secret"

# SMB
export SMB_USERNAME="domain\\user"
export SMB_PASSWORD="secret"
```

!!! danger "Credentials dans les query strings : signalés au runtime"
    Les URI fuitent régulièrement dans les logs d'erreur de connexion, les
    pistes d'audit, les rapports de crash et les access logs de proxy.
    Quand `GetConfigValue` résout une clé reconnue comme credential
    (`password`, `pass`, `secret`, `token`, `apikey`, `api_key`, `accesskey`,
    `access_key`, `clientsecret`, `privatekey`) depuis un paramètre de
    query, un log `WARN` est émis avec une recommandation d'utiliser les
    variables d'environnement. Le warning n'inclut que le scheme et le nom
    de la clé — **jamais la valeur** — et il est dédupé par couple
    `(scheme, key)` pour qu'un consumer en polling ne sature pas les logs.

    Utilisez les variables d'environnement en production. La forme userinfo
    (`user:pass@host`) est considérée comme la manière conventionnelle de
    porter des credentials dans une URI et n'est **pas** signalée — vous y
    avez explicitement consenti.

## Configuration du contexte

```go
ctx := gocamel.NewCamelContext()

// Configuration custom
cfg := ctx.Config()
```

## Sécurité

**Ne jamais hardcoder les credentials:**

```go
// ❌ MAUVAIS
endpoint := "ftp://user:secret@host"

// ✅ BON
endpoint := "ftp://host?username=${env:FTP_USER}"
```

## Proxy

```go
os.Setenv("HTTP_PROXY", "http://proxy.company:8080")
os.Setenv("HTTPS_PROXY", "http://proxy.company:8080")
os.Setenv("NO_PROXY", "localhost,127.0.0.1")
```
