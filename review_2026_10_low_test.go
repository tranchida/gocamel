package gocamel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression tests for points 7-14 of the 2026-10 code review.

// --- Parallel Split / RecipientList are bounded ------------------------------

// concurrencyProbe blocks every call until released and tracks how many calls
// run at the same time.
type concurrencyProbe struct {
	running atomic.Int32
	peak    atomic.Int32
	release chan struct{}
}

func newConcurrencyProbe() *concurrencyProbe {
	return &concurrencyProbe{release: make(chan struct{})}
}

func (p *concurrencyProbe) process(*Exchange) error {
	n := p.running.Add(1)
	defer p.running.Add(-1)
	for {
		peak := p.peak.Load()
		if n <= peak || p.peak.CompareAndSwap(peak, n) {
			break
		}
	}
	<-p.release
	return nil
}

// assertBounded waits until limit calls run, checks the limit is never
// exceeded, then releases them and waits for send to finish.
func (p *concurrencyProbe) assertBounded(t *testing.T, limit int32, done <-chan error) {
	t.Helper()
	require.Eventually(t, func() bool { return p.running.Load() == limit }, 5*time.Second, time.Millisecond)
	assert.Never(t, func() bool { return p.running.Load() > limit }, 100*time.Millisecond, 5*time.Millisecond)
	close(p.release)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("send did not finish")
	}
	assert.Equal(t, limit, p.peak.Load())
}

func sendAsync(t *testing.T, ctx *CamelContext, uri string) <-chan error {
	t.Helper()
	p, err := ctx.GetProducer(uri)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- p.Send(NewExchange(context.Background())) }()
	return done
}

func TestSplit_ParallelProcessingHonoursMaxConcurrency(t *testing.T) {
	ctx := NewCamelContext()
	probe := newConcurrencyProbe()
	_, err := ctx.CreateRouteBuilder().
		From("direct:psplit").
		Split(func(*Exchange) (any, error) { return make([]int, 50), nil }).
		ParallelProcessing(true).
		MaxConcurrency(3).
		ProcessFunc(probe.process).
		End().
		Build()
	require.NoError(t, err)
	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })

	probe.assertBounded(t, 3, sendAsync(t, ctx, "direct:psplit"))
}

func TestSplit_ParallelProcessingIsBoundedByDefault(t *testing.T) {
	ctx := NewCamelContext()
	probe := newConcurrencyProbe()
	_, err := ctx.CreateRouteBuilder().
		From("direct:psplit-default").
		Split(func(*Exchange) (any, error) { return make([]int, 100), nil }).
		ParallelProcessing(true).
		ProcessFunc(probe.process).
		End().
		Build()
	require.NoError(t, err)
	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })

	probe.assertBounded(t, DefaultMaxConcurrency, sendAsync(t, ctx, "direct:psplit-default"))
}

func TestRecipientList_ParallelProcessingIsBoundedByDefault(t *testing.T) {
	ctx := NewCamelContext()
	probe := newConcurrencyProbe()
	_, err := ctx.CreateRouteBuilder().From("direct:rl-slow").ProcessFunc(probe.process).Build()
	require.NoError(t, err)
	uris := make([]string, 40)
	for i := range uris {
		uris[i] = "direct:rl-slow"
	}
	_, err = ctx.CreateRouteBuilder().
		From("direct:rl-parallel").
		RecipientList(func(*Exchange) ([]string, error) { return uris, nil }).
		ParallelProcessing().
		End().
		Build()
	require.NoError(t, err)
	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })

	probe.assertBounded(t, DefaultMaxConcurrency, sendAsync(t, ctx, "direct:rl-parallel"))
}

// --- Aggregator restores the timeout of pending groups on start ---------------

