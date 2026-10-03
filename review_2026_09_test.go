package gocamel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingComponent records the URI of every endpoint it creates and counts
// producer starts/stops. Its producers can be made to block until released.
type recordingComponent struct {
	mu      sync.Mutex
	uris    []string
	started atomic.Int32
	stopped atomic.Int32
	block   chan struct{} // when non-nil, Send waits for it to be closed
}

func (c *recordingComponent) CreateEndpoint(uri string) (Endpoint, error) {
	c.mu.Lock()
	c.uris = append(c.uris, uri)
	c.mu.Unlock()
	return &recordingEndpoint{uri: uri, component: c}, nil
}

func (c *recordingComponent) lastURI() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.uris) == 0 {
		return ""
	}
	return c.uris[len(c.uris)-1]
}

type recordingEndpoint struct {
	uri       string
	component *recordingComponent
}

func (e *recordingEndpoint) URI() string { return e.uri }
func (e *recordingEndpoint) CreateProducer() (Producer, error) {
	return &recordingProducer{component: e.component, uri: e.uri}, nil
}
func (e *recordingEndpoint) CreateConsumer(Processor) (Consumer, error) {
	return nil, fmt.Errorf("producer-only endpoint")
}

type recordingProducer struct {
	component *recordingComponent
	uri       string
}

func (p *recordingProducer) Start(context.Context) error {
	p.component.started.Add(1)
	return nil
}
func (p *recordingProducer) Stop() error {
	p.component.stopped.Add(1)
	return nil
}
func (p *recordingProducer) Send(e *Exchange) error {
	if p.component.block != nil {
		<-p.component.block
	}
	e.GetOut().SetBody("reply from " + p.uri)
	return nil
}

func TestInterpolateURI_EscapesValuesByPosition(t *testing.T) {
	ex := NewExchange(context.Background())
	ex.SetHeader("q", "a&allowHeaderOverride=true#frag")
	ex.SetHeader("p", "x?allowHeaderOverride=true")
	ex.SetHeader("target", "seda:out?size=5")

	assert.Equal(t, "exec:echo?args=a%26allowHeaderOverride%3Dtrue%23frag",
		InterpolateURI("exec:echo?args=${header.q}", ex))
	assert.Equal(t, "http://api/items/x%3FallowHeaderOverride=true?v=1",
		InterpolateURI("http://api/items/${header.p}?v=1", ex))
	// A whole-URI template is a deliberate delegation: inserted verbatim.
	assert.Equal(t, "seda:out?size=5", InterpolateURI("${header.target}", ex))
	// Interpolate itself is unchanged.
	assert.Equal(t, "args=a&allowHeaderOverride=true#frag", Interpolate("args=${header.q}", ex))
}

func TestToD_HeaderCannotInjectEndpointOptions(t *testing.T) {
	camelCtx := NewCamelContext()
	rec := &recordingComponent{}
	camelCtx.AddComponent("rec", rec)
	require.NoError(t, camelCtx.Start())
	t.Cleanup(func() { _ = camelCtx.Stop() })

	ex := NewExchange(context.Background())
	ex.SetHeader("name", "hello&allowHeaderOverride=true")
	require.NoError(t, createToDProcessor(camelCtx, "rec:cmd?args=${header.name}").Process(ex))

	u, err := ParseURI(rec.lastURI())
	require.NoError(t, err)
	assert.Equal(t, "hello&allowHeaderOverride=true", u.Query().Get("args"))
	assert.Empty(t, u.Query().Get("allowHeaderOverride"))
}

func TestProducerCache_EvictsIdleProducersBeyondBound(t *testing.T) {
	camelCtx := NewCamelContext()
	rec := &recordingComponent{}
	camelCtx.AddComponent("rec", rec)
	camelCtx.SetProducerCacheSize(2)
	require.NoError(t, camelCtx.Start())
	t.Cleanup(func() { _ = camelCtx.Stop() })

	tod := createToDProcessor(camelCtx, "rec:item/${header.id}")
	for i := 0; i < 5; i++ {
		ex := NewExchange(context.Background())
		ex.SetHeader("id", i)
		require.NoError(t, tod.Process(ex))
	}

	assert.Equal(t, 2, camelCtx.producerCache.Size())
	assert.Equal(t, int32(5), rec.started.Load())
	assert.Equal(t, int32(3), rec.stopped.Load(), "evicted producers must be stopped")
}

