package nats

import (
	"context"
	"net"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gocamel "github.com/tranchida/gocamel"
)

func runEmbeddedNatsServer(t *testing.T) *server.Server {
	opts := &server.Options{
		Host: "127.0.0.1",
		Port: -1, // Use a random available port
	}
	ns, err := server.NewServer(opts)
	if err != nil {
		t.Fatalf("Failed to create NATS server: %v", err)
	}

	go ns.Start()

	// Wait for the server to be ready
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatalf("NATS server did not start in time")
	}

	return ns
}

func TestNATSComponent_PubSub(t *testing.T) {
	// Start embedded NATS server
	ns := runEmbeddedNatsServer(t)
	defer ns.Shutdown()

	clientURL := ns.ClientURL() // e.g. "nats://127.0.0.1:51341"

	camelCtx := gocamel.NewCamelContext()
	camelCtx.AddComponent("direct", gocamel.NewDirectComponent())
	camelCtx.AddComponent("nats", NewNATSComponent())

	done := make(chan string, 1)

	// Consumer route: nats subscription (subject 'orders') -> ProcessFunc
	_, err := camelCtx.CreateRouteBuilder().
		From("nats://orders?servers=" + clientURL).
		ProcessFunc(func(e *gocamel.Exchange) error {
			bodyBytes, ok := e.GetBody().([]byte)
			if ok {
				done <- string(bodyBytes)
			} else {
				str, _ := e.GetBodyAsString()
				done <- str
			}
			return nil
		}).
		Build()
	assert.NoError(t, err)

	// Producer route: direct:publish -> nats subject 'orders'
	_, err = camelCtx.CreateRouteBuilder().
		From("direct:publish").
		To("nats://orders?servers=" + clientURL).
		Build()
	assert.NoError(t, err)

	err = camelCtx.Start()
	assert.NoError(t, err)
	defer camelCtx.Stop()

	// Give subscription a moment to register on NATS server
	time.Sleep(100 * time.Millisecond)

	// Publish via direct:publish
	pubEndpoint, _ := camelCtx.CreateEndpoint("direct:publish")
	pubProducer, _ := pubEndpoint.CreateProducer()

	pubExchange := gocamel.NewExchange(context.Background())
	pubExchange.In.SetBody("Order #1234 Created")

	err = pubProducer.Send(pubExchange)
	assert.NoError(t, err)

	// Wait for message asynchronously using channel
	select {
	case val := <-done:
		assert.Equal(t, "Order #1234 Created", val)
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for NATS message in PubSub test")
	}
}

// Default (secure) behavior: without allowHeaderOverride=true, the
// CamelNATSSubject header is ignored and only the URI subject applies.
func TestNATSComponent_HeaderOverrideIgnoredByDefault(t *testing.T) {
	ns := runEmbeddedNatsServer(t)
	defer ns.Shutdown()

	clientURL := ns.ClientURL()

	camelCtx := gocamel.NewCamelContext()
	camelCtx.AddComponent("direct", gocamel.NewDirectComponent())
	camelCtx.AddComponent("nats", NewNATSComponent())

	done := make(chan string, 1)

	// Consumer subscribing to subject 'alerts'
	_, err := camelCtx.CreateRouteBuilder().
		From("nats://alerts?servers=" + clientURL).
		ProcessFunc(func(e *gocamel.Exchange) error {
			bodyBytes, ok := e.GetBody().([]byte)
			if ok {
				done <- string(bodyBytes)
			} else {
				str, _ := e.GetBodyAsString()
				done <- str
			}
			return nil
		}).
		Build()
	assert.NoError(t, err)

	// Producer bound to fixed subject 'alerts' — no opt-in.
	_, err = camelCtx.CreateRouteBuilder().
		From("direct:publish").
		To("nats://alerts?servers=" + clientURL).
		Build()
	assert.NoError(t, err)

	err = camelCtx.Start()
	assert.NoError(t, err)
	defer camelCtx.Stop()

	time.Sleep(100 * time.Millisecond)

	pubEndpoint, _ := camelCtx.CreateEndpoint("direct:publish")
	pubProducer, _ := pubEndpoint.CreateProducer()

	// The header attempts to redirect the publish to another subject,
	// but it must be ignored: the message lands on the URI subject.
	pubExchange := gocamel.NewExchange(context.Background())
	pubExchange.In.SetHeader(CamelNATSSubject, "other.subject")
	pubExchange.In.SetBody("Delivered To URI Subject")

	err = pubProducer.Send(pubExchange)
	assert.NoError(t, err)

	select {
	case val := <-done:
		assert.Equal(t, "Delivered To URI Subject", val)
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for NATS message in HeaderOverrideIgnoredByDefault test")
	}
}

