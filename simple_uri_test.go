package gocamel

import (
	"context"
	"strings"
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

func TestInterpolateURI_LeadingPlaceholderAndAuthoritySSRF(t *testing.T) {
	t.Run("leading placeholder query injection is escaped", func(t *testing.T) {
		ex := NewExchange(context.Background())
		ex.SetHeader("base", "http://api.internal/search")
		ex.SetHeader("user", "x&allowHeaderOverride=true")

		got := InterpolateURI("${header.base}?q=${header.user}", ex)
		assert.Equal(t, "http://api.internal/search?q=x%26allowHeaderOverride%3Dtrue", got)

		u, err := ParseURI(got)
		require.NoError(t, err)
		assert.Equal(t, "x&allowHeaderOverride=true", u.Query().Get("q"))
		assert.Empty(t, u.Query().Get("allowHeaderOverride"))
	})

	t.Run("authority path and userinfo SSRF characters are escaped", func(t *testing.T) {
		for _, tenant := range []string{
			"169.254.169.254/latest/",
			"x@169.254.169.254/",
			`a?b#c\d,e`,
		} {
			ex := NewExchange(context.Background())
			ex.SetHeader("tenant", tenant)

			got := InterpolateURI("http://${header.tenant}.internal/api", ex)
			// Authority must not leak unescaped '/', '@', '?', '#', '\', or ',' before ".internal".
			authority := strings.TrimPrefix(got, "http://")
			authority = strings.TrimSuffix(authority, "/api")
			assert.True(t, strings.HasSuffix(authority, ".internal"), "unexpected authority in %q", got)
			assert.NotContains(t, authority, "/")
			assert.NotContains(t, authority, "@")
			assert.NotContains(t, authority, "?")
			assert.NotContains(t, authority, "#")
			assert.NotContains(t, authority, `\`)
			assert.NotContains(t, authority, ",")

			u, err := ParseURI(got)
			if err == nil {
				assert.True(t, strings.HasSuffix(u.Host, ".internal"), "host escaped .internal: %q", u.Host)
			}
		}
	})

	t.Run("userinfo placeholder cannot hijack host", func(t *testing.T) {
		ex := NewExchange(context.Background())
		ex.SetHeader("user", "x@169.254.169.254/")

		got := InterpolateURI("http://${header.user}@trusted.internal/api", ex)
		u, err := ParseURI(got)
		require.NoError(t, err)
		assert.Equal(t, "trusted.internal", u.Host)
	})

	t.Run("valid authority and whole-URI delegation still work", func(t *testing.T) {
		ex := NewExchange(context.Background())
		ex.SetHeader("tenant", "acme")
		ex.SetHeader("target", "seda:out?size=5")

		got := InterpolateURI("http://${header.tenant}.internal/api", ex)
		u, err := ParseURI(got)
		require.NoError(t, err)
		assert.Equal(t, "acme.internal", u.Host)
		assert.Equal(t, "/api", u.Path)

		assert.Equal(t, "seda:out?size=5", InterpolateURI("${header.target}", ex))
	})
}

func TestEvaluateURI_LeadingPlaceholderAuthorityAndNil(t *testing.T) {
	t.Run("leading placeholder query injection is escaped", func(t *testing.T) {
		ex := NewExchange(context.Background())
		ex.SetHeader("base", "http://api.internal/search")
		ex.SetHeader("user", "x&allowHeaderOverride=true")

		tmpl, err := ParseSimpleTemplate("${header.base}?q=${header.user}")
		require.NoError(t, err)
		got, err := tmpl.EvaluateURI(ex)
		require.NoError(t, err)
		assert.Equal(t, "http://api.internal/search?q=x%26allowHeaderOverride%3Dtrue", got)
	})

	t.Run("authority SSRF characters are escaped", func(t *testing.T) {
		ex := NewExchange(context.Background())
		ex.SetHeader("tenant", "169.254.169.254/latest/")

		tmpl, err := ParseSimpleTemplate("http://${header.tenant}.internal/api")
		require.NoError(t, err)
		got, err := tmpl.EvaluateURI(ex)
		require.NoError(t, err)
		assert.Equal(t, "http://169.254.169.254%2Flatest%2F.internal/api", got)
	})

	t.Run("missing or nil placeholder formats as empty string not <nil>", func(t *testing.T) {
		ex := NewExchange(context.Background())
		ex.SetHeader("nilhdr", nil)

		for _, expr := range []string{
			"chan:${header.missing}",
			"chan:${header.nilhdr}",
		} {
			tmpl, err := ParseSimpleTemplate(expr)
			require.NoError(t, err)
			got, err := tmpl.EvaluateURI(ex)
			require.NoError(t, err)
			assert.Equal(t, "chan:", got)
		}

		tmpl, err := ParseSimpleTemplate("${header.missing}")
		require.NoError(t, err)
		got, err := tmpl.EvaluateURI(ex)
		require.NoError(t, err)
		assert.Equal(t, "", got)
	})

	t.Run("whole-URI delegation remains verbatim", func(t *testing.T) {
		ex := NewExchange(context.Background())
		ex.SetHeader("target", "seda:out?size=5")

		tmpl, err := ParseSimpleTemplate("${header.target}")
		require.NoError(t, err)
		got, err := tmpl.EvaluateURI(ex)
		require.NoError(t, err)
		assert.Equal(t, "seda:out?size=5", got)
	})
}