func TestAggregator_RouteRestartRearmsTimeoutOfPendingGroup(t *testing.T) {
	ctx := NewCamelContext()
	completed := make(chan *Exchange, 1)
	agg := NewAggregator(func(*Exchange) string { return "k" }, latestStrategy{}, NewMemoryAggregationRepository()).
		SetCompletionSize(10).
		SetCompletionTimeout(200 * time.Millisecond)
	route, err := ctx.CreateRouteBuilder().
		From("direct:agg-restart").
		Aggregate(agg).
		ProcessFunc(func(e *Exchange) error { completed <- e; return nil }).
		Build()
	require.NoError(t, err)
	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })

	p, err := ctx.GetProducer("direct:agg-restart")
	require.NoError(t, err)
	require.NoError(t, p.Send(NewExchange(context.Background())))
	require.NoError(t, route.Stop()) // cancels the pending group's timeout
	require.NoError(t, route.Start(ctx.GetContext()))

	select {
	case e := <-completed:
		size, _ := e.GetPropertyAsInt(CamelAggregatorSize)
		assert.Equal(t, 1, size)
	case <-time.After(5 * time.Second):
		t.Fatal("the group pending across the restart never timed out")
	}
}

func TestAggregator_TimeoutOfPersistedGroupIsRestoredOnStart(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1) // a single in-memory database
	t.Cleanup(func() { _ = db.Close() })
	repo := NewSQLAggregationRepository(db, SQLAggregationOptions{})
	require.NoError(t, repo.InitDB(context.Background()))

	// A group left pending by a previous process.
	pending := NewExchange(context.Background())
	pending.SetBody("left over")
	pending.SetProperty(CamelAggregatorSize, 2)
	require.NoError(t, repo.Add(context.Background(), "order-1", pending))
	keys, err := repo.Keys(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"order-1"}, keys)

	ctx := NewCamelContext()
	completed := make(chan *Exchange, 1)
	agg := NewAggregator(func(*Exchange) string { return "order-1" }, latestStrategy{}, repo).
		SetCompletionSize(10).
		SetCompletionTimeout(20 * time.Millisecond)
	_, err = ctx.CreateRouteBuilder().
		From("direct:agg-persisted").
		Aggregate(agg).
		ProcessFunc(func(e *Exchange) error { completed <- e; return nil }).
		Build()
	require.NoError(t, err)
	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })

	select {
	case e := <-completed:
		assert.Equal(t, "left over", e.GetIn().GetBody())
	case <-time.After(5 * time.Second):
		t.Fatal("the persisted group never timed out")
	}
	assert.Eventually(t, func() bool {
		keys, err := repo.Keys(context.Background())
		return err == nil && len(keys) == 0
	}, 5*time.Second, 5*time.Millisecond)
}

// --- exec: a header may only select allowed executables ----------------------

func TestExec_HeaderExecutableMustBeAllowed(t *testing.T) {
	newProducer := func(uri string) Producer {
		ep, err := NewExecComponent().CreateEndpoint(uri)
		require.NoError(t, err)
		p, err := ep.CreateProducer()
		require.NoError(t, err)
		return p
	}
	send := func(p Producer, executable, args string) (*Exchange, error) {
		ex := NewExchange(context.Background())
		ex.SetHeader(CamelExecCommandExecutable, executable)
		ex.SetHeader(CamelExecCommandArgs, args)
		return ex, p.Send(ex)
	}

	p := newProducer("exec:echo?allowHeaderOverride=true")
	_, err := send(p, "sh", "-c id")
	require.Error(t, err, "a bare shell name must not be accepted from a header")
	assert.Contains(t, err.Error(), "allowedExecutables")

	ex, err := send(p, "echo", "same executable")
	require.NoError(t, err, "the endpoint's own executable is always allowed")
	assert.Contains(t, ex.GetIn().GetBody(), "same executable")

	p = newProducer("exec:echo?allowHeaderOverride=true&allowedExecutables=printf,%20true")
	ex, err = send(p, "printf", "allowed")
	require.NoError(t, err)
	assert.Equal(t, "allowed", ex.GetIn().GetBody())
	_, err = send(p, "sh", "-c id")
	assert.Error(t, err)
	_, err = send(p, "/usr/bin/printf", "x")
	assert.Error(t, err, "a path is rejected even when its base name is allowed")
}

// --- Management server --------------------------------------------------------

type failingConsumerComponent struct{}

func (failingConsumerComponent) CreateEndpoint(uri string) (Endpoint, error) {
	return failingConsumerEndpoint{uri: uri}, nil
}

