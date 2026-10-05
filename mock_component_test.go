package gocamel

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sendToMock(t *testing.T, ctx *CamelContext, uri string, body any, headers map[string]any) *Exchange {
	t.Helper()
	endpoint, err := ctx.CreateEndpoint(uri)
	require.NoError(t, err)
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, producer.Start(context.Background()))
	t.Cleanup(func() { _ = producer.Stop() })

	exchange := NewExchange(context.Background())
	exchange.In.SetBody(body)
	for k, v := range headers {
		exchange.In.SetHeader(k, v)
	}
	require.NoError(t, producer.Send(exchange))
	return exchange
}

func TestMockComponent_RegisteredByDefault(t *testing.T) {
	ctx := NewCamelContext()
	_, err := ctx.GetComponent("mock")
	assert.NoError(t, err)
}

func TestMockComponent_SameNameResolvesToSameEndpoint(t *testing.T) {
	ctx := NewCamelContext()
	a, err := ctx.CreateEndpoint("mock:out")
	require.NoError(t, err)
	b, err := ctx.CreateEndpoint("mock:out?ignored=1")
	require.NoError(t, err)
	assert.Same(t, a, b)

	m, err := ctx.GetMockEndpoint("mock:out")
	require.NoError(t, err)
	assert.Same(t, a, m)
	byName, err := ctx.GetMockEndpoint("out")
	require.NoError(t, err)
	assert.Same(t, a, byName)
}

func TestMockComponent_ConsumerUnsupported(t *testing.T) {
	ctx := NewCamelContext()
	endpoint, err := ctx.CreateEndpoint("mock:out")
	require.NoError(t, err)
	_, err = endpoint.CreateConsumer(ProcessorFunc(func(*Exchange) error { return nil }))
	assert.Error(t, err)
}

func TestMockEndpoint_RecordsMessagesInOrder(t *testing.T) {
	ctx := NewCamelContext()
	first := sendToMock(t, ctx, "mock:out", "one", map[string]any{"n": 1})
	sendToMock(t, ctx, "mock:out", "two", map[string]any{"n": 2})

	mock, err := ctx.GetMockEndpoint("mock:out")
	require.NoError(t, err)
	require.Equal(t, 2, mock.MessageCount())

	assert.Equal(t, "one", mock.Body(0))
	assert.Equal(t, "two", mock.Body(1))
	n, ok := mock.Header(0, "n")
	assert.True(t, ok)
	assert.Equal(t, 1, n)
	n, ok = mock.Header(1, "n")
	assert.True(t, ok)
	assert.Equal(t, 2, n)
	_, ok = mock.Header(0, "missing")
	assert.False(t, ok)
	assert.Equal(t, first.ID, mock.Message(0).ID)
}

func TestMockEndpoint_OutOfRangeAccessors(t *testing.T) {
	ctx := NewCamelContext()
	mock, err := ctx.GetMockEndpoint("mock:empty")
	require.NoError(t, err)
	assert.Nil(t, mock.Message(0))
	assert.Nil(t, mock.Message(-1))
	assert.Nil(t, mock.Body(3))
	_, ok := mock.Header(0, "x")
	assert.False(t, ok)
}

func TestMockEndpoint_SnapshotImmuneToLaterMutation(t *testing.T) {
	ctx := NewCamelContext()
	live := sendToMock(t, ctx, "mock:out", map[string]any{"k": "v"}, map[string]any{"h": "orig"})

	live.In.Body.(map[string]any)["k"] = "mutated"
	live.In.SetHeader("h", "mutated")
	live.In.SetBody("replaced")

	mock, err := ctx.GetMockEndpoint("mock:out")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"k": "v"}, mock.Body(0))
	h, _ := mock.Header(0, "h")
	assert.Equal(t, "orig", h)
}

func TestMockEndpoint_ConcurrentSends(t *testing.T) {
	ctx := NewCamelContext()
	endpoint, err := ctx.CreateEndpoint("mock:out")
	require.NoError(t, err)
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	const n = 50
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ex := NewExchange(context.Background())
			ex.In.SetBody(i)
			assert.NoError(t, producer.Send(ex))
		}()
	}
	wg.Wait()

	mock := endpoint.(*MockEndpoint)
	assert.Equal(t, n, mock.MessageCount())
	seen := make(map[any]bool)
	for i := range mock.MessageCount() {
		seen[mock.Body(i)] = true
	}
	assert.Len(t, seen, n)
}

