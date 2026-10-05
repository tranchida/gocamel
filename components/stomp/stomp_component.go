// Package stomp implements a Camel STOMP component for producing and
// consuming messages with STOMP-compatible brokers such as ActiveMQ,
// RabbitMQ, Apollo or ActiveMQ Artemis.
package stomp

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-stomp/stomp"
	"github.com/go-stomp/stomp/frame"

	gocamel "gitlab.com/tranchida/gocamel"
)

// Well-known Camel STOMP headers
const (
	// CamelStompDestination is the STOMP destination the message was
	// received from, or the target destination when producing (honored
	// only with allowHeaderOverride=true).
	CamelStompDestination = "CamelStompDestination"
	// CamelStompContentType is the MIME content type sent with the
	// message, or the received message content type.
	CamelStompContentType = "CamelStompContentType"
)

// defaultHost is the broker address used when no host is specified in
// the URI.
const defaultHost = "localhost:61613"

// defaultConnectTimeout bounds connection establishment when the URI sets no
// connectTimeout option.
const defaultConnectTimeout = 30 * time.Second

// sslEnabled reports whether the URI enables SSL/TLS. Both "ssl" and
// "useSSL" are accepted (matching the GoCamel option conventions).
func sslEnabled(u *url.URL) bool {
	for _, key := range []string{"ssl", "useSSL"} {
		if v := gocamel.GetConfigValue(u, key); v != "" {
			if b, err := strconv.ParseBool(v); err == nil {
				return b
			}
		}
	}
	return false
}

// tlsError describes a TLS configuration problem so users can fix the
// URI options without decoding a stack of wrapped x509 errors.
func tlsError(option, path string, err error) error {
	return fmt.Errorf("stomp: invalid TLS configuration for %s (%s): %w", option, path, err)
}

