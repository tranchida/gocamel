package gocamel

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultRouteID(t *testing.T) {
	ctx := NewCamelContext()

	// Add a route without ID
	route1 := NewRoute()
	ctx.AddRoute(route1)
	if route1.ID != "route-1" {
		t.Errorf("Expected route-1, got %s", route1.ID)
	}

	// Add another route without ID
	route2 := NewRoute()
	ctx.AddRoute(route2)
	if route2.ID != "route-2" {
		t.Errorf("Expected route-2, got %s", route2.ID)
	}

	// Add a route with a custom ID
	routeCustom := NewRoute()
	routeCustom.ID = "my-custom-route"
	ctx.AddRoute(routeCustom)
	if routeCustom.ID != "my-custom-route" {
		t.Errorf("Expected my-custom-route, got %s", routeCustom.ID)
	}

	// Add another route without ID
	route3 := NewRoute()
	ctx.AddRoute(route3)
	if route3.ID != "route-3" {
		t.Errorf("Expected route-3, got %s", route3.ID)
	}
}

func TestDefaultRouteIDConflict(t *testing.T) {
	ctx := NewCamelContext()

	// Add a route with an ID that would normally be generated
	routeConflict := NewRoute()
	routeConflict.ID = "route-1"
	ctx.AddRoute(routeConflict)

	// Add a route without ID; should skip route-1 and use route-2
	routeNext := NewRoute()
	ctx.AddRoute(routeNext)
	if routeNext.ID != "route-2" {
		t.Errorf("Expected route-2 to avoid conflict, got %s", routeNext.ID)
	}
}

// TestCamelContext_GracefulStop_NotStarted verifies that GracefulStop is a
// no-op on a context that was never started.
func TestCamelContext_GracefulStop_NotStarted(t *testing.T) {
	ctx := NewCamelContext()
	require.NoError(t, ctx.GracefulStop(time.Second))
}

// TestCamelContext_GracefulStop_Empty verifies that GracefulStop on a started
// context with no routes returns immediately.
func TestCamelContext_GracefulStop_Empty(t *testing.T) {
	ctx := NewCamelContext()
	require.NoError(t, ctx.Start())
	require.NoError(t, ctx.GracefulStop(time.Second))
	assert.False(t, ctx.IsStarted())
}

type gracefulStopTestComponent struct {
	releaseOnce sync.Once
	release     chan struct{}
	producer    *gracefulStopTestProducer
}

func newGracefulStopTestComponent() *gracefulStopTestComponent {
	return &gracefulStopTestComponent{
		release:  make(chan struct{}),
		producer: &gracefulStopTestProducer{stopped: make(chan struct{})},
	}
}

func (c *gracefulStopTestComponent) CreateEndpoint(uri string) (Endpoint, error) {
	return &gracefulStopTestEndpoint{uri: uri, component: c}, nil
}

func (c *gracefulStopTestComponent) unblock() {
	c.releaseOnce.Do(func() { close(c.release) })
}

type gracefulStopTestEndpoint struct {
	uri       string
	component *gracefulStopTestComponent
}

func (e *gracefulStopTestEndpoint) URI() string { return e.uri }
func (e *gracefulStopTestEndpoint) CreateProducer() (Producer, error) {
	return e.component.producer, nil
}
func (e *gracefulStopTestEndpoint) CreateConsumer(Processor) (Consumer, error) {
	return &gracefulStopTestConsumer{release: e.component.release}, nil
}

type gracefulStopTestConsumer struct{ release <-chan struct{} }

func (*gracefulStopTestConsumer) Start(context.Context) error { return nil }
func (c *gracefulStopTestConsumer) Stop() error {
	<-c.release
	return nil
}

type gracefulStopTestProducer struct {
	stopOnce sync.Once
	stopped  chan struct{}
}

func (*gracefulStopTestProducer) Start(context.Context) error { return nil }
func (p *gracefulStopTestProducer) Stop() error {
	p.stopOnce.Do(func() { close(p.stopped) })
	return nil
}
func (*gracefulStopTestProducer) Send(*Exchange) error { return nil }

func TestCamelContext_GracefulStopTimeoutEventuallyStopsProducerCache(t *testing.T) {
	ctx := NewCamelContext()
	component := newGracefulStopTestComponent()
	t.Cleanup(component.unblock)
	ctx.AddComponent("shutdown-test", component)

	ctx.CreateRoute().From("shutdown-test:source")
	require.NoError(t, ctx.Start())
	_, err := ctx.GetProducer("shutdown-test:sink")
	require.NoError(t, err)

	err = ctx.GracefulStop(10 * time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
	assert.False(t, ctx.IsStarted())

	select {
	case <-component.producer.stopped:
		t.Fatal("producer cache stopped before the route finished shutting down")
	default:
	}

	component.unblock()
	select {
	case <-component.producer.stopped:
	case <-time.After(time.Second):
		t.Fatal("producer cache was not stopped after route shutdown completed")
	}
}

// TestRoute_Validate_NoFrom surfaces missing source endpoint at validate time.
func TestRoute_Validate_NoFrom(t *testing.T) {
	ctx := NewCamelContext()
	r := ctx.CreateRoute()
	err := r.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no source endpoint")
}

// TestRoute_Validate_BadChoiceExpression surfaces a malformed Simple
// expression in a Choice when-clause at validate time instead of waiting
// for the first matching message.
func TestRoute_Validate_BadChoiceExpression(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	r := ctx.CreateRoute().From("direct:start")
	// Force the unparseable template by injecting an oversized expression
	// that ParseSimpleTemplate rejects via the MaxSimpleInputBytes guard.
	oversize := "${header." + strings.Repeat("a", MaxSimpleInputBytes+1) + "}"
	choice := NewChoiceProcessor().When(oversize, ProcessorFunc(func(*Exchange) error { return nil }))
	r.AddProcessor(choice)

	err := r.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "choice when")
}
