package main

import (
	"fmt"
	"time"

	"github.com/tranchida/gocamel"
)

func main() {

	context := gocamel.NewCamelContext()

	// The HTTP component is already registered by NewCamelContext
	// ("http" and "https" schemes), so no AddComponent call is needed.

	// Create a route that listens on port 8080 and echoes the received message.
	route, err := context.CreateRouteBuilder().
		From("http://localhost:8080/echo").
		ProcessFunc(func(exchange *gocamel.Exchange) error {
			// Add a response header.
			exchange.SetHeader("X-Processed-At", time.Now().Format(time.RFC3339))
			//exchange.GetOut().SetBody(exchange.GetIn().GetBody())
			return nil
		}).Build()
	if err != nil {
		fmt.Printf("Error creating route: %v\n", err)
		return
	}

	context.AddRoute(route)
	if err := context.Start(); err != nil {
		fmt.Printf("Error starting context: %v\n", err)
		return
	}

	fmt.Println("Server started on http://localhost:8080/echo")
	fmt.Println("Press Ctrl+C to stop")

	// Wait indefinitely.
	select {}
}
