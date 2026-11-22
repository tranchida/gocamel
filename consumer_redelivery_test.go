package gocamel

import (
	"context"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConsumerRedeliveryPolicy_OverflowAndWaitCancellation(t *testing.T) {
	t.Run("overflows fall back to default", func(t *testing.T) {
		u, err := url.Parse("kafka:t?redeliveryDelay=99999999999999&maxRedeliveryDelay=9223372036854775807")
		require.NoError(t, err)

		p := ParseConsumerRedeliveryPolicy(u)
		assert.Equal(t, DefaultConsumerRedeliveryDelay, p.Delay)
		assert.Equal(t, DefaultConsumerMaxRedeliveryDelay, p.MaxDelay)
		assert.Greater(t, p.Delay, int64(0))
		assert.Greater(t, p.MaxDelay, int64(0))
	})

	t.Run("Wait returns false deterministically when ctx already cancelled and Delay is zero", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		p := ConsumerRedeliveryPolicy{Delay: 0, MaxDelay: 0}
		for i := 0; i < 100; i++ {
			assert.False(t, p.Wait(ctx, 0), "iteration %d should observe cancellation", i)
		}
	})
}
