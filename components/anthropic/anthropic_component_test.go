package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gocamel "github.com/tranchida/gocamel"
)

// stubServer returns an httptest server whose handler echoes a fixed Anthropic
// Messages response, recording the last request it received.
type stubServer struct {
	*httptest.Server
	mu       sync.Mutex
	lastReq  *http.Request
	lastBody []byte
}

func (s *stubServer) req() *http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastReq
}

func (s *stubServer) body() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastBody
}

func newStubServer(t *testing.T, status int, respBody string) *stubServer {
	t.Helper()
	s := &stubServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.mu.Lock()
		s.lastReq = r
		s.lastBody = body
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(respBody))
	}))
	t.Cleanup(s.Close)
	return s
}

func validResponseJSON(text string) string {
	type block struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type usage struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	}
	type resp struct {
		ID         string  `json:"id"`
		Type       string  `json:"type"`
		Role       string  `json:"role"`
		Model      string  `json:"model"`
		StopReason string  `json:"stop_reason"`
		Content    []block `json:"content"`
		Usage      usage   `json:"usage"`
	}
	r := resp{
		ID:         "msg_test",
		Type:       "message",
		Role:       "assistant",
		Model:      "claude-sonnet-4-5",
		StopReason: "end_turn",
		Content:    []block{{Type: "text", Text: text}},
		Usage:      usage{InputTokens: 5, OutputTokens: 3},
	}
	b, _ := json.Marshal(r)
	return string(b)
}

func producerForTest(t *testing.T, uri string) *AnthropicProducer {
	t.Helper()
	endpoint, err := NewAnthropicComponent().CreateEndpoint(uri)
	require.NoError(t, err, "CreateEndpoint(%q)", uri)
	prod, err := endpoint.CreateProducer()
	require.NoError(t, err)
	return prod.(*AnthropicProducer)
}

func TestAnthropicProducer_Start_RequiresApiKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	p := producerForTest(t, "anthropic:messages?model=claude-sonnet-4-5")
	err := p.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "apiKey")
}

func TestAnthropicProducer_Start_RequiresModel(t *testing.T) {
	p := producerForTest(t, "anthropic:messages?apiKey=test-key")
	err := p.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model")
}

func TestAnthropicProducer_Start_RejectsInvalidMaxTokens(t *testing.T) {
	p := producerForTest(t, "anthropic:messages?apiKey=k&model=m&maxTokens=-5")
	err := p.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maxTokens")
}

