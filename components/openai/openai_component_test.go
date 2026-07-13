package openai

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"testing"

	sdkopenai "github.com/sashabaranov/go-openai"

	gocamel "gitlab.com/tranchida/gocamel"
)

func TestOpenAIProducer_Start(t *testing.T) {
	// Set environment variable for test
	os.Setenv("OPENAI_API_KEY", "test-token")
	defer os.Unsetenv("OPENAI_API_KEY")

	u, _ := url.Parse("openai:chat?model=gpt-4&apiKey=test-token")
	endpoint := &OpenAIEndpoint{
		uri: "openai:chat?model=gpt-4",
		url: u,
	}
	producer := &OpenAIProducer{
		endpoint: endpoint,
	}

	err := producer.Start(context.Background())
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if producer.client == nil {
		t.Error("Expected client to be initialized")
	}

	if producer.model != "gpt-4" {
		t.Errorf("Expected model gpt-4, got %s", producer.model)
	}
}

func TestOpenAIProducer_Send_NotStarted(t *testing.T) {
	producer := &OpenAIProducer{}
	exchange := gocamel.NewExchange(context.Background())
	err := producer.Send(exchange)
	if err == nil {
		t.Error("Expected error when sending with unstarted producer")
	}
}

type contextRecordingDoer struct {
	ctx context.Context
}

func (d *contextRecordingDoer) Do(req *http.Request) (*http.Response, error) {
	d.ctx = req.Context()
	return nil, req.Context().Err()
}

func TestOpenAIProducer_SendUsesExchangeContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	doer := &contextRecordingDoer{}
	config := sdkopenai.DefaultConfig("test-token")
	config.HTTPClient = doer
	producer := &OpenAIProducer{
		client: sdkopenai.NewClientWithConfig(config),
		model:  sdkopenai.GPT3Dot5Turbo,
	}
	exchange := gocamel.NewExchange(ctx)
	exchange.SetBody("hello")

	err := producer.Send(exchange)
	if err == nil {
		t.Fatal("expected canceled request to fail")
	}
	if doer.ctx == nil || !errors.Is(doer.ctx.Err(), context.Canceled) {
		t.Fatalf("request did not receive the exchange context: %v", doer.ctx)
	}
}
