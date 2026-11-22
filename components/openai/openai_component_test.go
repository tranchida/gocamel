package openai

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	sdkopenai "github.com/sashabaranov/go-openai"

	gocamel "github.com/tranchida/gocamel"
)

func TestOpenAIProducer_Start(t *testing.T) {
	// Set environment variable for test
	t.Setenv("OPENAI_API_KEY", "test-token")

	u, _ := url.Parse("openai:chat?model=gpt-4&apiKey=test-token")
	endpoint := &OpenAIEndpoint{
		uri: "openai:chat?model=gpt-4",
		url: u,
	}
	producer := &OpenAIProducer{
		endpoint: endpoint,
	}

	err := producer.Start(context.Background())
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if producer.client == nil {
		t.Error("Expected client to be initialized")
	}

	if producer.model != "gpt-4" {
		t.Errorf("Expected model gpt-4, got %s", producer.model)
	}
}

func TestOpenAIProducer_Send_NotStarted(t *testing.T) {
	producer := &OpenAIProducer{}
	exchange := gocamel.NewExchange(context.Background())
	err := producer.Send(exchange)
	if err == nil {
		t.Error("Expected error when sending with unstarted producer")
	}
}

func TestOpenAIProducer_SendAfterStop(t *testing.T) {
	u, _ := url.Parse("openai:chat?model=gpt-4&apiKey=test-token")
	endpoint := &OpenAIEndpoint{uri: "openai:chat?model=gpt-4", url: u}
	producer := &OpenAIProducer{endpoint: endpoint}
	if err := producer.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if err := producer.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	exchange := gocamel.NewExchange(context.Background())
	if err := producer.Send(exchange); err == nil {
		t.Fatal("expected error when sending after Stop")
	}
}

func TestOpenAIEndpoint_URI_RedactsCredentials(t *testing.T) {
	uri := "openai:chat?model=gpt-4&apiKey=sk-super-secret-key"
	endpoint, err := NewOpenAIComponent().CreateEndpoint(uri)
	if err != nil {
		t.Fatalf("CreateEndpoint(%q): %v", uri, err)
	}

	got := endpoint.URI()
	if strings.Contains(got, "sk-super-secret-key") {
		t.Errorf("URI() leaks the API key: %q", got)
	}
	if !strings.Contains(got, "apiKey=xxxxx") {
		t.Errorf("URI() = %q, want apiKey masked with xxxxx", got)
	}
	if !strings.Contains(got, "model=gpt-4") {
		t.Errorf("URI() = %q, want non-sensitive parameters preserved", got)
	}

	// The authorizationToken alias must be masked as well.
	uri = "openai:chat?authorizationToken=sk-another-secret"
	endpoint, err = NewOpenAIComponent().CreateEndpoint(uri)
	if err != nil {
		t.Fatalf("CreateEndpoint(%q): %v", uri, err)
	}
	got = endpoint.URI()
	if strings.Contains(got, "sk-another-secret") {
		t.Errorf("URI() leaks the authorization token: %q", got)
	}
	if !strings.Contains(got, "authorizationToken=xxxxx") {
		t.Errorf("URI() = %q, want authorizationToken masked with xxxxx", got)
	}
}

func TestOpenAIEndpoint_URI_NoSecret(t *testing.T) {
	uri := "openai:chat?model=gpt-4"
	endpoint, err := NewOpenAIComponent().CreateEndpoint(uri)
	if err != nil {
		t.Fatalf("CreateEndpoint(%q): %v", uri, err)
	}
	if endpoint.URI() != uri {
		t.Errorf("URI() = %q, want URI unchanged when no secret is present", endpoint.URI())
	}
}

type contextRecordingDoer struct {
	ctx context.Context
}

func (d *contextRecordingDoer) Do(req *http.Request) (*http.Response, error) {
	d.ctx = req.Context()
	return nil, req.Context().Err()
}

