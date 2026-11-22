package gocamel

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDynamicRouter_Basic(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	var steps []string

	// Control function: decides next step based on a counter in exchange property
	controlFn := func(e *Exchange) (string, error) {
		val, _ := e.GetProperty("step_count")
		count, _ := val.(int)
		e.SetProperty("step_count", count+1)

		switch count {
		case 0:
			return "direct:step1", nil
		case 1:
			return "direct:step2", nil
		default:
			return "", nil // End routing
		}
	}

	_, err := ctx.CreateRouteBuilder().
		From("direct:start").
		DynamicRouter(controlFn).
		Build()
	require.NoError(t, err)

	_, err = ctx.CreateRouteBuilder().
		From("direct:step1").
		ProcessFunc(func(e *Exchange) error {
			steps = append(steps, "step1")
			return nil
		}).
		Build()
	require.NoError(t, err)

	_, err = ctx.CreateRouteBuilder().
		From("direct:step2").
		ProcessFunc(func(e *Exchange) error {
			steps = append(steps, "step2")
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	defer ctx.Stop()

	ex := NewExchange(context.Background())
	p, _ := ctx.CreateEndpoint("direct:start")
	prod, _ := p.CreateProducer()
	prod.Start(context.Background())
	prod.Send(ex)
	prod.Stop()

	assert.Equal(t, []string{"step1", "step2"}, steps)
}

func TestDynamicRouter_Simple(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	var received string

	// Use Simple expression to resolve next URI from a header
	// The header "next" is updated in each step.
	_, err := ctx.CreateRouteBuilder().
		From("direct:start").
		DynamicRouter("${header.next}").
		Build()
	require.NoError(t, err)

	_, err = ctx.CreateRouteBuilder().
		From("direct:step1").
		ProcessFunc(func(e *Exchange) error {
			e.GetIn().SetHeader("next", "direct:step2")
			return nil
		}).
		Build()
	require.NoError(t, err)

	_, err = ctx.CreateRouteBuilder().
		From("direct:step2").
		ProcessFunc(func(e *Exchange) error {
			received = "done"
			e.GetIn().RemoveHeader("next") // This will result in empty string -> end
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, ctx.Start())
	defer ctx.Stop()

	ex := NewExchange(context.Background())
	ex.GetIn().SetHeader("next", "direct:step1")

	p, _ := ctx.CreateEndpoint("direct:start")
	prod, _ := p.CreateProducer()
	prod.Start(context.Background())
	err = prod.Send(ex)
	require.NoError(t, err)
	prod.Stop()

	assert.Equal(t, "done", received)
}

var errDrStopAfterHop = errors.New("stop after test hop")

type drSpyComponent struct {
	mu          sync.Mutex
	uris        []string
	stopOnCount int
}

func (c *drSpyComponent) CreateEndpoint(uri string) (Endpoint, error) {
	return &drSpyEndpoint{comp: c, uri: uri}, nil
}

func (c *drSpyComponent) record(uri string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopOnCount > 0 && len(c.uris) >= c.stopOnCount {
		return errDrStopAfterHop
	}
	c.uris = append(c.uris, uri)
	return nil
}

func (c *drSpyComponent) recorded() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.uris))
	copy(out, c.uris)
	return out
}

type drSpyEndpoint struct {
	comp *drSpyComponent
	uri  string
}

func (e *drSpyEndpoint) URI() string { return e.uri }
func (e *drSpyEndpoint) CreateProducer() (Producer, error) {
	return &drSpyProducer{comp: e.comp, uri: e.uri}, nil
}
func (e *drSpyEndpoint) CreateConsumer(Processor) (Consumer, error) {
	return &drSpyConsumer{}, nil
}

type drSpyConsumer struct{}

func (c *drSpyConsumer) Start(context.Context) error { return nil }
func (c *drSpyConsumer) Stop() error                 { return nil }

type drSpyProducer struct {
	comp *drSpyComponent
	uri  string
}

func (p *drSpyProducer) Start(context.Context) error { return nil }
func (p *drSpyProducer) Stop() error                 { return nil }
func (p *drSpyProducer) Send(*Exchange) error {
	return p.comp.record(p.uri)
}

func TestDynamicRouter_InjectionClosed(t *testing.T) {
	t.Run("path option injection is escaped", func(t *testing.T) {
		ctx := NewCamelContext()
		spy := &drSpyComponent{stopOnCount: 1}
		ctx.AddComponent("direct", spy)

		route, err := ctx.CreateRouteBuilder().
			From("direct:start").
			DynamicRouter("direct:next-${header.x}").
			Build()
		require.NoError(t, err)
		require.NoError(t, ctx.Start())
		t.Cleanup(func() { _ = ctx.Stop() })

		ex := NewExchange(context.Background())
		ex.GetIn().SetHeader("x", "step?allowHeaderOverride=true#frag")

		err = route.Process(ex)
		require.ErrorIs(t, err, errDrStopAfterHop)
		require.Len(t, spy.recorded(), 1)
		got := spy.recorded()[0]
		assert.Equal(t, "direct:next-step%3FallowHeaderOverride=true%23frag", got)

		u, err := ParseURI(got)
		require.NoError(t, err)
		assert.Empty(t, u.RawQuery)
		assert.Empty(t, u.Query().Get("allowHeaderOverride"))
	})

	t.Run("leading placeholder query injection and authority SSRF are escaped", func(t *testing.T) {
		ctx := NewCamelContext()
		drLead := NewSimpleDynamicRouter(ctx, "${header.base}?q=${header.user}")
		ex := NewExchange(context.Background())
		ex.GetIn().SetHeader("base", "http://api.internal/next")
		ex.GetIn().SetHeader("user", "x&allowHeaderOverride=true")

		got, err := drLead.resolveNextURI(ex)
		require.NoError(t, err)
		assert.Equal(t, "http://api.internal/next?q=x%26allowHeaderOverride%3Dtrue", got)

		drAuth := NewSimpleDynamicRouter(ctx, "http://${header.tenant}.internal/next")
		ex2 := NewExchange(context.Background())
		ex2.GetIn().SetHeader("tenant", "169.254.169.254/latest/")

		got2, err := drAuth.resolveNextURI(ex2)
		require.NoError(t, err)
		assert.Equal(t, "http://169.254.169.254%2Flatest%2F.internal/next", got2)
	})

	t.Run("missing header resolves to empty string and ends routing", func(t *testing.T) {
		ctx := NewCamelContext()
		dr := NewSimpleDynamicRouter(ctx, "${header.missing}")
		got, err := dr.resolveNextURI(NewExchange(context.Background()))
		require.NoError(t, err)
		assert.Equal(t, "", got)
	})
}

func TestDynamicRouter_BuildAndValidateRejectMalformedTemplate(t *testing.T) {
	for _, expr := range []string{
		"direct:${header.x",
		"direct:${}",
		"${header." + strings.Repeat("a", MaxSimpleInputBytes+1) + "}",
	} {
		ctx := NewCamelContext()
		_, err := ctx.CreateRouteBuilder().
			From("direct:start").
			DynamicRouter(expr).
			Build()
		require.Error(t, err)

		dr := NewSimpleDynamicRouter(ctx, expr)
		v, ok := any(dr).(Validator)
		require.True(t, ok, "DynamicRouter must implement Validator")
		require.Error(t, v.Validate())
	}
}
