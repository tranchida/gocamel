package gocamel

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func startNetConsumer(t *testing.T, uri string, proc Processor) *NetConsumer {
	t.Helper()
	endpoint, err := NewNetComponent().CreateEndpoint(uri)
	require.NoError(t, err)
	consumer, err := endpoint.CreateConsumer(proc)
	require.NoError(t, err)
	nc := consumer.(*NetConsumer)
	require.NoError(t, nc.Start(context.Background()))
	t.Cleanup(func() { nc.Stop() })
	return nc
}

func newNetProducer(t *testing.T, uri string) Producer {
	t.Helper()
	endpoint, err := NewNetComponent().CreateEndpoint(uri)
	require.NoError(t, err)
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)
	return producer
}

// messageBodyString renders a message body the way a wire component delivers
// it ([]byte), matching GetBodyAsString's string-only contract.
func messageBodyString(m *Message) string {
	switch b := m.GetBody().(type) {
	case string:
		return b
	case []byte:
		return string(b)
	}
	return ""
}

func waitForMessage(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for message")
		return ""
	}
}

func TestNetCreateEndpoint(t *testing.T) {
	comp := NewNetComponent()

	tests := []struct {
		name      string
		uri       string
		wantProto string
		wantAddr  string
		wantErr   bool
	}{
		{"tcp", "net:tcp://localhost:9090", "tcp", "localhost:9090", false},
		{"udp", "net:udp://localhost:9090", "udp", "localhost:9090", false},
		{"opaque tcp", "net:tcp:localhost:9090", "tcp", "localhost:9090", false},
		{"opaque udp", "net:udp:localhost:9090", "udp", "localhost:9090", false},
		{"ipv6", "net:tcp://[::1]:9090", "tcp", "[::1]:9090", false},
		{"empty host defaults to localhost", "net:tcp://:9090", "tcp", "localhost:9090", false},
		{"missing port", "net:tcp://localhost", "", "", true},
		{"bad protocol", "net:foo://localhost:9090", "", "", true},
		{"no protocol", "net://localhost:9090", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			endpoint, err := comp.CreateEndpoint(tt.uri)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			ne := endpoint.(*NetEndpoint)
			assert.Equal(t, tt.wantProto, ne.protocol)
			assert.Equal(t, tt.wantAddr, ne.address)
		})
	}
}

func TestNetEndpointOptions(t *testing.T) {
	endpoint, err := NewNetComponent().CreateEndpoint(
		"net:tcp://localhost:9090?sync=false&textline=false&bufferSize=4096&timeout=5000&keepAlive=true")
	require.NoError(t, err)
	ne := endpoint.(*NetEndpoint)
	assert.False(t, ne.sync)
	assert.False(t, ne.textline)
	assert.Equal(t, 4096, ne.bufferSize)
	assert.Equal(t, 5*time.Second, ne.timeout)
	assert.True(t, ne.keepAlive)
}

func TestNetTCPRequestReply(t *testing.T) {
	consumer := startNetConsumer(t, "net:tcp://localhost:0?timeout=2000", ProcessorFunc(func(e *Exchange) error {
		e.GetOut().SetBody(strings.ToUpper(messageBodyString(e.GetIn())))
		return nil
	}))

	producer := newNetProducer(t, "net:tcp://"+consumer.Address().String()+"?timeout=2000")

	// Textline mode (default) frames messages with newlines.
	exchange := NewExchange(context.Background())
	exchange.SetBody("hello\n")
	require.NoError(t, producer.Send(exchange))

	reply := messageBodyString(exchange.GetOut())
	require.NotEmpty(t, reply)
	assert.Equal(t, "HELLO", reply)
}

func TestNetTCPFireAndForget(t *testing.T) {
	received := make(chan string, 1)
	consumer := startNetConsumer(t, "net:tcp://localhost:0?sync=false", ProcessorFunc(func(e *Exchange) error {
		received <- messageBodyString(e.GetIn())
		return nil
	}))

	producer := newNetProducer(t, "net:tcp://"+consumer.Address().String()+"?sync=false")

	exchange := NewExchange(context.Background())
	exchange.SetBody("ping\n")
	require.NoError(t, producer.Send(exchange))
	assert.Equal(t, "ping", waitForMessage(t, received))
}

