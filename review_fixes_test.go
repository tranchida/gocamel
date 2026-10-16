package gocamel

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getFreePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

// The HTTP consumer must not echo inbound request headers back in the
// response. Doing so leaked Cookie/Authorization to the client and reinjected
// the request's Content-Length, corrupting the response framing.
func TestHTTPConsumer_DoesNotEchoRequestHeaders(t *testing.T) {
	c := NewCamelContext()
	c.AddComponent("http", NewHTTPComponent())
	addr := getFreePort(t)
	c.CreateRouteBuilder().
		From("http://" + addr + "/noout").
		ProcessFunc(func(e *Exchange) error { return nil })
	require.NoError(t, c.Start())
	defer c.Stop()
	waitForHTTP(t, "http://"+addr+"/noout")

	req, _ := http.NewRequest("POST", "http://"+addr+"/noout", strings.NewReader("hello-body"))
	req.Header.Set("Cookie", "session=supersecret")
	req.Header.Set("Authorization", "Bearer token123")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	assert.Empty(t, resp.Header.Get("Cookie"), "request Cookie must not be echoed")
	assert.Empty(t, resp.Header.Get("Authorization"), "request Authorization must not be echoed")
	assert.Empty(t, resp.Header.Get("User-Agent"))
	// The response body is still delivered intact.
	assert.Equal(t, "hello-body", string(body))
}

// A header the route sets itself must still reach the client.
func TestHTTPConsumer_PropagatesRouteHeaders(t *testing.T) {
	c := NewCamelContext()
	c.AddComponent("http", NewHTTPComponent())
	addr := getFreePort(t)
	c.CreateRouteBuilder().
		From("http://" + addr + "/json").
		ProcessFunc(func(e *Exchange) error {
			e.GetOut().SetHeader("Content-Type", "application/json")
			e.GetOut().SetHeader(CamelHttpResponseCode, 201)
			e.GetOut().SetBody(`{"ok":true}`)
			return nil
		})
	require.NoError(t, c.Start())
	defer c.Stop()
	waitForHTTP(t, "http://"+addr+"/json")

	resp, err := http.Post("http://"+addr+"/json", "text/plain", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	assert.Equal(t, 201, resp.StatusCode, "CamelHttpResponseCode must drive the status")
	assert.Equal(t, `{"ok":true}`, string(body))
}

// A URI without a path must not panic ServeMux.
func TestHTTPConsumer_URIWithoutPath(t *testing.T) {
	c := NewCamelContext()
	c.AddComponent("http", NewHTTPComponent())
	addr := getFreePort(t)
	c.CreateRouteBuilder().
		From("http://" + addr).
		ProcessFunc(func(e *Exchange) error {
			e.GetOut().SetBody("root")
			return nil
		})
	require.NotPanics(t, func() {
		require.NoError(t, c.Start())
	})
	defer c.Stop()
	waitForHTTP(t, "http://"+addr+"/")

	resp, err := http.Post("http://"+addr+"/anything", "text/plain", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, "root", string(body))
}

// An oversized request body must be rejected rather than buffered whole.
func TestHTTPConsumer_RejectsOversizedBody(t *testing.T) {
	comp := NewHTTPComponent()
	comp.SetMaxBodySize(1024)
	c := NewCamelContext()
	c.AddComponent("http", comp)
	addr := getFreePort(t)
	c.CreateRouteBuilder().
		From("http://" + addr + "/upload").
		ProcessFunc(func(e *Exchange) error { return nil })
	require.NoError(t, c.Start())
	defer c.Stop()
	waitForHTTP(t, "http://"+addr+"/upload")

	resp, err := http.Post("http://"+addr+"/upload", "application/octet-stream",
		strings.NewReader(strings.Repeat("x", 4096)))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
}

// Internal error detail must not be disclosed to the client.
func TestHTTPConsumer_DoesNotLeakErrorDetail(t *testing.T) {
	c := NewCamelContext()
	c.AddComponent("http", NewHTTPComponent())
	addr := getFreePort(t)
	c.CreateRouteBuilder().
		From("http://" + addr + "/fail").
		ProcessFunc(func(e *Exchange) error {
			return errors.New("SELECT secret FROM vault -- internal detail")
		})
	require.NoError(t, c.Start())
	defer c.Stop()
	waitForHTTP(t, "http://"+addr+"/fail")

	resp, err := http.Post("http://"+addr+"/fail", "text/plain", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.NotContains(t, string(body), "vault")
	assert.NotContains(t, string(body), "SELECT")
}

// The producer must treat an upstream failure status as an error while still
// exposing the response for inspection.
func TestHTTPProducer_StatusIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream exploded", http.StatusInternalServerError)
	}))
	defer srv.Close()

	comp := NewHTTPComponent()
	c := NewCamelContext()
	c.AddComponent("http", comp)
	ep, err := c.CreateEndpoint(srv.URL)
	require.NoError(t, err)
	prod, err := ep.CreateProducer()
	require.NoError(t, err)

	ex := NewExchange(c.GetContext())
	err = prod.Send(ex)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrHTTPStatus)
	code, _ := ex.GetOut().GetHeader(CamelHttpResponseCode)
	assert.Equal(t, 500, code)

	// Opting out restores the permissive behaviour.
	comp.SetThrowExceptionOnFailure(false)
	ep2, err := c.CreateEndpoint(srv.URL)
	require.NoError(t, err)
	prod2, _ := ep2.CreateProducer()
	ex2 := NewExchange(c.GetContext())
	assert.NoError(t, prod2.Send(ex2))
}

