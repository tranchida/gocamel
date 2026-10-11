package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gocamel "github.com/tranchida/gocamel"
)

func TestConsumerRouteRestarts(t *testing.T) {
	server := miniredis.RunT(t)
	c := gocamel.NewCamelContext()
	c.AddComponent("redis", NewRedisComponent())
	received := make(chan string, 2)
	_, err := c.CreateRouteBuilder().From("redis://" + server.Addr() + "?subscribe=true&channel=events").ProcessFunc(func(e *gocamel.Exchange) error {
		body, _ := e.GetBodyAsString()
		received <- body
		return nil
	}).Build()
	require.NoError(t, err)
	defer c.Stop()
	for _, body := range []string{"first", "second"} {
		require.NoError(t, c.Start())
		server.Publish("events", body)
		select {
		case actual := <-received:
			assert.Equal(t, body, actual)
		case <-time.After(time.Second):
			t.Fatal("message not delivered")
		}
		require.NoError(t, c.Stop())
	}
}

func TestSharedClientSurvivesProducerStop(t *testing.T) {
	server := miniredis.RunT(t)
	ep, err := NewRedisComponent().CreateEndpoint("redis://" + server.Addr() + "?command=SET&key=item")
	require.NoError(t, err)
	first, err := ep.CreateProducer()
	require.NoError(t, err)
	second, err := ep.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, first.Start(context.Background()))
	require.NoError(t, second.Start(context.Background()))
	defer second.Stop()
	require.NoError(t, first.Stop())
	require.NoError(t, first.Stop())
	e := gocamel.NewExchange(context.Background())
	e.SetBody("survives")
	require.NoError(t, second.Send(e))
	value, err := server.Get("item")
	require.NoError(t, err)
	assert.Equal(t, "survives", value)
}
