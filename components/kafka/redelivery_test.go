package kafka

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gocamel "gitlab.com/tranchida/gocamel"
)

// oneRecordReader serves a single record, then cancels the consumer on the
// next fetch. Fields are only touched by the consumer goroutine and read
// after it has been joined.
type oneRecordReader struct {
	msg     kafka.Message
	fetches int
	commits []int64
	cancel  context.CancelFunc
}

func (r *oneRecordReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	r.fetches++
	if r.fetches > 1 {
		r.cancel()
		return kafka.Message{}, ctx.Err()
	}
	return r.msg, nil
}

func (r *oneRecordReader) CommitMessages(_ context.Context, msgs ...kafka.Message) error {
	r.commits = append(r.commits, msgs[0].Offset)
	return nil
}

func (*oneRecordReader) Close() error { return nil }

// runConsumer drives c.run until the reader cancels it, failing the test if
// the loop does not terminate.
func runConsumer(t *testing.T, c *KafkaConsumer, ctx context.Context, cancel context.CancelFunc) {
	t.Helper()
	c.wg.Add(1)
	var joined sync.WaitGroup
	joined.Go(func() { c.run(ctx) })
	done := make(chan struct{})
	go func() { joined.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		cancel()
		<-done
		t.Fatal("consumer did not finish")
	}
}

func fastPolicy(maxRedeliveries int) gocamel.ConsumerRedeliveryPolicy {
	return gocamel.ConsumerRedeliveryPolicy{MaxRedeliveries: maxRedeliveries, Delay: time.Millisecond, MaxDelay: time.Millisecond}
}

func TestConsumerCommitsPoisonRecordOnceRedeliveriesAreExhausted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := &oneRecordReader{msg: kafka.Message{Topic: "orders", Offset: 7, Value: []byte("poison")}, cancel: cancel}
	attempts := 0
	c := &KafkaConsumer{
		endpoint: &KafkaEndpoint{groupID: "group", redelivery: fastPolicy(2)},
		reader:   reader,
		processor: gocamel.ProcessorFunc(func(*gocamel.Exchange) error {
			attempts++
			return errors.New("always fails")
		}),
	}
	runConsumer(t, c, ctx, cancel)

	assert.Equal(t, 3, attempts, "1 delivery + 2 redeliveries")
	assert.Equal(t, []int64{7}, reader.commits, "offset committed after giving up")
	assert.Equal(t, 2, reader.fetches, "consumer moved on to the next record")
}

func TestConsumerDoesNotRedeliverDeadLetteredRecord(t *testing.T) {
	camel := gocamel.NewCamelContext()
	var dlq []string
	_, err := camel.CreateRouteBuilder().From("direct:dlq").ProcessFunc(func(e *gocamel.Exchange) error {
		dlq = append(dlq, string(e.GetBody().([]byte)))
		return nil
	}).Build()
	require.NoError(t, err)
	route, err := camel.CreateRouteBuilder().From("direct:in").ProcessFunc(func(*gocamel.Exchange) error {
		return errors.New("business failure")
	}).Build()
	require.NoError(t, err)
	// Default DeadLetterChannel: the original error is still returned to the
	// consumer after the record has been dead-lettered.
	route.SetErrorHandler(gocamel.NewDeadLetterChannel(camel, "direct:dlq"))
	require.NoError(t, camel.Start())
	t.Cleanup(func() { _ = camel.Stop() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := &oneRecordReader{msg: kafka.Message{Topic: "orders", Offset: 3, Value: []byte("order-1")}, cancel: cancel}
	c := &KafkaConsumer{
		endpoint:  &KafkaEndpoint{groupID: "group", redelivery: fastPolicy(-1)},
		reader:    reader,
		processor: route,
	}
	runConsumer(t, c, ctx, cancel)

	assert.Equal(t, []string{"order-1"}, dlq, "dead-lettered exactly once")
	assert.Equal(t, []int64{3}, reader.commits)
}

func TestConsumerDropsInboundCamelHeaders(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := &oneRecordReader{
		msg: kafka.Message{Topic: "orders", Value: []byte("x"), Headers: []protocol.Header{
			{Key: "CamelFileName", Value: []byte("../../etc/cron.d/evil")},
			{Key: "camelExecCommandExecutable", Value: []byte("sh")},
			{Key: "X-Trace", Value: []byte("abc")},
		}},
		cancel: cancel,
	}
	var headers map[string]any
	c := &KafkaConsumer{
		endpoint: &KafkaEndpoint{redelivery: fastPolicy(0)},
		reader:   reader,
		processor: gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
			headers = e.GetIn().GetHeaders()
			return nil
		}),
	}
	runConsumer(t, c, ctx, cancel)

	require.NotNil(t, headers)
	assert.NotContains(t, headers, "CamelFileName")
	assert.NotContains(t, headers, "camelExecCommandExecutable")
	assert.Equal(t, "abc", headers["X-Trace"])
	assert.Equal(t, "orders", headers[CamelKafkaTopic], "component-set Camel headers are kept")
}

func TestEndpointParsesRedeliveryOptions(t *testing.T) {
	ep, err := NewKafkaComponent().CreateEndpoint("kafka:orders?maxRedeliveries=5&redeliveryDelay=200&maxRedeliveryDelay=1000")
	require.NoError(t, err)
	p := ep.(*KafkaEndpoint).redelivery
	assert.Equal(t, 5, p.MaxRedeliveries)
	assert.Equal(t, 200*time.Millisecond, p.Delay)
	assert.Equal(t, time.Second, p.MaxDelay)

	ep, err = NewKafkaComponent().CreateEndpoint("kafka:orders")
	require.NoError(t, err)
	assert.Equal(t, gocamel.NewConsumerRedeliveryPolicy(), ep.(*KafkaEndpoint).redelivery)
}