func TestMockEndpoint_WaitSucceedsOnceExpectationMet(t *testing.T) {
	ctx := NewCamelContext()
	mock, err := ctx.GetMockEndpoint("mock:out")
	require.NoError(t, err)
	mock.ExpectedMessageCount(3)

	done := make(chan error, 1)
	go func() { done <- mock.Wait(5 * time.Second) }()

	for i := range 3 {
		sendToMock(t, ctx, "mock:out", fmt.Sprint(i), nil)
	}
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return")
	}
}

func TestMockEndpoint_WaitAcceptsMoreThanExpected(t *testing.T) {
	ctx := NewCamelContext()
	mock, err := ctx.GetMockEndpoint("mock:out")
	require.NoError(t, err)
	mock.ExpectedMessageCount(1)
	sendToMock(t, ctx, "mock:out", "a", nil)
	sendToMock(t, ctx, "mock:out", "b", nil)
	assert.NoError(t, mock.Wait(time.Second))
}

func TestMockEndpoint_WaitTimesOutWithCounts(t *testing.T) {
	ctx := NewCamelContext()
	mock, err := ctx.GetMockEndpoint("mock:out")
	require.NoError(t, err)
	mock.ExpectedMessageCount(2)
	sendToMock(t, ctx, "mock:out", "only one", nil)

	err = mock.Wait(50 * time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected at least 2")
	assert.Contains(t, err.Error(), "received 1")
}

func TestMockEndpoint_WaitWithoutExpectationReturnsImmediately(t *testing.T) {
	ctx := NewCamelContext()
	mock, err := ctx.GetMockEndpoint("mock:out")
	require.NoError(t, err)
	assert.NoError(t, mock.Wait(time.Millisecond))
}

func TestMockEndpoint_Reset(t *testing.T) {
	ctx := NewCamelContext()
	mock, err := ctx.GetMockEndpoint("mock:out")
	require.NoError(t, err)
	mock.ExpectedMessageCount(5)
	sendToMock(t, ctx, "mock:out", "a", nil)

	mock.Reset()
	assert.Equal(t, 0, mock.MessageCount())
	assert.NoError(t, mock.Wait(time.Millisecond))
}

func TestMockEndpoint_RouteEndToEnd(t *testing.T) {
	ctx := NewCamelContext()
	route, err := ctx.CreateRouteBuilder().
		From("direct:in").
		SetHeader("stage", "processed").
		To("mock:result").
		Build()
	require.NoError(t, err)
	require.NotNil(t, route)

	mock, err := ctx.GetMockEndpoint("mock:result")
	require.NoError(t, err)
	mock.ExpectedMessageCount(2)

	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })

	sendToMock(t, ctx, "direct:in", "hello", nil)
	sendToMock(t, ctx, "direct:in", "world", nil)

	require.NoError(t, mock.Wait(time.Second))
	assert.Equal(t, "hello", mock.Body(0))
	assert.Equal(t, "world", mock.Body(1))
	stage, _ := mock.Header(1, "stage")
	assert.Equal(t, "processed", stage)
}

func TestMockEndpoint_MulticastParallel(t *testing.T) {
	ctx := NewCamelContext()
	_, err := ctx.CreateRouteBuilder().
		From("direct:in").
		To("mock:a", "mock:b").
		Build()
	require.NoError(t, err)
	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })

	for range 10 {
		sendToMock(t, ctx, "direct:in", "x", nil)
	}
	a, err := ctx.GetMockEndpoint("mock:a")
	require.NoError(t, err)
	b, err := ctx.GetMockEndpoint("mock:b")
	require.NoError(t, err)
	a.ExpectedMessageCount(10)
	b.ExpectedMessageCount(10)
	assert.NoError(t, a.Wait(time.Second))
	assert.NoError(t, b.Wait(time.Second))
}