func TestNetTCPTextLineMultipleMessages(t *testing.T) {
	received := make(chan string, 2)
	consumer := startNetConsumer(t, "net:tcp://localhost:0?sync=false", ProcessorFunc(func(e *Exchange) error {
		received <- messageBodyString(e.GetIn())
		return nil
	}))

	conn, err := net.Dial("tcp", consumer.Address().String())
	require.NoError(t, err)
	defer conn.Close()

	// Two newline-delimited messages on one connection; CRLF is stripped.
	_, err = conn.Write([]byte("first line\nsecond line\r\n"))
	require.NoError(t, err)

	assert.Equal(t, "first line", waitForMessage(t, received))
	assert.Equal(t, "second line", waitForMessage(t, received))
}

func TestNetTCPRawMode(t *testing.T) {
	received := make(chan string, 1)
	consumer := startNetConsumer(t, "net:tcp://localhost:0?textline=false&sync=false", ProcessorFunc(func(e *Exchange) error {
		received <- messageBodyString(e.GetIn())
		return nil
	}))

	conn, err := net.Dial("tcp", consumer.Address().String())
	require.NoError(t, err)
	defer conn.Close()

	// Raw mode: no framing, each read is one message.
	_, err = conn.Write([]byte("hello world"))
	require.NoError(t, err)

	assert.Equal(t, "hello world", waitForMessage(t, received))
}

func TestNetUDPRequestReply(t *testing.T) {
	consumer := startNetConsumer(t, "net:udp://localhost:0?timeout=2000", ProcessorFunc(func(e *Exchange) error {
		e.GetOut().SetBody("echo:" + messageBodyString(e.GetIn()))
		return nil
	}))

	producer := newNetProducer(t, "net:udp://"+consumer.Address().String()+"?timeout=2000")

	exchange := NewExchange(context.Background())
	exchange.SetBody("ping")
	require.NoError(t, producer.Send(exchange))

	reply := messageBodyString(exchange.GetOut())
	require.NotEmpty(t, reply)
	assert.Equal(t, "echo:ping", reply)
}

func TestNetUDPFireAndForget(t *testing.T) {
	received := make(chan string, 1)
	consumer := startNetConsumer(t, "net:udp://localhost:0?sync=false", ProcessorFunc(func(e *Exchange) error {
		received <- messageBodyString(e.GetIn())
		return nil
	}))

	producer := newNetProducer(t, "net:udp://"+consumer.Address().String()+"?sync=false")

	exchange := NewExchange(context.Background())
	exchange.SetBody("stats")
	require.NoError(t, producer.Send(exchange))
	assert.Equal(t, "stats", waitForMessage(t, received))
}

func TestNetConsumerRemoteAddressHeader(t *testing.T) {
	remote := make(chan string, 1)
	consumer := startNetConsumer(t, "net:tcp://localhost:0?sync=false", ProcessorFunc(func(e *Exchange) error {
		v, ok := e.GetIn().GetHeaderAsString(CamelNetRemoteAddress)
		if !ok {
			t.Errorf("CamelNetRemoteAddress header missing")
		}
		remote <- v
		return nil
	}))

	conn, err := net.Dial("tcp", consumer.Address().String())
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.Write([]byte("hi\n"))
	require.NoError(t, err)

	addr := waitForMessage(t, remote)
	// The header carries the peer's host:port; the port part must be set.
	_, port, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	assert.NotEmpty(t, port)
}

func TestNetOversizedLineDropsConnection(t *testing.T) {
	received := make(chan string, 1)
	consumer := startNetConsumer(t, "net:tcp://localhost:0?sync=false&bufferSize=16", ProcessorFunc(func(e *Exchange) error {
		received <- messageBodyString(e.GetIn())
		return nil
	}))

	addr := consumer.Address().String()

	// A line longer than bufferSize without a newline is a protocol error:
	// the consumer drops the connection instead of buffering unbounded data.
	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	_, err = conn.Write([]byte(strings.Repeat("a", 64)))
	require.NoError(t, err)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	buf := make([]byte, 1)
	_, err = conn.Read(buf)
	require.Error(t, err) // EOF or reset: we were dropped
	conn.Close()

	// A well-behaved client still gets through.
	conn2, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer conn2.Close()
	_, err = conn2.Write([]byte("short\n"))
	require.NoError(t, err)
	assert.Equal(t, "short", waitForMessage(t, received))
}

