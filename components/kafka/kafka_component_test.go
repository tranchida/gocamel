package kafka

import (
	"context"
	"testing"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gocamel "github.com/tranchida/gocamel"
)

func TestKafkaComponent_CreateEndpoint(t *testing.T) {
	component := NewKafkaComponent()

	// URI with brokers, groupId, and partition
	uri := "kafka:orders?brokers=broker1:9092,broker2:9092&groupId=billing-group&partition=2&clientId=test-client"
	endpoint, err := component.CreateEndpoint(uri)
	require.NoError(t, err)

	ke, ok := endpoint.(*KafkaEndpoint)
	require.True(t, ok)
	assert.Equal(t, "orders", ke.topic)
	assert.Equal(t, []string{"broker1:9092", "broker2:9092"}, ke.brokers)
	assert.Equal(t, "billing-group", ke.groupID)
	assert.Equal(t, 2, ke.partition)
	assert.Equal(t, "test-client", ke.clientID)
}

func TestKafkaComponent_DefaultBrokers(t *testing.T) {
	component := NewKafkaComponent()

	endpoint, err := component.CreateEndpoint("kafka:notifications")
	require.NoError(t, err)

	ke, ok := endpoint.(*KafkaEndpoint)
	require.True(t, ok)
	assert.Equal(t, "notifications", ke.topic)
	assert.Equal(t, []string{"localhost:9092"}, ke.brokers)
	assert.Equal(t, -1, ke.partition)
	assert.Empty(t, ke.groupID)
}

func TestKafkaComponent_ProducerLifecycle(t *testing.T) {
	component := NewKafkaComponent()
	endpoint, err := component.CreateEndpoint("kafka:events?brokers=localhost:9092")
	require.NoError(t, err)

	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	ctx := context.Background()
	err = producer.Start(ctx)
	require.NoError(t, err)

	// Sending without a running broker returns error but doesn't panic
	ex := gocamel.NewExchange(ctx)
	ex.In.SetBody("Hello Kafka")
	ex.In.SetHeader(CamelKafkaKey, "key123")
	ex.In.SetHeader("X-Custom-Header", "custom-value")

	err = producer.Send(ex)
	// Sending should fail since no broker is running locally on 9092
	assert.Error(t, err)

	err = producer.Stop()
	assert.NoError(t, err)
}

func TestKafkaComponent_AllowHeaderOverrideParsing(t *testing.T) {
	component := NewKafkaComponent()

	// Default: allowHeaderOverride is false.
	endpoint, err := component.CreateEndpoint("kafka:orders?brokers=localhost:9092")
	require.NoError(t, err)
	assert.False(t, endpoint.(*KafkaEndpoint).allowHeaderOverride)

	// Explicit opt-in.
	endpoint, err = component.CreateEndpoint("kafka:orders?brokers=localhost:9092&allowHeaderOverride=true")
	require.NoError(t, err)
	assert.True(t, endpoint.(*KafkaEndpoint).allowHeaderOverride)
}

func TestKafkaProducer_TopicHeaderRequiresOptIn(t *testing.T) {
	tests := []struct {
		name                string
		allowHeaderOverride bool
		wantTopic           string
	}{
		{name: "without flag the URI topic is used", allowHeaderOverride: false, wantTopic: "orders"},
		{name: "with flag the header topic is used", allowHeaderOverride: true, wantTopic: "payments"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			producer := &KafkaProducer{endpoint: &KafkaEndpoint{
				topic:               "orders",
				allowHeaderOverride: tt.allowHeaderOverride,
			}}

			exchange := gocamel.NewExchange(context.Background())
			exchange.In.SetHeader(CamelKafkaTopic, "payments")

			assert.Equal(t, tt.wantTopic, producer.resolveTopic(exchange))
		})
	}
}

