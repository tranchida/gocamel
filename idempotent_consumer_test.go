package gocamel

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIdempotentConsumer_Memory(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	var received []string
	repo := NewMemoryIdempotentRepository()

	_, err := ctx.CreateRouteBuilder().
		From("direct:start").
		IdempotentConsumer("${header.MessageId}", repo).
		ProcessFunc(func(e *Exchange) error {
			body, _ := e.GetBodyAsString()
			received = append(received, body)
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	defer ctx.Stop()

	send := func(body, messageId string) {
		ex := NewExchange(context.Background())
		ex.In.SetBody(body)
		ex.In.SetHeader("MessageId", messageId)
		p, _ := ctx.CreateEndpoint("direct:start")
		prod, _ := p.CreateProducer()
		prod.Start(context.Background())
		prod.Send(ex)
		prod.Stop()
	}

	send("msg1", "1")
	send("msg2", "2")
	send("msg1-dup", "1") // Should be dropped
	send("msg3", "3")
	send("msg2-dup", "2") // Should be dropped

	assert.Equal(t, []string{"msg1", "msg2", "msg3"}, received)
}

func TestIdempotentConsumer_Func(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	var received []string
	repo := NewMemoryIdempotentRepository()

	_, err := ctx.CreateRouteBuilder().
		From("direct:start").
		IdempotentConsumer(func(e *Exchange) (string, error) {
			return e.GetIn().GetBody().(string), nil
		}, repo).
		ProcessFunc(func(e *Exchange) error {
			body, _ := e.GetBodyAsString()
			received = append(received, body)
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	defer ctx.Stop()

	send := func(body string) {
		ex := NewExchange(context.Background())
		ex.In.SetBody(body)
		p, _ := ctx.CreateEndpoint("direct:start")
		prod, _ := p.CreateProducer()
		prod.Start(context.Background())
		prod.Send(ex)
		prod.Stop()
	}

	send("a")
	send("b")
	send("a") // Duplicate body
	send("c")

	assert.Equal(t, []string{"a", "b", "c"}, received)
}

func TestIdempotentConsumer_RouteLevelRedeliveryDoesNotDropMessage(t *testing.T) {
	errTransient := errors.New("transient downstream failure")

	t.Run("intra-exchange redelivery succeeds and keeps key stored", func(t *testing.T) {
		ctx := NewCamelContext()
		repo := NewMemoryIdempotentRepository()

		var attempts atomic.Int32
		var delivered []string

		_, err := ctx.CreateRouteBuilder().
			From("direct:sub").
			IdempotentConsumer("${header.MessageId}", repo).
			ProcessFunc(func(e *Exchange) error {
				if attempts.Add(1) == 1 {
					return errTransient
				}
				body, _ := e.GetBodyAsString()
				delivered = append(delivered, body)
				return nil
			}).
			Build()
		require.NoError(t, err)

		_, err = ctx.CreateRouteBuilder().
			OnException(errTransient).
			MaximumRedeliveries(1).
			End().
			From("direct:main").
			To("direct:sub").
			Build()
		require.NoError(t, err)

		require.NoError(t, ctx.Start())
		t.Cleanup(func() { _ = ctx.Stop() })

		prod, err := ctx.GetProducer("direct:main")
		require.NoError(t, err)

		ex := NewExchange(context.Background())
		ex.In.SetBody("order-1")
		ex.In.SetHeader("MessageId", "msg-1")
		err = CompleteExchange(ex, prod.Send(ex))
		require.NoError(t, err)

		assert.Equal(t, int32(2), attempts.Load())
		assert.Equal(t, []string{"order-1"}, delivered)

		stored, err := repo.Contains(context.Background(), "msg-1")
		require.NoError(t, err)
		assert.True(t, stored, "key must remain stored after redelivery succeeds")

		// Subsequent distinct exchange with same MessageId must still be rejected as duplicate.
		exDup := NewExchange(context.Background())
		exDup.In.SetBody("order-1-dup")
		exDup.In.SetHeader("MessageId", "msg-1")
		require.NoError(t, CompleteExchange(exDup, prod.Send(exDup)))
		assert.Equal(t, []string{"order-1"}, delivered)
	})

	t.Run("exhausted redeliveries still roll back key", func(t *testing.T) {
		ctx := NewCamelContext()
		repo := NewMemoryIdempotentRepository()

		var attempts atomic.Int32
		_, err := ctx.CreateRouteBuilder().
			From("direct:sub-fail").
			IdempotentConsumer("${header.MessageId}", repo).
			ProcessFunc(func(*Exchange) error {
				attempts.Add(1)
				return errTransient
			}).
			Build()
		require.NoError(t, err)

		_, err = ctx.CreateRouteBuilder().
			OnException(errTransient).
			MaximumRedeliveries(1).
			End().
			From("direct:main-fail").
			To("direct:sub-fail").
			Build()
		require.NoError(t, err)

		require.NoError(t, ctx.Start())
		t.Cleanup(func() { _ = ctx.Stop() })

		prod, err := ctx.GetProducer("direct:main-fail")
		require.NoError(t, err)

		ex := NewExchange(context.Background())
		ex.In.SetHeader("MessageId", "msg-fail")
		err = CompleteExchange(ex, prod.Send(ex))
		require.ErrorIs(t, err, errTransient)
		assert.Equal(t, int32(2), attempts.Load())

		stored, err := repo.Contains(context.Background(), "msg-fail")
		require.NoError(t, err)
		assert.False(t, stored, "key must be rolled back when all redeliveries fail")
	})
}

func TestIdempotentConsumer_ImplementsValidator(t *testing.T) {
	var typedNilRepo *MemoryIdempotentRepository
	cases := []struct {
		name string
		proc *IdempotentConsumer
	}{
		{"nil processor", nil},
		{"nil expression", NewIdempotentConsumer(nil, NewMemoryIdempotentRepository())},
		{"nil repo", NewIdempotentConsumer(func(*Exchange) (string, error) { return "k", nil }, nil)},
		{"typed-nil repo", NewIdempotentConsumer(func(*Exchange) (string, error) { return "k", nil }, typedNilRepo)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, ok := any(tc.proc).(Validator)
			require.True(t, ok, "IdempotentConsumer must implement Validator")
			require.Error(t, v.Validate())
		})
	}
}

func TestMemoryIdempotentRepository_BoundedCapacity(t *testing.T) {
	ctx := context.Background()

	defaultRepo := NewMemoryIdempotentRepository()
	assert.Equal(t, DefaultMemoryIdempotentRepositorySize, defaultRepo.maxSize)

	repo := NewMemoryIdempotentRepositoryWithCapacity(2)
	added, err := repo.Add(ctx, "k1")
	require.NoError(t, err)
	assert.True(t, added)
	added, err = repo.Add(ctx, "k2")
	require.NoError(t, err)
	assert.True(t, added)

	// Refresh k1 via duplicate Add, then add k3 -> k2 (oldest) is evicted.
	added, err = repo.Add(ctx, "k1")
	require.NoError(t, err)
	assert.False(t, added)
	added, err = repo.Add(ctx, "k3")
	require.NoError(t, err)
	assert.True(t, added)

	hasK1, _ := repo.Contains(ctx, "k1")
	hasK2, _ := repo.Contains(ctx, "k2")
	hasK3, _ := repo.Contains(ctx, "k3")
	assert.True(t, hasK1)
	assert.False(t, hasK2)
	assert.True(t, hasK3)

	// Shrinking via SetMaxSize(1) evicts k1, keeping newest k3.
	repo.SetMaxSize(1)
	hasK1, _ = repo.Contains(ctx, "k1")
	hasK3, _ = repo.Contains(ctx, "k3")
	assert.False(t, hasK1)
	assert.True(t, hasK3)
}
