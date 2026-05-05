package gocamel

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestWireTap_Basic verifies that the wiretap delivers a copy of the exchange
// to the specified endpoint while the main route continues normally.
func TestWireTap_Basic(t *testing.T) {
	ctx := context.Background()
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	var (
		tapReceived bool
		mainDone    bool
		mu          sync.Mutex
	)

	// Build the tap receiver route
	tapRoute, err := NewRouteBuilder(camel).
		From("direct:tap").
		SetID("tap-route").
		ProcessFunc(func(e *Exchange) error {
			mu.Lock()
			tapReceived = true
			mu.Unlock()
			return nil
		}).
		Build()
	assert.NoError(t, err)
	camel.AddRoute(tapRoute)

	// Build the main route with wiretap
	mainRoute, err := NewRouteBuilder(camel).
		From("direct:start").
		SetID("main-route").
		WireTap("direct:tap").
		ProcessFunc(func(e *Exchange) error {
			mu.Lock()
			mainDone = true
			mu.Unlock()
			return nil
		}).
		Build()
	assert.NoError(t, err)
	camel.AddRoute(mainRoute)

	err = camel.Start()
	assert.NoError(t, err)
	defer camel.Stop()

	exchange := NewExchange(ctx)
	exchange.GetIn().SetBody("hello-world")

	err = mainRoute.Process(exchange)
	assert.NoError(t, err)
	assert.True(t, mainDone, "main route should have completed")

	// Wait a bit for the async tap to arrive
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	assert.True(t, tapReceived, "tap should have received the exchange")
	mu.Unlock()
}

// TestWireTap_DeepCopy verifies that the tapped exchange is a deep copy:
// modifications in the tap do not affect the main route and vice-versa.
func TestWireTap_DeepCopy(t *testing.T) {
	ctx := context.Background()
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	var (
		mainAfterBody any
		tapModified  any
		mu           sync.Mutex
	)

	// Tap route: receives the copy and modifies it
	tapRoute, err := NewRouteBuilder(camel).
		From("direct:tap").
		SetID("tap-route").
		ProcessFunc(func(e *Exchange) error {
			// Verify tap received the original body
			body := e.GetIn().GetBody()
			assert.Equal(t, "original", body)
			// Modify the tap copy's body and headers
			e.GetIn().SetBody("tap-modified")
			e.GetIn().SetHeader("tap-header", "tap-value")
			mu.Lock()
			tapModified = e.GetIn().GetBody()
			mu.Unlock()
			return nil
		}).
		Build()
	assert.NoError(t, err)
	camel.AddRoute(tapRoute)

	// Main route continues after the wiretap
	mainRoute, err := NewRouteBuilder(camel).
		From("direct:start").
		SetID("main-route").
		WireTap("direct:tap").
		ProcessFunc(func(e *Exchange) error {
			// Modify the main exchange body
			e.GetIn().SetBody("main-modified")
			mu.Lock()
			mainAfterBody = e.GetIn().GetBody()
			mu.Unlock()
			return nil
		}).
		Build()
	assert.NoError(t, err)
	camel.AddRoute(mainRoute)

	err = camel.Start()
	assert.NoError(t, err)
	defer camel.Stop()

	exchange := NewExchange(ctx)
	exchange.GetIn().SetBody("original")

	err = mainRoute.Process(exchange)
	assert.NoError(t, err)

	// Wait for tap to arrive
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	// Main route body after its ProcessFunc should be "main-modified"
	assert.Equal(t, "main-modified", mainAfterBody)
	// Tap modified its copy to "tap-modified" — that should not affect main
	assert.Equal(t, "tap-modified", tapModified)
	mu.Unlock()

	// Main route's body after processing should be "main-modified"
	assert.Equal(t, "main-modified", exchange.GetIn().GetBody())
}

