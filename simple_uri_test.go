package gocamel

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newURIExchange() *Exchange {
	ex := NewExchange(context.Background())
	ex.SetHeader("v", "a?b#c")
	ex.SetHeader("q", "1&x=y")
	ex.SetHeader("f", "a b&c")
	ex.SetProperty("ch", "orders")
	return ex
}

func TestSimpleTemplate_EvaluateURIEscapesByPosition(t *testing.T) {
	tests := []struct {
		name     string
		template string
		want     string
	}{
		// Before the scheme: a whole-URI or scheme placeholder is trusted and verbatim.
		{"whole URI verbatim", "${header.v}", "a?b#c"},
		{"scheme placeholder verbatim", "${header.v}:path", "a?b#c:path"},
		// After the scheme, in the path: only '?' and '#' are escaped.
		{"path escapes ? and #", "chan:${header.v}", "chan:a%3Fb%23c"},
		// After a literal '?': query escaping.
		{"query escapes &, =, ?, #", "seda:q?size=${header.q}", "seda:q?size=1%26x%3Dy"},
		// After a literal '#': the fragment is treated as query-like.
		{"fragment uses query escaping", "chan:x#${header.f}", "chan:x#a+b%26c"},
		// Full Simple language on the exchange property.
		{"exchange property expression", "chan:${exchangeProperty.ch}", "chan:orders"},
		// Literal template: returned unchanged.
		{"literal unchanged", "chan:static?bufferSize=4", "chan:static?bufferSize=4"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl, err := ParseSimpleTemplate(tt.template)
			require.NoError(t, err)
			got, err := tmpl.EvaluateURI(newURIExchange())
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSimpleTemplate_EvaluateURIMatchesInterpolateURIForSimplePlaceholders(t *testing.T) {
	templates := []string{
		"${header.v}",
		"chan:${header.v}",
		"seda:q?size=${header.q}",
		"chan:x#${header.f}",
		"exec:echo?args=${header.q}&tag=${header.f}",
		"http://api/items/${header.v}?v=${body}",
	}

	for _, template := range templates {
		t.Run(template, func(t *testing.T) {
			ex := newURIExchange()
			ex.GetIn().SetBody("body value")

			tmpl, err := ParseSimpleTemplate(template)
			require.NoError(t, err)
			got, err := tmpl.EvaluateURI(ex)
			require.NoError(t, err)
			assert.Equal(t, InterpolateURI(template, ex), got)
		})
	}
}

func TestSimpleTemplate_EvaluateURIReturnsEvaluationError(t *testing.T) {
	tmpl, err := ParseSimpleTemplate("chan:${random(0)}")
	require.NoError(t, err)

	got, err := tmpl.EvaluateURI(newURIExchange())
	require.Error(t, err)
	assert.Empty(t, got)
	assert.Contains(t, err.Error(), "random argument must be positive")
}