func TestKafkaProducer_KeyHeadersRequireOptIn(t *testing.T) {
	tests := []struct {
		name                string
		allowHeaderOverride bool
		wantKey             []byte
	}{
		{name: "without flag the key headers are ignored", allowHeaderOverride: false, wantKey: nil},
		{name: "with flag the partition key header is used", allowHeaderOverride: true, wantKey: []byte("key123")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			producer := &KafkaProducer{endpoint: &KafkaEndpoint{
				allowHeaderOverride: tt.allowHeaderOverride,
			}}

			exchange := gocamel.NewExchange(context.Background())
			exchange.In.SetHeader(CamelKafkaPartitionKey, "key123")
			exchange.In.SetHeader(CamelKafkaKey, "other")

			assert.Equal(t, tt.wantKey, producer.resolveKey(exchange))
		})
	}
}

func headerKeys(headers []kafka.Header) []string {
	keys := make([]string, 0, len(headers))
	for _, h := range headers {
		keys = append(keys, h.Key)
	}
	return keys
}

func TestKafkaProducer_SensitiveHeadersNotForwarded(t *testing.T) {
	component := NewKafkaComponent()
	endpoint, err := component.CreateEndpoint("kafka:orders?brokers=localhost:9092")
	require.NoError(t, err)
	producer := &KafkaProducer{endpoint: endpoint.(*KafkaEndpoint)}

	exchange := gocamel.NewExchange(context.Background())
	exchange.In.SetHeader("Authorization", "Bearer secret-token")
	exchange.In.SetHeader("Cookie", "session=abc")
	exchange.In.SetHeader("Proxy-Authorization", "Basic xyz")
	exchange.In.SetHeader("Set-Cookie", "sid=42")
	exchange.In.SetHeader("WWW-Authenticate", "Basic realm=app")
	exchange.In.SetHeader("tenant-id", "acme")
	exchange.In.SetHeader(CamelKafkaTopic, "payments")

	keys := headerKeys(producer.filterHeaders(exchange))
	assert.Contains(t, keys, "tenant-id")
	assert.NotContains(t, keys, "Authorization")
	assert.NotContains(t, keys, "Cookie")
	assert.NotContains(t, keys, "Proxy-Authorization")
	assert.NotContains(t, keys, "Set-Cookie")
	assert.NotContains(t, keys, "WWW-Authenticate")
	assert.NotContains(t, keys, CamelKafkaTopic, "Camel-prefixed headers must never be forwarded")
}

func TestKafkaProducer_ForwardHeadersAllowlist(t *testing.T) {
	component := NewKafkaComponent()
	endpoint, err := component.CreateEndpoint("kafka:orders?brokers=localhost:9092&forwardHeaders=tenant-id,%20trace-id")
	require.NoError(t, err)
	producer := &KafkaProducer{endpoint: endpoint.(*KafkaEndpoint)}

	exchange := gocamel.NewExchange(context.Background())
	exchange.In.SetHeader("tenant-id", "acme")
	exchange.In.SetHeader("trace-id", "t-1")
	exchange.In.SetHeader("X-Custom-Header", "custom-value")
	exchange.In.SetHeader("Authorization", "Bearer secret-token")

	headers := producer.filterHeaders(exchange)
	keys := headerKeys(headers)
	assert.Contains(t, keys, "tenant-id")
	assert.Contains(t, keys, "trace-id")
	assert.NotContains(t, keys, "X-Custom-Header", "headers outside the allowlist must be dropped")
	assert.NotContains(t, keys, "Authorization", "sensitive headers stay blocked even in the allowlist")
	for _, h := range headers {
		if h.Key == "tenant-id" {
			assert.Equal(t, "acme", string(h.Value))
		}
	}
}

func TestKafkaConsumer_StopReleasesEndpointRegistration(t *testing.T) {
	component := NewKafkaComponent()
	endpoint, err := component.CreateEndpoint("kafka:test-topic?brokers=localhost:9092")
	require.NoError(t, err)

	consumer, err := endpoint.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	require.NoError(t, err)

	// Attempting to create a second consumer on the same endpoint must fail
	_, err = endpoint.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	assert.Error(t, err)

	// Stopping the first consumer releases it
	assert.NoError(t, consumer.Stop())

	// Can now register another consumer
	_, err = endpoint.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	assert.NoError(t, err)
}