func TestOpenAIProducer_SendUsesExchangeContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	doer := &contextRecordingDoer{}
	config := sdkopenai.DefaultConfig("test-token")
	config.HTTPClient = doer
	producer := &OpenAIProducer{
		client: sdkopenai.NewClientWithConfig(config),
		model:  sdkopenai.GPT3Dot5Turbo,
	}
	exchange := gocamel.NewExchange(ctx)
	exchange.SetBody("hello")

	err := producer.Send(exchange)
	if err == nil {
		t.Fatal("expected canceled request to fail")
	}
	if doer.ctx == nil || !errors.Is(doer.ctx.Err(), context.Canceled) {
		t.Fatalf("request did not receive the exchange context: %v", doer.ctx)
	}
}

// urlRecordingDoer captures the request URL and returns a canned chat
// completion response so we can assert which baseURL the producer dialed.
type urlRecordingDoer struct {
	gotURL *url.URL
}

func (d *urlRecordingDoer) Do(req *http.Request) (*http.Response, error) {
	d.gotURL = req.URL
	body := `{"id":"chatcmpl-test","object":"chat.completion","model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
	resp.Header.Set("Content-Type", "application/json")
	return resp, nil
}

func TestOpenAIProducer_Start_DefaultBaseURL(t *testing.T) {
	u, _ := url.Parse("openai:chat?model=gpt-4&apiKey=test-token")
	endpoint := &OpenAIEndpoint{uri: "openai:chat?model=gpt-4", url: u}
	producer := &OpenAIProducer{endpoint: endpoint}

	if err := producer.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Replace the HTTP client with a recorder while keeping the baseURL that
	// Start() derived from the URI (default https://api.openai.com/v1).
	recorder := &urlRecordingDoer{}
	cfg := sdkopenai.DefaultConfig("test-token")
	cfg.HTTPClient = recorder
	producer.client = sdkopenai.NewClientWithConfig(cfg)

	exchange := gocamel.NewExchange(context.Background())
	exchange.SetBody("hello")
	if err := producer.Send(exchange); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if recorder.gotURL == nil {
		t.Fatal("no request was captured")
	}
	if recorder.gotURL.String() != "https://api.openai.com/v1/chat/completions" {
		t.Errorf("default baseURL: got %q, want https://api.openai.com/v1/chat/completions", recorder.gotURL.String())
	}
}

func TestOpenAIProducer_Start_CustomBaseURL(t *testing.T) {
	var gotPath string
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","choices":[{"message":{"content":"hi"}}]}`))
	}))
	defer server.Close()

	u, _ := url.Parse("openai:chat?model=gpt-4&apiKey=test-token&baseURL=" + url.QueryEscape(server.URL+"/v1"))
	endpoint := &OpenAIEndpoint{uri: "openai:chat?model=gpt-4", url: u}
	producer := &OpenAIProducer{endpoint: endpoint}

	if err := producer.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	exchange := gocamel.NewExchange(context.Background())
	exchange.SetBody("hello")
	if err := producer.Send(exchange); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("custom baseURL: got path %q, want /v1/chat/completions", gotPath)
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("custom baseURL: got auth %q, want Bearer test-token", gotAuth)
	}
}

func TestOpenAICreateEndpoint_BaseURLValidation(t *testing.T) {
	comp := NewOpenAIComponent()

	// Valid URLs
	for _, valid := range []string{
		"openai:chat?model=gpt-4&baseURL=https://api.openai.com/v1",
		"openai:chat?model=gpt-4&baseURL=http://localhost:8080/v1",
	} {
		_, err := comp.CreateEndpoint(valid)
		if err != nil {
			t.Errorf("expected valid baseURL for %q, got error: %v", valid, err)
		}
	}

	// Invalid URLs
	for _, invalid := range []string{
		"openai:chat?model=gpt-4&baseURL=ftp://example.com",
		"openai:chat?model=gpt-4&baseURL=relative/path",
		"openai:chat?model=gpt-4&baseURL=javascript:alert(1)",
	} {
		_, err := comp.CreateEndpoint(invalid)
		if err == nil {
			t.Errorf("expected error for invalid baseURL %q, got nil", invalid)
		}
	}
}

