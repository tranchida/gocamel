package gocamel

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChanComponent_Basic(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())
	camelCtx.AddComponent("chan", NewChanComponent())

	done := make(chan string, 1)

	// Consumer route: chan:incoming -> ProcessFunc
	_, err := camelCtx.CreateRouteBuilder().
		From("chan:incoming?bufferSize=5").
		ProcessFunc(func(e *Exchange) error {
			body, _ := e.GetBodyAsString()
			done <- body
			return nil
		}).
		Build()
	assert.NoError(t, err)

	// Producer route: direct:start -> chan:incoming
	_, err = camelCtx.CreateRouteBuilder().
		From("direct:start").
		To("chan:incoming?bufferSize=5").
		Build()
	assert.NoError(t, err)

	err = camelCtx.Start()
	assert.NoError(t, err)
	defer camelCtx.Stop()

	// Send message
	endpoint, _ := camelCtx.CreateEndpoint("direct:start")
	producer, _ := endpoint.CreateProducer()

	exchange := NewExchange(context.Background())
	exchange.In.SetBody("Hello Go Channels")

	err = producer.Send(exchange)
	assert.NoError(t, err)

	// Wait for asynchronous processing
	select {
	case body := <-done:
		assert.Equal(t, "Hello Go Channels", body)
	case <-time.After(1 * time.Second):
		t.Fatal("Timed out waiting for channel message")
	}
}

func TestChanComponent_MultipleExchanges(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())
	camelCtx.AddComponent("chan", NewChanComponent())

	done := make(chan string, 3)

	_, err := camelCtx.CreateRouteBuilder().
		From("chan:orders?bufferSize=10").
		ProcessFunc(func(e *Exchange) error {
			body, _ := e.GetBodyAsString()
			done <- body
			return nil
		}).
		Build()
	assert.NoError(t, err)

	_, err = camelCtx.CreateRouteBuilder().
		From("direct:start").
		To("chan:orders?bufferSize=10").
		Build()
	assert.NoError(t, err)

	err = camelCtx.Start()
	assert.NoError(t, err)
	defer camelCtx.Stop()

	endpoint, _ := camelCtx.CreateEndpoint("direct:start")
	producer, _ := endpoint.CreateProducer()

	// Send 3 messages in sequence
	for _, msg := range []string{"msg1", "msg2", "msg3"} {
		ex := NewExchange(context.Background())
		ex.In.SetBody(msg)
		_ = producer.Send(ex)
	}

	// Verify all received in order
	expected := []string{"msg1", "msg2", "msg3"}
	for _, exp := range expected {
		select {
		case val := <-done:
			assert.Equal(t, exp, val)
		case <-time.After(1 * time.Second):
			t.Fatalf("Timed out waiting for %s", exp)
		}
	}
}

func TestChanConsumer_StopReleasesEndpointRegistration(t *testing.T) {
	component := NewChanComponent()
	endpoint, err := component.CreateEndpoint("chan:restart")
	require.NoError(t, err)

	consumer, err := endpoint.CreateConsumer(ProcessorFunc(func(*Exchange) error { return nil }))
	require.NoError(t, err)
	require.NoError(t, consumer.Start(context.Background()))
	require.NoError(t, consumer.Stop())

	_, err = endpoint.CreateConsumer(ProcessorFunc(func(*Exchange) error { return nil }))
	assert.NoError(t, err)
}

func TestChanComponent_NoDuplicateWarningWithoutQuery(t *testing.T) {
	buf := &syncBuffer{}
	prev := GetLogger()
	t.Cleanup(func() { SetLogger(prev) })
	SetLogger(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn})))

	comp := NewChanComponent()
	ep1, err := comp.CreateEndpoint("chan:orders?bufferSize=10")
	require.NoError(t, err)
	ep2, err := comp.CreateEndpoint("chan:orders")
	require.NoError(t, err)
	assert.Same(t, ep1, ep2)

	assert.NotContains(t, buf.String(), "URI parameters ignored")

	ep3, err := comp.CreateEndpoint("chan:orders?bufferSize=20")
	require.NoError(t, err)
	assert.Same(t, ep1, ep3)

	assert.Contains(t, buf.String(), "URI parameters ignored")
}
