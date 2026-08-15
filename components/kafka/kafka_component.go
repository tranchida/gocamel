package kafka

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/segmentio/kafka-go"

	gocamel "gitlab.com/tranchida/gocamel"
)

// Well-known Camel Kafka headers
const (
	CamelKafkaTopic        = "CamelKafkaTopic"
	CamelKafkaKey          = "CamelKafkaKey"
	CamelKafkaPartitionKey = "CamelKafkaPartitionKey"
	CamelKafkaPartition    = "CamelKafkaPartition"
	CamelKafkaOffset       = "CamelKafkaOffset"
	CamelKafkaTimestamp    = "CamelKafkaTimestamp"
	CamelKafkaHeaders      = "CamelKafkaHeaders"
)

// KafkaComponent implements the Camel Kafka component.
type KafkaComponent struct{}

// NewKafkaComponent creates a new KafkaComponent instance.
func NewKafkaComponent() *KafkaComponent {
	return &KafkaComponent{}
}

// CreateEndpoint creates a new KafkaEndpoint from a URI.
// Example URIs:
//
//	kafka:orders?brokers=localhost:9092&groupId=billing
//	kafka://orders?brokers=broker1:9092,broker2:9092&partition=0
func (c *KafkaComponent) CreateEndpoint(uri string) (gocamel.Endpoint, error) {
	u, err := gocamel.ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid Kafka URI: %w", err)
	}

	topic := u.Host + u.Path
	if topic == "" {
		topic = u.Opaque
	}
	if topic == "" {
		topic = gocamel.GetConfigValue(u, "topic")
	}

	brokersStr := gocamel.GetConfigValue(u, "brokers")
	if brokersStr == "" {
		brokersStr = "localhost:9092"
	}
	brokers := strings.Split(brokersStr, ",")
	for i := range brokers {
		brokers[i] = strings.TrimSpace(brokers[i])
	}

	groupID := gocamel.GetConfigValue(u, "groupId")
	if groupID == "" {
		groupID = gocamel.GetConfigValue(u, "group")
	}

	partition := -1
	if pStr := gocamel.GetConfigValue(u, "partition"); pStr != "" {
		if p, err := strconv.Atoi(pStr); err == nil {
			partition = p
		}
	}

	clientID := gocamel.GetConfigValue(u, "clientId")

	return &KafkaEndpoint{
		uri:       uri,
		topic:     topic,
		brokers:   brokers,
		groupID:   groupID,
		partition: partition,
		clientID:  clientID,
	}, nil
}

// KafkaEndpoint represents a Kafka topic endpoint.
type KafkaEndpoint struct {
	uri       string
	topic     string
	brokers   []string
	groupID   string
	partition int
	clientID  string

	writer   *kafka.Writer
	consumer *KafkaConsumer
	mu       sync.Mutex
}

// URI returns the endpoint URI.
func (e *KafkaEndpoint) URI() string {
	return e.uri
}

// CreateProducer creates a KafkaProducer for publishing messages.
func (e *KafkaEndpoint) CreateProducer() (gocamel.Producer, error) {
	return &KafkaProducer{endpoint: e}, nil
}

// CreateConsumer creates a KafkaConsumer for subscribing to messages.
func (e *KafkaEndpoint) CreateConsumer(processor gocamel.Processor) (gocamel.Consumer, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.consumer != nil {
		return nil, fmt.Errorf("multiple consumers for the same Kafka endpoint are not allowed: %s", e.uri)
	}

	e.consumer = &KafkaConsumer{
		endpoint:  e,
		processor: processor,
	}
	return e.consumer, nil
}

// KafkaProducer publishes messages to a Kafka topic.
type KafkaProducer struct {
	endpoint *KafkaEndpoint
}

// Start initializes the Kafka writer.
func (p *KafkaProducer) Start(_ context.Context) error {
	p.endpoint.mu.Lock()
	defer p.endpoint.mu.Unlock()

	if p.endpoint.writer != nil {
		return nil
	}

	w := &kafka.Writer{
		Addr:         kafka.TCP(p.endpoint.brokers...),
		Topic:        p.endpoint.topic,
		Balancer:     &kafka.LeastBytes{},
		BatchTimeout: 10 * time.Millisecond,
	}
	p.endpoint.writer = w
	return nil
}

// Stop closes the Kafka writer.
func (p *KafkaProducer) Stop() error {
	p.endpoint.mu.Lock()
	defer p.endpoint.mu.Unlock()

	if p.endpoint.writer != nil {
		err := p.endpoint.writer.Close()
		p.endpoint.writer = nil
		return err
	}
	return nil
}

