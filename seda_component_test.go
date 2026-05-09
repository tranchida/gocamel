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

func TestSedaComponent_Basic(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("seda", NewSedaComponent())
	ctx.AddComponent("direct", NewDirectComponent())

	var mu sync.Mutex
	var received []string
	
	_, err := ctx.CreateRouteBuilder().
		From("seda:async?concurrentConsumers=2").
		ProcessFunc(func(e *Exchange) error {
			body, _ := e.GetBodyAsString()
			mu.Lock()
			received = append(received, body)
			mu.Unlock()
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	defer ctx.Stop()

	p, err := ctx.CreateEndpoint("seda:async")
	require.NoError(t, err)
	
	prod, err := p.CreateProducer()
	require.NoError(t, err)
	
	err = prod.Start(context.Background())
	require.NoError(t, err)

	for i := 0; i < 10; i++ {
		ex := NewExchange(context.Background())
		ex.In.SetBody(fmt.Sprintf("msg-%d", i))
		err := prod.Send(ex)
		require.NoError(t, err)
	}
	
	// Wait for processing as it's async
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(received) == 10
	}, 2*time.Second, 100*time.Millisecond)
	
	mu.Lock()
	assert.Len(t, received, 10)
	mu.Unlock()
	
	err = prod.Stop()
	require.NoError(t, err)
}

func TestSedaComponent_QueueFull(t *testing.T) {
	ctx := NewCamelContext()
	seda := NewSedaComponent()
	ctx.AddComponent("seda", seda)

	// Small queue size, no consumer to drain it
	p, err := ctx.CreateEndpoint("seda:full?size=2")
	require.NoError(t, err)
	
	prod, err := p.CreateProducer()
	require.NoError(t, err)

	// Filling the queue
	ex1 := NewExchange(context.Background())
	require.NoError(t, prod.Send(ex1))
	
	ex2 := NewExchange(context.Background())
	require.NoError(t, prod.Send(ex2))

	// Third one should block, test with timeout context
	ctxTimeout, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	
	ex3 := NewExchange(ctxTimeout)
	err = prod.Send(ex3)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
}
