// Package jev provides a GoCamel component for the TypeSafe Jev decision model
// served through the OpenRouter Decisions API.
//
// Jev reads natural language like an LLM but returns typed judgments (yes/no
// probabilities, choices, scores) instead of generated text.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	gocamel "github.com/tranchida/gocamel"
)

const (
	// DefaultBaseURL is the OpenRouter API root; the Decisions endpoint lives under it.
	DefaultBaseURL = "https://openrouter.ai/api/alpha"
	// DefaultModel is the Jev model requested when the URI sets no model.
	DefaultModel = "typesafe/jev-1.13"

	decisionsPath    = "/decisions"
	defaultTimeout   = 30 * time.Second
	maxResponseBytes = 4 << 20 // 4 MiB
	maxErrorBytes    = 512

	// HeaderQuestions overrides the endpoint questions (JSON object or map).
	HeaderQuestions = "JevQuestions"
	// HeaderModel overrides the endpoint model.
	HeaderModel = "JevModel"
	// HeaderElapsed is set on the Out message: the API round-trip time.
	HeaderElapsed = "JevElapsed"
)

// Answer is one typed judgment returned by Jev. Which fields are filled
// depends on Type: "noul" fills Noul, "choice" fills Choice and Probabilities,
// "score" fills Value and Legend. Confidence applies to choice and score.
type Answer struct {
	Type          string             `json:"type"`
	Noul          float64            `json:"noul,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Value         float64            `json:"value,omitempty"`
	Confidence    float64            `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Legend        map[string]string  `json:"legend,omitempty"`
}

type decisionsRequest struct {
	Model     string                     `json:"model"`
	State     any                        `json:"state"`
	Questions map[string]json.RawMessage `json:"questions"`
}

type decisionsResponse struct {
	Answers map[string]Answer `json:"answers"`
}

// JevComponent is the Jev component (OpenRouter Decisions API).
type JevComponent struct{}

// NewJevComponent creates a new JevComponent.
func NewJevComponent() *JevComponent {
	return &JevComponent{}
}

// CreateEndpoint creates a Jev endpoint from a URI.
//
// URI scheme: jev:decisions?apiKey=...&questions={"is_ironic":{...}}
//
// Supported options (via gocamel.GetConfigValue):
//   - apiKey              API key (alias: authorizationToken). Falls back to the
//     OPENROUTER_API_KEY environment variable. Masked by RedactURI.
//   - questions           JSON object mapping question ids to Jev questions
//     (types noul, choice, score), passed to the API as-is. Required unless
//     allowHeaderOverride=true and the JevQuestions header is used.
//   - model               (default typesafe/jev-1.13) Model name.
//   - baseURL             (default https://openrouter.ai/api/alpha) API root, for
//     proxies and tests. Must be http(s).
//   - timeout             (default 30s) Per-request timeout.
//   - allowHeaderOverride (default false) true to let the JevQuestions and
//     JevModel headers replace the configured values per message.
func (c *JevComponent) CreateEndpoint(uri string) (gocamel.Endpoint, error) {
	u, err := gocamel.ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid jev URI: %w", err)
	}

	baseURL := DefaultBaseURL
	if v := gocamel.GetConfigValue(u, "baseURL"); v != "" {
		if err := validateBaseURL(v); err != nil {
			return nil, fmt.Errorf("invalid jev baseURL: %w", err)
		}
		baseURL = strings.TrimRight(v, "/")
	}

	model := DefaultModel
	if v := gocamel.GetConfigValue(u, "model"); v != "" {
		model = v
	}

	timeout := defaultTimeout
	if v := gocamel.GetConfigValue(u, "timeout"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("invalid jev timeout %q: must be a positive duration", v)
		}
		timeout = d
	}

	var questions map[string]json.RawMessage
	if v := gocamel.GetConfigValue(u, "questions"); v != "" {
		questions, err = parseQuestions(v)
		if err != nil {
			return nil, fmt.Errorf("invalid jev questions: %w", err)
		}
	}

	allowHeaderOverride := false
	if v := gocamel.GetConfigValue(u, "allowHeaderOverride"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			allowHeaderOverride = b
		}
	}

	return &JevEndpoint{
		uri:                 uri,
		url:                 u,
		baseURL:             baseURL,
		model:               model,
		timeout:             timeout,
		questions:           questions,
		allowHeaderOverride: allowHeaderOverride,
	}, nil
}

// JevEndpoint is a Jev endpoint. Its fields are immutable after creation.
type JevEndpoint struct {
	uri                 string
	url                 *url.URL
	baseURL             string
	model               string
	timeout             time.Duration
	questions           map[string]json.RawMessage
	allowHeaderOverride bool // opt-in: Jev* request headers may override URI settings
}

// URI returns the endpoint URI with credentials masked for safe logging.
func (e *JevEndpoint) URI() string {
	return gocamel.RedactURI(e.uri)
}

// CreateProducer creates a Jev producer.
func (e *JevEndpoint) CreateProducer() (gocamel.Producer, error) {
	return &JevProducer{endpoint: e}, nil
}

