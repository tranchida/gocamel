package grpc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gocamel "gitlab.com/tranchida/gocamel"
)

func TestGRPCComponent_CreateEndpoint(t *testing.T) {
	component := NewGRPCComponent()

	uri := "grpc://localhost:9090/helloworld.Greeter/SayHello?insecure=true"
	endpoint, err := component.CreateEndpoint(uri)
	require.NoError(t, err)

	ge, ok := endpoint.(*GRPCEndpoint)
	require.True(t, ok)
	assert.Equal(t, "localhost:9090", ge.target)
	assert.Equal(t, "helloworld.Greeter", ge.service)
	assert.Equal(t, "SayHello", ge.method)
	assert.Equal(t, "/helloworld.Greeter/SayHello", ge.FullMethod())
	assert.True(t, ge.insecure)
}

func TestGRPCComponent_QueryServiceMethod(t *testing.T) {
	component := NewGRPCComponent()

	uri := "grpc://api.internal:50051?service=OrderService&method=CreateOrder"
	endpoint, err := component.CreateEndpoint(uri)
	require.NoError(t, err)

	ge, ok := endpoint.(*GRPCEndpoint)
	require.True(t, ok)
	assert.Equal(t, "api.internal:50051", ge.target)
	assert.Equal(t, "OrderService", ge.service)
	assert.Equal(t, "CreateOrder", ge.method)
	assert.Equal(t, "/OrderService/CreateOrder", ge.FullMethod())
}

func TestGRPCComponent_ClientServerIntegration(t *testing.T) {
	component := NewGRPCComponent()

	// 1. Setup server endpoint on 127.0.0.1:0
	serverEndpoint, err := component.CreateEndpoint("grpc://127.0.0.1:0/test.EchoService/Echo")
	require.NoError(t, err)

	consumer, err := serverEndpoint.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		req := string(e.In.GetBody().([]byte))
		e.Out.SetBody("Echo: " + req)
		return nil
	}))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = consumer.Start(ctx)
	require.NoError(t, err)
	defer consumer.Stop()

	serverConsumer := consumer.(*GRPCConsumer)
	serverAddr := serverConsumer.Address()
	require.NotEmpty(t, serverAddr)

	// Small pause to ensure listener is active
	time.Sleep(50 * time.Millisecond)

	// 2. Setup client producer targeting serverAddr
	clientEndpoint, err := component.CreateEndpoint("grpc://" + serverAddr + "/test.EchoService/Echo?insecure=true")
	require.NoError(t, err)

	producer, err := clientEndpoint.CreateProducer()
	require.NoError(t, err)

	err = producer.Start(ctx)
	require.NoError(t, err)
	defer producer.Stop()

	// 3. Send exchange
	exchange := gocamel.NewExchange(ctx)
	exchange.In.SetBody([]byte("Hello gRPC"))
	exchange.In.SetHeader("custom-header", "gocamel-grpc-test")

	err = producer.Send(exchange)
	require.NoError(t, err)

	respBody := exchange.Out.GetBody()
	require.NotNil(t, respBody)
	assert.Equal(t, []byte("Echo: Hello gRPC"), respBody.([]byte))

	statusCode, ok := exchange.GetOut().GetHeaderAsInt(CamelGrpcStatusCode)
	assert.True(t, ok)
	assert.Equal(t, 0, statusCode) // codes.OK == 0
}

func TestGRPCConsumer_StopReleasesEndpointRegistration(t *testing.T) {
	component := NewGRPCComponent()
	endpoint, err := component.CreateEndpoint("grpc://127.0.0.1:50059/Service/Method")
	require.NoError(t, err)

	consumer, err := endpoint.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	require.NoError(t, err)

	// Second consumer on same endpoint should fail
	_, err = endpoint.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	assert.Error(t, err)

	assert.NoError(t, consumer.Stop())

	// Re-register after stop should succeed
	_, err = endpoint.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	assert.NoError(t, err)
}