// buildTLSConfig assembles the tls.Config from the endpoint URI options.
// The default configuration verifies the server certificate against the
// system roots with TLS 1.2 as the minimum version — never lower the
// minimum version or skip verification outside controlled environments.
func buildTLSConfig(u *url.URL, host string) (*tls.Config, error) {
	// The default server name is the broker host without the port: the
	// TLS SNI and the certificate DNS names never include a port.
	serverName := host
	if h, _, err := net.SplitHostPort(host); err == nil && h != "" {
		serverName = h
	}

	tlsConfig := &tls.Config{
		ServerName: serverName,
		MinVersion: tls.VersionTLS12,
	}

	if sn := gocamel.GetConfigValue(u, "sslServerName"); sn != "" {
		tlsConfig.ServerName = sn
	}

	if caFile := gocamel.GetConfigValue(u, "sslCAFile"); caFile != "" {
		// Paths come from trusted configuration but are still validated
		// against traversal: a path escaping the process working directory
		// via '..' is rejected the same way other GoCamel path options are.
		if !gocamel.IsSafePath(caFile, false) {
			return nil, tlsError("sslCAFile", caFile, errors.New("unsafe path: directory traversal or null byte"))
		}
		caPEM, err := os.ReadFile(caFile) // #nosec G304 -- trusted endpoint configuration, checked by IsSafePath above
		if err != nil {
			return nil, tlsError("sslCAFile", caFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, tlsError("sslCAFile", caFile, errors.New("no valid PEM certificate found"))
		}
		tlsConfig.RootCAs = pool
	}

	certFile := gocamel.GetConfigValue(u, "sslCertFile")
	keyFile := gocamel.GetConfigValue(u, "sslKeyFile")
	if certFile != "" || keyFile != "" {
		if certFile == "" || keyFile == "" {
			return nil, fmt.Errorf("stomp: sslCertFile and sslKeyFile must both be set for mutual TLS")
		}
		if !gocamel.IsSafePath(certFile, false) {
			return nil, tlsError("sslCertFile", certFile, errors.New("unsafe path: directory traversal or null byte"))
		}
		if !gocamel.IsSafePath(keyFile, false) {
			return nil, tlsError("sslKeyFile", keyFile, errors.New("unsafe path: directory traversal or null byte"))
		}
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, tlsError("sslCertFile", certFile, err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	if v := gocamel.GetConfigValue(u, "sslInsecureSkipVerify"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil && b {
			// Only honoured as an explicit opt-in; a WARN log rather than
			// an error keeps controlled test setups workable.
			gocamel.LogWarn("stomp: sslInsecureSkipVerify=true disables server certificate verification; use only in controlled environments")
			tlsConfig.InsecureSkipVerify = true
		}
	}

	return tlsConfig, nil
}

// sensitiveOutboundHeaders are exchange headers carrying credentials or
// session state. They are never forwarded to the STOMP broker as frame
// headers (matching is case-insensitive), so a producer sitting downstream
// of a consumer does not leak a client's credentials to the broker.
var sensitiveOutboundHeaders = map[string]bool{
	"authorization":       true,
	"cookie":              true,
	"proxy-authorization": true,
	"set-cookie":          true,
	"www-authenticate":    true,
}

// parseForwardHeaders builds the case-insensitive header allowlist from
// the forwardHeaders URI option. It returns nil when the option is unset
// or empty, meaning "forward all non-Camel, non-sensitive headers".
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

// parseAckMode converts a URI ack option into a stomp.AckMode. The
// default is AckAuto ("auto"), matching STOMP semantics where the broker
// considers a message delivered as soon as it is sent.
func parseAckMode(value string) stomp.AckMode {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "client":
		return stomp.AckClient
	case "client-individual":
		return stomp.AckClientIndividual
	default: // "auto" or empty
		return stomp.AckAuto
	}
}

// StompComponent implements the Camel STOMP component. It connects to any
// STOMP 1.0/1.1/1.2 compliant broker (ActiveMQ, ActiveMQ Artemis, RabbitMQ,
// Apollo) using the go-stomp client.
type StompComponent struct{}

// NewStompComponent creates a new StompComponent instance.
func NewStompComponent() *StompComponent {
	return &StompComponent{}
}

// CreateEndpoint creates a new StompEndpoint from a URI.
// Example URIs:
//
//	stomp:queue:orders?host=localhost:61613
//	stomp://topic/prices?host=activemq.internal:61613
//	stomp:queue:billing?host=broker:61613&login=admin&passcode=admin
//	stomp:queue:work?host=broker:61613&ack=client-individual
//
// Options:
//
//	host                 Broker address host:port (default: localhost:61613)
//	destination          Default destination (otherwise taken from host + path
//	                     or the opaque part of the URI)
//	login                Login used in the STOMP CONNECT frame
//	passcode             Passcode used in the STOMP CONNECT frame
//	ack                  Acknowledgement mode: auto, client, client-individual
//	                     (consumer only, default: auto)
//	allowHeaderOverride true to let the CamelStompDestination header replace
//	                     the configured destination (default: false)
//	forwardHeaders       Comma-separated allowlist of exchange headers the
//	                     producer forwards as STOMP frame headers. When set,
//	                     ONLY these headers are propagated. When unset, all
//	                     non-Camel headers except sensitive credentials
//	                     (Authorization, Cookie, Proxy-Authorization, Set-Cookie,
//	                     WWW-Authenticate) are forwarded.
//	ssl                  true to connect over TLS (default: false)
//	sslCAFile            PEM file of the certificate authority verifying the
//	                     broker certificate (default: system roots)
//	sslCertFile          PEM file of the client certificate (requires sslKeyFile)
//	sslKeyFile           PEM file of the client private key (requires sslCertFile)
//	sslServerName        Server name used for SNI and certificate verification
//	                     (default: the host option)
//	sslInsecureSkipVerify true to skip broker certificate verification
//	                     (default: false — controlled environments only)
//	maxRedeliveries      Redeliveries of a message whose route failed before
//	                     it is acknowledged anyway (default: 10; -1 retries
//	                     forever, 0 acknowledges a failed message right away)
//	redeliveryDelay      First redelivery delay in ms, doubled on each attempt
//	                     (default: 100)
//	maxRedeliveryDelay   Cap of the redelivery backoff in ms (default: 30000)
//	connectTimeout       Timeout of the TCP connect plus the TLS and STOMP
//	                     handshakes, as a Go duration (default: 30s)
//
// The consumer reconnects and resubscribes, with a bounded backoff, when the
// broker connection is lost or the broker sends an ERROR frame.
//
// A message the route dead-lettered (DeadLetterChannel) is acknowledged, never
// redelivered. Inbound frame headers named Camel* are dropped: a client
// sending to the destination must not drive internal headers such as
// CamelFileName.
func (c *StompComponent) CreateEndpoint(uri string) (gocamel.Endpoint, error) {
	u, err := gocamel.ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid STOMP URI: %w", err)
	}

	// Destination identification: host + path, then opaque part, then
	// explicit destination option. "stomp://queue/orders" yields the
	// "/queue/orders" destination. STOMP destinations are rooted paths
	// ("/queue/...", "/topic/..."), so a missing leading slash is added.
	destination := u.Host + u.Path
	if destination == "" {
		destination = u.Opaque
	}
	if destination == "" {
		destination = gocamel.GetConfigValue(u, "destination")
	}
	if destination != "" && !strings.HasPrefix(destination, "/") {
		destination = "/" + destination
	}

	// The broker address always comes from the host option: the URI
	// host/path identifies the destination, not the broker (matching
	// the NATS servers / Kafka brokers option conventions).
	host := gocamel.GetConfigValue(u, "host")
	if host == "" {
		host = defaultHost
	}

	// GetConfigValue falls back to URI userinfo for the well-known
	// credential keys, so stomp://user:pass@broker:61613 works too.
	login := gocamel.GetConfigValue(u, "login")
	passcode := gocamel.GetConfigValue(u, "passcode")

	ackMode := parseAckMode(gocamel.GetConfigValue(u, "ack"))

	allowHeaderOverride := false
	if v := gocamel.GetConfigValue(u, "allowHeaderOverride"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			allowHeaderOverride = b
		}
	}

	// TLS configuration is validated at endpoint creation, not at first
	// dial: a missing CA file or an unreadable key pair surfaces
	// immediately in CreateEndpoint rather than at route Start.
	var tlsConfig *tls.Config
	if sslEnabled(u) {
		tlsConfig, err = buildTLSConfig(u, host)
		if err != nil {
			return nil, err
		}
	}

	return &StompEndpoint{
		uri:                 uri,
		host:                host,
		destination:         destination,
		login:               login,
		passcode:            passcode,
		ackMode:             ackMode,
		allowHeaderOverride: allowHeaderOverride,
		forwardHeaders:      parseForwardHeaders(gocamel.GetConfigValue(u, "forwardHeaders")),
		redelivery:          gocamel.ParseConsumerRedeliveryPolicy(u),
		tlsConfig:           tlsConfig,
		connectTimeout:      parseConnectTimeout(u),
	}, nil
}

