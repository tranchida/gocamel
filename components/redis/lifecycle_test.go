package redis

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
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

func TestRedisConsumerRepeatableStartStopOnSameInstance(t *testing.T) {
	server := miniredis.RunT(t)
	ep, err := NewRedisComponent().CreateEndpoint("redis://" + server.Addr() + "?subscribe=true&channel=events")
	require.NoError(t, err)

	received := make(chan string, 4)
	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		body, _ := e.GetBodyAsString()
		received <- body
		return nil
	}))
	require.NoError(t, err)

	for _, msg := range []string{"cycle-1", "cycle-2"} {
		require.NoError(t, consumer.Start(context.Background()))
		// Duplicate Start is a safe no-op
		require.NoError(t, consumer.Start(context.Background()))
		server.Publish("events", msg)
		select {
		case got := <-received:
			assert.Equal(t, msg, got)
		case <-time.After(time.Second):
			t.Fatalf("did not receive %s", msg)
		}
		require.NoError(t, consumer.Stop())
		require.NoError(t, consumer.Stop())
	}
}

func TestRedisProducerStartFailureReleasesClientWithoutPublishing(t *testing.T) {
	ep, err := NewRedisComponent().CreateEndpoint("redis://127.0.0.1:6379?command=GET&key=k")
	require.NoError(t, err)
	rep := ep.(*RedisEndpoint)
	rep.options = &redis.Options{
		Addr:       "pipe:6379",
		MaxRetries: -1,
		Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return nil, errors.New("simulated dial failure")
		},
	}

	p, err := ep.CreateProducer()
	require.NoError(t, err)
	require.Error(t, p.Start(context.Background()))

	rp := p.(*RedisProducer)
	assert.Nil(t, rp.connection)
	rep.mu.Lock()
	assert.Nil(t, rep.connection)
	rep.mu.Unlock()
}

func TestRedisConsumerRepeatableStartStopInMemoryPipe(t *testing.T) {
	ep, err := NewRedisComponent().CreateEndpoint("redis://127.0.0.1:6379?subscribe=true&channel=events")
	require.NoError(t, err)
	rep := ep.(*RedisEndpoint)
	rep.options = &redis.Options{
		Addr:            "pipe:6379",
		Protocol:        2,
		DisableIdentity: true,
		Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c1, c2 := net.Pipe()
			go func() {
				defer c2.Close()
				buf := make([]byte, 4096)
				var acc string
				for {
					n, err := c2.Read(buf)
					if err != nil {
						return
					}
					acc += strings.ToUpper(string(buf[:n]))
					if strings.Contains(acc, "HELLO") && strings.Count(acc, "\r\n") >= 5 {
						acc = ""
						_, _ = c2.Write([]byte("-NOPROTO unsupported protocol\r\n"))
					} else if strings.Contains(acc, "EVENTS\r\n") {
						acc = ""
						_, _ = c2.Write([]byte("*3\r\n$9\r\nsubscribe\r\n$6\r\nevents\r\n:1\r\n*3\r\n$7\r\nmessage\r\n$6\r\nevents\r\n$5\r\nhello\r\n"))
					}
				}
			}()
			return c1, nil
		},
	}

	received := make(chan string, 4)
	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		body, _ := e.GetBodyAsString()
		received <- body
		return nil
	}))
	require.NoError(t, err)

	for range 2 {
		require.NoError(t, consumer.Start(context.Background()))
		require.NoError(t, consumer.Start(context.Background()))
		select {
		case got := <-received:
			assert.Equal(t, "hello", got)
		case <-time.After(time.Second):
			t.Fatal("did not receive message from pipe pubsub")
		}
		require.NoError(t, consumer.Stop())
		require.NoError(t, consumer.Stop())
	}
}
