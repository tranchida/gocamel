package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ollama/ollama/api"

	gocamel "github.com/tranchida/gocamel"
)

const maxResponseBodyBytes = gocamel.DefaultMaxBodySize

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
//     Use https://ollama.com for Ollama Cloud (requires OLLAMA_API_KEY).
//   - OLLAMA_API_KEY (environment variable, not a URI option) Ollama Cloud
//     API key. It is sent, as a Bearer token over HTTPS, only when host is
//     ollama.com or one of its subdomains; other hosts never receive it.
//   - model      (required in chat/generate, optional in embed) Model name.
//   - system     (optional, generate) System prompt.
//   - stream     (default false) Streaming responses. Disabled for pipeline
//     determinism in v1.
//   - keepAlive  (optional duration, e.g. "5m") Model retention in memory.
//   - think      (optional bool / "low"|"medium"|"high"|"max") Reasoning effort.
//   - format     (optional, e.g. "json") Structured output format.
//   - allowHeaderOverride (default false) true to let the OllamaModel and
//     OllamaSystem headers replace the configured model and system prompt.
type ollamaOptions struct {
	mode                string
	baseURL             *url.URL
	model               string
	system              string
	keepAlive           time.Duration
	hasKeepAlive        bool
	think               *api.ThinkValue
	format              string
	allowHeaderOverride bool
}

func parseOllamaOptions(u *url.URL) (ollamaOptions, error) {
	if u == nil {
		return ollamaOptions{}, fmt.Errorf("invalid ollama endpoint: missing URL")
	}
	mode := resolveMode(u)
	switch mode {
	case "chat", "generate", "embed":
	default:
		return ollamaOptions{}, fmt.Errorf("invalid ollama mode %q: must be chat, generate or embed", mode)
	}

	var baseURL *url.URL
	var err error
	if host := gocamel.GetConfigValue(u, "host"); host != "" {
		if err := validateHostURL(host); err != nil {
			return ollamaOptions{}, fmt.Errorf("invalid ollama host: %w", err)
		}
		baseURL, err = url.Parse(host)
		if err != nil {
			return ollamaOptions{}, fmt.Errorf("invalid ollama host: %w", err)
		}
	} else {
		// Default to the standard local server; ClientFromEnvironment would
		// also honor OLLAMA_HOST but an explicit URL keeps tests deterministic.
		baseURL, err = url.Parse("http://localhost:11434")
		if err != nil {
			return ollamaOptions{}, fmt.Errorf("invalid default host: %w", err)
		}
	}

	var keepAlive time.Duration
	var hasKeepAlive bool
	if ka := gocamel.GetConfigValue(u, "keepAlive"); ka != "" {
		d, err := time.ParseDuration(ka)
		if err != nil {
			return ollamaOptions{}, fmt.Errorf("invalid keepAlive %q: %w", ka, err)
		}
		keepAlive = d
		hasKeepAlive = true
	}

	var thinkVal *api.ThinkValue
	if t := gocamel.GetConfigValue(u, "think"); t != "" {
		tv, err := parseThinkValue(t)
		if err != nil {
			return ollamaOptions{}, err
		}
		thinkVal = &tv
	}

	allowHeaderOverride := false
	if v := gocamel.GetConfigValue(u, "allowHeaderOverride"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return ollamaOptions{}, fmt.Errorf("invalid allowHeaderOverride %q: %w", v, err)
		}
		allowHeaderOverride = b
	}

	return ollamaOptions{
		mode:                mode,
		baseURL:             baseURL,
		model:               gocamel.GetConfigValue(u, "model"),
		system:              gocamel.GetConfigValue(u, "system"),
		keepAlive:           keepAlive,
		hasKeepAlive:        hasKeepAlive,
		think:               thinkVal,
		format:              gocamel.GetConfigValue(u, "format"),
		allowHeaderOverride: allowHeaderOverride,
	}, nil
}

func (c *OllamaComponent) CreateEndpoint(uri string) (gocamel.Endpoint, error) {
	u, err := gocamel.ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid ollama URI: %w", err)
	}
	opts, err := parseOllamaOptions(u)
	if err != nil {
		return nil, err
	}

	return &OllamaEndpoint{
		uri:                 uri,
		url:                 u,
		comp:                c,
		mode:                opts.mode,
		baseURL:             opts.baseURL,
		model:               opts.model,
		system:              opts.system,
		keepAlive:           opts.keepAlive,
		hasKeepAlive:        opts.hasKeepAlive,
		think:               opts.think,
		format:              opts.format,
		allowHeaderOverride: opts.allowHeaderOverride,
		optionsResolved:     true,
	}, nil
}

