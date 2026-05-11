package gocamel

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSimpleLanguage_Base64Functions(t *testing.T) {
	ex := NewExchange(context.Background())
	ex.In.SetBody("Hello World")
	ex.In.SetHeader("myHeader", "GoCamel")

	tests := []struct {
		expression string
		expected   string
	}{
		// Encoding
		{"${base64:encode:body}", "SGVsbG8gV29ybGQ="},
		{"${base64:encode:header.myHeader}", "R29DYW1lbA=="},

		// Decoding
		{"${base64:decode:'SGVsbG8gV29ybGQ='}", "Hello World"},
		{"${base64:decode:'R29DYW1lbA=='}", "GoCamel"},
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

func TestSimpleLanguage_Base64Complex(t *testing.T) {
	ex := NewExchange(context.Background())
	ex.In.SetBody("SGVsbG8=") // "Hello" in base64

	tests := []struct {
		expression string
		expected   string
	}{
		// Decode body, then encode it back
		{"${base64:encode:base64:decode:body}", "SGVsbG8="},
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
