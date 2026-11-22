package nats

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	gocamel "github.com/tranchida/gocamel"
)

// NATS Component constants
const (
	CamelNATSSubject = "CamelNATSSubject"
)

// NATSComponent represents the Camel NATS component.
type NATSComponent struct{}

// NewNATSComponent creates a new NATSComponent instance.
func NewNATSComponent() *NATSComponent {
	return &NATSComponent{}
}

// CreateEndpoint creates a new NATSEndpoint from a URI.
// Example URIs:
//
//	nats:news?servers=nats://localhost:4222
//	nats:orders
//
// Options:
//
//	subject             Default subject (otherwise taken from host + path)
//	servers             Comma-separated NATS server URLs (default: nats.DefaultURL)
//	allowHeaderOverride true to let the CamelNATSSubject header replace the
//	                    configured subject (default: false — see NATSProducer.Send)
func (c *NATSComponent) CreateEndpoint(uri string) (gocamel.Endpoint, error) {
	u, err := gocamel.ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid NATS URI: %w", err)
	}

	// SEDA-like subject identification: host + path.
	subject := u.Host + u.Path
	if subject == "" {
		subject = u.Opaque
	}
	if subject == "" {
		subject = gocamel.GetConfigValue(u, "subject")
	}

	servers := gocamel.GetConfigValue(u, "servers")
	if servers == "" {
		servers = nats.DefaultURL // "nats://127.0.0.1:4222"
	}

	allowHeaderOverride := false
	if v := gocamel.GetConfigValue(u, "allowHeaderOverride"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			allowHeaderOverride = b
		}
	}

	return &NATSEndpoint{
		uri:                 uri,
		servers:             servers,
		subject:             subject,
		allowHeaderOverride: allowHeaderOverride,
	}, nil
}

// NATSEndpoint represents a NATS endpoint.
type NATSEndpoint struct {
	uri                 string
	servers             string
	subject             string
	allowHeaderOverride bool // opt-in: the CamelNATSSubject header may override the subject
	conn                *nats.Conn
	connUsers           int
	consumer            *NATSConsumer
	mu                  sync.Mutex
}

// closeConn decrements the active user count and closes the endpoint's shared
// NATS connection when the last user releases it.
func (e *NATSEndpoint) closeConn() {
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

	conn.Close()
}

