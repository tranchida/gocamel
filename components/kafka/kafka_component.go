package kafka

import (
	"context"
	"errors"
	"fmt"
	"io"
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

// sensitiveOutboundHeaders are exchange headers carrying credentials or
// session state. They are never forwarded to the Kafka broker as record
// headers (matching is case-insensitive), so a producer sitting downstream
// of a consumer does not leak a client's credentials to the broker.
var sensitiveOutboundHeaders = map[string]bool{
	"authorization":       true,
	"cookie":              true,
	"proxy-authorization": true,
	"set-cookie":          true,
	"www-authenticate":    true,
}

// parseForwardHeaders builds the case-insensitive header allowlist from the
// forwardHeaders URI option. It returns nil when the option is unset or
// empty, meaning "forward all non-Camel, non-sensitive headers".
func parseForwardHeaders(value string) map[string]bool {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	allow := make(map[string]bool)
	for _, name := range strings.Split(value, ",") {
		if name = strings.TrimSpace(name); name != "" {
			allow[strings.ToLower(name)] = true
		}
	}
	if len(allow) == 0 {
		return nil
	}
	return allow
}

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
//
// Options:
//
//	brokers             Comma-separated list of Kafka brokers (default: localhost:9092)
//	groupId             Consumer group ID
//	partition           Partition to consume from (when no groupId is set)
//	clientId            Kafka client ID
//	topic               Topic name (alternative to the URI host/path)
//	allowHeaderOverride true to let the CamelKafkaTopic, CamelKafkaPartitionKey and
//	                    CamelKafkaKey headers replace the corresponding URI
//	                    configuration when producing (default: false)
//	forwardHeaders      Comma-separated allowlist of exchange headers the
//	                    producer forwards as Kafka record headers. When set,
//	                    ONLY these headers are propagated. When unset, all
//	                    non-Camel headers except sensitive credentials
//	                    (Authorization, Cookie, Proxy-Authorization, Set-Cookie,
//	                    WWW-Authenticate) are forwarded.
//	maxRedeliveries     Redeliveries of a record whose route failed before its
//	                    offset is committed anyway (default: 10; -1 retries
//	                    forever, 0 skips a failed record right away)
//	redeliveryDelay     First redelivery delay in ms, doubled on each attempt
//	                    (default: 100)
//	maxRedeliveryDelay  Cap of the redelivery backoff in ms (default: 30000)
//
// A record the route dead-lettered (DeadLetterChannel) is committed, never
// redelivered. Inbound record headers named Camel* are dropped: a producer
// writing to the topic must not drive internal headers such as CamelFileName.
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

	allowHeaderOverride := false
	if v := gocamel.GetConfigValue(u, "allowHeaderOverride"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			allowHeaderOverride = b
		}
	}

	return &KafkaEndpoint{
		uri:                 uri,
		topic:               topic,
		brokers:             brokers,
		groupID:             groupID,
		partition:           partition,
		clientID:            clientID,
		allowHeaderOverride: allowHeaderOverride,
		forwardHeaders:      parseForwardHeaders(gocamel.GetConfigValue(u, "forwardHeaders")),
		redelivery:          gocamel.ParseConsumerRedeliveryPolicy(u),
	}, nil
}