// Send publishes the exchange body to Kafka.
func (p *KafkaProducer) Send(exchange *gocamel.Exchange) error {
	p.endpoint.mu.Lock()
	w := p.endpoint.writer
	p.endpoint.mu.Unlock()

	if w == nil {
		return fmt.Errorf("kafka producer is not started")
	}

	targetTopic := p.endpoint.topic
	if hTopic, ok := exchange.GetHeaderAsString(CamelKafkaTopic); ok && hTopic != "" {
		targetTopic = hTopic
	}

	if targetTopic == "" {
		return fmt.Errorf("kafka: topic is required to publish a message")
	}

	var keyBytes []byte
	if keyStr, ok := exchange.GetHeaderAsString(CamelKafkaPartitionKey); ok {
		keyBytes = []byte(keyStr)
	} else if keyStr, ok := exchange.GetHeaderAsString(CamelKafkaKey); ok {
		keyBytes = []byte(keyStr)
	} else if key, exists := exchange.In.GetHeader(CamelKafkaKey); exists {
		if kb, ok := key.([]byte); ok {
			keyBytes = kb
		}
	}

	body := exchange.In.GetBody()
	var valueBytes []byte
	switch b := body.(type) {
	case []byte:
		valueBytes = b
	case string:
		valueBytes = []byte(b)
	case nil:
		valueBytes = []byte{}
	default:
		valueBytes = []byte(fmt.Sprintf("%v", b))
	}

	var kafkaHeaders []kafka.Header
	for k, v := range exchange.In.GetHeaders() {
		if strings.HasPrefix(k, "Camel") {
			continue
		}
		kafkaHeaders = append(kafkaHeaders, kafka.Header{
			Key:   k,
			Value: []byte(fmt.Sprintf("%v", v)),
		})
	}

	msg := kafka.Message{
		Topic:   targetTopic,
		Key:     keyBytes,
		Value:   valueBytes,
		Headers: kafkaHeaders,
		Time:    time.Now(),
	}

	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}

	if err := w.WriteMessages(ctx, msg); err != nil {
		return fmt.Errorf("kafka publish error on topic %q: %w", targetTopic, err)
	}

	return nil
}

// KafkaConsumer consumes messages from a Kafka topic.
type KafkaConsumer struct {
	endpoint  *KafkaEndpoint
	processor gocamel.Processor
	reader    *kafka.Reader
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// Start begins consuming Kafka messages.
func (c *KafkaConsumer) Start(ctx context.Context) error {
	c.endpoint.mu.Lock()
	defer c.endpoint.mu.Unlock()

	if c.endpoint.topic == "" {
		return fmt.Errorf("kafka consumer: topic is required")
	}

	readerConfig := kafka.ReaderConfig{
		Brokers:  c.endpoint.brokers,
		Topic:    c.endpoint.topic,
		MinBytes: 1,
		MaxBytes: 10e6, // 10MB
	}

	if c.endpoint.groupID != "" {
		readerConfig.GroupID = c.endpoint.groupID
	} else if c.endpoint.partition >= 0 {
		readerConfig.Partition = c.endpoint.partition
	}

	c.reader = kafka.NewReader(readerConfig)

	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	c.wg.Add(1)
	go c.run(ctx)

	return nil
}

// Stop stops the consumer and closes the reader.
func (c *KafkaConsumer) Stop() error {
	if c.cancel != nil {
		c.cancel()
	}
	if c.reader != nil {
		_ = c.reader.Close()
	}
	c.wg.Wait()

	c.endpoint.mu.Lock()
	defer c.endpoint.mu.Unlock()
	if c.endpoint.consumer == c {
		c.endpoint.consumer = nil
	}
	return nil
}

func (c *KafkaConsumer) run(ctx context.Context) {
	defer c.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		default:
			msg, err := c.reader.FetchMessage(ctx)
			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return
				}
				// Reader closed or EOF
				if strings.Contains(err.Error(), "closed") || strings.Contains(err.Error(), "EOF") {
					return
				}
				time.Sleep(100 * time.Millisecond)
				continue
			}

			exchange := gocamel.NewExchange(ctx)
			exchange.In.SetBody(msg.Value)
			exchange.In.SetHeader(CamelKafkaTopic, msg.Topic)
			exchange.In.SetHeader(CamelKafkaPartition, msg.Partition)
			exchange.In.SetHeader(CamelKafkaOffset, msg.Offset)
			exchange.In.SetHeader(CamelKafkaKey, string(msg.Key))
			exchange.In.SetHeader(CamelKafkaPartitionKey, string(msg.Key))
			exchange.In.SetHeader(CamelKafkaTimestamp, msg.Time)

			for _, h := range msg.Headers {
				exchange.In.SetHeader(h.Key, string(h.Value))
			}

			if err := gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange)); err != nil {
				gocamel.LogError("kafka consumer processing error",
					"uri", c.endpoint.uri,
					"error", err,
					"exchange_id", exchange.ID,
				)
			} else {
				// Commit message on successful processing if groupID is configured
				if c.endpoint.groupID != "" {
					_ = c.reader.CommitMessages(ctx, msg)
				}
			}
		}
	}
}
