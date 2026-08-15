package kafka

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gocamel "gitlab.com/tranchida/gocamel"
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