// OllamaEndpoint represents an Ollama endpoint.
type OllamaEndpoint struct {
	uri                 string
	url                 *url.URL
	comp                *OllamaComponent
	mode                string
	baseURL             *url.URL
	model               string
	system              string
	keepAlive           time.Duration
	hasKeepAlive        bool
	think               *api.ThinkValue
	format              string
	allowHeaderOverride bool // opt-in: OllamaModel/OllamaSystem headers may override URI settings
	optionsResolved     bool
}

func (e *OllamaEndpoint) resolvedOptions() (ollamaOptions, error) {
	if e.optionsResolved {
		return ollamaOptions{
			mode:                e.mode,
			baseURL:             e.baseURL,
			model:               e.model,
			system:              e.system,
			keepAlive:           e.keepAlive,
			hasKeepAlive:        e.hasKeepAlive,
			think:               e.think,
			format:              e.format,
			allowHeaderOverride: e.allowHeaderOverride,
		}, nil
	}
	return parseOllamaOptions(e.url)
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
	endpoint     *OllamaEndpoint
	mu           sync.RWMutex
	httpClient   *http.Client
	client       ollamaAPI
	model        string
	mode         string
	system       string
	keepAlive    time.Duration
	hasKeepAlive bool
	think        *api.ThinkValue
	format       string
	started      bool
}

func (p *OllamaProducer) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	opts, err := p.endpoint.resolvedOptions()
	if err != nil {
		return err
	}

	if opts.model == "" && opts.mode != "embed" {
		return fmt.Errorf("model is required for ollama in %s mode", opts.mode)
	}

	p.mode = opts.mode
	p.model = opts.model
	p.system = opts.system
	p.keepAlive = opts.keepAlive
	p.hasKeepAlive = opts.hasKeepAlive
	p.think = opts.think
	p.format = opts.format

	p.httpClient = newSafeHTTPClient(60 * time.Second)
	// The API key is sent to Ollama Cloud hosts only, so a key exported for
	// the ollama CLI never reaches (nor breaks) a local or LAN server.
	if apiKey := os.Getenv(cloudAPIKeyEnv); apiKey != "" && isCloudHost(opts.baseURL.Hostname()) {
		if err := requireSecureKeyTransport(opts.baseURL); err != nil {
			return err
		}
		p.client = newCloudClient(opts.baseURL, p.httpClient, apiKey)
	} else {
		p.client = api.NewClient(opts.baseURL, p.httpClient)
	}

	p.started = true
	return nil
}

