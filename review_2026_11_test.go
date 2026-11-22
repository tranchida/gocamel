package gocamel

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A route answering JSON to a JSON request sets Content-Type to the value the
// request carried: the echo guard must not drop it.
func TestHTTPConsumer_KeepsContentTypeEqualToRequest(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())

	c := NewCamelContext()
	_, err = c.CreateRouteBuilder().
		From("http://"+addr+"/api").
		SetBody(`{"ok":true}`).
		SetHeader("Content-Type", "application/json").
		Build()
	require.NoError(t, err)
	require.NoError(t, c.Start())
	t.Cleanup(func() { _ = c.Stop() })

	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/api", strings.NewReader(`{}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	assert.JSONEq(t, `{"ok":true}`, string(body))
	assert.Empty(t, resp.Header.Get("Authorization"), "credentials must still not be echoed")
}

// An unparseable URI must not carry its credentials into the error or through
// RedactURI.
func TestParseURI_ErrorDoesNotLeakCredentials(t *testing.T) {
	const uri = "ftp://user:pa%ss@host/dir?password=to%zzp&mode=x"

	_, err := ParseURI(uri)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "pa%ss")
	assert.NotContains(t, err.Error(), "%ss")
	assert.NotContains(t, err.Error(), "to%zzp")
	assert.Contains(t, err.Error(), "invalid percent-encoding")

	assert.Equal(t, "ftp://user:xxxxx@host/dir?password=xxxxx&mode=x", RedactURI(uri))
}

// The Stop EIP inside a Loop ends routing of the exchange: the processors
// after the loop must not run. A Filter inside the loop only ends the loop.
func TestLoop_StopEIPStopsTheRoute(t *testing.T) {
	c := NewCamelContext()
	afterStop, afterFilter := false, false
	_, err := c.CreateRouteBuilder().
		From("direct:loopStop").
		Loop(3).Stop().End().
		ProcessFunc(func(*Exchange) error { afterStop = true; return nil }).
		Build()
	require.NoError(t, err)
	_, err = c.CreateRouteBuilder().
		From("direct:loopFilter").
		Loop(3).Process(NewMessageFilter(func(*Exchange) (bool, error) { return false, nil })).End().
		ProcessFunc(func(*Exchange) error { afterFilter = true; return nil }).
		Build()
	require.NoError(t, err)
	require.NoError(t, c.Start())
	t.Cleanup(func() { _ = c.Stop() })

	for _, uri := range []string{"direct:loopStop", "direct:loopFilter"} {
		p, err := c.GetProducer(uri)
		require.NoError(t, err)
		require.NoError(t, CompleteExchange(NewExchange(context.Background()), p.Send(NewExchange(context.Background()))))
	}

	assert.False(t, afterStop, "Stop inside a Loop must stop the route")
	assert.True(t, afterFilter, "a Filter inside a Loop only ends the loop")
}

// A buffered exchange is delivered after the request that brought it is over:
// it must not run downstream with that request's cancelled context.
func TestResequencer_BufferedExchangeOutlivesRequestContext(t *testing.T) {
	seqOf := func(e *Exchange) (int64, error) {
		v, _ := e.GetHeaderAsInt("seq")
		return int64(v), nil
	}
	deliver := func(t *testing.T, rs *Resequencer) context.Context {
		delivered := make(chan context.Context, 1)
		rs.SetProcessor(ProcessorFunc(func(e *Exchange) error {
			delivered <- e.Context
			return nil
		}))
		t.Cleanup(func() { _ = rs.Stop() })

		reqCtx, cancel := context.WithCancel(context.Background())
		ex := NewExchange(reqCtx)
		ex.SetHeader("seq", 2) // out of order: buffered until the timeout
		require.ErrorIs(t, rs.Process(ex), ErrStopRouting)
		cancel()

		select {
		case ctx := <-delivered:
			return ctx
		case <-time.After(2 * time.Second):
			t.Fatal("buffered exchange was not delivered")
			return nil
		}
	}

	t.Run("standalone", func(t *testing.T) {
		ctx := deliver(t, NewResequencer(seqOf, 1, 20*time.Millisecond))
		assert.NoError(t, ctx.Err())
	})

	t.Run("in a route", func(t *testing.T) {
		routeCtx, stopRoute := context.WithCancel(context.Background())
		defer stopRoute()
		rs := NewResequencer(seqOf, 1, 20*time.Millisecond)
		rs.start(routeCtx)
		ctx := deliver(t, rs)
		assert.NoError(t, ctx.Err())
		stopRoute()
		assert.Error(t, ctx.Err(), "delivery follows the route lifecycle")
	})
}

