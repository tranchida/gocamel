// Package stomp implements a Camel STOMP component for producing and
// consuming messages with STOMP-compatible brokers such as ActiveMQ,
// RabbitMQ, Apollo or ActiveMQ Artemis.
package stomp

import (
	"bytes"
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
	"sync/atomic"
	"time"

	"github.com/go-stomp/stomp"
	"github.com/go-stomp/stomp/frame"

	gocamel "github.com/tranchida/gocamel"
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

// stompProtocolHeaders are reserved STOMP 1.0/1.1/1.2 frame headers. They are
// never forwarded from arbitrary exchange headers (matching is case-insensitive),
// preventing callers from injecting receipt/transaction/framing directives.
var stompProtocolHeaders = map[string]bool{
	"receipt":        true,
	"transaction":    true,
	"destination":    true,
	"content-length": true,
	"content-type":   true,
	"id":             true,
	"ack":            true,
	"subscription":   true,
	"message-id":     true,
	"receipt-id":     true,
	"host":           true,
	"accept-version": true,
	"version":        true,
	"login":          true,
	"passcode":       true,
	"heart-beat":     true,
	"session":        true,
	"server":         true,
	"message":        true,
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

	// Fall back to URI userinfo / username / password when login/passcode are unset.
	login := gocamel.GetConfigValue(u, "login")
	if login == "" {
		login = gocamel.GetConfigValue(u, "username")
	}
	passcode := gocamel.GetConfigValue(u, "passcode")
	if passcode == "" {
		passcode = gocamel.GetConfigValue(u, "password")
	}

	if strings.ContainsRune(destination, '\x00') {
		return nil, fmt.Errorf("invalid STOMP URI: destination contains null byte")
	}
	if strings.ContainsRune(host, '\x00') {
		return nil, fmt.Errorf("invalid STOMP URI: host contains null byte")
	}
	if strings.ContainsRune(login, '\x00') || strings.ContainsRune(passcode, '\x00') {
		return nil, fmt.Errorf("invalid STOMP URI: credentials contain null byte")
	}

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

	conn      *stomp.Conn
	connUsers int
	consumer  *StompConsumer
	mu        sync.Mutex
}

// closeConn decrements the active user count and closes the endpoint's shared
// STOMP producer connection when the last producer releases it.
//
// Multiple producers created from the same endpoint share one connection, and
// each calls Stop() on shutdown. The mutex serialises the close against
// connection establishment and Send's conn snapshot; reference counting keeps
// the connection open while another producer is still active. Consumers use
// their own dedicated connection so stopping or reconnecting a consumer never
// disrupts or stalls shared producers.
func (e *StompEndpoint) closeConn() {
	e.mu.Lock()
	if e.connUsers > 0 {
		e.connUsers--
	}
	if e.connUsers > 0 || e.conn == nil {
		e.mu.Unlock()
		return
	}
	conn := e.conn
	e.conn = nil
	e.mu.Unlock()

	// MustDisconnect, not Disconnect: a graceful Disconnect waits for the
	// broker's RECEIPT while holding go-stomp's close mutex. If the connection
	// is already broken, the client's read loop reacts by calling
	// MustDisconnect itself and blocks on that same mutex, deadlocking both
	// sides. Many brokers never send a RECEIPT for DISCONNECT either. Closing
	// the TCP connection abruptly is equivalent from the broker's point of
	// view: it requeues unacknowledged messages.
	_ = conn.MustDisconnect()
}

// URI returns the endpoint URI with sensitive credentials masked.
func (e *StompEndpoint) URI() string {
	return gocamel.RedactURI(e.uri)
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
		return nil, fmt.Errorf("multiple consumers for the same STOMP endpoint are not allowed: %s", e.URI())
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
	conn, _, err := e.dialWithTracking(ctx, false)
	return conn, err
}

func (e *StompEndpoint) dialWithTracking(ctx context.Context, trackAcks bool) (*stomp.Conn, *ackTrackingConn, error) {
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
		return nil, nil, fmt.Errorf("stomp connect to %s failed: %w", e.host, err)
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
			return nil, nil, fmt.Errorf("stomp TLS handshake with %s failed: %w", e.host, err)
		}
		netConn = tlsConn
	}

	var tracked *ackTrackingConn
	wireConn := netConn
	if trackAcks {
		tracked = newAckTrackingConn(netConn)
		wireConn = tracked
	}

	conn, err := stomp.Connect(wireConn, opts...)
	if err != nil {
		_ = wireConn.Close()
		return nil, nil, fmt.Errorf("stomp connect to %s failed: %w", e.host, err)
	}
	if !stopAbort() {
		// ctx ended: the abort may already have expired the deadline.
		_ = conn.MustDisconnect()
		return nil, nil, fmt.Errorf("stomp connect to %s aborted: %w", e.host, ctx.Err())
	}
	// The established connection is long-lived: liveness is left to STOMP.
	_ = tcpConn.SetDeadline(time.Time{})
	return conn, tracked, nil
}

