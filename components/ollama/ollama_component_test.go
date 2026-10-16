package ollama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gocamel "github.com/tranchida/gocamel"
)

type stubServer struct {
	*httptest.Server
	mu       sync.Mutex
	lastPath string
	lastBody []byte
}

func (s *stubServer) path() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastPath
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
		s.lastPath = r.URL.Path
		s.lastBody = body
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(respBody))
	}))
	t.Cleanup(s.Close)
	return s
}

func producerForTest(t *testing.T, uri string) *OllamaProducer {
	t.Helper()
	endpoint, err := NewOllamaComponent().CreateEndpoint(uri)
	require.NoError(t, err, "CreateEndpoint(%q)", uri)
	prod, err := endpoint.CreateProducer()
	require.NoError(t, err)
	return prod.(*OllamaProducer)
}

func TestOllamaProducer_Start_RequiresModel(t *testing.T) {
	p := producerForTest(t, "ollama:chat?host=http://localhost:11434")
	err := p.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model")
}

func TestOllamaProducer_Start_RejectsInvalidMode(t *testing.T) {
	_, err := NewOllamaComponent().CreateEndpoint("ollama:bogus?model=m")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mode")
}

func TestOllamaProducer_Start_RejectsInvalidHost(t *testing.T) {
	_, err := NewOllamaComponent().CreateEndpoint("ollama:chat?model=m&host=ftp://x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "host")
}

func TestOllamaProducer_Start_RejectsInvalidKeepAlive(t *testing.T) {
	_, err := NewOllamaComponent().CreateEndpoint("ollama:chat?model=m&keepAlive=abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "keepAlive")
}

func TestOllamaProducer_Start_DefaultHost(t *testing.T) {
	p := producerForTest(t, "ollama:chat?model=m")
	require.NoError(t, p.Start(context.Background()))
	require.NotNil(t, p.client)
	assert.Equal(t, "chat", p.mode)
}

func TestOllamaProducer_Chat(t *testing.T) {
	stub := newStubServer(t, http.StatusOK, `{"model":"llama3.1","created_at":"2026-01-01T00:00:00Z","message":{"role":"assistant","content":"hi"},"done":true,"done_reason":"stop","total_duration":1000,"prompt_eval_count":2,"eval_count":3}`)

	p := producerForTest(t, "ollama:chat?model=llama3.1&host="+stub.URL)
	require.NoError(t, p.Start(context.Background()))

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("hello")
	require.NoError(t, p.Send(ex))

	assert.Equal(t, "hi", ex.GetOut().GetBody())
	assert.Equal(t, "llama3.1", mustGetHeader(t, ex, "OllamaModel"))
	assert.Equal(t, "stop", mustGetHeader(t, ex, "OllamaDoneReason"))
	assert.EqualValues(t, 2, mustGetHeader(t, ex, "OllamaPromptEvalCount"))
	assert.EqualValues(t, 3, mustGetHeader(t, ex, "OllamaEvalCount"))

	assert.Equal(t, "/api/chat", stub.path())
	var got map[string]any
	require.NoError(t, json.Unmarshal(stub.body(), &got))
	assert.Equal(t, "llama3.1", got["model"])
	assert.EqualValues(t, false, got["stream"])
	msgs, _ := got["messages"].([]any)
	require.Len(t, msgs, 1)
	first, _ := msgs[0].(map[string]any)
	assert.Equal(t, "user", first["role"])
	assert.Equal(t, "hello", first["content"])
}

func TestOllamaProducer_Chat_SystemFromHeader(t *testing.T) {
	stub := newStubServer(t, http.StatusOK, `{"model":"m","created_at":"2026-01-01T00:00:00Z","message":{"role":"assistant","content":"ok"},"done":true,"done_reason":"stop"}`)

	p := producerForTest(t, "ollama:chat?model=m&allowHeaderOverride=true&host="+stub.URL)
	require.NoError(t, p.Start(context.Background()))

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("hi")
	ex.GetIn().SetHeader("OllamaSystem", "be concise")
	require.NoError(t, p.Send(ex))

	var got map[string]any
	require.NoError(t, json.Unmarshal(stub.body(), &got))
	msgs, _ := got["messages"].([]any)
	require.Len(t, msgs, 2)
	first, _ := msgs[0].(map[string]any)
	assert.Equal(t, "system", first["role"])
	assert.Equal(t, "be concise", first["content"])
}

func TestOllamaProducer_Chat_ModelOverrideHeader(t *testing.T) {
	stub := newStubServer(t, http.StatusOK, `{"model":"phi3","created_at":"2026-01-01T00:00:00Z","message":{"role":"assistant","content":"ok"},"done":true,"done_reason":"stop"}`)

	p := producerForTest(t, "ollama:chat?model=llama3.1&allowHeaderOverride=true&host="+stub.URL)
	require.NoError(t, p.Start(context.Background()))

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("hi")
	ex.GetIn().SetHeader("OllamaModel", "phi3")
	require.NoError(t, p.Send(ex))

	var got map[string]any
	require.NoError(t, json.Unmarshal(stub.body(), &got))
	assert.Equal(t, "phi3", got["model"])
}

func TestOllamaProducer_Generate(t *testing.T) {
	stub := newStubServer(t, http.StatusOK, `{"model":"llama3.1","created_at":"2026-01-01T00:00:00Z","response":"answer","done":true,"done_reason":"stop","total_duration":5000,"prompt_eval_count":4,"eval_count":5}`)

	p := producerForTest(t, "ollama:generate?model=llama3.1&system=sysprompt&host="+stub.URL)
	require.NoError(t, p.Start(context.Background()))

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("hi")
	require.NoError(t, p.Send(ex))

	assert.Equal(t, "answer", ex.GetOut().GetBody())
	assert.Equal(t, "stop", mustGetHeader(t, ex, "OllamaDoneReason"))
	assert.EqualValues(t, 4, mustGetHeader(t, ex, "OllamaPromptEvalCount"))
	assert.EqualValues(t, 5, mustGetHeader(t, ex, "OllamaEvalCount"))

	assert.Equal(t, "/api/generate", stub.path())
	var got map[string]any
	require.NoError(t, json.Unmarshal(stub.body(), &got))
	assert.Equal(t, "llama3.1", got["model"])
	assert.Equal(t, "hi", got["prompt"])
	assert.Equal(t, "sysprompt", got["system"])
	assert.EqualValues(t, false, got["stream"])
}

func TestOllamaProducer_Generate_SystemHeaderOverride(t *testing.T) {
	stub := newStubServer(t, http.StatusOK, `{"model":"m","created_at":"2026-01-01T00:00:00Z","response":"ok","done":true,"done_reason":"stop"}`)

	p := producerForTest(t, "ollama:generate?model=m&system=uri-system&allowHeaderOverride=true&host="+stub.URL)
	require.NoError(t, p.Start(context.Background()))

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("hi")
	ex.GetIn().SetHeader("OllamaSystem", "header-system")
	require.NoError(t, p.Send(ex))

	var got map[string]any
	require.NoError(t, json.Unmarshal(stub.body(), &got))
	assert.Equal(t, "header-system", got["system"])
}

func TestOllamaProducer_Chat_HeaderOverridesIgnoredByDefault(t *testing.T) {
	stub := newStubServer(t, http.StatusOK, `{"model":"llama3.1","created_at":"2026-01-01T00:00:00Z","message":{"role":"assistant","content":"ok"},"done":true,"done_reason":"stop"}`)

	p := producerForTest(t, "ollama:chat?model=llama3.1&host="+stub.URL)
	require.NoError(t, p.Start(context.Background()))

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("hi")
	ex.GetIn().SetHeader("OllamaModel", "phi3")
	ex.GetIn().SetHeader("OllamaSystem", "header-system")
	require.NoError(t, p.Send(ex))

	var got map[string]any
	require.NoError(t, json.Unmarshal(stub.body(), &got))
	assert.Equal(t, "llama3.1", got["model"])
	msgs, _ := got["messages"].([]any)
	require.Len(t, msgs, 1, "the system header must not add a system message")
}

func TestOllamaProducer_Generate_SystemHeaderIgnoredByDefault(t *testing.T) {
	stub := newStubServer(t, http.StatusOK, `{"model":"m","created_at":"2026-01-01T00:00:00Z","response":"ok","done":true,"done_reason":"stop"}`)

	p := producerForTest(t, "ollama:generate?model=m&system=uri-system&host="+stub.URL)
	require.NoError(t, p.Start(context.Background()))

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("hi")
	ex.GetIn().SetHeader("OllamaSystem", "header-system")
	ex.GetIn().SetHeader("OllamaModel", "phi3")
	require.NoError(t, p.Send(ex))

	var got map[string]any
	require.NoError(t, json.Unmarshal(stub.body(), &got))
	assert.Equal(t, "uri-system", got["system"])
	assert.Equal(t, "m", got["model"])
}

func TestOllamaProducer_Embed(t *testing.T) {
	stub := newStubServer(t, http.StatusOK, `{"model":"nomic-embed-text","embeddings":[[0.1,0.2,0.3]],"total_duration":1000}`)

	p := producerForTest(t, "ollama:embed?model=nomic-embed-text&host="+stub.URL)
	require.NoError(t, p.Start(context.Background()))

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("embed me")
	require.NoError(t, p.Send(ex))

	body := ex.GetOut().GetBody()
	v, ok := body.([]float32)
	require.True(t, ok, "expected []float32, got %T", body)
	assert.Equal(t, []float32{0.1, 0.2, 0.3}, v)
	assert.Equal(t, "/api/embed", stub.path())

	var got map[string]any
	require.NoError(t, json.Unmarshal(stub.body(), &got))
	assert.Equal(t, "nomic-embed-text", got["model"])
	assert.Equal(t, "embed me", got["input"])
}

func TestOllamaProducer_Send_UsesExchangeContext(t *testing.T) {
	stub := newStubServer(t, http.StatusOK, `{"model":"m","created_at":"2026-01-01T00:00:00Z","message":{"role":"assistant","content":"ok"},"done":true,"done_reason":"stop"}`)
	p := producerForTest(t, "ollama:chat?model=m&host="+stub.URL)
	require.NoError(t, p.Start(context.Background()))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ex := gocamel.NewExchange(ctx)
	ex.SetBody("hi")
	err := p.Send(ex)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "ollama chat error"), "got %v", err)
}

