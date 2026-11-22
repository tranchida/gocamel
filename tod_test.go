package gocamel

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToD(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	receivedBody := ""
	ctx.CreateRouteBuilder().
		From("direct:start").
		ToD("direct:${header.target}").
		Build()

	ctx.CreateRouteBuilder().
		From("direct:a").
		ProcessFunc(func(exchange *Exchange) error {
			receivedBody = exchange.GetIn().GetBody().(string)
			return nil
		}).
		Build()

	ctx.CreateRouteBuilder().
		From("direct:b").
		ProcessFunc(func(exchange *Exchange) error {
			receivedBody = exchange.GetIn().GetBody().(string)
			return nil
		}).
		Build()

	err := ctx.Start()
	require.NoError(t, err)
	defer ctx.Stop()

	// Test 1: Send to 'a'
	exchange1 := NewExchange(context.Background())
	exchange1.In.SetBody("Hello A")
	exchange1.In.SetHeader("target", "a")

	endpoint, err := ctx.CreateEndpoint("direct:start")
	require.NoError(t, err)
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	err = producer.Start(context.Background())
	require.NoError(t, err)

	err = producer.Send(exchange1)
	assert.NoError(t, err)
	assert.Equal(t, "Hello A", receivedBody)

	// Test 2: Send to 'b'
	exchange2 := NewExchange(context.Background())
	exchange2.In.SetBody("Hello B")
	exchange2.In.SetHeader("target", "b")

	err = producer.Send(exchange2)
	assert.NoError(t, err)
	assert.Equal(t, "Hello B", receivedBody)
}

func TestToDWithPropertyAndBody(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	receivedURI := ""
	ctx.CreateRouteBuilder().
		From("direct:start").
		ToD("direct:${property.prefix}-${body}").
		Build()

	// Intercept all direct routes to see which one is called
	ctx.CreateRouteBuilder().
		From("direct:msg-hello").
		ProcessFunc(func(exchange *Exchange) error {
			receivedURI = "direct:msg-hello"
			return nil
		}).
		Build()

	err := ctx.Start()
	require.NoError(t, err)
	defer ctx.Stop()

	exchange := NewExchange(context.Background())
	exchange.In.SetBody("hello")
	exchange.SetProperty("prefix", "msg")

	endpoint, err := ctx.CreateEndpoint("direct:start")
	require.NoError(t, err)
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	err = producer.Start(context.Background())
	require.NoError(t, err)

	err = producer.Send(exchange)
	assert.NoError(t, err)
	assert.Equal(t, "direct:msg-hello", receivedURI)
}

func TestToDInSplit(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	var receivedBodies []string
	ctx.CreateRouteBuilder().
		From("direct:start").
		Split(func(e *Exchange) (any, error) {
			return []string{"a", "b"}, nil
		}).
		ToD("direct:${body}").
		End()

	ctx.CreateRouteBuilder().
		From("direct:a").
		ProcessFunc(func(exchange *Exchange) error {
			receivedBodies = append(receivedBodies, "received-a")
			return nil
		}).
		Build()

	ctx.CreateRouteBuilder().
		From("direct:b").
		ProcessFunc(func(exchange *Exchange) error {
			receivedBodies = append(receivedBodies, "received-b")
			return nil
		}).
		Build()

	err := ctx.Start()
	require.NoError(t, err)
	defer ctx.Stop()

	exchange := NewExchange(context.Background())
	exchange.In.SetBody("start")

	endpoint, err := ctx.CreateEndpoint("direct:start")
	require.NoError(t, err)
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	err = producer.Start(context.Background())
	require.NoError(t, err)

	err = producer.Send(exchange)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"received-a", "received-b"}, receivedBodies)
}

type todSpyComponent struct {
	mu   sync.Mutex
	uris []string
}

func (c *todSpyComponent) CreateEndpoint(uri string) (Endpoint, error) {
	u, err := ParseURI(uri)
	if err != nil {
		return nil, err
	}
	_ = u
	c.mu.Lock()
	c.uris = append(c.uris, uri)
	c.mu.Unlock()
	return &todSpyEndpoint{uri: uri}, nil
}

func (c *todSpyComponent) lastURI() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.uris) == 0 {
		return ""
	}
	return c.uris[len(c.uris)-1]
}

type todSpyEndpoint struct{ uri string }

func (e *todSpyEndpoint) URI() string                       { return e.uri }
func (e *todSpyEndpoint) CreateProducer() (Producer, error) { return &todSpyProducer{}, nil }
func (e *todSpyEndpoint) CreateConsumer(Processor) (Consumer, error) {
	return nil, nil
}

type todSpyProducer struct{}

func (p *todSpyProducer) Start(context.Context) error { return nil }
func (p *todSpyProducer) Stop() error                 { return nil }
func (p *todSpyProducer) Send(*Exchange) error        { return nil }

func TestToD_LeadingPlaceholderAndAuthoritySSRF(t *testing.T) {
	t.Run("leading placeholder query injection is escaped", func(t *testing.T) {
		ctx := NewCamelContext()
		spy := &todSpyComponent{}
		ctx.AddComponent("rec", spy)
		require.NoError(t, ctx.Start())
		t.Cleanup(func() { _ = ctx.Stop() })

		ex := NewExchange(context.Background())
		ex.SetHeader("base", "rec:search")
		ex.SetHeader("user", "x&allowHeaderOverride=true")

		require.NoError(t, createToDProcessor(ctx, "${header.base}?q=${header.user}").Process(ex))

		u, err := ParseURI(spy.lastURI())
		require.NoError(t, err)
		assert.Equal(t, "x&allowHeaderOverride=true", u.Query().Get("q"))
		assert.Empty(t, u.Query().Get("allowHeaderOverride"))
	})

	t.Run("authority SSRF cannot hijack host", func(t *testing.T) {
		ctx := NewCamelContext()
		spy := &todSpyComponent{}
		ctx.AddComponent("http", spy)
		require.NoError(t, ctx.Start())
		t.Cleanup(func() { _ = ctx.Stop() })

		for _, tenant := range []string{
			"169.254.169.254/latest/",
			"x@169.254.169.254/",
		} {
			ex := NewExchange(context.Background())
			ex.SetHeader("tenant", tenant)

			err := createToDProcessor(ctx, "http://${header.tenant}.internal/api").Process(ex)
			require.Error(t, err)
			assert.Empty(t, spy.lastURI())
		}

		// Valid tenant still resolves cleanly to *.internal.
		ex := NewExchange(context.Background())
		ex.SetHeader("tenant", "acme")
		require.NoError(t, createToDProcessor(ctx, "http://${header.tenant}.internal/api").Process(ex))
		u, err := ParseURI(spy.lastURI())
		require.NoError(t, err)
		assert.True(t, strings.HasSuffix(u.Host, ".internal"))
		assert.Equal(t, "acme.internal", u.Host)
	})
}