// parseConnectTimeout reads the connectTimeout option with the shared GoCamel
// parser, keeping the STOMP default when the option is unset.
func parseConnectTimeout(u *url.URL) time.Duration {
	if gocamel.GetConfigValue(u, "connectTimeout") == "" {
		return defaultConnectTimeout
	}
	return gocamel.ParseConnectTimeout(u)
}

// StompEndpoint represents a STOMP destination endpoint.
type StompEndpoint struct {
	uri                 string
	host                string
	destination         string
	login               string
	passcode            string
	ackMode             stomp.AckMode
	allowHeaderOverride bool // opt-in: the CamelStompDestination header may override the destination
	// forwardHeaders is the optional header allowlist: when non-nil, ONLY the
	// listed headers are forwarded to the broker; when nil, every non-Camel,
	// non-sensitive header is forwarded.
	forwardHeaders map[string]bool
	// redelivery bounds how a failed message is re-run before it is
	// acknowledged (consumer side).
	redelivery gocamel.ConsumerRedeliveryPolicy

	// tlsConfig is non-nil when ssl=true: the connection is established over
	// TLS using this configuration.
	tlsConfig *tls.Config
	// connectTimeout bounds the TCP connect plus the TLS and STOMP handshakes.
	connectTimeout time.Duration

	conn     *stomp.Conn
	consumer *StompConsumer
	mu       sync.Mutex
}

// closeConn closes the endpoint's shared STOMP connection.
//
// The producer and the consumer created from the same endpoint may share
// one connection, and both call Stop() on shutdown. The mutex serialises
// the close against connection establishment and Send's conn snapshot;
// nil-ing the reference keeps the teardown idempotent.
func (e *StompEndpoint) closeConn() {
	e.mu.Lock()
	conn := e.conn
	e.conn = nil
	e.mu.Unlock()

	if conn != nil {
		_ = conn.Disconnect()
	}
}

// URI returns the endpoint URI.
func (e *StompEndpoint) URI() string {
	return e.uri
}

// CreateProducer creates a StompProducer.
func (e *StompEndpoint) CreateProducer() (gocamel.Producer, error) {
	return &StompProducer{endpoint: e}, nil
}

// CreateConsumer creates a StompConsumer.
func (e *StompEndpoint) CreateConsumer(processor gocamel.Processor) (gocamel.Consumer, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.consumer != nil {
		return nil, fmt.Errorf("multiple consumers for the same STOMP endpoint are not allowed: %s", e.uri)
	}

	e.consumer = &StompConsumer{
		endpoint:  e,
		processor: processor,
	}
	return e.consumer, nil
}

