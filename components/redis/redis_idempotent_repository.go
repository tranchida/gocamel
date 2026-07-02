package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisIdempotentRepository implements IdempotentRepository using Redis.
// It stores keys with a specific prefix and an optional TTL (Time To Live).
type RedisIdempotentRepository struct {
	client *redis.Client
	prefix string
	ttl    time.Duration
}

// NewRedisIdempotentRepository creates a new RedisIdempotentRepository.
func NewRedisIdempotentRepository(client *redis.Client, prefix string, ttl time.Duration) *RedisIdempotentRepository {
	return &RedisIdempotentRepository{
		client: client,
		prefix: prefix,
		ttl:    ttl,
	}
}

// Add attempts to add the key to the repository.
// Returns true if the key was added (did not exist before), false if it already exists.
func (r *RedisIdempotentRepository) Add(ctx context.Context, key string) (bool, error) {
	redisKey := r.prefix + key
	// SetNX sets the key only if it does not already exist
	added, err := r.client.SetNX(ctx, redisKey, "1", r.ttl).Result()
	if err != nil {
		return false, fmt.Errorf("redis idempotent repository Add error: %w", err)
	}
	return added, nil
}

// Contains returns true if the key is already in the repository.
func (r *RedisIdempotentRepository) Contains(ctx context.Context, key string) (bool, error) {
	redisKey := r.prefix + key
	exists, err := r.client.Exists(ctx, redisKey).Result()
	if err != nil {
		return false, fmt.Errorf("redis idempotent repository Contains error: %w", err)
	}
	return exists > 0, nil
}

// Remove deletes the key from the repository.
func (r *RedisIdempotentRepository) Remove(ctx context.Context, key string) error {
	redisKey := r.prefix + key
	err := r.client.Del(ctx, redisKey).Err()
	if err != nil {
		return fmt.Errorf("redis idempotent repository Remove error: %w", err)
	}
	return nil
}

// Clear removes all keys matching the prefix.
func (r *RedisIdempotentRepository) Clear(ctx context.Context) error {
	pattern := r.prefix + "*"

	// Scan keys with the configured prefix
	var cursor uint64
	for {
		var keys []string
		var err error
		keys, cursor, err = r.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return fmt.Errorf("redis idempotent repository Clear error: %w", err)
		}

		if len(keys) > 0 {
			err = r.client.Del(ctx, keys...).Err()
			if err != nil {
				return fmt.Errorf("redis idempotent repository Clear (Del) error: %w", err)
			}
		}

		if cursor == 0 {
			break
		}
	}

	return nil
}
