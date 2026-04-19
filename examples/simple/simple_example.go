// Example: Simple Language expression parser demonstration with Phase 3 & 4 Features
//
// This example demonstrates the Simple Language expression parser
// including all Phase 3 & 4 features:
//
// PHASE 3: String Operations, Logical Operators, Ternary Operator
// - contains, startsWith, endsWith, regex operators
// - && (AND), || (OR), ! (NOT) operators
// - conditional: ${condition ? true-value : false-value}
//
// PHASE 4: String Functions, Math Operations, Type/Range Operations
// - trim(), uppercase/upper(), lowercase/lower(), size/length(), substring(), replace()
// - +, -, *, /, % math operations
// - "in" list membership, "range" check, "is" type checking
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
	fmt.Println("=== GoCamel Simple Language Example with Phase 3 & 4 Features ===")
	fmt.Println("=================================================================")
	fmt.Println()

	// Create the Camel context
	ctx := gocamel.NewCamelContext()

	// Create a test exchange
	exchange := gocamel.NewExchange(ctx.GetContext())
	exchange.GetIn().SetBody("  Hello from GoCamel!  ")
	exchange.GetIn().SetHeader("Content-Type", "text/plain")
	exchange.GetIn().SetHeader("X-Request-ID", "req-12345")
	exchange.SetProperty("processedAt", time.Now().Format(time.RFC3339))
	exchange.SetProperty("user", "john.doe")

	// Demo 1: Basic variable access (existing functionality)
	fmt.Println("Demo 1: Basic Variable Access")
	fmt.Println("-----------------------------")
	demoExpressions(ctx, exchange, []string{
		"Body: ${body}",
		"Content-Type header: ${header.Content-Type}",
		"User property: ${exchangeProperty.user}",
	})
	fmt.Println()

	// Demo 2: PHASE 3 - String Operations
	fmt.Println("Demo 2: String Operations (Phase 3)")
	fmt.Println("------------------------------------")
	exchange.GetIn().SetBody("URGENT: Process this order")
	exchange.GetIn().SetHeader("filename", "/data/orders.json")
	exchange.GetIn().SetHeader("email", "user@example.com")
	demoExpressionsAsBool(exchange, []struct {
		expr string
		desc string
	}{
		{"${body contains 'URGENT'}", "body contains 'URGENT'"},
		{"${body contains 'urgent'}", "body contains 'urgent' (case sensitive - false)"},
		{"${header.filename startsWith '/data/'}", "filename starts with '/data/'"},
		{"${header.filename endsWith '.json'}", "filename ends with '.json'"},
		{"${header.email regex '^[a-z]+@[a-z]+\\\\.[a-z]+$'}", "email matches pattern"},
		{"${body regex 'Order \\d+'}", "body matches 'Order \\d+' pattern"},
	})
	fmt.Println()

	// Demo 3: PHASE 3 - Logical Operators
	fmt.Println("Demo 3: Logical Operators (Phase 3)")
	fmt.Println("-------------------------------------")
	exchange.GetIn().SetHeader("count", 150)
	exchange.GetIn().SetHeader("type", "gold")
	exchange.GetIn().SetHeader("status", "active")
	demoExpressionsAsBool(exchange, []struct {
		expr string
		desc string
	}{
		{"${header.count > 100 && header.type == 'gold'}", "count > 100 AND type == 'gold'"},
		{"${header.count > 200 || header.type == 'gold'}", "count > 200 OR type == 'gold'"},
		{"${!header.active}", "NOT header.active (false)"},
		{"${header.status == 'active' && (header.count > 100 || header.type == 'silver')}", "complex: status active AND (count > 100 OR type silver)"},
	})
	fmt.Println()

	// Demo 4: PHASE 3 - Ternary Operator
	fmt.Println("Demo 4: Ternary Operator (Phase 3)")
	fmt.Println("----------------------------------")
	demoExpressions(ctx, exchange, []string{
		"Priority: ${header.count > 100 ? 'High' : 'Low'}",
		"Customer Type: ${header.type == 'gold' ? 'Premium' : 'Standard'}",
		"Status Message: ${header.status == 'active' ? body : 'Inactive'}",
	})
	fmt.Println()

	// Demo 5: PHASE 4 - String Functions
	fmt.Println("Demo 5: String Functions (Phase 4)")
	fmt.Println("-----------------------------------")
	exchange.GetIn().SetBody("  Hello World  ")
	exchange.GetIn().SetHeader("name", "john doe")
	demoExpressions(ctx, exchange, []string{
		"Original: [${body}]",
		"After trim(): [${body.trim()}]",
		"Uppercase: ${header.name.uppercase()}",
		"Lowercase: ${header.name.lowercase()}",
		"Length: ${body.trim().size()}",
		"Substring(0,5): ${body.trim().substring(0,5)}",
		"Replace: ${body.trim().replace('World', 'Universe')}",
	})
	fmt.Println()

	// Demo 6: PHASE 4 - Function Chaining
	fmt.Println("Demo 6: Function Chaining (Phase 4)")
	fmt.Println("------------------------------------")
	exchange.GetIn().SetHeader("raw", "  TeXt  DaTa  ")
	demoExpressions(ctx, exchange, []string{
		"Input: [${header.raw}]",
		"trim().uppercase(): [${header.raw.trim().uppercase()}]",
		"trim().lowercase().substring(0,5): ${header.raw.trim().lowercase().substring(0,5)}",
		"trim().size(): ${header.raw.trim().size()}",
	})
	fmt.Println()

	// Demo 7: PHASE 4 - Math Operations
	fmt.Println("Demo 7: Math Operations (Phase 4)")
	fmt.Println("---------------------------------")
	exchange.GetIn().SetBody("50")
	exchange.GetIn().SetHeader("count", 10)
	demoExpressions(ctx, exchange, []string{
		"10 + 5 = ${header.count + 5}",
		"10 - 3 = ${header.count - 3}",
		"10 * 4 = ${header.count * 4}",
		"10 / 2 = ${header.count / 2}",
		"10 % 3 = ${header.count % 3}",
		"50 + 10 - 20 = ${body + 10 - 20}",
	})
	fmt.Println()

	// Demo 8: PHASE 4 - Type/Range Operations
	fmt.Println("Demo 8: Type/Range Operations (Phase 4)")
	fmt.Println("----------------------------------------")
	exchange.GetIn().SetBody("Hello")
	exchange.GetIn().SetHeader("code", 150)
	exchange.GetIn().SetHeader("category", "A")
	exchange.GetIn().SetHeader("data", map[string]interface{}{"key": "value"})
	exchange.GetIn().SetHeader("numbers", []int{1, 2, 3})

	demoExpressionsAsBool(exchange, []struct {
		expr string
		desc string
	}{
		{"${header.category in 'A,B,C'}", "category in list 'A,B,C'"},
		{"${header.code range 100..199}", "code in range 100..199"},
		{"${body is 'String'}", "body is 'String'"},
		{"${header.data is 'Map'}", "header.data is 'Map'"},
		{"${header.numbers is 'Slice'}", "header.numbers is 'Slice'"},
	})
	fmt.Println()

	// Demo 9: PHASE 3 & 4 - Complex Expressions
	fmt.Println("Demo 9: Complex Expressions (Phase 3 & 4 Combined)")
	fmt.Println("--------------------------------------------------")
	exchange.GetIn().SetBody("URGENT: Review order #12345")
	exchange.GetIn().SetHeader("amount", 250)
	exchange.GetIn().SetHeader("customer", "platinum")

	demoExpressions(ctx, exchange, []string{
		"Alert: ${body contains 'URGENT' && header.amount > 100 ? 'HIGH PRIORITY' : 'Standard'}",
		"Category: ${header.customer in 'gold,platinum' ? header.customer.uppercase() : 'regular'}",
		"Range Check: ${header.amount range 200..299 ? 'Tier 2' : 'Other Tier'}",
		"Message: ${body.trim().substring(0,6).uppercase() == 'URGENT' ? 'Urgent' : 'Normal'}",
	})
	fmt.Println()

	// Demo 10: Route Building with New Features
	fmt.Println("Demo 10: Route Building with Logical Operators")
	fmt.Println("----------------------------------------------")
	demoChoiceWithLogicalOperators(ctx)
	fmt.Println()

	// Demo 11: Null-Safe Function Chaining
	fmt.Println("Demo 11: Null-Safe Function Chaining")
	fmt.Println("--------------------------------------")
	exchange.GetIn().SetBody(nil)
	demoExpressions(ctx, exchange, []string{
		"Safe access: ${body?.trim()}",
		"Safe access chain: ${body?.trim().uppercase()}",
		"Safe ternary: ${body?.trim() == nil ? 'Empty' : body}",
	})
	fmt.Println()

	fmt.Println("=== All demos complete! ===")
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

		fmt.Printf("  %s\n", expr)
		fmt.Printf("  → %s\n\n", result)
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