func TestNetProducerDialError(t *testing.T) {
	// Port 1 on localhost: nothing listens there, the dial fails fast.
	producer := newNetProducer(t, "net:tcp://localhost:1?timeout=500")

	exchange := NewExchange(context.Background())
	exchange.SetBody("x")
	err := producer.Send(exchange)
	require.Error(t, err)
}

func TestNetConsumerBindConflict(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	defer ln.Close()

	endpoint, err := NewNetComponent().CreateEndpoint("net:tcp://" + ln.Addr().String())
	require.NoError(t, err)
	consumer, err := endpoint.CreateConsumer(ProcessorFunc(func(e *Exchange) error { return nil }))
	require.NoError(t, err)

	err = consumer.Start(context.Background())
	require.Error(t, err)
}

func TestNetConsumerStopIdempotent(t *testing.T) {
	consumer := startNetConsumer(t, "net:tcp://localhost:0", ProcessorFunc(func(e *Exchange) error {
		return nil
	}))
	require.NoError(t, consumer.Stop())
	require.NoError(t, consumer.Stop())
}

func TestNetConsumerStringBodyReply(t *testing.T) {
	// A non-[]byte/non-string body on Out falls back to its %v form.
	consumer := startNetConsumer(t, "net:tcp://localhost:0?timeout=2000", ProcessorFunc(func(e *Exchange) error {
		e.GetOut().SetBody(42)
		return nil
	}))

	producer := newNetProducer(t, "net:tcp://"+consumer.Address().String()+"?timeout=2000")

	exchange := NewExchange(context.Background())
	exchange.SetBody("x\n")
	require.NoError(t, producer.Send(exchange))

	reply := messageBodyString(exchange.GetOut())
	require.NotEmpty(t, reply)
	assert.Equal(t, "42", reply)
}

func TestNetTCPTextlineFramesRequestAndReply(t *testing.T) {
	// Regression: the producer did not terminate its request, so a textline
	// consumer waited for '\n' while the producer waited for the reply until
	// the timeout.
	consumer := startNetConsumer(t, "net:tcp://localhost:0", ProcessorFunc(func(e *Exchange) error {
		e.GetOut().SetBody(strings.ToUpper(messageBodyString(e.GetIn())))
		return nil
	}))
	producer := newNetProducer(t, "net:tcp://"+consumer.Address().String()+"?timeout=2000")

	body := make([]byte, 4, 16)
	copy(body, "ping")
	exchange := NewExchange(context.Background())
	exchange.SetBody(body)
	start := time.Now()
	require.NoError(t, producer.Send(exchange))
	assert.Less(t, time.Since(start), time.Second, "round trip must not wait for the timeout")
	assert.Equal(t, "PING", messageBodyString(exchange.GetOut()))
	assert.Equal(t, "ping", string(body[:cap(body)][:4]), "caller's body is not modified")
	assert.Equal(t, byte(0), body[:cap(body)][4], "delimiter not written into the caller's backing array")
}

func TestNetTCPTextlineConsumerReplyIsDelimited(t *testing.T) {
	consumer := startNetConsumer(t, "net:tcp://localhost:0", ProcessorFunc(func(e *Exchange) error {
		e.GetOut().SetBody("reply-" + messageBodyString(e.GetIn()))
		return nil
	}))
	conn, err := net.Dial("tcp", consumer.Address().String())
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.SetDeadline(time.Now().Add(2*time.Second)))

	// Two requests on one connection yield two distinguishable replies.
	_, err = conn.Write([]byte("a\nb\n"))
	require.NoError(t, err)
	reader := bufio.NewReader(conn)
	first, err := reader.ReadString('\n')
	require.NoError(t, err)
	second, err := reader.ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "reply-a\n", first)
	assert.Equal(t, "reply-b\n", second)
}

func TestNetTCPTextlineReplyLongerThanBufferSize(t *testing.T) {
	consumer := startNetConsumer(t, "net:tcp://localhost:0", ProcessorFunc(func(e *Exchange) error {
		e.GetOut().SetBody(strings.Repeat("x", 64))
		return nil
	}))
	producer := newNetProducer(t, "net:tcp://"+consumer.Address().String()+"?timeout=2000&bufferSize=16")

	exchange := NewExchange(context.Background())
	exchange.SetBody("q")
	err := producer.Send(exchange)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds bufferSize")
}