type failingConsumerEndpoint struct{ uri string }

func (e failingConsumerEndpoint) URI() string                     { return e.uri }
func (failingConsumerEndpoint) CreateProducer() (Producer, error) { return &MockProducer{}, nil }
func (failingConsumerEndpoint) CreateConsumer(Processor) (Consumer, error) {
	return failingConsumer{}, nil
}

type failingConsumer struct{}

func (failingConsumer) Start(context.Context) error {
	return errors.New("dial tcp db.internal:5432: password authentication failed for admin")
}
func (failingConsumer) Stop() error { return nil }

func managementRequest(h http.Handler, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.Host = "127.0.0.1:8081"
	for k, v := range headers {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func newManagedContext(t *testing.T) (*CamelContext, *Route) {
	t.Helper()
	ctx := NewCamelContext()
	ctx.AddComponent("mock", &MockComponent{})
	route := ctx.CreateRoute()
	route.ID = "managed"
	route.From("mock:source")
	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })
	return ctx, route
}

func TestManagementServer_StartReportsBindError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	m := NewManagementServer(NewCamelContext())
	err = m.Start(ln.Addr().String())
	require.Error(t, err)
	assert.Nil(t, m.Addr())
	assert.NoError(t, m.Stop())
}

func TestManagementServer_ServesOnBoundAddress(t *testing.T) {
	m := NewManagementServer(NewCamelContext())
	require.NoError(t, m.Start("127.0.0.1:0"))
	t.Cleanup(func() { _ = m.Stop() })
	require.NotNil(t, m.Addr())
	assert.Error(t, m.Start("127.0.0.1:0"), "a second Start is rejected")

	resp, err := http.Get("http://" + m.Addr().String() + "/api/context")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestManagementServer_RejectsDNSRebindingHost(t *testing.T) {
	_, route := newManagedContext(t)
	h := NewManagementServer(route.context).handler()

	w := managementRequest(h, http.MethodPost, "/api/routes/managed/stop", map[string]string{"Host": "attacker.example:8081"})
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.True(t, route.IsStarted())

	for _, host := range []string{"localhost:8081", "[::1]:8081", "127.0.0.1"} {
		w = managementRequest(h, http.MethodGet, "/api/routes", map[string]string{"Host": host})
		assert.Equal(t, http.StatusOK, w.Code, host)
	}

	h = NewManagementServer(route.context).SetAllowedHosts("mgmt.internal").handler()
	w = managementRequest(h, http.MethodGet, "/api/routes", map[string]string{"Host": "MGMT.internal:443"})
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestManagementServer_RejectsCrossOriginPost(t *testing.T) {
	_, route := newManagedContext(t)
	h := NewManagementServer(route.context).handler()

	w := managementRequest(h, http.MethodPost, "/api/routes/managed/stop", map[string]string{
		"Origin":         "https://attacker.example",
		"Sec-Fetch-Site": "cross-site",
	})
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.True(t, route.IsStarted(), "a cross-site form must not stop the route")

	w = managementRequest(h, http.MethodPost, "/api/routes/managed/stop", nil) // curl, no browser headers
	assert.Equal(t, http.StatusOK, w.Code)
	assert.False(t, route.IsStarted())
}

func TestManagementServer_AuthToken(t *testing.T) {
	_, route := newManagedContext(t)
	h := NewManagementServer(route.context).SetAuthToken("s3cret").handler()

	w := managementRequest(h, http.MethodGet, "/api/routes", nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	w = managementRequest(h, http.MethodPost, "/api/routes/managed/stop", map[string]string{"Authorization": "Bearer wrong"})
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.True(t, route.IsStarted())

	w = managementRequest(h, http.MethodPost, "/api/routes/managed/stop", map[string]string{"Authorization": "Bearer s3cret"})
	assert.Equal(t, http.StatusOK, w.Code)
	assert.False(t, route.IsStarted())
}

func TestManagementServer_StartRouteRequiresStartedContext(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("mock", &MockComponent{})
	route := ctx.CreateRoute()
	route.ID = "idle"
	route.From("mock:source")
	h := NewManagementServer(ctx).handler()

	w := managementRequest(h, http.MethodPost, "/api/routes/idle/start", nil)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.False(t, route.IsStarted(), "CamelContext.Stop would never stop it")
}

func TestManagementServer_DoesNotLeakInternalErrors(t *testing.T) {
	ctx, _ := newManagedContext(t)
	ctx.AddComponent("failing", failingConsumerComponent{})
	route := ctx.CreateRoute()
	route.ID = "broken"
	route.From("failing:x")
	h := NewManagementServer(ctx).handler()

	w := managementRequest(h, http.MethodPost, "/api/routes/broken/start", nil)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NotContains(t, w.Body.String(), "db.internal")
	assert.NotContains(t, w.Body.String(), "admin")
}

// --- HTTP producer does not replay response headers ---------------------------

func TestHTTPProducer_ChainedCallDoesNotForwardResponseHeaders(t *testing.T) {
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "session=service-a-secret")
		w.Header().Set("Server", "service-a/1.2.3")
		w.Header().Set("Www-Authenticate", `Basic realm="a"`)
		w.Header().Set("X-Correlation", "abc")
		_, _ = w.Write([]byte("from a"))
	}))
	t.Cleanup(a.Close)
	var mu sync.Mutex
	var received http.Header
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		received = r.Header.Clone()
		mu.Unlock()
	}))
	t.Cleanup(b.Close)

	ctx := NewCamelContext()
	_, err := ctx.CreateRouteBuilder().From("direct:chain").To(a.URL).To(b.URL).Build()
	require.NoError(t, err)
	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })
	p, err := ctx.GetProducer("direct:chain")
	require.NoError(t, err)
	require.NoError(t, p.Send(NewExchange(context.Background())))

	mu.Lock()
	defer mu.Unlock()
	require.NotNil(t, received)
	for _, h := range []string{"Set-Cookie", "Server", "Www-Authenticate", "Status-Code"} {
		assert.Empty(t, received.Get(h), "%s from service A must not reach service B", h)
	}
	assert.Equal(t, "abc", received.Get("X-Correlation"), "application headers still flow")
}

