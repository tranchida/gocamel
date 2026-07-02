package redis

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/redis/go-redis/v9"

	gocamel "gitlab.com/tranchida/gocamel"
)

// Redis Component constants
const (
	CamelRedisCommand = "CamelRedisCommand"
	CamelRedisKey     = "CamelRedisKey"
	CamelRedisChannel = "CamelRedisChannel"
)

// RedisComponent represents the Camel Redis component.
type RedisComponent struct {
	mu sync.Mutex
}

// NewRedisComponent creates a new RedisComponent instance.
func NewRedisComponent() *RedisComponent {
	return &RedisComponent{}
}

// CreateEndpoint creates a new RedisEndpoint from a URI.
// Example URIs:
//
//	redis://localhost:6379?command=SET&key=mykey
//	redis://localhost:6379?subscribe=true&channel=mychannel
func (c *RedisComponent) CreateEndpoint(uri string) (gocamel.Endpoint, error) {
	u, err := gocamel.ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid Redis URI: %w", err)
	}

	addr := u.Host
	if addr == "" {
		addr = "localhost:6379" // Default Redis address
	}

	// Parse options
	command := gocamel.GetConfigValue(u, "command")
	if command == "" {
		command = "SET" // Default command
	}
	key := gocamel.GetConfigValue(u, "key")
	channel := gocamel.GetConfigValue(u, "channel")

	subscribe := false
	if subStr := gocamel.GetConfigValue(u, "subscribe"); subStr == "true" {
		subscribe = true
	}

	client := redis.NewClient(&redis.Options{
		Addr: addr,
	})

	return &RedisEndpoint{
		uri:       uri,
		client:    client,
		command:   command,
		key:       key,
		channel:   channel,
		subscribe: subscribe,
	}, nil
}

// RedisEndpoint represents a Redis connection endpoint.
type RedisEndpoint struct {
	uri       string
	client    *redis.Client
	command   string
	key       string
	channel   string
	subscribe bool
	consumer  *RedisConsumer
	mu        sync.Mutex
}

// URI returns the endpoint URI.
func (e *RedisEndpoint) URI() string {
	return e.uri
}

// CreateProducer creates a RedisProducer.
func (e *RedisEndpoint) CreateProducer() (gocamel.Producer, error) {
	return &RedisProducer{endpoint: e}, nil
}

// CreateConsumer creates a RedisConsumer.
func (e *RedisEndpoint) CreateConsumer(processor gocamel.Processor) (gocamel.Consumer, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.consumer != nil {
		return nil, fmt.Errorf("multiple consumers for the same Redis endpoint are not allowed: %s", e.uri)
	}

	e.consumer = &RedisConsumer{
		endpoint:  e,
		processor: processor,
	}
	return e.consumer, nil
}

// RedisProducer executes commands on Redis.
type RedisProducer struct {
	endpoint *RedisEndpoint
}

// Start starts the producer.
func (p *RedisProducer) Start(ctx context.Context) error {
	// Ping to verify connection
	return p.endpoint.client.Ping(ctx).Err()
}

// Stop stops the producer and closes the Redis client.
func (p *RedisProducer) Stop() error {
	return p.endpoint.client.Close()
}

// Send executes commands (SET, GET, DEL, PUBLISH) based on parameters or exchange headers.
func (p *RedisProducer) Send(exchange *gocamel.Exchange) error {
	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}

	// Determine command
	cmd := p.endpoint.command
	if hCmd, ok := exchange.GetHeaderAsString(CamelRedisCommand); ok {
		cmd = hCmd
	}
	cmd = strings.ToUpper(cmd)

	// Determine key
	key := p.endpoint.key
	if hKey, ok := exchange.GetHeaderAsString(CamelRedisKey); ok {
		key = hKey
	}

	// Determine channel (specifically for PUBLISH)
	channel := p.endpoint.channel
	if hChannel, ok := exchange.GetHeaderAsString(CamelRedisChannel); ok {
		channel = hChannel
	}

	body := exchange.In.GetBody()
	bodyStr := ""
	if body != nil {
		bodyStr = fmt.Sprintf("%v", body)
	}

	switch cmd {
	case "SET":
		if key == "" {
			return fmt.Errorf("redis: key is required for SET command")
		}
		err := p.endpoint.client.Set(ctx, key, bodyStr, 0).Err()
		if err != nil {
			return err
		}
		exchange.Out.SetBody("OK")

	case "GET":
		if key == "" {
			return fmt.Errorf("redis: key is required for GET command")
		}
		val, err := p.endpoint.client.Get(ctx, key).Result()
		if err != nil {
			if err == redis.Nil {
				exchange.Out.SetBody(nil)
				return nil
			}
			return err
		}
		exchange.Out.SetBody(val)

	case "DEL":
		if key == "" {
			return fmt.Errorf("redis: key is required for DEL command")
		}
		deleted, err := p.endpoint.client.Del(ctx, key).Result()
		if err != nil {
			return err
		}
		exchange.Out.SetBody(deleted)

	case "PUBLISH":
		pubChan := channel
		if pubChan == "" {
			pubChan = key // fallback to key parameter as channel name
		}
		if pubChan == "" {
			return fmt.Errorf("redis: channel or key is required for PUBLISH command")
		}
		receivers, err := p.endpoint.client.Publish(ctx, pubChan, bodyStr).Result()
		if err != nil {
			return err
		}
		exchange.Out.SetBody(receivers)

	default:
		return fmt.Errorf("redis: unsupported command %s", cmd)
	}

	return nil
}

// RedisConsumer subscribes to a Redis Pub/Sub channel.
type RedisConsumer struct {
	endpoint  *RedisEndpoint
	processor gocamel.Processor
	pubSub    *redis.PubSub
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// Start opens a connection, subscribes to the channel, and processes incoming messages.
func (c *RedisConsumer) Start(ctx context.Context) error {
	if !c.endpoint.subscribe {
		return fmt.Errorf("redis consumer: subscribe option must be true to start a consumer")
	}

	channel := c.endpoint.channel
	if channel == "" {
		channel = c.endpoint.key // fallback to key parameter
	}
	if channel == "" {
		return fmt.Errorf("redis consumer: channel or key is required for subscription")
	}

	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	c.pubSub = c.endpoint.client.Subscribe(ctx, channel)

	// Verify subscription
	_, err := c.pubSub.Receive(ctx)
	if err != nil {
		cancel()
		return fmt.Errorf("redis subscription failed: %w", err)
	}

	c.wg.Add(1)
	go c.run(ctx)

	return nil
}

// Stop unsubscribes and closes the pubsub client and Redis connection.
func (c *RedisConsumer) Stop() error {
	if c.cancel != nil {
		c.cancel()
	}
	if c.pubSub != nil {
		_ = c.pubSub.Close()
	}
	c.wg.Wait()
	return c.endpoint.client.Close()
}

func (c *RedisConsumer) run(ctx context.Context) {
	defer c.wg.Done()
	ch := c.pubSub.Channel()

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}

			exchange := gocamel.NewExchange(ctx)
			exchange.In.SetBody(msg.Payload)
			exchange.In.SetHeader(CamelRedisChannel, msg.Channel)

			if err := gocamel.CompleteExchange(exchange, c.processor.Process(exchange)); err != nil {
				gocamel.LogError("redis consumer processing error",
					"uri", c.endpoint.uri,
					"error", err,
					"exchange_id", exchange.ID,
				)
			}
		}
	}
}
