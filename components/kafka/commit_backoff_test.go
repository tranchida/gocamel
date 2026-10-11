package kafka

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gocamel "github.com/tranchida/gocamel"
)

// failingCommitReader serves one record and fails every commit, cancelling
// the consumer after maxCommits attempts. Fields are only touched by the
// consumer goroutine and read after it has been joined.
type failingCommitReader struct {
	fetched    bool
	commitAt   []time.Time
	maxCommits int
	cancel     context.CancelFunc
}

func (r *failingCommitReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	if r.fetched {
		r.cancel()
		return kafka.Message{}, ctx.Err()
	}
	r.fetched = true
	return kafka.Message{Topic: "orders", Offset: 7}, nil
}

func (r *failingCommitReader) CommitMessages(context.Context, ...kafka.Message) error {
	r.commitAt = append(r.commitAt, time.Now())
	if len(r.commitAt) >= r.maxCommits {
		r.cancel()
	}
	return errors.New("commit failure")
}

func (*failingCommitReader) Close() error { return nil }

func TestCommitRetryBackoffIsExponentialAndCapped(t *testing.T) {
	assert.Equal(t, 100*time.Millisecond, commitRetry.Backoff(0))
	assert.Equal(t, 200*time.Millisecond, commitRetry.Backoff(1))
	assert.Equal(t, 400*time.Millisecond, commitRetry.Backoff(2))
	assert.Equal(t, 3200*time.Millisecond, commitRetry.Backoff(5))
	assert.Equal(t, 5*time.Second, commitRetry.Backoff(6))
	assert.Equal(t, 5*time.Second, commitRetry.Backoff(100))
	assert.False(t, commitRetry.Exhausted(1000), "commits are retried until they succeed")
}

func TestConsumerBacksOffExponentiallyOnCommitFailure(t *testing.T) {
	saved := commitRetry
	commitRetry.Delay = 10 * time.Millisecond
	commitRetry.MaxDelay = 40 * time.Millisecond
	t.Cleanup(func() { commitRetry = saved })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := &failingCommitReader{maxCommits: 5, cancel: cancel}
	c := &KafkaConsumer{
		endpoint:  &KafkaEndpoint{groupID: "group", redelivery: gocamel.NewConsumerRedeliveryPolicy()},
		reader:    reader,
		processor: gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }),
	}
	runConsumer(t, c, ctx, cancel)

	require.Len(t, reader.commitAt, 5)
	gaps := make([]time.Duration, 0, 4)
	for i := 1; i < len(reader.commitAt); i++ {
		gaps = append(gaps, reader.commitAt[i].Sub(reader.commitAt[i-1]))
	}
	// Timers never fire early, so lower bounds are exact: 10ms, 20ms, 40ms, 40ms (cap).
	assert.GreaterOrEqual(t, gaps[1], 20*time.Millisecond)
	assert.GreaterOrEqual(t, gaps[2], 40*time.Millisecond)
	assert.GreaterOrEqual(t, gaps[3], 40*time.Millisecond)
	// The first retry uses the initial delay, not a fixed 100ms.
	assert.Less(t, gaps[0], 80*time.Millisecond)
}

func TestConsumerStopsDuringCommitBackoff(t *testing.T) {
	saved := commitRetry
	commitRetry.Delay = time.Hour
	commitRetry.MaxDelay = time.Hour
	t.Cleanup(func() { commitRetry = saved })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := &failingCommitReader{maxCommits: 1, cancel: cancel}
	c := &KafkaConsumer{
		endpoint:  &KafkaEndpoint{groupID: "group", redelivery: gocamel.NewConsumerRedeliveryPolicy()},
		reader:    reader,
		processor: gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }),
	}
	runConsumer(t, c, ctx, cancel)
	assert.Len(t, reader.commitAt, 1)
}
