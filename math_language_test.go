package gocamel

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSimpleLanguage_MathFunctions(t *testing.T) {
	ex := NewExchange(context.Background())
	ex.In.SetBody(10)
	ex.In.SetHeader("val", 5)

	tests := []struct {
		expression string
		expected   string
	}{
		{"${math(body * 2)}", "20"},
		{"${math(body + 5)}", "15"},
		{"${math(header.val + 10)}", "15"},
		{"${math(body / 2)}", "5"},
		{"${math(body % 3)}", "1"},
		{"${math(body - header.val)}", "5"},
		{"${math(round(5.6))}", "6"},
		{"${math(floor(5.6))}", "5"},
		{"${math(ceil(5.1))}", "6"},
		{"${math(abs(-10))}", "10"},
		{"${math(sqrt(16))}", "4"},
	}

	for _, tt := range tests {
		t.Run(tt.expression, func(t *testing.T) {
			result, err := ParseSimpleTemplate(tt.expression)
			require.NoError(t, err)

			val, err := result.EvaluateAsString(ex)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, val)
		})
	}
}

func TestSimpleLanguage_NumericFunctionsStandalone(t *testing.T) {
	ex := NewExchange(context.Background())
	ex.In.SetBody(5.6)

	tests := []struct {
		expression string
		expected   string
	}{
		{"${round(body)}", "6"},
		{"${floor(body)}", "5"},
		{"${ceil(body)}", "6"},
		{"${abs(-10.5)}", "10.5"},
	}

	for _, tt := range tests {
		t.Run(tt.expression, func(t *testing.T) {
			result, err := ParseSimpleTemplate(tt.expression)
			require.NoError(t, err)

			val, err := result.EvaluateAsString(ex)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, val)
		})
	}
}

func TestSimpleLanguage_MathComplex(t *testing.T) {
	ex := NewExchange(context.Background())
	ex.In.SetBody(10)

	// Test nesting and parentheses
	// Note: Our current math engine is simple recursion, so we need to be careful with parentheses.
	// ${math((body + 2) * 3)} should evaluate:
	// mathMatch[1] = (body + 2) * 3
	// evaluateVariable("(body + 2) * 3") -> mathMultiplyRegex matches
	// left = (body + 2), right = 3
	// evaluateVariable("(body + 2)") -> body + 2
	// ... result 12 * 3 = 36

	tests := []struct {
		expression string
		expected   string
	}{
		{"${math((body + 2) * 3)}", "36"},
		{"${math(100 / (body - 5))}", "20"},
	}

	for _, tt := range tests {
		t.Run(tt.expression, func(t *testing.T) {
			result, err := ParseSimpleTemplate(tt.expression)
			require.NoError(t, err)

			val, err := result.EvaluateAsString(ex)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, val)
		})
	}
}