func TestNATSComponent_DynamicSubject(t *testing.T) {
	ns := runEmbeddedNatsServer(t)
	defer ns.Shutdown()

	clientURL := ns.ClientURL()

	camelCtx := gocamel.NewCamelContext()
	camelCtx.AddComponent("direct", gocamel.NewDirectComponent())
	camelCtx.AddComponent("nats", NewNATSComponent())

	done := make(chan string, 1)

	// Consumer subscribing to subject 'alerts'
	_, err := camelCtx.CreateRouteBuilder().
		From("nats://alerts?servers=" + clientURL).
		ProcessFunc(func(e *gocamel.Exchange) error {
			bodyBytes, ok := e.GetBody().([]byte)
			if ok {
				done <- string(bodyBytes)
			} else {
				str, _ := e.GetBodyAsString()
				done <- str
			}
			return nil
		}).
		Build()
	assert.NoError(t, err)

	// Generic producer route opting in to header-based subject override
	_, err = camelCtx.CreateRouteBuilder().
		From("direct:publish").
		To("nats://?servers=" + clientURL + "&allowHeaderOverride=true").
		Build()
	assert.NoError(t, err)

	err = camelCtx.Start()
	assert.NoError(t, err)
	defer camelCtx.Stop()

	time.Sleep(100 * time.Millisecond)

	pubEndpoint, _ := camelCtx.CreateEndpoint("direct:publish")
	pubProducer, _ := pubEndpoint.CreateProducer()

	// Send message dynamically targeting subject 'alerts' via header
	pubExchange := gocamel.NewExchange(context.Background())
	pubExchange.In.SetHeader(CamelNATSSubject, "alerts")
	pubExchange.In.SetBody("System Overheat Alert!")

	err = pubProducer.Send(pubExchange)
	assert.NoError(t, err)

	// Wait for message asynchronously using channel
	select {
	case val := <-done:
		assert.Equal(t, "System Overheat Alert!", val)
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for NATS message in DynamicSubject test")
	}

	// Invalid overridden subjects (empty or containing a space) are rejected.
	emptyExchange := gocamel.NewExchange(context.Background())
	emptyExchange.In.SetHeader(CamelNATSSubject, "")
	emptyExchange.In.SetBody("data")
	err = pubProducer.Send(emptyExchange)
	assert.ErrorContains(t, err, "invalid subject")

	spacedExchange := gocamel.NewExchange(context.Background())
	spacedExchange.In.SetHeader(CamelNATSSubject, "bad subject")
	spacedExchange.In.SetBody("data")
	err = pubProducer.Send(spacedExchange)
	assert.ErrorContains(t, err, "invalid subject")
}

// startTestConsumer starts a consumer on subject "resilient" that forwards
// every received body to the returned channel.
func startTestConsumer(t *testing.T, clientURL string) (*NATSConsumer, <-chan string) {
	endpoint, err := NewNATSComponent().CreateEndpoint("nats://resilient?servers=" + clientURL)
	require.NoError(t, err)

	received := make(chan string, 16)
	consumer, err := endpoint.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		body, _ := e.GetBody().([]byte)
		received <- string(body)
		return nil
	}))
	require.NoError(t, err)

	natsConsumer := consumer.(*NATSConsumer)
	require.NoError(t, natsConsumer.Start(context.Background()))
	t.Cleanup(func() { _ = natsConsumer.Stop() })
	return natsConsumer, received
}

