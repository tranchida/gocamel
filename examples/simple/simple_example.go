// Example: Simple Language expression parser demonstration
//
// This example demonstrates the Simple Language expression parser
// which allows you to use expressions like ${body}, ${header.name},
// ${exchangeProperty.name}, and functions like ${date:now}, ${random(100)}, ${uuid}
//
// NEW FEATURES:
// - Bracket notation: ${body['key']}, ${body[0]}, ${header['name']}
// - Null-safe operator: ${body?.field}, ${header?.X-Header}
// - Choice routing: .Choice().When(expr).Process().Otherwise().Process().EndChoice()
//
// To run: go run simple_example.go
package main

import (
	"fmt"
	"log"
	"time"

	"github.com/tranchida/gocamel"
)

func main() {
	fmt.Println("=== GoCamel Simple Language Example ===")
	fmt.Println("========================================")
	fmt.Println()

	// Create the Camel context
	ctx := gocamel.NewCamelContext()

	// Create a test exchange
	exchange := gocamel.NewExchange(ctx.GetContext())
	exchange.GetIn().SetBody("Hello from GoCamel!")
	exchange.GetIn().SetHeader("Content-Type", "text/plain")
	exchange.GetIn().SetHeader("X-Request-ID", "req-12345")
	exchange.SetProperty("processedAt", time.Now().Format(time.RFC3339))
	exchange.SetProperty("user", "john.doe")

	// Demo 1: Basic variable access
	fmt.Println("Demo 1: Variable Access")
	fmt.Println("-----------------------")
	demoExpressions(ctx, exchange, []string{
		"Simple body: ${body}",
		"Content-Type header: ${header.Content-Type}",
		"Request ID: ${header.X-Request-ID}",
		"User property: ${exchangeProperty.user}",
		"Processing time: ${exchangeProperty.processedAt}",
	})
	fmt.Println()

	// Demo 2: Template composition
	fmt.Println("Demo 2: Template Composition")
	fmt.Println("----------------------------")
	demoExpressions(ctx, exchange, []string{
		"Received body: [${body}] from user: ${exchangeProperty.user}",
		"Response type is ${header.Content-Type} with ID ${header.X-Request-ID}",
		"Request at ${exchangeProperty.processedAt}",
	})
	fmt.Println()

	// Demo 3: Date and Random Functions
	fmt.Println("Demo 3: Functions")
	fmt.Println("-----------------")
	demoExpressions(ctx, exchange, []string{
		"Current time (RFC3339): ${date:now}",
		"Current date: ${date:now:2006-01-02}",
		"Random number (0-99): ${random(100)}",
		"Generated UUID: ${uuid}",
		"Custom format: ${date:now:January 2, 2006 at 3:04 PM}",
	})
	fmt.Println()

	// Demo 4: Comparisons
	fmt.Println("Demo 4: Comparisons")
	fmt.Println("-------------------")
	exchange.GetIn().SetHeader("count", 10)
	exchange.SetProperty("status", "active")
	demoExpressionsAsBool(exchange, []struct {
		expr string
		desc string
	}{
		{"${body == 'Hello from GoCamel!'}", "body equals 'Hello from GoCamel!'"},
		{"${body != 'Goodbye'}", "body not equals 'Goodbye'"},
		{"${header.count > 5}", "count greater than 5"},
		{"${header.count <= 15}", "count less than or equal 15"},
		{"${header.count == 10}", "count equals 10"},
		{"${exchangeProperty.status == 'active'}", "status is active"},
	})
	fmt.Println()

	// Demo 5: Map Access with Bracket Notation
	fmt.Println("Demo 5: Map Access with Bracket Notation")
	fmt.Println("----------------------------------------")
	exchange.GetIn().SetBody(map[string]interface{}{
		"name":    "John Doe",
		"email":   "john@example.com",
		"address": map[string]interface{}{
			"street":  "123 Main St",
			"city":    "New York",
			"country": "USA",
		},
		"hobbies": []interface{}{"reading", "coding", "gaming"},
		"key with spaces": "value with spaces",
	})
	demoMapAccess(ctx, exchange)
	fmt.Println()

	// Demo 6: List/Array Access with Bracket Notation
	fmt.Println("Demo 6: List/Array Access with Bracket Notation")
	fmt.Println("-----------------------------------------------")
	exchange.GetIn().SetBody([]interface{}{
		"first item",
		"second item",
		"third item",
		map[string]interface{}{
			"name":  "Nested Map",
			"value": 42,
		},
	})
	demoListAccess(ctx, exchange)
	fmt.Println()

	// Demo 7: Nested Access (Mixed Notation)
	fmt.Println("Demo 7: Nested Access (Map[List[Map]])")
	fmt.Println("--------------------------------------")
	exchange.GetIn().SetBody(map[string]interface{}{
		"users": []interface{}{
			map[string]interface{}{
				"name":  "Alice",
				"email": "alice@example.com",
				"roles": []interface{}{"admin", "user"},
			},
			map[string]interface{}{
				"name":  "Bob",
				"email": "bob@example.com",
				"roles": []interface{}{"user"},
			},
		},
	})
	demoNestedAccess(ctx, exchange)
	fmt.Println()

	// Demo 8: Null-Safe Operator
	fmt.Println("Demo 8: Null-Safe Operator (?.)")
	fmt.Println("--------------------------------")
	demoNullSafeOperator(ctx, exchange)
	fmt.Println()

	// Demo 9: Route Builder with Choice Pattern
	fmt.Println("Demo 9: Route Builder with Choice/When/Otherwise")
	fmt.Println("--------------------------------------------------")
	demoChoiceRouting(ctx)
	fmt.Println()

	// Demo 10: Route Builder with Simple Language Integration
	fmt.Println("Demo 10: Route Builder Integration")
	fmt.Println("-----------------------------------")
	demoRouteBuilder(ctx)
}

