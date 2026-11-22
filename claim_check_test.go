package gocamel

import (
	"context"
	"regexp"
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

func TestClaimCheck_DistinguishesMissingVsStoredNilAndImplementsValidator(t *testing.T) {
	repo := NewMemoryClaimCheckRepository()

	// Push explicit nil body -> Get and Pop must succeed and restore nil.
	ex := NewExchange(context.Background())
	ex.In.SetBody(nil)

	push := NewClaimCheck(ClaimCheckPush, "nil-key", repo)
	require.NoError(t, push.Process(ex))
	assert.Equal(t, "nil-key", ex.In.GetBody())

	ex.In.SetBody("overwrite")
	get := NewClaimCheck(ClaimCheckGet, "nil-key", repo)
	require.NoError(t, get.Process(ex))
	assert.Nil(t, ex.In.GetBody())

	ex.In.SetBody("overwrite-again")
	pop := NewClaimCheck(ClaimCheckPop, "nil-key", repo)
	require.NoError(t, pop.Process(ex))
	assert.Nil(t, ex.In.GetBody())

	// Missing key must still return an error.
	err := NewClaimCheck(ClaimCheckGet, "missing-key", repo).Process(NewExchange(context.Background()))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	// Validator checks
	var typedNilRepo *MemoryClaimCheckRepository
	for _, cc := range []*ClaimCheck{
		nil,
		NewClaimCheck(ClaimCheckPush, "k", nil),
		NewClaimCheck(ClaimCheckPush, "k", typedNilRepo),
		NewClaimCheck(ClaimCheckOperation("InvalidOp"), "k", repo),
	} {
		v, ok := any(cc).(Validator)
		require.True(t, ok, "ClaimCheck must implement Validator")
		require.Error(t, v.Validate())
	}
}

func TestMemoryClaimCheckRepository_GeneratesUUIDKeys(t *testing.T) {
	repo := NewMemoryClaimCheckRepository()
	key1, err := repo.Add(context.Background(), "v1")
	require.NoError(t, err)
	key2, err := repo.Add(context.Background(), "v2")
	require.NoError(t, err)

	uuidRe := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	assert.Regexp(t, uuidRe, key1)
	assert.Regexp(t, uuidRe, key2)
	assert.NotEqual(t, key1, key2)
}

func TestMemoryClaimCheckRepository_BoundedCapacity(t *testing.T) {
	ctx := context.Background()

	defaultRepo := NewMemoryClaimCheckRepository()
	assert.Equal(t, DefaultMemoryClaimCheckRepositorySize, defaultRepo.maxSize)

	repo := NewMemoryClaimCheckRepositoryWithCapacity(2)
	require.NoError(t, repo.Set(ctx, "k1", "v1"))
	require.NoError(t, repo.Set(ctx, "k2", "v2"))
	// Touch k1 via Lookup -> k2 becomes LRU.
	v, ok, err := repo.Lookup(ctx, "k1")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "v1", v)

	require.NoError(t, repo.Set(ctx, "k3", "v3"))
	_, ok2, _ := repo.Lookup(ctx, "k2")
	_, ok1, _ := repo.Lookup(ctx, "k1")
	_, ok3, _ := repo.Lookup(ctx, "k3")
	assert.False(t, ok2, "least recently used key k2 must be evicted")
	assert.True(t, ok1)
	assert.True(t, ok3)

	repo.SetMaxSize(1)
	_, ok1, _ = repo.Lookup(ctx, "k1")
	_, ok3, _ = repo.Lookup(ctx, "k3")
	assert.False(t, ok1)
	assert.True(t, ok3)

	// Inserting at capacity 1 after SetMaxSize(1) must evict k3 and retain only k4.
	require.NoError(t, repo.Set(ctx, "k4", "v4"))
	_, ok3, _ = repo.Lookup(ctx, "k3")
	_, ok4, _ := repo.Lookup(ctx, "k4")
	assert.False(t, ok3, "k3 must be evicted when inserting k4 at capacity 1")
	assert.True(t, ok4)

	// Direct WithCapacity(1) must also cap at 1 entry across Add/Set.
	capOne := NewMemoryClaimCheckRepositoryWithCapacity(1)
	keyA, err := capOne.Add(ctx, "va")
	require.NoError(t, err)
	keyB, err := capOne.Add(ctx, "vb")
	require.NoError(t, err)
	_, okA, _ := capOne.Lookup(ctx, keyA)
	_, okB, _ := capOne.Lookup(ctx, keyB)
	assert.False(t, okA, "WithCapacity(1) must evict first entry when second is added")
	assert.True(t, okB)
}
