package gocamel

import (
	"testing"
)

// ==================== PHASE 3 TESTS: String Operations ====================

func TestStringContainsOperator(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetBody("Hello World")
	ex.GetIn().SetHeader("message", "The quick brown fox")

	tests := []struct {
		name       string
		expression string
		want       string // "true" or "false"
	}{
		{
			name:       "body contains text",
			expression: "${body contains 'World'}",
			want:       "true",
		},
		{
			name:       "body contains text - not found",
			expression: "${body contains 'xyz'}",
			want:       "false",
		},
		{
			name:       "header contains text",
			expression: "${header.message contains 'brown'}",
			want:       "true",
		},
		{
			name:       "header contains text - case sensitive",
			expression: "${header.message contains 'BROWN'}",
			want:       "false",
		},
		{
			name:       "empty string contains",
			expression: "${body contains ''}",
			want:       "true",
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

func TestStringStartsWithOperator(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetBody("Hello World")
	ex.GetIn().SetHeader("prefix", "http://")

	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{
			name:       "body startsWith",
			expression: "${body startsWith 'Hello'}",
			want:       "true",
		},
		{
			name:       "body startsWith - not matching",
			expression: "${body startsWith 'World'}",
			want:       "false",
		},
		{
			name:       "header startsWith",
			expression: "${header.prefix startsWith 'http'}",
			want:       "true",
		},
		{
			name:       "empty startsWith",
			expression: "${body startsWith ''}",
			want:       "true",
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

func TestStringEndsWithOperator(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetBody("Hello World")
	ex.GetIn().SetHeader("path", "/api/users.json")

	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{
			name:       "body endsWith",
			expression: "${body endsWith 'World'}",
			want:       "true",
		},
		{
			name:       "body endsWith - not matching",
			expression: "${body endsWith 'Hello'}",
			want:       "false",
		},
		{
			name:       "header endsWith",
			expression: "${header.path endsWith '.json'}",
			want:       "true",
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

func TestStringRegexOperator(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetBody("Order 12345")
	ex.GetIn().SetHeader("email", "user@example.com")

	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{
			name:       "body regex matches digits",
			expression: "${body regex '\\d+'}",
			want:       "true",
		},
		{
			name:       "body regex matches pattern",
			expression: "${body regex 'Order\\s+\\d+'}",
			want:       "true",
		},
		{
			name:       "body regex - no match",
			expression: "${body regex '^World'}",
			want:       "false",
		},
		{
			name:       "header regex validates email",
			expression: "${header.email regex '^.+@.+\\..+$'}",
			want:       "true",
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

// ==================== PHASE 3 TESTS: Logical Operators ====================

func TestLogicalAndOperator(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetHeader("count", 10)
	ex.GetIn().SetHeader("type", "gold")

	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{
			name:       "AND with both true",
			expression: "${header.count > 5 && header.type == 'gold'}",
			want:       "true",
		},
		{
			name:       "AND with first false",
			expression: "${header.count > 20 && header.type == 'gold'}",
			want:       "false",
		},
		{
			name:       "AND with second false",
			expression: "${header.count > 5 && header.type == 'silver'}",
			want:       "false",
		},
		{
			name:       "AND with both false",
			expression: "${header.count > 20 && header.type == 'silver'}",
			want:       "false",
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

func TestLogicalOrOperator(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetHeader("count", 15)
	ex.GetIn().SetHeader("type", "gold")

	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{
			name:       "OR with both true",
			expression: "${header.count > 10 || header.type == 'gold'}",
			want:       "true",
		},
		{
			name:       "OR with first true",
			expression: "${header.count > 10 || header.type == 'silver'}",
			want:       "true",
		},
		{
			name:       "OR with second true",
			expression: "${header.count > 20 || header.type == 'gold'}",
			want:       "true",
		},
		{
			name:       "OR with both false",
			expression: "${header.count > 20 || header.type == 'silver'}",
			want:       "false",
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

func TestLogicalNotOperator(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.SetProperty("processed", true)
	ex.SetProperty("skip", false)

	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{
			name:       "NOT on true",
			expression: "${!exchangeProperty.processed}",
			want:       "false",
		},
		{
			name:       "NOT on false",
			expression: "${!exchangeProperty.skip}",
			want:       "true",
		},
		{
			name:       "NOT on nil header",
			expression: "${!header.Missing}",
			want:       "true",
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

func TestLogicalOperatorPrecedence(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetHeader("a", true)
	ex.GetIn().SetHeader("b", false)
	ex.GetIn().SetHeader("c", true)

	tests := []struct {
		name       string
		expression string
		want       string
		desc       string
	}{
		{
			name:       "AND has higher precedence than OR",
			expression: "${header.b || header.a && header.c}",
			want:       "true",
			desc:       "b || (a && c) = false || true = true",
		},
		{
			name:       "parentheses override precedence",
			expression: "${(header.b || header.a) && header.c}",
			want:       "true",
			desc:       "(b || a) && c = true && true = true",
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
				t.Errorf("EvaluateAsString() = %v, want %v (%s)", got, tt.want, tt.desc)
			}
		})
	}
}

// ==================== PHASE 3 TESTS: Ternary Operator ====================

func TestTernaryOperator(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetHeader("type", "gold")
	ex.GetIn().SetHeader("code", "VIP")

	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{
			name:       "ternary true branch",
			expression: "${header.type == 'gold' ? 'Premium' : 'Standard'}",
			want:       "Premium",
		},
		{
			name:       "ternary false branch",
			expression: "${header.type == 'silver' ? 'Premium' : 'Standard'}",
			want:       "Standard",
		},
		{
			name:       "ternary with variables",
			expression: "${header.code == 'VIP' ? header.code : 'REGULAR'}",
			want:       "VIP",
		},
		{
			name:       "ternary with comparison",
			expression: "${header.count > 5 ? 'High' : 'Low'}",
			want:       "Low",
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

// ==================== PHASE 4 TESTS: String Functions ====================

func TestStringFunctions(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetBody("  Hello World  ")
	ex.GetIn().SetHeader("name", "john doe")

	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{
			name:       "trim function",
			expression: "${body.trim()}",
			want:       "Hello World",
		},
		{
			name:       "uppercase function",
			expression: "${header.name.uppercase()}",
			want:       "JOHN DOE",
		},
		{
			name:       "upper alias",
			expression: "${header.name.upper()}",
			want:       "JOHN DOE",
		},
		{
			name:       "lowercase function",
			expression: "${header.name.lowercase()}",
			want:       "john doe",
		},
		{
			name:       "lower alias",
			expression: "${header.name.lower()}",
			want:       "john doe",
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

func TestStringLengthFunctions(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetBody("Hello")
	ex.GetIn().SetHeader("items", []string{"a", "b", "c"})

	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{
			name:       "size on string",
			expression: "${body.size()}",
			want:       "5",
		},
		{
			name:       "length on string",
			expression: "${body.length()}",
			want:       "5",
		},
		{
			name:       "size on slice",
			expression: "${header.items.size()}",
			want:       "3",
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

func TestStringSubstringFunction(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetBody("Hello World")
	ex.GetIn().SetHeader("value", "ABCDEFGHI")

	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{
			name:       "substring with start",
			expression: "${body.substring(6)}",
			want:       "World",
		},
		{
			name:       "substring with start and end",
			expression: "${body.substring(0,5)}",
			want:       "Hello",
		},
		{
			name:       "substring on header",
			expression: "${header.value.substring(3,6)}",
			want:       "DEF",
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

func TestStringReplaceFunction(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetBody("Hello World World")
	ex.GetIn().SetHeader("template", "Hello {{name}}")

	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{
			name:       "replace all occurrences",
			expression: "${body.replace('World', 'Universe')}",
			want:       "Hello Universe Universe",
		},
		{
			name:       "replace in header",
			expression: "${header.template.replace('name', 'John')}",
			want:       "Hello {{John}}",
		},
		{
			name:       "replace with no match",
			expression: "${body.replace('xyz', 'abc')}",
			want:       "Hello World World",
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

func TestStringFunctionChaining(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetBody("  Hello World  ")
	ex.GetIn().SetHeader("text", "  TeSt  ")

	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{
			name:       "chain trim and uppercase",
			expression: "${body.trim().uppercase()}",
			want:       "HELLO WORLD",
		},
		{
			name:       "chain trim, uppercase and size",
			expression: "${body.trim().uppercase().size()}",
			want:       "11", // "HELLO WORLD" has 11 chars
		},
		{
			name:       "complex chain on header",
			expression: "${header.text.trim().lowercase().substring(0,2)}",
			want:       "te",
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

func TestStringSplitFunction(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetBody("apple,banana,orange")
	ex.GetIn().SetHeader("csv", "one;two;three;four")
	ex.GetIn().SetHeader("text", "foo bar baz")

	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{
			name:       "split with default delimiter",
			expression: "${body.split()}",
			want:       "[apple banana orange]",
		},
		{
			name:       "split with custom delimiter",
			expression: "${header.csv.split(';')}",
			want:       "[one two three four]",
		},
		{
			name:       "split with limit",
			expression: "${header.csv.split(';', 2)}",
			want:       "[one two]",
		},
		{
			name:       "split with space delimiter",
			expression: "${header.text.split(' ')}",
			want:       "[foo bar baz]",
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

func TestStringNormalizeWhitespaceFunction(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetBody("Hello\t\t\n\r\n  World")
	ex.GetIn().SetHeader("messy", "  This    is\ta\t\nmessy   text  ")

	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{
			name:       "normalize with tabs and newlines",
			expression: "${body.normalizeWhitespace()}",
			want:       "Hello World",
		},
		{
			name:       "normalize messy header",
			expression: "${header.messy.normalizeWhitespace()}",
			want:       "This is a messy text",
		},
		{
			name:       "normalize already clean text",
			expression: "${body.normalizeWhitespace()}",
			want:       "Hello World",
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

func TestStringFunctionNullSafe(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetBody(nil)

	// When body is nil, functions should return nil
	template, err := ParseSimpleTemplate("${body?.trim()}")
	if err != nil {
		t.Fatalf("ParseSimpleTemplate() error = %v", err)
	}

	got, err := template.EvaluateAsString(ex)
	if err != nil {
		t.Fatalf("EvaluateAsString() error = %v", err)
	}
	if got != "<nil>" {
		t.Errorf("EvaluateAsString() = %v, want <nil>", got)
	}
}

// ==================== PHASE 4 TESTS: Math Operations ====================

func TestMathOperations(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetHeader("count", 10)

	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{
			name:       "addition",
			expression: "${header.count + 10}",
			want:       "20",
		},
		{
			name:       "subtraction",
			expression: "${header.count - 5}",
			want:       "5",
		},
		{
			name:       "multiplication",
			expression: "${header.count * 2}",
			want:       "20",
		},
		{
			name:       "division",
			expression: "${header.count / 2}",
			want:       "5",
		},
		{
			name:       "modulo",
			expression: "${header.count % 3}",
			want:       "1",
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

func TestMathOperationPrecedence(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetHeader("a", 2)
	ex.GetIn().SetHeader("b", 3)
	ex.GetIn().SetHeader("c", 4)

	tests := []struct {
		name       string
		expression string
		want       string
		desc       string
	}{
		{
			name:       "multiplication before addition",
			expression: "${header.a + header.b * header.c}",
			want:       "14",
			desc:       "2 + 3 * 4 = 2 + 12 = 14",
		},
		{
			name:       "division before subtraction",
			expression: "${header.c / header.a - 1}",
			want:       "1",
			desc:       "4 / 2 - 1 = 2 - 1 = 1",
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
				t.Errorf("EvaluateAsString() = %v, want %v (%s)", got, tt.want, tt.desc)
			}
		})
	}
}

// ==================== PHASE 4 TESTS: Type/Range Operations ====================

func TestInOperator(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetHeader("type", "gold")
	ex.GetIn().SetHeader("status", "pending")

	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{
			name:       "in list - match",
			expression: "${header.type in 'gold,silver,bronze'}",
			want:       "true",
		},
		{
			name:       "in list - no match",
			expression: "${header.type in 'silver,bronze'}",
			want:       "false",
		},
		{
			name:       "in list with spaces",
			expression: "${header.status in 'pending, processing, completed'}",
			want:       "true",
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

func TestRangeOperator(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetHeader("code", 150)

	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{
			name:       "in range - match",
			expression: "${header.code range 100..199}",
			want:       "true",
		},
		{
			name:       "in range - boundary",
			expression: "${header.code range 150..200}",
			want:       "true",
		},
		{
			name:       "in range - boundary start",
			expression: "${header.code range 100..150}",
			want:       "true",
		},
		{
			name:       "in range - no match",
			expression: "${header.code range 200..299}",
			want:       "false",
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

func TestIsOperator(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetBody("Hello")
	ex.GetIn().SetHeader("data", map[string]interface{}{"key": "value"})
	ex.GetIn().SetHeader("numbers", []int{1, 2, 3})
	ex.GetIn().SetHeader("count", 42)
	ex.GetIn().SetHeader("flag", true)

	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{
			name:       "is String",
			expression: "${body is 'String'}",
			want:       "true",
		},
		{
			name:       "is Map",
			expression: "${header.data is 'Map'}",
			want:       "true",
		},
		{
			name:       "is String on map - false",
			expression: "${header.data is 'String'}",
			want:       "false",
		},
		{
			name:       "is Int",
			expression: "${header.count is 'Int'}",
			want:       "true",
		},
		{
			name:       "is Bool",
			expression: "${header.flag is 'Bool'}",
			want:       "true",
		},
		{
			name:       "is lowercase",
			expression: "${body is 'string'}",
			want:       "true",
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

// ==================== INTEGRATION TESTS ====================

func TestComplexExpressionWithAllFeatures(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetBody("URGENT: Process this order")
	ex.GetIn().SetHeader("priority", "high")
	ex.GetIn().SetHeader("amount", 150)
	ex.GetIn().SetHeader("status", "new")

	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{
			name:       "contains with comparison",
			expression: "${body contains 'URGENT' && header.amount > 100}",
			want:       "true",
		},
		{
			name:       "ternary with contains",
			expression: "${body contains 'URGENT' ? 'Fast' : 'Normal'}",
			want:       "Fast",
		},
		{
			name:       "logical with all operators",
			expression: "${header.priority == 'high' && header.amount > 100 || header.status == 'urgent'}",
			want:       "true",
		},
		{
			name:       "function in ternary",
			expression: "${header.amount > 100 ? body.substring(0,6) : body}",
			want:       "URGENT",
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

func TestChoiceWithNewOperators(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())

	choice := NewChoiceProcessor().
		When("${header.priority == 'high' && header.amount > 100}", ProcessorFunc(func(ex *Exchange) error {
			ex.GetOut().SetBody("High priority transaction")
			return nil
		})).
		When("${body contains 'URGENT' || body startsWith 'CRITICAL'}", ProcessorFunc(func(ex *Exchange) error {
			ex.GetOut().SetBody("Urgent message")
			return nil
		})).
		When("${header.category in 'A,B,C'}", ProcessorFunc(func(ex *Exchange) error {
			ex.GetOut().SetBody("Category A, B, or C")
			return nil
		})).
		Otherwise(ProcessorFunc(func(ex *Exchange) error {
			ex.GetOut().SetBody("Default")
			return nil
		}))

	tests := []struct {
		name     string
		body     string
		headers  map[string]interface{}
		wantBody string
	}{
		{
			name:     "high priority and high amount",
			body:     "test",
			headers:  map[string]interface{}{"priority": "high", "amount": 150},
			wantBody: "High priority transaction",
		},
		{
			name:     "urgent in body",
			body:     "This is URGENT",
			headers:  map[string]interface{}{},
			wantBody: "Urgent message",
		},
		{
			name:     "critical at start",
			body:     "CRITICAL: System down",
			headers:  map[string]interface{}{},
			wantBody: "Urgent message",
		},
		{
			name:     "category A",
			body:     "test",
			headers:  map[string]interface{}{"category": "A"},
			wantBody: "Category A, B, or C",
		},
		{
			name:     "default case",
			body:     "normal",
			headers:  map[string]interface{}{"category": "Z"},
			wantBody: "Default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ex.GetIn().SetBody(tt.body)
			for k, v := range tt.headers {
				ex.GetIn().SetHeader(k, v)
			}
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

func TestSimpleSetBodyWithFunctions(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetBody("  hello world  ")
	ex.GetIn().SetHeader("name", "john doe")
	ex.GetIn().SetHeader("amount", 100)

	builder := ctx.CreateRouteBuilder()
	builder.
		SimpleSetBody("${body.trim().uppercase()}").
		SetID("test-route")

	route := builder.Build()
	if route == nil {
		t.Fatal("Expected non-nil route")
	}

	err := route.Process(ex)
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	got := ex.GetOut().GetBody()
	want := "HELLO WORLD"
	if got != want {
		t.Errorf("GetOut().GetBody() = %v, want %v", got, want)
	}
}

func TestSimpleSetHeaderWithTernary(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetHeader("amount", 150)

	builder := ctx.CreateRouteBuilder()
	builder.
		SimpleSetHeader("X-Priority", "${header.amount > 100 ? 'High' : 'Low'}").
		SetID("test-route")

	route := builder.Build()
	if route == nil {
		t.Fatal("Expected non-nil route")
	}

	err := route.Process(ex)
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	got, _ := ex.GetOut().GetHeader("X-Priority")
	want := "High"
	if got != want {
		t.Errorf("X-Priority header = %v, want %v", got, want)
	}
}

func TestEdgeCases(t *testing.T) {
	ctx := NewCamelContext()
	ex := NewExchange(ctx.GetContext())
	ex.GetIn().SetBody("")
	ex.GetIn().SetHeader("nil-value", nil)

	tests := []struct {
		name       string
		expression string
		wantCheck  func(string) bool
		wantErr    bool
	}{
		{
			name:       "empty body contains empty string",
			expression: "${body contains ''}",
			wantCheck:  func(s string) bool { return s == "true" },
			wantErr:    false,
		},
		{
			name:       "nil value type check",
			expression: "${header.nil-value is 'nil'}",
			wantCheck:  func(s string) bool { return s == "true" },
			wantErr:    false,
		},
		{
			name:       "chained operations on nil",
			expression: "${header.missing?.trim()}",
			wantCheck:  func(s string) bool { return s == "<nil>" },
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
			if !tt.wantCheck(got) {
				t.Errorf("EvaluateAsString() = %v, failed check", got)
			}
		})
	}
}