func TestOpenAI_ReadsOpenAIAPIKeyEnvAndBlocksRedirectAndRejectsNilBody(t *testing.T) {
	t.Run("reads OPENAI_API_KEY env", func(t *testing.T) {
		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			gotAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"chatcmpl-1","choices":[{"message":{"content":"ok"}}]}`))
		}))
		t.Cleanup(srv.Close)

		t.Setenv("OPENAI_API_KEY", "env-openai-secret")
		ep, err := NewOpenAIComponent().CreateEndpoint("openai:chat?model=gpt-4&baseURL=" + url.QueryEscape(srv.URL+"/v1"))
		if err != nil {
			t.Fatalf("CreateEndpoint failed: %v", err)
		}
		prod, err := ep.CreateProducer()
		if err != nil {
			t.Fatalf("CreateProducer failed: %v", err)
		}
		if err := prod.Start(context.Background()); err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		t.Cleanup(func() { _ = prod.Stop() })

		ex := gocamel.NewExchange(context.Background())
		ex.SetBody("hello")
		if err := prod.(*OpenAIProducer).Send(ex); err != nil {
			t.Fatalf("Send failed: %v", err)
		}
		if gotAuth != "Bearer env-openai-secret" {
			t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer env-openai-secret")
		}
	})

	t.Run("blocks 307 redirect without leaking Authorization", func(t *testing.T) {
		var targetHits atomic.Int32
		var leakedAuth string
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			targetHits.Add(1)
			leakedAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"chatcmpl-1","choices":[{"message":{"content":"leaked"}}]}`))
		}))
		t.Cleanup(target.Close)

		redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL+"/v1/chat/completions", http.StatusTemporaryRedirect)
		}))
		t.Cleanup(redirector.Close)

		ep, err := NewOpenAIComponent().CreateEndpoint("openai:chat?model=gpt-4&apiKey=secret-openai-key&baseURL=" + url.QueryEscape(redirector.URL+"/v1"))
		if err != nil {
			t.Fatalf("CreateEndpoint failed: %v", err)
		}
		prod, err := ep.CreateProducer()
		if err != nil {
			t.Fatalf("CreateProducer failed: %v", err)
		}
		if err := prod.Start(context.Background()); err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		t.Cleanup(func() { _ = prod.Stop() })

		ex := gocamel.NewExchange(context.Background())
		ex.SetBody("hello")
		if err := prod.(*OpenAIProducer).Send(ex); err == nil {
			t.Fatal("expected error on 307 redirect, got nil")
		}
		if targetHits.Load() != 0 {
			t.Fatalf("redirect target was contacted %d times (leaked auth=%q)", targetHits.Load(), leakedAuth)
		}
	})

	t.Run("rejects nil body", func(t *testing.T) {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"chatcmpl-1","choices":[{"message":{"content":"ok"}}]}`))
		}))
		t.Cleanup(srv.Close)

		ep, err := NewOpenAIComponent().CreateEndpoint("openai:chat?model=gpt-4&apiKey=k&baseURL=" + url.QueryEscape(srv.URL+"/v1"))
		if err != nil {
			t.Fatalf("CreateEndpoint failed: %v", err)
		}
		prod, err := ep.CreateProducer()
		if err != nil {
			t.Fatalf("CreateProducer failed: %v", err)
		}
		if err := prod.Start(context.Background()); err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		t.Cleanup(func() { _ = prod.Stop() })

		ex := gocamel.NewExchange(context.Background())
		if err := prod.(*OpenAIProducer).Send(ex); err == nil {
			t.Fatal("expected error for nil body, got nil")
		}
		if hits.Load() != 0 {
			t.Fatalf("HTTP server should not be called for nil body, got %d hits", hits.Load())
		}
	})

	t.Run("rejects oversized response over 32 MiB", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"chatcmpl-1","choices":[{"message":{"content":"`))
			chunk := bytes.Repeat([]byte("a"), 1<<20)
			for i := 0; i < 33; i++ {
				if _, err := w.Write(chunk); err != nil {
					return
				}
			}
			_, _ = w.Write([]byte(`"}}]}`))
		}))
		t.Cleanup(srv.Close)

		ep, err := NewOpenAIComponent().CreateEndpoint("openai:chat?model=gpt-4&apiKey=k&baseURL=" + url.QueryEscape(srv.URL+"/v1"))
		if err != nil {
			t.Fatalf("CreateEndpoint failed: %v", err)
		}
		prod, err := ep.CreateProducer()
		if err != nil {
			t.Fatalf("CreateProducer failed: %v", err)
		}
		if err := prod.Start(context.Background()); err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		t.Cleanup(func() { _ = prod.Stop() })

		ex := gocamel.NewExchange(context.Background())
		ex.SetBody("hello")
		if err := prod.(*OpenAIProducer).Send(ex); err == nil {
			t.Fatal("expected error for oversized response, got nil")
		}
	})

	t.Run("rejects hostless and opaque baseURL", func(t *testing.T) {
		comp := NewOpenAIComponent()
		for _, bad := range []string{"http:///v1", "https://", "http:opaque"} {
			if _, err := comp.CreateEndpoint("openai:chat?model=gpt-4&baseURL=" + url.QueryEscape(bad)); err == nil {
				t.Errorf("expected error for baseURL=%q", bad)
			}
		}
	})

	t.Run("requires API key when OPENAI_API_KEY is unset and rejects CreateConsumer", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "")
		ep, err := NewOpenAIComponent().CreateEndpoint("openai:chat?model=gpt-4")
		if err != nil {
			t.Fatalf("CreateEndpoint failed: %v", err)
		}
		if _, err := ep.CreateConsumer(nil); err == nil {
			t.Fatal("expected CreateConsumer error, got nil")
		}
		prod, err := ep.CreateProducer()
		if err != nil {
			t.Fatalf("CreateProducer failed: %v", err)
		}
		if err := prod.Start(context.Background()); err == nil {
			t.Fatal("expected Start error when API key is missing, got nil")
		}
	})

	t.Run("rejects invalid Content-Length and direct endpoint bad baseURL", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Length", strconv.FormatInt(maxResponseBodyBytes+1, 10))
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(srv.Close)

		ep, err := NewOpenAIComponent().CreateEndpoint("openai:chat?model=gpt-4&apiKey=k&baseURL=" + url.QueryEscape(srv.URL+"/v1"))
		if err != nil {
			t.Fatalf("CreateEndpoint failed: %v", err)
		}
		prod, err := ep.CreateProducer()
		if err != nil {
			t.Fatalf("CreateProducer failed: %v", err)
		}
		if err := prod.Start(context.Background()); err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		t.Cleanup(func() { _ = prod.Stop() })

		ex := gocamel.NewExchange(context.Background())
		ex.SetBody("hello")
		if err := prod.(*OpenAIProducer).Send(ex); err == nil {
			t.Fatal("expected error for Content-Length > 32 MiB")
		}

		badU, _ := url.Parse("openai:chat?apiKey=k&baseURL=" + url.QueryEscape("http:///bad"))
		pBad := &OpenAIProducer{endpoint: &OpenAIEndpoint{uri: badU.String(), url: badU}}
		if err := pBad.Start(context.Background()); err == nil {
			t.Fatal("expected error for direct endpoint with hostless baseURL")
		}

		rcExact := &limitedReadCloser{rc: io.NopCloser(strings.NewReader("ok")), remaining: 2, limit: 2}
		if n, err := rcExact.Read(nil); err != nil || n != 0 {
			t.Fatalf("zero-len Read = (%d, %v), want (0, nil)", n, err)
		}
		b, err := io.ReadAll(rcExact)
		if err != nil || string(b) != "ok" {
			t.Fatalf("exact ReadAll = (%q, %v), want (\"ok\", nil)", b, err)
		}
		rcOver := &limitedReadCloser{rc: io.NopCloser(strings.NewReader("ok!")), remaining: 2, limit: 2}
		if _, err := io.ReadAll(rcOver); err == nil {
			t.Fatal("expected error on over-limit read")
		}
		if _, err := rcOver.Read(make([]byte, 2)); err == nil {
			t.Fatal("expected repeated error after limit exceeded")
		}
	})
}
