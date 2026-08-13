package gocamel

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// NetComponent implements Component for raw TCP and UDP sockets.
//
// It is the standard-library-only counterpart of Apache Camel's netty/mina
// components: same URI shape (net:tcp://host:port, net:udp://host:port), no
// third-party dependency. The consumer listens and feeds inbound messages
// into the route; the producer dials and sends the message body.
type NetComponent struct{}

// NewNetComponent creates a new NetComponent.
func NewNetComponent() *NetComponent {
	return &NetComponent{}
}

// CreateEndpoint creates a NetEndpoint from the URI.
//
// URI forms:
//   - net:tcp://host:port  (also net:tcp:host:port)
//   - net:udp://host:port  (also net:udp:host:port)
//
// Supported parameters:
//   - sync:       request-reply mode (default true)
//   - textline:   newline-delimited messages, TCP only (default true)
//   - bufferSize: maximum message size in bytes (default 8192)
//   - timeout:    producer dial/reply timeout in ms (default 30000, 0 = none)
//   - keepAlive:  TCP keepalive on connections (default false)
//   - readTimeout: consumer read deadline in ms (default 60000, 0 = none).
//     Bounds how long a connection may stay idle before it is closed, so a
//     slowloris-style client cannot hold a goroutine open indefinitely.
func (c *NetComponent) CreateEndpoint(uri string) (Endpoint, error) {
	u, err := ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid net URI: %w", err)
	}

	protocol, address, err := netTarget(u)
	if err != nil {
		return nil, fmt.Errorf("invalid net URI %q: %w", uri, err)
	}

	endpoint := &NetEndpoint{
		uri:         uri,
		protocol:    protocol,
		address:     address,
		sync:        true,
		textline:    true,
		bufferSize:  8192,
		timeout:     30 * time.Second,
		readTimeout: 60 * time.Second,
	}

	if val := GetConfigValue(u, "sync"); val != "" {
		if v, err := strconv.ParseBool(val); err == nil {
			endpoint.sync = v
		} else {
			LogWarn("net: invalid sync value; using default", "value", val, "default", endpoint.sync)
		}
	}
	if val := GetConfigValue(u, "textline"); val != "" {
		if v, err := strconv.ParseBool(val); err == nil {
			endpoint.textline = v
		} else {
			LogWarn("net: invalid textline value; using default", "value", val, "default", endpoint.textline)
		}
	}
	if val := GetConfigValue(u, "bufferSize"); val != "" {
		if v, err := strconv.Atoi(val); err == nil && v > 0 {
			endpoint.bufferSize = v
		} else {
			LogWarn("net: invalid bufferSize; using default", "value", val, "default", endpoint.bufferSize)
		}
	}
	if val := GetConfigValue(u, "timeout"); val != "" {
		if v, err := strconv.ParseInt(val, 10, 64); err == nil && v >= 0 {
			endpoint.timeout = time.Duration(v) * time.Millisecond
		} else {
			LogWarn("net: invalid timeout; using default", "value", val, "default", endpoint.timeout)
		}
	}
	if val := GetConfigValue(u, "keepAlive"); val != "" {
		if v, err := strconv.ParseBool(val); err == nil {
			endpoint.keepAlive = v
		} else {
			LogWarn("net: invalid keepAlive value; using default", "value", val, "default", endpoint.keepAlive)
		}
	}
	if val := GetConfigValue(u, "readTimeout"); val != "" {
		if v, err := strconv.ParseInt(val, 10, 64); err == nil && v >= 0 {
			endpoint.readTimeout = time.Duration(v) * time.Millisecond
		} else {
			LogWarn("net: invalid readTimeout; using default", "value", val, "default", endpoint.readTimeout)
		}
	}

	return endpoint, nil
}

