package anthropic

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	gocamel "gitlab.com/tranchida/gocamel"
)

// AnthropicComponent represents the Anthropic component (Claude Messages API).
type AnthropicComponent struct{}

// NewAnthropicComponent creates a new AnthropicComponent.
func NewAnthropicComponent() *AnthropicComponent {
	return &AnthropicComponent{}
}

// CreateEndpoint creates a new Anthropic endpoint from a URI.
//
// URI scheme: anthropic:messages?model=claude-sonnet-4-5&apiKey=...
//
// Supported options (via gocamel.GetConfigValue):
//   - apiKey             (required) API key (alias: authorizationToken). Masked by RedactURI.
//   - model              (required) Model name, e.g. claude-sonnet-4-5.
//   - maxTokens          (default 1024) Maximum tokens to generate.
//   - system             (optional) System prompt.
//   - temperature        (optional) 0.0-1.0.
//   - baseURL            (optional) Override API base URL (proxies, Bedrock-compatible gateways).
func (c *AnthropicComponent) CreateEndpoint(uri string) (gocamel.Endpoint, error) {
	u, err := gocamel.ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid anthropic URI: %w", err)
	}
	if baseURL := gocamel.GetConfigValue(u, "baseURL"); baseURL != "" {
		if err := validateBaseURL(baseURL); err != nil {
			return nil, fmt.Errorf("invalid anthropic baseURL: %w", err)
		}
	}
	return &AnthropicEndpoint{
		uri:  uri,
		url:  u,
		comp: c,
	}, nil
}

// AnthropicEndpoint represents an Anthropic endpoint.
type AnthropicEndpoint struct {
	uri  string
	url  *url.URL
	comp *AnthropicComponent
}

// URI returns the endpoint URI with credentials (apiKey, authorizationToken)
// masked for safe logging.
func (e *AnthropicEndpoint) URI() string {
	return gocamel.RedactURI(e.uri)
}

// CreateProducer creates an Anthropic producer.
func (e *AnthropicEndpoint) CreateProducer() (gocamel.Producer, error) {
	return &AnthropicProducer{endpoint: e}, nil
}

// CreateConsumer returns an error: the Anthropic component supports Producer mode only.
func (e *AnthropicEndpoint) CreateConsumer(processor gocamel.Processor) (gocamel.Consumer, error) {
	return nil, fmt.Errorf("the Anthropic component does not support Consumer mode (Producer only)")
}

// AnthropicProducer represents an Anthropic producer.
type AnthropicProducer struct {
	endpoint  *AnthropicEndpoint
	client    anthropic.Client
	model     anthropic.Model
	maxTokens int64
	system    string
	started   bool
}

func (p *AnthropicProducer) Start(ctx context.Context) error {
	u := p.endpoint.url

	token := gocamel.GetConfigValue(u, "authorizationToken")
	if token == "" {
		token = gocamel.GetConfigValue(u, "apiKey") // alias
		if token == "" {
			return fmt.Errorf("apiKey (or authorizationToken) missing for Anthropic")
		}
	}

	model := gocamel.GetConfigValue(u, "model")
	if model == "" {
		return fmt.Errorf("model is required for Anthropic (e.g. claude-sonnet-4-5)")
	}
	p.model = anthropic.Model(model)

	maxTokens := int64(1024)
	if v := gocamel.GetConfigValue(u, "maxTokens"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			return fmt.Errorf("invalid maxTokens %q: must be a positive integer", v)
		}
		maxTokens = n
	}
	p.maxTokens = maxTokens

	p.system = gocamel.GetConfigValue(u, "system")

	opts := []option.RequestOption{
		option.WithAPIKey(token),
		option.WithHTTPClient(&http.Client{Timeout: 60 * time.Second}),
	}
	if baseURL := gocamel.GetConfigValue(u, "baseURL"); baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	p.client = anthropic.NewClient(opts...)
	p.started = true
	return nil
}

func (p *AnthropicProducer) Stop() error {
	return nil
}

// Send calls the Anthropic Messages API with the exchange body as the user
// prompt and writes the first text block of the response to the Out message.
//
// Per-message overrides via exchange headers:
//   - AnthropicSystem       overrides the URI system prompt
//   - AnthropicModel        overrides the URI model
//   - AnthropicMaxTokens    overrides the URI maxTokens
//   - AnthropicTemperature  overrides the URI temperature
func (p *AnthropicProducer) Send(exchange *gocamel.Exchange) error {
	if !p.started {
		return fmt.Errorf("the Anthropic producer has not been started")
	}

	prompt := bodyAsString(exchange.GetIn().GetBody())

	system := p.system
	if v, ok := getStringHeader(exchange, "AnthropicSystem"); ok && v != "" {
		system = v
	}

	model := p.model
	if v, ok := getStringHeader(exchange, "AnthropicModel"); ok && v != "" {
		model = anthropic.Model(v)
	}

	maxTokens := p.maxTokens
	if v, ok := getStringHeader(exchange, "AnthropicMaxTokens"); ok && v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			maxTokens = n
		}
	}

	params := anthropic.MessageNewParams{
		Model:     model,
		MaxTokens: maxTokens,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	}
	if system != "" {
		params.System = []anthropic.TextBlockParam{{Text: system}}
	}
	if v, ok := getStringHeader(exchange, "AnthropicTemperature"); ok && v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			params.Temperature = anthropic.Float(f)
		}
	}

	reqCtx := exchange.Context
	if reqCtx == nil {
		reqCtx = context.Background()
	}
	resp, err := p.client.Messages.New(reqCtx, params)
	if err != nil {
		return fmt.Errorf("request error from Anthropic: %w", err)
	}

	out := exchange.GetOut()
	for _, block := range resp.Content {
		if block.Type == "text" && block.Text != "" {
			out.SetBody(block.Text)
			out.SetHeader("AnthropicUsageInputTokens", resp.Usage.InputTokens)
			out.SetHeader("AnthropicUsageOutputTokens", resp.Usage.OutputTokens)
			out.SetHeader("AnthropicStopReason", string(resp.StopReason))
			out.SetHeader("AnthropicModel", resp.Model)
			return nil
		}
	}
	return fmt.Errorf("no text content returned by Anthropic")
}

// getStringHeader returns a string-typed exchange In header, treating
// []byte and fmt.Stringer values as strings for convenience.
func getStringHeader(exchange *gocamel.Exchange, key string) (string, bool) {
	v, ok := exchange.GetIn().GetHeader(key)
	if !ok {
		return "", false
	}
	switch s := v.(type) {
	case string:
		return s, true
	case []byte:
		return string(s), true
	}
	return "", false
}

// bodyAsString converts the exchange body to a prompt string.
func bodyAsString(body any) string {
	switch v := body.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// validateBaseURL rejects non-absolute URLs and non-http(s) schemes to
// preventSSRF-style misconfiguration via attacker-controlled headers.
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