func TestAnthropicProducer_Start_RejectsInvalidBaseURL(t *testing.T) {
	_, err := NewAnthropicComponent().CreateEndpoint("anthropic:messages?apiKey=k&model=m&baseURL=ftp://x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "baseURL")
}

func TestAnthropicProducer_Start_DefaultsMaxTokens(t *testing.T) {
	p := producerForTest(t, "anthropic:messages?apiKey=k&model=m")
	require.NoError(t, p.Start(context.Background()))
	assert.EqualValues(t, 1024, p.maxTokens)
}

func TestAnthropicProducer_Send_TextResponse(t *testing.T) {
	stub := newStubServer(t, http.StatusOK, validResponseJSON("hello back"))

	p := producerForTest(t, "anthropic:messages?apiKey=k&model=claude-sonnet-4-5&maxTokens=50&baseURL="+stub.URL)
	require.NoError(t, p.Start(context.Background()))

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("hi")
	require.NoError(t, p.Send(ex))

	assert.Equal(t, "hello back", ex.GetOut().GetBody())
	v, ok := ex.GetOut().GetHeader("AnthropicUsageInputTokens")
	require.True(t, ok)
	assert.EqualValues(t, 5, v)
	v, ok = ex.GetOut().GetHeader("AnthropicUsageOutputTokens")
	require.True(t, ok)
	assert.EqualValues(t, 3, v)
	v, ok = ex.GetOut().GetHeader("AnthropicStopReason")
	require.True(t, ok)
	assert.Equal(t, "end_turn", v)
	v, ok = ex.GetOut().GetHeader("AnthropicModel")
	require.True(t, ok)
	assert.Equal(t, "claude-sonnet-4-5", v)

	// Verify the request hit the expected path and carried the API key.
	req := stub.req()
	require.NotNil(t, req)
	assert.Equal(t, "/v1/messages", req.URL.Path)
	assert.Equal(t, "k", req.Header.Get("x-api-key"))

	// Verify the request body carries model, maxTokens and the user prompt.
	var got map[string]any
	require.NoError(t, json.Unmarshal(stub.body(), &got))
	assert.Equal(t, "claude-sonnet-4-5", got["model"])
	assert.EqualValues(t, 50, got["max_tokens"])
	msgs, _ := got["messages"].([]any)
	require.Len(t, msgs, 1)
	first, _ := msgs[0].(map[string]any)
	assert.Equal(t, "user", first["role"])
	content, _ := first["content"].([]any)
	require.Len(t, content, 1)
	block, _ := content[0].(map[string]any)
	assert.Equal(t, "text", block["type"])
	assert.Equal(t, "hi", block["text"])
}

func TestAnthropicProducer_Send_SystemFromHeader(t *testing.T) {
	stub := newStubServer(t, http.StatusOK, validResponseJSON("ok"))

	p := producerForTest(t, "anthropic:messages?apiKey=k&model=m&system=default-system&allowHeaderOverride=true&baseURL="+stub.URL)
	require.NoError(t, p.Start(context.Background()))

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("hi")
	ex.GetIn().SetHeader("AnthropicSystem", "override-system")
	require.NoError(t, p.Send(ex))

	var got map[string]any
	require.NoError(t, json.Unmarshal(stub.body(), &got))
	sys, _ := got["system"].([]any)
	require.Len(t, sys, 1)
	first, _ := sys[0].(map[string]any)
	assert.Equal(t, "override-system", first["text"])
}

func TestAnthropicProducer_Send_TemperatureFromHeader(t *testing.T) {
	stub := newStubServer(t, http.StatusOK, validResponseJSON("ok"))
	p := producerForTest(t, "anthropic:messages?apiKey=k&model=m&allowHeaderOverride=true&baseURL="+stub.URL)
	require.NoError(t, p.Start(context.Background()))

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("hi")
	ex.GetIn().SetHeader("AnthropicTemperature", "0.42")
	require.NoError(t, p.Send(ex))

	var got map[string]any
	require.NoError(t, json.Unmarshal(stub.body(), &got))
	assert.EqualValues(t, 0.42, got["temperature"])
}

func TestAnthropicProducer_Send_HeaderOverridesIgnoredByDefault(t *testing.T) {
	stub := newStubServer(t, http.StatusOK, validResponseJSON("ok"))
	p := producerForTest(t, "anthropic:messages?apiKey=k&model=uri-model&maxTokens=50&system=uri-system&baseURL="+stub.URL)
	require.NoError(t, p.Start(context.Background()))

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("hi")
	ex.GetIn().SetHeader("AnthropicSystem", "header-system")
	ex.GetIn().SetHeader("AnthropicModel", "header-model")
	ex.GetIn().SetHeader("AnthropicMaxTokens", "4096")
	ex.GetIn().SetHeader("AnthropicTemperature", "0.42")
	require.NoError(t, p.Send(ex))

	var got map[string]any
	require.NoError(t, json.Unmarshal(stub.body(), &got))
	assert.Equal(t, "uri-model", got["model"])
	assert.EqualValues(t, 50, got["max_tokens"])
	assert.NotContains(t, got, "temperature")
	sys, _ := got["system"].([]any)
	require.Len(t, sys, 1)
	first, _ := sys[0].(map[string]any)
	assert.Equal(t, "uri-system", first["text"])
}

func TestAnthropicProducer_Send_HeaderOverridesWithOptIn(t *testing.T) {
	stub := newStubServer(t, http.StatusOK, validResponseJSON("ok"))
	p := producerForTest(t, "anthropic:messages?apiKey=k&model=uri-model&maxTokens=50&allowHeaderOverride=true&baseURL="+stub.URL)
	require.NoError(t, p.Start(context.Background()))

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("hi")
	ex.GetIn().SetHeader("AnthropicModel", "header-model")
	ex.GetIn().SetHeader("AnthropicMaxTokens", "4096")
	require.NoError(t, p.Send(ex))

	var got map[string]any
	require.NoError(t, json.Unmarshal(stub.body(), &got))
	assert.Equal(t, "header-model", got["model"])
	assert.EqualValues(t, 4096, got["max_tokens"])
}

func TestAnthropicProducer_Send_UsesExchangeContext(t *testing.T) {
	stub := newStubServer(t, http.StatusOK, validResponseJSON("ok"))
	p := producerForTest(t, "anthropic:messages?apiKey=k&model=m&baseURL="+stub.URL)
	require.NoError(t, p.Start(context.Background()))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ex := gocamel.NewExchange(ctx)
	ex.SetBody("hi")
	err := p.Send(ex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request error from Anthropic")
}

func TestAnthropicProducer_Send_ErrorWrapped(t *testing.T) {
	stub := newStubServer(t, http.StatusInternalServerError, `{"type":"error","error":{"type":"server_error","message":"boom"}}`)
	p := producerForTest(t, "anthropic:messages?apiKey=k&model=m&baseURL="+stub.URL)
	require.NoError(t, p.Start(context.Background()))

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("hi")
	err := p.Send(ex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request error from Anthropic")
	// Out body must remain unchanged on error.
	assert.Nil(t, ex.GetOut().GetBody())
}

func TestAnthropicProducer_Send_NotStarted(t *testing.T) {
	p := &AnthropicProducer{}
	ex := gocamel.NewExchange(context.Background())
	err := p.Send(ex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not been started")
}

func TestAnthropicProducer_SendAfterStop(t *testing.T) {
	p := producerForTest(t, "anthropic:messages?apiKey=k&model=m")
	require.NoError(t, p.Start(context.Background()))
	require.NoError(t, p.Stop())

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("hi")
	err := p.Send(ex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not been started")
}

func TestAnthropicProducer_Send_NoTextContent(t *testing.T) {
	// Response with only a non-text content block.
	body := `{"id":"msg","type":"message","role":"assistant","model":"m","stop_reason":"end_turn","content":[{"type":"tool_use","id":"t1","name":"x","input":{}}],"usage":{"input_tokens":1,"output_tokens":1}}`
	stub := newStubServer(t, http.StatusOK, body)
	p := producerForTest(t, "anthropic:messages?apiKey=k&model=m&baseURL="+stub.URL)
	require.NoError(t, p.Start(context.Background()))

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("hi")
	err := p.Send(ex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no text content")
}

func TestAnthropicEndpoint_URI_RedactsCredentials(t *testing.T) {
	uri := "anthropic:messages?model=claude-sonnet-4-5&apiKey=sk-secret-key"
	endpoint, err := NewAnthropicComponent().CreateEndpoint(uri)
	require.NoError(t, err)
	got := endpoint.URI()
	assert.NotContains(t, got, "sk-secret-key")
	assert.True(t, strings.Contains(got, "apiKey=xxxxx"), "want apiKey masked, got %q", got)
	assert.True(t, strings.Contains(got, "model=claude-sonnet-4-5"), "want model preserved, got %q", got)
}

func TestAnthropicEndpoint_URI_NoSecret(t *testing.T) {
	uri := "anthropic:messages?model=claude-sonnet-4-5"
	endpoint, err := NewAnthropicComponent().CreateEndpoint(uri)
	require.NoError(t, err)
	assert.Equal(t, uri, endpoint.URI())
}

func TestAnthropicEndpoint_CreateConsumer_Error(t *testing.T) {
	endpoint, err := NewAnthropicComponent().CreateEndpoint("anthropic:messages?model=m&apiKey=k")
	require.NoError(t, err)
	_, err = endpoint.CreateConsumer(noopProcessor{})
	require.Error(t, err)
}

type noopProcessor struct{}

func (noopProcessor) Process(*gocamel.Exchange) error { return nil }

// guard against silent scheme regressions: ParseURI must accept the
// "anthropic:" scheme with a non-empty host/path/query.
func TestAnthropicComponent_CreateEndpoint_Valid(t *testing.T) {
	u, err := url.Parse("anthropic:messages?model=m&apiKey=k")
	require.NoError(t, err)
	_ = u
	_, err = NewAnthropicComponent().CreateEndpoint("anthropic:messages?model=m&apiKey=k")
	require.NoError(t, err)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestAnthropic_RefusesRedirectAndIgnoresAmbientEnvAndAppliesTemperatureAndRejectsNilBodyAndNilResp(t *testing.T) {
	t.Run("ignores ambient ANTHROPIC_AUTH_TOKEN and ANTHROPIC_BASE_URL and reads ANTHROPIC_API_KEY", func(t *testing.T) {
		var ambientHits atomic.Int32
		ambientSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ambientHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(validResponseJSON("ambient")))
		}))
		t.Cleanup(ambientSrv.Close)

		t.Setenv("ANTHROPIC_API_KEY", "env-anthropic-key")
		t.Setenv("ANTHROPIC_AUTH_TOKEN", "ambient-bearer-token")
		t.Setenv("ANTHROPIC_BASE_URL", ambientSrv.URL)

		p := producerForTest(t, "anthropic:messages?model=claude-sonnet-4-5")
		require.NoError(t, p.Start(context.Background()))
		t.Cleanup(func() { _ = p.Stop() })

		var capturedURL string
		var capturedAPIKey string
		var capturedAuth string
		p.httpClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			capturedURL = req.URL.String()
			capturedAPIKey = req.Header.Get("X-Api-Key")
			capturedAuth = req.Header.Get("Authorization")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(validResponseJSON("ok"))),
			}, nil
		})

		ex := gocamel.NewExchange(context.Background())
		ex.SetBody("hi")
		require.NoError(t, p.Send(ex))

		assert.Equal(t, "ok", ex.GetOut().GetBody())
		assert.Equal(t, "https://api.anthropic.com/v1/messages", capturedURL)
		assert.Equal(t, "env-anthropic-key", capturedAPIKey)
		assert.Empty(t, capturedAuth, "ambient ANTHROPIC_AUTH_TOKEN must be ignored")
		assert.Zero(t, ambientHits.Load(), "ambient ANTHROPIC_BASE_URL must be ignored")
	})

	t.Run("refuses 307 redirect without leaking X-Api-Key", func(t *testing.T) {
		var targetHits atomic.Int32
		var leakedKey string
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			targetHits.Add(1)
			leakedKey = r.Header.Get("x-api-key")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(validResponseJSON("leaked")))
		}))
		t.Cleanup(target.Close)

		redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL+"/v1/messages", http.StatusTemporaryRedirect)
		}))
		t.Cleanup(redirector.Close)

		p := producerForTest(t, "anthropic:messages?apiKey=secret-api-key&model=m&baseURL="+redirector.URL)
		require.NoError(t, p.Start(context.Background()))
		t.Cleanup(func() { _ = p.Stop() })

		ex := gocamel.NewExchange(context.Background())
		ex.SetBody("hi")
		err := p.Send(ex)
		require.Error(t, err)
		assert.Zero(t, targetHits.Load(), "redirect target must not be contacted")
		assert.Empty(t, leakedKey, "X-Api-Key must not be leaked on redirect")
	})

	t.Run("applies temperature=0 URI option and rejects invalid URI and header temperature", func(t *testing.T) {
		stub := newStubServer(t, http.StatusOK, validResponseJSON("ok"))
		p := producerForTest(t, "anthropic:messages?apiKey=k&model=m&temperature=0&baseURL="+stub.URL)
		require.NoError(t, p.Start(context.Background()))
		t.Cleanup(func() { _ = p.Stop() })

		ex := gocamel.NewExchange(context.Background())
		ex.SetBody("hi")
		require.NoError(t, p.Send(ex))

		var got map[string]any
		require.NoError(t, json.Unmarshal(stub.body(), &got))
		tempVal, hasTemp := got["temperature"]
		require.True(t, hasTemp, "temperature=0 must be serialized in request JSON")
		assert.EqualValues(t, 0, tempVal)

		for _, badTemp := range []string{"NaN", "Inf", "-Inf", "-0.1", "1.1", "abc"} {
			_, err := NewAnthropicComponent().CreateEndpoint("anthropic:messages?apiKey=k&model=m&temperature=" + url.QueryEscape(badTemp))
			assert.Error(t, err, "CreateEndpoint should reject temperature=%s", badTemp)
		}

		pOverride := producerForTest(t, "anthropic:messages?apiKey=k&model=m&allowHeaderOverride=true&baseURL="+stub.URL)
		require.NoError(t, pOverride.Start(context.Background()))
		t.Cleanup(func() { _ = pOverride.Stop() })

		for _, badHeader := range []any{"NaN", "Inf", "-0.1", "1.5", "abc", math.NaN(), math.Inf(1)} {
			exBad := gocamel.NewExchange(context.Background())
			exBad.SetBody("hi")
			exBad.GetIn().SetHeader("AnthropicTemperature", badHeader)
			err := pOverride.Send(exBad)
			assert.Error(t, err, "Send should reject AnthropicTemperature=%v", badHeader)
		}
	})

	t.Run("rejects nil body", func(t *testing.T) {
		var hits atomic.Int32
		stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(validResponseJSON("ok")))
		}))
		t.Cleanup(stub.Close)

		p := producerForTest(t, "anthropic:messages?apiKey=k&model=m&baseURL="+stub.URL)
		require.NoError(t, p.Start(context.Background()))
		t.Cleanup(func() { _ = p.Stop() })

		ex := gocamel.NewExchange(context.Background())
		err := p.Send(ex)
		require.Error(t, err)
		assert.Zero(t, hits.Load(), "must not send HTTP request for nil body")
	})

	t.Run("handles HTTP 200 null body without panic", func(t *testing.T) {
		stub := newStubServer(t, http.StatusOK, "null")
		p := producerForTest(t, "anthropic:messages?apiKey=k&model=m&baseURL="+stub.URL)
		require.NoError(t, p.Start(context.Background()))
		t.Cleanup(func() { _ = p.Stop() })

		ex := gocamel.NewExchange(context.Background())
		ex.SetBody("hi")
		err := p.Send(ex)
		require.Error(t, err)
	})

	t.Run("rejects oversized response over 32 MiB cleanly", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"msg","type":"message","role":"assistant","model":"m","stop_reason":"end_turn","content":[{"type":"text","text":"`))
			chunk := bytes.Repeat([]byte("a"), 1<<20)
			for i := 0; i < 33; i++ {
				if _, err := w.Write(chunk); err != nil {
					return
				}
			}
			_, _ = w.Write([]byte(`"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
		}))
		t.Cleanup(srv.Close)

		p := producerForTest(t, "anthropic:messages?apiKey=k&model=m&baseURL="+srv.URL)
		require.NoError(t, p.Start(context.Background()))
		t.Cleanup(func() { _ = p.Stop() })

		ex := gocamel.NewExchange(context.Background())
		ex.SetBody("hi")
		err := p.Send(ex)
		require.Error(t, err)
	})

	t.Run("rejects hostless and opaque baseURL", func(t *testing.T) {
		for _, bad := range []string{"http:///v1", "https://", "http:opaque"} {
			_, err := NewAnthropicComponent().CreateEndpoint("anthropic:messages?apiKey=k&model=m&baseURL=" + url.QueryEscape(bad))
			assert.Error(t, err, "should reject baseURL=%q", bad)
		}
	})

	t.Run("disables SDK retries via WithMaxRetries(0)", func(t *testing.T) {
		var attempts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts.Add(1)
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"server_error","message":"boom"}}`))
		}))
		t.Cleanup(srv.Close)

		p := producerForTest(t, "anthropic:messages?apiKey=k&model=m&baseURL="+srv.URL)
		require.NoError(t, p.Start(context.Background()))
		t.Cleanup(func() { _ = p.Stop() })

		ex := gocamel.NewExchange(context.Background())
		ex.SetBody("hi")
		err := p.Send(ex)
		require.Error(t, err)
		assert.EqualValues(t, 1, attempts.Load(), "SDK must not retry when WithMaxRetries(0) is configured")
	})

	t.Run("rejects invalid allowHeaderOverride and invalid AnthropicMaxTokens override", func(t *testing.T) {
		_, err := NewAnthropicComponent().CreateEndpoint("anthropic:messages?apiKey=k&model=m&allowHeaderOverride=bogus")
		require.Error(t, err)

		stub := newStubServer(t, http.StatusOK, validResponseJSON("ok"))
		p := producerForTest(t, "anthropic:messages?apiKey=k&model=m&allowHeaderOverride=true&baseURL="+stub.URL)
		require.NoError(t, p.Start(context.Background()))
		t.Cleanup(func() { _ = p.Stop() })

		for _, badMax := range []string{"-1", "0", "notanint"} {
			ex := gocamel.NewExchange(context.Background())
			ex.SetBody("hi")
			ex.GetIn().SetHeader("AnthropicMaxTokens", badMax)
			require.Error(t, p.Send(ex))
		}
	})

	t.Run("parses valid temperature header types and rejects invalid types", func(t *testing.T) {
		for _, good := range []any{[]byte("0.25"), float64(0.5), float32(0.5), int(1), int64(0), stringerVal("0.75")} {
			v, err := parseTemperatureHeader(good)
			require.NoError(t, err, "value=%v", good)
			require.NotNil(t, v)
		}
		for _, empty := range []any{nil, "", []byte(nil), stringerVal("")} {
			v, err := parseTemperatureHeader(empty)
			require.NoError(t, err)
			assert.Nil(t, v)
		}
		_, err := parseTemperatureHeader(struct{}{})
		require.Error(t, err)
	})

	t.Run("handles Content-Length over 32 MiB and exact boundary in limitedReadCloser", func(t *testing.T) {
		rt := &limitedResponseTransport{
			base: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode:    http.StatusOK,
					ContentLength: maxResponseBodyBytes + 1,
					Body:          io.NopCloser(strings.NewReader("x")),
				}, nil
			}),
			maxBytes: maxResponseBodyBytes,
		}
		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		require.NoError(t, err)
		_, err = rt.RoundTrip(req)
		require.Error(t, err)

		rcExact := &limitedReadCloser{rc: io.NopCloser(strings.NewReader("hello")), remaining: 5, limit: 5}
		n, err := rcExact.Read(nil)
		require.NoError(t, err)
		assert.Zero(t, n)
		got, err := io.ReadAll(rcExact)
		require.NoError(t, err)
		assert.Equal(t, "hello", string(got))

		rcOver := &limitedReadCloser{rc: io.NopCloser(strings.NewReader("hello!")), remaining: 5, limit: 5}
		_, err = io.ReadAll(rcOver)
		require.Error(t, err)
		_, err = rcOver.Read(make([]byte, 4))
		require.Error(t, err)
	})

	t.Run("direct endpoint struct fallback works in Start", func(t *testing.T) {
		u, err := url.Parse("anthropic:messages?apiKey=k&model=m&temperature=0.5&maxTokens=128")
		require.NoError(t, err)
		p := &AnthropicProducer{endpoint: &AnthropicEndpoint{uri: u.String(), url: u}}
		require.NoError(t, p.Start(context.Background()))
		assert.True(t, p.hasTemperature)
		assert.InDelta(t, 0.5, p.temperature, 1e-9)
		assert.EqualValues(t, 128, p.maxTokens)
		_ = p.Stop()

		badU, err := url.Parse("anthropic:messages?apiKey=k&model=m&baseURL=" + url.QueryEscape("http:///bad") + "&" + strconv.Itoa(1))
		require.NoError(t, err)
		pBad := &AnthropicProducer{endpoint: &AnthropicEndpoint{uri: badU.String(), url: badU}}
		require.Error(t, pBad.Start(context.Background()))
	})
}

type stringerVal string

func (s stringerVal) String() string { return string(s) }