// --- ProducerCache: no stale producer after a stop --------------------------

type blockingStartComponent struct {
	started chan struct{}
	release chan struct{}
	stopped atomic.Bool
}

func (c *blockingStartComponent) CreateEndpoint(uri string) (Endpoint, error) {
	return &blockingStartEndpoint{uri: uri, c: c}, nil
}

type blockingStartEndpoint struct {
	uri string
	c   *blockingStartComponent
}

func (e *blockingStartEndpoint) URI() string { return e.uri }
func (e *blockingStartEndpoint) CreateProducer() (Producer, error) {
	return &blockingStartProducer{c: e.c}, nil
}
func (e *blockingStartEndpoint) CreateConsumer(Processor) (Consumer, error) {
	return nil, fmt.Errorf("not supported")
}

type blockingStartProducer struct{ c *blockingStartComponent }

func (p *blockingStartProducer) Start(context.Context) error {
	close(p.c.started)
	<-p.c.release
	return nil
}
func (p *blockingStartProducer) Stop() error          { p.c.stopped.Store(true); return nil }
func (p *blockingStartProducer) Send(*Exchange) error { return nil }

func TestProducerCache_ProducerCreatedAcrossStopIsNotCached(t *testing.T) {
	comp := &blockingStartComponent{started: make(chan struct{}), release: make(chan struct{})}
	ctx := NewCamelContext()
	ctx.AddComponent("blocking", comp)
	require.NoError(t, ctx.Start())

	got := make(chan error, 1)
	go func() {
		_, err := ctx.GetProducer("blocking:x")
		got <- err
	}()
	<-comp.started // Start received the context that Stop is about to cancel
	require.NoError(t, ctx.Stop())
	close(comp.release)

	select {
	case err := <-got:
		assert.Error(t, err, "a producer started with the cancelled context must not be handed out")
	case <-time.After(5 * time.Second):
		t.Fatal("GetProducer did not return")
	}
	assert.True(t, comp.stopped.Load(), "the stale producer is stopped")
	assert.Equal(t, 0, ctx.producerCache.Size())
}
