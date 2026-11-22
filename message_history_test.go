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

func TestMessageHistory_Enabled(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())

	_, err := camelCtx.CreateRouteBuilder().
		From("direct:start").
		EnableMessageHistory().
		ProcessFunc(func(e *Exchange) error {
			// time.Sleep is intentional: ensures a non-zero execution time.
			time.Sleep(10 * time.Millisecond)
			return nil
		}).
		TransformSimple("Modified: ${body}").
		To("direct:end").
		Build()

	if err != nil {
		t.Fatalf("Failed to build route: %v", err)
	}

	_, _ = camelCtx.CreateRouteBuilder().
		From("direct:end").
		ProcessFunc(func(e *Exchange) error {
			// Inside the synchronous downstream route, the parent route's To (lazyProducer)
			// is still executing, so it has not completed and recorded its history entry yet.
			midHistory := GetMessageHistory(e)
			if len(midHistory) != 2 {
				t.Errorf("Expected 2 completed history entries during downstream execution, got %d: %v", len(midHistory), midHistory)
			}
			return nil
		}).
		Build()

	if err := camelCtx.Start(); err != nil {
		t.Fatalf("Failed to start context: %v", err)
	}
	defer camelCtx.Stop()

	exchange := NewExchange(context.Background())
	exchange.In.SetBody("Apple")

	endpoint, _ := camelCtx.CreateEndpoint("direct:start")
	producer, _ := endpoint.CreateProducer()

	if err := producer.Send(exchange); err != nil {
		t.Fatalf("Exchange failed: %v", err)
	}

	// Verify that history is captured completely AFTER the entire synchronous run completes
	history := GetMessageHistory(exchange)
	if len(history) == 0 {
		t.Fatal("Expected message history, but got none")
	}

	// The first route had 3 processors: ProcessFunc, Transformer (TransformSimple), and lazyProducer (To)
	if len(history) < 3 {
		t.Errorf("Expected at least 3 history entries, got %d: %v", len(history), history)
	}

	// Check fields on history entries
	foundProcessFunc := false
	foundTransformer := false
	foundLazyProducer := false

	for _, entry := range history {
		if entry.RouteID == "" {
			t.Error("Expected non-empty RouteID")
		}
		if entry.Timestamp.IsZero() {
			t.Error("Expected non-zero Timestamp")
		}
		if entry.Elapsed < 0 {
			t.Errorf("Expected positive elapsed duration, got %s", entry.Elapsed)
		}

		switch entry.NodeID {
		case "gocamel.ProcessorFunc":
			foundProcessFunc = true
			if entry.Elapsed < 10*time.Millisecond {
				t.Errorf("Expected ProcessFunc execution to take >=10ms, got %s", entry.Elapsed)
			}
		case "*gocamel.Transformer":
			foundTransformer = true
		case "*gocamel.lazyProducer":
			foundLazyProducer = true
		}
	}

	if !foundProcessFunc {
		t.Error("Expected to find ProcessorFunc in message history")
	}
	if !foundTransformer {
		t.Error("Expected to find Transformer in message history")
	}
	if !foundLazyProducer {
		t.Error("Expected to find lazyProducer in message history")
	}
}

func TestMessageHistory_Disabled(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())

	_, err := camelCtx.CreateRouteBuilder().
		From("direct:start").
		// EnableMessageHistory() is deliberately NOT called here
		ProcessFunc(func(e *Exchange) error {
			return nil
		}).
		To("direct:end").
		Build()

	if err != nil {
		t.Fatalf("Failed to build route: %v", err)
	}

	var history []MessageHistoryEntry

	_, _ = camelCtx.CreateRouteBuilder().
		From("direct:end").
		ProcessFunc(func(e *Exchange) error {
			history = GetMessageHistory(e)
			return nil
		}).
		Build()

	if err := camelCtx.Start(); err != nil {
		t.Fatalf("Failed to start context: %v", err)
	}
	defer camelCtx.Stop()

	exchange := NewExchange(context.Background())
	exchange.In.SetBody("Orange")

	endpoint, _ := camelCtx.CreateEndpoint("direct:start")
	producer, _ := endpoint.CreateProducer()

	if err := producer.Send(exchange); err != nil {
		t.Fatalf("Exchange failed: %v", err)
	}

	// Verify that history is NOT captured
	if len(history) != 0 {
		t.Errorf("Expected empty message history when disabled, but got %d entries: %v", len(history), history)
	}
}