// TestWireTap_DataIntegrity verifies that the tap receives the exact data
// sent by the main route (body, headers, properties).
func TestWireTap_DataIntegrity(t *testing.T) {
	ctx := context.Background()
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	var (
		tapBody      any
		tapHeader    any
		tapProperty  any
		tapHasHeader bool
		mu           sync.Mutex
	)

	tapRoute, err := NewRouteBuilder(camel).
		From("direct:audit").
		SetID("audit-route").
		ProcessFunc(func(e *Exchange) error {
			mu.Lock()
			tapBody = e.GetIn().GetBody()
			tapHeader, tapHasHeader = e.GetIn().GetHeader("X-Custom")
			tapProperty, _ = e.GetProperty("trace-id")
			mu.Unlock()
			return nil
		}).
		Build()
	assert.NoError(t, err)
	camel.AddRoute(tapRoute)

	mainRoute, err := NewRouteBuilder(camel).
		From("direct:start").
		SetID("main-route").
		ProcessFunc(func(e *Exchange) error {
			e.GetIn().SetHeader("X-Custom", "my-value")
			e.SetProperty("trace-id", "abc-123-trace")
			return nil
		}).
		WireTap("direct:audit").
		Build()
	assert.NoError(t, err)
	camel.AddRoute(mainRoute)

	err = camel.Start()
	assert.NoError(t, err)
	defer camel.Stop()

	exchange := NewExchange(ctx)
	exchange.GetIn().SetBody(map[string]string{"key": "value"})

	err = mainRoute.Process(exchange)
	assert.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	assert.Equal(t, map[string]string{"key": "value"}, tapBody)
	assert.True(t, tapHasHeader)
	assert.Equal(t, "my-value", tapHeader)
	assert.Equal(t, "abc-123-trace", tapProperty)
	mu.Unlock()
}

// TestWireTap_NonBlocking verifies that the main route does NOT wait
// for the tap to complete. We use a tap endpoint that blocks, and confirm
// the main route finishes first.
func TestWireTap_NonBlocking(t *testing.T) {
	ctx := context.Background()
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	var (
		mainFinished bool
		tapFinished  bool
		mu           sync.Mutex
		tapStarted   = make(chan struct{})
	)

	tapRoute, err := NewRouteBuilder(camel).
		From("direct:slow-tap").
		SetID("slow-tap-route").
		ProcessFunc(func(e *Exchange) error {
			close(tapStarted)
			time.Sleep(300 * time.Millisecond)
			mu.Lock()
			tapFinished = true
			mu.Unlock()
			return nil
		}).
		Build()
	assert.NoError(t, err)
	camel.AddRoute(tapRoute)

	mainRoute, err := NewRouteBuilder(camel).
		From("direct:start").
		SetID("main-route").
		WireTap("direct:slow-tap").
		ProcessFunc(func(e *Exchange) error {
			mu.Lock()
			mainFinished = true
			mu.Unlock()
			return nil
		}).
		Build()
	assert.NoError(t, err)
	camel.AddRoute(mainRoute)

	err = camel.Start()
	assert.NoError(t, err)
	defer camel.Stop()

	start := time.Now()
	exchange := NewExchange(ctx)

	err = mainRoute.Process(exchange)
	elapsed := time.Since(start)

	assert.NoError(t, err)

	// Main route should complete quickly (sub-200ms, independent of the 300ms tap)
	assert.True(t, elapsed < 200*time.Millisecond,
		"main route took %v, should finish before tap completes", elapsed)

	// Wait for the tap goroutine to actually start
	<-tapStarted

	mu.Lock()
	assert.True(t, mainFinished, "main route should be finished already")
	assert.False(t, tapFinished, "tap should still be sleeping")
	mu.Unlock()

	// Wait for tap to finish (sleeps 300ms, so 400ms should be enough)
	time.Sleep(400 * time.Millisecond)
	mu.Lock()
	assert.True(t, tapFinished, "tap should eventually finish")
	mu.Unlock()
}

