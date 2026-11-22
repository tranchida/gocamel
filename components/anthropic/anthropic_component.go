package anthropic

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	gocamel "github.com/tranchida/gocamel"
)

const maxResponseBodyBytes = gocamel.DefaultMaxBodySize

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
//   - apiKey             (required, falls back to ANTHROPIC_API_KEY) API key (alias: authorizationToken). Masked by RedactURI.
//   - model              (required) Model name, e.g. claude-sonnet-4-5.
//   - maxTokens          (default 1024) Maximum tokens to generate.
//   - system             (optional) System prompt.
//   - temperature        (optional) Finite number in [0.0, 1.0].
//   - baseURL            (optional) Override API base URL (proxies, Bedrock-compatible gateways).
//   - allowHeaderOverride (default false) true to let the AnthropicSystem,
//     AnthropicModel, AnthropicMaxTokens and AnthropicTemperature headers
//     replace the configured values per message.
type anthropicOptions struct {
	baseURL             string
	model               string
	maxTokensRaw        string
	system              string
	temperature         float64
	hasTemperature      bool
	allowHeaderOverride bool
}

func parseAnthropicOptions(u *url.URL) (anthropicOptions, error) {
	if u == nil {
		return anthropicOptions{}, nil
	}
	baseURL := gocamel.GetConfigValue(u, "baseURL")
	if baseURL != "" {
		if err := validateBaseURL(baseURL); err != nil {
			return anthropicOptions{}, fmt.Errorf("invalid anthropic baseURL: %w", err)
		}
	}

	var temperature float64
	var hasTemperature bool
	if v := gocamel.GetConfigValue(u, "temperature"); v != "" {
		t, err := parseTemperature(v)
		if err != nil {
			return anthropicOptions{}, err
		}
		temperature = t
		hasTemperature = true
	}

	allowHeaderOverride := false
	if v := gocamel.GetConfigValue(u, "allowHeaderOverride"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return anthropicOptions{}, fmt.Errorf("invalid allowHeaderOverride %q: %w", v, err)
		}
		allowHeaderOverride = b
	}

	return anthropicOptions{
		baseURL:             baseURL,
		model:               gocamel.GetConfigValue(u, "model"),
		maxTokensRaw:        gocamel.GetConfigValue(u, "maxTokens"),
		system:              gocamel.GetConfigValue(u, "system"),
		temperature:         temperature,
		hasTemperature:      hasTemperature,
		allowHeaderOverride: allowHeaderOverride,
	}, nil
}

func (c *AnthropicComponent) CreateEndpoint(uri string) (gocamel.Endpoint, error) {
	u, err := gocamel.ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid anthropic URI: %w", err)
	}
	opts, err := parseAnthropicOptions(u)
	if err != nil {
		return nil, err
	}

	return &AnthropicEndpoint{
		uri:                 uri,
		url:                 u,
		comp:                c,
		baseURL:             opts.baseURL,
		model:               opts.model,
		maxTokensRaw:        opts.maxTokensRaw,
		system:              opts.system,
		temperature:         opts.temperature,
		hasTemperature:      opts.hasTemperature,
		allowHeaderOverride: opts.allowHeaderOverride,
		optionsResolved:     true,
	}, nil
}

// AnthropicEndpoint represents an Anthropic endpoint.
type AnthropicEndpoint struct {
	uri                 string
	url                 *url.URL
	comp                *AnthropicComponent
	baseURL             string
	model               string
	maxTokensRaw        string
	system              string
	temperature         float64
	hasTemperature      bool
	allowHeaderOverride bool // opt-in: Anthropic* request headers may override URI settings
	optionsResolved     bool
}

func (e *AnthropicEndpoint) resolvedOptions() (anthropicOptions, error) {
	if e.optionsResolved {
		return anthropicOptions{
			baseURL:             e.baseURL,
			model:               e.model,
			maxTokensRaw:        e.maxTokensRaw,
			system:              e.system,
			temperature:         e.temperature,
			hasTemperature:      e.hasTemperature,
			allowHeaderOverride: e.allowHeaderOverride,
		}, nil
	}
	return parseAnthropicOptions(e.url)
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
	endpoint       *AnthropicEndpoint
	mu             sync.RWMutex
	httpClient     *http.Client
	client         anthropic.Client
	model          anthropic.Model
	maxTokens      int64
	system         string
	temperature    float64
	hasTemperature bool
	started        bool
}

func (p *AnthropicProducer) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	u := p.endpoint.url

	token := gocamel.GetConfigValue(u, "authorizationToken")
	if token == "" {
		token = gocamel.GetConfigValue(u, "apiKey") // alias
	}
	if token == "" {
		token = os.Getenv("ANTHROPIC_API_KEY")
	}
	if token == "" {
		return fmt.Errorf("apiKey (or authorizationToken, or ANTHROPIC_API_KEY) missing for Anthropic")
	}

	epOpts, err := p.endpoint.resolvedOptions()
	if err != nil {
		return err
	}

	if epOpts.model == "" {
		return fmt.Errorf("model is required for Anthropic (e.g. claude-sonnet-4-5)")
	}
	p.model = anthropic.Model(epOpts.model)

	maxTokens := int64(1024)
	if epOpts.maxTokensRaw != "" {
		n, err := strconv.ParseInt(epOpts.maxTokensRaw, 10, 64)
		if err != nil || n <= 0 {
			return fmt.Errorf("invalid maxTokens %q: must be a positive integer", epOpts.maxTokensRaw)
		}
		maxTokens = n
	}
	p.maxTokens = maxTokens
	p.system = epOpts.system
	p.temperature = epOpts.temperature
	p.hasTemperature = epOpts.hasTemperature

	p.httpClient = newSafeHTTPClient(60 * time.Second)
	opts := []option.RequestOption{
		option.WithoutEnvironmentDefaults(),
		option.WithAPIKey(token),
		option.WithHTTPClient(p.httpClient),
		option.WithRequestTimeout(60 * time.Second),
		option.WithMaxRetries(0),
	}
	if epOpts.baseURL != "" {
		opts = append(opts, option.WithBaseURL(epOpts.baseURL))
	}
	p.client = anthropic.NewClient(opts...)
	p.started = true
	return nil
}

