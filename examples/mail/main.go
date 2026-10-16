package main

import (
	"context"
	"fmt"
	mail "github.com/tranchida/gocamel/components/mail"
	"log"
	"time"

	"github.com/tranchida/gocamel"
)

// ExampleMailProducer shows how to send an email via SMTP.
func ExampleMailProducer() {
	// Create context
	ctx := gocamel.NewCamelContext()

	// Create and register mail component
	mailComponent := mail.NewMailComponent()
	mailComponent.SetDefaultFrom("sender@example.com")
	ctx.AddComponent("smtp", mailComponent)
	ctx.AddComponent("smtps", mailComponent)
	ctx.AddComponent("direct", gocamel.NewDirectComponent())

	// Create a route that sends an email
	_, err := ctx.CreateRouteBuilder().
		From("direct:send-email").
		ProcessFunc(func(exchange *gocamel.Exchange) error {
			// Email is configured via headers or URI options
			fmt.Println("Preparing email...")
			return nil
		}).
		To("smtp://smtp.example.com:587?to=recipient@example.com&subject=Hello%20World").
		Build()
	if err != nil {
		log.Fatalf("Route build error: %v", err)
	}

	// Start the context
	if err := ctx.Start(); err != nil {
		log.Fatalf("Startup error: %v", err)
	}
	defer ctx.Stop()

	// Send a message
	producerCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Simulate sending a message
	exchange := gocamel.NewExchange(producerCtx)
	exchange.SetBody([]byte("Hello, this is a GoCamel test!"))
	exchange.SetHeader("Subject", "GoCamel Mail Test")
	exchange.SetHeader("To", "recipient@example.com")

	fmt.Println("Email ready to be sent!")
}

// ExampleMailWithAttachments shows how to send an email with attachments.
func ExampleMailWithAttachments() {
	ctx := gocamel.NewCamelContext()

	mailComponent := mail.NewMailComponent()
	ctx.AddComponent("smtp", mailComponent)
	ctx.AddComponent("direct", gocamel.NewDirectComponent())
	_, err := ctx.CreateRouteBuilder().
		From("direct:send-with-attachments").
		To("smtp://smtp.gmail.com:587?username=user@gmail.com&password=mypassword&to=dest@example.com&subject=Daily%20Report").
		Build()
	if err != nil {
		log.Fatalf("Route build error: %v", err)
	}

	if err := ctx.Start(); err != nil {
		log.Fatalf("Startup error: %v", err)
	}
	defer ctx.Stop()

	// Create an exchange with attachments
	exchange := gocamel.NewExchange(context.Background())
	exchange.SetBody([]byte("Please find the report attached."))

	// Add an attachment via headers
	exchange.SetHeader(mail.MailAttachmentPrefix+"_report.pdf", []byte("%PDF-1.4 PDF content..."))
	exchange.SetHeader(mail.MailAttachmentPrefix+"_data.csv", "col1,col2\n1,2\n3,4")

	fmt.Println("Email with attachments ready!")
}

// ExampleSMTPS shows secure sending via SMTPS (port 465).
func ExampleSMTPS() {
	// For Gmail, use smtps://smtp.gmail.com:465
	uri := "smtps://smtp.gmail.com:465?username=user@gmail.com&password=apppassword&to=dest@example.com&subject=SMTPS%20Test"

	fmt.Printf("SMTPS URI: %s\n", uri)

	// TLS configuration is automatic for SMTPS
	// For SMTP with STARTTLS (port 587), use smtp:// with the same options
	// Go's net/smtp package automatically handles STARTTLS
}

// ExampleMailConsumer shows the configuration of an IMAP consumer.
func ExampleMailConsumer() {
	ctx := gocamel.NewCamelContext()

	mailComponent := mail.NewMailComponent()
	ctx.AddComponent("imap", mailComponent)
	ctx.AddComponent("imaps", mailComponent)

	// Create a route that reads emails
	_, err := ctx.CreateRouteBuilder().
		From("imaps://imap.gmail.com:993?folderName=INBOX&username=user@gmail.com&password=apppassword&unseen=true&delete=false").
		ProcessFunc(func(exchange *gocamel.Exchange) error {
			// Process received email
			subject, _ := exchange.GetIn().GetHeader("Subject")
			from, _ := exchange.GetIn().GetHeader("From")
			body := exchange.GetIn().GetBody()

			fmt.Printf("Email received from: %v\n", from)
			fmt.Printf("Subject: %v\n", subject)
			fmt.Printf("Body: %v\n", body)

			return nil
		}).
		To("direct:processed").
		Build()
	if err != nil {
		log.Fatalf("Route build error: %v", err)
	}

	if err := ctx.Start(); err != nil {
		log.Fatalf("Startup error: %v", err)
	}
	defer ctx.Stop()

	fmt.Println("Mail consumer started!")
	// In a real implementation, the consumer would run continuously
	time.Sleep(100 * time.Millisecond) // Just for demo
}

// ExampleMailWithHTML shows sending an HTML email.
func ExampleMailWithHTML() {
	uri := "smtp://smtp.example.com:587?to=dest@example.com&from=sender@example.com&contentType=text/html"

	fmt.Printf("URI for HTML: %s\n", uri)

	htmlBody := `<html>
<body>
    <h1>Title</h1>
    <p>This is an <strong>HTML email</strong>.</p>
</body>
</html>`

	_ = htmlBody // In real usage: exchange.SetBody(htmlBody)
	fmt.Println("HTML email configured!")
}

func main() {
	fmt.Println("=== GoCamel Mail Component Examples ===")
	fmt.Println()

	fmt.Println("1. SMTP Producer Example:")
	ExampleMailProducer()
	fmt.Println()

	fmt.Println("2. Email with Attachments Example:")
	ExampleMailWithAttachments()
	fmt.Println()

	fmt.Println("3. Secure SMTPS Example:")
	ExampleSMTPS()
	fmt.Println()

	fmt.Println("4. IMAP Consumer Example:")
	ExampleMailConsumer()
	fmt.Println()

	fmt.Println("5. HTML Email Example:")
	ExampleMailWithHTML()
	fmt.Println()

	fmt.Println("=== Examples finished ===")
}