// ctxAwareIdempotentRepo fails like a network-backed repository when it is
// handed a cancelled context.
type ctxAwareIdempotentRepo struct {
	*MemoryIdempotentRepository
}

func (r ctxAwareIdempotentRepo) Remove(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.MemoryIdempotentRepository.Remove(ctx, key)
}

// The key of a failed exchange must be rolled back even when the exchange
// failed because its context was cancelled.
func TestIdempotentConsumer_RollsBackKeyWithCancelledContext(t *testing.T) {
	repo := ctxAwareIdempotentRepo{NewMemoryIdempotentRepository()}
	consumer := NewIdempotentConsumer(func(*Exchange) (string, error) { return "k1", nil }, repo)

	ctx, cancel := context.WithCancel(context.Background())
	ex := NewExchange(ctx)
	require.NoError(t, consumer.Process(ex))
	cancel()
	_ = CompleteExchange(ex, context.Canceled)

	found, err := repo.Contains(context.Background(), "k1")
	require.NoError(t, err)
	assert.False(t, found, "the key must be removed so the message can be redelivered")
}

// A failure of a buffered exchange must not be reported to the exchange whose
// arrival flushed the buffer; an exchange's own failure still is.
func TestResequencer_ReportsOnlyTheCallersOwnFailure(t *testing.T) {
	failing := int64(0)
	rs := NewResequencer(func(e *Exchange) (int64, error) {
		v, _ := e.GetHeaderAsInt("seq")
		return int64(v), nil
	}, 1, time.Minute)
	var delivered []int
	rs.SetProcessor(ProcessorFunc(func(e *Exchange) error {
		seq, _ := e.GetHeaderAsInt("seq")
		delivered = append(delivered, seq)
		if int64(seq) == failing {
			return assert.AnError
		}
		return nil
	}))
	t.Cleanup(func() { _ = rs.Stop() })
	send := func(seq int) error {
		ex := NewExchange(context.Background())
		ex.SetHeader("seq", seq)
		return rs.Process(ex)
	}

	failing = 2
	require.ErrorIs(t, send(2), ErrStopRouting) // buffered
	err := send(1)                              // flushes 1 then 2; 2 fails
	assert.ErrorIs(t, err, ErrStopRouting)
	assert.NotErrorIs(t, err, assert.AnError, "exchange 1 succeeded")
	assert.Equal(t, []int{1, 2}, delivered)

	failing = 3
	assert.ErrorIs(t, send(3), assert.AnError, "an exchange's own failure is returned")
}

// eagerComponent's consumer routes one exchange from within Start, like a
// file consumer finding a backlog or a timer with delay=0.
type eagerComponent struct{}

func (eagerComponent) CreateEndpoint(uri string) (Endpoint, error) { return eagerEndpoint{uri}, nil }

type eagerEndpoint struct{ uri string }

func (e eagerEndpoint) URI() string                       { return e.uri }
func (e eagerEndpoint) CreateProducer() (Producer, error) { return nil, assert.AnError }
func (e eagerEndpoint) CreateConsumer(p Processor) (Consumer, error) {
	return &eagerConsumer{processor: p}, nil
}

type eagerConsumer struct{ processor Processor }

func (c *eagerConsumer) Start(ctx context.Context) error {
	ex := NewExchange(ctx)
	return CompleteExchange(ex, ProcessSafely(c.processor, ex))
}
func (c *eagerConsumer) Stop() error { return nil }

// A route calling direct: must find the called route started, even when that
// route is declared after it.
func TestCamelContext_StartsDirectRoutesFirst(t *testing.T) {
	c := NewCamelContext()
	c.AddComponent("eager", eagerComponent{})
	reached := false
	_, err := c.CreateRouteBuilder().From("eager:now").To("direct:later").Build()
	require.NoError(t, err)
	_, err = c.CreateRouteBuilder().From("direct:later").
		ProcessFunc(func(*Exchange) error { reached = true; return nil }).Build()
	require.NoError(t, err)

	require.NoError(t, c.Start())
	t.Cleanup(func() { _ = c.Stop() })
	assert.True(t, reached)
}

