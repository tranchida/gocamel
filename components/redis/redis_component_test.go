package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"

	gocamel "github.com/tranchida/gocamel"
)

func TestRedisComponent_Producer(t *testing.T) {
	// Spin up in-memory miniredis server
	s, err := miniredis.Run()
	assert.NoError(t, err)
	defer s.Close()

	camelCtx := gocamel.NewCamelContext()
	camelCtx.AddComponent("direct", gocamel.NewDirectComponent())

	// Create and register redis component with miniredis address
	redisComp := NewRedisComponent()
	camelCtx.AddComponent("redis", redisComp)

	// Set Route: direct:set -> redis (SET hello)
	_, err = camelCtx.CreateRouteBuilder().
		From("direct:set").
		To("redis://" + s.Addr() + "?command=SET&key=hello").
		Build()
	assert.NoError(t, err)

	// Get Route: direct:get -> redis (GET hello)
	_, err = camelCtx.CreateRouteBuilder().
		From("direct:get").
		To("redis://" + s.Addr() + "?command=GET&key=hello").
		Build()
	assert.NoError(t, err)

	err = camelCtx.Start()
	assert.NoError(t, err)
	defer camelCtx.Stop()

	// 1. SET exchange
	setEndpoint, _ := camelCtx.CreateEndpoint("direct:set")
	setProducer, _ := setEndpoint.CreateProducer()

	setExchange := gocamel.NewExchange(context.Background())
	setExchange.In.SetBody("World")

	err = setProducer.Send(setExchange)
	assert.NoError(t, err)

	val, err := s.Get("hello") // Verify directly in miniredis
	assert.NoError(t, err)
	assert.Equal(t, "World", val)

	// 2. GET exchange
	getEndpoint, _ := camelCtx.CreateEndpoint("direct:get")
	getProducer, _ := getEndpoint.CreateProducer()

	getExchange := gocamel.NewExchange(context.Background())
	err = getProducer.Send(getExchange)
	assert.NoError(t, err)
	assert.Equal(t, "World", getExchange.Out.GetBody())
}

// Default (secure) behavior: without allowHeaderOverride=true, the
// CamelRedisCommand/CamelRedisKey/CamelRedisChannel headers are ignored and
// only the URI parameters apply.
func TestRedisComponent_ProducerHeadersIgnoredByDefault(t *testing.T) {
	s, _ := miniredis.Run()
	defer s.Close()

	camelCtx := gocamel.NewCamelContext()
	camelCtx.AddComponent("direct", gocamel.NewDirectComponent())
	camelCtx.AddComponent("redis", NewRedisComponent())

	// Route with fixed URI config (command=SET, key=fixed) — no opt-in.
	_, err := camelCtx.CreateRouteBuilder().
		From("direct:start").
		To("redis://" + s.Addr() + "?command=SET&key=fixed").
		Build()
	assert.NoError(t, err)

	_ = camelCtx.Start()
	defer camelCtx.Stop()

	endpoint, _ := camelCtx.CreateEndpoint("direct:start")
	producer, _ := endpoint.CreateProducer()

	// Headers attempt to override command and key, but must be ignored.
	ex := gocamel.NewExchange(context.Background())
	ex.In.SetHeader(CamelRedisCommand, "GET")
	ex.In.SetHeader(CamelRedisKey, "other")
	ex.In.SetBody("Value")
	err = producer.Send(ex)
	assert.NoError(t, err)

	// The fixed URI key was written, not the header-provided one.
	val, err := s.Get("fixed")
	assert.NoError(t, err)
	assert.Equal(t, "Value", val)
	assert.False(t, s.Exists("other"))
	assert.Equal(t, "OK", ex.Out.GetBody())
}

