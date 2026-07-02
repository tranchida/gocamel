package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
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
