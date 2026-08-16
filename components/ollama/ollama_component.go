package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/ollama/ollama/api"

	gocamel "gitlab.com/tranchida/gocamel"
)

// OllamaComponent represents the Ollama component (local LLM server).
type OllamaComponent struct{}

// NewOllamaComponent creates a new OllamaComponent.
func NewOllamaComponent() *OllamaComponent {
	return &OllamaComponent{}
}

// CreateEndpoint creates a new Ollama endpoint from a URI.
//
// URI scheme: ollama:<mode>?model=...&host=http://localhost:11434
//
// Modes (selected by the URI path, like openai:chat):
//   - chat     (default) — multi-turn chat API (/api/chat)
//   - generate           — one-shot generation (/api/generate)
//   - embed              — embeddings (/api/embed)
//
// Common options (via gocamel.GetConfigValue):
//   - host       (default http://localhost:11434) Ollama server base URL.
//   - model      (required in chat/generate, optional in embed) Model name.
//   - system     (optional, generate) System prompt.
//   - stream     (default false) Streaming responses. Disabled for pipeline
//                determinism in v1.
//   - keepAlive  (optional duration, e.g. "5m") Model retention in memory.
//   - think      (optional bool / "low"|"medium"|"high"|"max") Reasoning effort.
//   - format     (optional, e.g. "json") Structured output format.
func (c *OllamaComponent) CreateEndpoint(uri string) (gocamel.Endpoint, error) {
	u, err := gocamel.ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid ollama URI: %w", err)
	}
	mode := u.Opaque
	if mode == "" {
		mode = "chat"
	}
	switch mode {
	case "chat", "generate", "embed":
	default:
		return nil, fmt.Errorf("invalid ollama mode %q: must be chat, generate or embed", mode)
	}
	if host := gocamel.GetConfigValue(u, "host"); host != "" {
		if err := validateHostURL(host); err != nil {
			return nil, fmt.Errorf("invalid ollama host: %w", err)
		}
	}
	if ka := gocamel.GetConfigValue(u, "keepAlive"); ka != "" {
		if _, err := time.ParseDuration(ka); err != nil {
			return nil, fmt.Errorf("invalid keepAlive %q: %w", ka, err)
		}
	}
	return &OllamaEndpoint{uri: uri, url: u, comp: c}, nil
}

// OllamaEndpoint represents an Ollama endpoint.
type OllamaEndpoint struct {
	uri  string
	url  *url.URL
	comp *OllamaComponent
}

// URI returns the endpoint URI. Ollama URIs carry no credentials, so no
// redaction is needed; we return the value unchanged for parity with other
// components.
func (e *OllamaEndpoint) URI() string {
	return e.uri
}

// CreateProducer creates an Ollama producer.
func (e *OllamaEndpoint) CreateProducer() (gocamel.Producer, error) {
	return &OllamaProducer{endpoint: e}, nil
}

// CreateConsumer returns an error: Ollama supports Producer mode only.
// (Model lifecycle operations like Pull/List are out of scope for the
// pipeline Producer contract and would belong in a separate admin tool.)
func (e *OllamaEndpoint) CreateConsumer(processor gocamel.Processor) (gocamel.Consumer, error) {
	return nil, fmt.Errorf("the Ollama component does not support Consumer mode (Producer only)")
}

// OllamaProducer represents an Ollama producer.
type OllamaProducer struct {
	endpoint  *OllamaEndpoint
	client    *api.Client
	model     string
	mode      string
	system    string
	keepAlive string
	think     string
	format    string
	started   bool
}

func (p *OllamaProducer) Start(ctx context.Context) error {
	u := p.endpoint.url

	mode := u.Opaque
	if mode == "" {
		mode = "chat"
	}
	p.mode = mode

	p.model = gocamel.GetConfigValue(u, "model")
	if p.model == "" && mode != "embed" {
		return fmt.Errorf("model is required for ollama in %s mode", mode)
	}

	host := gocamel.GetConfigValue(u, "host")
	var base *url.URL
	var err error
	if host != "" {
		base, err = url.Parse(host)
		if err != nil {
			return fmt.Errorf("invalid ollama host: %w", err)
		}
	} else {
		// Default to the standard local server; ClientFromEnvironment would
		// also honor OLLAMA_HOST but an explicit URL keeps tests deterministic.
		base, err = url.Parse("http://localhost:11434")
		if err != nil {
			return fmt.Errorf("invalid default host: %w", err)
		}
	}
	p.client = api.NewClient(base, &http.Client{Timeout: 60 * time.Second})

	p.system = gocamel.GetConfigValue(u, "system")
	p.keepAlive = gocamel.GetConfigValue(u, "keepAlive")
	p.think = gocamel.GetConfigValue(u, "think")
	p.format = gocamel.GetConfigValue(u, "format")
	p.started = true
	return nil
}

func (p *OllamaProducer) Stop() error {
	return nil
}

// Send dispatches the exchange to the configured Ollama API mode.
func (p *OllamaProducer) Send(exchange *gocamel.Exchange) error {
	if !p.started {
		return fmt.Errorf("the Ollama producer has not been started")
	}
	switch p.mode {
	case "chat":
		return p.sendChat(exchange)
	case "generate":
		return p.sendGenerate(exchange)
	case "embed":
		return p.sendEmbed(exchange)
	default:
		return fmt.Errorf("unknown ollama mode %q", p.mode)
	}
}

