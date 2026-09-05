package gocamel

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReviewBodyPropagation(t *testing.T) {
	c := NewCamelContext()
	var body any
	r, err := c.CreateRouteBuilder().From("direct:review").SetBody("new").ProcessFunc(func(e *Exchange) error { body = e.GetBody(); return nil }).Build()
	require.NoError(t, err)
	e := NewExchange(context.Background())
	e.SetBody("old")
	require.NoError(t, r.Process(e))
	assert.Equal(t, "new", body)
}

func TestReviewHeaderRemoval(t *testing.T) {
	c := NewCamelContext()
	_, err := c.CreateRouteBuilder().From("direct:sink").Build()
	require.NoError(t, err)
	r, err := c.CreateRouteBuilder().From("direct:review").SetHeader("Authorization", "secret").RemoveHeader("Authorization").To("direct:sink").Build()
	require.NoError(t, err)
	require.NoError(t, c.Start())
	defer c.Stop()
	e := NewExchange(context.Background())
	require.NoError(t, r.Process(e))
	_, exists := e.GetHeader("Authorization")
	assert.False(t, exists)
}

func TestReviewSplitPartIsolation(t *testing.T) {
	shared := []byte{0}
	s := NewSplitter(func(*Exchange) (any, error) { return []any{shared, shared}, nil }).SetParallelProcessing(true)
	s.AddProcessor(ProcessorFunc(func(e *Exchange) error { e.GetBody().([]byte)[0] = 1; return nil }))
	require.NoError(t, s.Process(NewExchange(context.Background())))
	assert.Equal(t, byte(0), shared[0])
}

func TestReviewHistoryRace(t *testing.T) {
	r := NewRoute().EnableMessageHistory()
	r.AddProcessor(ProcessorFunc(func(*Exchange) error { return nil }))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; _ = r.Process(NewExchange(context.Background())) }()
	}
	close(start)
	wg.Wait()
}

type reviewPanicStrategy struct{}

func (reviewPanicStrategy) Aggregate(_, e *Exchange) *Exchange { panic("review aggregation panic") }
func TestReviewAggregationPanic(t *testing.T) {
	m := NewMulticast().SetParallelProcessing(true).SetAggregationStrategy(reviewPanicStrategy{})
	m.AddProcessor(ProcessorFunc(func(*Exchange) error { return nil }))
	assert.ErrorIs(t, ProcessSafely(m, NewExchange(context.Background())), ErrProcessorPanic)
}

func TestReviewResequencerBuilderInvalid(t *testing.T) {
	assert.NotPanics(t, func() {
		_, err := NewCamelContext().CreateRouteBuilder().From("direct:review").Resequence(123, 0).Timeout(time.Second).End().Build()
		assert.Error(t, err)
	})
}

func TestReviewNestedCompletion(t *testing.T) {
	repo := NewMemoryIdempotentRepository()
	s := NewSplitter(func(*Exchange) (any, error) { return []string{"one"}, nil })
	s.AddProcessor(NewIdempotentConsumer(func(*Exchange) (string, error) { return "key", nil }, repo))
	s.AddProcessor(ProcessorFunc(func(*Exchange) error { return errors.New("downstream failure") }))
	ex := NewExchange(context.Background())
	require.Error(t, CompleteExchange(ex, ProcessSafely(s, ex)))
	added, err := repo.Add(context.Background(), "key")
	require.NoError(t, err)
	assert.True(t, added, "failed branch must release idempotency key")
}

func TestReviewPollEnrichPreservesQueuedMessages(t *testing.T) {
	c := NewCamelContext()
	ep, err := c.CreateEndpoint("seda:review-poll")
	require.NoError(t, err)
	prod, err := ep.CreateProducer()
	require.NoError(t, err)
	for _, body := range []string{"first", "second", "third"} {
		e := NewExchange(context.Background())
		e.SetBody(body)
		require.NoError(t, prod.Send(e))
	}
	p := NewPollEnricher(c, "seda:review-poll", nil, time.Second)
	require.NoError(t, p.Process(NewExchange(context.Background())))
	assert.Equal(t, 2, len(ep.(*SedaEndpoint).queue), "a single poll must preserve the remaining messages")
	require.NoError(t, prod.Send(NewExchange(context.Background())), "polling should leave the queue usable")
}

func TestReviewNetCancellation(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	ep, err := NewNetComponent().CreateEndpoint("net:tcp://" + ln.Addr().String() + "?timeout=0&sync=false")
	require.NoError(t, err)
	p, err := ep.CreateProducer()
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ex := NewExchange(ctx)
	ex.SetBody(make([]byte, 16<<20))
	sent := make(chan error, 1)
	go func() { sent <- p.Send(ex) }()
	var conn net.Conn
	select {
	case conn = <-accepted:
	case <-time.After(time.Second):
		t.Fatal("no connection")
	}
	defer conn.Close()
	cancel()
	select {
	case <-sent:
	case <-time.After(300 * time.Millisecond):
		t.Error("Send remains blocked after cancellation and configured timeout")
		conn.Close()
		<-sent
	}
}