func TestRedisComponent_ProducerHeadersOverrideAllowed(t *testing.T) {
	s, _ := miniredis.Run()
	defer s.Close()

	camelCtx := gocamel.NewCamelContext()
	camelCtx.AddComponent("direct", gocamel.NewDirectComponent())
	camelCtx.AddComponent("redis", NewRedisComponent())

	// Generic route where command and key are dynamic via headers (opt-in).
	_, err := camelCtx.CreateRouteBuilder().
		From("direct:start").
		To("redis://" + s.Addr() + "?allowHeaderOverride=true").
		Build()
	assert.NoError(t, err)

	_ = camelCtx.Start()
	defer camelCtx.Stop()

	endpoint, _ := camelCtx.CreateEndpoint("direct:start")
	producer, _ := endpoint.CreateProducer()

	// 1. SET dynamically
	ex1 := gocamel.NewExchange(context.Background())
	ex1.In.SetHeader(CamelRedisCommand, "SET")
	ex1.In.SetHeader(CamelRedisKey, "fruit")
	ex1.In.SetBody("Mango")
	err = producer.Send(ex1)
	assert.NoError(t, err)

	val, err := s.Get("fruit")
	assert.NoError(t, err)
	assert.Equal(t, "Mango", val)

	// 2. GET dynamically
	ex2 := gocamel.NewExchange(context.Background())
	ex2.In.SetHeader(CamelRedisCommand, "GET")
	ex2.In.SetHeader(CamelRedisKey, "fruit")
	err = producer.Send(ex2)
	assert.NoError(t, err)
	assert.Equal(t, "Mango", ex2.Out.GetBody())

	// 3. DEL dynamically
	ex3 := gocamel.NewExchange(context.Background())
	ex3.In.SetHeader(CamelRedisCommand, "DEL")
	ex3.In.SetHeader(CamelRedisKey, "fruit")
	err = producer.Send(ex3)
	assert.NoError(t, err)
	assert.Equal(t, int64(1), ex3.Out.GetBody())
	assert.False(t, s.Exists("fruit"))
}

func TestRedisComponent_PubSub(t *testing.T) {
	s, _ := miniredis.Run()
	defer s.Close()

	camelCtx := gocamel.NewCamelContext()
	camelCtx.AddComponent("direct", gocamel.NewDirectComponent())
	camelCtx.AddComponent("redis", NewRedisComponent())

	// Buffered channel: the consumer goroutine writes, the test goroutine reads.
	// A shared variable with a sleep would be a data race (no happens-before).
	received := make(chan string, 1)

	// Consumer route: redis pubsub (subscribe to 'news') -> ProcessFunc
	_, err := camelCtx.CreateRouteBuilder().
		From("redis://" + s.Addr() + "?subscribe=true&channel=news").
		ProcessFunc(func(e *gocamel.Exchange) error {
			body, _ := e.GetBodyAsString()
			received <- body
			return nil
		}).
		Build()
	assert.NoError(t, err)

	// Producer route: direct:publish -> redis PUBLISH news
	_, err = camelCtx.CreateRouteBuilder().
		From("direct:publish").
		To("redis://" + s.Addr() + "?command=PUBLISH&channel=news").
		Build()
	assert.NoError(t, err)

	err = camelCtx.Start()
	assert.NoError(t, err)
	defer camelCtx.Stop()

	// Connect direct go-redis client to verify pubsub is active
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer client.Close()

	// Wait briefly for the subscription to register in miniredis
	time.Sleep(50 * time.Millisecond)

	// Publish via direct:publish
	pubEndpoint, _ := camelCtx.CreateEndpoint("direct:publish")
	pubProducer, _ := pubEndpoint.CreateProducer()

	pubExchange := gocamel.NewExchange(context.Background())
	pubExchange.In.SetBody("Breaking News!")

	err = pubProducer.Send(pubExchange)
	assert.NoError(t, err)

	// Wait for asynchronous consumer processing
	select {
	case body := <-received:
		assert.Equal(t, "Breaking News!", body)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the pubsub message to be consumed")
	}
}

func TestRedisEndpoint_URI_RedactsCredentials(t *testing.T) {
	comp := NewRedisComponent()
	ep, err := comp.CreateEndpoint("redis://:super-secret@localhost:6379?password=query-secret&key=mykey")
	assert.NoError(t, err)

	uri := ep.URI()
	assert.NotContains(t, uri, "super-secret")
	assert.NotContains(t, uri, "query-secret")
	assert.Contains(t, uri, "key=mykey")
}

func TestRedisConsumer_StartFailureReleasesConnection(t *testing.T) {
	s, err := miniredis.Run()
	assert.NoError(t, err)
	addr := s.Addr()
	// Close miniredis immediately so Receive(ctx) fails during consumer Start.
	s.Close()

	comp := NewRedisComponent()
	ep, err := comp.CreateEndpoint("redis://" + addr + "?subscribe=true&channel=news")
	assert.NoError(t, err)
	redisEp := ep.(*RedisEndpoint)

	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	assert.NoError(t, err)
	redisConsumer := consumer.(*RedisConsumer)

	err = consumer.Start(context.Background())
	assert.Error(t, err)
	assert.Nil(t, redisConsumer.pubSub)
	assert.Nil(t, redisConsumer.connection)

	redisEp.mu.Lock()
	conn := redisEp.connection
	redisEp.mu.Unlock()
	assert.Nil(t, conn, "failed Start must release the endpoint connection")

	// Calling Stop after a failed Start must be a safe no-op.
	assert.NoError(t, consumer.Stop())
}

