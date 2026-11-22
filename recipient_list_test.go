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

func TestRecipientList_StaticURIs(t *testing.T) {
	ctx := context.Background()
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	var calls []string
	var mu sync.Mutex

	// Set up target routes
	for _, name := range []string{"a", "b", "c"} {
		targetName := name
		builder := NewRouteBuilder(camel)
		builder.From("direct:" + targetName).ProcessFunc(func(e *Exchange) error {
			mu.Lock()
			calls = append(calls, targetName)
			mu.Unlock()
			return nil
		})
		route, err := builder.Build()
		assert.NoError(t, err)
		camel.AddRoute(route)
	}

	// Create route with RecipientList using static comma-separated URIs
	builder := NewRouteBuilder(camel)
	builder.From("direct:start").RecipientList("direct:a,direct:b,direct:c")

	route, err := builder.Build()
	assert.NoError(t, err)
	camel.AddRoute(route)

	err = camel.Start()
	assert.NoError(t, err)
	defer camel.Stop()

	exchange := NewExchange(ctx)
	err = route.Process(exchange)
	assert.NoError(t, err)

	assert.ElementsMatch(t, []string{"a", "b", "c"}, calls)
}

func TestRecipientList_DynamicViaHeader(t *testing.T) {
	ctx := context.Background()
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	var calls []string
	var mu sync.Mutex

	for _, name := range []string{"x", "y"} {
		targetName := name
		builder := NewRouteBuilder(camel)
		builder.From("direct:" + targetName).ProcessFunc(func(e *Exchange) error {
			mu.Lock()
			calls = append(calls, targetName)
			mu.Unlock()
			return nil
		})
		route, err := builder.Build()
		assert.NoError(t, err)
		camel.AddRoute(route)
	}

	// RecipientList using Simple Language expression from header
	// Note: SetHeader on RouteBuilder sets Out headers, but Simple Language
	// reads from In headers. We use ProcessFunc to set the In header directly.
	builder := NewRouteBuilder(camel)
	builder.From("direct:start").
		ProcessFunc(func(e *Exchange) error {
			e.GetIn().SetHeader("targets", "direct:x,direct:y")
			return nil
		}).
		RecipientList("${header.targets}")

	route, err := builder.Build()
	assert.NoError(t, err)
	camel.AddRoute(route)

	err = camel.Start()
	assert.NoError(t, err)
	defer camel.Stop()

	exchange := NewExchange(ctx)
	err = route.Process(exchange)
	assert.NoError(t, err)

	assert.ElementsMatch(t, []string{"x", "y"}, calls)
}

func TestRecipientList_GoFunction(t *testing.T) {
	ctx := context.Background()
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	var calls []string
	var mu sync.Mutex

	for _, name := range []string{"p", "q"} {
		targetName := name
		builder := NewRouteBuilder(camel)
		builder.From("direct:" + targetName).ProcessFunc(func(e *Exchange) error {
			mu.Lock()
			calls = append(calls, targetName)
			mu.Unlock()
			return nil
		})
		route, err := builder.Build()
		assert.NoError(t, err)
		camel.AddRoute(route)
	}

	// RecipientList using a Go function returning dynamic URIs
	builder := NewRouteBuilder(camel)
	builder.From("direct:start").RecipientList(func(e *Exchange) ([]string, error) {
		return []string{"direct:p", "direct:q"}, nil
	})

	route, err := builder.Build()
	assert.NoError(t, err)
	camel.AddRoute(route)

	err = camel.Start()
	assert.NoError(t, err)
	defer camel.Stop()

	exchange := NewExchange(ctx)
	err = route.Process(exchange)
	assert.NoError(t, err)

	assert.ElementsMatch(t, []string{"p", "q"}, calls)
}

func TestRecipientList_ParallelProcessing(t *testing.T) {
	ctx := context.Background()
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	var mu sync.Mutex
	var order []string

	for _, name := range []string{"slow", "fast"} {
		targetName := name
		builder := NewRouteBuilder(camel)
		builder.From("direct:" + targetName).ProcessFunc(func(e *Exchange) error {
			if targetName == "slow" {
				// time.Sleep is intentional: simulates a slow recipient to test ordering.
				time.Sleep(50 * time.Millisecond)
			}
			mu.Lock()
			order = append(order, targetName)
			mu.Unlock()
			return nil
		})
		route, err := builder.Build()
		assert.NoError(t, err)
		camel.AddRoute(route)
	}

	builder := NewRouteBuilder(camel)
	builder.From("direct:start").
		RecipientList("direct:slow,direct:fast").ParallelProcessing()

	route, err := builder.Build()
	assert.NoError(t, err)
	camel.AddRoute(route)

	err = camel.Start()
	assert.NoError(t, err)
	defer camel.Stop()

	exchange := NewExchange(ctx)
	err = route.Process(exchange)
	assert.NoError(t, err)

	assert.Len(t, order, 2)
	assert.Equal(t, "fast", order[0], "fast should finish before slow (parallel)")
	assert.Equal(t, "slow", order[1])
}

