// Example: Processor implementation and usage based on Apache Camel documentation
//
// This example demonstrates how to:
// 1. Implement the Processor interface with a struct
// 2. Use typed accessors (GetBodyAsString, GetHeaderAsInt, etc.)
// 3. Register a processor in the Registry
// 4. Use ProcessRef to refer to a registered processor
// 5. Use ProcessFunc for quick closures
//
// Based on: https://camel.apache.org/manual/processor.html
package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/tranchida/gocamel"
)

// MyCustomProcessor is a structured processor, similar to implementing Processor in Java
type MyCustomProcessor struct {
	Prefix string
}

// Process implements the gocamel.Processor interface
func (p *MyCustomProcessor) Process(exchange *gocamel.Exchange) error {
	// 1. Accessing the In message body with typed helper
	body, ok := exchange.GetBodyAsString()
	if !ok {
		return fmt.Errorf("body is not a string")
	}

	// 2. Accessing headers with typed helper
	requestID, _ := exchange.GetHeaderAsString("X-Request-ID")
	
	log.Printf("[MyCustomProcessor] Processing message %s: %s", requestID, body)

	// 3. Modifying the message (Message Translator pattern)
	newBody := fmt.Sprintf("%s: %s", p.Prefix, strings.ToUpper(body))
	exchange.GetIn().SetBody(newBody)

	// 4. Setting headers
	exchange.GetIn().SetHeader("X-Processed-By", "MyCustomProcessor")
	
	return nil
}

// OrderValidationProcessor validates an order
type OrderValidationProcessor struct{}

func (p *OrderValidationProcessor) Process(exchange *gocamel.Exchange) error {
	// Accessing numeric headers
	amount, ok := exchange.GetHeaderAsInt("Amount")
	if !ok || amount <= 0 {
		return fmt.Errorf("invalid amount: %d", amount)
	}

	if amount > 1000 {
		exchange.GetIn().SetHeader("OrderType", "Premium")
	} else {
		exchange.GetIn().SetHeader("OrderType", "Standard")
	}

	return nil
}

func main() {
	fmt.Println("=== GoCamel Processor Example (Apache Camel Style) ===")
	fmt.Println()

	// Create the Camel context
	ctx := gocamel.NewCamelContext()

	// Register components needed for the example
	ctx.AddComponent("direct", gocamel.NewDirectComponent())

	// --- 1. Registering processors in the Registry ---
	// This allows using them by name via ProcessRef
	ctx.GetComponentRegistry().Bind("validator", &OrderValidationProcessor{})
	ctx.GetComponentRegistry().Bind("translator", &MyCustomProcessor{Prefix: "PROCESSED"})

	// --- 2. Building a route using different processor types ---
	builder := ctx.CreateRouteBuilder()
	builder.From("direct:start").
		// Use a closure for simple logic
		ProcessFunc(func(exchange *gocamel.Exchange) error {
			log.Println("Received new exchange")
			return nil
		}).
		// Use a registered processor by name
		ProcessRef("validator").
		// Use a struct instance directly
		Process(&MyCustomProcessor{Prefix: "FINAL"}).
		To("direct:end")

	route := builder.Build()
	ctx.AddRoute(route)

	// Add a consumer for direct:end to complete the flow
	endBuilder := ctx.CreateRouteBuilder()
	endBuilder.From("direct:end").
		LogSimple("Final result received: ${body} (Type: ${header.OrderType})")
	
	ctx.AddRoute(endBuilder.Build())

	// Start the context
	if err := ctx.Start(); err != nil {
		log.Fatalf("Failed to start context: %v", err)
	}
	defer ctx.Stop()

	// --- 3. Testing the route ---
	testCase := func(id string, amount int, body string) {
		fmt.Printf("\n--- Testing Order %s (Amount: %d) ---\n", id, amount)
		exchange := gocamel.NewExchange(ctx.GetContext())
		exchange.GetIn().SetHeader("X-Request-ID", id)
		exchange.GetIn().SetHeader("Amount", amount)
		exchange.GetIn().SetBody(body)

		err := route.Process(exchange)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			return
		}

		fmt.Printf("Result body: %v\n", exchange.GetIn().GetBody())
		fmt.Printf("Order Type:  %v\n", exchange.GetIn().GetHeaders()["OrderType"])
		fmt.Printf("Processed By: %v\n", exchange.GetIn().GetHeaders()["X-Processed-By"])
	}

	testCase("ORD-001", 500, "laptop")
	testCase("ORD-002", 1500, "workstation")
	testCase("ORD-003", -1, "invalid")

	fmt.Println("\n=== Example complete ===")
}