// dial establishes a STOMP connection to the broker, over plain TCP or
// TLS depending on the ssl option. Connection establishment is bounded by
// connectTimeout and aborted when ctx ends.
func (e *StompEndpoint) dial(ctx context.Context) (*stomp.Conn, error) {
	var opts []func(*stomp.Conn) error
	if e.login != "" || e.passcode != "" {
		opts = append(opts, stomp.ConnOpt.Login(e.login, e.passcode))
	}

	// go-stomp has no native TLS dialer, but stomp.Connect accepts any
	// established io.ReadWriteCloser: dial the TCP or TLS connection
	// first, then run the STOMP handshake over it.
	dialer := &net.Dialer{Timeout: e.connectTimeout}
	tcpConn, err := dialer.DialContext(ctx, "tcp", e.host)
	if err != nil {
		return nil, fmt.Errorf("stomp connect to %s failed: %w", e.host, err)
	}

	// A broker accepting the TCP connection but never answering must not
	// block the handshakes (and the endpoint lock held by Start) forever.
	if e.connectTimeout > 0 {
		_ = tcpConn.SetDeadline(time.Now().Add(e.connectTimeout))
	}
	stopAbort := context.AfterFunc(ctx, func() { _ = tcpConn.SetDeadline(time.Unix(1, 0)) })
	defer stopAbort()

	netConn := tcpConn
	if e.tlsConfig != nil {
		// tls.Client performs the handshake lazily on first Read/Write in
		// Go < 1.24; handshake immediately so certificate errors surface in
		// dial rather than during the first STOMP frame exchange.
		tlsConn := tls.Client(netConn, e.tlsConfig.Clone())
		if err := tlsConn.Handshake(); err != nil {
			_ = netConn.Close()
			return nil, fmt.Errorf("stomp TLS handshake with %s failed: %w", e.host, err)
		}
		netConn = tlsConn
	}

	conn, err := stomp.Connect(netConn, opts...)
	if err != nil {
		_ = netConn.Close()
		return nil, fmt.Errorf("stomp connect to %s failed: %w", e.host, err)
	}
	if !stopAbort() {
		// ctx ended: the abort may already have expired the deadline.
		_ = conn.MustDisconnect()
		return nil, fmt.Errorf("stomp connect to %s aborted: %w", e.host, ctx.Err())
	}
	// The established connection is long-lived: liveness is left to STOMP.
	_ = tcpConn.SetDeadline(time.Time{})
	return conn, nil
}

// StompProducer publishes messages to a STOMP destination.
type StompProducer struct {
	endpoint *StompEndpoint
}

// Start starts the producer by opening a STOMP connection.
func (p *StompProducer) Start(ctx context.Context) error {
	p.endpoint.mu.Lock()
	defer p.endpoint.mu.Unlock()

	if p.endpoint.conn != nil {
		return nil
	}

	conn, err := p.endpoint.dial(ctx)
	if err != nil {
		return err
	}
	p.endpoint.conn = conn
	return nil
}

// Stop stops the producer by closing the shared STOMP connection.
func (p *StompProducer) Stop() error {
	p.endpoint.closeConn()
	return nil
}

// resolveDestination returns the destination to publish to.
// The CamelStompDestination header is honoured only when the endpoint opts
// in with allowHeaderOverride=true: headers can be influenced by untrusted
// input and would otherwise let a caller publish to arbitrary destinations.
func (p *StompProducer) resolveDestination(exchange *gocamel.Exchange) (string, error) {
	destination := p.endpoint.destination
	if p.endpoint.allowHeaderOverride {
		if hDest, ok := exchange.GetHeaderAsString(CamelStompDestination); ok {
			if hDest == "" {
				return "", fmt.Errorf("stomp: invalid destination %q in %s header: must be non-empty", hDest, CamelStompDestination)
			}
			destination = hDest
		}
	}
	if destination == "" {
		return "", fmt.Errorf("stomp: destination is required to publish a message")
	}
	return destination, nil
}

// filterHeaders returns the exchange headers allowed to be forwarded to
// the STOMP broker: Camel-prefixed internal headers and sensitive
// credential headers are never forwarded; when an explicit forwardHeaders
// allowlist is configured, only the listed headers remain (sensitive ones
// stay blocked).
func (p *StompProducer) filterHeaders(exchange *gocamel.Exchange) []func(*frame.Frame) error {
	var opts []func(*frame.Frame) error
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
		opts = append(opts, stomp.SendOpt.Header(name, fmt.Sprintf("%v", value)))
	}
	return opts
}