func demoExpressions(ctx *gocamel.CamelContext, exchange *gocamel.Exchange, expressions []string) {
	for _, expr := range expressions {
		template, err := gocamel.ParseSimpleTemplate(expr)
		if err != nil {
			log.Printf("Error parsing '%s': %v", expr, err)
			continue
		}

		result, err := template.EvaluateAsString(exchange)
		if err != nil {
			log.Printf("Error evaluating '%s': %v", expr, err)
			continue
		}

		fmt.Printf("  Expression: %s\n", expr)
		fmt.Printf("  Result:     %s\n\n", result)
	}
}

func demoExpressionsAsBool(exchange *gocamel.Exchange, expressions []struct {
	expr string
	desc string
}) {
	for _, item := range expressions {
		template, err := gocamel.ParseSimpleTemplate(item.expr)
		if err != nil {
			log.Printf("Error parsing '%s': %v", item.expr, err)
			continue
		}

		result, err := template.Evaluate(exchange)
		if err != nil {
			log.Printf("Error evaluating '%s': %v", item.expr, err)
			continue
		}

		fmt.Printf("  Condition: %s\n", item.desc)
		fmt.Printf("  Expression: %s\n", item.expr)
		fmt.Printf("  Result:     %v\n\n", result)
	}
}

func demoMapAccess(ctx *gocamel.CamelContext, exchange *gocamel.Exchange) {
	expressions := []string{
		"Name: ${body['name']}",
		"Email: ${body[\"email\"]}",
		"Address city: ${body['address']['city']}",
		"Address country: ${body[\"address\"][\"country\"]}",
		"Key with spaces: ${body['key with spaces']}",
		"Missing key: ${body['nonexistent']}",
	}

	for _, expr := range expressions {
		template, err := gocamel.ParseSimpleTemplate(expr)
		if err != nil {
			log.Printf("Error parsing '%s': %v", expr, err)
			continue
		}

		result, err := template.EvaluateAsString(exchange)
		if err != nil {
			log.Printf("Error evaluating '%s': %v", expr, err)
			continue
		}

		fmt.Printf("  %s\n", expr)
		fmt.Printf("  → %s\n\n", result)
	}
}