// The producer must not forward the inbound request's framing headers.
func TestHTTPProducer_StripsHopByHopHeaders(t *testing.T) {
	var mu sync.Mutex
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = r.Header.Clone()
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewCamelContext()
	c.AddComponent("http", NewHTTPComponent())
	ep, _ := c.CreateEndpoint(srv.URL)
	prod, _ := ep.CreateProducer()

	ex := NewExchange(c.GetContext())
	ex.SetBody("payload")
	ex.SetHeader("Content-Length", "99999")
	ex.SetHeader("Connection", "close")
	ex.SetHeader("X-Trace", "keep-me")
	require.NoError(t, prod.Send(ex))

	mu.Lock()
	header := got.Clone()
	mu.Unlock()

	assert.Equal(t, "keep-me", header.Get("X-Trace"), "ordinary headers still forwarded")
	assert.NotEqual(t, "99999", header.Get("Content-Length"), "framing header must not be forwarded")
}

// A panicking processor must fail its exchange, not the process.
func TestProcessorPanicBecomesError(t *testing.T) {
	c := NewCamelContext()
	c.AddComponent("direct", NewDirectComponent())
	route := c.CreateRoute().From("direct:panic")
	route.ProcessFunc(func(e *Exchange) error { panic("boom") })
	require.NoError(t, c.Start())
	defer c.Stop()

	ep, err := c.CreateEndpoint("direct:panic")
	require.NoError(t, err)
	prod, err := ep.CreateProducer()
	require.NoError(t, err)

	ex := NewExchange(c.GetContext())
	err = prod.Send(ex)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrProcessorPanic)
}

// Split bookkeeping properties are branch-scoped and must not survive on the
// parent exchange.
func TestSplitter_DoesNotLeakBranchProperties(t *testing.T) {
	s := NewSplitter(func(e *Exchange) (any, error) { return []string{"a", "b"}, nil })
	s.SetAggregationStrategy(lastWinsStrategy{})

	ex := NewExchange(context.Background())
	ex.SetBody("orig")
	require.NoError(t, s.Process(ex))

	for _, key := range []string{CamelSplitIndex, CamelSplitSize, CamelSplitComplete} {
		_, present := ex.GetProperty(key)
		assert.Falsef(t, present, "%s must not leak onto the parent exchange", key)
	}
}

func TestMulticast_DoesNotLeakBranchProperties(t *testing.T) {
	m := NewMulticast()
	m.AddProcessor(ProcessorFunc(func(e *Exchange) error { return nil }))
	m.AddProcessor(ProcessorFunc(func(e *Exchange) error { return nil }))
	m.SetAggregationStrategy(lastWinsStrategy{})

	ex := NewExchange(context.Background())
	require.NoError(t, m.Process(ex))

	for _, key := range []string{CamelMulticastIndex, CamelMulticastSize, CamelMulticastComplete} {
		_, present := ex.GetProperty(key)
		assert.Falsef(t, present, "%s must not leak onto the parent exchange", key)
	}
}

type lastWinsStrategy struct{}

func (lastWinsStrategy) Aggregate(old, latest *Exchange) *Exchange { return latest }