// Send publishes the exchange body to a STOMP destination.
func (p *StompProducer) Send(exchange *gocamel.Exchange) error {
	p.endpoint.mu.Lock()
	conn := p.endpoint.conn
	p.endpoint.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("stomp producer is not started or connected")
	}

	destination, err := p.resolveDestination(exchange)
	if err != nil {
		return err
	}

	contentType := ""
	if ct, ok := exchange.GetHeaderAsString(CamelStompContentType); ok {
		contentType = ct
	}

	body := exchange.In.GetBody()
	var payload []byte
	switch b := body.(type) {
	case []byte:
		payload = b
	case string:
		payload = []byte(b)
	case nil:
		payload = []byte{}
	default:
		payload = []byte(fmt.Sprintf("%v", b))
	}

	if err := conn.Send(destination, contentType, payload, p.filterHeaders(exchange)...); err != nil {
		return fmt.Errorf("stomp send error on destination %q: %w", destination, err)
	}
	return nil
}

// StompConsumer consumes messages from a STOMP destination.
type StompConsumer struct {
	endpoint  *StompEndpoint
	processor gocamel.Processor
	sub       subscriptionReader
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// Start opens the STOMP connection, subscribes to the destination and
// handles incoming messages.
func (c *StompConsumer) Start(ctx context.Context) error {
	c.endpoint.mu.Lock()
	defer c.endpoint.mu.Unlock()

	if c.endpoint.destination == "" {
		return fmt.Errorf("stomp consumer: destination is required to subscribe")
	}

	if c.endpoint.conn == nil {
		conn, err := c.endpoint.dial(ctx)
		if err != nil {
			return err
		}
		c.endpoint.conn = conn
	}

	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	sub, err := c.endpoint.conn.Subscribe(c.endpoint.destination, c.endpoint.ackMode)
	if err != nil {
		cancel()
		return fmt.Errorf("stomp subscription to %q failed: %w", c.endpoint.destination, err)
	}
	c.sub = sub

	c.wg.Add(1)
	go c.run(ctx)

	return nil
}

// Stop releases the endpoint's shared STOMP connection.
//
// The go-stomp Unsubscribe blocks until the broker returns a RECEIPT for
// the UNSUBSCRIBE frame, which not all brokers send reliably (the embedded
// test broker never does). Instead the connection is disconnected first:
// the resulting TCP close unblocks the run loop's Read, and the broker
// requeues any unacknowledged message, which is the correct STOMP
// behavior on abrupt disconnect.
func (c *StompConsumer) Stop() error {
	if c.cancel != nil {
		c.cancel()
	}

	// Closing the connection unblocks the run loop's Read (the
	// subscription channel closes with ErrCompletedSubscription) before
	// wg.Wait.
	c.endpoint.closeConn()
	c.wg.Wait()

	c.endpoint.mu.Lock()
	if c.endpoint.consumer == c {
		c.endpoint.consumer = nil
	}
	c.endpoint.mu.Unlock()
	return nil
}

// subscriptionReader abstracts the subscription read contract so consumer
// delivery and ack ordering can be verified without a broker. The
// go-stomp Subscription implements this contract.
type subscriptionReader interface {
	Read() (*stomp.Message, error)
}

func (c *StompConsumer) run(ctx context.Context) {
	defer c.wg.Done()

	failures := 0
	for {
		msg, err := c.sub.Read()
		// Server-side ERROR frames surface as a read error or as a message
		// with Err set; either way the broker closed the connection.
		if err == nil && msg.Err != nil {
			err = msg.Err
		}
		if err != nil {
			// Stop closed the connection to unblock Read: exit normally.
			if ctx.Err() != nil {
				return
			}
			// Connection lost or broker ERROR: returning would leave the
			// route started with no consumer, so reconnect until stopped.
			delay := retryDelay(failures)
			failures++
			gocamel.LogWarn("stomp subscription failed; reconnecting",
				"uri", c.endpoint.uri,
				"destination", c.endpoint.destination,
				"retry_in", delay,
				"error", err,
			)
			if !waitForRetry(ctx, delay) {
				return
			}
			if err := c.reconnect(ctx); err != nil {
				gocamel.LogWarn("stomp reconnect failed",
					"uri", c.endpoint.uri,
					"error", err,
				)
			}
			continue
		}
		failures = 0

		// Resolve this delivery before reading another message. Acknowledging
		// a later message would otherwise discard a failed one under the
		// client (cumulative) ack mode.
		for redeliveries := 0; ; redeliveries++ {
			if ctx.Err() != nil {
				return
			}
			exchange := gocamel.NewExchange(ctx)
			exchange.In.SetBody(append([]byte(nil), msg.Body...))
			exchange.In.SetHeader(CamelStompDestination, msg.Destination)
			if msg.ContentType != "" {
				exchange.In.SetHeader(CamelStompContentType, msg.ContentType)
			}
			for i := 0; i < msg.Header.Len(); i++ {
				key, value := msg.Header.GetAt(i)
				// Protocol-level headers that duplicate the exchange
				// metadata are not propagated as exchange headers.
				if key == "destination" || key == "content-type" || key == "content-length" ||
					key == "message-id" || key == "subscription" || key == "ack" {
					continue
				}
				// A client sending to the destination must not set
				// internal Camel* headers.
				if gocamel.IsCamelHeader(key) {
					continue
				}
				exchange.In.SetHeader(key, value)
			}
			err := gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange))
			if err == nil {
				break
			}
			if gocamel.IsDeadLettered(exchange) {
				// Redelivering would dead-letter a duplicate on every attempt.
				gocamel.LogWarn("stomp message dead-lettered; acknowledging",
					"destination", c.endpoint.destination,
					"error", err,
					"exchange_id", exchange.ID,
				)
				break
			}
			if c.endpoint.redelivery.Exhausted(redeliveries) {
				gocamel.LogError("stomp message failed; redeliveries exhausted, acknowledging",
					"destination", c.endpoint.destination,
					"redeliveries", redeliveries,
					"error", err,
					"exchange_id", exchange.ID,
				)
				break
			}
			gocamel.LogError("stomp processing failed; retrying before acknowledging",
				"destination", c.endpoint.destination,
				"redelivery", redeliveries+1,
				"retry_in", c.endpoint.redelivery.Backoff(redeliveries),
				"error", err,
				"exchange_id", exchange.ID,
			)
			if !c.endpoint.redelivery.Wait(ctx, redeliveries) {
				return
			}
		}

		if msg.ShouldAck() {
			// Retrying an ack must not re-run a successful processor.
			for {
				err := msg.Conn.Ack(msg)
				if err == nil {
					break
				}
				if errors.Is(err, stomp.ErrClosedUnexpectedly) {
					// The broker requeues the unacknowledged message; the
					// next Read fails and triggers the reconnection.
					gocamel.LogWarn("stomp ack failed on a closed connection; message will be redelivered",
						"destination", c.endpoint.destination,
						"error", err,
					)
					break
				}
				gocamel.LogError("stomp ack failed; retrying before advancing",
					"destination", c.endpoint.destination,
					"error", err,
				)
				if !waitForRetry(ctx, ackRetryDelay) {
					return
				}
			}
		}
	}
}