func demoListAccess(ctx *gocamel.CamelContext, exchange *gocamel.Exchange) {
	expressions := []string{
		"First element: ${body[0]}",
		"Second element: ${body[1]}",
		"Last element (map): ${body[3]}",
		"Nested map value: ${body[3]['name']}",
		"Out of bounds: ${body[10]}",
	}

	for _, expr := range expressions {
		template, err := gocamel.ParseSimpleTemplate(expr)
		if err != nil {
			log.Printf("Error parsing '%s': %v", expr, err)
			continue
		}

		result, err := template.EvaluateAsString(exchange)
		if err != nil {
			log.Printf("Error evaluating '%s': %v", expr, err)
			continue
		}

		fmt.Printf("  %s\n", expr)
		fmt.Printf("  → %s\n\n", result)
	}

	// Demo 'last' index
	lastExpr := "Last index: ${body[last]}"
	template, _ := gocamel.ParseSimpleTemplate(lastExpr)
	result, _ := template.EvaluateAsString(exchange)
	fmt.Printf("  %s\n", lastExpr)
	fmt.Printf("  → %s\n\n", result)

	lastMinusOne := "Last minus 1: ${body[last-1]}"
	template, _ = gocamel.ParseSimpleTemplate(lastMinusOne)
	result, _ = template.EvaluateAsString(exchange)
	fmt.Printf("  %s\n", lastMinusOne)
	fmt.Printf("  → %s\n\n", result)
}

func demoNestedAccess(ctx *gocamel.CamelContext, exchange *gocamel.Exchange) {
	expressions := []string{
		"First user name: ${body['users'][0]['name']}",
		"First user email: ${body['users'][0]['email']}",
		"First user's first role: ${body['users'][0]['roles'][0]}",
		"Second user name: ${body['users'][1]['name']}",
		"Second user's last role: ${body['users'][1]['roles'][0]}",
	}

	for _, expr := range expressions {
		template, err := gocamel.ParseSimpleTemplate(expr)
		if err != nil {
			log.Printf("Error parsing '%s': %v", expr, err)
			continue
		}

		result, err := template.EvaluateAsString(exchange)
		if err != nil {
			log.Printf("Error evaluating '%s': %v", expr, err)
			continue
		}

		fmt.Printf("  %s\n", expr)
		fmt.Printf("  → %s\n\n", result)
	}
}

func demoNullSafeOperator(ctx *gocamel.CamelContext, exchange *gocamel.Exchange) {
	// First, test with a map body that has nested structure
	exchange.GetIn().SetBody(map[string]interface{}{
		"user": map[string]interface{}{
			"profile": map[string]interface{}{
				"name": "John",
			},
		},
	})

	expressions := []string{
		"Safe access: ${body?.user?.profile?.name}",
		"Mixed access: ${body?.user?.profile}",
	}

	fmt.Println("  With non-nil body:")
	for _, expr := range expressions {
		template, err := gocamel.ParseSimpleTemplate(expr)
		if err != nil {
			log.Printf("Error parsing '%s': %v", expr, err)
			continue
		}

		result, err := template.EvaluateAsString(exchange)
		if err != nil {
			log.Printf("Error evaluating '%s': %v", expr, err)
			continue
		}

		fmt.Printf("    %s\n", expr)
		fmt.Printf("    → %s\n\n", result)
	}

	// Test with nil body
	exchange.GetIn().SetBody(nil)

	fmt.Println("  With nil body:")
	safeExpr := "Safe on nil: ${body?.field}"
	template, _ := gocamel.ParseSimpleTemplate(safeExpr)
	result, _ := template.EvaluateAsString(exchange)
	fmt.Printf("    %s\n", safeExpr)
	fmt.Printf("    → %s (no panic!)\n\n", result)

	// Test with header using null-safe
	exchange.GetIn().SetHeader("X-Exists", "present-value")

	safeHeader := "Safe header access: ${header?.X-Exists}"
	template, _ = gocamel.ParseSimpleTemplate(safeHeader)
	result, _ = template.EvaluateAsString(exchange)
	fmt.Printf("  %s\n", safeHeader)
	fmt.Printf("    → %s\n\n", result)
}

