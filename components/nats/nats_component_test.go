package nats

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/assert"

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

	// Generic producer route
	_, err = camelCtx.CreateRouteBuilder().
		From("direct:publish").
		To("nats://?servers=" + clientURL).
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
}
