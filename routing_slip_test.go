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

func TestRoutingSlip_StaticURIsSequentiallyPropagatesBody(t *testing.T) {
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	_, err := camel.CreateRouteBuilder().
		From("direct:a").
		ProcessFunc(func(e *Exchange) error {
			body, _ := e.GetBodyAsString()
			e.GetOut().SetBody(body + "-a")
			return nil
		}).
		Build()
	require.NoError(t, err)

	_, err = camel.CreateRouteBuilder().
		From("direct:b").
		ProcessFunc(func(e *Exchange) error {
			body, _ := e.GetBodyAsString()
			e.GetOut().SetBody(body + "-b")
			return nil
		}).
		Build()
	require.NoError(t, err)

	var got string
	route, err := camel.CreateRouteBuilder().
		From("direct:start").
		RoutingSlip("direct:a,direct:b").
		ProcessFunc(func(e *Exchange) error {
			got, _ = e.GetBodyAsString()
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, camel.Start())
	defer camel.Stop()

	exchange := NewExchange(context.Background())
	exchange.SetBody("start")
	require.NoError(t, route.Process(exchange))

	assert.Equal(t, "start-a-b", got)
}

func TestRoutingSlip_ClearsOutBetweenSteps(t *testing.T) {
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	_, err := camel.CreateRouteBuilder().
		From("direct:a").
		ProcessFunc(func(e *Exchange) error {
			e.GetOut().SetBody("from-out")
			return nil
		}).
		Build()
	require.NoError(t, err)

	_, err = camel.CreateRouteBuilder().
		From("direct:b").
		ProcessFunc(func(e *Exchange) error {
			body, _ := e.GetBodyAsString()
			e.GetIn().SetBody(body + "-mutated-in")
			return nil
		}).
		Build()
	require.NoError(t, err)

	route, err := camel.CreateRouteBuilder().
		From("direct:start").
		RoutingSlip("direct:a,direct:b").
		Build()
	require.NoError(t, err)

	require.NoError(t, camel.Start())
	defer camel.Stop()

	exchange := NewExchange(context.Background())
	require.NoError(t, route.Process(exchange))

	assert.Equal(t, "from-out-mutated-in", exchange.GetIn().GetBody())
	assert.False(t, exchange.HasOut(), "routing slip should not leave a stale Out message")
}

func TestRoutingSlip_DynamicViaHeader(t *testing.T) {
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	var calls []string
	for _, name := range []string{"x", "y"} {
		targetName := name
		_, err := camel.CreateRouteBuilder().
			From("direct:" + targetName).
			ProcessFunc(func(e *Exchange) error {
				calls = append(calls, targetName)
				return nil
			}).
			Build()
		require.NoError(t, err)
	}

	route, err := camel.CreateRouteBuilder().
		From("direct:start").
		ProcessFunc(func(e *Exchange) error {
			e.GetIn().SetHeader("slip", "direct:x,direct:y")
			return nil
		}).
		RoutingSlip("${header.slip}").
		Build()
	require.NoError(t, err)

	require.NoError(t, camel.Start())
	defer camel.Stop()

	require.NoError(t, route.Process(NewExchange(context.Background())))
	assert.Equal(t, []string{"x", "y"}, calls)
}

func TestRoutingSlip_GoFunction(t *testing.T) {
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	var calls []string
	for _, name := range []string{"p", "q"} {
		targetName := name
		_, err := camel.CreateRouteBuilder().
			From("direct:" + targetName).
			ProcessFunc(func(e *Exchange) error {
				calls = append(calls, targetName)
				return nil
			}).
			Build()
		require.NoError(t, err)
	}

	route, err := camel.CreateRouteBuilder().
		From("direct:start").
		RoutingSlip(func(e *Exchange) ([]string, error) {
			return []string{"direct:p", "direct:q"}, nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, camel.Start())
	defer camel.Stop()

	require.NoError(t, route.Process(NewExchange(context.Background())))
	assert.Equal(t, []string{"p", "q"}, calls)
}

func TestRoutingSlip_Properties(t *testing.T) {
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	var indices []int
	var sizes []int
	var completes []bool
	for _, name := range []string{"a", "b"} {
		_, err := camel.CreateRouteBuilder().
			From("direct:" + name).
			ProcessFunc(func(e *Exchange) error {
				index, _ := e.GetPropertyAsInt(CamelRoutingSlipIndex)
				size, _ := e.GetPropertyAsInt(CamelRoutingSlipSize)
				complete, _ := e.GetPropertyAsBool(CamelRoutingSlipComplete)
				indices = append(indices, index)
				sizes = append(sizes, size)
				completes = append(completes, complete)
				return nil
			}).
			Build()
		require.NoError(t, err)
	}

	route, err := camel.CreateRouteBuilder().
		From("direct:start").
		RoutingSlip("direct:a,direct:b").
		Build()
	require.NoError(t, err)

	require.NoError(t, camel.Start())
	defer camel.Stop()

	require.NoError(t, route.Process(NewExchange(context.Background())))

	assert.Equal(t, []int{0, 1}, indices)
	assert.Equal(t, []int{2, 2}, sizes)
	assert.Equal(t, []bool{false, true}, completes)
}

func TestRoutingSlip_URIFunctionError(t *testing.T) {
	rs := NewRoutingSlip(NewCamelContext()).SetURIFunc(func(e *Exchange) ([]string, error) {
		return nil, errors.New("boom")
	})

	err := rs.Process(NewExchange(context.Background()))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "routing slip URI function error")
}

func TestRouteBuilder_RoutingSlipUnsupportedType(t *testing.T) {
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	route, err := camel.CreateRouteBuilder().
		From("direct:start").
		RoutingSlip(42).
		Build()

	assert.Nil(t, route)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported RoutingSlip expression type")
}

type rsSpyComponent struct {
	mu   sync.Mutex
	uris []string
}

func (c *rsSpyComponent) CreateEndpoint(uri string) (Endpoint, error) {
	return &rsSpyEndpoint{comp: c, uri: uri}, nil
}

func (c *rsSpyComponent) record(uri string) {
	c.mu.Lock()
	c.uris = append(c.uris, uri)
	c.mu.Unlock()
}

func (c *rsSpyComponent) recorded() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.uris))
	copy(out, c.uris)
	return out
}