// netTarget extracts the transport protocol and the host:port address from a
// parsed net URI. url.Parse maps both URI forms onto Opaque:
//
//	"net:tcp://host:port"  -> Opaque = "tcp://host:port"
//	"net:tcp:host:port"    -> Opaque = "tcp:host:port"
//
// both are accepted, as is an IPv6 host. The host defaults to "localhost"
// when omitted.
func netTarget(u *url.URL) (string, string, error) {
	protocol := u.Opaque
	host := u.Host

	if host == "" && protocol != "" {
		for _, p := range []string{"tcp", "udp"} {
			if rest, ok := strings.CutPrefix(protocol, p+":"); ok {
				protocol = p
				host = strings.TrimPrefix(rest, "//")
				break
			}
		}
	}

	if protocol != "tcp" && protocol != "udp" {
		return "", "", fmt.Errorf("protocol must be tcp or udp, got %q", protocol)
	}

	h, port, err := net.SplitHostPort(host)
	if err != nil {
		return "", "", fmt.Errorf("expected host:port, got %q: %w", host, err)
	}
	if port == "" {
		return "", "", errors.New("port is required")
	}
	if h == "" {
		h = "localhost"
	}
	return protocol, net.JoinHostPort(h, port), nil
}

// NetEndpoint represents a TCP or UDP endpoint.
type NetEndpoint struct {
	uri         string
	protocol    string
	address     string
	sync        bool
	textline    bool
	bufferSize  int
	timeout     time.Duration
	keepAlive   bool
	readTimeout time.Duration
}

// URI returns the endpoint URI.
func (e *NetEndpoint) URI() string {
	return e.uri
}

// CreateProducer creates a NetProducer.
func (e *NetEndpoint) CreateProducer() (Producer, error) {
	return &NetProducer{endpoint: e}, nil
}

// CreateConsumer creates a NetConsumer.
func (e *NetEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return &NetConsumer{
		endpoint:  e,
		processor: processor,
		stop:      make(chan struct{}),
	}, nil
}

// keepAliveInterval returns the net.Dialer.KeepAlive value matching the
// endpoint's keepAlive option (negative disables keep-alives).
func (e *NetEndpoint) keepAliveInterval() time.Duration {
	if e.keepAlive {
		return 30 * time.Second
	}
	return -1
}

// NetProducer sends the message body over TCP or UDP.
type NetProducer struct {
	endpoint *NetEndpoint
}

// Start starts the producer.
func (p *NetProducer) Start(ctx context.Context) error {
	return nil
}

// Stop stops the producer.
func (p *NetProducer) Stop() error {
	return nil
}

// Send dials the endpoint address, writes the In message body, and — when the
// endpoint is in sync mode — waits for a reply that is published on Out.
// Only []byte and string bodies are sent; other types send an empty payload,
// matching the HTTP producer.
func (p *NetProducer) Send(exchange *Exchange) error {
	var payload []byte
	switch body := exchange.GetIn().GetBody().(type) {
	case []byte:
		payload = body
	case string:
		payload = []byte(body)
	}

	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}

	dialer := &net.Dialer{
		Timeout:   p.endpoint.timeout,
		KeepAlive: p.endpoint.keepAliveInterval(),
	}
	conn, err := dialer.DialContext(ctx, p.endpoint.protocol, p.endpoint.address)
	if err != nil {
		return fmt.Errorf("net: error dialing %s://%s: %w", p.endpoint.protocol, p.endpoint.address, err)
	}
	defer conn.Close()

	if _, err := conn.Write(payload); err != nil {
		return fmt.Errorf("net: error sending to %s://%s: %w", p.endpoint.protocol, p.endpoint.address, err)
	}

	if !p.endpoint.sync {
		return nil
	}

	// Request-reply: read a bounded reply and publish it on Out.
	if p.endpoint.timeout > 0 {
		if err := conn.SetReadDeadline(time.Now().Add(p.endpoint.timeout)); err != nil {
			return fmt.Errorf("net: error setting read deadline: %w", err)
		}
	}
	buf := make([]byte, p.endpoint.bufferSize)
	n, err := conn.Read(buf)
	if err != nil {
		return fmt.Errorf("net: error reading reply from %s://%s: %w", p.endpoint.protocol, p.endpoint.address, err)
	}
	exchange.GetOut().SetBody(buf[:n])
	exchange.GetOut().SetHeader(CamelNetLocalAddress, conn.LocalAddr().String())
	return nil
}

