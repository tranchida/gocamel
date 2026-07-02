package gocamel

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

func TestRedisComponent_Producer(t *testing.T) {
	// Spin up in-memory miniredis server
	s, err := miniredis.Run()
	assert.NoError(t, err)
	defer s.Close()

	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())
	
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

	setExchange := NewExchange(context.Background())
	setExchange.In.SetBody("World")

	err = setProducer.Send(setExchange)
	assert.NoError(t, err)

	val, err := s.Get("hello") // Verify directly in miniredis
	assert.NoError(t, err)
	assert.Equal(t, "World", val)

	// 2. GET exchange
	getEndpoint, _ := camelCtx.CreateEndpoint("direct:get")
	getProducer, _ := getEndpoint.CreateProducer()

	getExchange := NewExchange(context.Background())
	err = getProducer.Send(getExchange)
	assert.NoError(t, err)
	assert.Equal(t, "World", getExchange.Out.GetBody())
}

func TestRedisComponent_ProducerHeaders(t *testing.T) {
	s, _ := miniredis.Run()
	defer s.Close()

	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())
	camelCtx.AddComponent("redis", NewRedisComponent())

	// Generic route where command and key are dynamic via headers
	_, err := camelCtx.CreateRouteBuilder().
		From("direct:start").
		To("redis://" + s.Addr()).
		Build()
	assert.NoError(t, err)

	_ = camelCtx.Start()
	defer camelCtx.Stop()

	endpoint, _ := camelCtx.CreateEndpoint("direct:start")
	producer, _ := endpoint.CreateProducer()

	// 1. SET dynamically
	ex1 := NewExchange(context.Background())
	ex1.In.SetHeader(CamelRedisCommand, "SET")
	ex1.In.SetHeader(CamelRedisKey, "fruit")
	ex1.In.SetBody("Mango")
	err = producer.Send(ex1)
	assert.NoError(t, err)

	val, err := s.Get("fruit")
	assert.NoError(t, err)
	assert.Equal(t, "Mango", val)

	// 2. GET dynamically
	ex2 := NewExchange(context.Background())
	ex2.In.SetHeader(CamelRedisCommand, "GET")
	ex2.In.SetHeader(CamelRedisKey, "fruit")
	err = producer.Send(ex2)
	assert.NoError(t, err)
	assert.Equal(t, "Mango", ex2.Out.GetBody())

	// 3. DEL dynamically
	ex3 := NewExchange(context.Background())
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

	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())
	camelCtx.AddComponent("redis", NewRedisComponent())

	// Buffered channel: the consumer goroutine writes, the test goroutine reads.
	// A shared variable with a sleep would be a data race (no happens-before).
	received := make(chan string, 1)

	// Consumer route: redis pubsub (subscribe to 'news') -> ProcessFunc
	_, err := camelCtx.CreateRouteBuilder().
		From("redis://" + s.Addr() + "?subscribe=true&channel=news").
		ProcessFunc(func(e *Exchange) error {
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

	pubExchange := NewExchange(context.Background())
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