const ackFlushWaitTimeout = 250 * time.Millisecond

// ackTrackingConn observes outbound STOMP ACK frames flushed by go-stomp's
// writer goroutine so StompConsumer.Stop can ensure an in-flight ACK hits the
// socket before MustDisconnect closes the connection.
type ackTrackingConn struct {
	net.Conn
	acksExpected atomic.Uint64
	mu           sync.Mutex
	ackWrites    uint64
	closed       bool
	notify       chan struct{}
}

func newAckTrackingConn(c net.Conn) *ackTrackingConn {
	return &ackTrackingConn{
		Conn:   c,
		notify: make(chan struct{}),
	}
}

func (a *ackTrackingConn) noteAck() {
	a.acksExpected.Add(1)
}

func (a *ackTrackingConn) Write(p []byte) (int, error) {
	n, err := a.Conn.Write(p)
	if err == nil && bytes.HasPrefix(p, []byte("ACK\n")) {
		a.mu.Lock()
		a.ackWrites++
		close(a.notify)
		a.notify = make(chan struct{})
		a.mu.Unlock()
	}
	return n, err
}

func (a *ackTrackingConn) Close() error {
	a.mu.Lock()
	if !a.closed {
		a.closed = true
		close(a.notify)
		a.notify = make(chan struct{})
	}
	a.mu.Unlock()
	return a.Conn.Close()
}

func (a *ackTrackingConn) waitAllAcksFlushed(timeout time.Duration) {
	target := a.acksExpected.Load()
	if target == 0 {
		return
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		a.mu.Lock()
		if a.ackWrites >= target || a.closed {
			a.mu.Unlock()
			return
		}
		ch := a.notify
		a.mu.Unlock()

		select {
		case <-ch:
		case <-timer.C:
			return
		}
	}
}

// StompProducer publishes messages to a STOMP destination.
type StompProducer struct {
	endpoint *StompEndpoint
	mu       sync.Mutex
	started  bool
}

// Start starts the producer by opening a STOMP connection.
func (p *StompProducer) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started {
		return nil
	}

	p.endpoint.mu.Lock()
	defer p.endpoint.mu.Unlock()

	if p.endpoint.conn == nil {
		conn, err := p.endpoint.dial(ctx)
		if err != nil {
			return err
		}
		p.endpoint.conn = conn
	}
	p.endpoint.connUsers++
	p.started = true
	return nil
}

// Stop stops the producer by releasing its reference to the shared STOMP connection.
func (p *StompProducer) Stop() error {
	p.mu.Lock()
	if !p.started {
		p.mu.Unlock()
		return nil
	}
	p.started = false
	p.mu.Unlock()

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
	if strings.ContainsRune(destination, '\x00') {
		return "", fmt.Errorf("stomp: invalid destination %q: contains null byte", destination)
	}
	return destination, nil
}

// filterHeaders returns the exchange headers allowed to be forwarded to
// the STOMP broker: Camel-prefixed internal headers, sensitive credential
// headers, and STOMP protocol headers are never forwarded; when an explicit
// forwardHeaders allowlist is configured, only the listed non-blocked headers
// remain. Header names or values containing NUL bytes are rejected to prevent
// STOMP frame truncation.
func (p *StompProducer) filterHeaders(exchange *gocamel.Exchange) ([]func(*frame.Frame) error, error) {
	var opts []func(*frame.Frame) error
	for name, value := range exchange.In.GetHeaders() {
		if gocamel.IsCamelHeader(name) {
			continue
		}
		lower := strings.ToLower(name)
		if sensitiveOutboundHeaders[lower] || stompProtocolHeaders[lower] {
			continue
		}
		if p.endpoint.forwardHeaders != nil && !p.endpoint.forwardHeaders[lower] {
			continue
		}
		valStr := fmt.Sprintf("%v", value)
		if strings.ContainsRune(name, '\x00') || strings.ContainsRune(valStr, '\x00') {
			return nil, fmt.Errorf("stomp: header %q contains null byte", name)
		}
		opts = append(opts, stomp.SendOpt.Header(name, valStr))
	}
	return opts, nil
}

