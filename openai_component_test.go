package gocamel

import (
	"context"
	"net/url"
	"os"
	"testing"
)

func TestOpenAIProducer_Start(t *testing.T) {
	// Set environment variable for test
	os.Setenv("OPENAI_API_KEY", "test-token")
	defer os.Unsetenv("OPENAI_API_KEY")

	u, _ := url.Parse("openai:chat?model=gpt-4")
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
	exchange := NewExchange(context.Background())
	err := producer.Send(exchange)
	if err == nil {
		t.Error("Expected error when sending with unstarted producer")
	}
}
