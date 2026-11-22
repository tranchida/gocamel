package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedisIdempotentRepository(t *testing.T) {
	// Spin up in-memory miniredis server
	s, err := miniredis.Run()
	if err != nil {
		t.Fatalf("Failed to start miniredis: %v", err)
	}
	defer s.Close()

	// Connect go-redis client
	client := redis.NewClient(&redis.Options{
		Addr: s.Addr(),
	})
	defer client.Close()

	ctx := context.Background()
	repo := NewRedisIdempotentRepository(client, "test-prefix:", 10*time.Minute)

	// Test Add (first time -> true)
	added, err := repo.Add(ctx, "msg1")
	if err != nil {
		t.Fatalf("Add error: %v", err)
	}
	if !added {
		t.Error("Expected key 'msg1' to be added successfully")
	}

	// Test Add (second time -> false)
	added, err = repo.Add(ctx, "msg1")
	if err != nil {
		t.Fatalf("Add second error: %v", err)
	}
	if added {
		t.Error("Expected key 'msg1' to not be added again")
	}

	// Test Contains
	contains, err := repo.Contains(ctx, "msg1")
	if err != nil {
		t.Fatalf("Contains error: %v", err)
	}
	if !contains {
		t.Error("Expected repository to contain 'msg1'")
	}

	contains, err = repo.Contains(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("Contains nonexistent error: %v", err)
	}
	if contains {
		t.Error("Expected repository to not contain 'nonexistent'")
	}

	// Test Remove
	err = repo.Remove(ctx, "msg1")
	if err != nil {
		t.Fatalf("Remove error: %v", err)
	}

	contains, _ = repo.Contains(ctx, "msg1")
	if contains {
		t.Error("Expected key 'msg1' to be removed from repository")
	}
}

func TestRedisIdempotentRepository_Clear(t *testing.T) {
	s, _ := miniredis.Run()
	defer s.Close()

	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer client.Close()

	ctx := context.Background()
	repo := NewRedisIdempotentRepository(client, "prefix1:", 0)
	otherRepo := NewRedisIdempotentRepository(client, "prefix2:", 0)

	_, _ = repo.Add(ctx, "keyA")
	_, _ = repo.Add(ctx, "keyB")
	_, _ = otherRepo.Add(ctx, "keyC")

	// Verify they are present
	containsA, _ := repo.Contains(ctx, "keyA")
	containsC, _ := otherRepo.Contains(ctx, "keyC")
	if !containsA || !containsC {
		t.Fatal("Keys not inserted successfully")
	}

	// Clear repo (should only remove keys with "prefix1:")
	err := repo.Clear(ctx)
	if err != nil {
		t.Fatalf("Clear error: %v", err)
	}

	containsA, _ = repo.Contains(ctx, "keyA")
	containsB, _ := repo.Contains(ctx, "keyB")
	containsC, _ = otherRepo.Contains(ctx, "keyC")

	if containsA || containsB {
		t.Error("Expected keys keyA and keyB to be cleared")
	}
	if !containsC {
		t.Error("Expected other prefix keyC to remain intact")
	}
}

func TestRedisIdempotentRepository_ClearEscapesGlobAndRejectsEmptyPrefix(t *testing.T) {
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	t.Run("rejects empty prefix without deleting keys", func(t *testing.T) {
		require.NoError(t, client.Set(ctx, "important:data", "keep", 0).Err())
		repo := NewRedisIdempotentRepository(client, "", 0)

		err := repo.Clear(ctx)
		require.Error(t, err)

		val, err := client.Get(ctx, "important:data").Result()
		require.NoError(t, err)
		assert.Equal(t, "keep", val)
	})

	t.Run("escapes glob metacharacters in prefix", func(t *testing.T) {
		cases := []struct {
			name      string
			prefix    string
			key       string
			unrelated string
		}{
			{name: "asterisk", prefix: "ns*:", key: "k1", unrelated: "nsOther:k1"},
			{name: "brackets", prefix: "app[1]:", key: "k1", unrelated: "app1:k1"},
			{name: "question", prefix: "q?:", key: "k1", unrelated: "qa:k1"},
			{name: "backslash", prefix: `bs\:`, key: "k1", unrelated: "bs:k1"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				repo := NewRedisIdempotentRepository(client, tc.prefix, 0)
				added, err := repo.Add(ctx, tc.key)
				require.NoError(t, err)
				require.True(t, added)
				require.NoError(t, client.Set(ctx, tc.unrelated, "keep", 0).Err())

				require.NoError(t, repo.Clear(ctx))

				contains, err := repo.Contains(ctx, tc.key)
				require.NoError(t, err)
				assert.False(t, contains, "repository key with metacharacter prefix must be cleared")

				val, err := client.Get(ctx, tc.unrelated).Result()
				require.NoError(t, err, "unrelated key %q must not be matched by escaped prefix", tc.unrelated)
				assert.Equal(t, "keep", val)
			})
		}
	})
}