// NetConsumer listens on the endpoint address and feeds inbound messages into
// the route. In sync mode (the default) the route's response is written back
// to the peer, giving request-reply semantics.
type NetConsumer struct {
	endpoint  *NetEndpoint
	processor Processor

	mu      sync.Mutex
	started bool
	conns   map[net.Conn]struct{}
	stop    chan struct{}
	wg      sync.WaitGroup

	listener   net.Listener
	packetConn net.PacketConn
	addr       net.Addr // bound address, useful when the URI port is 0
}

// Address returns the address the consumer is bound to. Useful when the URI
// specified port 0 (OS-assigned ephemeral port).
func (c *NetConsumer) Address() net.Addr {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.addr
}

// Start begins listening. Bind errors are returned synchronously so
// misconfiguration surfaces at Start, not on the first message.
func (c *NetConsumer) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return errors.New("net consumer is already running")
	}
	c.mu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}
	stop := make(chan struct{})

	if c.endpoint.protocol == "udp" {
		pc, err := net.ListenPacket("udp", c.endpoint.address)
		if err != nil {
			return fmt.Errorf("net: error listening on udp://%s: %w", c.endpoint.address, err)
		}
		c.mu.Lock()
		c.started = true
		c.stop = stop
		c.packetConn = pc
		c.addr = pc.LocalAddr()
		c.mu.Unlock()
		c.wg.Add(1)
		go c.runUDP(ctx, pc, stop)
		return nil
	}

	ln, err := net.Listen("tcp", c.endpoint.address)
	if err != nil {
		return fmt.Errorf("net: error listening on tcp://%s: %w", c.endpoint.address, err)
	}
	c.mu.Lock()
	c.started = true
	c.stop = stop
	c.listener = ln
	c.addr = ln.Addr()
	c.conns = make(map[net.Conn]struct{})
	c.mu.Unlock()
	c.wg.Add(1)
	go c.acceptLoop(ctx, ln, stop)
	return nil
}

// Stop gracefully shuts the consumer down: it stops accepting, closes the
// listener and every tracked connection, and waits for the goroutines to
// unwind. Safe to call multiple times.
func (c *NetConsumer) Stop() error {
	c.mu.Lock()
	if !c.started {
		c.mu.Unlock()
		return nil
	}
	close(c.stop)
	if c.listener != nil {
		_ = c.listener.Close()
	}
	if c.packetConn != nil {
		_ = c.packetConn.Close()
	}
	for conn := range c.conns {
		_ = conn.Close()
	}
	c.started = false
	c.mu.Unlock()

	c.wg.Wait()
	return nil
}

// acceptLoop accepts connections until the listener is closed (Stop).
func (c *NetConsumer) acceptLoop(ctx context.Context, ln net.Listener, stop <-chan struct{}) {
	defer c.wg.Done()
	for {
		conn, err := ln.Accept()
		if err != nil {
			// Listener closed (Stop) or fatal error: the consumer is done.
			return
		}
		if c.endpoint.keepAlive {
			if tc, ok := conn.(*net.TCPConn); ok {
				_ = tc.SetKeepAlive(true)
			}
		}

		c.mu.Lock()
		if !c.started {
			// Stop() already ran its teardown; this connection was accepted
			// concurrently and will not be closed by it.
			c.mu.Unlock()
			_ = conn.Close()
			return
		}
		c.conns[conn] = struct{}{}
		c.mu.Unlock()

		c.wg.Add(1)
		go c.handleConn(ctx, conn)
	}
}