func (p *OllamaProducer) sendChat(exchange *gocamel.Exchange) error {
	prompt := bodyAsString(exchange.GetIn().GetBody())
	model := p.resolveModel(exchange)

	req := &api.ChatRequest{
		Model:    model,
		Messages: []api.Message{{Role: "user", Content: prompt}},
		Stream:   boolPtr(false),
	}
	p.applyOptions(req)
	if v, ok := getStringHeader(exchange, "OllamaSystem"); ok {
		req.Messages = append([]api.Message{{Role: "system", Content: v}}, req.Messages...)
	}

	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}
	var resp api.ChatResponse
	err := p.client.Chat(ctx, req, func(r api.ChatResponse) error {
		resp = r
		return nil
	})
	if err != nil {
		return fmt.Errorf("ollama chat error: %w", err)
	}
	if resp.Message.Content == "" {
		return fmt.Errorf("ollama returned no message content")
	}
	out := exchange.GetOut()
	out.SetBody(resp.Message.Content)
	out.SetHeader("OllamaModel", resp.Model)
	out.SetHeader("OllamaDoneReason", resp.DoneReason)
	out.SetHeader("OllamaTotalDuration", resp.TotalDuration)
	out.SetHeader("OllamaPromptEvalCount", resp.PromptEvalCount)
	out.SetHeader("OllamaEvalCount", resp.EvalCount)
	return nil
}

func (p *OllamaProducer) sendGenerate(exchange *gocamel.Exchange) error {
	prompt := bodyAsString(exchange.GetIn().GetBody())
	model := p.resolveModel(exchange)
	system := p.system
	if v, ok := getStringHeader(exchange, "OllamaSystem"); ok {
		system = v
	}

	req := &api.GenerateRequest{
		Model:  model,
		Prompt: prompt,
		Stream: boolPtr(false),
	}
	p.applyOptions(req)
	if system != "" {
		req.System = system
	}

	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}
	var resp api.GenerateResponse
	err := p.client.Generate(ctx, req, func(r api.GenerateResponse) error {
		resp = r
		return nil
	})
	if err != nil {
		return fmt.Errorf("ollama generate error: %w", err)
	}
	if resp.Response == "" {
		return fmt.Errorf("ollama returned no generate content")
	}
	out := exchange.GetOut()
	out.SetBody(resp.Response)
	out.SetHeader("OllamaModel", resp.Model)
	out.SetHeader("OllamaDoneReason", resp.DoneReason)
	out.SetHeader("OllamaTotalDuration", resp.TotalDuration)
	out.SetHeader("OllamaPromptEvalCount", resp.PromptEvalCount)
	out.SetHeader("OllamaEvalCount", resp.EvalCount)
	return nil
}

func (p *OllamaProducer) sendEmbed(exchange *gocamel.Exchange) error {
	input := bodyAsString(exchange.GetIn().GetBody())
	model := p.resolveModel(exchange)

	req := &api.EmbedRequest{
		Model: model,
		Input: input,
	}
	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}
	resp, err := p.client.Embed(ctx, req)
	if err != nil {
		return fmt.Errorf("ollama embed error: %w", err)
	}
	if len(resp.Embeddings) == 0 {
		return fmt.Errorf("ollama returned no embeddings")
	}
	out := exchange.GetOut()
	out.SetBody(resp.Embeddings[0])
	out.SetHeader("OllamaModel", model)
	out.SetHeader("OllamaEmbeddingsCount", len(resp.Embeddings))
	return nil
}

// resolveModel returns the effective model, allowing per-message override
// via the OllamaModel header.
func (p *OllamaProducer) resolveModel(exchange *gocamel.Exchange) string {
	if v, ok := getStringHeader(exchange, "OllamaModel"); ok && v != "" {
		return v
	}
	return p.model
}

// applyOptions mutates the request to carry keepAlive/think/format when set.
func (p *OllamaProducer) applyOptions(req any) {
	if p.keepAlive == "" && p.think == "" && p.format == "" {
		return
	}
	if ka, err := time.ParseDuration(p.keepAlive); err == nil {
		switch r := req.(type) {
		case *api.ChatRequest:
			d := api.Duration{Duration: ka}
			r.KeepAlive = &d
		case *api.GenerateRequest:
			d := api.Duration{Duration: ka}
			r.KeepAlive = &d
		}
	}
	if p.think != "" {
		tv := parseThinkValue(p.think)
		switch r := req.(type) {
		case *api.ChatRequest:
			r.Think = &tv
		case *api.GenerateRequest:
			r.Think = &tv
		}
	}
	if p.format != "" {
		switch r := req.(type) {
		case *api.ChatRequest:
			r.Format = json.RawMessage(strconv.Quote(p.format))
		case *api.GenerateRequest:
			r.Format = json.RawMessage(strconv.Quote(p.format))
		}
	}
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

// getStringHeader returns a string-typed exchange In header.
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

func boolPtr(b bool) *bool { return &b }

// parseThinkValue maps the URI string to the Ollama ThinkValue.
// Accepted values: "true", "false", "low", "medium", "high", "max".
func parseThinkValue(s string) api.ThinkValue {
	switch s {
	case "true":
		return api.ThinkValue{Value: true}
	case "false":
		return api.ThinkValue{Value: false}
	case "low", "medium", "high", "max":
		return api.ThinkValue{Value: s}
	default:
		return api.ThinkValue{Value: true}
	}
}

// validateHostURL rejects non-absolute URLs and non-http(s) schemes.
func validateHostURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	if !u.IsAbs() {
		return fmt.Errorf("host must be absolute: %s", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("host scheme must be http or https, got %q", u.Scheme)
	}
	return nil
}