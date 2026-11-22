package gocamel

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSimpleTemplate(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		isLiteral  bool
		partCount  int
		wantErr    bool
	}{
		{
			name:       "literal text",
			expression: "Hello World",
			isLiteral:  true,
			partCount:  1,
			wantErr:    false,
		},
		{
			name:       "simple variable",
			expression: "${body}",
			isLiteral:  false,
			partCount:  1,
			wantErr:    false,
		},
		{
			name:       "template with text and variable",
			expression: "Hello ${body}!",
			isLiteral:  false,
			partCount:  3, // "Hello ", ${body}, "!"
			wantErr:    false,
		},
		{
			name:       "template with header",
			expression: "Header: ${header.Content-Type}",
			isLiteral:  false,
			partCount:  2, // "Header: ", ${header.Content-Type}
			wantErr:    false,
		},
		{
			name:       "template with exchangeProperty",
			expression: "Prop: ${exchangeProperty.myProp}",
			isLiteral:  false,
			partCount:  2,
			wantErr:    false,
		},
		{
			name:       "multiple variables",
			expression: "${header.a}-${header.b}",
			isLiteral:  false,
			partCount:  3, // ${}, -, ${}
			wantErr:    false,
		},
		{
			name:       "bracket notation for map",
			expression: "Value: ${body['key']}",
			isLiteral:  false,
			partCount:  2,
			wantErr:    false,
		},
		{
			name:       "bracket notation for list",
			expression: "Item: ${body[0]}",
			isLiteral:  false,
			partCount:  2,
			wantErr:    false,
		},
		{
			name:       "null-safe operator",
			expression: "Safe: ${body?.field}",
			isLiteral:  false,
			partCount:  2,
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			template, err := ParseSimpleTemplate(tt.expression)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseSimpleTemplate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if template == nil {
				t.Fatal("Expected non-nil template")
			}
			if template.isLiteral != tt.isLiteral {
				t.Errorf("isLiteral = %v, want %v", template.isLiteral, tt.isLiteral)
			}
			if len(template.parts) != tt.partCount {
				t.Errorf("partCount = %v, want %v", len(template.parts), tt.partCount)
			}
		})
	}
}

// TestSimpleTemplateCacheIsBounded verifies that the parse cache stops growing
// once MaxSimpleTemplateCacheEntries is reached: parsing still succeeds for
// uncached expressions, and the internal size counter stops at the cap.
func TestSimpleTemplateCacheIsBounded(t *testing.T) {
	// Restore the global cap and size after the test.
	savedCap := MaxSimpleTemplateCacheEntries
	t.Cleanup(func() {
		MaxSimpleTemplateCacheEntries = savedCap
		// The cache itself keeps the entries inserted below; that is fine —
		// the counter, not the map contents, is what the cap guards.
	})

	// Note: the cache is shared with other tests in this package, so the
	// baseline size is unknown. Use a cap relative to that baseline.
	base := simpleCacheSize.Load()

	smallCap := int(base + 5)
	MaxSimpleTemplateCacheEntries = smallCap

	// First 5 new expressions are cached.
	for i := 0; i < 5; i++ {
		expr := fmt.Sprintf("bounded-cache-test-%d: ${body}", i)
		_, err := ParseSimpleTemplate(expr)
		if err != nil {
			t.Fatalf("ParseSimpleTemplate(%q) failed: %v", expr, err)
		}
	}
	if got := simpleCacheSize.Load(); got != base+5 {
		t.Fatalf("expected cache size %d after 5 inserts, got %d", base+5, got)
	}

	// Beyond the cap: parsing succeeds but the cache does not grow.
	for i := 5; i < 25; i++ {
		expr := fmt.Sprintf("bounded-cache-test-%d: ${body}", i)
		tpl, err := ParseSimpleTemplate(expr)
		if err != nil {
			t.Fatalf("ParseSimpleTemplate(%q) failed: %v", expr, err)
		}
		if tpl == nil {
			t.Fatal("expected a parsed template even when the cache is full")
		}
	}
	if got := simpleCacheSize.Load(); got != base+5 {
		t.Fatalf("cache must not grow past the cap: expected size %d, got %d", base+5, got)
	}

	// An expression already inserted is still served from the cache.
	tpl, err := ParseSimpleTemplate("bounded-cache-test-0: ${body}")
	if err != nil {
		t.Fatalf("ParseSimpleTemplate failed: %v", err)
	}
	if _, ok := simpleTemplateCache.Load("bounded-cache-test-0: ${body}"); !ok {
		t.Fatal("previously cached expression must remain in the cache")
	}
	_ = tpl
}

