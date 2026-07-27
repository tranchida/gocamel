package main

import (
	"fmt"
	"time"

	"gitlab.com/tranchida/gocamel"
)

func main() {
	context := gocamel.NewCamelContext()

	// Register the HTTP component.
	context.AddComponent("http", gocamel.NewHTTPComponent())

	// Create a route that listens on port 8080 and echoes the received message.
	route, err := context.CreateRouteBuilder().
		From("http://localhost:8080/echo").
		SetID("myroute").
		LogBody("Received message").
		LogHeaders("Received headers").
		SetBody("Hello, World!").
		SetHeader("Content-Type", "text/plain").
		// CamelHttpResponseCode drives the HTTP status of the response.
		// The legacy "Status-Code" header is still honoured.
		SetHeader(gocamel.CamelHttpResponseCode, 200).
		SetHeader("X-Processed-At", time.Now().Format(time.RFC3339)).
		Build()
	if err != nil {
		fmt.Printf("Error creating route: %v\n", err)
		return
	}

	context.AddRoute(route)
	context.Start()

	mgmt := gocamel.NewManagementServer(context)
	mgmt.Start(":8081")

	fmt.Println("Server started on http://localhost:8080/echo")
	fmt.Println("Press Ctrl+C to stop")

	// Wait indefinitely.
	select {}
}
