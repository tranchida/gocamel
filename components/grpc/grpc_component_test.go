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

func TestGRPCComponent_AllowHeaderOverrideParsing(t *testing.T) {
	component := NewGRPCComponent()

	endpoint, err := component.CreateEndpoint("grpc://localhost:9090/Svc/Mth")
	require.NoError(t, err)
	assert.False(t, endpoint.(*GRPCEndpoint).allowHeaderOverride, "allowHeaderOverride must default to false")

	endpoint, err = component.CreateEndpoint("grpc://localhost:9090/Svc/Mth?allowHeaderOverride=true")
	require.NoError(t, err)
	assert.True(t, endpoint.(*GRPCEndpoint).allowHeaderOverride)
}

// startEchoServer hosts a gRPC server returning the invoked full method name.
func startEchoServer(t *testing.T, component *GRPCComponent, ctx context.Context) (string, *GRPCConsumer) {
	t.Helper()

	serverEndpoint, err := component.CreateEndpoint("grpc://127.0.0.1:0/test.EchoService/Echo")
	require.NoError(t, err)

	consumer, err := serverEndpoint.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		method, _ := e.GetHeaderAsString(CamelGrpcMethod)
		e.Out.SetBody(method)
		return nil
	}))
	require.NoError(t, err)
	require.NoError(t, consumer.Start(ctx))
	t.Cleanup(func() { _ = consumer.Stop() })

	time.Sleep(50 * time.Millisecond)
	serverConsumer := consumer.(*GRPCConsumer)
	require.NotEmpty(t, serverConsumer.Address())
	return serverConsumer.Address(), serverConsumer
}

func newStartedProducer(t *testing.T, component *GRPCComponent, ctx context.Context, uri string) gocamel.Producer {
	t.Helper()
	endpoint, err := component.CreateEndpoint(uri)
	require.NoError(t, err)
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, producer.Start(ctx))
	t.Cleanup(func() { _ = producer.Stop() })
	return producer
}

func sendAndGetMethod(t *testing.T, producer gocamel.Producer, ctx context.Context, headerMethod string) (string, error) {
	t.Helper()
	exchange := gocamel.NewExchange(ctx)
	exchange.In.SetBody([]byte("ping"))
	if headerMethod != "" {
		exchange.In.SetHeader(CamelGrpcMethod, headerMethod)
	}
	err := producer.Send(exchange)
	if err != nil {
		return "", err
	}
	return string(exchange.Out.GetBody().([]byte)), nil
}

func TestGRPCProducer_HeaderOverrideIgnoredByDefault(t *testing.T) {
	component := NewGRPCComponent()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, _ := startEchoServer(t, component, ctx)
	producer := newStartedProducer(t, component, ctx, "grpc://"+addr+"/test.EchoService/Echo?insecure=true")

	// Without allowHeaderOverride, CamelGrpcMethod must be ignored.
	method, err := sendAndGetMethod(t, producer, ctx, "/test.OtherService/Other")
	require.NoError(t, err)
	assert.Equal(t, "/test.EchoService/Echo", method)
}

func TestGRPCProducer_HeaderOverrideApplied(t *testing.T) {
	component := NewGRPCComponent()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, _ := startEchoServer(t, component, ctx)
	producer := newStartedProducer(t, component, ctx,
		"grpc://"+addr+"/test.EchoService/Echo?insecure=true&allowHeaderOverride=true")

	// With allowHeaderOverride=true, CamelGrpcMethod replaces the configured method.
	method, err := sendAndGetMethod(t, producer, ctx, "/helloworld.Greeter/SayHello")
	require.NoError(t, err)
	assert.Equal(t, "/helloworld.Greeter/SayHello", method)

	// The "Service/Method" notation (without leading slash) is also accepted.
	method, err = sendAndGetMethod(t, producer, ctx, "helloworld.Greeter/SayHello")
	require.NoError(t, err)
	assert.Equal(t, "/helloworld.Greeter/SayHello", method)
}

func TestGRPCProducer_HeaderOverrideRejectsMalformedMethod(t *testing.T) {
	component := NewGRPCComponent()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, _ := startEchoServer(t, component, ctx)
	producer := newStartedProducer(t, component, ctx,
		"grpc://"+addr+"/test.EchoService/Echo?insecure=true&allowHeaderOverride=true")

	for _, bad := range []string{"no-slash", "9bad/Method", "/Service/", "Service/Method/", "Service//Method", "//Service/Method"} {
		_, err := sendAndGetMethod(t, producer, ctx, bad)
		require.Error(t, err, "malformed method %q must be rejected", bad)
		assert.Contains(t, err.Error(), "malformed method")
	}
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
