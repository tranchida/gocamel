package openai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/sashabaranov/go-openai"

	gocamel "github.com/tranchida/gocamel"
)

const maxResponseBodyBytes = gocamel.DefaultMaxBodySize

// OpenAIComponent represents the OpenAI component
type OpenAIComponent struct{}

// NewOpenAIComponent creates a new OpenAIComponent
func NewOpenAIComponent() *OpenAIComponent {
	return &OpenAIComponent{}
}

type openAIOptions struct {
	baseURL string
	model   string
}

func parseOpenAIOptions(u *url.URL) (openAIOptions, error) {
	if u == nil {
		return openAIOptions{model: openai.GPT3Dot5Turbo}, nil
	}
	baseURL := gocamel.GetConfigValue(u, "baseURL")
	if baseURL != "" {
		if err := validateBaseURL(baseURL); err != nil {
			return openAIOptions{}, fmt.Errorf("invalid openai baseURL: %w", err)
		}
	}
	model := gocamel.GetConfigValue(u, "model")
	if model == "" {
		model = openai.GPT3Dot5Turbo // default model
	}
	return openAIOptions{baseURL: baseURL, model: model}, nil
}

// CreateEndpoint creates a new endpoint OpenAI
func (c *OpenAIComponent) CreateEndpoint(uri string) (gocamel.Endpoint, error) {
	u, err := gocamel.ParseURI(uri)
	if err != nil {
		return nil, err
	}
	opts, err := parseOpenAIOptions(u)
	if err != nil {
		return nil, err
	}

	return &OpenAIEndpoint{
		uri:             uri,
		url:             u,
		comp:            c,
		baseURL:         opts.baseURL,
		model:           opts.model,
		optionsResolved: true,
	}, nil
}

// OpenAIEndpoint represents a OpenAI endpoint
type OpenAIEndpoint struct {
	uri             string
	url             *url.URL
	comp            *OpenAIComponent
	baseURL         string
	model           string
	optionsResolved bool
}

func (e *OpenAIEndpoint) resolvedOptions() (openAIOptions, error) {
	if e.optionsResolved {
		return openAIOptions{baseURL: e.baseURL, model: e.model}, nil
	}
	return parseOpenAIOptions(e.url)
}

// URI returns the endpoint URI, with sensitive credentials
// (authorizationToken, apiKey) masked for safe logging.
func (e *OpenAIEndpoint) URI() string {
	return gocamel.RedactURI(e.uri)
}

// CreateProducer creates an OpenAI producer.
func (e *OpenAIEndpoint) CreateProducer() (gocamel.Producer, error) {
	return &OpenAIProducer{
		endpoint: e,
	}, nil
}

// CreateConsumer returns an error: OpenAI component supports Producer mode only.
func (e *OpenAIEndpoint) CreateConsumer(processor gocamel.Processor) (gocamel.Consumer, error) {
	return nil, fmt.Errorf("the OpenAI component does not support Consumer mode (Producer only)")
}

// OpenAIProducer represents an OpenAI producer.
type OpenAIProducer struct {
	endpoint   *OpenAIEndpoint
	mu         sync.RWMutex
	httpClient *http.Client
	client     *openai.Client
	model      string
	started    bool
}

func (p *OpenAIProducer) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	token := gocamel.GetConfigValue(p.endpoint.url, "authorizationToken")
	if token == "" {
		token = gocamel.GetConfigValue(p.endpoint.url, "apiKey") // alias
	}
	if token == "" {
		token = os.Getenv("OPENAI_API_KEY")
	}
	if token == "" {
		return fmt.Errorf("authorizationToken (or apiKey, or OPENAI_API_KEY) missing for OpenAI")
	}

	opts, err := p.endpoint.resolvedOptions()
	if err != nil {
		return err
	}

	p.httpClient = newSafeHTTPClient(60 * time.Second)
	cfg := openai.DefaultConfig(token)
	cfg.HTTPClient = p.httpClient
	if opts.baseURL != "" {
		cfg.BaseURL = opts.baseURL
	}
	p.client = openai.NewClientWithConfig(cfg)
	p.model = opts.model
	p.started = true

	return nil
}

