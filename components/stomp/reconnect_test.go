package stomp

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-stomp/stomp/frame"
	"github.com/go-stomp/stomp/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gocamel "github.com/tranchida/gocamel"
)

func TestStompEndpoint_ConnectTimeoutOption(t *testing.T) {
	component := NewStompComponent()

	endpoint, err := component.CreateEndpoint("stomp://queue/orders")
	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, endpoint.(*StompEndpoint).connectTimeout)

	endpoint, err = component.CreateEndpoint("stomp://queue/orders?connectTimeout=250ms")
	require.NoError(t, err)
	assert.Equal(t, 250*time.Millisecond, endpoint.(*StompEndpoint).connectTimeout)
}

// startProducerWithin starts a producer on host and fails the test if Start
// does not return within the deadline.
func startProducerWithin(t *testing.T, host string, deadline time.Duration) error {
	t.Helper()
	endpoint, err := NewStompComponent().CreateEndpoint("stomp://queue/orders?host=" + host + "&connectTimeout=100ms")
	require.NoError(t, err)
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	result := make(chan error, 1)
	go func() { result <- producer.Start(context.Background()) }()
	select {
	case err := <-result:
		return err
	case <-time.After(deadline):
		t.Fatalf("producer Start did not honour connectTimeout against %s", host)
		return nil
	}
}

func TestStompEndpoint_DialHonoursConnectTimeout(t *testing.T) {
	// Non-routable address: the TCP connect never completes.
	assert.Error(t, startProducerWithin(t, "10.255.255.1:1", time.Second))
}

func TestStompEndpoint_HandshakeHonoursConnectTimeout(t *testing.T) {
	// A broker accepting TCP connections but never answering CONNECT.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()

	assert.Error(t, startProducerWithin(t, l.Addr().String(), time.Second))
}

// droppableListener records accepted connections so a test can simulate a
// broker crash by dropping every client connection.
type droppableListener struct {
	net.Listener
	mu    sync.Mutex
	conns []net.Conn
}

func (l *droppableListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err == nil {
		l.mu.Lock()
		l.conns = append(l.conns, conn)
		l.mu.Unlock()
	}
	return conn, err
}

func (l *droppableListener) dropAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, conn := range l.conns {
		_ = conn.Close()
	}
	l.conns = nil
}

func startDroppableBroker(t *testing.T) (string, *droppableListener) {
	t.Helper()
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	l := &droppableListener{Listener: inner}
	t.Cleanup(func() { _ = l.Close() })

	srv := &server.Server{}
	go func() { _ = srv.Serve(l) }()
	return inner.Addr().String(), l
}

func startReceivingRoute(t *testing.T, uri string) <-chan string {
	t.Helper()
	camelCtx := gocamel.NewCamelContext()
	camelCtx.AddComponent("stomp", NewStompComponent())

	received := make(chan string, 64)
	_, err := camelCtx.CreateRouteBuilder().
		From(uri).
		ProcessFunc(func(e *gocamel.Exchange) error {
			body, _ := e.GetBody().([]byte)
			received <- string(body)
			return nil
		}).
		Build()
	require.NoError(t, err)
	require.NoError(t, camelCtx.Start())
	t.Cleanup(func() { _ = camelCtx.Stop() })
	return received
}

func TestStompConsumer_ReconnectsAfterBrokerConnectionLoss(t *testing.T) {
	addr, listener := startDroppableBroker(t)
	received := startReceivingRoute(t, "stomp://queue/resilient?host="+addr)

	sendRaw(t, addr, "/queue/resilient", "before")
	select {
	case body := <-received:
		require.Equal(t, "before", body)
	case <-time.After(5 * time.Second):
		t.Fatal("message not received before the connection loss")
	}

	listener.dropAll()

	// Messages sent while the broker still holds the dead subscription may be
	// lost: keep sending until one reaches the reconnected consumer.
	attempt := 0
	assert.Eventually(t, func() bool {
		attempt++
		sendRaw(t, addr, "/queue/resilient", fmt.Sprintf("after-%d", attempt))
		select {
		case body := <-received:
			return strings.HasPrefix(body, "after-")
		case <-time.After(50 * time.Millisecond):
			return false
		}
	}, 10*time.Second, 10*time.Millisecond, "consumer did not reconnect after the connection loss")
}

// startScriptedBroker serves each accepted connection with the next handler.
// Handlers receive the subscription id of the client's SUBSCRIBE frame.
func startScriptedBroker(t *testing.T, handlers ...func(w *frame.Writer, subID string)) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	go func() {
		for _, handle := range handlers {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
			go func() {
				r, w := frame.NewReader(conn), frame.NewWriter(conn)
				if _, err := r.Read(); err != nil { // CONNECT
					return
				}
				if w.Write(frame.New(frame.CONNECTED, frame.Version, "1.2")) != nil {
					return
				}
				for {
					f, err := r.Read()
					if err != nil || f == nil {
						return
					}
					switch f.Command {
					case frame.SUBSCRIBE:
						handle(w, f.Header.Get(frame.Id))
					case frame.DISCONNECT:
						_ = w.Write(frame.New(frame.RECEIPT, frame.ReceiptId, f.Header.Get(frame.Receipt)))
						return
					}
				}
			}()
		}
	}()
	return l.Addr().String()
}

func TestStompConsumer_ReconnectsAfterBrokerErrorFrame(t *testing.T) {
	addr := startScriptedBroker(t,
		func(w *frame.Writer, _ string) {
			_ = w.Write(frame.New(frame.ERROR, frame.Message, "broker failure"))
		},
		func(w *frame.Writer, subID string) {
			_ = w.Write(frame.New(frame.MESSAGE,
				frame.Destination, "/queue/resilient",
				frame.MessageId, "1",
				frame.Subscription, subID))
		},
	)
	received := startReceivingRoute(t, "stomp://queue/resilient?host="+addr)

	select {
	case <-received:
	case <-time.After(10 * time.Second):
		t.Fatal("consumer did not reconnect after a broker ERROR frame")
	}
}
