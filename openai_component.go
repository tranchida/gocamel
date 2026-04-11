package gocamel

import (
	"context"
	"fmt"
	"net/url"

	"github.com/sashabaranov/go-openai"
)

// OpenAIComponent représente le composant OpenAI
type OpenAIComponent struct{}

// NewOpenAIComponent crée une nouvelle instance de OpenAIComponent
func NewOpenAIComponent() *OpenAIComponent {
	return &OpenAIComponent{}
}

// CreateEndpoint crée un nouvel endpoint OpenAI
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

// OpenAIEndpoint représente un endpoint OpenAI
type OpenAIEndpoint struct {
	uri  string
	url  *url.URL
	comp *OpenAIComponent
}

// URI retourne l'URI de l'endpoint
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

// OpenAIProducer représente un producteur OpenAI
type OpenAIProducer struct {
	endpoint *OpenAIEndpoint
}

func (p *OpenAIProducer) Start(ctx context.Context) error {
	return nil
}

func (p *OpenAIProducer) Stop() error {
	return nil
}

func (p *OpenAIProducer) Send(exchange *Exchange) error {
	token := GetConfigValue(p.endpoint.url, "authorizationToken")
	if token == "" {
		token = GetConfigValue(p.endpoint.url, "apiKey") // alias
		if token == "" {
			return fmt.Errorf("authorizationToken (ou apiKey) manquant pour OpenAI")
		}
	}

	model := GetConfigValue(p.endpoint.url, "model")
	if model == "" {
		model = openai.GPT3Dot5Turbo // default model
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

	client := openai.NewClient(token)
	resp, err := client.CreateChatCompletion(
		context.Background(),
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
		return fmt.Errorf("erreur de requête OpenAI: %w", err)
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