// Send publishes the exchange body to a STOMP destination.
func (p *StompProducer) Send(exchange *gocamel.Exchange) error {
	if exchange == nil {
		return fmt.Errorf("stomp producer: exchange cannot be nil")
	}

	p.mu.Lock()
	started := p.started
	p.mu.Unlock()
	if !started {
		return fmt.Errorf("stomp producer is not started or connected")
	}

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
		if strings.ContainsRune(ct, '\x00') {
			return fmt.Errorf("stomp: invalid content-type %q: contains null byte", ct)
		}
		contentType = ct
	}

	headerOpts, err := p.filterHeaders(exchange)
	if err != nil {
		return err
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

	if err := conn.Send(destination, contentType, payload, headerOpts...); err != nil {
		return fmt.Errorf("stomp send error on destination %q: %w", destination, err)
	}
	return nil
}

// StompConsumer consumes messages from a STOMP destination.
type StompConsumer struct {
	endpoint   *StompEndpoint
	processor  gocamel.Processor
	subMu      sync.Mutex
	sub        subscriptionReader
	mu         sync.Mutex
	transition chan struct{}
	conn       *stomp.Conn
	rawConn    *ackTrackingConn
	started    bool
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

// acquireTransition waits for an in-progress Start/Stop and claims the next
// transition. It never holds c.mu while waiting.
func (c *StompConsumer) acquireTransition() {
	for {
		c.mu.Lock()
		if c.transition != nil {
			done := c.transition
			c.mu.Unlock()
			<-done
			continue
		}
		c.transition = make(chan struct{})
		c.mu.Unlock()
		return
	}
}

// releaseTransition publishes the final state and wakes Start/Stop waiters.
func (c *StompConsumer) releaseTransition() {
	c.mu.Lock()
	close(c.transition)
	c.transition = nil
	c.mu.Unlock()
}

func (c *StompConsumer) subscription() subscriptionReader {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	return c.sub
}

func (c *StompConsumer) setSubscription(sub subscriptionReader) {
	c.subMu.Lock()
	c.sub = sub
	c.subMu.Unlock()
}

// Start opens a dedicated STOMP connection for this consumer, subscribes to
// the destination, and handles incoming messages.
func (c *StompConsumer) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	c.acquireTransition()
	defer c.releaseTransition()

	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	if c.endpoint.destination == "" {
		return fmt.Errorf("stomp consumer: destination is required to subscribe")
	}

	c.endpoint.mu.Lock()
	if c.endpoint.consumer != nil && c.endpoint.consumer != c {
		c.endpoint.mu.Unlock()
		return fmt.Errorf("multiple consumers for the same STOMP endpoint are not allowed: %s", c.endpoint.URI())
	}
	c.endpoint.mu.Unlock()

	conn, rawConn, err := c.endpoint.dialWithTracking(ctx, true)
	if err != nil {
		return err
	}

	sub, err := conn.Subscribe(c.endpoint.destination, c.endpoint.ackMode)
	if err != nil {
		_ = conn.MustDisconnect()
		return fmt.Errorf("stomp subscription to %q failed: %w", c.endpoint.destination, err)
	}

	runCtx, cancel := context.WithCancel(ctx)

	c.endpoint.mu.Lock()
	if c.endpoint.consumer != nil && c.endpoint.consumer != c {
		c.endpoint.mu.Unlock()
		cancel()
		_ = conn.MustDisconnect()
		return fmt.Errorf("multiple consumers for the same STOMP endpoint are not allowed: %s", c.endpoint.URI())
	}
	c.endpoint.consumer = c
	c.endpoint.mu.Unlock()

	c.setSubscription(sub)
	c.mu.Lock()
	c.conn = conn
	c.rawConn = rawConn
	c.cancel = cancel
	c.started = true
	c.wg.Add(1)
	c.mu.Unlock()

	go c.run(runCtx)

	return nil
}