// acquireConn returns the shared live NATS connection or connects outside e.mu
// so slow or stalled brokers never block Send, Stop, or concurrent callers.
func (e *NATSEndpoint) acquireConn(ctx context.Context) (*nats.Conn, error) {
	e.mu.Lock()
	if conn := e.conn; conn != nil && !conn.IsClosed() {
		e.connUsers++
		e.mu.Unlock()
		return conn, nil
	}
	e.mu.Unlock()

	newConn, err := connectNATS(ctx, e.servers)
	if err != nil {
		return nil, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.conn != nil && !e.conn.IsClosed() {
		// Another caller established a shared connection concurrently.
		newConn.Close()
		e.connUsers++
		return e.conn, nil
	}
	e.conn = newConn
	e.connUsers++
	return newConn, nil
}

// contextDialer bridges nats.CustomDialer to net.Dialer.DialContext and tracks
// dialed sockets so an in-progress NATS handshake can be aborted if ctx ends.
// Once initial connect succeeds, complete() detaches ctx and clears conns so
// subsequent nats.go reconnect dials use context.Background() (bounded by
// dialer.Timeout) and do not accumulate sockets.
type contextDialer struct {
	ctx    context.Context
	dialer net.Dialer

	mu        sync.Mutex
	completed bool
	aborted   bool
	conns     []net.Conn
}

func (d *contextDialer) Dial(network, address string) (net.Conn, error) {
	d.mu.Lock()
	completed := d.completed
	aborted := d.aborted
	d.mu.Unlock()

	if aborted {
		if err := d.ctx.Err(); err != nil {
			return nil, err
		}
		return nil, context.Canceled
	}

	dialCtx := d.ctx
	if completed {
		dialCtx = context.Background()
	}

	conn, err := d.dialer.DialContext(dialCtx, network, address)
	if err != nil {
		return nil, err
	}

	if completed {
		return conn, nil
	}

	d.mu.Lock()
	if d.aborted || (!d.completed && d.ctx.Err() != nil) {
		d.mu.Unlock()
		_ = conn.Close()
		if err := d.ctx.Err(); err != nil {
			return nil, err
		}
		return nil, context.Canceled
	}
	if !d.completed {
		d.conns = append(d.conns, conn)
	}
	d.mu.Unlock()
	return conn, nil
}

func (d *contextDialer) complete() {
	d.mu.Lock()
	d.completed = true
	d.conns = nil
	d.mu.Unlock()
}

func (d *contextDialer) abort() {
	d.mu.Lock()
	if d.completed {
		d.mu.Unlock()
		return
	}
	d.aborted = true
	conns := d.conns
	d.conns = nil
	d.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

func connectNATS(ctx context.Context, servers string) (*nats.Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dialer := &contextDialer{
		ctx:    ctx,
		dialer: net.Dialer{Timeout: nats.DefaultTimeout},
	}
	stopAbort := context.AfterFunc(ctx, dialer.abort)
	conn, err := nats.Connect(servers, nats.SetCustomDialer(dialer))
	if !stopAbort() || ctx.Err() != nil {
		dialer.abort()
		if conn != nil {
			conn.Close()
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ctx.Err(), err)
		}
		return nil, ctx.Err()
	}
	if err != nil {
		dialer.abort()
		return nil, err
	}
	dialer.complete()
	return conn, nil
}

// URI returns the endpoint URI with sensitive parameters masked, including
// user:pass@ and token@ credentials embedded in the servers query parameter.
func (e *NATSEndpoint) URI() string {
	return redactNATSURI(e.uri)
}

var natsServersQueryEscaper = strings.NewReplacer(
	"&", "%26",
	";", "%3B",
	"+", "%2B",
	"#", "%23",
	" ", "%20",
)

func redactNATSURI(rawURI string) string {
	redacted := gocamel.RedactURI(rawURI)
	u, err := url.Parse(redacted)
	if err != nil {
		rest, fragment, hasFragment := strings.Cut(redacted, "#")
		base, query, hasQuery := strings.Cut(rest, "?")
		if !hasQuery {
			return redacted
		}
		if maskedQuery, changed := redactRawServersQuery(query); changed {
			out := base + "?" + maskedQuery
			if hasFragment {
				out += "#" + fragment
			}
			return out
		}
		return redacted
	}
	changed := false
	if u.User != nil {
		if _, hasPass := u.User.Password(); !hasPass && u.User.Username() != "" {
			u.User = url.User("xxxxx")
			changed = true
		}
	}
	if maskedQuery, queryChanged := redactRawServersQuery(u.RawQuery); queryChanged {
		u.RawQuery = maskedQuery
		changed = true
	}
	if changed {
		return u.String()
	}
	return redacted
}

func redactRawServersQuery(rawQuery string) (string, bool) {
	if rawQuery == "" {
		return rawQuery, false
	}
	changed := false
	parts := strings.Split(rawQuery, "&")
	for i, part := range parts {
		subParts := strings.Split(part, ";")
		subChanged := false
		for j, sub := range subParts {
			name, val, hasEq := strings.Cut(sub, "=")
			if !hasEq || !strings.EqualFold(name, "servers") {
				continue
			}
			decoded := val
			if unescaped, err := url.QueryUnescape(val); err == nil {
				decoded = unescaped
			}
			if masked, ok := redactNATSServersList(decoded); ok {
				subParts[j] = name + "=" + natsServersQueryEscaper.Replace(masked)
				subChanged = true
			}
		}
		if subChanged {
			parts[i] = strings.Join(subParts, ";")
			changed = true
		}
	}
	if !changed {
		return rawQuery, false
	}
	return strings.Join(parts, "&"), true
}

func redactNATSServersList(servers string) (string, bool) {
	parts := strings.Split(servers, ",")
	changed := false
	for i, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		if masked, ok := redactNATSServerURL(trimmed); ok {
			leading := part[:len(part)-len(strings.TrimLeft(part, " \t"))]
			trailing := part[len(strings.TrimRight(part, " \t")):]
			parts[i] = leading + masked + trailing
			changed = true
		}
	}
	if !changed {
		return servers, false
	}
	return strings.Join(parts, ","), true
}