// assertEventuallyReceived publishes body on an independent connection until
// the consumer delivers it: messages published while the consumer is
// resubscribing are not buffered by core NATS.
func assertEventuallyReceived(t *testing.T, clientURL string, received <-chan string, body string) {
	publisher, err := nats.Connect(clientURL)
	require.NoError(t, err)
	t.Cleanup(publisher.Close)

	assert.Eventually(t, func() bool {
		if publisher.Publish("resilient", []byte(body)) != nil {
			return false
		}
		select {
		case got := <-received:
			return got == body
		case <-time.After(50 * time.Millisecond):
			return false
		}
	}, 10*time.Second, 10*time.Millisecond, "consumer stopped receiving after a transient error")
}

// A closed connection (e.g. reconnect attempts exhausted after a broker
// outage) must not end the consumer: it reconnects and resubscribes.
func TestNATSConsumer_RecoversFromClosedConnection(t *testing.T) {
	ns := runEmbeddedNatsServer(t)
	defer ns.Shutdown()

	consumer, received := startTestConsumer(t, ns.ClientURL())

	consumer.endpoint.mu.Lock()
	conn := consumer.endpoint.conn
	consumer.endpoint.mu.Unlock()
	conn.Close()

	assertEventuallyReceived(t, ns.ClientURL(), received, "after reconnect")
}

// An invalidated subscription on a live connection is replaced.
func TestNATSConsumer_RecoversFromInvalidSubscription(t *testing.T) {
	ns := runEmbeddedNatsServer(t)
	defer ns.Shutdown()

	consumer, received := startTestConsumer(t, ns.ClientURL())
	require.NoError(t, consumer.subscription().Unsubscribe())

	assertEventuallyReceived(t, ns.ClientURL(), received, "after resubscribe")
}

func TestRetryDelayIsBoundedExponentialBackoff(t *testing.T) {
	assert.Equal(t, minRetryDelay, retryDelay(0))
	assert.Equal(t, 2*minRetryDelay, retryDelay(1))
	assert.Equal(t, maxRetryDelay, retryDelay(100))
}

func TestNATSConsumer_StopReleasesEndpointRegistration(t *testing.T) {
	component := NewNATSComponent()
	endpoint, err := component.CreateEndpoint("nats://restart")
	assert.NoError(t, err)

	consumer, err := endpoint.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	assert.NoError(t, err)
	assert.NoError(t, consumer.Stop())

	_, err = endpoint.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	assert.NoError(t, err)
}

func TestNATSEndpoint_SharedConnectionReferenceCounting(t *testing.T) {
	ns := runEmbeddedNatsServer(t)
	defer ns.Shutdown()

	comp := NewNATSComponent()
	ep, err := comp.CreateEndpoint("nats://shared?servers=" + ns.ClientURL())
	require.NoError(t, err)
	ne := ep.(*NATSEndpoint)

	p1, err := ep.CreateProducer()
	require.NoError(t, err)
	p2, err := ep.CreateProducer()
	require.NoError(t, err)
	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, p1.Start(ctx))
	require.NoError(t, p2.Start(ctx))
	require.NoError(t, consumer.Start(ctx))
	assert.Equal(t, 3, ne.connUsers)

	// Stopping p1 and consumer (including duplicate Stop calls) keeps p2's connection alive.
	require.NoError(t, p1.Stop())
	require.NoError(t, p1.Stop())
	assert.Equal(t, 2, ne.connUsers)

	require.NoError(t, consumer.Stop())
	require.NoError(t, consumer.Stop())
	assert.Equal(t, 1, ne.connUsers)
	require.NotNil(t, ne.conn)
	assert.False(t, ne.conn.IsClosed())

	ex := gocamel.NewExchange(ctx)
	ex.In.SetBody("still open")
	require.NoError(t, p2.Send(ex))

	require.NoError(t, p2.Stop())
	assert.Equal(t, 0, ne.connUsers)
	assert.Nil(t, ne.conn)
}