// CreateConsumer returns an error: the Jev component supports Producer mode only.
func (e *JevEndpoint) CreateConsumer(processor gocamel.Processor) (gocamel.Consumer, error) {
	return nil, fmt.Errorf("the Jev component does not support Consumer mode (Producer only)")
}

// JevProducer sends the exchange body to Jev as the state to judge.
type JevProducer struct {
	endpoint *JevEndpoint
	client   *http.Client
	apiKey   string
	started  bool
}

// Start resolves the API key and builds the HTTP client.
func (p *JevProducer) Start(ctx context.Context) error {
	u := p.endpoint.url

	token := gocamel.GetConfigValue(u, "authorizationToken")
	if token == "" {
		token = gocamel.GetConfigValue(u, "apiKey")
	}
	if token == "" {
		token = os.Getenv("OPENROUTER_API_KEY")
	}
	if token == "" {
		return fmt.Errorf("apiKey (or authorizationToken, or OPENROUTER_API_KEY) missing for Jev")
	}
	if len(p.endpoint.questions) == 0 && !p.endpoint.allowHeaderOverride {
		return fmt.Errorf("questions is required for Jev (or set allowHeaderOverride=true and send the %s header)", HeaderQuestions)
	}

	p.apiKey = token
	p.client = &http.Client{Timeout: p.endpoint.timeout}
	p.started = true
	return nil
}

// Stop releases idle connections.
func (p *JevProducer) Stop() error {
	if p.client != nil {
		p.client.CloseIdleConnections()
	}
	return nil
}

// Send asks Jev the configured questions about the exchange body (the "state")
// and writes the typed answers, a map[string]Answer keyed by question id, to
// the Out message body. The body is sent as-is when it is a string or []byte,
// and as JSON otherwise (maps, structs).
//
// Out headers: JevModel (model used) and JevElapsed (time.Duration).
//
// Per-message overrides (only with allowHeaderOverride=true): JevQuestions
// (JSON string or map[string]any) and JevModel.
//
// SECURITY: questions and model are trusted configuration. Enable the override
// only where headers cannot be influenced by untrusted input, otherwise a
// sender could pick the model (cost) and rewrite the questions.
func (p *JevProducer) Send(exchange *gocamel.Exchange) error {
	if !p.started {
		return fmt.Errorf("the Jev producer has not been started")
	}
	e := p.endpoint

	model := e.model
	questions := e.questions
	if e.allowHeaderOverride {
		if v, ok := stringHeader(exchange, HeaderModel); ok && v != "" {
			model = v
		}
		if v, ok := exchange.GetIn().GetHeader(HeaderQuestions); ok {
			q, err := questionsFromHeader(v)
			if err != nil {
				return fmt.Errorf("invalid %s header: %w", HeaderQuestions, err)
			}
			questions = q
		}
	}
	if len(questions) == 0 {
		return fmt.Errorf("no Jev questions configured")
	}

	state, err := stateFromBody(exchange.GetIn().GetBody())
	if err != nil {
		return err
	}
	payload, err := json.Marshal(decisionsRequest{Model: model, State: state, Questions: questions})
	if err != nil {
		return fmt.Errorf("failed to encode Jev request: %w", err)
	}

	reqCtx := exchange.Context
	if reqCtx == nil {
		reqCtx = context.Background()
	}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, e.baseURL+decisionsPath, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to build Jev request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	start := time.Now()
	resp, err := p.client.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		return fmt.Errorf("request error from Jev: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
		return fmt.Errorf("jev API error: status %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	var result decisionsResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&result); err != nil {
		return fmt.Errorf("failed to decode Jev response: %w", err)
	}
	if len(result.Answers) == 0 {
		return fmt.Errorf("no answers returned by Jev")
	}

	out := exchange.GetOut()
	out.SetBody(result.Answers)
	out.SetHeader(HeaderModel, model)
	out.SetHeader(HeaderElapsed, elapsed)
	return nil
}

// parseQuestions decodes a JSON object of question id -> question definition.
func parseQuestions(raw string) (map[string]json.RawMessage, error) {
	var q map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &q); err != nil {
		return nil, err
	}
	if len(q) == 0 {
		return nil, fmt.Errorf("at least one question is required")
	}
	return q, nil
}

func questionsFromHeader(v any) (map[string]json.RawMessage, error) {
	switch s := v.(type) {
	case string:
		return parseQuestions(s)
	case []byte:
		return parseQuestions(string(s))
	case map[string]json.RawMessage:
		return s, nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		return parseQuestions(string(b))
	}
}

// stateFromBody converts the exchange body to the request "state".
func stateFromBody(body any) (any, error) {
	switch v := body.(type) {
	case nil:
		return nil, fmt.Errorf("the exchange body (Jev state) is empty")
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	default:
		return v, nil // marshalled as JSON with the request
	}
}

func stringHeader(exchange *gocamel.Exchange, key string) (string, bool) {
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

// validateBaseURL rejects relative URLs and non-http(s) schemes.
func validateBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	if !u.IsAbs() {
		return fmt.Errorf("baseURL must be absolute: %s", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("baseURL scheme must be http or https, got %q", u.Scheme)
	}
	return nil
}
