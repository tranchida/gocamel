package gocamel

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func reseqInt64(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	}
	return 0
}

func reseqSend(ctx *CamelContext, seq int64) {
	ex := NewExchange(context.Background())
	ex.In.SetHeader("seq", seq)
	p, _ := ctx.CreateEndpoint("direct:start")
	prod, _ := p.CreateProducer()
	prod.Start(context.Background())
	prod.Send(ex)
	prod.Stop()
}

func TestResequencer_Basic(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	var mu sync.Mutex
	var received []int64

	_, err := ctx.CreateRouteBuilder().
		From("direct:start").
		Resequence("${header.seq}", 1).
		Timeout(100 * time.Millisecond).
		ProcessFunc(func(e *Exchange) error {
			seq, _ := e.GetIn().GetHeader("seq")
			mu.Lock()
			received = append(received, reseqInt64(seq))
			mu.Unlock()
			return nil
		}).
		End().
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	defer ctx.Stop()

	// Send out of order: 2, 1, 4, 3
	reseqSend(ctx, 2)
	reseqSend(ctx, 1)
	reseqSend(ctx, 4)
	reseqSend(ctx, 3)

	// Wait for processing
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(received) == 4
	}, 1*time.Second, 50*time.Millisecond)

	assert.Equal(t, []int64{1, 2, 3, 4}, received)
}

func TestResequencer_Timeout(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	var mu sync.Mutex
	var received []int64

	_, err := ctx.CreateRouteBuilder().
		From("direct:start").
		Resequence("${header.seq}", 1).
		Timeout(50 * time.Millisecond).
		ProcessFunc(func(e *Exchange) error {
			seq, _ := e.GetIn().GetHeader("seq")
			mu.Lock()
			received = append(received, reseqInt64(seq))
			mu.Unlock()
			return nil
		}).
		End().
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	defer ctx.Stop()

	// Send: 1, 3 (2 is missing)
	reseqSend(ctx, 1)
	reseqSend(ctx, 3)

	// 1 should be processed immediately
	// 3 should wait for timeout of 2, then be processed
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(received) == 2
	}, 1*time.Second, 50*time.Millisecond)

	assert.Equal(t, []int64{1, 3}, received)
}
