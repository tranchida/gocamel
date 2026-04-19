package gocamel

import (
	"context"
	"fmt"
	"net/url"

	"github.com/sashabaranov/go-openai"
)

// OpenAIComponent represents the OpenAI component
type OpenAIComponent struct{}

// NewOpenAIComponent creates a new OpenAIComponent
func NewOpenAIComponent() *OpenAIComponent {
	return &OpenAIComponent{}
}

// CreateEndpoint creates a new endpoint OpenAI
func (c *OpenAIComponent) CreateEndpoint(uri string) (Endpoint, error) {
	u, err := ParseURI(uri)
	if err != nil {
		return nil, err
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

// URI returns the URI de l'endpoint
func (e *OpenAIEndpoint) URI() string {
	return e.uri
}

// CreateProducer crée un producteur OpenAI
func (e *OpenAIEndpoint) CreateProducer() (Producer, error) {
	return &OpenAIProducer{
		endpoint: e,
	}, nil
}

// CreateConsumer crée un consommateur OpenAI
func (e *OpenAIEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return nil, fmt.Errorf("le composant OpenAI ne supporte pas le mode Consumer (uniquement Producer)")
}

// OpenAIProducer represents a producteur OpenAI
type OpenAIProducer struct {
	endpoint *OpenAIEndpoint
	client   *openai.Client
	model    string
}

func (p *OpenAIProducer) Start(ctx context.Context) error {
	token := GetConfigValue(p.endpoint.url, "authorizationToken")
	if token == "" {
		token = GetConfigValue(p.endpoint.url, "apiKey") // alias
		if token == "" {
			return fmt.Errorf("authorizationToken (ou apiKey) missing for OpenAI")
		}
	}

	model := GetConfigValue(p.endpoint.url, "model")
	if model == "" {
		model = openai.GPT3Dot5Turbo // default model
	}

	p.client = openai.NewClient(token)
	p.model = model

	return nil
}

func (p *OpenAIProducer) Stop() error {
	return nil
}

func (p *OpenAIProducer) Send(exchange *Exchange) error {
	if p.client == nil {
		return fmt.Errorf("le producteur OpenAI n'a pas été démarré")
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

	resp, err := p.client.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model: p.model,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: prompt,
				},
			},
		},
	)

	if err != nil {
		return fmt.Errorf("error de requête OpenAI: %w", err)
	}

	if len(resp.Choices) > 0 {
		exchange.GetOut().SetBody(resp.Choices[0].Message.Content)
		// Optionnel: copier certains headers de la réponse
		exchange.GetOut().SetHeader("OpenAIUsageTotalTokens", resp.Usage.TotalTokens)
	} else {
		return fmt.Errorf("OpenAI n'a retourné aucune réponse")
	}

	return nil
}