// A []byte body is a binary payload, not a collection to split byte by byte.
func TestSplitter_DoesNotSplitByteSlicePerByte(t *testing.T) {
	var parts int
	s := NewSplitter(func(e *Exchange) (any, error) { return []byte("hello"), nil })
	s.AddProcessor(ProcessorFunc(func(e *Exchange) error {
		parts++
		return nil
	}))

	ex := NewExchange(context.Background())
	require.NoError(t, s.Process(ex))
	assert.Equal(t, 1, parts, "a []byte body must produce a single part, not one per byte")
}

// Zero-configuration guards: these used to be nil-pointer panics.
func TestNoPanicsFromZeroValueConstructors(t *testing.T) {
	require.NotPanics(t, func() {
		r := NewRoute().From("direct:x")
		assert.Error(t, r.Validate())
	})
	require.NotPanics(t, func() {
		assert.Error(t, NewCircuitBreaker().Process(NewExchange(context.Background())))
	})
}

// exec must publish its result on Out so InOut routes see the command output.
func TestExecProducer_PublishesResultOnOut(t *testing.T) {
	c := NewCamelContext()
	c.AddComponent("exec", NewExecComponent())
	ep, err := c.CreateEndpoint("exec:echo?args=hello")
	require.NoError(t, err)
	prod, _ := ep.CreateProducer()

	ex := NewExchange(c.GetContext())
	require.NoError(t, prod.Send(ex))
	assert.True(t, ex.HasOut(), "exec must set Out")
	assert.Equal(t, "hello\n", ex.GetOut().GetBody())
	assert.Equal(t, "hello\n", ex.GetIn().GetBody(), "In stays in sync for compatibility")
}

// Arguments are passed via execve, not a shell: metacharacters are data.
func TestValidateExecArgs_AllowsShellMetacharacters(t *testing.T) {
	for _, arg := range []string{`{"key":"value"}`, `p@ssw0rd$ecret`, `a>b`, `x & y`, `../relative/path`} {
		assert.NoErrorf(t, validateExecArgs([]string{arg}), "arg %q should be accepted", arg)
	}
	assert.Error(t, validateExecArgs([]string{"bad\x00arg"}), "NUL must still be rejected")
	assert.Error(t, validateExecArgs([]string{"bad\x07arg"}), "control chars must still be rejected")
}

// A CTE is a row-returning statement and must not be executed as a write.
func TestReturnsRows(t *testing.T) {
	rowReturning := []string{
		"SELECT * FROM t",
		"  select 1",
		"WITH t AS (SELECT 1) SELECT * FROM t",
		"-- a comment\nSELECT 1",
		"/* block */ SELECT 1",
		"(SELECT 1)",
		"INSERT INTO t VALUES (1) RETURNING id",
		"VALUES (1)",
		"PRAGMA table_info(t)",
	}
	for _, q := range rowReturning {
		assert.Truef(t, returnsRows(q), "%q should be treated as row-returning", q)
	}

	writes := []string{
		"INSERT INTO t VALUES (1)",
		"UPDATE t SET x = 1",
		"DELETE FROM t",
		"CREATE TABLE t (id INT)",
	}
	for _, q := range writes {
		assert.Falsef(t, returnsRows(q), "%q should be treated as a write", q)
	}
}

func TestChildProcessorContainers_ImplementInterface(t *testing.T) {
	cb := NewCircuitBreaker()
	p1 := NewPipeline()
	p2 := NewPipeline()
	cb.SetProcessor(p1)
	cb.SetFallbackProcessor(p2)
	cbContainer, ok := interface{}(cb).(childProcessorContainer)
	require.True(t, ok, "CircuitBreaker must implement childProcessorContainer")
	assert.Equal(t, []Processor{p1, p2}, cbContainer.childProcessors())

	res := NewResequencer(func(e *Exchange) (int64, error) { return 0, nil }, 0, time.Second)
	res.SetProcessor(p1)
	resContainer, ok := interface{}(res).(childProcessorContainer)
	require.True(t, ok, "Resequencer must implement childProcessorContainer")
	assert.Equal(t, []Processor{p1}, resContainer.childProcessors())

	saga := NewSagaProcessor()
	saga.AddStep(p1, p2)
	sagaContainer, ok := interface{}(saga).(childProcessorContainer)
	require.True(t, ok, "SagaProcessor must implement childProcessorContainer")
	assert.Equal(t, []Processor{p1, p2}, sagaContainer.childProcessors())

	tryProc := NewTryProcessor()
	tryProc.SetTryBlock(p1)
	tryProc.AddCatchClause("err", p2)
	p3 := NewPipeline()
	tryProc.SetFinallyBlock(p3)
	tryContainer, ok := interface{}(tryProc).(childProcessorContainer)
	require.True(t, ok, "TryProcessor must implement childProcessorContainer")
	assert.Equal(t, []Processor{p1, p2, p3}, tryContainer.childProcessors())
}

