package openai

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/sashabaranov/go-openai"

	gocamel "github.com/tranchida/gocamel"
)

// OpenAIComponent represents the OpenAI component
type OpenAIComponent struct{}

// NewOpenAIComponent creates a new OpenAIComponent
func NewOpenAIComponent() *OpenAIComponent {
	return &OpenAIComponent{}
}

// CreateEndpoint creates a new endpoint OpenAI
func (c *OpenAIComponent) CreateEndpoint(uri string) (gocamel.Endpoint, error) {
	u, err := gocamel.ParseURI(uri)
	if err != nil {
		return nil, err
	}
	if baseURL := gocamel.GetConfigValue(u, "baseURL"); baseURL != "" {
		if err := validateBaseURL(baseURL); err != nil {
			return nil, fmt.Errorf("invalid openai baseURL: %w", err)
		}
	}

	return &OpenAIEndpoint{
		uri:  uri,
		url:  u,
		comp: c,
	}, nil
}

// OpenAIEndpoint represents a OpenAI endpoint
type OpenAIEndpoint struct {
	uri  string
	url  *url.URL
	comp *OpenAIComponent
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
		if token == "" {
			return fmt.Errorf("authorizationToken (or apiKey) missing for OpenAI")
		}
	}

	model := gocamel.GetConfigValue(p.endpoint.url, "model")
	if model == "" {
		model = openai.GPT3Dot5Turbo // default model
	}

	p.httpClient = &http.Client{Timeout: 60 * time.Second}
	cfg := openai.DefaultConfig(token)
	cfg.HTTPClient = p.httpClient
	if baseURL := gocamel.GetConfigValue(p.endpoint.url, "baseURL"); baseURL != "" {
		cfg.BaseURL = baseURL
	}
	p.client = openai.NewClientWithConfig(cfg)
	p.model = model
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

	var prompt string
	switch body := exchange.GetIn().GetBody().(type) {
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

// validateBaseURL rejects non-absolute URLs and non-http(s) schemes to
// prevent SSRF-style misconfiguration via attacker-controlled input.
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
	return nil
}