func redactNATSServerURL(raw string) (string, bool) {
	hasScheme := strings.Contains(raw, "://")
	parseTarget := raw
	if !hasScheme {
		parseTarget = "nats://" + raw
	}
	u, err := url.Parse(parseTarget)
	if err != nil {
		return redactUnparsedNATSServer(raw)
	}
	changed := false
	if u.User != nil {
		if _, hasPass := u.User.Password(); hasPass {
			u.User = url.UserPassword(u.User.Username(), "xxxxx")
			changed = true
		} else if u.User.Username() != "" {
			u.User = url.User("xxxxx")
			changed = true
		}
	}
	out := u.String()
	if !hasScheme {
		out = strings.TrimPrefix(out, "nats://")
	}
	if redacted := gocamel.RedactURI(out); redacted != out {
		out = redacted
		changed = true
	}
	if !changed {
		return raw, false
	}
	return out, true
}

func redactUnparsedNATSServer(raw string) (string, bool) {
	prefix := ""
	rest := raw
	if idx := strings.Index(raw, "://"); idx >= 0 {
		prefix = raw[:idx+3]
		rest = raw[idx+3:]
	}
	delim := strings.IndexAny(rest, "/?#")
	authority := rest
	if delim >= 0 {
		authority = rest[:delim]
	}
	at := strings.LastIndexByte(authority, '@')
	if at < 0 {
		return raw, false
	}
	userInfo := authority[:at]
	if colon := strings.IndexByte(userInfo, ':'); colon >= 0 {
		return prefix + userInfo[:colon+1] + "xxxxx" + rest[at:], true
	}
	if at > 0 {
		return prefix + "xxxxx" + rest[at:], true
	}
	return raw, false
}

// CreateProducer creates a NATSProducer.
func (e *NATSEndpoint) CreateProducer() (gocamel.Producer, error) {
	return &NATSProducer{endpoint: e}, nil
}

// CreateConsumer creates a NATSConsumer.
func (e *NATSEndpoint) CreateConsumer(processor gocamel.Processor) (gocamel.Consumer, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.consumer != nil {
		return nil, fmt.Errorf("multiple consumers for the same NATS endpoint are not allowed: %s", e.URI())
	}

	e.consumer = &NATSConsumer{
		endpoint:  e,
		processor: processor,
	}
	return e.consumer, nil
}

// lifecycleGate serializes Start and Stop transitions so a concurrent Stop
// cannot observe started=false and return while Start is still dialing or
// subscribing outside locks.
type lifecycleGate struct {
	mu         sync.Mutex
	transition chan struct{}
}

func (g *lifecycleGate) acquire() {
	for {
		g.mu.Lock()
		if g.transition != nil {
			done := g.transition
			g.mu.Unlock()
			<-done
			continue
		}
		g.transition = make(chan struct{})
		g.mu.Unlock()
		return
	}
}

func (g *lifecycleGate) release() {
	g.mu.Lock()
	close(g.transition)
	g.transition = nil
	g.mu.Unlock()
}

// NATSProducer publishes messages to NATS.
type NATSProducer struct {
	endpoint *NATSEndpoint
	gate     lifecycleGate
	mu       sync.Mutex
	started  bool
}

// Start starts the producer by opening a NATS connection.
func (p *NATSProducer) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	p.gate.acquire()
	defer p.gate.release()

	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	if _, err := p.endpoint.acquireConn(ctx); err != nil {
		return fmt.Errorf("nats producer connect failed: %w", err)
	}

	p.mu.Lock()
	p.started = true
	p.mu.Unlock()
	return nil
}