func (p *OpenAIProducer) Stop() error {
	p.mu.Lock()
	httpClient := p.httpClient
	p.httpClient = nil
	p.client = nil
	p.started = false
	p.mu.Unlock()
	if httpClient != nil {
		httpClient.CloseIdleConnections()
	}
	return nil
}

func (p *OpenAIProducer) Send(exchange *gocamel.Exchange) error {
	p.mu.RLock()
	client := p.client
	model := p.model
	p.mu.RUnlock()

	if client == nil {
		return fmt.Errorf("the OpenAI producer has not been started")
	}

	rawBody := exchange.GetIn().GetBody()
	if rawBody == nil {
		return fmt.Errorf("exchange body cannot be nil")
	}
	var prompt string
	switch body := rawBody.(type) {
	case string:
		prompt = body
	case []byte:
		prompt = string(body)
	default:
		prompt = fmt.Sprintf("%v", body)
	}

	reqCtx := exchange.Context
	if reqCtx == nil {
		reqCtx = context.Background()
	}
	resp, err := client.CreateChatCompletion(
		reqCtx,
		openai.ChatCompletionRequest{
			Model: model,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: prompt,
				},
			},
		},
	)

	if err != nil {
		return fmt.Errorf("OpenAI request error: %w", err)
	}

	if len(resp.Choices) > 0 {
		exchange.GetOut().SetBody(resp.Choices[0].Message.Content)
		// Optional: copy certain response headers
		exchange.GetOut().SetHeader("OpenAIUsageTotalTokens", resp.Usage.TotalTokens)
	} else {
		return fmt.Errorf("OpenAI returned no response")
	}

	return nil
}

// validateBaseURL rejects non-absolute URLs, hostless/opaque URLs, and
// non-http(s) schemes to prevent SSRF-style misconfiguration via
// attacker-controlled input.
func validateBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	if !u.IsAbs() {
		return fmt.Errorf("baseURL must be absolute: %s", raw)
	}
	scheme := u.Scheme
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("baseURL scheme must be http or https, got %q", scheme)
	}
	if u.Hostname() == "" || u.Opaque != "" {
		return fmt.Errorf("baseURL must include a host: %s", raw)
	}
	return nil
}

func newSafeHTTPClient(timeout time.Duration) *http.Client {
	var base http.RoundTripper
	if dt, ok := http.DefaultTransport.(*http.Transport); ok {
		base = dt.Clone()
	} else if http.DefaultTransport != nil {
		base = http.DefaultTransport
	} else {
		base = &http.Transport{}
	}
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &limitedResponseTransport{
			base:     base,
			maxBytes: maxResponseBodyBytes,
		},
	}
}

type limitedResponseTransport struct {
	base     http.RoundTripper
	maxBytes int64
}

func (t *limitedResponseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.ContentLength > t.maxBytes {
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, fmt.Errorf("response body exceeds the %d byte limit", t.maxBytes)
	}
	if resp.Body != nil && resp.Body != http.NoBody {
		resp.Body = &limitedReadCloser{
			rc:        resp.Body,
			remaining: t.maxBytes,
			limit:     t.maxBytes,
		}
	}
	return resp, nil
}

func (t *limitedResponseTransport) CloseIdleConnections() {
	type idleCloser interface {
		CloseIdleConnections()
	}
	if ic, ok := t.base.(idleCloser); ok {
		ic.CloseIdleConnections()
	}
}

type limitedReadCloser struct {
	rc        io.ReadCloser
	remaining int64
	limit     int64
	exceeded  bool
}

func (l *limitedReadCloser) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if l.exceeded {
		return 0, fmt.Errorf("response body exceeds the %d byte limit", l.limit)
	}
	if l.remaining <= 0 {
		var probe [1]byte
		n, err := l.rc.Read(probe[:])
		if n > 0 {
			l.exceeded = true
			return 0, fmt.Errorf("response body exceeds the %d byte limit", l.limit)
		}
		if err != nil {
			return 0, err
		}
		return 0, io.EOF
	}
	if int64(len(p)) > l.remaining {
		p = p[:l.remaining]
	}
	n, err := l.rc.Read(p)
	l.remaining -= int64(n)
	return n, err
}

func (l *limitedReadCloser) Close() error {
	return l.rc.Close()
}
