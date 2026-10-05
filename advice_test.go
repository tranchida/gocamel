package gocamel

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realEndpointGuard starts a route consuming uri that fails the test if any
// exchange reaches it.
func realEndpointGuard(t *testing.T, ctx *CamelContext, uri string) {
	t.Helper()
	_, err := ctx.CreateRouteBuilder().
		From(uri).
		ProcessFunc(func(*Exchange) error {
			t.Errorf("exchange reached %s, which should have been advised away", uri)
			return nil
		}).
		Build()
	require.NoError(t, err)
}

func sendBody(t *testing.T, ctx *CamelContext, uri string, body any) {
	t.Helper()
	producer, err := ctx.GetProducer(uri)
	require.NoError(t, err)
	exchange := NewExchange(context.Background())
	exchange.In.SetBody(body)
	require.NoError(t, producer.Send(exchange))
}

func TestAdviceWith_ReplacesToWithMock(t *testing.T) {
	ctx := NewCamelContext()
	realEndpointGuard(t, ctx, "direct:real")
	route, err := ctx.CreateRouteBuilder().
		From("direct:in").
		SetBody("advised").
		To("direct:real").
		Build()
	require.NoError(t, err)

	require.NoError(t, route.AdviceWith(map[string]string{"direct:real": "mock:out"}))

	mock, err := ctx.GetMockEndpoint("mock:out")
	require.NoError(t, err)
	mock.ExpectedMessageCount(1)
	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })

	sendBody(t, ctx, "direct:in", "original")

	require.NoError(t, mock.Wait(time.Second))
	assert.Equal(t, "advised", mock.Body(0))
}

func TestAdviceWith_NestedAndMulticastProducers(t *testing.T) {
	ctx := NewCamelContext()
	realEndpointGuard(t, ctx, "direct:a")
	realEndpointGuard(t, ctx, "direct:b")
	realEndpointGuard(t, ctx, "direct:c")
	route, err := ctx.CreateRouteBuilder().
		From("direct:in").
		To("direct:a", "direct:b").
		Choice().
		When("${body} == 'x'").
		To("direct:c").
		EndChoice().
		Build()
	require.NoError(t, err)

	require.NoError(t, route.AdviceWith(map[string]string{
		"direct:a": "mock:a",
		"direct:b": "mock:b",
		"direct:c": "mock:c",
	}))
	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })

	sendBody(t, ctx, "direct:in", "x")

	for _, name := range []string{"mock:a", "mock:b", "mock:c"} {
		mock, err := ctx.GetMockEndpoint(name)
		require.NoError(t, err)
		mock.ExpectedMessageCount(1)
		assert.NoError(t, mock.Wait(time.Second), name)
	}
}

func TestAdviceWith_SubstitutionIsNotTransitive(t *testing.T) {
	ctx := NewCamelContext()
	route, err := ctx.CreateRouteBuilder().
		From("direct:in").
		To("mock:a").
		To("mock:b").
		Build()
	require.NoError(t, err)

	// A swap: a single pass, so a->b and b->a do not chain into a cycle.
	require.NoError(t, route.AdviceWith(map[string]string{"mock:a": "mock:b", "mock:b": "mock:a"}))
	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })

	sendBody(t, ctx, "direct:in", "x")
	a, _ := ctx.GetMockEndpoint("mock:a")
	b, _ := ctx.GetMockEndpoint("mock:b")
	assert.Equal(t, 1, a.MessageCount())
	assert.Equal(t, 1, b.MessageCount())
}

func TestAdviceWith_AfterStartReturnsError(t *testing.T) {
	ctx := NewCamelContext()
	route, err := ctx.CreateRouteBuilder().
		From("direct:in").
		To("mock:real").
		Build()
	require.NoError(t, err)
	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })

	err = route.AdviceWith(map[string]string{"mock:real": "mock:other"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "before Start")

	sendBody(t, ctx, "direct:in", "x")
	real, _ := ctx.GetMockEndpoint("mock:real")
	assert.Equal(t, 1, real.MessageCount(), "a rejected advice must not rewrite anything")
}

func TestAdviceWith_AfterStopIsAllowed(t *testing.T) {
	ctx := NewCamelContext()
	route, err := ctx.CreateRouteBuilder().
		From("direct:in").
		To("mock:real").
		Build()
	require.NoError(t, err)
	require.NoError(t, ctx.Start())
	sendBody(t, ctx, "direct:in", "first")
	require.NoError(t, ctx.Stop())

	require.NoError(t, route.AdviceWith(map[string]string{"mock:real": "mock:other"}))
	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })
	sendBody(t, ctx, "direct:in", "second")

	other, _ := ctx.GetMockEndpoint("mock:other")
	require.Equal(t, 1, other.MessageCount())
	assert.Equal(t, "second", other.Body(0))
}

func TestAdviceWith_UnknownURIIsNoOp(t *testing.T) {
	ctx := NewCamelContext()
	route, err := ctx.CreateRouteBuilder().
		From("direct:in").
		To("mock:real").
		Build()
	require.NoError(t, err)

	assert.NoError(t, route.AdviceWith(map[string]string{"direct:nowhere": "mock:other"}))
	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })

	sendBody(t, ctx, "direct:in", "x")
	real, _ := ctx.GetMockEndpoint("mock:real")
	other, _ := ctx.GetMockEndpoint("mock:other")
	assert.Equal(t, 1, real.MessageCount())
	assert.Equal(t, 0, other.MessageCount())
}

func TestAdviceWith_ToDIsNotSubstituted(t *testing.T) {
	ctx := NewCamelContext()
	route, err := ctx.CreateRouteBuilder().
		From("direct:in").
		ToD("mock:dynamic").
		Build()
	require.NoError(t, err)

	require.NoError(t, route.AdviceWith(map[string]string{"mock:dynamic": "mock:other"}))
	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })

	sendBody(t, ctx, "direct:in", "x")
	dynamic, _ := ctx.GetMockEndpoint("mock:dynamic")
	assert.Equal(t, 1, dynamic.MessageCount())
}

func TestAdviceWith_EndToEndTimer(t *testing.T) {
	ctx := NewCamelContext()
	realEndpointGuard(t, ctx, "direct:billing")
	route, err := ctx.CreateRouteBuilder().
		From("timer:tick?period=10ms").
		SetBody("tick").
		To("direct:billing").
		Build()
	require.NoError(t, err)
	require.NoError(t, route.AdviceWith(map[string]string{"direct:billing": "mock:billing"}))

	mock, err := ctx.GetMockEndpoint("billing")
	require.NoError(t, err)
	mock.ExpectedMessageCount(2)
	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })

	require.NoError(t, mock.Wait(5*time.Second))
	assert.Equal(t, "tick", mock.Body(0))
}