func TestNATSProducer_SendRejectsUnstartedOrStoppedProducerWithSharedConn(t *testing.T) {
	ns := runEmbeddedNatsServer(t)
	defer ns.Shutdown()

	comp := NewNATSComponent()
	ep, err := comp.CreateEndpoint("nats://shared?servers=" + ns.ClientURL())
	require.NoError(t, err)

	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	require.NoError(t, err)
	require.NoError(t, consumer.Start(context.Background()))
	defer consumer.Stop()

	prod, err := ep.CreateProducer()
	require.NoError(t, err)

	ex := gocamel.NewExchange(context.Background())
	ex.In.SetBody("should fail")
	err = prod.Send(ex)
	assert.ErrorContains(t, err, "nats producer is not started")

	require.NoError(t, prod.Start(context.Background()))
	require.NoError(t, prod.Send(ex))

	require.NoError(t, prod.Stop())
	err = prod.Send(ex)
	assert.ErrorContains(t, err, "nats producer is not started")
}

func TestNATSEndpoint_RedactsCredentialsInServersParam(t *testing.T) {
	comp := NewNATSComponent()

	t.Run("redacts user:pass and token in comma-separated servers", func(t *testing.T) {
		ep, err := comp.CreateEndpoint("nats://orders?servers=nats://alice:super-secret@broker1:4222,nats://secret-token@broker2:4222")
		require.NoError(t, err)

		uri := ep.URI()
		assert.NotContains(t, uri, "super-secret")
		assert.NotContains(t, uri, "secret-token")
		assert.Contains(t, uri, "broker1:4222")
		assert.Contains(t, uri, "broker2:4222")

		parsed, err := url.Parse(uri)
		require.NoError(t, err)
		assert.Equal(t, "nats://alice:xxxxx@broker1:4222,nats://xxxxx@broker2:4222", parsed.Query().Get("servers"))

		// Multiple consumers error uses the redacted URI.
		_, err = ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
		require.NoError(t, err)
		_, err = ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "super-secret")
		assert.NotContains(t, err.Error(), "secret-token")
	})

	t.Run("redacts schemeless servers and outer credential params", func(t *testing.T) {
		ep, err := comp.CreateEndpoint("nats://orders?servers=bob:pass123@broker1:4222,tok999@broker2:4222&token=outer-secret")
		require.NoError(t, err)

		uri := ep.URI()
		assert.NotContains(t, uri, "pass123")
		assert.NotContains(t, uri, "tok999")
		assert.NotContains(t, uri, "outer-secret")

		parsed, err := url.Parse(uri)
		require.NoError(t, err)
		assert.Equal(t, "bob:xxxxx@broker1:4222,xxxxx@broker2:4222", parsed.Query().Get("servers"))
		assert.Equal(t, "xxxxx", parsed.Query().Get("token"))
	})

	t.Run("redacts malformed percent-encoded password inside servers", func(t *testing.T) {
		ep := &NATSEndpoint{uri: "nats://orders?servers=nats://alice:bad%pass@broker1:4222,nats://tok%en@broker2:4222"}
		uri := ep.URI()
		assert.NotContains(t, uri, "bad%pass")
		assert.NotContains(t, uri, "tok%en")
		assert.Contains(t, uri, "alice:xxxxx@broker1:4222")
		assert.Contains(t, uri, "xxxxx@broker2:4222")
	})
}