// handleConn reads messages from one connection and runs the route for each.
// One message is one newline-delimited line (textline mode, the default) or
// one read (raw mode).
func (c *NetConsumer) handleConn(ctx context.Context, conn net.Conn) {
	defer c.wg.Done()
	defer func() {
		_ = conn.Close()
		c.mu.Lock()
		delete(c.conns, conn)
		c.mu.Unlock()
	}()

	reader := bufio.NewReaderSize(conn, c.endpoint.bufferSize)
	writeReply := func(resp []byte) error {
		_, err := conn.Write(resp)
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stop:
			return
		default:
		}

		// Bound the idle time on each read so a client that opens a connection
		// and then goes silent (slowloris) cannot pin a goroutine forever.
		if c.endpoint.readTimeout > 0 {
			if err := conn.SetReadDeadline(time.Now().Add(c.endpoint.readTimeout)); err != nil {
				return
			}
		}

		var msg []byte
		var err error
		if c.endpoint.textline {
			msg, err = readTextLine(reader)
		} else {
			buf := make([]byte, c.endpoint.bufferSize)
			var n int
			n, err = conn.Read(buf)
			msg = buf[:n]
		}

		switch {
		case err == nil:
			c.processMessage(ctx, msg, conn.RemoteAddr().String(), writeReply)
		case len(msg) > 0 && errors.Is(err, io.EOF):
			// Final unterminated line: process it, then close.
			c.processMessage(ctx, msg, conn.RemoteAddr().String(), writeReply)
			return
		case errors.Is(err, io.EOF), errors.Is(err, net.ErrClosed):
			// Normal close, or the connection was closed by Stop.
			return
		case errors.Is(err, bufio.ErrBufferFull):
			// Line longer than bufferSize: protocol error. Dropping the
			// connection instead of buffering unbounded data.
			LogWarn("net: message exceeds bufferSize; closing connection",
				"uri", c.endpoint.uri,
				"remote", conn.RemoteAddr().String(),
				"limit", c.endpoint.bufferSize)
			return
		default:
			LogError("net: error reading from connection",
				"uri", c.endpoint.uri,
				"remote", conn.RemoteAddr().String(),
				"error", err)
			return
		}
	}
}

// readTextLine reads one newline-terminated line, stripped of its trailing
// \n and optional \r. An unterminated final line yields io.EOF alongside the
// data; a line longer than the reader buffer yields bufio.ErrBufferFull.
func readTextLine(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return nil, bufio.ErrBufferFull
	}
	return bytes.TrimRight(line, "\r\n"), err
}

// runUDP reads datagrams and runs the route for each. Each datagram is one
// message, sized at most bufferSize.
func (c *NetConsumer) runUDP(ctx context.Context, pc net.PacketConn, stop <-chan struct{}) {
	defer c.wg.Done()
	buf := make([]byte, c.endpoint.bufferSize)
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		default:
		}

		n, remote, err := pc.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return
			}
			LogError("net: UDP read error", "uri", c.endpoint.uri, "error", err)
			continue
		}

		// Copy: buf is reused for the next datagram.
		msg := make([]byte, n)
		copy(msg, buf[:n])
		c.processMessage(ctx, msg, remote.String(), func(resp []byte) error {
			_, err := pc.WriteTo(resp, remote)
			return err
		})
	}
}

// processMessage builds an exchange from a received message, runs the route
// through the consumer's processor, and — in sync mode — delivers the route's
// response via sendReply. Panics in user processors are contained by
// safeProcess so a misbehaving route cannot crash the consumer.
func (c *NetConsumer) processMessage(ctx context.Context, msg []byte, remote string, sendReply func([]byte) error) {
	exchange := NewExchange(ctx)
	exchange.SetBody(msg)
	exchange.GetIn().SetHeader(CamelNetRemoteAddress, remote)
	exchange.GetIn().SetHeader(CamelNetLocalAddress, c.addr.String())

	err := safeProcess(func() error { return c.processor.Process(exchange) })
	if err := CompleteExchange(exchange, err); err != nil {
		LogError("net consumer route error",
			"uri", c.endpoint.uri,
			"exchange_id", exchange.ID,
			"remote", remote,
			"error", err)
		return
	}
	if !c.endpoint.sync {
		return
	}

	// Response: Out if set, otherwise In (Apache Camel InOut semantics).
	var payload []byte
	switch body := exchange.GetResponse().GetBody().(type) {
	case []byte:
		payload = body
	case string:
		payload = []byte(body)
	case nil:
		return
	default:
		payload = []byte(fmt.Sprintf("%v", body))
	}
	if err := sendReply(payload); err != nil {
		LogError("net: error writing reply",
			"uri", c.endpoint.uri,
			"remote", remote,
			"error", err)
	}
}
