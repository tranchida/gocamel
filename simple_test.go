package gocamel

import (
	"strings"
	"testing"
	"time"
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
		{
			name:       "date now function with custom format",
			expression: "${date:now:2006-01-02}",
			wantCheck: func(s string) bool {
				return len(s) == 10 && strings.Count(s, "-") == 2
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

	got, exists := ex.GetOut().GetHeader("X-Processed")
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

	route := builder.Build()

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

	route := builder.Build()

	if route == nil {
		t.Fatal("Expected non-nil route")
	}

	if len(route.processors) != 1 {
		t.Errorf("Expected 1 processor, got %d", len(route.processors))
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
		"name":    "John",
		"email":   "john@example.com",
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
		"key with spaces": "value with spaces",
		"special-chars!@#": "special value",
		"":                  "empty key value",
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
				"name": "Alice",
				"email": "alice@example.com",
			},
			map[string]interface{}{
				"name": "Bob",
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
		name      string
		count     int
		wantBody  string
	}{
		{
			name:      "high count",
			count:     150,
			wantBody:  "High count",
		},
		{
			name:      "medium count",
			count:     75,
			wantBody:  "Medium count",
		},
		{
			name:      "low count",
			count:     25,
			wantBody:  "Low count",
		},
		{
			name:      "zero count",
			count:     0,
			wantBody:  "No count",
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

	route := builder.Build()
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

	route := builder.Build()

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
			ex.GetOut().SetHeaders(nil)

			err := route.Process(ex)
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

	route := builder.Build()

	// Test processed case
	ex.GetIn().SetHeader("process", "true")
	err := route.Process(ex)
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	header, _ := ex.GetOut().GetHeader("X-Processed")
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

	route := builder.Build()

	ex.GetIn().SetHeader("multiple", "yes")
	err := route.Process(ex)
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	step1, exists1 := ex.GetOut().GetHeader("Step1")
	step2, exists2 := ex.GetOut().GetHeader("Step2")

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
		headers   map[string]interface{}
		want      bool
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
