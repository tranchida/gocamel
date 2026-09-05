package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gocamel "gitlab.com/tranchida/gocamel"
)

// stubServer returns an httptest server whose handler echoes a fixed Anthropic
// Messages response, recording the last request it received.
type stubServer struct {
	*httptest.Server
	lastReq  *http.Request
	lastBody []byte
}

func newStubServer(t *testing.T, status int, respBody string) *stubServer {
	t.Helper()
	s := &stubServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		s.lastReq = r
		s.lastBody = body
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
	require.NotNil(t, stub.lastReq)
	assert.Equal(t, "/v1/messages", stub.lastReq.URL.Path)
	assert.Equal(t, "k", stub.lastReq.Header.Get("x-api-key"))

	// Verify the request body carries model, maxTokens and the user prompt.
	var got map[string]any
	require.NoError(t, json.Unmarshal(stub.lastBody, &got))
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

	p := producerForTest(t, "anthropic:messages?apiKey=k&model=m&system=default-system&baseURL="+stub.URL)
	require.NoError(t, p.Start(context.Background()))

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("hi")
	ex.GetIn().SetHeader("AnthropicSystem", "override-system")
	require.NoError(t, p.Send(ex))

	var got map[string]any
	require.NoError(t, json.Unmarshal(stub.lastBody, &got))
	sys, _ := got["system"].([]any)
	require.Len(t, sys, 1)
	first, _ := sys[0].(map[string]any)
	assert.Equal(t, "override-system", first["text"])
}

func TestAnthropicProducer_Send_TemperatureFromHeader(t *testing.T) {
	stub := newStubServer(t, http.StatusOK, validResponseJSON("ok"))
	p := producerForTest(t, "anthropic:messages?apiKey=k&model=m&baseURL="+stub.URL)
	require.NoError(t, p.Start(context.Background()))

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("hi")
	ex.GetIn().SetHeader("AnthropicTemperature", "0.42")
	require.NoError(t, p.Send(ex))

	var got map[string]any
	require.NoError(t, json.Unmarshal(stub.lastBody, &got))
	assert.EqualValues(t, 0.42, got["temperature"])
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