func demoChoiceRouting(ctx *gocamel.CamelContext) {
	// Create a route with Choice pattern
	builder := ctx.CreateRouteBuilder()
	builder.
		SetID("choice-demo-route").
		Choice().
		When("${header.priority == 'high'}").
		SimpleSetBody("HIGH PRIORITY: ${body}").
		SetHeader("X-Priority-Flag", "high").
		When("${header.priority == 'medium'}").
		SimpleSetBody("Medium Priority: ${body}").
		SetHeader("X-Priority-Flag", "medium").
		When("${header.priority == 'low'}").
		SimpleSetBody("(low) ${body}").
		SetHeader("X-Priority-Flag", "low").
		Otherwise().
		SimpleSetBody("Unknown Priority: ${body}").
		SetHeader("X-Priority-Flag", "unknown").
		EndChoice()

	route := builder.Build()

	// Test different priorities
	testCases := []struct {
		priority string
		body     string
	}{
		{"high", "Critical task"},
		{"medium", "Normal task"},
		{"low", "Background task"},
		{"invalid", "Unknown task"},
	}

	for _, tc := range testCases {
		exchange := gocamel.NewExchange(ctx.GetContext())
		exchange.GetIn().SetHeader("priority", tc.priority)
		exchange.GetIn().SetBody(tc.body)

		err := route.Process(exchange)
		if err != nil {
			log.Printf("Error processing: %v", err)
			continue
		}

		fmt.Printf("  Input: priority=%s, body=%s\n", tc.priority, tc.body)
		fmt.Printf("  Output: %s\n", exchange.GetOut().GetBody())
		if flag, exists := exchange.GetOut().GetHeader("X-Priority-Flag"); exists {
			fmt.Printf("  Header X-Priority-Flag: %s\n", flag)
		}
		fmt.Println()
	}

	// Another example: Content-based routing
	fmt.Println("  --- Content-Based Routing Example ---")

	builder2 := ctx.CreateRouteBuilder()
	builder2.
		Choice().
		When("${header.Content-Type == 'application/json'}").
		SimpleSetBody("Processing JSON: ${body}").
		When("${header.Content-Type == 'application/xml'}").
		SimpleSetBody("Processing XML: ${body}").
		When("${header.Content-Type == 'text/plain'}").
		SimpleSetBody("Processing Plain Text: ${body}").
		Otherwise().
		SimpleSetBody("Unknown Content-Type: ${body}").
		EndChoice()

	route2 := builder2.Build()

	contentTypes := []string{
		"application/json",
		"application/xml",
		"text/plain",
		"image/png",
	}

	for _, ct := range contentTypes {
		exchange := gocamel.NewExchange(ctx.GetContext())
		exchange.GetIn().SetHeader("Content-Type", ct)
		exchange.GetIn().SetBody("Sample data")

		err := route2.Process(exchange)
		if err != nil {
			log.Printf("Error: %v", err)
			continue
		}

		fmt.Printf("    Content-Type: %s → %s\n", ct, exchange.GetOut().GetBody())
	}
	fmt.Println()
}