func demoChoiceWithLogicalOperators(ctx *gocamel.CamelContext) {
	// Create a route with Choice pattern using logical operators
	builder := ctx.CreateRouteBuilder()
	builder.
		SetID("logical-choice-demo").
		Choice().
		When("${header.priority == 'high' && header.amount > 100}").
		SimpleSetBody("HIGH PRIORITY TRANSACTION: ${body}").
		SetHeader("X-Priority", "high").
		When("${body contains 'URGENT' || body startsWith 'CRITICAL'}").
		SimpleSetBody("URGENT: ${body}").
		SetHeader("X-Priority", "urgent").
		When("${header.category in 'A,B,C'}").
		SimpleSetBody("Category ${header.category}: ${body}").
		Otherwise().
		SimpleSetBody("Standard: ${body}").
		SetHeader("X-Priority", "normal").
		EndChoice()

	route := builder.Build()

	// Test different scenarios
	testCases := []struct {
		priority string
		amount   int
		body     string
		category string
	}{
		{"high", 150, "Transaction", ""},
		{"normal", 50, "URGENT: Help needed", ""},
		{"low", 10, "CRITICAL: Server down", ""},
		{"normal", 50, "Regular task", "A"},
		{"low", 10, "Background job", "Z"},
	}

	for _, tc := range testCases {
		exchange := gocamel.NewExchange(ctx.GetContext())
		exchange.GetIn().SetHeader("priority", tc.priority)
		exchange.GetIn().SetHeader("amount", tc.amount)
		exchange.GetIn().SetHeader("category", tc.category)
		exchange.GetIn().SetBody(fmt.Sprintf("Message: %s", tc.body))

		err := route.Process(exchange)
		if err != nil {
			log.Printf("Error processing: %v", err)
			continue
		}

		priority, _ := exchange.GetOut().GetHeader("X-Priority")
		fmt.Printf("  priority=%s, amount=%d, category=%s\n", tc.priority, tc.amount, tc.category)
		fmt.Printf("    → %s (prioritized as: %s)\n\n", exchange.GetOut().GetBody(), priority)
	}
}

