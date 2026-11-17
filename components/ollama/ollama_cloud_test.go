package ollama

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gocamel "github.com/tranchida/gocamel"
)

// authStubServer records the Authorization header of each request.
func newAuthStubServer(t *testing.T, respBody string, gotAuth *string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		*gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respBody))
	}))
	t.Cleanup(s.Close)
	return s
}

func TestOllamaCloud_Chat_SendsBearerKey(t *testing.T) {
	t.Setenv(cloudAPIKeyEnv, "secret-key")
	var auth string
	srv := newAuthStubServer(t, `{"model":"gpt-oss:120b","message":{"role":"assistant","content":"hi"},"done":true}`, &auth)

	p := producerForTest(t, "ollama:chat?model=gpt-oss:120b&host="+srv.URL)
	require.NoError(t, p.Start(context.Background()))
	t.Cleanup(func() { _ = p.Stop() })
	_, ok := p.client.(*cloudClient)
	require.True(t, ok, "client should be the cloud client when OLLAMA_API_KEY is set")

	ex := gocamel.NewExchange(context.Background())
	ex.GetIn().SetBody("hello")
	require.NoError(t, p.Send(ex))
	assert.Equal(t, "Bearer secret-key", auth)
	assert.Equal(t, "hi", ex.GetOut().GetBody())
}

func TestOllamaCloud_Generate_And_Embed(t *testing.T) {
	t.Setenv(cloudAPIKeyEnv, "k")
	var auth string
	srv := newAuthStubServer(t, `{"model":"m","response":"gen","done":true}`, &auth)
	p := producerForTest(t, "ollama:generate?model=m&host="+srv.URL)
	require.NoError(t, p.Start(context.Background()))
	t.Cleanup(func() { _ = p.Stop() })
	ex := gocamel.NewExchange(context.Background())
	ex.GetIn().SetBody("x")
	require.NoError(t, p.Send(ex))
	assert.Equal(t, "gen", ex.GetOut().GetBody())
	assert.Equal(t, "Bearer k", auth)

	embSrv := newAuthStubServer(t, `{"model":"m","embeddings":[[0.1,0.2]]}`, &auth)
	pe := producerForTest(t, "ollama:embed?model=m&host="+embSrv.URL)
	require.NoError(t, pe.Start(context.Background()))
	t.Cleanup(func() { _ = pe.Stop() })
	ex2 := gocamel.NewExchange(context.Background())
	ex2.GetIn().SetBody("x")
	require.NoError(t, pe.Send(ex2))
	assert.Equal(t, []float32{0.1, 0.2}, ex2.GetOut().GetBody())
}

func TestOllamaCloud_APIError(t *testing.T) {
	t.Setenv(cloudAPIKeyEnv, "bad")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	t.Cleanup(srv.Close)

	p := producerForTest(t, "ollama:chat?model=m&host="+srv.URL)
	require.NoError(t, p.Start(context.Background()))
	t.Cleanup(func() { _ = p.Stop() })
	ex := gocamel.NewExchange(context.Background())
	ex.GetIn().SetBody("x")
	err := p.Send(ex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unauthorized")
}

func TestOllamaCloud_NoKey_UsesStandardClient(t *testing.T) {
	t.Setenv(cloudAPIKeyEnv, "")
	p := producerForTest(t, "ollama:chat?model=m")
	require.NoError(t, p.Start(context.Background()))
	t.Cleanup(func() { _ = p.Stop() })
	_, ok := p.client.(*cloudClient)
	assert.False(t, ok)
}

func TestOllamaCloud_RefusesKeyOverPlainHTTP(t *testing.T) {
	t.Setenv(cloudAPIKeyEnv, "secret")
	p := producerForTest(t, "ollama:chat?model=m&host=http://ollama.example.com")
	err := p.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), cloudAPIKeyEnv)
}

func TestRequireSecureKeyTransport(t *testing.T) {
	cases := map[string]bool{
		"https://ollama.com":        true,
		"http://localhost:11434":    true,
		"http://127.0.0.1:11434":    true,
		"http://ollama.example.com": false,
	}
	for raw, wantOK := range cases {
		u, err := url.Parse(raw)
		require.NoError(t, err)
		err = requireSecureKeyTransport(u)
		if wantOK {
			assert.NoError(t, err, raw)
		} else {
			assert.Error(t, err, raw)
		}
	}
}
