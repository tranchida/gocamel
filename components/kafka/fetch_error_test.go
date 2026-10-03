package kafka

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	gocamel "gitlab.com/tranchida/gocamel"
)

// failingFetchReader returns the scripted fetch errors first, then behaves
// like oneRecordReader.
type failingFetchReader struct {
	oneRecordReader
	errs []error
}

func (r *failingFetchReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	if len(r.errs) > 0 {
		err := r.errs[0]
		r.errs = r.errs[1:]
		return kafka.Message{}, err
	}
	return r.oneRecordReader.FetchMessage(ctx)
}

func newFetchErrorConsumer(reader messageReader, processed *int) *KafkaConsumer {
	return &KafkaConsumer{
		endpoint: &KafkaEndpoint{groupID: "group", redelivery: fastPolicy(0)},
		reader:   reader,
		processor: gocamel.ProcessorFunc(func(*gocamel.Exchange) error {
			*processed++
			return nil
		}),
	}
}

func TestConsumerRetriesTransientFetchErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := &failingFetchReader{
		oneRecordReader: oneRecordReader{msg: kafka.Message{Topic: "orders", Offset: 3}, cancel: cancel},
		// Neither means the reader is closed: the consumer must keep going.
		errs: []error{errors.New("write tcp: use of closed network connection"), io.ErrUnexpectedEOF},
	}
	processed := 0
	runConsumer(t, newFetchErrorConsumer(reader, &processed), ctx, cancel)

	assert.Equal(t, 1, processed, "the record after the transient errors is consumed")
	assert.Equal(t, []int64{3}, reader.commits)
}

func TestConsumerStopsWhenReaderIsClosed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := &failingFetchReader{
		oneRecordReader: oneRecordReader{msg: kafka.Message{Topic: "orders", Offset: 3}, cancel: cancel},
		errs:            []error{io.EOF}, // kafka-go: the reader was closed
	}
	processed := 0
	runConsumer(t, newFetchErrorConsumer(reader, &processed), ctx, cancel)

	assert.Zero(t, processed)
	assert.NoError(t, ctx.Err(), "the loop ended on io.EOF, not on cancellation")
}
