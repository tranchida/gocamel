package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/ollama/ollama/api"

	gocamel "github.com/tranchida/gocamel"
)

// CloudHost is the base URL of the Ollama Cloud API.
const CloudHost = "https://ollama.com"

// cloudAPIKeyEnv is the environment variable holding the Ollama Cloud API key.
// The key is read from the environment only (never from the URI) so that it
// cannot leak through endpoint URIs, logs or route dumps.
const cloudAPIKeyEnv = "OLLAMA_API_KEY" // #nosec G101 -- environment variable name, not a credential

// ollamaAPI is the subset of the Ollama API used by the producer. It is
// satisfied both by the official *api.Client (local servers) and by
// cloudClient (Ollama Cloud with an API key).
type ollamaAPI interface {
	Chat(ctx context.Context, req *api.ChatRequest, fn api.ChatResponseFunc) error
	Generate(ctx context.Context, req *api.GenerateRequest, fn api.GenerateResponseFunc) error
	Embed(ctx context.Context, req *api.EmbedRequest) (*api.EmbedResponse, error)
}

var _ ollamaAPI = (*api.Client)(nil)
var _ ollamaAPI = (*cloudClient)(nil)

// cloudClient talks to the Ollama Cloud API using a Bearer API key.
//
// The official api.Client signs every request with the local Ollama private
// key when the host is ollama.com, which fails on machines without an
// `ollama signin` setup. Ollama Cloud API keys are sent as a standard Bearer
// token instead, so this minimal client bypasses the signing path. Only the
// non-streaming endpoints used by the producer are implemented.
type cloudClient struct {
	base   *url.URL
	http   *http.Client
	apiKey string
}

// newCloudClient creates a cloudClient for the given base URL.
func newCloudClient(base *url.URL, httpClient *http.Client, apiKey string) *cloudClient {
	return &cloudClient{base: base, http: httpClient, apiKey: apiKey}
}

// Chat sends a non-streaming chat request and invokes fn once with the response.
func (c *cloudClient) Chat(ctx context.Context, req *api.ChatRequest, fn api.ChatResponseFunc) error {
	var resp api.ChatResponse
	if err := c.post(ctx, "/api/chat", req, &resp); err != nil {
		return err
	}
	return fn(resp)
}

// Generate sends a non-streaming generate request and invokes fn once with the response.
func (c *cloudClient) Generate(ctx context.Context, req *api.GenerateRequest, fn api.GenerateResponseFunc) error {
	var resp api.GenerateResponse
	if err := c.post(ctx, "/api/generate", req, &resp); err != nil {
		return err
	}
	return fn(resp)
}

// Embed sends an embedding request.
func (c *cloudClient) Embed(ctx context.Context, req *api.EmbedRequest) (*api.EmbedResponse, error) {
	var resp api.EmbedResponse
	if err := c.post(ctx, "/api/embed", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// post performs an authenticated JSON POST and decodes the JSON response.
func (c *cloudClient) post(ctx context.Context, path string, reqBody, respBody any) error {
	data, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal ollama request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base.JoinPath(path).String(), bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create ollama request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := gocamel.ReadLimited(resp.Body, maxResponseBytes)
	if err != nil {
		return fmt.Errorf("read ollama response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		var apiErr struct {
			Error string `json:"error"`
		}
		msg := string(body)
		if json.Unmarshal(body, &apiErr) == nil && apiErr.Error != "" {
			msg = apiErr.Error
		}
		return api.StatusError{
			StatusCode:   resp.StatusCode,
			Status:       resp.Status,
			ErrorMessage: truncateErrorSnippet(msg, maxErrorBytes),
		}
	}
	if err := json.Unmarshal(body, respBody); err != nil {
		return fmt.Errorf("decode ollama response: %w", err)
	}
	return nil
}

const (
	// maxResponseBytes bounds the size of a single Ollama Cloud response (DoS guard).
	maxResponseBytes = gocamel.DefaultMaxBodySize
	// maxErrorBytes caps upstream error snippets included in StatusError.
	maxErrorBytes = 512
)

func truncateErrorSnippet(s string, maxBytes int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxBytes {
		return s
	}
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}

// isCloudHost reports whether the API key may be sent to host. It is a
// variable so that tests can point the cloud client at a local httptest server.
var isCloudHost = isOllamaCloudHost

// isOllamaCloudHost reports whether host is ollama.com or one of its
// subdomains. OLLAMA_API_KEY is sent to these hosts only: any other host,
// including a local or LAN Ollama server, uses the standard client and never
// receives the key.
func isOllamaCloudHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	return host == "ollama.com" || strings.HasSuffix(host, ".ollama.com")
}

// requireSecureKeyTransport ensures the API key is never sent in cleartext.
// Plain HTTP is tolerated only towards loopback addresses (local test servers).
func requireSecureKeyTransport(base *url.URL) error {
	if base.Scheme == "https" {
		return nil
	}
	if base.Scheme == "http" && isLoopbackHost(base.Hostname()) {
		return nil
	}
	return fmt.Errorf("refusing to send %s over %q: use an https host", cloudAPIKeyEnv, base.Scheme)
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
