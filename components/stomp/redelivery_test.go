package stomp

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/go-stomp/stomp"
	"github.com/go-stomp/stomp/frame"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gocamel "github.com/tranchida/gocamel"
)

// sendRaw publishes a frame straight to the broker, bypassing the GoCamel
// producer (which never forwards Camel* headers).
func sendRaw(t *testing.T, addr, destination, body string, opts ...func(*frame.Frame) error) {
	t.Helper()
	conn, err := stomp.Dial("tcp", addr)
	require.NoError(t, err)
	defer func() { _ = conn.Disconnect() }()
	require.NoError(t, conn.Send(destination, "text/plain", []byte(body), opts...))
}

func TestStompConsumer_GivesUpOnPoisonMessage(t *testing.T) {
	addr := startTestBroker(t)
	camelCtx := gocamel.NewCamelContext()
	camelCtx.AddComponent("stomp", NewStompComponent())

	var mu sync.Mutex
	attempts := map[string]int{}
	healthy := make(chan struct{}, 1)
	_, err := camelCtx.CreateRouteBuilder().
		From("stomp://queue/poison?host=" + addr + "&maxRedeliveries=2&redeliveryDelay=1&maxRedeliveryDelay=1").
		ProcessFunc(func(e *gocamel.Exchange) error {
			body := string(e.GetBody().([]byte))
			mu.Lock()
			attempts[body]++
			mu.Unlock()
			if body == "poison" {
				return errors.New("cannot process")
			}
			healthy <- struct{}{}
			return nil
		}).
		Build()
	require.NoError(t, err)
	require.NoError(t, camelCtx.Start())
	t.Cleanup(func() { _ = camelCtx.Stop() })

	sendRaw(t, addr, "/queue/poison", "poison")
	sendRaw(t, addr, "/queue/poison", "healthy")

	select {
	case <-healthy:
	case <-time.After(5 * time.Second):
		t.Fatal("poison message blocked the subscription")
	}
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 3, attempts["poison"], "1 delivery + 2 redeliveries")
	assert.Equal(t, 1, attempts["healthy"])
}

func TestStompConsumer_DeadLetteredMessageIsNotRedelivered(t *testing.T) {
	addr := startTestBroker(t)
	camelCtx := gocamel.NewCamelContext()
	camelCtx.AddComponent("stomp", NewStompComponent())

	dlq := make(chan string, 10)
	_, err := camelCtx.CreateRouteBuilder().From("direct:dlq").ProcessFunc(func(e *gocamel.Exchange) error {
		dlq <- string(e.GetBody().([]byte))
		return nil
	}).Build()
	require.NoError(t, err)
	route, err := camelCtx.CreateRouteBuilder().
		From("stomp://queue/dlc?host=" + addr + "&redeliveryDelay=1").
		ProcessFunc(func(*gocamel.Exchange) error { return errors.New("business failure") }).
		Build()
	require.NoError(t, err)
	route.SetErrorHandler(gocamel.NewDeadLetterChannel(camelCtx, "direct:dlq"))
	require.NoError(t, camelCtx.Start())
	t.Cleanup(func() { _ = camelCtx.Stop() })

	sendRaw(t, addr, "/queue/dlc", "order-1")

	select {
	case body := <-dlq:
		assert.Equal(t, "order-1", body)
	case <-time.After(5 * time.Second):
		t.Fatal("message was not dead-lettered")
	}
	// Under the previous behaviour the consumer redelivered every 100ms,
	// dead-lettering a duplicate each time.
	select {
	case body := <-dlq:
		t.Fatalf("message dead-lettered twice: %s", body)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestStompConsumer_DropsInboundCamelHeaders(t *testing.T) {
	addr := startTestBroker(t)
	camelCtx := gocamel.NewCamelContext()
	camelCtx.AddComponent("stomp", NewStompComponent())

	received := make(chan map[string]any, 1)
	_, err := camelCtx.CreateRouteBuilder().
		From("stomp://queue/headers?host=" + addr).
		ProcessFunc(func(e *gocamel.Exchange) error {
			received <- e.GetIn().GetHeaders()
			return nil
		}).
		Build()
	require.NoError(t, err)
	require.NoError(t, camelCtx.Start())
	t.Cleanup(func() { _ = camelCtx.Stop() })

	sendRaw(t, addr, "/queue/headers", "x",
		stomp.SendOpt.Header("CamelFileName", "../../etc/cron.d/evil"),
		stomp.SendOpt.Header("camelHttpMethod", "DELETE"),
		stomp.SendOpt.Header("x-trace", "abc"))

	select {
	case headers := <-received:
		assert.NotContains(t, headers, "CamelFileName")
		assert.NotContains(t, headers, "camelHttpMethod")
		assert.Equal(t, "abc", headers["x-trace"])
		assert.Equal(t, "/queue/headers", headers[CamelStompDestination])
	case <-time.After(5 * time.Second):
		t.Fatal("message not received")
	}
}