// KafkaEndpoint represents a Kafka topic endpoint.
type KafkaEndpoint struct {
	uri                 string
	topic               string
	brokers             []string
	groupID             string
	partition           int
	clientID            string
	allowHeaderOverride bool // opt-in: CamelKafkaTopic/CamelKafkaKey headers may override URI settings
	// forwardHeaders is the optional header allowlist: when non-nil, ONLY the
	// listed headers are forwarded to the broker; when nil, every non-Camel,
	// non-sensitive header is forwarded.
	forwardHeaders map[string]bool
	// redelivery bounds how a failed record is re-run before its offset is
	// committed (consumer side).
	redelivery gocamel.ConsumerRedeliveryPolicy

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

// resolveTopic returns the topic to publish to.
// The CamelKafkaTopic header is honoured only when the endpoint opts in with
// allowHeaderOverride=true: headers can be influenced by untrusted input and
// would otherwise let a caller publish to arbitrary topics.
func (p *KafkaProducer) resolveTopic(exchange *gocamel.Exchange) string {
	if p.endpoint.allowHeaderOverride {
		if hTopic, ok := exchange.GetHeaderAsString(CamelKafkaTopic); ok && hTopic != "" {
			return hTopic
		}
	}
	return p.endpoint.topic
}

// resolveKey returns the message key to publish with.
// The CamelKafkaPartitionKey and CamelKafkaKey headers are honoured only when
// the endpoint opts in with allowHeaderOverride=true.
func (p *KafkaProducer) resolveKey(exchange *gocamel.Exchange) []byte {
	if !p.endpoint.allowHeaderOverride {
		return nil
	}
	if keyStr, ok := exchange.GetHeaderAsString(CamelKafkaPartitionKey); ok {
		return []byte(keyStr)
	}
	if keyStr, ok := exchange.GetHeaderAsString(CamelKafkaKey); ok {
		return []byte(keyStr)
	}
	if key, exists := exchange.In.GetHeader(CamelKafkaKey); exists {
		if kb, ok := key.([]byte); ok {
			return kb
		}
	}
	return nil
}

// filterHeaders returns the exchange headers allowed to be forwarded to the
// Kafka broker: Camel-prefixed internal headers and sensitive credential
// headers are never forwarded; when an explicit forwardHeaders allowlist is
// configured, only the listed headers remain (sensitive ones stay blocked).
func (p *KafkaProducer) filterHeaders(exchange *gocamel.Exchange) []kafka.Header {
	var kafkaHeaders []kafka.Header
	for name, value := range exchange.In.GetHeaders() {
		if strings.HasPrefix(name, "Camel") {
			continue
		}
		lower := strings.ToLower(name)
		if sensitiveOutboundHeaders[lower] {
			continue
		}
		if p.endpoint.forwardHeaders != nil && !p.endpoint.forwardHeaders[lower] {
			continue
		}
		kafkaHeaders = append(kafkaHeaders, kafka.Header{
			Key:   name,
			Value: []byte(fmt.Sprintf("%v", value)),
		})
	}
	return kafkaHeaders
}

// Send publishes the exchange body to Kafka.
func (p *KafkaProducer) Send(exchange *gocamel.Exchange) error {
	p.endpoint.mu.Lock()
	w := p.endpoint.writer
	p.endpoint.mu.Unlock()

	if w == nil {
		return fmt.Errorf("kafka producer is not started")
	}

	targetTopic := p.resolveTopic(exchange)

	if targetTopic == "" {
		return fmt.Errorf("kafka: topic is required to publish a message")
	}

	keyBytes := p.resolveKey(exchange)

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

	kafkaHeaders := p.filterHeaders(exchange)

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
	reader    messageReader
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

// messageReader allows consumer delivery and commit ordering to be verified
// without a broker. kafka.Reader implements this contract.
type messageReader interface {
	FetchMessage(context.Context) (kafka.Message, error)
	CommitMessages(context.Context, ...kafka.Message) error
	Close() error
}

func (c *KafkaConsumer) run(ctx context.Context) {
	defer c.wg.Done()
	for ctx.Err() == nil {
		msg, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return
			}
			// kafka-go returns exactly io.EOF once the reader is closed (it
			// turns an EOF from the broker into io.ErrUnexpectedEOF). Any
			// other error, even one mentioning "closed" or "EOF", is
			// transient: stopping on it would leave the route started with
			// no consumer goroutine.
			if errors.Is(err, io.EOF) {
				return
			}
			gocamel.LogWarn("kafka fetch failed; retrying", "topic", c.endpoint.topic, "error", err)
			if !waitForRetry(ctx) {
				return
			}
			continue
		}
		// Resolve this delivery before fetching another offset. Committing a later
		// offset would otherwise acknowledge a failed message in the same partition.
		for redeliveries := 0; ; redeliveries++ {
			if ctx.Err() != nil {
				return
			}
			exchange := gocamel.NewExchange(ctx)
			exchange.In.SetBody(append([]byte(nil), msg.Value...))
			exchange.In.SetHeader(CamelKafkaTopic, msg.Topic)
			exchange.In.SetHeader(CamelKafkaPartition, msg.Partition)
			exchange.In.SetHeader(CamelKafkaOffset, msg.Offset)
			exchange.In.SetHeader(CamelKafkaKey, string(msg.Key))
			exchange.In.SetHeader(CamelKafkaPartitionKey, string(msg.Key))
			exchange.In.SetHeader(CamelKafkaTimestamp, msg.Time)
			for _, h := range msg.Headers {
				// A record producer must not set internal Camel* headers.
				if gocamel.IsCamelHeader(h.Key) {
					continue
				}
				exchange.In.SetHeader(h.Key, string(h.Value))
			}
			err := gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange))
			if err == nil {
				break
			}
			if gocamel.IsDeadLettered(exchange) {
				// Redelivering would dead-letter a duplicate on every attempt.
				gocamel.LogWarn("kafka record dead-lettered; committing offset", "topic", msg.Topic, "offset", msg.Offset, "error", err)
				break
			}
			if c.endpoint.redelivery.Exhausted(redeliveries) {
				gocamel.LogError("kafka record failed; redeliveries exhausted, committing offset",
					"topic", msg.Topic, "partition", msg.Partition, "offset", msg.Offset,
					"redeliveries", redeliveries, "error", err)
				break
			}
			gocamel.LogError("kafka processing failed; retrying before advancing offset",
				"topic", msg.Topic, "offset", msg.Offset, "redelivery", redeliveries+1,
				"retry_in", c.endpoint.redelivery.Backoff(redeliveries), "error", err)
			if !c.endpoint.redelivery.Wait(ctx, redeliveries) {
				return
			}
		}
		if c.endpoint.groupID != "" {
			// Retrying a commit must not re-run a successful processor. The
			// backoff restarts from commitRetry.Delay for every record.
			for attempt := 0; ; attempt++ {
				err := c.reader.CommitMessages(ctx, msg)
				if err == nil {
					break
				}
				gocamel.LogError("kafka commit failed; retrying before advancing offset", "topic", msg.Topic, "offset", msg.Offset,
					"attempt", attempt+1, "retry_in", commitRetry.Backoff(attempt), "error", err)
				if !commitRetry.Wait(ctx, attempt) {
					return
				}
			}
		}
	}
}

// commitRetry paces offset commit retries: a persistently failing commit
// backs off exponentially from 100ms up to 5s instead of spinning, and is
// retried until it succeeds (a negative MaxRedeliveries never gives up).
var commitRetry = gocamel.ConsumerRedeliveryPolicy{
	MaxRedeliveries: -1,
	Delay:           100 * time.Millisecond,
	MaxDelay:        5 * time.Second,
}

func waitForRetry(ctx context.Context) bool {
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