type rsSpyEndpoint struct {
	comp *rsSpyComponent
	uri  string
}

func (e *rsSpyEndpoint) URI() string { return e.uri }
func (e *rsSpyEndpoint) CreateProducer() (Producer, error) {
	return &rsSpyProducer{comp: e.comp, uri: e.uri}, nil
}
func (e *rsSpyEndpoint) CreateConsumer(Processor) (Consumer, error) {
	return &rsSpyConsumer{}, nil
}

type rsSpyConsumer struct{}

func (c *rsSpyConsumer) Start(context.Context) error { return nil }
func (c *rsSpyConsumer) Stop() error                 { return nil }

type rsSpyProducer struct {
	comp *rsSpyComponent
	uri  string
}

func (p *rsSpyProducer) Start(context.Context) error { return nil }
func (p *rsSpyProducer) Stop() error                 { return nil }
func (p *rsSpyProducer) Send(*Exchange) error {
	p.comp.record(p.uri)
	return nil
}

func TestRoutingSlip_InjectionClosed(t *testing.T) {
	t.Run("comma and query option injection is escaped and never invokes exec", func(t *testing.T) {
		ctx := NewCamelContext()
		directSpy := &rsSpyComponent{}
		execSpy := &rsSpyComponent{}
		ctx.AddComponent("direct", directSpy)
		ctx.AddComponent("exec", execSpy)

		route, err := ctx.CreateRouteBuilder().
			From("direct:start").
			RoutingSlip("direct:step-${header.step},direct:done").
			Build()
		require.NoError(t, err)
		require.NoError(t, ctx.Start())
		t.Cleanup(func() { _ = ctx.Stop() })

		ex := NewExchange(context.Background())
		ex.GetIn().SetHeader("step", "a,exec:echo?args=pwn")

		require.NoError(t, route.Process(ex))
		assert.Empty(t, execSpy.recorded(), "injected exec endpoint must never be invoked")
		assert.Equal(t, []string{
			"direct:step-a%2Cexec:echo%3Fargs=pwn",
			"direct:done",
		}, directSpy.recorded())
	})

	t.Run("leading placeholder query injection and authority SSRF in routing slip", func(t *testing.T) {
		ctx := NewCamelContext()
		rs := NewRoutingSlip(ctx).SetSimpleExpression("${header.base}?q=${header.user},direct:done")
		ex := NewExchange(context.Background())
		ex.GetIn().SetHeader("base", "direct:step")
		ex.GetIn().SetHeader("user", "x&allowHeaderOverride=true,exec:pwn")

		uris, err := rs.resolveURIs(ex)
		require.NoError(t, err)
		assert.Equal(t, []string{
			"direct:step?q=x%26allowHeaderOverride%3Dtrue%2Cexec%3Apwn",
			"direct:done",
		}, uris)

		rsAuth := NewRoutingSlip(ctx).SetSimpleExpression("http://${header.tenant}.internal/api,direct:done")
		ex2 := NewExchange(context.Background())
		ex2.GetIn().SetHeader("tenant", "169.254.169.254/latest/,exec:pwn")

		uris2, err := rsAuth.resolveURIs(ex2)
		require.NoError(t, err)
		assert.Equal(t, []string{
			"http://169.254.169.254%2Flatest%2F%2Cexec:pwn.internal/api",
			"direct:done",
		}, uris2)
	})

	t.Run("whole-list delegation still works", func(t *testing.T) {
		ctx := NewCamelContext()
		rs := NewRoutingSlip(ctx).SetSimpleExpression("${header.slip}")
		ex := NewExchange(context.Background())
		ex.GetIn().SetHeader("slip", "direct:x?a=1,direct:y?b=2")

		uris, err := rs.resolveURIs(ex)
		require.NoError(t, err)
		assert.Equal(t, []string{"direct:x?a=1", "direct:y?b=2"}, uris)
	})
}

func TestRoutingSlip_BuildAndValidateRejectMalformedTemplate(t *testing.T) {
	malformedExprs := []string{
		"direct:${header.x",
		"direct:${}",
		"${header." + strings.Repeat("a", MaxSimpleInputBytes+1) + "}",
	}

	for _, expr := range malformedExprs {
		t.Run("RouteBuilder "+expr[:min(len(expr), 20)], func(t *testing.T) {
			ctx := NewCamelContext()
			_, err := ctx.CreateRouteBuilder().
				From("direct:start").
				RoutingSlip(expr).
				Build()
			require.Error(t, err)
		})

		t.Run("Validator "+expr[:min(len(expr), 20)], func(t *testing.T) {
			ctx := NewCamelContext()
			rs := NewRoutingSlip(ctx).SetSimpleExpression(expr)
			v, ok := any(rs).(Validator)
			require.True(t, ok, "RoutingSlip must implement Validator")
			require.Error(t, v.Validate())

			r := ctx.CreateRoute().From("direct:start")
			r.AddProcessor(rs)
			require.Error(t, r.Validate())
		})
	}
}
