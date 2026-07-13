package gocamel

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestHTTPConsumer_StartReturnsBindError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	consumer := &HTTPConsumer{
		uri:       "http://" + listener.Addr().String() + "/test",
		processor: ProcessorFunc(func(*Exchange) error { return nil }),
	}
	err = consumer.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "error listening")
}

func TestHTTPConsumer_BindsConfiguredHost(t *testing.T) {
	consumer := &HTTPConsumer{
		uri:       "http://127.0.0.1:0/test",
		processor: ProcessorFunc(func(*Exchange) error { return nil }),
	}
	require.NoError(t, consumer.Start(context.Background()))
	assert.Equal(t, "127.0.0.1:0", consumer.server.Addr)
	require.NoError(t, consumer.Stop())
}

func TestHTTPProducer_UsesExchangeContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var requestContext context.Context
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		requestContext = req.Context()
		return nil, req.Context().Err()
	})}
	producer := &HTTPProducer{uri: "http://example.test", client: client}
	exchange := NewExchange(ctx)

	err := producer.Send(exchange)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.True(t, errors.Is(requestContext.Err(), context.Canceled))
}
