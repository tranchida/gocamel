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
	// Create the templates directory if it does not exist.
	templateDir := "templates"
	if err := os.MkdirAll(templateDir, 0750); err != nil {
		log.Fatal(err)
	}

	// Create a basic email template.
	emailTemplate := `Hello {{.Headers.name}},

You have received the following message: {{.Body}}

Date: {{now | formatDate "2006-01-02 15:04:05"}}
Exchange ID: {{.Exchange.ID}}

--
This is an automated message from GoCamel
`
	if err := os.WriteFile(filepath.Join(templateDir, "email.tmpl"), []byte(emailTemplate), 0600); err != nil {
		log.Fatal(err)
	}

	// Create a template for a JSON report.
	jsonTemplate := `{
  "timestamp": "{{now | formatDate "2006-01-02T15:04:05Z07:00"}}",
  "content": {{.Body | toString | safeHTML}},
  "user": "{{.Headers.user}}",
  "status": {{if .Headers.status}}"{{.Headers.status | upper}}"{{else}}"UNKNOWN"{{end}}
}`
	if err := os.WriteFile(filepath.Join(templateDir, "report.json.tmpl"), []byte(jsonTemplate), 0600); err != nil {
		log.Fatal(err)
	}

	// Create the Camel context.
	camelCtx := gocamel.NewCamelContext()
	defer camelCtx.Stop()

	// Register template component
	camelCtx.AddComponent("template", gocamel.NewTemplateComponent())

	// Start the context.
	if err := camelCtx.Start(); err != nil {
		log.Fatal(err)
	}

	// Test routes
	fmt.Println("GoCamel Template Component Demonstration")
	fmt.Println("==========================================")
	fmt.Println()

	// Test 1: Simple email
	fmt.Println("1. Simple email generation test")
	emailEndpoint, err := camelCtx.CreateEndpoint("template:templates/email.tmpl")
	if err != nil {
		log.Fatalf("Error creating email endpoint: %v", err)
	}
	emailProducer, err := emailEndpoint.CreateProducer()
	if err != nil {
		log.Fatalf("Error creating email producer: %v", err)
	}
	err = emailProducer.Start(context.TODO())
	if err != nil {
		log.Fatalf("Error starting email producer: %v", err)
	}
	emailExchange := gocamel.NewExchange(context.Background())
	emailExchange.SetHeader("name", "Ali")
	emailExchange.SetBody("Your order has been shipped.")
	err = emailProducer.Send(emailExchange)
	if err != nil {
		log.Fatalf("Error sending email: %v", err)
	}
	fmt.Println("=== Generated Email ===")
	fmt.Println(emailExchange.GetBody())

	// Test 2: JSON Report
	fmt.Println("\n2. JSON report generation test")
	jsonEndpoint, err := camelCtx.CreateEndpoint("template:templates/report.json.tmpl")
	if err != nil {
		log.Fatalf("Error creating JSON endpoint: %v", err)
	}
	jsonProducer, err := jsonEndpoint.CreateProducer()
	if err != nil {
		log.Fatalf("Error creating JSON producer: %v", err)
	}
	err = jsonProducer.Start(context.TODO())
	if err != nil {
		log.Fatalf("Error starting JSON producer: %v", err)
	}
	jsonExchange := gocamel.NewExchange(context.Background())
	jsonExchange.SetHeader("user", "Giampaolo")
	jsonExchange.SetHeader("status", "success")
	jsonExchange.SetBody("Operation completed successfully")
	err = jsonProducer.Send(jsonExchange)
	if err != nil {
		log.Fatalf("Error sending JSON: %v", err)
	}
	fmt.Println("=== Generated JSON Report ===")
	fmt.Println(jsonExchange.GetBody())

	// Test 3: With cache
	fmt.Println("\n3. Template cache test")
	cachedEndpoint, err := camelCtx.CreateEndpoint("template:templates/email.tmpl?contentCache=true")
	if err != nil {
		log.Fatalf("Error creating cached endpoint: %v", err)
	}
	cachedProducer, err := cachedEndpoint.CreateProducer()
	if err != nil {
		log.Fatalf("Error creating cached producer: %v", err)
	}
	err = cachedProducer.Start(context.TODO())
	if err != nil {
		log.Fatalf("Error starting cached producer: %v", err)
	}
	cachedExchange := gocamel.NewExchange(context.Background())
	cachedExchange.SetHeader("name", "Rachel")
	cachedExchange.SetBody("Welcome to the GoCamel system.")
	err = cachedProducer.Send(cachedExchange)
	if err != nil {
		log.Fatalf("Error sending cached: %v", err)
	}
	fmt.Println("=== Generated Email with Cache ===")
	fmt.Println(cachedExchange.GetBody())

	fmt.Println("\n==========================================")
	fmt.Println("Demonstration finished!")
	fmt.Println("Templates used are in the 'templates/' folder")
}
