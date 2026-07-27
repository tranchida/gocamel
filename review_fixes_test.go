package gocamel

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The HTTP consumer must not echo inbound request headers back in the
// response. Doing so leaked Cookie/Authorization to the client and reinjected
// the request's Content-Length, corrupting the response framing.
func TestHTTPConsumer_DoesNotEchoRequestHeaders(t *testing.T) {
	c := NewCamelContext()
	c.AddComponent("http", NewHTTPComponent())
	c.CreateRouteBuilder().
		From("http://127.0.0.1:18191/noout").
		ProcessFunc(func(e *Exchange) error { return nil })
	require.NoError(t, c.Start())
	defer c.Stop()
	waitForHTTP(t, "http://127.0.0.1:18191/noout")

	req, _ := http.NewRequest("POST", "http://127.0.0.1:18191/noout", strings.NewReader("hello-body"))
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
	c.CreateRouteBuilder().
		From("http://127.0.0.1:18192/json").
		ProcessFunc(func(e *Exchange) error {
			e.GetOut().SetHeader("Content-Type", "application/json")
			e.GetOut().SetHeader(CamelHttpResponseCode, 201)
			e.GetOut().SetBody(`{"ok":true}`)
			return nil
		})
	require.NoError(t, c.Start())
	defer c.Stop()
	waitForHTTP(t, "http://127.0.0.1:18192/json")

	resp, err := http.Post("http://127.0.0.1:18192/json", "text/plain", nil)
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
	c.CreateRouteBuilder().
		From("http://127.0.0.1:18193").
		ProcessFunc(func(e *Exchange) error {
			e.GetOut().SetBody("root")
			return nil
		})
	require.NotPanics(t, func() {
		require.NoError(t, c.Start())
	})
	defer c.Stop()
	waitForHTTP(t, "http://127.0.0.1:18193/")

	resp, err := http.Post("http://127.0.0.1:18193/anything", "text/plain", nil)
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
	c.CreateRouteBuilder().
		From("http://127.0.0.1:18194/upload").
		ProcessFunc(func(e *Exchange) error { return nil })
	require.NoError(t, c.Start())
	defer c.Stop()
	waitForHTTP(t, "http://127.0.0.1:18194/upload")

	resp, err := http.Post("http://127.0.0.1:18194/upload", "application/octet-stream",
		strings.NewReader(strings.Repeat("x", 4096)))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
}

// Internal error detail must not be disclosed to the client.
func TestHTTPConsumer_DoesNotLeakErrorDetail(t *testing.T) {
	c := NewCamelContext()
	c.AddComponent("http", NewHTTPComponent())
	c.CreateRouteBuilder().
		From("http://127.0.0.1:18195/fail").
		ProcessFunc(func(e *Exchange) error {
			return errors.New("SELECT secret FROM vault -- internal detail")
		})
	require.NoError(t, c.Start())
	defer c.Stop()
	waitForHTTP(t, "http://127.0.0.1:18195/fail")

	resp, err := http.Post("http://127.0.0.1:18195/fail", "text/plain", nil)
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
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
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

	assert.Equal(t, "keep-me", got.Get("X-Trace"), "ordinary headers still forwarded")
	assert.NotEqual(t, "99999", got.Get("Content-Length"), "framing header must not be forwarded")
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

func waitForHTTP(t *testing.T, url string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		resp, err := http.Post(url, "text/plain", nil)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server at %s never became ready", url)
}