func TestMessageHistory_ParallelMulticastNoRace(t *testing.T) {
	ctx := NewCamelContext()

	var (
		mu        sync.Mutex
		histories = make(map[string][]MessageHistoryEntry)
		tapDone   = make(chan struct{})
	)
	record := func(branch string) ProcessorFunc {
		return func(e *Exchange) error {
			h := GetMessageHistory(e)
			cp := make([]MessageHistoryEntry, len(h))
			copy(cp, h)
			mu.Lock()
			histories[branch] = cp
			mu.Unlock()
			return nil
		}
	}

	var capBeforeFanout int
	var lenBeforeFanout int
	_, err := ctx.CreateRouteBuilder().
		From("direct:hist-start").
		EnableMessageHistory().
		SetHeader("s1", 1).
		SetHeader("s2", 2).
		SetHeader("s3", 3).
		SetHeader("s4", 4).
		SetHeader("s5", 5).
		ProcessFunc(func(e *Exchange) error {
			h := GetMessageHistory(e)
			lenBeforeFanout = len(h)
			capBeforeFanout = cap(h)
			if capBeforeFanout <= lenBeforeFanout {
				return fmt.Errorf("expected cap > len before fan-out, got len=%d cap=%d", lenBeforeFanout, capBeforeFanout)
			}
			return nil
		}).
		WireTap("direct:hist-tap").
		Multicast().
		ParallelProcessing().
		To("direct:hist-b1", "direct:hist-b2").
		End().
		Build()
	require.NoError(t, err)

	_, err = ctx.CreateRouteBuilder().
		From("direct:hist-b1").
		SetID("route-b1").
		EnableMessageHistory().
		SetHeader("branch", "b1").
		Process(record("b1")).
		Build()
	require.NoError(t, err)

	_, err = ctx.CreateRouteBuilder().
		From("direct:hist-b2").
		SetID("route-b2").
		EnableMessageHistory().
		SetHeader("branch", "b2").
		Process(record("b2")).
		Build()
	require.NoError(t, err)

	_, err = ctx.CreateRouteBuilder().
		From("direct:hist-tap").
		SetID("route-tap").
		EnableMessageHistory().
		SetHeader("branch", "tap").
		Process(record("tap")).
		ProcessFunc(func(*Exchange) error {
			close(tapDone)
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })

	prod, err := ctx.GetProducer("direct:hist-start")
	require.NoError(t, err)
	ex := NewExchange(context.Background())
	require.NoError(t, prod.Send(ex))

	select {
	case <-tapDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for wiretap")
	}

	assert.Greater(t, capBeforeFanout, lenBeforeFanout, "spare capacity must exist before fan-out to exercise aliasing")

	mu.Lock()
	b1 := histories["b1"]
	b2 := histories["b2"]
	tap := histories["tap"]
	mu.Unlock()

	require.NotEmpty(t, b1)
	require.NotEmpty(t, b2)
	require.NotEmpty(t, tap)

	hasRoute := func(entries []MessageHistoryEntry, routeID string) bool {
		for _, entry := range entries {
			if entry.RouteID == routeID {
				return true
			}
		}
		return false
	}
	assert.True(t, hasRoute(b1, "route-b1"))
	assert.False(t, hasRoute(b1, "route-b2"))
	assert.False(t, hasRoute(b1, "route-tap"))

	assert.True(t, hasRoute(b2, "route-b2"))
	assert.False(t, hasRoute(b2, "route-b1"))
	assert.False(t, hasRoute(b2, "route-tap"))

	assert.True(t, hasRoute(tap, "route-tap"))
	assert.False(t, hasRoute(tap, "route-b1"))
	assert.False(t, hasRoute(tap, "route-b2"))

	// Also verify sequential Multicast (which uses shallow exchange.Copy) clips
	// slice capacity so later branches do not see or overwrite earlier branches.
	seqCtx := NewCamelContext()
	var seqMu sync.Mutex
	seqHistories := make(map[string][]MessageHistoryEntry)
	_, err = seqCtx.CreateRouteBuilder().
		From("direct:seq-hist-start").
		EnableMessageHistory().
		SetHeader("s1", 1).
		SetHeader("s2", 2).
		SetHeader("s3", 3).
		SetHeader("s4", 4).
		SetHeader("s5", 5).
		Multicast().
		To("direct:seq-b1", "direct:seq-b2").
		End().
		Build()
	require.NoError(t, err)

	for _, id := range []string{"seq-b1", "seq-b2"} {
		branchID := id
		_, err = seqCtx.CreateRouteBuilder().
			From("direct:"+branchID).
			SetID("route-"+branchID).
			EnableMessageHistory().
			SetHeader("branch", branchID).
			ProcessFunc(func(e *Exchange) error {
				h := GetMessageHistory(e)
				cp := make([]MessageHistoryEntry, len(h))
				copy(cp, h)
				seqMu.Lock()
				seqHistories[branchID] = cp
				seqMu.Unlock()
				return nil
			}).
			Build()
		require.NoError(t, err)
	}

	require.NoError(t, seqCtx.Start())
	t.Cleanup(func() { _ = seqCtx.Stop() })

	seqProd, err := seqCtx.GetProducer("direct:seq-hist-start")
	require.NoError(t, err)
	require.NoError(t, seqProd.Send(NewExchange(context.Background())))

	assert.True(t, hasRoute(seqHistories["seq-b1"], "route-seq-b1"))
	assert.False(t, hasRoute(seqHistories["seq-b1"], "route-seq-b2"))
	assert.True(t, hasRoute(seqHistories["seq-b2"], "route-seq-b2"))
	assert.False(t, hasRoute(seqHistories["seq-b2"], "route-seq-b1"))
}