func TestProducerCache_NeverEvictsInUseOrPinnedProducers(t *testing.T) {
	camelCtx := NewCamelContext()
	rec := &recordingComponent{}
	camelCtx.AddComponent("rec", rec)
	camelCtx.SetProducerCacheSize(1)
	require.NoError(t, camelCtx.Start())
	t.Cleanup(func() { _ = camelCtx.Stop() })

	pinned, err := camelCtx.GetProducer("rec:pinned")
	require.NoError(t, err)

	_, release, err := camelCtx.acquireProducer("rec:busy")
	require.NoError(t, err)
	// Both are protected: the cache exceeds its bound rather than stopping
	// a producer somebody is using.
	assert.Equal(t, 2, camelCtx.producerCache.Size())
	assert.Equal(t, int32(0), rec.stopped.Load())

	release()
	release() // idempotent
	assert.Equal(t, 1, camelCtx.producerCache.Size())
	assert.Equal(t, int32(1), rec.stopped.Load(), "the released producer is evicted, the pinned one is kept")

	again, err := camelCtx.GetProducer("rec:pinned")
	require.NoError(t, err)
	assert.Same(t, pinned, again)
}

func TestDynamicRouter_StepOutputFeedsNextStep(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("rec", &recordingComponent{})
	require.NoError(t, camelCtx.Start())
	t.Cleanup(func() { _ = camelCtx.Stop() })

	var seen []string
	router := NewDynamicRouter(camelCtx, func(e *Exchange) (string, error) {
		body, _ := e.GetIn().GetBody().(string)
		seen = append(seen, body)
		if len(seen) == 1 {
			return "rec:step1", nil
		}
		return "", nil
	})
	ex := NewExchange(context.Background())
	ex.SetBody("input")
	require.NoError(t, router.Process(ex))
	assert.Equal(t, []string{"input", "reply from rec:step1"}, seen)
}

func startHTTPRoute(t *testing.T, camelCtx *CamelContext, path string, proc func(*Exchange) error) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	camelCtx.CreateRoute().From("http://" + addr + path).ProcessFunc(proc)
	require.NoError(t, camelCtx.Start())
	t.Cleanup(func() { _ = camelCtx.Stop() })
	base := "http://" + addr
	require.Eventually(t, func() bool {
		conn, err := net.Dial("tcp", addr)
		if err == nil {
			_ = conn.Close()
		}
		return err == nil
	}, 2*time.Second, 10*time.Millisecond)
	return base
}

func TestHTTPConsumer_DoesNotExposeOrAcceptCamelHeaders(t *testing.T) {
	var inbound any
	var hadInbound bool
	base := startHTTPRoute(t, NewCamelContext(), "/p", func(e *Exchange) error {
		inbound, hadInbound = e.GetHeader("Camelfilename")
		e.SetHeader(CamelFileName, "/srv/secret/in.csv")
		e.SetHeader("Set-Cookie", []string{"a=1", "b=2"})
		return nil
	})

	req, err := http.NewRequest(http.MethodGet, base+"/p?token=abc", nil)
	require.NoError(t, err)
	req.Header.Set("CamelFileName", "../../etc/passwd")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.False(t, hadInbound, "inbound Camel* header must be dropped, got %v", inbound)
	for key := range resp.Header {
		assert.False(t, IsCamelHeader(key), "response leaks internal header %s", key)
	}
	assert.Equal(t, []string{"a=1", "b=2"}, resp.Header.Values("Set-Cookie"))
}

func TestHTTPProducer_BodyTypesAndMultiValuedResponseHeaders(t *testing.T) {
	var received string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		received = string(b)
		w.Header().Add("Set-Cookie", "a=1")
		w.Header().Add("Set-Cookie", "b=2")
	}))
	t.Cleanup(srv.Close)
	producer, err := NewCamelContext().GetProducer(srv.URL)
	require.NoError(t, err)

	ex := NewExchange(context.Background())
	ex.SetBody(strings.NewReader("streamed"))
	require.NoError(t, producer.Send(ex))
	assert.Equal(t, "streamed", received)
	cookies, _ := ex.GetOut().GetHeader("Set-Cookie")
	assert.Equal(t, []string{"a=1", "b=2"}, cookies)

	ex = NewExchange(context.Background())
	ex.SetBody(map[string]any{"a": 1})
	err = producer.Send(ex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported body type")
}