func TestOllamaProducer_Send_ErrorWrapped(t *testing.T) {
	stub := newStubServer(t, http.StatusInternalServerError, `{"error":"model not found"}`)
	p := producerForTest(t, "ollama:chat?model=m&host="+stub.URL)
	require.NoError(t, p.Start(context.Background()))

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("hi")
	err := p.Send(ex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ollama chat error")
	assert.Nil(t, ex.GetOut().GetBody())
}

func TestOllamaProducer_Send_NotStarted(t *testing.T) {
	p := &OllamaProducer{}
	ex := gocamel.NewExchange(context.Background())
	err := p.Send(ex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not been started")
}

func TestOllamaProducer_Chat_NoContent(t *testing.T) {
	stub := newStubServer(t, http.StatusOK, `{"model":"m","created_at":"2026-01-01T00:00:00Z","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop"}`)
	p := producerForTest(t, "ollama:chat?model=m&host="+stub.URL)
	require.NoError(t, p.Start(context.Background()))

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("hi")
	err := p.Send(ex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no message content")
}

func TestOllamaEndpoint_CreateConsumer_Error(t *testing.T) {
	endpoint, err := NewOllamaComponent().CreateEndpoint("ollama:chat?model=m")
	require.NoError(t, err)
	_, err = endpoint.CreateConsumer(noopProcessor{})
	require.Error(t, err)
}

func TestOllamaEndpoint_URI_Unchanged(t *testing.T) {
	uri := "ollama:chat?model=llama3.1"
	endpoint, err := NewOllamaComponent().CreateEndpoint(uri)
	require.NoError(t, err)
	assert.Equal(t, uri, endpoint.URI())
}

func mustGetHeader(t *testing.T, ex *gocamel.Exchange, key string) any {
	t.Helper()
	v, ok := ex.GetOut().GetHeader(key)
	require.True(t, ok, "missing out header %q", key)
	return v
}

type noopProcessor struct{}

func (noopProcessor) Process(*gocamel.Exchange) error { return nil }
