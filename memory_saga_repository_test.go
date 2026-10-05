package gocamel

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runSagaRepositoryContract exercises the SagaRepository contract. It is shared
// by the memory and SQL implementations so both behave identically.
func runSagaRepositoryContract(t *testing.T, newRepo func(t *testing.T) SagaRepository) {
	t.Run("LoadUnknown", func(t *testing.T) {
		repo := newRepo(t)
		_, err := repo.Load(context.Background(), "missing")
		assert.ErrorIs(t, err, ErrSagaNotFound)
	})

	t.Run("SaveLoadRoundTrip", func(t *testing.T) {
		repo := newRepo(t)
		ctx := context.Background()
		now := time.Now().UTC().Truncate(time.Millisecond)
		state := SagaState{
			SagaID: "saga-1",
			Status: SagaRunning,
			CompletedSteps: []CompletedStep{
				{Index: 0, CompensationURI: "direct:cancel-hotel"},
				{Index: 1, InProcessCompensation: true},
			},
			Message:   SagaMessage{Body: "order-42", Headers: map[string]any{"OrderId": "42"}},
			UpdatedAt: now,
		}
		require.NoError(t, repo.Save(ctx, "saga-1", state))

		loaded, err := repo.Load(ctx, "saga-1")
		require.NoError(t, err)
		assert.Equal(t, "saga-1", loaded.SagaID)
		assert.Equal(t, SagaRunning, loaded.Status)
		assert.Equal(t, state.CompletedSteps, loaded.CompletedSteps)
		assert.Equal(t, "order-42", loaded.Message.Body)
		assert.Equal(t, "42", loaded.Message.Headers["OrderId"])
		assert.True(t, now.Equal(loaded.UpdatedAt), "updatedAt %v != %v", loaded.UpdatedAt, now)
	})

	t.Run("SaveOverwrites", func(t *testing.T) {
		repo := newRepo(t)
		ctx := context.Background()
		require.NoError(t, repo.Save(ctx, "saga-1", SagaState{SagaID: "saga-1", Status: SagaRunning}))
		require.NoError(t, repo.Save(ctx, "saga-1", SagaState{
			SagaID:         "saga-1",
			Status:         SagaCompensating,
			CompletedSteps: []CompletedStep{{Index: 3, CompensationURI: "direct:c"}},
		}))
		loaded, err := repo.Load(ctx, "saga-1")
		require.NoError(t, err)
		assert.Equal(t, SagaCompensating, loaded.Status)
		assert.Equal(t, []CompletedStep{{Index: 3, CompensationURI: "direct:c"}}, loaded.CompletedSteps)
	})

	t.Run("SaveIsIsolatedFromCaller", func(t *testing.T) {
		repo := newRepo(t)
		ctx := context.Background()
		state := SagaState{
			SagaID:         "saga-1",
			Status:         SagaRunning,
			CompletedSteps: []CompletedStep{{Index: 0, CompensationURI: "direct:a"}},
			Message:        SagaMessage{Headers: map[string]any{"k": "v"}},
		}
		require.NoError(t, repo.Save(ctx, "saga-1", state))
		state.CompletedSteps[0].CompensationURI = "mutated"
		state.Message.Headers["k"] = "mutated"

		loaded, err := repo.Load(ctx, "saga-1")
		require.NoError(t, err)
		assert.Equal(t, "direct:a", loaded.CompletedSteps[0].CompensationURI)
		assert.Equal(t, "v", loaded.Message.Headers["k"])
	})

	t.Run("ListIncompleteExcludesTerminalStatuses", func(t *testing.T) {
		repo := newRepo(t)
		ctx := context.Background()
		base := time.Now().UTC()
		states := map[string]SagaStatus{
			"running":      SagaRunning,
			"compensating": SagaCompensating,
			"completed":    SagaCompleted,
			"compensated":  SagaCompensated,
		}
		i := 0
		for id, status := range states {
			require.NoError(t, repo.Save(ctx, id, SagaState{SagaID: id, Status: status, UpdatedAt: base.Add(time.Duration(i) * time.Second)}))
			i++
		}
		ids, err := repo.ListIncomplete(ctx)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"running", "compensating"}, ids)
	})

	t.Run("ListIncompleteIsBounded", func(t *testing.T) {
		repo := newRepo(t)
		ctx := context.Background()
		base := time.Now().UTC()
		for i := 0; i < DefaultSagaListLimit+5; i++ {
			id := fmt.Sprintf("saga-%04d", i)
			require.NoError(t, repo.Save(ctx, id, SagaState{SagaID: id, Status: SagaRunning, UpdatedAt: base.Add(time.Duration(i) * time.Millisecond)}))
		}
		ids, err := repo.ListIncomplete(ctx)
		require.NoError(t, err)
		require.Len(t, ids, DefaultSagaListLimit)
		// Oldest first, so the longest-stuck sagas are recovered first.
		assert.Equal(t, "saga-0000", ids[0])
	})

	t.Run("Remove", func(t *testing.T) {
		repo := newRepo(t)
		ctx := context.Background()
		require.NoError(t, repo.Save(ctx, "saga-1", SagaState{SagaID: "saga-1", Status: SagaRunning}))
		require.NoError(t, repo.Remove(ctx, "saga-1"))
		_, err := repo.Load(ctx, "saga-1")
		assert.ErrorIs(t, err, ErrSagaNotFound)
		ids, err := repo.ListIncomplete(ctx)
		require.NoError(t, err)
		assert.Empty(t, ids)
		// Removing an unknown saga is not an error.
		assert.NoError(t, repo.Remove(ctx, "saga-1"))
	})
}

func TestMemorySagaRepository_Contract(t *testing.T) {
	runSagaRepositoryContract(t, func(t *testing.T) SagaRepository {
		return NewMemorySagaRepository()
	})
}