func TestHTTPProxy_DoesNotForwardClientCredentialsByDefault(t *testing.T) {
	var upstreamAuth, upstreamCamel []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamAuth = r.Header.Values("Authorization")
		for key := range r.Header {
			if IsCamelHeader(key) {
				upstreamCamel = append(upstreamCamel, key)
			}
		}
	}))
	t.Cleanup(upstream.Close)

	for _, tc := range []struct {
		name      string
		forward   bool
		routeAuth string
		want      []string
	}{
		{name: "client credential dropped", want: nil},
		{name: "opt-in forwards it", forward: true, want: []string{"Bearer client"}},
		{name: "route-set credential sent", routeAuth: "Bearer service", want: []string{"Bearer service"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstreamAuth, upstreamCamel = nil, nil
			camelCtx := NewCamelContext()
			httpComp := NewHTTPComponent()
			httpComp.SetForwardInboundCredentials(tc.forward)
			camelCtx.AddComponent("http", httpComp)
			base := startHTTPRoute(t, camelCtx, "/proxy", func(e *Exchange) error {
				if tc.routeAuth != "" {
					e.SetHeader("Authorization", tc.routeAuth)
				}
				e.SetHeader(CamelHttpMethod, http.MethodGet)
				return camelCtx.sendToURI(upstream.URL, e)
			})

			req, err := http.NewRequest(http.MethodGet, base+"/proxy", nil)
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer client")
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			_ = resp.Body.Close()
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, tc.want, upstreamAuth)
			assert.Empty(t, upstreamCamel, "Camel* headers must not reach the upstream")
		})
	}
}

func TestSeda_ConsumerRunsUnderItsOwnContext(t *testing.T) {
	camelCtx := NewCamelContext()
	endpoint, err := camelCtx.CreateEndpoint("seda:ctx-probe")
	require.NoError(t, err)
	got := make(chan error, 1)
	consumer, err := endpoint.CreateConsumer(ProcessorFunc(func(e *Exchange) error {
		got <- e.Context.Err()
		return nil
	}))
	require.NoError(t, err)
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	senderCtx, cancel := context.WithCancel(context.Background())
	ex := NewExchange(senderCtx)
	ex.SetBody("x")
	require.NoError(t, producer.Send(ex))
	cancel() // e.g. the HTTP request that enqueued it has returned

	require.NoError(t, consumer.Start(context.Background()))
	t.Cleanup(func() { _ = consumer.Stop() })
	select {
	case err := <-got:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("exchange not consumed")
	}
}

func TestSeda_StopDrainsQueuedExchanges(t *testing.T) {
	camelCtx := NewCamelContext()
	endpoint, err := camelCtx.CreateEndpoint("seda:drain-probe")
	require.NoError(t, err)
	var processed atomic.Int32
	release := make(chan struct{})
	consumer, err := endpoint.CreateConsumer(ProcessorFunc(func(e *Exchange) error {
		<-release
		processed.Add(1)
		return nil
	}))
	require.NoError(t, err)
	require.NoError(t, consumer.Start(context.Background()))
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		require.NoError(t, producer.Send(NewExchange(context.Background())))
	}

	close(release)
	require.NoError(t, consumer.Stop())
	assert.Equal(t, int32(5), processed.Load())

	err = producer.Send(NewExchange(context.Background()))
	assert.Error(t, err, "sends after Stop are rejected")
}

func TestSeda_ShutdownTimeoutBoundsStop(t *testing.T) {
	camelCtx := NewCamelContext()
	endpoint, err := camelCtx.CreateEndpoint("seda:blocked-probe?shutdownTimeout=50")
	require.NoError(t, err)
	consumer, err := endpoint.CreateConsumer(ProcessorFunc(func(e *Exchange) error {
		<-e.Context.Done() // blocks until the consumer cancels
		return e.Context.Err()
	}))
	require.NoError(t, err)
	require.NoError(t, consumer.Start(context.Background()))
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, producer.Send(NewExchange(context.Background())))

	start := time.Now()
	require.NoError(t, consumer.Stop())
	assert.Less(t, time.Since(start), 2*time.Second)
}

