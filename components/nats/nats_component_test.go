package nats

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gocamel "gitlab.com/tranchida/gocamel"
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
