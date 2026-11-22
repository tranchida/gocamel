package ollama

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ollama/ollama/api"
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

// treatAllHostsAsCloud lets the cloud client target a local httptest server.
func treatAllHostsAsCloud(t *testing.T) {
	t.Helper()
	orig := isCloudHost
	isCloudHost = func(string) bool { return true }
	t.Cleanup(func() { isCloudHost = orig })
}

func TestOllamaCloud_Chat_SendsBearerKey(t *testing.T) {
	treatAllHostsAsCloud(t)
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
	treatAllHostsAsCloud(t)
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
	treatAllHostsAsCloud(t)
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
	p := producerForTest(t, "ollama:chat?model=m&host=http://ollama.com")
	err := p.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), cloudAPIKeyEnv)
}

func TestOllamaCloud_UsesCloudClientForOllamaCom(t *testing.T) {
	t.Setenv(cloudAPIKeyEnv, "secret")
	p := producerForTest(t, "ollama:chat?model=m&host="+CloudHost)
	require.NoError(t, p.Start(context.Background()))
	t.Cleanup(func() { _ = p.Stop() })
	_, ok := p.client.(*cloudClient)
	assert.True(t, ok)
}

// A key exported for the ollama CLI must neither be sent to, nor break, a
// local or LAN server reached over plain HTTP.
func TestOllamaCloud_KeyNotSentToOtherHosts(t *testing.T) {
	t.Setenv(cloudAPIKeyEnv, "secret")
	var auth string
	srv := newAuthStubServer(t, `{"model":"m","message":{"role":"assistant","content":"hi"},"done":true}`, &auth)

	for _, host := range []string{srv.URL, "http://192.168.1.10:11434", "https://ollama.example.com", ""} {
		uri := "ollama:chat?model=m"
		if host != "" {
			uri += "&host=" + host
		}
		p := producerForTest(t, uri)
		require.NoError(t, p.Start(context.Background()), host)
		_, isCloud := p.client.(*cloudClient)
		assert.False(t, isCloud, host)
		_ = p.Stop()
	}

	p := producerForTest(t, "ollama:chat?model=m&host="+srv.URL)
	require.NoError(t, p.Start(context.Background()))
	t.Cleanup(func() { _ = p.Stop() })
	ex := gocamel.NewExchange(context.Background())
	ex.GetIn().SetBody("hello")
	require.NoError(t, p.Send(ex))
	assert.Empty(t, auth, "the API key must not reach a non-cloud host")
}

func TestIsOllamaCloudHost(t *testing.T) {
	cases := map[string]bool{
		"ollama.com":         true,
		"OLLAMA.COM":         true,
		"ollama.com.":        true,
		"api.ollama.com":     true,
		"localhost":          false,
		"127.0.0.1":          false,
		"192.168.1.10":       false,
		"ollama.example.com": false,
		"evilollama.com":     false,
		"ollama.com.evil.io": false,
	}
	for host, want := range cases {
		assert.Equal(t, want, isOllamaCloudHost(host), host)
	}
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

func TestOllamaCloud_RejectsPaddedOversizedJSONAndTruncatesUTF8Errors(t *testing.T) {
	treatAllHostsAsCloud(t)
	t.Setenv(cloudAPIKeyEnv, "secret")

	// Valid JSON followed by >32 MiB of whitespace must fail rather than silently truncating.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"model":"m","message":{"role":"assistant","content":"ok"},"done":true}`))
		pad := bytes.Repeat([]byte(" "), 1<<20)
		for i := 0; i < 33; i++ {
			if _, err := w.Write(pad); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)

	p := producerForTest(t, "ollama:chat?model=m&host="+srv.URL)
	require.NoError(t, p.Start(context.Background()))
	t.Cleanup(func() { _ = p.Stop() })

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("hi")
	err := p.Send(ex)
	require.Error(t, err)

	// Multi-byte UTF-8 error JSON truncation must stay valid UTF-8 and <= 512 bytes.
	utf8Msg := strings.Repeat("é", 400) // 800 bytes
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"` + utf8Msg + `"}`))
	}))
	t.Cleanup(errSrv.Close)

	pErr := producerForTest(t, "ollama:chat?model=m&host="+errSrv.URL)
	require.NoError(t, pErr.Start(context.Background()))
	t.Cleanup(func() { _ = pErr.Stop() })

	err2 := pErr.Send(ex)
	require.Error(t, err2)
	var statusErr api.StatusError
	require.True(t, errors.As(err2, &statusErr))
	assert.LessOrEqual(t, len(statusErr.ErrorMessage), maxErrorBytes)
	assert.True(t, utf8.ValidString(statusErr.ErrorMessage))
}