func demoRouteBuilder(ctx *gocamel.CamelContext) {
	// Create a route that processes a message
	exchange := gocamel.NewExchange(ctx.GetContext())
	exchange.GetIn().SetBody("Original Message")
	exchange.GetIn().SetHeader("X-Input", "input-value")
	exchange.SetProperty("sessionId", "abc123")

	fmt.Println("Creating route with SimpleSetBody and SimpleSetHeader...")
	fmt.Println()

	// Build a route using the SimpleSetBody and SimpleSetHeader methods
	// Note: The RouteBuilder still requires a valid endpoint to be set
	// so we'll use the route's AddProcessor directly for this demo
	route := gocamel.NewRoute()
	route.SetID("simple-demo-route")
	route.AddProcessor(&gocamel.SimpleLanguageProcessor{
		Template: mustParse("Processed: ${body}"),
	})
	route.AddProcessor(&gocamel.SimpleSetHeaderProcessor{
		HeaderName: "X-Processed-By",
		Expression: gocamel.ExpressionFunc(func(ex *gocamel.Exchange) (interface{}, error) {
			return "GoCamel Processor", nil
		}),
	})
	route.AddProcessor(&gocamel.SimpleSetHeaderProcessor{
		HeaderName: "X-Session",
		Expression: mustParse("${exchangeProperty.sessionId}"),
	})
	route.AddProcessor(&gocamel.SimpleSetHeaderProcessor{
		HeaderName: "X-Timestamp",
		Expression: mustParse("${date:now}"),
	})

	// Add a Choice processor to demonstrate mixing patterns
	choice := gocamel.NewChoiceProcessor().
		When("${header.type == 'special'}", gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
			ex.GetOut().SetHeader("X-Special", "true")
			return nil
		})).
		Otherwise(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
			ex.GetOut().SetHeader("X-Special", "false")
			return nil
		}))
	route.AddProcessor(choice)

	// Test with 'special' header
	exchange.GetIn().SetHeader("type", "special")

	// Process the exchange
	if err := route.Process(exchange); err != nil {
		log.Printf("Error processing: %v", err)
		return
	}

	// Display results
	fmt.Println("Route processing complete!")
	fmt.Println()
	fmt.Printf("Original Body: %v\n", "Original Message")
	fmt.Printf("Output Body:   %v\n", exchange.GetOut().GetBody())
	fmt.Println()
	fmt.Println("Output Headers:")
	for key, value := range exchange.GetOut().GetHeaders() {
		fmt.Printf("  %s: %v\n", key, value)
	}
	fmt.Println()

	// Show the exchange properties
	fmt.Println("Exchange Properties:")
	if sessionId, exists := exchange.GetProperty("sessionId"); exists {
		fmt.Printf("  sessionId: %v\n", sessionId)
	}

	// Test with non-special header
	fmt.Println()
	fmt.Println("--- Processing with non-special type ---")
	exchange2 := gocamel.NewExchange(ctx.GetContext())
	exchange2.GetIn().SetBody("Another Message")
	exchange2.GetIn().SetHeader("type", "normal")
	exchange2.SetProperty("sessionId", "xyz789")

	route2 := gocamel.NewRoute()
	route2.AddProcessor(choice) // Reuse the same choice processor

	if err := route2.Process(exchange2); err != nil {
		log.Printf("Error: %v", err)
		return
	}

	if special, exists := exchange2.GetOut().GetHeader("X-Special"); exists {
		fmt.Printf("X-Special header: %v (expected: false)\n", special)
	}
}

func mustParse(expr string) *gocamel.SimpleTemplate {
	template, err := gocamel.ParseSimpleTemplate(expr)
	if err != nil {
		panic(err)
	}
	return template
}

// Example output:
// =================
// === GoCamel Simple Language Example ===
//
// Demo 1: Variable Access
// -------------------------
// ...
//
// Demo 5: Map Access with Bracket Notation
// ----------------------------------------
// ...
//
// Demo 6: List/Array Access with Bracket Notation
// -----------------------------------------------
// ...
//
// Demo 7: Nested Access (Map[List[Map]])
// --------------------------------------
// ...
//
// Demo 8: Null-Safe Operator
// --------------------------------
// ...
//
// Demo 9: Route Builder with Choice/When/Otherwise
// --------------------------------------------------
// ...
//
// Demo 10: Route Builder Integration
// -----------------------------------
// ...