func startHangingTCPListener(t *testing.T) (addr string, accepted <-chan struct{}) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	acceptedCh := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	var conns []net.Conn

	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		for _, c := range conns {
			_ = c.Close()
		}
		mu.Unlock()
	})

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
			once.Do(func() { close(acceptedCh) })
		}
	}()

	return ln.Addr().String(), acceptedCh
}

func TestNATS_ConnectRespectsContextOutsideLock(t *testing.T) {
	t.Run("producer connect does not hold endpoint lock and aborts on context cancel", func(t *testing.T) {
		addr, accepted := startHangingTCPListener(t)
		ep, err := NewNATSComponent().CreateEndpoint("nats://orders?servers=nats://" + addr)
		require.NoError(t, err)
		ne := ep.(*NATSEndpoint)

		prod, err := ep.CreateProducer()
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		startErr := make(chan error, 1)
		go func() {
			startErr <- prod.Start(ctx)
		}()

		select {
		case <-accepted:
		case <-time.After(2 * time.Second):
			t.Fatal("nats.Connect did not dial listener")
		}

		// endpoint.mu must not be held while connecting.
		lockAcquired := make(chan bool, 1)
		go func() {
			ne.mu.Lock()
			isNil := ne.conn == nil
			ne.mu.Unlock()
			lockAcquired <- isNil
		}()
		select {
		case isNil := <-lockAcquired:
			assert.True(t, isNil)
		case <-time.After(100 * time.Millisecond):
			t.Fatal("NATSEndpoint.mu is held during NATS connect")
		}

		// Cancelling ctx must abort Start promptly instead of waiting for nats.DefaultTimeout (2s).
		cancelStart := time.Now()
		cancel()
		select {
		case err := <-startErr:
			require.Error(t, err)
			assert.ErrorIs(t, err, context.Canceled)
			assert.Less(t, time.Since(cancelStart), 500*time.Millisecond)
		case <-time.After(500 * time.Millisecond):
			t.Fatal("NATSProducer.Start did not abort promptly on context cancellation")
		}
	})

	t.Run("consumer connect respects context outside lock and serializes Start/Stop", func(t *testing.T) {
		addr, accepted := startHangingTCPListener(t)
		ep, err := NewNATSComponent().CreateEndpoint("nats://orders?servers=nats://" + addr)
		require.NoError(t, err)
		ne := ep.(*NATSEndpoint)

		consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		startErr := make(chan error, 1)
		go func() {
			startErr <- consumer.Start(ctx)
		}()

		select {
		case <-accepted:
		case <-time.After(2 * time.Second):
			t.Fatal("nats.Connect did not dial listener")
		}

		// endpoint.mu must not be held while connecting.
		lockAcquired := make(chan bool, 1)
		go func() {
			ne.mu.Lock()
			isNil := ne.conn == nil
			ne.mu.Unlock()
			lockAcquired <- isNil
		}()
		select {
		case isNil := <-lockAcquired:
			assert.True(t, isNil)
		case <-time.After(100 * time.Millisecond):
			t.Fatal("NATSEndpoint.mu is held during NATSConsumer connect")
		}

		// Concurrent Stop must wait for the in-progress Start transition rather than returning early.
		stopDone := make(chan error, 1)
		go func() {
			stopDone <- consumer.Stop()
		}()
		select {
		case <-stopDone:
			t.Fatal("NATSConsumer.Stop returned before in-progress Start finished")
		case <-time.After(50 * time.Millisecond):
		}

		cancel()
		select {
		case err := <-startErr:
			require.Error(t, err)
			assert.ErrorIs(t, err, context.Canceled)
		case <-time.After(500 * time.Millisecond):
			t.Fatal("NATSConsumer.Start did not abort promptly on context cancellation")
		}
		select {
		case err := <-stopDone:
			require.NoError(t, err)
		case <-time.After(500 * time.Millisecond):
			t.Fatal("NATSConsumer.Stop did not finish after Start aborted")
		}

		ne.mu.Lock()
		assert.Nil(t, ne.consumer)
		ne.mu.Unlock()
	})
}