func waitForHTTP(t *testing.T, url string) {
	t.Helper()
	require.Eventually(t, func() bool {
		resp, err := http.Post(url, "text/plain", nil)
		if err != nil {
			return false
		}
		resp.Body.Close()
		return true
	}, 2*time.Second, 20*time.Millisecond, "server at %s never became ready", url)
}

func TestCamelContext_StartAfterStop(t *testing.T) {
	c := NewCamelContext()
	got := make(chan string, 1)
	_, err := c.CreateRouteBuilder().
		From("direct:restart").
		ProcessFunc(func(e *Exchange) error {
			got <- e.GetBody().(string)
			return nil
		}).
		Build()
	require.NoError(t, err)
	require.NoError(t, c.Start())
	require.NoError(t, c.Stop())
	require.NoError(t, c.Start())
	t.Cleanup(func() { _ = c.Stop() })

	ep, err := c.CreateEndpoint("direct:restart")
	require.NoError(t, err)
	prod, err := ep.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, prod.Start(c.GetContext()))
	ex := NewExchange(c.GetContext())
	ex.SetBody("after-restart")
	require.NoError(t, prod.Send(ex))
	select {
	case body := <-got:
		assert.Equal(t, "after-restart", body)
	case <-time.After(2 * time.Second):
		t.Fatal("route did not process after Start following Stop")
	}
}

func TestNestedSplitFilterErrorSurfacesAtBuild(t *testing.T) {
	c := NewCamelContext()
	split := c.CreateRouteBuilder().
		From("direct:split-err").
		Split(func(e *Exchange) (any, error) { return []string{"a"}, nil })
	split.DelayFunc(nil)
	_, err := split.End().Build()
	require.Error(t, err)
}

func TestChoiceAttachesToSplitContainer(t *testing.T) {
	c := NewCamelContext()
	split := c.CreateRouteBuilder().
		From("direct:split-choice").
		Split(func(e *Exchange) (any, error) {
			return []string{"keep", "drop"}, nil
		})
	split.Choice().
		When("${body}").
		SetBody("matched").
		EndChoice()
	route, err := split.End().Build()
	require.NoError(t, err)
	require.Len(t, route.processors, 1)
	kids := route.processors[0].(childProcessorContainer).childProcessors()
	require.Len(t, kids, 1)
	_, ok := kids[0].(*ChoiceProcessor)
	assert.True(t, ok, "Choice must be nested inside the Splitter, not the route")
}

func TestHTTPConsumer_SetsMethodHeaders(t *testing.T) {
	c := NewCamelContext()
	c.AddComponent("http", NewHTTPComponent())
	addr := getFreePort(t)
	var mu sync.Mutex
	var method, path string
	c.CreateRouteBuilder().
		From("http://" + addr + "/hdr").
		ProcessFunc(func(e *Exchange) error {
			m, _ := e.GetHeaderAsString(CamelHttpMethod)
			p, _ := e.GetHeaderAsString(CamelHttpPath)
			mu.Lock()
			method, path = m, p
			mu.Unlock()
			e.GetOut().SetBody("ok")
			return nil
		})
	require.NoError(t, c.Start())
	t.Cleanup(func() { _ = c.Stop() })
	waitForHTTP(t, "http://"+addr+"/hdr")

	resp, err := http.Get("http://" + addr + "/hdr")
	require.NoError(t, err)
	defer resp.Body.Close()
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, http.MethodGet, method)
	assert.Equal(t, "/hdr", path)
}

func TestHTTPEndpoint_RejectsHTTPSConsumer(t *testing.T) {
	ep, err := NewHTTPComponent().CreateEndpoint("https://127.0.0.1:8443/x")
	require.NoError(t, err)
	_, err = ep.CreateConsumer(ProcessorFunc(func(*Exchange) error { return nil }))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TLS")
}