// reconnect replaces the endpoint's shared connection with a new one and
// resubscribes. The new connection is installed only while ctx is live:
// Stop cancels ctx before closing the endpoint connection, so a connection
// installed here is always closed by Stop.
func (c *StompConsumer) reconnect(ctx context.Context) error {
	e := c.endpoint
	conn, err := e.dial(ctx)
	if err != nil {
		return err
	}
	sub, err := conn.Subscribe(e.destination, e.ackMode)
	if err != nil {
		_ = conn.MustDisconnect()
		return fmt.Errorf("stomp subscription to %q failed: %w", e.destination, err)
	}

	e.mu.Lock()
	if ctx.Err() != nil {
		e.mu.Unlock()
		_ = conn.MustDisconnect()
		return ctx.Err()
	}
	old := e.conn
	e.conn = conn
	e.mu.Unlock()

	if old != nil {
		// The old connection is broken: a graceful Disconnect could wait
		// forever for a RECEIPT.
		_ = old.MustDisconnect()
	}
	c.sub = sub
	return nil
}

// Delays between attempts to recover from a failure.
const (
	ackRetryDelay = 100 * time.Millisecond
	minRetryDelay = 100 * time.Millisecond
	maxRetryDelay = 5 * time.Second
)

// retryDelay returns the exponential backoff before the given reconnection
// attempt.
func retryDelay(attempt int) time.Duration {
	delay := minRetryDelay
	for i := 0; i < attempt && delay < maxRetryDelay; i++ {
		delay *= 2
	}
	return min(delay, maxRetryDelay)
}

// waitForRetry sleeps for delay and reports false if ctx ended first.
func waitForRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
