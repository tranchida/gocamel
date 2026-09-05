package redis

import (
	"context"
	"fmt"
	"strconv"
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
type RedisComponent struct{}

// NewRedisComponent creates a new RedisComponent instance.
func NewRedisComponent() *RedisComponent {
	return &RedisComponent{}
}

// CreateEndpoint creates a new RedisEndpoint from a URI.
// Example URIs:
//
//	redis://localhost:6379?command=SET&key=mykey
//	redis://localhost:6379?subscribe=true&channel=mychannel
//
// Options:
//
//	command             Default command (SET, GET, DEL, PUBLISH) (default: SET)
//	key                 Default Redis key
//	channel             Default Pub/Sub channel
//	subscribe           true to create a consumer endpoint (default: false)
//	allowHeaderOverride true to let the CamelRedisCommand, CamelRedisKey and
//	                    CamelRedisChannel headers replace the configured
//	                    command, key and channel (default: false)
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

	allowHeaderOverride := false
	if v := gocamel.GetConfigValue(u, "allowHeaderOverride"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			allowHeaderOverride = b
		}
	}

	options := &redis.Options{Addr: addr}

	return &RedisEndpoint{
		uri:                 uri,
		options:             options,
		command:             command,
		key:                 key,
		channel:             channel,
		subscribe:           subscribe,
		allowHeaderOverride: allowHeaderOverride,
	}, nil
}

// RedisEndpoint represents a Redis connection endpoint.
type RedisEndpoint struct {
	uri                 string
	options             *redis.Options
	connection          *redisConnection // guarded by mu
	command             string
	key                 string
	channel             string
	subscribe           bool
	allowHeaderOverride bool // opt-in: CamelRedisCommand/CamelRedisKey/CamelRedisChannel headers may override URI settings
	consumer            *RedisConsumer
	mu                  sync.Mutex // guards consumer and connection ownership
}

// redisConnection belongs to one endpoint lifecycle. Reference counting keeps
// one owner from closing the client while another producer/consumer uses it.
type redisConnection struct {
	client    *redis.Client
	users     int // guarded by RedisEndpoint.mu
	closeOnce sync.Once
	closeErr  error
}

func (e *RedisEndpoint) acquireClient() *redisConnection {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.connection == nil {
		e.connection = &redisConnection{client: redis.NewClient(e.options)}
	}
	e.connection.users++
	return e.connection
}

func (e *RedisEndpoint) releaseClient(conn *redisConnection) error {
	if conn == nil {
		return nil
	}
	e.mu.Lock()
	conn.users--
	last := conn.users == 0
	if last && e.connection == conn {
		e.connection = nil
	}
	e.mu.Unlock()
	if last {
		conn.closeOnce.Do(func() { conn.closeErr = conn.client.Close() })
		return conn.closeErr
	}
	return nil
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
	endpoint   *RedisEndpoint
	mu         sync.Mutex // protects connection
	connection *redisConnection
}

// Start starts the producer.
func (p *RedisProducer) Start(ctx context.Context) error {
	p.mu.Lock()
	if p.connection != nil {
		p.mu.Unlock()
		return nil
	}
	conn := p.endpoint.acquireClient()
	p.connection = conn
	p.mu.Unlock()
	if err := conn.client.Ping(ctx).Err(); err != nil {
		_ = p.Stop()
		return err
	}
	return nil
}

// Stop releases this producer's ownership, closing the last owner's client.
func (p *RedisProducer) Stop() error {
	p.mu.Lock()
	conn := p.connection
	p.connection = nil
	p.mu.Unlock()
	return p.endpoint.releaseClient(conn)
}

// Send executes commands (SET, GET, DEL, PUBLISH) based on parameters or exchange headers.
//
// The CamelRedisCommand, CamelRedisKey and CamelRedisChannel headers replace
// the configured settings wholesale, so they are honoured only when the
// endpoint opts in with allowHeaderOverride=true. Enable it only where
// headers cannot be influenced by untrusted input: any component that maps
// external metadata onto headers would otherwise hand an attacker control of
// the target key, channel and command.
func (p *RedisProducer) Send(exchange *gocamel.Exchange) error {
	p.mu.Lock()
	conn := p.connection
	p.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("redis producer is not started")
	}
	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}

	// Determine command (header override honoured only when opted in)
	cmd := p.endpoint.command
	if p.endpoint.allowHeaderOverride {
		if hCmd, ok := exchange.GetHeaderAsString(CamelRedisCommand); ok {
			cmd = hCmd
		}
	}
	cmd = strings.ToUpper(cmd)

	// Determine key (header override honoured only when opted in)
	key := p.endpoint.key
	if p.endpoint.allowHeaderOverride {
		if hKey, ok := exchange.GetHeaderAsString(CamelRedisKey); ok {
			key = hKey
		}
	}

	// Determine channel specifically for PUBLISH (header override honoured
	// only when opted in)
	channel := p.endpoint.channel
	if p.endpoint.allowHeaderOverride {
		if hChannel, ok := exchange.GetHeaderAsString(CamelRedisChannel); ok {
			channel = hChannel
		}
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
		err := conn.client.Set(ctx, key, bodyStr, 0).Err()
		if err != nil {
			return err
		}
		exchange.Out.SetBody("OK")

	case "GET":
		if key == "" {
			return fmt.Errorf("redis: key is required for GET command")
		}
		val, err := conn.client.Get(ctx, key).Result()
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
		deleted, err := conn.client.Del(ctx, key).Result()
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
		receivers, err := conn.client.Publish(ctx, pubChan, bodyStr).Result()
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
	endpoint   *RedisEndpoint
	processor  gocamel.Processor
	pubSub     *redis.PubSub
	connection *redisConnection
	stopOnce   sync.Once
	stopErr    error
	cancel     context.CancelFunc
	wg         sync.WaitGroup
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

	c.connection = c.endpoint.acquireClient()
	c.pubSub = c.connection.client.Subscribe(ctx, channel)

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
	c.stopOnce.Do(func() {
		if c.cancel != nil {
			c.cancel()
		}
		if c.pubSub != nil {
			_ = c.pubSub.Close()
		}
		c.wg.Wait()
		c.stopErr = c.endpoint.releaseClient(c.connection)
		c.endpoint.mu.Lock()
		if c.endpoint.consumer == c {
			c.endpoint.consumer = nil
		}
		c.endpoint.mu.Unlock()
	})
	return c.stopErr
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

			if err := gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange)); err != nil {
				gocamel.LogError("redis consumer processing error",
					"uri", c.endpoint.uri,
					"error", err,
					"exchange_id", exchange.ID,
				)
			}
		}
	}
}