func (p *OllamaProducer) Stop() error {
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

// Send dispatches the exchange to the configured Ollama API mode.
func (p *OllamaProducer) Send(exchange *gocamel.Exchange) error {
	p.mu.RLock()
	started := p.started
	client := p.client
	mode := p.mode
	p.mu.RUnlock()
	if !started || client == nil {
		return fmt.Errorf("the Ollama producer has not been started")
	}
	switch mode {
	case "chat":
		return p.sendChat(exchange, client)
	case "generate":
		return p.sendGenerate(exchange, client)
	case "embed":
		return p.sendEmbed(exchange, client)
	default:
		return fmt.Errorf("unknown ollama mode %q", mode)
	}
}

func (p *OllamaProducer) sendChat(exchange *gocamel.Exchange, client ollamaAPI) error {
	prompt, err := bodyAsString(exchange.GetIn().GetBody())
	if err != nil {
		return err
	}
	model := p.resolveModel(exchange)

	req := &api.ChatRequest{
		Model:    model,
		Messages: []api.Message{{Role: "user", Content: prompt}},
		Stream:   boolPtr(false),
	}
	p.applyOptions(req)
	if v, ok := p.overrideHeader(exchange, "OllamaSystem"); ok {
		req.Messages = append([]api.Message{{Role: "system", Content: v}}, req.Messages...)
	}

	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}
	var resp api.ChatResponse
	err = client.Chat(ctx, req, func(r api.ChatResponse) error {
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

func (p *OllamaProducer) sendGenerate(exchange *gocamel.Exchange, client ollamaAPI) error {
	prompt, err := bodyAsString(exchange.GetIn().GetBody())
	if err != nil {
		return err
	}
	model := p.resolveModel(exchange)
	p.mu.RLock()
	system := p.system
	p.mu.RUnlock()
	if v, ok := p.overrideHeader(exchange, "OllamaSystem"); ok {
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
	err = client.Generate(ctx, req, func(r api.GenerateResponse) error {
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

func (p *OllamaProducer) sendEmbed(exchange *gocamel.Exchange, client ollamaAPI) error {
	input, err := bodyAsString(exchange.GetIn().GetBody())
	if err != nil {
		return err
	}
	model := p.resolveModel(exchange)

	req := &api.EmbedRequest{
		Model: model,
		Input: input,
	}
	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}
	resp, err := client.Embed(ctx, req)
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
// via the OllamaModel header when allowHeaderOverride is set.
func (p *OllamaProducer) resolveModel(exchange *gocamel.Exchange) string {
	if v, ok := p.overrideHeader(exchange, "OllamaModel"); ok && v != "" {
		return v
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.model
}

// overrideHeader returns a per-message override header.
//
// SECURITY: the OllamaModel and OllamaSystem headers are honoured only when
// the endpoint opts in with allowHeaderOverride=true. Enable it only where
// headers cannot be influenced by untrusted input: they would let a sender
// replace the system prompt (prompt injection) and pick the model.
func (p *OllamaProducer) overrideHeader(exchange *gocamel.Exchange, key string) (string, bool) {
	if !p.endpoint.allowHeaderOverride {
		return "", false
	}
	return getStringHeader(exchange, key)
}

// applyOptions mutates the request to carry keepAlive/think/format when set.
func (p *OllamaProducer) applyOptions(req any) {
	p.mu.RLock()
	keepAlive := p.keepAlive
	hasKeepAlive := p.hasKeepAlive
	think := p.think
	format := p.format
	p.mu.RUnlock()
	if !hasKeepAlive && think == nil && format == "" {
		return
	}
	if hasKeepAlive {
		switch r := req.(type) {
		case *api.ChatRequest:
			d := api.Duration{Duration: keepAlive}
			r.KeepAlive = &d
		case *api.GenerateRequest:
			d := api.Duration{Duration: keepAlive}
			r.KeepAlive = &d
		}
	}
	if think != nil {
		tv := *think
		switch r := req.(type) {
		case *api.ChatRequest:
			r.Think = &tv
		case *api.GenerateRequest:
			r.Think = &tv
		}
	}
	if format != "" {
		switch r := req.(type) {
		case *api.ChatRequest:
			r.Format = json.RawMessage(strconv.Quote(format))
		case *api.GenerateRequest:
			r.Format = json.RawMessage(strconv.Quote(format))
		}
	}
}

// bodyAsString converts the exchange body to a prompt string.
func bodyAsString(body any) (string, error) {
	if body == nil {
		return "", fmt.Errorf("exchange body cannot be nil")
	}
	switch v := body.(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	default:
		return fmt.Sprintf("%v", v), nil
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

func resolveMode(u *url.URL) string {
	if u.Opaque != "" {
		return u.Opaque
	}
	if u.Host != "" {
		if u.Path != "" && u.Path != "/" {
			return u.Host + u.Path
		}
		return u.Host
	}
	if p := strings.TrimPrefix(u.Path, "/"); p != "" {
		return p
	}
	return "chat"
}

// parseThinkValue maps the URI string to the Ollama ThinkValue.
// Accepted values: "true", "false", "low", "medium", "high", "max".
func parseThinkValue(s string) (api.ThinkValue, error) {
	switch s {
	case "true":
		return api.ThinkValue{Value: true}, nil
	case "false":
		return api.ThinkValue{Value: false}, nil
	case "low", "medium", "high", "max":
		return api.ThinkValue{Value: s}, nil
	default:
		return api.ThinkValue{}, fmt.Errorf("invalid think value %q: must be true, false, low, medium, high or max", s)
	}
}

// validateHostURL rejects non-absolute URLs, hostless/opaque URLs, and
// non-http(s) schemes.
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
	if u.Hostname() == "" || u.Opaque != "" {
		return fmt.Errorf("host must include a hostname: %s", raw)
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