// Stop stops the producer by releasing its reference to the shared NATS connection.
func (p *NATSProducer) Stop() error {
	p.gate.acquire()
	defer p.gate.release()

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

// Send publishes the exchange body to NATS.
func (p *NATSProducer) Send(exchange *gocamel.Exchange) error {
	p.mu.Lock()
	started := p.started
	p.mu.Unlock()
	if !started {
		return fmt.Errorf("nats producer is not started or connected")
	}

	p.endpoint.mu.Lock()
	conn := p.endpoint.conn
	p.endpoint.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("nats producer is not started or connected")
	}

	// Determine NATS subject. The CamelNATSSubject header replaces the
	// configured subject wholesale, so it is honoured only when the endpoint
	// opts in with allowHeaderOverride=true. Enable it only where headers
	// cannot be influenced by untrusted input: any component that maps
	// external metadata onto headers would otherwise give an attacker control
	// of the publish target.
	subject := p.endpoint.subject
	if p.endpoint.allowHeaderOverride {
		if hSubject, ok := exchange.GetHeaderAsString(CamelNATSSubject); ok {
			// Lightweight validation: NATS subjects must be non-empty tokens
			// separated by dots and cannot contain spaces.
			if hSubject == "" || strings.Contains(hSubject, " ") {
				return fmt.Errorf("nats: invalid subject %q in CamelNATSSubject header: must be non-empty and contain no spaces", hSubject)
			}
			subject = hSubject
		}
	}

	if subject == "" {
		return fmt.Errorf("nats: subject is required to publish a message")
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

	err := conn.Publish(subject, payload)
	if err != nil {
		return fmt.Errorf("nats publish error on subject %q: %w", subject, err)
	}

	return nil
}

// Bounds of the backoff between attempts to recover from a receive error.
const (
	minRetryDelay = 100 * time.Millisecond
	maxRetryDelay = 5 * time.Second
)

// NATSConsumer subscribes to a NATS subject.
type NATSConsumer struct {
	endpoint  *NATSEndpoint
	processor gocamel.Processor
	gate      lifecycleGate
	subLock   sync.Mutex // Protects sub, replaced by run on resubscription
	sub       *nats.Subscription
	mu        sync.Mutex
	started   bool
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

func (c *NATSConsumer) subscription() *nats.Subscription {
	c.subLock.Lock()
	defer c.subLock.Unlock()
	return c.sub
}

// Start opens NATS connection, subscribes to subject and handles incoming messages.
func (c *NATSConsumer) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	c.gate.acquire()
	defer c.gate.release()

	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	if c.endpoint.subject == "" {
		return fmt.Errorf("nats consumer: subject is required to subscribe")
	}

	c.endpoint.mu.Lock()
	if c.endpoint.consumer != nil && c.endpoint.consumer != c {
		c.endpoint.mu.Unlock()
		return fmt.Errorf("multiple consumers for the same NATS endpoint are not allowed: %s", c.endpoint.URI())
	}
	c.endpoint.mu.Unlock()

	conn, err := c.endpoint.acquireConn(ctx)
	if err != nil {
		return fmt.Errorf("nats consumer connect failed: %w", err)
	}

	sub, err := conn.SubscribeSync(c.endpoint.subject)
	if err != nil {
		c.endpoint.closeConn()
		return fmt.Errorf("nats subscription failed: %w", err)
	}

	runCtx, cancel := context.WithCancel(ctx)

	c.endpoint.mu.Lock()
	if c.endpoint.consumer != nil && c.endpoint.consumer != c {
		c.endpoint.mu.Unlock()
		cancel()
		_ = sub.Unsubscribe()
		c.endpoint.closeConn()
		return fmt.Errorf("multiple consumers for the same NATS endpoint are not allowed: %s", c.endpoint.URI())
	}
	c.endpoint.consumer = c
	c.endpoint.mu.Unlock()

	c.subLock.Lock()
	c.sub = sub
	c.subLock.Unlock()

	c.mu.Lock()
	c.cancel = cancel
	c.started = true
	c.wg.Add(1)
	c.mu.Unlock()

	go c.run(runCtx)

	return nil
}

// Stop unsubscribes and releases the endpoint's shared NATS connection. The
// connection is closed via closeConn when the last user releases it.
func (c *NATSConsumer) Stop() error {
	c.gate.acquire()
	defer c.gate.release()

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
	// Unsubscribing wakes run up from NextMsg instead of waiting for its timeout.
	if sub := c.subscription(); sub != nil {
		_ = sub.Unsubscribe()
	}
	c.wg.Wait()

	c.endpoint.closeConn()

	c.endpoint.mu.Lock()
	if c.endpoint.consumer == c {
		c.endpoint.consumer = nil
	}
	c.endpoint.mu.Unlock()

	c.mu.Lock()
	c.started = false
	c.mu.Unlock()
	return nil
}

func (c *NATSConsumer) run(ctx context.Context) {
	defer c.wg.Done()
	// A subscription created by a resubscription racing Stop is released here.
	defer func() {
		if sub := c.subscription(); sub != nil {
			_ = sub.Unsubscribe()
		}
	}()

	failures := 0
	for ctx.Err() == nil {
		// Fetch NATS message with timeout
		msg, err := c.subscription().NextMsg(nats.DefaultTimeout)
		if err != nil {
			if errors.Is(err, nats.ErrTimeout) {
				continue // loop and check context cancellation
			}
			if ctx.Err() != nil {
				return
			}
			// Connection closed, subscription invalidated, slow consumer...:
			// returning would leave the route started with no consumer.
			delay := retryDelay(failures)
			failures++
			gocamel.LogWarn("nats receive failed; retrying",
				"uri", c.endpoint.URI(), "retry_in", delay, "error", err)
			if !waitForRetry(ctx, delay) {
				return
			}
			if err := c.resubscribe(ctx); err != nil {
				gocamel.LogWarn("nats resubscription failed; retrying",
					"uri", c.endpoint.URI(), "error", err)
			}
			continue
		}
		failures = 0

		exchange := gocamel.NewExchange(ctx)
		exchange.In.SetBody(msg.Data)
		exchange.In.SetHeader(CamelNATSSubject, msg.Subject)

		if err := gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange)); err != nil {
			gocamel.LogError("nats consumer processing error",
				"uri", c.endpoint.URI(),
				"error", err,
				"exchange_id", exchange.ID,
			)
		}
	}
}