func TestRecipientList_Aggregation(t *testing.T) {
	ctx := context.Background()
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	// Set up target routes that set specific bodies
	for _, entry := range []struct{ name, body string }{
		{"a", "resultA"},
		{"b", "resultB"},
	} {
		targetBody := entry.body
		builder := NewRouteBuilder(camel)
		builder.From("direct:" + entry.name).ProcessFunc(func(e *Exchange) error {
			e.GetIn().SetBody(targetBody)
			return nil
		})
		route, err := builder.Build()
		assert.NoError(t, err)
		camel.AddRoute(route)
	}

	strategy := &StringConcatStrategy{}

	builder := NewRouteBuilder(camel)
	builder.From("direct:start").
		RecipientList("direct:a,direct:b").AggregationStrategy(strategy)

	route, err := builder.Build()
	assert.NoError(t, err)
	camel.AddRoute(route)

	err = camel.Start()
	assert.NoError(t, err)
	defer camel.Stop()

	exchange := NewExchange(ctx)
	err = route.Process(exchange)
	assert.NoError(t, err)

	// StringConcatStrategy produces "old,new" -> "resultA,resultB"
	assert.Equal(t, "resultA,resultB", exchange.GetIn().GetBody())
}

func TestRecipientList_RecipientProperties(t *testing.T) {
	ctx := context.Background()
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	var indices []int
	var sizes []int
	var completes []bool
	var mu sync.Mutex

	for _, name := range []string{"1", "2", "3"} {
		targetName := name
		builder := NewRouteBuilder(camel)
		builder.From("direct:" + targetName).ProcessFunc(func(e *Exchange) error {
			idx, _ := e.GetPropertyAsInt(CamelRecipientListIndex)
			sz, _ := e.GetPropertyAsInt(CamelRecipientListSize)
			cpl, _ := e.GetPropertyAsBool(CamelRecipientListComplete)
			mu.Lock()
			indices = append(indices, idx)
			sizes = append(sizes, sz)
			completes = append(completes, cpl)
			mu.Unlock()
			return nil
		})
		route, err := builder.Build()
		assert.NoError(t, err)
		camel.AddRoute(route)
	}

	builder := NewRouteBuilder(camel)
	builder.From("direct:start").RecipientList("direct:1,direct:2,direct:3")

	route, err := builder.Build()
	assert.NoError(t, err)
	camel.AddRoute(route)

	err = camel.Start()
	assert.NoError(t, err)
	defer camel.Stop()

	exchange := NewExchange(ctx)
	err = route.Process(exchange)
	assert.NoError(t, err)

	assert.ElementsMatch(t, []int{0, 1, 2}, indices)
	for _, sz := range sizes {
		assert.Equal(t, 3, sz)
	}
	assert.Contains(t, completes, false)
	assert.Contains(t, completes, true)
}

func TestRecipientList_EmptyExpression(t *testing.T) {
	// Test that empty expression resolves to empty list and does nothing
	rl := NewRecipientList(NewCamelContext())
	rl.SetStaticURIs("")
	exchange := NewExchange(context.Background())
	err := rl.Process(exchange)
	assert.NoError(t, err)
}

type rlSpyComponent struct {
	mu   sync.Mutex
	uris []string
}

func (c *rlSpyComponent) CreateEndpoint(uri string) (Endpoint, error) {
	return &rlSpyEndpoint{comp: c, uri: uri}, nil
}

func (c *rlSpyComponent) record(uri string) {
	c.mu.Lock()
	c.uris = append(c.uris, uri)
	c.mu.Unlock()
}

func (c *rlSpyComponent) recorded() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.uris))
	copy(out, c.uris)
	return out
}

type rlSpyEndpoint struct {
	comp *rlSpyComponent
	uri  string
}

func (e *rlSpyEndpoint) URI() string { return e.uri }
func (e *rlSpyEndpoint) CreateProducer() (Producer, error) {
	return &rlSpyProducer{comp: e.comp, uri: e.uri}, nil
}
func (e *rlSpyEndpoint) CreateConsumer(Processor) (Consumer, error) {
	return &rlSpyConsumer{}, nil
}

type rlSpyConsumer struct{}

func (c *rlSpyConsumer) Start(context.Context) error { return nil }
func (c *rlSpyConsumer) Stop() error                 { return nil }

type rlSpyProducer struct {
	comp *rlSpyComponent
	uri  string
}

func (p *rlSpyProducer) Start(context.Context) error { return nil }
func (p *rlSpyProducer) Stop() error                 { return nil }
func (p *rlSpyProducer) Send(*Exchange) error {
	p.comp.record(p.uri)
	return nil
}