// Stop cancels the consumer loop, waits for any in-flight message processing
// and final ACK to finish, and then closes the consumer's dedicated STOMP
// connection so producers sharing the endpoint are never blocked or disrupted.
func (c *StompConsumer) Stop() error {
	c.acquireTransition()
	defer c.releaseTransition()

	c.mu.Lock()
	if !c.started {
		c.mu.Unlock()
		c.endpoint.mu.Lock()
		if c.endpoint.consumer == c {
			c.endpoint.consumer = nil
		}
		c.endpoint.mu.Unlock()
		return nil
	}
	cancel := c.cancel
	c.cancel = nil
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	// Wait for the run loop (including any completed in-flight Ack) before
	// closing the STOMP connection.
	c.wg.Wait()

	c.mu.Lock()
	conn := c.conn
	rawConn := c.rawConn
	c.conn = nil
	c.rawConn = nil
	c.started = false
	c.mu.Unlock()

	c.setSubscription(nil)

	if rawConn != nil {
		rawConn.waitAllAcksFlushed(ackFlushWaitTimeout)
	}
	if conn != nil {
		_ = conn.MustDisconnect()
	}

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

func (c *StompConsumer) readMessage(ctx context.Context) (*stomp.Message, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	sub := c.subscription()
	if sub == nil {
		return nil, stomp.ErrCompletedSubscription
	}
	if s, ok := sub.(*stomp.Subscription); ok {
		if !s.Active() {
			return nil, stomp.ErrCompletedSubscription
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case msg, ok := <-s.C:
			if !ok {
				return nil, stomp.ErrCompletedSubscription
			}
			if msg != nil && msg.Err != nil {
				return nil, msg.Err
			}
			return msg, nil
		}
	}
	return sub.Read()
}

func (c *StompConsumer) run(ctx context.Context) {
	defer c.wg.Done()

	failures := 0
	for {
		msg, err := c.readMessage(ctx)
		if err == nil && msg == nil {
			continue
		}
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
				"uri", c.endpoint.URI(),
				"destination", c.endpoint.destination,
				"retry_in", delay,
				"error", err,
			)
			if !waitForRetry(ctx, delay) {
				return
			}
			if err := c.reconnect(ctx); err != nil {
				gocamel.LogWarn("stomp reconnect failed",
					"uri", c.endpoint.URI(),
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
			if ctx.Err() != nil {
				return
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
			if msg.Conn == nil {
				gocamel.LogError("stomp ack failed: message has no connection",
					"destination", c.endpoint.destination,
				)
				continue
			}
			// Retrying an ack must not re-run a successful processor.
			const maxAckRetries = 3
			for ackAttempts := 0; ; ackAttempts++ {
				err := msg.Conn.Ack(msg)
				if err == nil {
					c.mu.Lock()
					rc := c.rawConn
					c.mu.Unlock()
					if rc != nil {
						rc.noteAck()
					}
					break
				}
				if errors.Is(err, stomp.ErrClosedUnexpectedly) || errors.Is(err, stomp.ErrAlreadyClosed) {
					// The broker requeues the unacknowledged message; the
					// next Read fails and triggers the reconnection.
					gocamel.LogWarn("stomp ack failed on a closed connection; message will be redelivered",
						"destination", c.endpoint.destination,
						"error", err,
					)
					break
				}
				if errors.Is(err, stomp.ErrNotReceivedMessage) || errors.Is(err, stomp.ErrCompletedSubscription) || ackAttempts >= maxAckRetries {
					gocamel.LogError("stomp ack failed permanently; advancing",
						"destination", c.endpoint.destination,
						"attempts", ackAttempts+1,
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

// reconnect opens a new dedicated STOMP connection for this consumer and
// resubscribes, leaving any shared producer connection on the endpoint
// untouched. The new connection is installed only while ctx is live.
func (c *StompConsumer) reconnect(ctx context.Context) error {
	e := c.endpoint
	conn, rawConn, err := e.dialWithTracking(ctx, true)
	if err != nil {
		return err
	}
	sub, err := conn.Subscribe(e.destination, e.ackMode)
	if err != nil {
		_ = conn.MustDisconnect()
		return fmt.Errorf("stomp subscription to %q failed: %w", e.destination, err)
	}

	c.mu.Lock()
	if ctx.Err() != nil || !c.started {
		c.mu.Unlock()
		_ = conn.MustDisconnect()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("stomp consumer is stopped")
	}
	oldConn := c.conn
	c.conn = conn
	c.rawConn = rawConn
	c.mu.Unlock()

	c.setSubscription(sub)
	if oldConn != nil {
		_ = oldConn.MustDisconnect()
	}
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