func TestTemplateJSON_IsHTMLSafeJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"body": {{json .Body}}, "m": {{json .Headers.m}}}`), 0o600))
	producer, err := NewCamelContext().GetProducer("template:" + path)
	require.NoError(t, err)

	ex := NewExchange(context.Background())
	ex.SetBody("</script><img src=x onerror=alert(1)>\x01")
	ex.SetHeader("m", map[string]string{"k": "<b>"})
	require.NoError(t, producer.Send(ex))

	out, _ := ex.GetIn().GetBody().(string)
	assert.NotContains(t, out, "<")
	assert.JSONEq(t, `{"body": "</script><img src=x onerror=alert(1)>\u0001", "m": {"k": "<b>"}}`, out)
}

func TestTemplateCache_KeyIncludesDelimiters(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.txt")
	require.NoError(t, os.WriteFile(path, []byte(`{{.Body}}|[[.Body]]`), 0o600))
	camelCtx := NewCamelContext()

	render := func(uri string) string {
		producer, err := camelCtx.GetProducer(uri)
		require.NoError(t, err)
		ex := NewExchange(context.Background())
		ex.SetBody("v")
		require.NoError(t, producer.Send(ex))
		s, _ := ex.GetIn().GetBody().(string)
		return s
	}
	assert.Equal(t, "v|[[.Body]]", render("template:"+path+"?contentCache=true"))
	assert.Equal(t, "{{.Body}}|v", render("template:"+path+"?contentCache=true&startDelimiter=[[&endDelimiter=]]"))
}

type nilErrorHandler struct{ calls atomic.Int32 }

func (h *nilErrorHandler) HandleError(*Exchange, error) error {
	h.calls.Add(1)
	return nil // "ignore" the error: the route retries the step
}

func TestRoute_ErrorHandlerReturningNilIsBounded(t *testing.T) {
	route := NewRoute()
	handler := &nilErrorHandler{}
	route.SetErrorHandler(handler)
	boom := errors.New("boom")
	route.ProcessFunc(func(*Exchange) error { return boom })

	err := route.Process(NewExchange(context.Background()))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTooManyRetries)
	assert.ErrorIs(t, err, boom)
	assert.Equal(t, int32(MaxErrorHandlerRetries+1), handler.calls.Load())
}

func TestDeadLetterHandled_CompletesTheExchange(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("rec", &recordingComponent{})
	require.NoError(t, camelCtx.Start())
	t.Cleanup(func() { _ = camelCtx.Stop() })
	boom := errors.New("boom")

	for _, handled := range []bool{false, true} {
		route := NewRoute()
		route.SetErrorHandler(NewDeadLetterChannel(camelCtx, "rec:dlq", DeadLetterHandled(handled)))
		route.ProcessFunc(func(*Exchange) error { return boom })

		err := route.Process(NewExchange(context.Background()))
		assert.ErrorIs(t, err, boom)
		if handled {
			assert.NoError(t, CompleteExchange(NewExchange(context.Background()), err))
		} else {
			assert.Error(t, CompleteExchange(NewExchange(context.Background()), err))
		}
	}
}

func TestNetConsumer_MaxConnectionsRejectsExtraClients(t *testing.T) {
	consumer := startNetConsumer(t, "net:tcp://localhost:0?maxConnections=1&readTimeout=5000", ProcessorFunc(func(e *Exchange) error {
		e.GetOut().SetBody("ok")
		return nil
	}))

	first, err := net.Dial("tcp", consumer.Address().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Close() })
	require.Eventually(t, func() bool {
		consumer.mu.Lock()
		defer consumer.mu.Unlock()
		return len(consumer.conns) == 1
	}, 2*time.Second, 10*time.Millisecond)

	second, err := net.Dial("tcp", consumer.Address().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Close() })
	require.NoError(t, second.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, err = second.Read(make([]byte, 1))
	assert.ErrorIs(t, err, io.EOF, "the connection above the limit is closed by the server")
}

// flakyListener fails Accept with a transient error before delegating.
type flakyListener struct {
	net.Listener
	failures atomic.Int32
}

func (l *flakyListener) Accept() (net.Conn, error) {
	if l.failures.Add(-1) >= 0 {
		return nil, errors.New("accept: too many open files")
	}
	return l.Listener.Accept()
}

func TestNetConsumer_AcceptLoopSurvivesTransientErrors(t *testing.T) {
	endpoint, err := NewNetComponent().CreateEndpoint("net:tcp://localhost:0?sync=false")
	require.NoError(t, err)
	received := make(chan string, 1)
	consumer, err := endpoint.CreateConsumer(ProcessorFunc(func(e *Exchange) error {
		received <- messageBodyString(e.GetIn())
		return nil
	}))
	require.NoError(t, err)
	nc := consumer.(*NetConsumer)

	inner, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ln := &flakyListener{Listener: inner}
	ln.failures.Store(3)
	stop := make(chan struct{})
	nc.mu.Lock()
	nc.started, nc.stop, nc.listener, nc.addr, nc.conns = true, stop, ln, inner.Addr(), make(map[net.Conn]struct{})
	nc.mu.Unlock()
	nc.wg.Add(1)
	go nc.acceptLoop(context.Background(), ln, stop)
	t.Cleanup(func() { _ = nc.Stop() })

	conn, err := net.Dial("tcp", inner.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	_, err = conn.Write([]byte("ping\n"))
	require.NoError(t, err)
	assert.Equal(t, "ping", waitForMessage(t, received))
}
