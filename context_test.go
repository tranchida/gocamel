package gocamel

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
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

func TestStdlibComponentsAreRegisteredByDefault(t *testing.T) {
	ctx := NewCamelContext()
	_, err := ctx.CreateEndpoint("direct:auto")
	require.NoError(t, err)
	_, err = ctx.CreateEndpoint("timer:tick")
	require.NoError(t, err)
}

func TestRouteSetID_ReleasesPreviousID(t *testing.T) {
	ctx := NewCamelContext()
	route := ctx.CreateRoute()
	old := route.ID
	require.NotEmpty(t, old)
	route.SetID("renamed")
	assert.Zero(t, ctx.usedRouteIDs[old])
	assert.Equal(t, 1, ctx.usedRouteIDs["renamed"])
	assert.Equal(t, "renamed", route.ID)
}

func TestAddRoute_DuplicateIDWarnsButKeepsBoth(t *testing.T) {
	ctx := NewCamelContext()
	a := NewRoute()
	a.ID = "dup"
	b := NewRoute()
	b.ID = "dup"
	ctx.AddRoute(a)
	ctx.AddRoute(b)
	assert.Equal(t, 2, ctx.GetRouteCount())
	assert.Equal(t, a, ctx.GetRoute("dup"))
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

func TestCamelContext_StartRejectedWhileGracefulStopInProgress(t *testing.T) {
	ctx := NewCamelContext()
	component := newGracefulStopTestComponent()
	t.Cleanup(component.unblock)
	ctx.AddComponent("shutdown-test", component)

	ctx.CreateRoute().From("shutdown-test:source")
	require.NoError(t, ctx.Start())

	err := ctx.GracefulStop(10 * time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")

	err = ctx.Start()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "still stopping")

	component.unblock()
	require.Eventually(t, func() bool {
		ctx.startLock.RLock()
		defer ctx.startLock.RUnlock()
		return !ctx.stopping
	}, time.Second, 10*time.Millisecond)
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

func TestRoute_Validate_JoinsErrors(t *testing.T) {
	r := NewRoute()
	r.err = fmt.Errorf("bad from")
	err := r.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad from")
	assert.Contains(t, err.Error(), "no source endpoint")
}

func TestCamelContext_RemoveRouteStopsAndReleasesID(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	route := ctx.CreateRoute().From("direct:remove-me")
	route.SetID("custom-id")
	require.NoError(t, ctx.Start())
	require.True(t, route.IsStarted())

	require.NoError(t, ctx.RemoveRoute(route))
	assert.False(t, route.IsStarted())
	assert.Nil(t, ctx.GetRoute("custom-id"))

	// The released ID can be reused by a later auto-generated assignment
	// only for empty IDs; an explicit SetID of the same value is still allowed.
	next := ctx.CreateRoute()
	assert.NotEqual(t, "custom-id", next.ID)
	assert.Zero(t, ctx.usedRouteIDs["custom-id"])
}

type slowStartComponent struct {
	delay time.Duration
}

func (c *slowStartComponent) CreateEndpoint(uri string) (Endpoint, error) {
	return &slowStartEndpoint{uri: uri, delay: c.delay}, nil
}

type slowStartEndpoint struct {
	uri   string
	delay time.Duration
}

func (e *slowStartEndpoint) URI() string                       { return e.uri }
func (e *slowStartEndpoint) CreateProducer() (Producer, error) { return &stubProducer{}, nil }
func (e *slowStartEndpoint) CreateConsumer(Processor) (Consumer, error) {
	return &slowStartConsumer{delay: e.delay}, nil
}

type slowStartConsumer struct {
	delay time.Duration
}

func (c *slowStartConsumer) Start(context.Context) error {
	time.Sleep(c.delay)
	return nil
}
func (c *slowStartConsumer) Stop() error { return nil }

func TestRemoveRoute_RacingStartAndDuplicateIDs(t *testing.T) {
	t.Run("racing Start and RemoveRoute never orphans a running route", func(t *testing.T) {
		for i := 0; i < 20; i++ {
			ctx := NewCamelContext()
			ctx.AddComponent("slow", &slowStartComponent{delay: 10 * time.Millisecond})

			route := ctx.CreateRoute().From("slow:source")
			route.SetID("raced-route")
			require.NoError(t, ctx.Start())
			require.NoError(t, route.Stop())

			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				_ = route.Start(ctx.GetContext())
			}()
			go func() {
				defer wg.Done()
				time.Sleep(2 * time.Millisecond)
				_ = ctx.RemoveRoute(route)
			}()
			wg.Wait()

			if route.IsStarted() && ctx.GetRoute("raced-route") == nil {
				t.Fatalf("iteration %d: route is running outside CamelContext (orphaned)", i)
			}
			require.NoError(t, ctx.Stop())
			assert.False(t, route.IsStarted(), "iteration %d: CamelContext.Stop must stop any remaining route", i)
		}
	})

	t.Run("usedRouteIDs tracks duplicates and ignores detached routes", func(t *testing.T) {
		ctx := NewCamelContext()

		r1 := NewRoute()
		r1.ID = "route-1"
		ctx.AddRoute(r1)

		r2 := NewRoute()
		r2.ID = "route-1"
		ctx.AddRoute(r2)
		assert.Equal(t, 2, ctx.usedRouteIDs["route-1"])

		require.NoError(t, ctx.RemoveRoute(r1))
		assert.Equal(t, 1, ctx.usedRouteIDs["route-1"])
		// Calling RemoveRoute a second time on a detached route must be a no-op.
		require.NoError(t, ctx.RemoveRoute(r1))
		assert.Equal(t, 1, ctx.usedRouteIDs["route-1"])

		// route-1 is still held by r2 while routeCounter == 0, so auto-ID must skip to route-2.
		r3 := NewRoute()
		ctx.AddRoute(r3)
		assert.Equal(t, "route-2", r3.ID)
		assert.Equal(t, r2, ctx.GetRoute("route-1"))

		// Renaming a detached context-owned route must be ignored and must not leak.
		r4 := ctx.CreateRoute()
		origID := r4.ID
		require.NoError(t, ctx.RemoveRoute(r4))
		r4.SetID("detached-leak")
		assert.Equal(t, origID, r4.ID)
		assert.Zero(t, ctx.usedRouteIDs["detached-leak"])
		assert.Zero(t, ctx.usedRouteIDs[origID])

		// Removing r2 releases route-1 completely.
		require.NoError(t, ctx.RemoveRoute(r2))
		assert.Zero(t, ctx.usedRouteIDs["route-1"])
	})
}

type activeLoopComponent struct {
	started chan struct{}
	sendErr atomic.Value
}

func (c *activeLoopComponent) CreateEndpoint(uri string) (Endpoint, error) {
	return &activeLoopEndpoint{uri: uri, comp: c}, nil
}

type activeLoopEndpoint struct {
	uri  string
	comp *activeLoopComponent
}

func (e *activeLoopEndpoint) URI() string { return e.uri }
func (e *activeLoopEndpoint) CreateProducer() (Producer, error) {
	return nil, fmt.Errorf("not supported")
}
func (e *activeLoopEndpoint) CreateConsumer(p Processor) (Consumer, error) {
	return &activeLoopConsumer{processor: p, comp: e.comp}, nil
}

type activeLoopConsumer struct {
	processor Processor
	comp      *activeLoopComponent
	stopOnce  sync.Once
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

func (c *activeLoopConsumer) Start(ctx context.Context) error {
	c.stopCh = make(chan struct{})
	var firstOnce sync.Once
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		for {
			select {
			case <-c.stopCh:
				return
			case <-ctx.Done():
				return
			default:
			}
			ex := NewExchange(ctx)
			if err := ProcessSafely(c.processor, ex); err != nil {
				c.comp.sendErr.CompareAndSwap(nil, err)
			}
			firstOnce.Do(func() { close(c.comp.started) })
		}
	}()
	return nil
}

func (c *activeLoopConsumer) Stop() error {
	c.stopOnce.Do(func() {
		if c.stopCh != nil {
			close(c.stopCh)
		}
	})
	c.wg.Wait()
	return nil
}

func TestCamelContext_StopResourcesStopsActiveBeforeDirect(t *testing.T) {
	ctx := NewCamelContext()
	active := &activeLoopComponent{started: make(chan struct{})}
	ctx.AddComponent("active", active)

	// Declare direct:sub BEFORE the active consumer calling it.
	_, err := ctx.CreateRouteBuilder().
		From("direct:sub").
		ProcessFunc(func(*Exchange) error {
			time.Sleep(15 * time.Millisecond)
			return nil
		}).
		Build()
	require.NoError(t, err)

	_, err = ctx.CreateRouteBuilder().
		From("active:src").
		To("direct:sub").
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	<-active.started
	require.NoError(t, ctx.Stop())

	if v := active.sendErr.Load(); v != nil {
		t.Fatalf("active route failed during Stop(): %v", v.(error))
	}
}