func TestNATS_AutoReconnectSurvivesStartupContextCancel(t *testing.T) {
	t.Run("detaches startup ctx and clears dialed conns on connect completion", func(t *testing.T) {
		ns := runEmbeddedNatsServer(t)
		defer ns.Shutdown()

		ep, err := NewNATSComponent().CreateEndpoint("nats://orders?servers=" + ns.ClientURL())
		require.NoError(t, err)
		ne := ep.(*NATSEndpoint)

		prod, err := ep.CreateProducer()
		require.NoError(t, err)

		startCtx, cancelStart := context.WithCancel(context.Background())
		require.NoError(t, prod.Start(startCtx))
		t.Cleanup(func() { _ = prod.Stop() })

		ne.mu.Lock()
		conn := ne.conn
		ne.mu.Unlock()
		require.NotNil(t, conn)

		cd, ok := conn.Opts.CustomDialer.(*contextDialer)
		require.True(t, ok)

		cd.mu.Lock()
		assert.Empty(t, cd.conns, "conns must be cleared once connectNATS succeeds")
		cd.mu.Unlock()

		// Cancelling the startup context after Start must not break subsequent reconnect dials.
		cancelStart()

		dialed, err := cd.Dial("tcp", ns.Addr().String())
		require.NoError(t, err, "custom dialer must remain usable after startup context is cancelled")
		_ = dialed.Close()

		cd.mu.Lock()
		assert.Empty(t, cd.conns, "post-completion dials must not accumulate sockets in conns")
		cd.mu.Unlock()
	})

	t.Run("nats.Conn auto-reconnects after broker restart when startup ctx is cancelled", func(t *testing.T) {
		ns1 := runEmbeddedNatsServer(t)
		tcpAddr, ok := ns1.Addr().(*net.TCPAddr)
		require.True(t, ok)
		port := tcpAddr.Port
		clientURL := ns1.ClientURL()

		ep, err := NewNATSComponent().CreateEndpoint("nats://reconnected?servers=" + clientURL)
		require.NoError(t, err)
		ne := ep.(*NATSEndpoint)

		prod, err := ep.CreateProducer()
		require.NoError(t, err)

		startCtx, cancelStart := context.WithCancel(context.Background())
		require.NoError(t, prod.Start(startCtx))
		t.Cleanup(func() { _ = prod.Stop() })

		ne.mu.Lock()
		conn := ne.conn
		ne.mu.Unlock()
		require.NotNil(t, conn)

		// Cancel startup context once Producer.Start has returned.
		cancelStart()

		// Restart the broker on the same port to trigger nats.go's background reconnect loop.
		ns1.Shutdown()
		ns1.WaitForShutdown()

		ns2, err := server.NewServer(&server.Options{
			Host: "127.0.0.1",
			Port: port,
		})
		require.NoError(t, err)
		go ns2.Start()
		defer ns2.Shutdown()
		require.True(t, ns2.ReadyForConnections(5*time.Second), "restarted NATS server did not become ready")

		require.Eventually(t, func() bool {
			return conn.Status() == nats.CONNECTED && conn.Stats().Reconnects >= 1
		}, 6*time.Second, 20*time.Millisecond, "nats.Conn failed to auto-reconnect after startup context cancellation")

		subConn, err := nats.Connect(ns2.ClientURL())
		require.NoError(t, err)
		defer subConn.Close()
		sub, err := subConn.SubscribeSync("reconnected")
		require.NoError(t, err)
		require.NoError(t, subConn.Flush())

		ex := gocamel.NewExchange(context.Background())
		ex.In.SetBody("after-restart")
		require.NoError(t, prod.Send(ex))

		msg, err := sub.NextMsg(2 * time.Second)
		require.NoError(t, err)
		assert.Equal(t, "after-restart", string(msg.Data))
	})
}