func TestSimpleTemplateEvaluate(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetBody("Hello World")
	ex.GetIn().SetHeader("Content-Type", "application/json")
	ex.GetIn().SetHeader("X-Custom", "custom-value")
	ex.SetProperty("myProp", "my-value")
	ex.SetProperty("intProp", 42)

	tests := []struct {
		name       string
		expression string
		want       string
		wantErr    bool
	}{
		{
			name:       "literal text",
			expression: "Static text",
			want:       "Static text",
			wantErr:    false,
		},
		{
			name:       "body variable",
			expression: "${body}",
			want:       "Hello World",
			wantErr:    false,
		},
		{
			name:       "body in template",
			expression: "Body: ${body}",
			want:       "Body: Hello World",
			wantErr:    false,
		},
		{
			name:       "header variable",
			expression: "${header.Content-Type}",
			want:       "application/json",
			wantErr:    false,
		},
		{
			name:       "header in template",
			expression: "Type: ${header.Content-Type}",
			want:       "Type: application/json",
			wantErr:    false,
		},
		{
			name:       "exchangeProperty variable",
			expression: "${exchangeProperty.myProp}",
			want:       "my-value",
			wantErr:    false,
		},
		{
			name:       "exchangeProperty in template",
			expression: "Prop: ${exchangeProperty.myProp}",
			want:       "Prop: my-value",
			wantErr:    false,
		},
		{
			name:       "multiple headers",
			expression: "${header.Content-Type} - ${header.X-Custom}",
			want:       "application/json - custom-value",
			wantErr:    false,
		},
		{
			name:       "missing header returns nil",
			expression: "${header.Missing}",
			want:       "<nil>",
			wantErr:    false,
		},
		{
			name:       "missing property returns nil",
			expression: "${exchangeProperty.Missing}",
			want:       "<nil>",
			wantErr:    false,
		},
		{
			name:       "complex template",
			expression: "Body: ${body} | Type: ${header.Content-Type} | Prop: ${exchangeProperty.myProp}",
			want:       "Body: Hello World | Type: application/json | Prop: my-value",
			wantErr:    false,
		},
		{
			name:       "numeric body",
			expression: "Value: ${body}",
			want:       "Value: 123",
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// For numeric body test
			if tt.name == "numeric body" {
				ex.GetIn().SetBody(123)
			} else if tt.name != "literal text" && strings.Contains(tt.name, "body") {
				ex.GetIn().SetBody("Hello World")
			}

			template, err := ParseSimpleTemplate(tt.expression)
			if err != nil {
				t.Fatalf("ParseSimpleTemplate() error = %v", err)
			}

			got, err := template.EvaluateAsString(ex)
			if (err != nil) != tt.wantErr {
				t.Errorf("EvaluateAsString() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("EvaluateAsString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSimpleTemplateFunctions(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())

	tests := []struct {
		name       string
		expression string
		wantCheck  func(string) bool
		wantErr    bool
	}{
		{
			name:       "uuid function",
			expression: "${uuid}",
			wantCheck: func(s string) bool {
				return len(s) == 36 && strings.Count(s, "-") == 4
			},
			wantErr: false,
		},
		{
			name:       "random function",
			expression: "${random(100)}",
			wantCheck: func(s string) bool {
				val, err := time.ParseDuration(s + "ns")
				_ = val // Just checking it's a valid number
				return err == nil
			},
			wantErr: false,
		},
		{
			name:       "date now function with default format",
			expression: "${date:now}",
			wantCheck: func(s string) bool {
				// Should be a valid timestamp
				_, err := time.Parse(time.RFC3339, s)
				return err == nil
			},
			wantErr: false,
		},
		// Handle date now function with custom format
		{
			name:       "date now function with custom format",
			expression: "${date:now:2006-01-02}",
			wantCheck: func(s string) bool {
				// 2006-01-02 format returns a date like "2006-01-02"
				// Just check it has the right format pattern
				return len(s) == 10 && s[4] == '-' && s[7] == '-'
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			template, err := ParseSimpleTemplate(tt.expression)
			if err != nil {
				t.Fatalf("ParseSimpleTemplate() error = %v", err)
			}

			got, err := template.EvaluateAsString(ex)
			if (err != nil) != tt.wantErr {
				t.Errorf("EvaluateAsString() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantCheck(got) {
				t.Errorf("EvaluateAsString() = %v, failed check", got)
			}
		})
	}
}

func TestSimpleTemplateComparisons(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetBody("test")
	ex.GetIn().SetHeader("count", 10)
	ex.GetIn().SetHeader("name", "John")
	ex.SetProperty("value", 5)

	tests := []struct {
		name       string
		expression string
		want       interface{}
		wantType   string // "bool" or "string"
		wantErr    bool
	}{
		{
			name:       "body equals",
			expression: "${body == 'test'}",
			want:       "true",
			wantType:   "string",
			wantErr:    false,
		},
		{
			name:       "body not equals",
			expression: "${body != 'other'}",
			want:       "true",
			wantType:   "string",
			wantErr:    false,
		},
		{
			name:       "numeric greater than",
			expression: "${header.count > 5}",
			want:       "true",
			wantType:   "string",
			wantErr:    false,
		},
		{
			name:       "numeric less than",
			expression: "${header.count < 20}",
			want:       "true",
			wantType:   "string",
			wantErr:    false,
		},
		{
			name:       "numeric greater than or equal",
			expression: "${header.count >= 10}",
			want:       "true",
			wantType:   "string",
			wantErr:    false,
		},
		{
			name:       "numeric less than or equal",
			expression: "${header.count <= 10}",
			want:       "true",
			wantType:   "string",
			wantErr:    false,
		},
		{
			name:       "string equals",
			expression: "${header.name == 'John'}",
			want:       "true",
			wantType:   "string",
			wantErr:    false,
		},
		{
			name:       "property equals",
			expression: "${exchangeProperty.value == 5}",
			want:       "true",
			wantType:   "string",
			wantErr:    false,
		},
		{
			name:       "property greater than",
			expression: "${exchangeProperty.value > 3}",
			want:       "true",
			wantType:   "string",
			wantErr:    false,
		},
		{
			name:       "body equals with double quotes",
			expression: "${body == \"test\"}",
			want:       "true",
			wantType:   "string",
			wantErr:    false,
		},
		{
			name:       "body equals false",
			expression: "${body == 'wrong'}",
			want:       "false",
			wantType:   "string",
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			template, err := ParseSimpleTemplate(tt.expression)
			if err != nil {
				t.Fatalf("ParseSimpleTemplate() error = %v", err)
			}

			got, err := template.EvaluateAsString(ex)
			if (err != nil) != tt.wantErr {
				t.Errorf("EvaluateAsString() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("EvaluateAsString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSimpleSetBodyProcessor(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetBody("Original")
	ex.GetIn().SetHeader("type", "text")

	// Test SimpleSetBodyProcessor
	template, _ := ParseSimpleTemplate("Body: ${body}")
	processor := &SimpleLanguageProcessor{Template: template}

	err := processor.Process(ex)
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	got := ex.GetOut().GetBody()
	want := "Body: Original"
	if got != want {
		t.Errorf("GetOut().GetBody() = %v, want %v", got, want)
	}

	// Verify headers are copied
	if header, exists := ex.GetOut().GetHeader("type"); !exists || header != "text" {
		t.Error("Headers were not copied to output message")
	}
}

func TestSimpleSetHeaderProcessor(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetBody("Hello")
	ex.GetIn().SetHeader("X-Original", "value")

	// Test SimpleSetHeaderProcessor
	template, _ := ParseSimpleTemplate("${body} World")
	processor := &SimpleSetHeaderProcessor{
		HeaderName: "X-Processed",
		Expression: template,
	}

	err := processor.Process(ex)
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	got, exists := ex.GetResponse().GetHeader("X-Processed")
	if !exists {
		t.Fatal("X-Processed header not found")
	}
	if got != "Hello World" {
		t.Errorf("X-Processed header = %v, want %v", got, "Hello World")
	}
}

func TestRouteBuilderSimpleSetBody(t *testing.T) {
	ctx := NewCamelContext()
	builder := ctx.CreateRouteBuilder()

	// Build a route using SimpleSetBody and SimpleSetHeader
	// Note: We use route.Builder().ProcessFunc(...) pattern since RouteBuilder
	// requires a valid endpoint
	builder.SetID("test-route").
		SimpleSetBody("Got: ${body}").
		SimpleSetHeader("X-Simple", "simple")

	route, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}

	if route == nil {
		t.Fatal("Expected non-nil route")
	}

	if len(route.processors) != 2 {
		t.Errorf("Expected 2 processors, got %d", len(route.processors))
	}
}

func TestRouteBuilderSimpleSetHeader(t *testing.T) {
	ctx := NewCamelContext()
	builder := ctx.CreateRouteBuilder()

	builder.SetID("test-route").
		SimpleSetHeader("X-Message", "${body}")

	route, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}

	if route == nil {
		t.Fatal("Expected non-nil route")
	}

	if len(route.processors) != 1 {
		t.Errorf("Expected 1 processor, got %d", len(route.processors))
	}
}

func TestRouteBuilder_BuildSurfacesFromError(t *testing.T) {
	ctx := NewCamelContext()
	_, err := ctx.CreateRouteBuilder().From("unknown:foo").Build()
	if err == nil {
		t.Fatal("expected Build to fail on an unknown component")
	}
}

func TestSimpleSetBodyAddsToSplitContainer(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	route, err := ctx.CreateRouteBuilder().
		From("direct:start").
		Split(func(e *Exchange) (any, error) {
			return []string{"a", "b"}, nil
		}).
		SimpleSetBody("x-${body}").
		End().
		Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(route.processors) != 1 {
		t.Fatalf("expected 1 top-level processor (the splitter), got %d", len(route.processors))
	}
	split, ok := route.processors[0].(*Splitter)
	if !ok {
		t.Fatalf("expected *Splitter, got %T", route.processors[0])
	}
	if len(split.processors) != 1 {
		t.Fatalf("expected SimpleSetBody inside the splitter, got %d nested processors", len(split.processors))
	}
}

func TestComplexTemplateWithAllFeatures(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetBody("TestMessage")
	ex.GetIn().SetHeader("RequestId", "12345")
	ex.SetProperty("timestamp", time.Date(2026, 4, 19, 10, 30, 0, 0, time.UTC))

	// Complex template using multiple features
	template, err := ParseSimpleTemplate("ID: ${header.RequestId}, Body: ${body}")
	if err != nil {
		t.Fatalf("ParseSimpleTemplate() error = %v", err)
	}

	result, err := template.EvaluateAsString(ex)
	if err != nil {
		t.Fatalf("EvaluateAsString() error = %v", err)
	}

	want := "ID: 12345, Body: TestMessage"
	if result != want {
		t.Errorf("Result = %v, want %v", result, want)
	}
}

func TestExpressionFuncType(t *testing.T) {
	// Test that ExpressionFunc implements Expression
	var _ Expression = ExpressionFunc(func(ex *Exchange) (interface{}, error) {
		return "test", nil
	})
}

// ==================== MAP ACCESS TESTS ====================

func TestMapAccessWithBracketNotation(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetBody(map[string]interface{}{
		"name":  "John",
		"email": "john@example.com",
		"address": map[string]interface{}{
			"city":    "New York",
			"country": "USA",
		},
	})

	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{
			name:       "simple map key with single quotes",
			expression: "${body['name']}",
			want:       "John",
		},
		{
			name:       "simple map key with double quotes",
			expression: "${body[\"email\"]}",
			want:       "john@example.com",
		},
		{
			name:       "nested map access",
			expression: "${body['address']['city']}",
			want:       "New York",
		},
		{
			name:       "nested map with double quotes",
			expression: "${body[\"address\"][\"country\"]}",
			want:       "USA",
		},
		{
			name:       "missing key returns nil",
			expression: "${body['nonexistent']}",
			want:       "<nil>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			template, err := ParseSimpleTemplate(tt.expression)
			if err != nil {
				t.Fatalf("ParseSimpleTemplate() error = %v", err)
			}

			got, err := template.EvaluateAsString(ex)
			if err != nil {
				t.Fatalf("EvaluateAsString() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("EvaluateAsString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMapAccessWithSpacesAndSpecialChars(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetBody(map[string]interface{}{
		"key with spaces":  "value with spaces",
		"special-chars!@#": "special value",
		"":                 "empty key value",
	})

	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{
			name:       "key with spaces",
			expression: "${body['key with spaces']}",
			want:       "value with spaces",
		},
		{
			name:       "key with special characters",
			expression: "${body['special-chars!@#']}",
			want:       "special value",
		},
		{
			name:       "empty key",
			expression: "${body['']}",
			want:       "empty key value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			template, err := ParseSimpleTemplate(tt.expression)
			if err != nil {
				t.Fatalf("ParseSimpleTemplate() error = %v", err)
			}

			got, err := template.EvaluateAsString(ex)
			if err != nil {
				t.Fatalf("EvaluateAsString() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("EvaluateAsString() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ==================== LIST ACCESS TESTS ====================

func TestListAccessWithBracketNotation(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetBody([]interface{}{
		"first",
		"second",
		"third",
		map[string]interface{}{
			"nested": "value",
		},
	})

	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{
			name:       "first element",
			expression: "${body[0]}",
			want:       "first",
		},
		{
			name:       "second element",
			expression: "${body[1]}",
			want:       "second",
		},
		{
			name:       "last element (map)",
			expression: "${body[3]}",
			want:       "map[nested:value]",
		},
		{
			name:       "out of bounds returns nil",
			expression: "${body[10]}",
			want:       "<nil>",
		},
		{
			name:       "negative index returns nil",
			expression: "${body[-1]}",
			want:       "<nil>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			template, err := ParseSimpleTemplate(tt.expression)
			if err != nil {
				t.Fatalf("ParseSimpleTemplate() error = %v", err)
			}

			got, err := template.EvaluateAsString(ex)
			if err != nil {
				t.Fatalf("EvaluateAsString() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("EvaluateAsString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestListLastIndexAccess(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetBody([]interface{}{
		"item0",
		"item1",
		"item2",
		"item3",
	})

	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{
			name:       "last index",
			expression: "${body[last]}",
			want:       "item3",
		},
		{
			name:       "last minus 1",
			expression: "${body[last-1]}",
			want:       "item2",
		},
		{
			name:       "last minus 2",
			expression: "${body[last-2]}",
			want:       "item1",
		},
		{
			name:       "last minus 3",
			expression: "${body[last-3]}",
			want:       "item0",
		},
		{
			name:       "last minus 4 out of bounds",
			expression: "${body[last-4]}",
			want:       "<nil>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			template, err := ParseSimpleTemplate(tt.expression)
			if err != nil {
				t.Fatalf("ParseSimpleTemplate() error = %v", err)
			}

			got, err := template.EvaluateAsString(ex)
			if err != nil {
				t.Fatalf("EvaluateAsString() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("EvaluateAsString() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ==================== NESTED ACCESS TESTS ====================

func TestNestedAccessMixedNotation(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetBody(map[string]interface{}{
		"users": []interface{}{
			map[string]interface{}{
				"name":  "Alice",
				"email": "alice@example.com",
			},
			map[string]interface{}{
				"name":  "Bob",
				"email": "bob@example.com",
			},
		},
	})

	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{
			name:       "users list",
			expression: "${body['users']}",
			want:       "[map[email:alice@example.com name:Alice] map[email:bob@example.com name:Bob]]",
		},
		{
			name:       "first user",
			expression: "${body['users'][0]}",
			want:       "map[email:alice@example.com name:Alice]",
		},
		{
			name:       "first user name",
			expression: "${body['users'][0]['name']}",
			want:       "Alice",
		},
		{
			name:       "second user name",
			expression: "${body['users'][1]['name']}",
			want:       "Bob",
		},
		{
			name:       "second user email mixed notation",
			expression: "${body['users'][1][\"email\"]}",
			want:       "bob@example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			template, err := ParseSimpleTemplate(tt.expression)
			if err != nil {
				t.Fatalf("ParseSimpleTemplate() error = %v", err)
			}

			got, err := template.EvaluateAsString(ex)
			if err != nil {
				t.Fatalf("EvaluateAsString() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("EvaluateAsString() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ==================== NULL-SAFE OPERATOR TESTS ====================

func TestNullSafeOperatorWithDotNotation(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())

	// Test with nil body
	ex.GetIn().SetBody(nil)

	template, err := ParseSimpleTemplate("${body?.field}")
	if err != nil {
		t.Fatalf("ParseSimpleTemplate() error = %v", err)
	}

	got, err := template.EvaluateAsString(ex)
	if err != nil {
		t.Fatalf("EvaluateAsString() error = %v", err)
	}
	if got != "<nil>" {
		t.Errorf("With nil body, body?.field = %v, want <nil>", got)
	}

	// Test with non-nil body
	ex.GetIn().SetBody(map[string]interface{}{
		"field": "value",
	})

	got, err = template.EvaluateAsString(ex)
	if err != nil {
		t.Fatalf("EvaluateAsString() error = %v", err)
	}
	if got != "value" {
		t.Errorf("With non-nil body, body?.field = %v, want value", got)
	}
}

func TestNullSafeOperatorChaining(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetBody(map[string]interface{}{
		"user": map[string]interface{}{
			"profile": map[string]interface{}{
				"name": "John",
			},
		},
	})

	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{
			name:       "double null-safe access",
			expression: "${body?.user?.profile?.name}",
			want:       "John",
		},
		{
			name:       "mixed null-safe and regular",
			expression: "${body?.user?.profile}",
			want:       "map[name:John]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			template, err := ParseSimpleTemplate(tt.expression)
			if err != nil {
				t.Fatalf("ParseSimpleTemplate() error = %v", err)
			}

			got, err := template.EvaluateAsString(ex)
			if err != nil {
				t.Fatalf("EvaluateAsString() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("EvaluateAsString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNullSafeOperatorWithHeaderAndProperty(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())

	// Test with header key that doesn't exist
	ex.GetIn().SetHeader("X-Exists", "exists-value")

	template, err := ParseSimpleTemplate("${header?.X-Exists}")
	if err != nil {
		t.Fatalf("ParseSimpleTemplate() error = %v", err)
	}

	got, err := template.EvaluateAsString(ex)
	if err != nil {
		t.Fatalf("EvaluateAsString() error = %v", err)
	}
	if got != "exists-value" {
		t.Errorf("header?.X-Exists = %v, want exists-value", got)
	}

	// Test with missing property using null-safe
	template2, err := ParseSimpleTemplate("${exchangeProperty?.ExistingProp}")
	if err != nil {
		t.Fatalf("ParseSimpleTemplate() error = %v", err)
	}

	got, err = template2.EvaluateAsString(ex)
	if err != nil {
		t.Fatalf("EvaluateAsString() error = %v", err)
	}
	if got != "<nil>" {
		t.Errorf("exchangeProperty?.ExistingProp with no property = %v, want <nil>", got)
	}
}

// ==================== CHOICE PROCESSOR TESTS ====================

func TestChoiceProcessorWithWhenConditions(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())

	// Create a choice processor
	choice := NewChoiceProcessor().
		When("${header.type == 'A'}", ProcessorFunc(func(ex *Exchange) error {
			ex.GetOut().SetBody("Type A processed")
			return nil
		})).
		When("${header.type == 'B'}", ProcessorFunc(func(ex *Exchange) error {
			ex.GetOut().SetBody("Type B processed")
			return nil
		})).
		Otherwise(ProcessorFunc(func(ex *Exchange) error {
			ex.GetOut().SetBody("Unknown type")
			return nil
		}))

	tests := []struct {
		name        string
		headerValue string
		wantBody    string
	}{
		{
			name:        "matches first when",
			headerValue: "A",
			wantBody:    "Type A processed",
		},
		{
			name:        "matches second when",
			headerValue: "B",
			wantBody:    "Type B processed",
		},
		{
			name:        "falls to otherwise",
			headerValue: "C",
			wantBody:    "Unknown type",
		},
		{
			name:        "falls to otherwise with empty",
			headerValue: "",
			wantBody:    "Unknown type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ex.GetIn().SetHeader("type", tt.headerValue)
			ex.GetOut().SetBody(nil) // Reset output

			err := choice.Process(ex)
			if err != nil {
				t.Fatalf("Process() error = %v", err)
			}

			got := ex.GetOut().GetBody()
			if got != tt.wantBody {
				t.Errorf("Process() body = %v, want %v", got, tt.wantBody)
			}
		})
	}
}

func TestChoiceProcessorNumericComparisons(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())

	// Create a choice processor with numeric conditions
	choice := NewChoiceProcessor().
		When("${header.count > 100}", ProcessorFunc(func(ex *Exchange) error {
			ex.GetOut().SetBody("High count")
			return nil
		})).
		When("${header.count > 50}", ProcessorFunc(func(ex *Exchange) error {
			ex.GetOut().SetBody("Medium count")
			return nil
		})).
		When("${header.count > 0}", ProcessorFunc(func(ex *Exchange) error {
			ex.GetOut().SetBody("Low count")
			return nil
		})).
		Otherwise(ProcessorFunc(func(ex *Exchange) error {
			ex.GetOut().SetBody("No count")
			return nil
		}))

	tests := []struct {
		name     string
		count    int
		wantBody string
	}{
		{
			name:     "high count",
			count:    150,
			wantBody: "High count",
		},
		{
			name:     "medium count",
			count:    75,
			wantBody: "Medium count",
		},
		{
			name:     "low count",
			count:    25,
			wantBody: "Low count",
		},
		{
			name:     "zero count",
			count:    0,
			wantBody: "No count",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ex.GetIn().SetHeader("count", tt.count)
			ex.GetOut().SetBody(nil)

			err := choice.Process(ex)
			if err != nil {
				t.Fatalf("Process() error = %v", err)
			}

			got := ex.GetOut().GetBody()
			if got != tt.wantBody {
				t.Errorf("Process() body = %v, want %v", got, tt.wantBody)
			}
		})
	}
}

func TestChoiceProcessorWithoutOtherwise(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())

	// Create a choice processor without Otherwise
	choice := NewChoiceProcessor().
		When("${header.type == 'known'}", ProcessorFunc(func(ex *Exchange) error {
			ex.GetOut().SetBody("Known type")
			return nil
		}))

	// Test with known type
	ex.GetIn().SetHeader("type", "known")
	err := choice.Process(ex)
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if ex.GetOut().GetBody() != "Known type" {
		t.Errorf("Expected 'Known type', got %v", ex.GetOut().GetBody())
	}

	// Test with unknown type - should silently return without error
	ex.GetIn().SetHeader("type", "unknown")
	ex.GetOut().SetBody(nil)
	err = choice.Process(ex)
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	// Output body should be nil (no processor executed)
	if ex.GetOut().GetBody() != nil {
		t.Errorf("Expected nil body when no match, got %v", ex.GetOut().GetBody())
	}
}

func TestRouteBuilderChoicePattern(t *testing.T) {
	ctx := NewCamelContext()
	builder := ctx.CreateRouteBuilder()

	// Build a route with Choice pattern
	builder.
		SetID("choice-route").
		Choice().
		When("${header.action == 'create'}").
		SetBody("Creating new item").
		When("${header.action == 'update'}").
		SetBody("Updating item").
		When("${header.action == 'delete'}").
		SetBody("Deleting item").
		Otherwise().
		SetBody("Unknown action").
		EndChoice()

	route, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	if route == nil {
		t.Fatal("Expected non-nil route")
	}

	// Should have 1 processor (the Choice processor)
	if len(route.processors) != 1 {
		t.Errorf("Expected 1 processor, got %d", len(route.processors))
	}

	// Verify it's a ChoiceProcessor
	choice, ok := route.processors[0].(*ChoiceProcessor)
	if !ok {
		t.Fatalf("Expected *ChoiceProcessor, got %T", route.processors[0])
	}

	// Verify the When clauses
	if len(choice.whens) != 3 {
		t.Errorf("Expected 3 When clauses, got %d", len(choice.whens))
	}

	// Verify Otherwise is set
	if choice.otherwise == nil {
		t.Error("Expected Otherwise to be set")
	}
}

func TestChoiceProcessorWithSimpleExpressions(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())

	builder := ctx.CreateRouteBuilder()
	builder.
		Choice().
		When("${header.priority == 'high'}").
		SimpleSetBody("High priority: ${body}").
		When("${header.priority == 'low'}").
		SimpleSetBody("Low priority: ${body}").
		Otherwise().
		SimpleSetBody("Normal priority: ${body}").
		EndChoice()

	route, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		priority string
		body     string
		want     string
	}{
		{
			name:     "high priority",
			priority: "high",
			body:     "Task",
			want:     "High priority: Task",
		},
		{
			name:     "low priority",
			priority: "low",
			body:     "Task",
			want:     "Low priority: Task",
		},
		{
			name:     "normal priority",
			priority: "normal",
			body:     "Task",
			want:     "Normal priority: Task",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ex.GetIn().SetHeader("priority", tt.priority)
			ex.GetIn().SetBody(tt.body)

			// Reset output
			ex.GetOut().SetBody(nil)
			ex.GetOut().ClearHeaders()

			err = route.Process(ex)
			if err != nil {
				t.Fatalf("Process() error = %v", err)
			}

			got := ex.GetOut().GetBody()
			if got != tt.want {
				t.Errorf("Process() body = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestChoiceProcessorNestedConditions(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())

	// Test nested evaluation
	ex.GetIn().SetBody(map[string]interface{}{
		"status": "active",
	})
	ex.GetIn().SetHeader("level", 10)

	choice := NewChoiceProcessor().
		When("${body['status'] == 'active'}", ProcessorFunc(func(ex *Exchange) error {
			ex.GetOut().SetBody("Active and high level")
			return nil
		})).
		When("${body['status'] == 'pending'}", ProcessorFunc(func(ex *Exchange) error {
			ex.GetOut().SetBody("Pending status")
			return nil
		})).
		Otherwise(ProcessorFunc(func(ex *Exchange) error {
			ex.GetOut().SetBody("Default")
			return nil
		}))

	// Note: This test checks that bracket notation works in expressions

	ex.GetIn().SetBody(map[string]interface{}{
		"status": "active",
	})
	err := choice.Process(ex)
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	// Should match the first condition
	got := ex.GetOut().GetBody()
	if got != "Active and high level" {
		t.Errorf("Process() body = %v, expected 'Active and high level'", got)
	}

	// Negative-branch assertions: must not fall into first When clause
	ex.GetIn().SetBody(map[string]interface{}{
		"status": "pending",
	})
	ex.GetOut().SetBody(nil)
	require.NoError(t, choice.Process(ex))
	assert.Equal(t, "Pending status", ex.GetOut().GetBody())

	ex.GetIn().SetBody(map[string]interface{}{
		"status": "other",
	})
	ex.GetOut().SetBody(nil)
	require.NoError(t, choice.Process(ex))
	assert.Equal(t, "Default", ex.GetOut().GetBody())
}

func TestChoiceWithSetHeader(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())

	builder := ctx.CreateRouteBuilder()
	builder.
		Choice().
		When("${header.process == 'true'}").
		SetHeader("X-Processed", "yes").
		SetBody("Processed").
		Otherwise().
		SetHeader("X-Processed", "no").
		SetBody("Skipped").
		EndChoice()

	route, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}

	// Test processed case
	ex.GetIn().SetHeader("process", "true")
	err = route.Process(ex)
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	header, _ := ex.GetResponse().GetHeader("X-Processed")
	if header != "yes" {
		t.Errorf("Expected X-Processed header = 'yes', got %v", header)
	}
	if ex.GetOut().GetBody() != "Processed" {
		t.Errorf("Expected body = 'Processed', got %v", ex.GetOut().GetBody())
	}
}

func TestChoiceProcessorWithMultipleProcessors(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())

	// Test that we can chain multiple processors within a When clause
	// This verifies that the RouteBuilder pattern works correctly

	builder := ctx.CreateRouteBuilder()
	builder.
		Choice().
		When("${header.multiple == 'yes'}").
		ProcessFunc(func(ex *Exchange) error {
			ex.GetOut().SetHeader("Step1", "done")
			return nil
		}).
		ProcessFunc(func(ex *Exchange) error {
			ex.GetOut().SetHeader("Step2", "done")
			return nil
		}).
		ProcessFunc(func(ex *Exchange) error {
			ex.GetOut().SetBody("All steps completed")
			return nil
		}).
		Otherwise().
		SetBody("No steps run").
		EndChoice()

	route, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}

	ex.GetIn().SetHeader("multiple", "yes")
	err = route.Process(ex)
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	step1, exists1 := ex.GetResponse().GetHeader("Step1")
	step2, exists2 := ex.GetResponse().GetHeader("Step2")

	if !exists1 || step1 != "done" {
		t.Errorf("Expected Step1 header = 'done', got %v (exists=%v)", step1, exists1)
	}
	if !exists2 || step2 != "done" {
		t.Errorf("Expected Step2 header = 'done', got %v (exists=%v)", step2, exists2)
	}
	if ex.GetOut().GetBody() != "All steps completed" {
		t.Errorf("Expected body = 'All steps completed', got %v", ex.GetOut().GetBody())
	}
}

func TestChoiceEvaluateAsBoolWithDifferentTypes(t *testing.T) {
	ctx := NewCamelContext()

	tests := []struct {
		name       string
		expression string
		body       interface{}
		headers    map[string]interface{}
		want       bool
	}{
		{
			name:       "string true",
			expression: "${body}",
			body:       "true",
			want:       true,
		},
		{
			name:       "string false",
			expression: "${body}",
			body:       "false",
			want:       false,
		},
		{
			name:       "numeric non-zero",
			expression: "${body}",
			body:       42,
			want:       true,
		},
		{
			name:       "numeric zero",
			expression: "${body}",
			body:       0,
			want:       false,
		},
		{
			name:       "comparison true",
			expression: "${header.count == 5}",
			body:       "test",
			headers:    map[string]interface{}{"count": 5},
			want:       true,
		},
		{
			name:       "comparison false",
			expression: "${header.count == 3}",
			body:       "test",
			headers:    map[string]interface{}{"count": 5},
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ex := NewExchange(ctx.GetContext())
			ex.GetIn().SetBody(tt.body)
			for k, v := range tt.headers {
				ex.GetIn().SetHeader(k, v)
			}

			template, err := ParseSimpleTemplate(tt.expression)
			if err != nil {
				t.Fatalf("ParseSimpleTemplate() error = %v", err)
			}

			got, err := template.EvaluateAsBool(ex)
			if err != nil {
				t.Fatalf("EvaluateAsBool() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("EvaluateAsBool() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ==================== BODY PROPERTY ACCESS TESTS ====================

func TestBodyDotPropertyAccess(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetBody(map[string]interface{}{
		"name":  "TestName",
		"value": 123,
	})

	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{
			name:       "access name property",
			expression: "${body.name}",
			want:       "TestName",
		},
		{
			name:       "access value property",
			expression: "${body.value}",
			want:       "123",
		},
		{
			name:       "missing property returns nil",
			expression: "${body.nonexistent}",
			want:       "<nil>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			template, err := ParseSimpleTemplate(tt.expression)
			if err != nil {
				t.Fatalf("ParseSimpleTemplate() error = %v", err)
			}

			got, err := template.EvaluateAsString(ex)
			if err != nil {
				t.Fatalf("EvaluateAsString() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("EvaluateAsString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHeaderBracketAccess(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetHeader("X-Custom-Header", "custom-value")

	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{
			name:       "header with bracket notation",
			expression: "${header['X-Custom-Header']}",
			want:       "custom-value",
		},
		{
			name:       "header bracket with double quotes",
			expression: "${header[\"X-Custom-Header\"]}",
			want:       "custom-value",
		},
		{
			name:       "missing header returns nil",
			expression: "${header['missing']}",
			want:       "<nil>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			template, err := ParseSimpleTemplate(tt.expression)
			if err != nil {
				t.Fatalf("ParseSimpleTemplate() error = %v", err)
			}

			got, err := template.EvaluateAsString(ex)
			if err != nil {
				t.Fatalf("EvaluateAsString() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("EvaluateAsString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExchangePropertyBracketAccess(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.SetProperty("my-special-prop", "special-value")

	template, err := ParseSimpleTemplate("${exchangeProperty['my-special-prop']}")
	if err != nil {
		t.Fatalf("ParseSimpleTemplate() error = %v", err)
	}

	got, err := template.EvaluateAsString(ex)
	if err != nil {
		t.Fatalf("EvaluateAsString() error = %v", err)
	}
	if got != "special-value" {
		t.Errorf("EvaluateAsString() = %v, want special-value", got)
	}
}

func TestSimple_CompoundVariableComparisons(t *testing.T) {
	t.Run("body.role == 'admin'", func(t *testing.T) {
		tmpl, err := ParseSimpleTemplate("${body.role == 'admin'}")
		require.NoError(t, err)

		ex := NewExchange(context.Background())
		ex.GetIn().SetBody(map[string]any{"role": "guest"})
		b, err := tmpl.EvaluateAsBool(ex)
		require.NoError(t, err)
		assert.False(t, b)
		s, err := tmpl.EvaluateAsString(ex)
		require.NoError(t, err)
		assert.Equal(t, "false", s)

		ex.GetIn().SetBody(map[string]any{"role": "admin"})
		b, err = tmpl.EvaluateAsBool(ex)
		require.NoError(t, err)
		assert.True(t, b)
	})

	t.Run("body['status'] == 'active'", func(t *testing.T) {
		tmpl, err := ParseSimpleTemplate("${body['status'] == 'active'}")
		require.NoError(t, err)

		ex := NewExchange(context.Background())
		ex.GetIn().SetBody(map[string]any{"status": "pending"})
		b, err := tmpl.EvaluateAsBool(ex)
		require.NoError(t, err)
		assert.False(t, b)

		ex.GetIn().SetBody(map[string]any{"status": "active"})
		b, err = tmpl.EvaluateAsBool(ex)
		require.NoError(t, err)
		assert.True(t, b)
	})

	t.Run("body?.authorized == true", func(t *testing.T) {
		tmpl, err := ParseSimpleTemplate("${body?.authorized == true}")
		require.NoError(t, err)

		ex := NewExchange(context.Background())
		ex.GetIn().SetBody(map[string]any{"authorized": false})
		b, err := tmpl.EvaluateAsBool(ex)
		require.NoError(t, err)
		assert.False(t, b)

		ex.GetIn().SetBody(nil)
		b, err = tmpl.EvaluateAsBool(ex)
		require.NoError(t, err)
		assert.False(t, b)

		ex.GetIn().SetBody(map[string]any{"authorized": true})
		b, err = tmpl.EvaluateAsBool(ex)
		require.NoError(t, err)
		assert.True(t, b)
	})

	t.Run("header['count'] > 100", func(t *testing.T) {
		tmpl, err := ParseSimpleTemplate("${header['count'] > 100}")
		require.NoError(t, err)

		ex := NewExchange(context.Background())
		ex.GetIn().SetHeader("count", 1)
		b, err := tmpl.EvaluateAsBool(ex)
		require.NoError(t, err)
		assert.False(t, b)

		ex.GetIn().SetHeader("count", 101)
		b, err = tmpl.EvaluateAsBool(ex)
		require.NoError(t, err)
		assert.True(t, b)
	})

	t.Run("body.price + 10", func(t *testing.T) {
		tmpl, err := ParseSimpleTemplate("${body.price + 10}")
		require.NoError(t, err)

		ex := NewExchange(context.Background())
		ex.GetIn().SetBody(map[string]any{"price": 5})
		s, err := tmpl.EvaluateAsString(ex)
		require.NoError(t, err)
		assert.Equal(t, "15", s)
	})

	t.Run("body[0]?.name", func(t *testing.T) {
		tmpl, err := ParseSimpleTemplate("${body[0]?.name}")
		require.NoError(t, err)

		ex := NewExchange(context.Background())
		ex.GetIn().SetBody([]map[string]any{{"name": "Alice"}})
		s, err := tmpl.EvaluateAsString(ex)
		require.NoError(t, err)
		assert.Equal(t, "Alice", s)

		ex.GetIn().SetBody([]any{nil})
		s, err = tmpl.EvaluateAsString(ex)
		require.NoError(t, err)
		assert.Equal(t, "<nil>", s)
	})
}

func TestSimple_PrecedenceParensQuotesAndFunctions(t *testing.T) {
	t.Run("unary ! precedence before &&", func(t *testing.T) {
		tmpl, err := ParseSimpleTemplate("${!header.a && header.b}")
		require.NoError(t, err)

		ex := NewExchange(context.Background())
		ex.GetIn().SetHeader("a", false)
		ex.GetIn().SetHeader("b", false)
		b, err := tmpl.EvaluateAsBool(ex)
		require.NoError(t, err)
		assert.False(t, b)

		ex.GetIn().SetHeader("b", true)
		b, err = tmpl.EvaluateAsBool(ex)
		require.NoError(t, err)
		assert.True(t, b)

		ex.GetIn().SetHeader("a", true)
		b, err = tmpl.EvaluateAsBool(ex)
		require.NoError(t, err)
		assert.False(t, b)
	})

	t.Run("unary ! precedence before ternary", func(t *testing.T) {
		tmpl, err := ParseSimpleTemplate("${!header.a ? 'yes' : 'no'}")
		require.NoError(t, err)

		ex := NewExchange(context.Background())
		ex.GetIn().SetHeader("a", false)
		s, err := tmpl.EvaluateAsString(ex)
		require.NoError(t, err)
		assert.Equal(t, "yes", s)

		ex.GetIn().SetHeader("a", true)
		s, err = tmpl.EvaluateAsString(ex)
		require.NoError(t, err)
		assert.Equal(t, "no", s)
	})

	t.Run("balanced parens with ||", func(t *testing.T) {
		tmpl, err := ParseSimpleTemplate("${(header.a == 1) || (header.b == 2)}")
		require.NoError(t, err)

		ex := NewExchange(context.Background())
		ex.GetIn().SetHeader("a", 0)
		ex.GetIn().SetHeader("b", 2)
		b, err := tmpl.EvaluateAsBool(ex)
		require.NoError(t, err)
		assert.True(t, b)

		ex.GetIn().SetHeader("b", 3)
		b, err = tmpl.EvaluateAsBool(ex)
		require.NoError(t, err)
		assert.False(t, b)
	})

	t.Run("quoted literal comparison", func(t *testing.T) {
		tmpl, err := ParseSimpleTemplate("${'a' == 'b'}")
		require.NoError(t, err)

		b, err := tmpl.EvaluateAsBool(NewExchange(context.Background()))
		require.NoError(t, err)
		assert.False(t, b)
	})

	t.Run("multiple math calls", func(t *testing.T) {
		tmpl, err := ParseSimpleTemplate("${math(1+2) + math(3+4)}")
		require.NoError(t, err)

		s, err := tmpl.EvaluateAsString(NewExchange(context.Background()))
		require.NoError(t, err)
		assert.Equal(t, "10", s)
	})

	t.Run("date function inside comparison", func(t *testing.T) {
		tmpl, err := ParseSimpleTemplate("${date:now:2006 == '1999'}")
		require.NoError(t, err)

		b, err := tmpl.EvaluateAsBool(NewExchange(context.Background()))
		require.NoError(t, err)
		assert.False(t, b)
	})

	t.Run("unary minus in binary arithmetic", func(t *testing.T) {
		ex := NewExchange(context.Background())

		tmplMul, err := ParseSimpleTemplate("${10 * -5}")
		require.NoError(t, err)
		s, err := tmplMul.EvaluateAsString(ex)
		require.NoError(t, err)
		assert.Equal(t, "-50", s)

		tmplSub, err := ParseSimpleTemplate("${10 - -5}")
		require.NoError(t, err)
		s, err = tmplSub.EvaluateAsString(ex)
		require.NoError(t, err)
		assert.Equal(t, "15", s)
	})

	t.Run("jsonpath wildcard", func(t *testing.T) {
		tmpl, err := ParseSimpleTemplate("${jsonpath:$.book[*].author}")
		require.NoError(t, err)

		ex := NewExchange(context.Background())
		ex.GetIn().SetBody(`{"book":[{"author":"Alice"},{"author":"Bob"}]}`)
		s, err := tmpl.EvaluateAsString(ex)
		require.NoError(t, err)
		assert.Equal(t, "[Alice Bob]", s)
	})

	t.Run("operator keyword inside string literal", func(t *testing.T) {
		tmpl, err := ParseSimpleTemplate("${header.msg == 'status is ok'}")
		require.NoError(t, err)

		ex := NewExchange(context.Background())
		ex.GetIn().SetHeader("msg", "status is ok")
		b, err := tmpl.EvaluateAsBool(ex)
		require.NoError(t, err)
		assert.True(t, b)
	})

	t.Run("null-safe method call compared to nil in ternary", func(t *testing.T) {
		tmpl, err := ParseSimpleTemplate("${body?.trim() == nil ? 'Empty' : body}")
		require.NoError(t, err)

		ex := NewExchange(context.Background())
		ex.GetIn().SetBody(nil)
		s, err := tmpl.EvaluateAsString(ex)
		require.NoError(t, err)
		assert.Equal(t, "Empty", s)

		ex.GetIn().SetBody("hello")
		s, err = tmpl.EvaluateAsString(ex)
		require.NoError(t, err)
		assert.Equal(t, "hello", s)
	})
}

func TestSimple_ReflectionAndChainSafety(t *testing.T) {
	t.Run("unexported struct field does not panic", func(t *testing.T) {
		tmpl, err := ParseSimpleTemplate("${body.wall}")
		require.NoError(t, err)

		ex := NewExchange(context.Background())
		ex.GetIn().SetBody(time.Now())
		require.NotPanics(t, func() {
			s, evalErr := tmpl.EvaluateAsString(ex)
			require.NoError(t, evalErr)
			assert.Equal(t, "<nil>", s)
		})
	})

	t.Run("defined string map key does not panic", func(t *testing.T) {
		type Key string
		tmpl, err := ParseSimpleTemplate("${body['k']}")
		require.NoError(t, err)

		ex := NewExchange(context.Background())
		ex.GetIn().SetBody(map[Key]any{"k": "v"})
		require.NotPanics(t, func() {
			s, evalErr := tmpl.EvaluateAsString(ex)
			require.NoError(t, evalErr)
			assert.Equal(t, "v", s)
		})
	})

	t.Run("closing paren before opening paren returns syntax error without panic", func(t *testing.T) {
		tmpl, err := ParseSimpleTemplate("${body.a)b(}")
		require.NoError(t, err)

		ex := NewExchange(context.Background())
		ex.GetIn().SetBody("abc")
		require.NotPanics(t, func() {
			_, evalErr := tmpl.EvaluateAsString(ex)
			assert.Error(t, evalErr)
		})
	})

	t.Run("method args containing comma or closing paren", func(t *testing.T) {
		ex := NewExchange(context.Background())
		ex.GetIn().SetBody("a,b,c")

		tmplComma, err := ParseSimpleTemplate("${body.replace(',', ';')}")
		require.NoError(t, err)
		s, err := tmplComma.EvaluateAsString(ex)
		require.NoError(t, err)
		assert.Equal(t, "a;b;c", s)

		ex.GetIn().SetBody("a)b")
		tmplParen, err := ParseSimpleTemplate("${body.replace(')', 'x')}")
		require.NoError(t, err)
		s, err = tmplParen.EvaluateAsString(ex)
		require.NoError(t, err)
		assert.Equal(t, "axb", s)
	})

	t.Run("method chain on nil receiver preserves trailing operators", func(t *testing.T) {
		ex := NewExchange(context.Background())
		ex.GetIn().SetHeader("fallback", "ok")

		tmplNe, err := ParseSimpleTemplate("${header.missing.trim() != 'hello'}")
		require.NoError(t, err)
		b, err := tmplNe.EvaluateAsBool(ex)
		require.NoError(t, err)
		assert.True(t, b)

		tmplOr, err := ParseSimpleTemplate("${header.missing.trim() || header.fallback == 'ok'}")
		require.NoError(t, err)
		b, err = tmplOr.EvaluateAsBool(ex)
		require.NoError(t, err)
		assert.True(t, b)
	})

	t.Run("mixed null-safe and regular chains", func(t *testing.T) {
		tmpl, err := ParseSimpleTemplate("${body?.user.address?.city}")
		require.NoError(t, err)

		ex := NewExchange(context.Background())
		ex.GetIn().SetBody(map[string]any{
			"user": map[string]any{
				"address": map[string]any{
					"city": "Paris",
				},
			},
		})
		s, err := tmpl.EvaluateAsString(ex)
		require.NoError(t, err)
		assert.Equal(t, "Paris", s)

		ex.GetIn().SetBody(map[string]any{"user": nil})
		s, err = tmpl.EvaluateAsString(ex)
		require.NoError(t, err)
		assert.Equal(t, "<nil>", s)
	})

	t.Run("nil exported embedded struct pointer does not panic", func(t *testing.T) {
		type Embedded struct {
			InnerField string
		}
		type Outer struct {
			*Embedded
			EMBEDDED string
		}

		ex := NewExchange(context.Background())
		ex.GetIn().SetBody(Outer{Embedded: nil, EMBEDDED: "own"})

		for _, expr := range []string{"${body.InnerField}", "${body?.InnerField}", "${body.innerfield}"} {
			tmpl, err := ParseSimpleTemplate(expr)
			require.NoError(t, err, expr)
			require.NotPanics(t, func() {
				s, evalErr := tmpl.EvaluateAsString(ex)
				require.NoError(t, evalErr, expr)
				assert.Equal(t, "<nil>", s, expr)
			}, expr)
		}

		// Own non-anonymous field takes precedence over case-insensitive anonymous embed name at the same BFS depth.
		tmplOwn, err := ParseSimpleTemplate("${body.embedded}")
		require.NoError(t, err)
		s, err := tmplOwn.EvaluateAsString(ex)
		require.NoError(t, err)
		assert.Equal(t, "own", s)

		// Non-nil embedded struct resolves normally via exact and case-insensitive lookup.
		ex.GetIn().SetBody(Outer{Embedded: &Embedded{InnerField: "resolved"}, EMBEDDED: "own"})
		tmplNonNil, err := ParseSimpleTemplate("${body.InnerField}")
		require.NoError(t, err)
		s, err = tmplNonNil.EvaluateAsString(ex)
		require.NoError(t, err)
		assert.Equal(t, "resolved", s)

		tmplNonNilCI, err := ParseSimpleTemplate("${body.innerfield}")
		require.NoError(t, err)
		s, err = tmplNonNilCI.EvaluateAsString(ex)
		require.NoError(t, err)
		assert.Equal(t, "resolved", s)
	})
}

func TestSimple_NoTypeJugglingAndToBoolAndNullType(t *testing.T) {
	t.Run("exact string equality prevents type juggling", func(t *testing.T) {
		ex := NewExchange(context.Background())

		cases := []struct {
			expr string
			want bool
		}{
			{"${'0e12345' == '0e99999'}", false},
			{"${'0e12345' != '0e99999'}", true},
			{"${'0042' == '42'}", false},
			{"${'0042' != '42'}", true},
			{"${'NaN' == 'NaN'}", true},
			{"${'Inf' == 'Infinity'}", false},
		}

		for _, tc := range cases {
			tmpl, err := ParseSimpleTemplate(tc.expr)
			require.NoError(t, err, tc.expr)
			got, err := tmpl.EvaluateAsBool(ex)
			require.NoError(t, err, tc.expr)
			assert.Equal(t, tc.want, got, tc.expr)
		}
	})

	t.Run("int64 and uint64 precision preserved in comparisons", func(t *testing.T) {
		ex := NewExchange(context.Background())
		ex.GetIn().SetHeader("id1", int64(9007199254740993))
		ex.GetIn().SetHeader("id2", int64(9007199254740992))
		ex.GetIn().SetHeader("u1", uint64(18446744073709551615))
		ex.GetIn().SetHeader("u2", uint64(18446744073709551614))
		ex.GetIn().SetHeader("neg", int64(-1))
		ex.GetIn().SetHeader("f2", float64(9007199254740992))
		ex.GetIn().SetHeader("nanVal", math.NaN())

		cases := []struct {
			expr string
			want bool
		}{
			{"${header.id1 == header.id2}", false},
			{"${header.id1 != header.id2}", true},
			{"${header.id1 > header.id2}", true},
			{"${header.u1 == header.u2}", false},
			{"${header.u1 > header.u2}", true},
			{"${header.neg < header.u1}", true},
			{"${header.id1 == header.f2}", false},
			{"${header.id1 > header.f2}", true},
			{"${header.nanVal == 'NaN'}", false},
			{"${header.nanVal == 0}", false},
			{"${'NaN' == 0}", false},
		}

		for _, tc := range cases {
			tmpl, err := ParseSimpleTemplate(tc.expr)
			require.NoError(t, err, tc.expr)
			got, err := tmpl.EvaluateAsBool(ex)
			require.NoError(t, err, tc.expr)
			assert.Equal(t, tc.want, got, tc.expr)
		}

		// Direct compareValues edge cases: Inf, MinInt64 vs float64, unknown operator
		ok, err := compareValues(int64(math.MinInt64), "==", float64(-9223372036854775808.0))
		require.NoError(t, err)
		assert.True(t, ok)
		ok, err = compareValues(int64(1), "<", math.Inf(1))
		require.NoError(t, err)
		assert.True(t, ok)
		ok, err = compareValues(int64(1), ">", math.Inf(-1))
		require.NoError(t, err)
		assert.True(t, ok)
		ok, err = compareValues("9007199254740993", ">", "9007199254740992")
		require.NoError(t, err)
		assert.True(t, ok)
		_, err = compareValues(1, "~=", 2)
		require.Error(t, err)
	})

	t.Run("toBool handles all numeric kinds and NaN", func(t *testing.T) {
		type customInt int64
		type customUint uint32
		type customFloat float64

		assert.False(t, toBool(int8(0)))
		assert.False(t, toBool(int16(0)))
		assert.False(t, toBool(int32(0)))
		assert.False(t, toBool(int64(0)))
		assert.False(t, toBool(uint(0)))
		assert.False(t, toBool(uint8(0)))
		assert.False(t, toBool(uint16(0)))
		assert.False(t, toBool(uint32(0)))
		assert.False(t, toBool(uint64(0)))
		assert.False(t, toBool(uintptr(0)))
		assert.False(t, toBool(float32(0)))
		assert.False(t, toBool(float32(math.NaN())))
		assert.False(t, toBool(math.NaN()))
		assert.False(t, toBool(customInt(0)))
		assert.False(t, toBool(customUint(0)))
		assert.False(t, toBool(customFloat(math.NaN())))

		assert.True(t, toBool(int64(1)))
		assert.True(t, toBool(uint64(42)))
		assert.True(t, toBool(float32(1.5)))
		assert.True(t, toBool(customInt(-3)))

		ex := NewExchange(context.Background())
		ex.GetIn().SetHeader("zero64", int64(0))
		ex.GetIn().SetHeader("nanVal", math.NaN())

		for _, expr := range []string{"${!header.zero64}", "${!header.nanVal}"} {
			tmpl, err := ParseSimpleTemplate(expr)
			require.NoError(t, err, expr)
			got, err := tmpl.EvaluateAsBool(ex)
			require.NoError(t, err, expr)
			assert.True(t, got, expr)
		}
	})

	t.Run("is operator supports case-insensitive null and nil", func(t *testing.T) {
		ex := NewExchange(context.Background())
		for _, expr := range []string{
			"${header.missing is 'null'}",
			"${header.missing is 'Null'}",
			"${header.missing is 'NULL'}",
			"${header.missing is 'nil'}",
			"${header.missing is 'NIL'}",
		} {
			tmpl, err := ParseSimpleTemplate(expr)
			require.NoError(t, err, expr)
			got, err := tmpl.EvaluateAsBool(ex)
			require.NoError(t, err, expr)
			assert.True(t, got, expr)
		}
	})
}

func TestSimple_UTF8StringsAndQuotedBraces(t *testing.T) {
	t.Run("capitalize is UTF-8 rune-safe", func(t *testing.T) {
		ex := NewExchange(context.Background())
		ex.GetIn().SetBody("école")

		tmpl, err := ParseSimpleTemplate("${body.capitalize()}")
		require.NoError(t, err)
		got, err := tmpl.EvaluateAsString(ex)
		require.NoError(t, err)
		assert.Equal(t, "École", got)
		assert.True(t, utf8.ValidString(got))

		ex.GetIn().SetBody("ÉCOLE")
		got, err = tmpl.EvaluateAsString(ex)
		require.NoError(t, err)
		assert.Equal(t, "École", got)
	})

	t.Run("padstart and padend are UTF-8 rune-safe", func(t *testing.T) {
		ex := NewExchange(context.Background())
		ex.GetIn().SetBody("a")

		tmplStart, err := ParseSimpleTemplate("${body.padstart(4, 'é')}")
		require.NoError(t, err)
		gotStart, err := tmplStart.EvaluateAsString(ex)
		require.NoError(t, err)
		assert.True(t, utf8.ValidString(gotStart))
		assert.Equal(t, "éééa", gotStart)
		assert.Equal(t, 4, utf8.RuneCountInString(gotStart))

		tmplEnd, err := ParseSimpleTemplate("${body.padend(4, 'é')}")
		require.NoError(t, err)
		gotEnd, err := tmplEnd.EvaluateAsString(ex)
		require.NoError(t, err)
		assert.True(t, utf8.ValidString(gotEnd))
		assert.Equal(t, "aééé", gotEnd)
		assert.Equal(t, 4, utf8.RuneCountInString(gotEnd))

		// Multi-rune pad string and multi-byte body
		ex.GetIn().SetBody("é")
		tmplMixed, err := ParseSimpleTemplate("${body.padstart(4, 'éx')}")
		require.NoError(t, err)
		gotMixed, err := tmplMixed.EvaluateAsString(ex)
		require.NoError(t, err)
		assert.True(t, utf8.ValidString(gotMixed))
		assert.Equal(t, "éxéé", gotMixed)

		// Negative and NaN target length return original string cleanly
		gotNeg, err := applyStringFunction("padstart", "é", []interface{}{-5, "0"})
		require.NoError(t, err)
		assert.Equal(t, "é", gotNeg)
		gotNaN, err := applyStringFunction("padend", "é", []interface{}{math.NaN(), "0"})
		require.NoError(t, err)
		assert.Equal(t, "é", gotNaN)
	})

	t.Run("quote-aware placeholder scanner handles braces in quoted strings", func(t *testing.T) {
		ex := NewExchange(context.Background())
		ex.GetIn().SetHeader("code", "123")
		ex.GetIn().SetBody(`{"ok":true}`)

		tmplRegex, err := ParseSimpleTemplate(`${header.code regex '^\d{3}$'}`)
		require.NoError(t, err)
		matched, err := tmplRegex.EvaluateAsBool(ex)
		require.NoError(t, err)
		assert.True(t, matched)

		ex.GetIn().SetHeader("code", "12a")
		matched, err = tmplRegex.EvaluateAsBool(ex)
		require.NoError(t, err)
		assert.False(t, matched)

		tmplContains, err := ParseSimpleTemplate(`${body contains '{"ok":true}'}`)
		require.NoError(t, err)
		containsMatch, err := tmplContains.EvaluateAsBool(ex)
		require.NoError(t, err)
		assert.True(t, containsMatch)

		// Mixed template with literal text, escaped quotes, double quotes, and multiple placeholders with braces
		ex.GetIn().SetHeader("code", "404")
		tmplMulti, err := ParseSimpleTemplate(`prefix ${header.code regex '^\d{3}$'} mid ${body contains "{\"ok\":true}"} suffix`)
		require.NoError(t, err)
		multiOut, err := tmplMulti.EvaluateAsString(ex)
		require.NoError(t, err)
		assert.Equal(t, "prefix true mid true suffix", multiOut)

		// Escaped single quote inside single-quoted string containing '}'
		ex.GetIn().SetBody("it's } ok")
		tmplEsc, err := ParseSimpleTemplate(`${body contains 'it\'s } ok'}`)
		require.NoError(t, err)
		escMatch, err := tmplEsc.EvaluateAsBool(ex)
		require.NoError(t, err)
		assert.True(t, escMatch)

		// Malformed templates still fail cleanly
		for _, bad := range []string{"direct:${header.x", "direct:${}", "direct:${   }", "${body == 'unclosed}"} {
			_, badErr := ParseSimpleTemplate(bad)
			require.Error(t, badErr, bad)
		}
	})

	t.Run("regex operator uses bounded cache", func(t *testing.T) {
		savedCap := MaxSimpleTemplateCacheEntries
		t.Cleanup(func() {
			MaxSimpleTemplateCacheEntries = savedCap
		})

		base := simpleRegexCacheSize.Load()
		MaxSimpleTemplateCacheEntries = int(base + 3)

		ex := NewExchange(context.Background())
		ex.GetIn().SetBody("abc123")

		for i := 0; i < 6; i++ {
			expr := fmt.Sprintf(`${body regex '^abc\d{%d}$'}`, i+1)
			tmpl, err := ParseSimpleTemplate(expr)
			require.NoError(t, err, expr)
			got, err := tmpl.EvaluateAsBool(ex)
			require.NoError(t, err, expr)
			assert.Equal(t, i+1 == 3, got, expr)
		}
		assert.Equal(t, base+3, simpleRegexCacheSize.Load())

		// Invalid regex returns an error without inflating the cache
		badTmpl, err := ParseSimpleTemplate(`${body regex '[invalid'}`)
		require.NoError(t, err)
		_, err = badTmpl.EvaluateAsBool(ex)
		require.Error(t, err)
		assert.Equal(t, base+3, simpleRegexCacheSize.Load())
	})
}
