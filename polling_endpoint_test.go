package gocamel

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSingleMessagePollingPreservesQueue(t *testing.T) {
	for _, uri := range []string{"seda:single", "chan:single"} {
		t.Run(uri, func(t *testing.T) {
			c := NewCamelContext()
			ep, err := c.CreateEndpoint(uri)
			require.NoError(t, err)
			poll := ep.(PollingEndpoint)
			ex, err := poll.Poll(context.Background(), 0)
			require.NoError(t, err)
			assert.Nil(t, ex)
			p, err := ep.CreateProducer()
			require.NoError(t, err)
			for _, body := range []string{"first", "second"} {
				e := NewExchange(context.Background())
				e.SetBody(body)
				require.NoError(t, p.Send(e))
			}
			for _, body := range []string{"first", "second"} {
				ex, err := poll.Poll(context.Background(), 0)
				require.NoError(t, err)
				require.NotNil(t, ex)
				assert.Equal(t, body, ex.GetBody())
				require.NoError(t, CompleteExchange(ex, nil))
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err = poll.Poll(ctx, -time.Second)
			assert.ErrorIs(t, err, context.Canceled)
			consumer, err := ep.CreateConsumer(ProcessorFunc(func(*Exchange) error { return nil }))
			require.NoError(t, err)
			defer consumer.Stop()
			_, err = poll.Poll(context.Background(), 0)
			assert.Error(t, err, "an active receiver registration must exclude polling")
		})
	}
}

func TestPollEnrichRejectsContinuousOnlyEndpoint(t *testing.T) {
	c := NewCamelContext()
	p := NewPollEnricher(c, "direct:unsupported", nil, 0)
	assert.ErrorContains(t, p.Process(NewExchange(context.Background())), "does not support single-message polling")
}