// TestWireTap_ErrorIsolation verifies that errors in the tap do not
// affect the main route.
func TestWireTap_ErrorIsolation(t *testing.T) {
	ctx := context.Background()
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	var mainSucceeded bool

	// Tap route that always fails
	tapRoute, err := NewRouteBuilder(camel).
		From("direct:failing-tap").
		SetID("failing-tap-route").
		ProcessFunc(func(e *Exchange) error {
			return assert.AnError
		}).
		Build()
	assert.NoError(t, err)
	camel.AddRoute(tapRoute)

	mainRoute, err := NewRouteBuilder(camel).
		From("direct:start").
		SetID("main-route").
		WireTap("direct:failing-tap").
		ProcessFunc(func(e *Exchange) error {
			mainSucceeded = true
			return nil
		}).
		Build()
	assert.NoError(t, err)
	camel.AddRoute(mainRoute)

	err = camel.Start()
	assert.NoError(t, err)
	defer camel.Stop()

	exchange := NewExchange(ctx)
	exchange.GetIn().SetBody("test")

	err = mainRoute.Process(exchange)
	assert.NoError(t, err, "main route should not fail even if tap fails")
	assert.True(t, mainSucceeded, "main route processor should have executed")

	time.Sleep(50 * time.Millisecond)
}

// TestWireTap_Multiple verifies that multiple wiretaps can be used
// in the same route and all deliver correctly.
func TestWireTap_Multiple(t *testing.T) {
	ctx := context.Background()
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	var (
		tapACalled bool
		tapBCalled bool
		mu         sync.Mutex
	)

	tapRouteA, _ := NewRouteBuilder(camel).
		From("direct:tap-a").SetID("tap-a").
		ProcessFunc(func(e *Exchange) error {
			mu.Lock()
			tapACalled = true
			mu.Unlock()
			return nil
		}).Build()
	camel.AddRoute(tapRouteA)

	tapRouteB, _ := NewRouteBuilder(camel).
		From("direct:tap-b").SetID("tap-b").
		ProcessFunc(func(e *Exchange) error {
			mu.Lock()
			tapBCalled = true
			mu.Unlock()
			return nil
		}).Build()
	camel.AddRoute(tapRouteB)

	mainRoute, err := NewRouteBuilder(camel).
		From("direct:start").
		SetID("main-route").
		WireTap("direct:tap-a").
		WireTap("direct:tap-b").
		Build()
	assert.NoError(t, err)
	camel.AddRoute(mainRoute)

	err = camel.Start()
	assert.NoError(t, err)
	defer camel.Stop()

	err = mainRoute.Process(NewExchange(ctx))
	assert.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	assert.True(t, tapACalled, "tap A should have been called")
	assert.True(t, tapBCalled, "tap B should have been called")
	mu.Unlock()
}

// TestWireTap_ChainedInSplit verifies that WireTap works inside a Split EIP block.
func TestWireTap_ChainedInSplit(t *testing.T) {
	ctx := context.Background()
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	var (
		tapCount int
		mu       sync.Mutex
	)

	tapRoute, _ := NewRouteBuilder(camel).
		From("direct:split-tap").SetID("split-tap").
		ProcessFunc(func(e *Exchange) error {
			mu.Lock()
			tapCount++
			mu.Unlock()
			return nil
		}).Build()
	camel.AddRoute(tapRoute)

	mainRoute, err := NewRouteBuilder(camel).
		From("direct:start").
		SetID("main-route").
		Split(func(e *Exchange) (any, error) {
			return []string{"a", "b", "c"}, nil
		}).
			WireTap("direct:split-tap").
		End().
		Build()
	assert.NoError(t, err)
	camel.AddRoute(mainRoute)

	err = camel.Start()
	assert.NoError(t, err)
	defer camel.Stop()

	err = mainRoute.Process(NewExchange(ctx))
	assert.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	assert.Equalf(t, 3, tapCount, "expected 3 taps for 3 split parts, got %d", tapCount)
	mu.Unlock()
}
