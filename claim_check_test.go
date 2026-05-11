package gocamel

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaimCheck_Memory(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())
	repo := NewMemoryClaimCheckRepository()

	var receivedBody any

	_, err := ctx.CreateRouteBuilder().
		From("direct:start").
		ClaimCheck(ClaimCheckPush, "myKey", repo).
		ProcessFunc(func(e *Exchange) error {
			// Body should be the key now
			assert.Equal(t, "myKey", e.GetIn().GetBody())
			return nil
		}).
		SetBody("modified body during transit").
		ClaimCheck(ClaimCheckPop, "myKey", repo).
		ProcessFunc(func(e *Exchange) error {
			receivedBody = e.GetIn().GetBody()
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	defer ctx.Stop()

	ex := NewExchange(context.Background())
	ex.In.SetBody("large original body")

	p, _ := ctx.CreateEndpoint("direct:start")
	prod, _ := p.CreateProducer()
	prod.Start(context.Background())
	prod.Send(ex)
	prod.Stop()

	assert.Equal(t, "large original body", receivedBody)

	// Verify it was removed from repo (Pop)
	data, _ := repo.Get(context.Background(), "myKey")
	assert.Nil(t, data)
}

func TestClaimCheck_AutoKey(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())
	repo := NewMemoryClaimCheckRepository()

	var receivedBody any

	_, err := ctx.CreateRouteBuilder().
		From("direct:start").
		ClaimCheck(ClaimCheckPush, "", repo). // Auto-generate key
		SetBody("trash").
		ClaimCheck(ClaimCheckPop, "", repo). // Use key from header
		ProcessFunc(func(e *Exchange) error {
			receivedBody = e.GetIn().GetBody()
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	defer ctx.Stop()

	ex := NewExchange(context.Background())
	ex.In.SetBody("auto-key-body")

	p, _ := ctx.CreateEndpoint("direct:start")
	prod, _ := p.CreateProducer()
	prod.Start(context.Background())
	prod.Send(ex)
	prod.Stop()

	assert.Equal(t, "auto-key-body", receivedBody)
}
