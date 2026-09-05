package kafka

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/protocol"
	"github.com/segmentio/kafka-go/protocol/metadata"
	"github.com/segmentio/kafka-go/protocol/produce"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gocamel "gitlab.com/tranchida/gocamel"
)

type recordingTransport struct{ topics chan string }

func (r recordingTransport) RoundTrip(_ context.Context, _ net.Addr, request protocol.Message) (protocol.Message, error) {
	switch req := request.(type) {
	case *metadata.Request:
		return &metadata.Response{Topics: []metadata.ResponseTopic{{Name: req.TopicNames[0], Partitions: []metadata.ResponsePartition{{PartitionIndex: 0}}}}}, nil
	case *produce.Request:
		r.topics <- req.Topics[0].Topic
		return &produce.Response{Topics: []produce.ResponseTopic{{Topic: req.Topics[0].Topic, Partitions: []produce.ResponsePartition{{Partition: 0}}}}}, nil
	default:
		return nil, fmt.Errorf("unexpected request %T", request)
	}
}

func TestProducerPublishesConfiguredAndOverriddenTopic(t *testing.T) {
	ep, err := NewKafkaComponent().CreateEndpoint("kafka:orders?allowHeaderOverride=true")
	require.NoError(t, err)
	producer, err := ep.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, producer.Start(context.Background()))
	defer producer.Stop()
	topics := make(chan string, 2)
	ep.(*KafkaEndpoint).writer.Transport = recordingTransport{topics: topics}
	for _, topic := range []string{"orders", "other"} {
		e := gocamel.NewExchange(context.Background())
		e.SetBody("payload")
		if topic != "orders" {
			e.SetHeader(CamelKafkaTopic, topic)
		}
		require.NoError(t, producer.Send(e))
		assert.Equal(t, topic, <-topics)
	}
}

type scriptedReader struct {
	events  []string // accessed only by consumer goroutine; read after join
	fetches int
	commits int
	cancel  context.CancelFunc
}

func (r *scriptedReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	r.fetches++
	r.events = append(r.events, fmt.Sprintf("fetch:%d", r.fetches))
	if r.fetches > 1 {
		r.cancel()
		return kafka.Message{}, ctx.Err()
	}
	return kafka.Message{Topic: "orders", Offset: 10, Value: []byte("original")}, nil
}
func (r *scriptedReader) CommitMessages(_ context.Context, msg ...kafka.Message) error {
	r.commits++
	r.events = append(r.events, fmt.Sprintf("commit:%d", msg[0].Offset))
	if r.commits == 1 {
		return errors.New("temporary commit failure")
	}
	return nil
}
func (*scriptedReader) Close() error { return nil }

func TestConsumerResolvesProcessingAndCommitBeforeFetchingNext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := &scriptedReader{cancel: cancel}
	attempts := 0
	c := &KafkaConsumer{endpoint: &KafkaEndpoint{groupID: "group"}, reader: reader}
	c.processor = gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		attempts++
		reader.events = append(reader.events, "process")
		if string(e.GetBody().([]byte)) != "original" {
			return errors.New("retry body was mutated")
		}
		if attempts == 1 {
			e.GetBody().([]byte)[0] = 'X'
			return errors.New("temporary processing failure")
		}
		return nil
	})
	c.wg.Add(1)
	var joined sync.WaitGroup
	joined.Go(func() { c.run(ctx) })
	done := make(chan struct{})
	go func() { joined.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		cancel()
		<-done
		t.Fatal("consumer did not finish")
	}
	assert.Equal(t, []string{"fetch:1", "process", "process", "commit:10", "commit:10", "fetch:2"}, reader.events)
}