func TestRecipientList_InjectionClosed(t *testing.T) {
	t.Run("comma and query option injection is escaped and never invokes exec", func(t *testing.T) {
		ctx := NewCamelContext()
		directSpy := &rlSpyComponent{}
		execSpy := &rlSpyComponent{}
		ctx.AddComponent("direct", directSpy)
		ctx.AddComponent("exec", execSpy)

		route, err := ctx.CreateRouteBuilder().
			From("direct:start").
			RecipientList("direct:svc-${header.tenant},direct:audit").
			End().
			Build()
		require.NoError(t, err)
		require.NoError(t, ctx.Start())
		t.Cleanup(func() { _ = ctx.Stop() })

		ex := NewExchange(context.Background())
		ex.GetIn().SetHeader("tenant", "a,exec:echo?args=pwn")

		require.NoError(t, route.Process(ex))
		assert.Empty(t, execSpy.recorded(), "injected exec endpoint must never be invoked")
		assert.Equal(t, []string{
			"direct:svc-a%2Cexec:echo%3Fargs=pwn",
			"direct:audit",
		}, directSpy.recorded())
	})

	t.Run("SetStaticURIs comma and option injection is escaped", func(t *testing.T) {
		ctx := NewCamelContext()
		rl := NewRecipientList(ctx).SetStaticURIs("direct:svc-${header.tenant},direct:audit")
		ex := NewExchange(context.Background())
		ex.GetIn().SetHeader("tenant", "a,exec:echo?args=pwn")

		uris, err := rl.resolveURIs(ex)
		require.NoError(t, err)
		assert.Equal(t, []string{
			"direct:svc-a%2Cexec:echo%3Fargs=pwn",
			"direct:audit",
		}, uris)
	})

	t.Run("authority SSRF and leading placeholder query injection in list template", func(t *testing.T) {
		ctx := NewCamelContext()
		rlAuth := NewRecipientList(ctx).SetSimpleExpression("http://${header.tenant}.internal/api,direct:audit")
		ex := NewExchange(context.Background())
		ex.GetIn().SetHeader("tenant", "169.254.169.254/latest/,exec:pwn")

		uris, err := rlAuth.resolveURIs(ex)
		require.NoError(t, err)
		assert.Equal(t, []string{
			"http://169.254.169.254%2Flatest%2F%2Cexec:pwn.internal/api",
			"direct:audit",
		}, uris)

		rlLead := NewRecipientList(ctx).SetSimpleExpression("${header.base}?q=${header.user},direct:audit")
		ex2 := NewExchange(context.Background())
		ex2.GetIn().SetHeader("base", "direct:svc")
		ex2.GetIn().SetHeader("user", "x&allowHeaderOverride=true,exec:pwn")

		uris2, err := rlLead.resolveURIs(ex2)
		require.NoError(t, err)
		assert.Equal(t, []string{
			"direct:svc?q=x%26allowHeaderOverride%3Dtrue%2Cexec%3Apwn",
			"direct:audit",
		}, uris2)
	})

	t.Run("whole-list delegation and commas inside placeholder expressions still work", func(t *testing.T) {
		ctx := NewCamelContext()

		rlWhole := NewRecipientList(ctx).SetSimpleExpression("${header.targets}")
		ex := NewExchange(context.Background())
		ex.GetIn().SetHeader("targets", "direct:x?a=1,direct:y?b=2")
		uris, err := rlWhole.resolveURIs(ex)
		require.NoError(t, err)
		assert.Equal(t, []string{"direct:x?a=1", "direct:y?b=2"}, uris)

		rlFn := NewRecipientList(ctx).SetSimpleExpression("direct:${header.env in 'prod,staging' ? 'prod' : 'dev'},direct:audit")
		ex2 := NewExchange(context.Background())
		ex2.GetIn().SetHeader("env", "prod")
		uris2, err := rlFn.resolveURIs(ex2)
		require.NoError(t, err)
		assert.Equal(t, []string{"direct:prod", "direct:audit"}, uris2)
	})
}

func TestRecipientList_BuildAndValidateRejectMalformedTemplate(t *testing.T) {
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
				RecipientList(expr).
				End().
				Build()
			require.Error(t, err)
		})

		t.Run("Multicast "+expr[:min(len(expr), 20)], func(t *testing.T) {
			ctx := NewCamelContext()
			_, err := ctx.CreateRouteBuilder().
				From("direct:start").
				Multicast().
				RecipientList(expr).
				End().
				Build()
			require.Error(t, err)
		})

		t.Run("Pipeline "+expr[:min(len(expr), 20)], func(t *testing.T) {
			ctx := NewCamelContext()
			_, err := ctx.CreateRouteBuilder().
				From("direct:start").
				Multicast().
				Pipeline().
				RecipientList(expr).
				End().
				End().
				Build()
			require.Error(t, err)
		})

		t.Run("Split "+expr[:min(len(expr), 20)], func(t *testing.T) {
			ctx := NewCamelContext()
			_, err := ctx.CreateRouteBuilder().
				From("direct:start").
				Split(func(e *Exchange) (any, error) { return []string{"1"}, nil }).
				RecipientList(expr).
				End().
				Build()
			require.Error(t, err)
		})

		t.Run("Validator "+expr[:min(len(expr), 20)], func(t *testing.T) {
			ctx := NewCamelContext()
			rl := NewRecipientList(ctx).SetSimpleExpression(expr)
			v, ok := any(rl).(Validator)
			require.True(t, ok, "RecipientList must implement Validator")
			require.Error(t, v.Validate())

			r := ctx.CreateRoute().From("direct:start")
			r.AddProcessor(rl)
			require.Error(t, r.Validate())
		})
	}
}