// Two routes can be exposed on the same host:port under different paths, and
// stopping one leaves the other serving.
func TestHTTPConsumer_RoutesShareAPort(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())

	c := NewCamelContext()
	orders, err := c.CreateRouteBuilder().From("http://" + addr + "/orders").SetBody("orders").Build()
	require.NoError(t, err)
	_, err = c.CreateRouteBuilder().From("http://" + addr + "/health").SetBody("ok").Build()
	require.NoError(t, err)
	require.NoError(t, c.Start())
	t.Cleanup(func() { _ = c.Stop() })

	get := func(path string) (int, string) {
		resp, err := http.Get("http://" + addr + path)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, string(body)
	}

	code, body := get("/orders")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "orders", body)
	code, body = get("/health")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "ok", body)

	require.NoError(t, orders.Stop())
	code, _ = get("/orders")
	assert.Equal(t, http.StatusNotFound, code)
	code, body = get("/health")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "ok", body)

	// The same path cannot be served twice on one port.
	dup, err := c.CreateRouteBuilder().From("http://" + addr + "/health").SetBody("dup").Build()
	require.NoError(t, err)
	err = dup.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already served")

	// Once every consumer is stopped the port is released.
	require.NoError(t, c.Stop())
	l, err = net.Listen("tcp", addr)
	require.NoError(t, err)
	require.NoError(t, l.Close())
}

type redactionTestComponent struct {
	sendErr error
	stopErr error
	pollErr error
	polling bool
}

func (c *redactionTestComponent) CreateEndpoint(uri string) (Endpoint, error) {
	return &redactionTestEndpoint{uri: uri, cfg: c}, nil
}

type redactionTestEndpoint struct {
	uri string
	cfg *redactionTestComponent
}

func (e *redactionTestEndpoint) URI() string { return e.uri }
func (e *redactionTestEndpoint) CreateProducer() (Producer, error) {
	return &redactionTestProducer{cfg: e.cfg}, nil
}
func (e *redactionTestEndpoint) CreateConsumer(Processor) (Consumer, error) {
	return nil, errors.New("not supported")
}
func (e *redactionTestEndpoint) Poll(context.Context, time.Duration) (*Exchange, error) {
	if !e.cfg.polling {
		return nil, errors.New("not polling")
	}
	return nil, e.cfg.pollErr
}

type nonPollingRedactionComponent struct{}

func (nonPollingRedactionComponent) CreateEndpoint(uri string) (Endpoint, error) {
	return nonPollingRedactionEndpoint{uri: uri}, nil
}

type nonPollingRedactionEndpoint struct{ uri string }

func (e nonPollingRedactionEndpoint) URI() string { return e.uri }
func (e nonPollingRedactionEndpoint) CreateProducer() (Producer, error) {
	return &redactionTestProducer{cfg: &redactionTestComponent{}}, nil
}
func (e nonPollingRedactionEndpoint) CreateConsumer(Processor) (Consumer, error) {
	return nil, errors.New("not supported")
}

type redactionTestProducer struct{ cfg *redactionTestComponent }

func (p *redactionTestProducer) Start(context.Context) error { return nil }
func (p *redactionTestProducer) Stop() error                 { return p.cfg.stopErr }
func (p *redactionTestProducer) Send(*Exchange) error        { return p.cfg.sendErr }

