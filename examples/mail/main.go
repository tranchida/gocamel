package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/tranchida/gocamel"
)

// ExampleMailProducer montre comment envoyer un email via SMTP.
func ExampleMailProducer() {
	// Creer le contexte
	ctx := gocamel.NewCamelContext()

	// Creer et enregistrer le composant mail
	mailComponent := gocamel.NewMailComponent()
	mailComponent.SetDefaultFrom("sender@example.com")
	ctx.AddComponent("smtp", mailComponent)
	ctx.AddComponent("smtps", mailComponent)
	ctx.AddComponent("direct", gocamel.NewDirectComponent())
	// Creer une route qui envoie un email
	route := ctx.CreateRoute()
	route.From("direct:send-email").
		ProcessFunc(func(exchange *gocamel.Exchange) error {
			// L'email est configure via les headers ou les options de l'URI
			fmt.Println("Preparation de l'email...")
			return nil
		}).
		To("smtp://smtp.example.com:587?to=recipient@example.com\u0026subject=Hello%20World")

	// Demarrer le contexte
	if err := ctx.Start(); err != nil {
		log.Fatalf("Erreur demarrage: %v", err)
	}
	defer ctx.Stop()

	// Envoyer un message
	producerCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Simuler l'envoi d'un message
	exchange := gocamel.NewExchange(producerCtx)
	exchange.SetBody([]byte("Bonjour, ceci est un test GoCamel!"))
	exchange.SetHeader("Subject", "Test GoCamel Mail")
	exchange.SetHeader("To", "recipient@example.com")

	fmt.Println("Email pret a etre envoye!")
}

// ExampleMailWithAttachments montre comment envoyer un email avec pieces jointes.
func ExampleMailWithAttachments() {
	ctx := gocamel.NewCamelContext()

	mailComponent := gocamel.NewMailComponent()
	ctx.AddComponent("smtp", mailComponent)
	ctx.AddComponent("direct", gocamel.NewDirectComponent())
	route := ctx.CreateRoute()
	route.From("direct:send-with-attachments").
		To("smtp://smtp.gmail.com:587?username=user@gmail.com&password=mypassword&to=dest@example.com&subject=Rapport%20quotidien")

	if err := ctx.Start(); err != nil {
		log.Fatalf("Erreur demarrage: %v", err)
	}
	defer ctx.Stop()

	// Creer un echange avec pieces jointes
	exchange := gocamel.NewExchange(context.Background())
	exchange.SetBody([]byte("Veuillez trouver ci-joint le rapport."))

	// Ajouter une piece jointe via les headers
	exchange.SetHeader(gocamel.MailAttachmentPrefix+"_rapport.pdf", []byte("%PDF-1.4 contenu du PDF..."))
	exchange.SetHeader(gocamel.MailAttachmentPrefix+"_data.csv", "col1,col2\n1,2\n3,4")

	fmt.Println("Email avec pieces jointes pret!")
}

// ExampleSMTPS montre l'envoi securise via SMTPS (port 465).
func ExampleSMTPS() {
	// Pour Gmail, utilisez smtps://smtp.gmail.com:465
	uri := "smtps://smtp.gmail.com:465?username=user@gmail.com&password=apppassword&to=dest@example.com&subject=Test%20SMTPS"

	fmt.Printf("URI SMTPS: %s\n", uri)

	// La configuration TLS est automatique pour SMTPS
	// Pour SMTP avec STARTTLS (port 587), utilisez smtp:// avec les memes options
	// Le package net/smtp de Go gere automatiquement STARTTLS
}

// ExampleMailConsumer montre la configuration d'un consommateur IMAP.
func ExampleMailConsumer() {
	ctx := gocamel.NewCamelContext()

	mailComponent := gocamel.NewMailComponent()
	ctx.AddComponent("imap", mailComponent)
	ctx.AddComponent("imaps", mailComponent)

	// Creer une route qui lit les emails
	route := ctx.CreateRoute()
	route.From("imaps://imap.gmail.com:993?folderName=INBOX\u0026username=user@gmail.com\u0026password=apppassword\u0026unseen=true\u0026delete=false").
		ProcessFunc(func(exchange *gocamel.Exchange) error {
			// Traiter l'email recu
			subject, _ := exchange.GetIn().GetHeader("Subject")
			from, _ := exchange.GetIn().GetHeader("From")
			body := exchange.GetIn().GetBody()

			fmt.Printf("Email recu de: %v\n", from)
			fmt.Printf("Sujet: %v\n", subject)
			fmt.Printf("Corps: %v\n", body)

			return nil
		}).
		To("direct:processed")

	if err := ctx.Start(); err != nil {
		log.Fatalf("Erreur demarrage: %v", err)
	}
	defer ctx.Stop()

	fmt.Println("Consommateur mail demarre!")
	// Dans une implementation reelle, le consommateur s'executerait en continu
	time.Sleep(100 * time.Millisecond) // Juste pour la demo
}

// ExampleMailWithHTML montre l'envoi d'un email HTML.
func ExampleMailWithHTML() {
	uri := "smtp://smtp.example.com:587?to=dest@example.com&from=sender@example.com&contentType=text/html"

	fmt.Printf("URI pour HTML: %s\n", uri)

	htmlBody := `<html>
<body>
    <h1>Titre</h1>
    <p>Ceci est un <strong>email HTML</strong>.</p>
</body>
</html>`

	_ = htmlBody // Dans une vraie utilisation: exchange.SetBody(htmlBody)
	fmt.Println("Email HTML configure!")
}

func main() {
	fmt.Println("=== GoCamel Mail Component Examples ===")
	fmt.Println()

	fmt.Println("1. Exemple Producteur SMTP:")
	ExampleMailProducer()
	fmt.Println()

	fmt.Println("2. Exemple Email avec Pieces Jointes:")
	ExampleMailWithAttachments()
	fmt.Println()

	fmt.Println("3. Exemple SMTPS securise:")
	ExampleSMTPS()
	fmt.Println()

	fmt.Println("4. Exemple Consommateur IMAP:")
	ExampleMailConsumer()
	fmt.Println()

	fmt.Println("5. Exemple Email HTML:")
	ExampleMailWithHTML()
	fmt.Println()

	fmt.Println("=== Exemples termines ===")
}
