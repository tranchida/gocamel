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

func TestResequencer_BufferCapacity(t *testing.T) {
	r := NewResequencer(func(e *Exchange) (int64, error) {
		v, _ := e.GetHeaderAsInt("seq")
		return int64(v), nil
	}, 1, time.Second)
	r.SetCapacity(2)
	r.SetProcessor(ProcessorFunc(func(*Exchange) error { return nil }))

	send := func(seq int) error {
		ex := NewExchange(context.Background())
		ex.SetHeader("seq", seq)
		return r.Process(ex)
	}

	require.ErrorIs(t, send(2), ErrStopRouting)
	require.ErrorIs(t, send(3), ErrStopRouting)
	err := send(4)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "buffer full")
}

func TestResequencer_RaceAndFullBufferAcceptsExpectedNextSeqAndRestart(t *testing.T) {
	t.Run("concurrent outdated and valid messages under race", func(t *testing.T) {
		r := NewResequencer(func(e *Exchange) (int64, error) {
			v, _ := e.GetHeaderAsInt("seq")
			return int64(v), nil
		}, 10, 50*time.Millisecond)
		var mu sync.Mutex
		var delivered []int64
		r.SetProcessor(ProcessorFunc(func(e *Exchange) error {
			v, _ := e.GetHeaderAsInt("seq")
			mu.Lock()
			delivered = append(delivered, int64(v))
			mu.Unlock()
			return nil
		}))
		t.Cleanup(func() { _ = r.Stop() })

		var wg sync.WaitGroup
		for g := 0; g < 4; g++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for i := 0; i < 30; i++ {
					ex := NewExchange(context.Background())
					ex.SetHeader("seq", (i%12)+id) // includes outdated <10 and valid >=10
					_ = r.Process(ex)
				}
			}(g)
		}
		wg.Wait()
	})

	t.Run("full buffer accepts expected nextSeq", func(t *testing.T) {
		r := NewResequencer(func(e *Exchange) (int64, error) {
			v, _ := e.GetHeaderAsInt("seq")
			return int64(v), nil
		}, 1, time.Second)
		r.SetCapacity(2)
		var mu sync.Mutex
		var delivered []int
		r.SetProcessor(ProcessorFunc(func(e *Exchange) error {
			v, _ := e.GetHeaderAsInt("seq")
			mu.Lock()
			delivered = append(delivered, v)
			mu.Unlock()
			return nil
		}))
		t.Cleanup(func() { _ = r.Stop() })

		send := func(seq int) error {
			ex := NewExchange(context.Background())
			ex.SetHeader("seq", seq)
			return r.Process(ex)
		}

		require.ErrorIs(t, send(2), ErrStopRouting)
		require.ErrorIs(t, send(3), ErrStopRouting)
		// Buffer is full (2/2), but seq=1 IS nextSeq so accepting it drains [1,2,3].
		require.ErrorIs(t, send(1), ErrStopRouting)

		mu.Lock()
		assert.Equal(t, []int{1, 2, 3}, delivered)
		mu.Unlock()
	})

	t.Run("route restart delivers buffered entries under new live context", func(t *testing.T) {
		c := NewCamelContext()
		delivered := make(chan context.Context, 2)
		route, err := c.CreateRouteBuilder().
			From("direct:reseq-restart").
			Resequence("${header.seq}", 1).
			Timeout(40 * time.Millisecond).
			ProcessFunc(func(e *Exchange) error {
				delivered <- e.Context
				return nil
			}).
			End().
			Build()
		require.NoError(t, err)

		ctx1, cancel1 := context.WithCancel(context.Background())
		require.NoError(t, route.Start(ctx1))

		ex2 := NewExchange(context.Background())
		ex2.In.SetHeader("seq", 2)
		require.ErrorIs(t, route.Process(ex2), ErrStopRouting)

		require.NoError(t, route.Stop())
		cancel1()

		ctx2, cancel2 := context.WithCancel(context.Background())
		defer cancel2()
		require.NoError(t, route.Start(ctx2))
		t.Cleanup(func() { _ = route.Stop() })

		select {
		case gotCtx := <-delivered:
			require.NotNil(t, gotCtx)
			assert.NoError(t, gotCtx.Err(), "delivered context after restart must be the new live context")
		case <-time.After(time.Second):
			t.Fatal("buffered entry was not delivered after route restart")
		}
	})
}