func (p *AnthropicProducer) Stop() error {
	p.mu.Lock()
	httpClient := p.httpClient
	p.httpClient = nil
	p.started = false
	p.mu.Unlock()
	if httpClient != nil {
		httpClient.CloseIdleConnections()
	}
	return nil
}

// Send calls the Anthropic Messages API with the exchange body as the user
// prompt and writes the first text block of the response to the Out message.
//
// Per-message overrides via exchange headers (only with allowHeaderOverride=true):
//   - AnthropicSystem       overrides the URI system prompt
//   - AnthropicModel        overrides the URI model
//   - AnthropicMaxTokens    overrides the URI maxTokens
//   - AnthropicTemperature  sets the sampling temperature
//
// SECURITY: these headers are honoured only when the endpoint opts in with
// allowHeaderOverride=true. Enable it only where headers cannot be influenced
// by untrusted input: they would let a sender replace the system prompt
// (prompt injection) and pick the model and token budget (cost).
func (p *AnthropicProducer) Send(exchange *gocamel.Exchange) error {
	p.mu.RLock()
	started := p.started
	client := p.client
	model := p.model
	maxTokens := p.maxTokens
	system := p.system
	temperature := p.temperature
	hasTemperature := p.hasTemperature
	p.mu.RUnlock()

	if !started {
		return fmt.Errorf("the Anthropic producer has not been started")
	}

	prompt, err := bodyAsString(exchange.GetIn().GetBody())
	if err != nil {
		return err
	}
	override := p.endpoint.allowHeaderOverride

	if v, ok := getStringHeader(exchange, "AnthropicSystem"); override && ok && v != "" {
		system = v
	}

	if v, ok := getStringHeader(exchange, "AnthropicModel"); override && ok && v != "" {
		model = anthropic.Model(v)
	}

	if v, ok := getStringHeader(exchange, "AnthropicMaxTokens"); override && ok && v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			return fmt.Errorf("invalid AnthropicMaxTokens header %q: must be a positive integer", v)
		}
		maxTokens = n
	}

	if override {
		if rawTemp, ok := exchange.GetIn().GetHeader("AnthropicTemperature"); ok {
			tVal, err := parseTemperatureHeader(rawTemp)
			if err != nil {
				return err
			}
			if tVal != nil {
				temperature = *tVal
				hasTemperature = true
			}
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
	if hasTemperature {
		params.Temperature = anthropic.Float(temperature)
	}

	reqCtx := exchange.Context
	if reqCtx == nil {
		reqCtx = context.Background()
	}
	resp, err := client.Messages.New(reqCtx, params)
	if err != nil {
		return fmt.Errorf("request error from Anthropic: %w", err)
	}
	if resp == nil {
		return fmt.Errorf("empty response returned by Anthropic")
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
	case fmt.Stringer:
		return s.String(), true
	}
	return "", false
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

func parseTemperature(raw string) (float64, error) {
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > 1 {
		return 0, fmt.Errorf("invalid temperature %q: must be a finite number in [0, 1]", raw)
	}
	if f == 0 {
		f = 0
	}
	return f, nil
}

func parseTemperatureHeader(v any) (*float64, error) {
	if v == nil {
		return nil, nil
	}
	var f float64
	switch val := v.(type) {
	case string:
		if val == "" {
			return nil, nil
		}
		parsed, err := parseTemperature(val)
		if err != nil {
			return nil, err
		}
		return &parsed, nil
	case []byte:
		if len(val) == 0 {
			return nil, nil
		}
		parsed, err := parseTemperature(string(val))
		if err != nil {
			return nil, err
		}
		return &parsed, nil
	case float64:
		f = val
	case float32:
		f = float64(val)
	case int:
		f = float64(val)
	case int64:
		f = float64(val)
	case fmt.Stringer:
		s := val.String()
		if s == "" {
			return nil, nil
		}
		parsed, err := parseTemperature(s)
		if err != nil {
			return nil, err
		}
		return &parsed, nil
	default:
		return nil, fmt.Errorf("invalid AnthropicTemperature header type %T", v)
	}
	if math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > 1 {
		return nil, fmt.Errorf("invalid AnthropicTemperature header %v: must be a finite number in [0, 1]", f)
	}
	if f == 0 {
		f = 0
	}
	return &f, nil
}

// validateBaseURL rejects non-absolute URLs, hostless/opaque URLs, and
// non-http(s) schemes to prevent SSRF-style misconfiguration.
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