// Additional helper for parsing expressions
func mustParse(expr string) *gocamel.SimpleTemplate {
	template, err := gocamel.ParseSimpleTemplate(expr)
	if err != nil {
		panic(err)
	}
	return template
}

// Example output:
//=================
//=== GoCamel Simple Language Example with Phase 3 & 4 Features ===
//
// Demo 1: Basic Variable Access
// -----------------------------
// ...
//
// Demo 2: String Operations (Phase 3)
// ------------------------------------
// ...
//
// Demo 3: Logical Operators (Phase 3)
// -------------------------------------
// ...
//
// Demo 4: Ternary Operator (Phase 3)
// ----------------------------------
// ...
//
// Demo 5: String Functions (Phase 4)
// -----------------------------------
// ...
//
// Demo 6: Function Chaining (Phase 4)
// ------------------------------------
// ...
//
// Demo 7: Math Operations (Phase 4)
// ---------------------------------
// ...
//
// Demo 8: Type/Range Operations (Phase 4)
// ----------------------------------------
// ...
//
// Demo 9: Complex Expressions (Phase 3 & 4 Combined)
// --------------------------------------------------
// ...
//
// Demo 10: Route Building with Logical Operators
// ----------------------------------------------
// ...
//
// Demo 11: Null-Safe Function Chaining
// --------------------------------------
// ...