func TestCreateEndpointAndEIPs_RedactURIsInErrors(t *testing.T) {
	const (
		userSecret  = "s3cr3tPass"
		querySecret = "s3cr3tQuery"
		badURI      = "ftp://user:my%pass@host/dir"
		secretURI   = "unknown://user:" + userSecret + "@host/dir?password=" + querySecret
	)

	t.Run("ComponentRegistry.CreateEndpoint", func(t *testing.T) {
		reg := NewComponentRegistry()
		_, err := reg.CreateEndpoint(badURI)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "my%pass")
	})

	ctx := NewCamelContext()
	ctx.AddComponent("stopfail", &redactionTestComponent{stopErr: errors.New("stop boom")})
	ctx.AddComponent("sendfail", &redactionTestComponent{sendErr: errors.New("send boom")})
	ctx.AddComponent("pollfail", &redactionTestComponent{polling: true, pollErr: errors.New("poll boom")})
	ctx.AddComponent("nopoll", nonPollingRedactionComponent{})

	t.Run("lazyProducer stop and stopped errors", func(t *testing.T) {
		stopFailURI := "stopfail://user:" + userSecret + "@host/dir?password=" + querySecret
		r, err := ctx.CreateRouteBuilder().
			From("direct:lp-redact").
			To(stopFailURI).
			Build()
		require.NoError(t, err)
		require.NoError(t, ctx.Start())
		t.Cleanup(func() { _ = ctx.Stop() })

		require.NoError(t, r.Process(NewExchange(context.Background())))

		stopErr := r.Stop()
		require.Error(t, stopErr)
		assert.NotContains(t, stopErr.Error(), userSecret)
		assert.NotContains(t, stopErr.Error(), querySecret)

		stoppedErr := r.Process(NewExchange(context.Background()))
		require.Error(t, stoppedErr)
		assert.NotContains(t, stoppedErr.Error(), userSecret)
		assert.NotContains(t, stoppedErr.Error(), querySecret)
	})

	t.Run("RecipientList errors and debug logs", func(t *testing.T) {
		buf := withCapturedLogger(t)
		ex := NewExchange(context.Background())
		ex.SetHeader("target", secretURI)

		for _, rl := range []*RecipientList{
			NewRecipientList(ctx).SetStaticURIs(secretURI),
			NewRecipientList(ctx).SetSimpleExpression("${header.target}"),
			NewRecipientList(ctx).SetURIFunc(func(*Exchange) ([]string, error) { return []string{secretURI}, nil }),
		} {
			err := rl.Process(ex.Copy())
			require.Error(t, err)
			assert.NotContains(t, err.Error(), userSecret)
			assert.NotContains(t, err.Error(), querySecret)
		}

		logs := buf.String()
		assert.NotContains(t, logs, userSecret)
		assert.NotContains(t, logs, querySecret)
	})

	t.Run("RoutingSlip errors and debug logs", func(t *testing.T) {
		buf := withCapturedLogger(t)
		sendFailURI := "sendfail://user:" + userSecret + "@host/dir?password=" + querySecret
		ex := NewExchange(context.Background())
		ex.SetHeader("target", secretURI)

		for _, rs := range []*RoutingSlip{
			NewRoutingSlip(ctx).SetStaticURIs(secretURI),
			NewRoutingSlip(ctx).SetSimpleExpression("${header.target}"),
			NewRoutingSlip(ctx).SetURIFunc(func(*Exchange) ([]string, error) { return []string{secretURI}, nil }),
			NewRoutingSlip(ctx).SetStaticURIs(sendFailURI),
		} {
			err := rs.Process(ex.Copy())
			require.Error(t, err)
			assert.NotContains(t, err.Error(), userSecret)
			assert.NotContains(t, err.Error(), querySecret)
		}

		logs := buf.String()
		assert.NotContains(t, logs, userSecret)
		assert.NotContains(t, logs, querySecret)
	})

	t.Run("DynamicRouter error and debug log", func(t *testing.T) {
		buf := withCapturedLogger(t)
		dr := NewDynamicRouter(ctx, func(*Exchange) (string, error) {
			return secretURI, nil
		})
		err := dr.Process(NewExchange(context.Background()))
		require.Error(t, err)
		assert.NotContains(t, err.Error(), userSecret)
		assert.NotContains(t, err.Error(), querySecret)

		logs := buf.String()
		assert.NotContains(t, logs, userSecret)
		assert.NotContains(t, logs, querySecret)
	})

	t.Run("ContentEnricher errors", func(t *testing.T) {
		for _, u := range []string{
			secretURI,
			"sendfail://user:" + userSecret + "@host/dir?password=" + querySecret,
		} {
			err := NewContentEnricher(ctx, u, nil).Process(NewExchange(context.Background()))
			require.Error(t, err)
			assert.NotContains(t, err.Error(), userSecret)
			assert.NotContains(t, err.Error(), querySecret)
		}
	})

	t.Run("PollEnricher errors", func(t *testing.T) {
		for _, u := range []string{
			secretURI,
			"nopoll://user:" + userSecret + "@host/dir?password=" + querySecret,
			"pollfail://user:" + userSecret + "@host/dir?password=" + querySecret,
		} {
			err := NewPollEnricher(ctx, u, nil, 10*time.Millisecond).Process(NewExchange(context.Background()))
			require.Error(t, err)
			assert.NotContains(t, err.Error(), userSecret)
			assert.NotContains(t, err.Error(), querySecret)
		}
	})

	t.Run("WireTap warn and debug logs", func(t *testing.T) {
		buf := withCapturedLogger(t)

		wtFail := NewWireTap(ctx, secretURI)
		require.NoError(t, wtFail.Process(NewExchange(context.Background())))
		require.NoError(t, wtFail.Stop())

		wtOk := NewWireTap(ctx, "mock:tap?password="+querySecret)
		require.NoError(t, wtOk.Process(NewExchange(context.Background())))
		require.NoError(t, wtOk.Stop())

		logs := buf.String()
		assert.Contains(t, logs, "WireTap: tap delivery failed")
		assert.Contains(t, logs, "WireTap: tap delivered successfully")
		assert.NotContains(t, logs, userSecret)
		assert.NotContains(t, logs, querySecret)
	})
}