// resubscribe replaces an invalid subscription, reconnecting first when the
// shared connection is closed. A still-valid subscription is kept: the error
// was transient (e.g. slow consumer) and the next NextMsg may succeed.
func (c *NATSConsumer) resubscribe(ctx context.Context) error {
	if sub := c.subscription(); sub != nil && sub.IsValid() {
		return nil
	}

	conn, err := c.liveConn(ctx)
	if err != nil {
		return err
	}
	sub, err := conn.SubscribeSync(c.endpoint.subject)
	if err != nil {
		return fmt.Errorf("nats subscription to %q failed: %w", c.endpoint.subject, err)
	}

	c.subLock.Lock()
	c.sub = sub
	c.subLock.Unlock()
	return nil
}

// liveConn returns the endpoint's shared connection, replacing it when it is
// closed. The connection is established outside the endpoint lock so that a
// slow broker does not block the producer or Stop.
func (c *NATSConsumer) liveConn(ctx context.Context) (*nats.Conn, error) {
	e := c.endpoint
	e.mu.Lock()
	conn := e.conn
	e.mu.Unlock()
	if conn != nil && !conn.IsClosed() {
		return conn, nil
	}

	newConn, err := connectNATS(ctx, e.servers)
	if err != nil {
		return nil, fmt.Errorf("nats consumer reconnect failed: %w", err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.connUsers == 0 {
		newConn.Close()
		return nil, errors.New("nats endpoint has no active users")
	}
	if e.conn != nil && e.conn != conn && !e.conn.IsClosed() {
		// The producer reconnected concurrently: share its connection.
		newConn.Close()
		return e.conn, nil
	}
	e.conn = newConn
	return newConn, nil
}

// retryDelay returns the exponential backoff before the given retry attempt.
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
