package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/tranchida/gocamel"
)

func main() {
	// Créer le répertoire de templates s'il n'existe pas
	templateDir := "templates"
	if err := os.MkdirAll(templateDir, 0755); err != nil {
		log.Fatal(err)
	}

	// Créer un template de base pour un email
	emailTemplate := `Bonjour {{.Headers.name}},

Vous avez reçu le message suivant : {{.Body}}

Date: {{now | formatDate "2006-01-02 15:04:05"}}
ID échange: {{.Exchange.ID}}

--
Ceci est un message automatique de GoCamel
`
	if err := os.WriteFile(filepath.Join(templateDir, "email.tmpl"), []byte(emailTemplate), 0644); err != nil {
		log.Fatal(err)
	}

	// Créer un template pour un rapport JSON
	jsonTemplate := `{
  "timestamp": "{{now | formatDate "2006-01-02T15:04:05Z07:00"}}",
  "content": {{.Body | toString | safeHTML}},
  "user": "{{.Headers.user}}",
  "status": {{if .Headers.status}}"{{.Headers.status | upper}}"{{else}}"UNKNOWN"{{end}}
}`
	if err := os.WriteFile(filepath.Join(templateDir, "report.json.tmpl"), []byte(jsonTemplate), 0644); err != nil {
		log.Fatal(err)
	}

	// Créer le contexte Camel
	camelCtx := gocamel.NewCamelContext()
	defer camelCtx.Stop()

	// Enregistrer le composant template
	camelCtx.AddComponent("template", gocamel.NewTemplateComponent())

	// Démarrer le contexte
	if err := camelCtx.Start(); err != nil {
		log.Fatal(err)
	}

	// Tester les routes
	fmt.Println("Démonstration du composant Template GoCamel")
	fmt.Println("==========================================")
	fmt.Println()

	// Test 1: Email simple
	fmt.Println("1. Test de génération d'email simple")
	emailEndpoint, err := camelCtx.CreateEndpoint("template:templates/email.tmpl")
	if err != nil {
		log.Fatalf("Erreur création endpoint email: %v", err)
	}
	emailProducer, err := emailEndpoint.CreateProducer()
	if err != nil {
		log.Fatalf("Erreur création producer email: %v", err)
	}
	err = emailProducer.Start(nil)
	if err != nil {
		log.Fatalf("Erreur start producer email: %v", err)
	}
	emailExchange := gocamel.NewExchange(context.Background())
	emailExchange.SetHeader("name", "Ali")
	emailExchange.SetBody("Votre commande a été expédiée.")
	err = emailProducer.Send(emailExchange)
	if err != nil {
		log.Fatalf("Erreur Send email: %v", err)
	}
	fmt.Println("=== Email généré ===")
	fmt.Println(emailExchange.GetBody())

	// Test 2: Rapport JSON
	fmt.Println("\n2. Test de génération de rapport JSON")
	jsonEndpoint, err := camelCtx.CreateEndpoint("template:templates/report.json.tmpl")
	if err != nil {
		log.Fatalf("Erreur création endpoint JSON: %v", err)
	}
	jsonProducer, err := jsonEndpoint.CreateProducer()
	if err != nil {
		log.Fatalf("Erreur création producer JSON: %v", err)
	}
	err = jsonProducer.Start(nil)
	if err != nil {
		log.Fatalf("Erreur start producer JSON: %v", err)
	}
	jsonExchange := gocamel.NewExchange(context.Background())
	jsonExchange.SetHeader("user", "Giampaolo")
	jsonExchange.SetHeader("status", "success")
	jsonExchange.SetBody("Opération terminée avec succès")
	err = jsonProducer.Send(jsonExchange)
	if err != nil {
		log.Fatalf("Erreur Send JSON: %v", err)
	}
	fmt.Println("=== Rapport JSON généré ===")
	fmt.Println(jsonExchange.GetBody())

	// Test 3: Avec cache
	fmt.Println("\n3. Test avec cache de template")
	cachedEndpoint, err := camelCtx.CreateEndpoint("template:templates/email.tmpl?contentCache=true")
	if err != nil {
		log.Fatalf("Erreur création endpoint cached: %v", err)
	}
	cachedProducer, err := cachedEndpoint.CreateProducer()
	if err != nil {
		log.Fatalf("Erreur création producer cached: %v", err)
	}
	err = cachedProducer.Start(nil)
	if err != nil {
		log.Fatalf("Erreur start producer cached: %v", err)
	}
	cachedExchange := gocamel.NewExchange(context.Background())
	cachedExchange.SetHeader("name", "Rachel")
	cachedExchange.SetBody("Bienvenue dans le système GoCamel.")
	err = cachedProducer.Send(cachedExchange)
	if err != nil {
		log.Fatalf("Erreur Send cached: %v", err)
	}
	fmt.Println("=== Email avec cache généré ===")
	fmt.Println(cachedExchange.GetBody())

	fmt.Println("\n==========================================")
	fmt.Println("Démonstration terminée!")
	fmt.Println("Les templates utilisés sont dans le dossier 'templates/'")
}
