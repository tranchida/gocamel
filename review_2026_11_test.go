package gocamel

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
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

func TestRouteValidate_RecursesIntoNestedContainersAndOnException(t *testing.T) {
	cases := []struct {
		name    string
		wantErr string
		build   func(*CamelContext) (*Route, error)
	}{
		{
			name:    "Choice When",
			wantErr: "period",
			build: func(ctx *CamelContext) (*Route, error) {
				return ctx.CreateRouteBuilder().
					From("direct:choice-when").
					Choice().
					When("${body} == 'a'").
					Process(NewSamplerPeriod(0)).
					EndChoice().
					Build()
			},
		},
		{
			name:    "Choice Otherwise",
			wantErr: "period",
			build: func(ctx *CamelContext) (*Route, error) {
				return ctx.CreateRouteBuilder().
					From("direct:choice-other").
					Choice().
					When("${body} == 'a'").
					SetBody("ok").
					Otherwise().
					Process(NewSamplerPeriod(0)).
					EndChoice().
					Build()
			},
		},
		{
			name:    "Multicast",
			wantErr: "period",
			build: func(ctx *CamelContext) (*Route, error) {
				return ctx.CreateRouteBuilder().
					From("direct:multicast").
					Multicast().
					Process(NewSamplerPeriod(0)).
					End().
					Build()
			},
		},
		{
			name:    "Splitter",
			wantErr: "period",
			build: func(ctx *CamelContext) (*Route, error) {
				return ctx.CreateRouteBuilder().
					From("direct:splitter").
					Split(func(e *Exchange) (any, error) { return []string{"a"}, nil }).
					Process(NewSamplerPeriod(0)).
					End().
					Build()
			},
		},
		{
			name:    "Loop",
			wantErr: "period",
			build: func(ctx *CamelContext) (*Route, error) {
				return ctx.CreateRouteBuilder().
					From("direct:loop").
					Loop(2).
					Process(NewSamplerPeriod(0)).
					End().
					Build()
			},
		},
		{
			name:    "DoTry Try",
			wantErr: "period",
			build: func(ctx *CamelContext) (*Route, error) {
				return ctx.CreateRouteBuilder().
					From("direct:try").
					DoTry().
					Process(NewSamplerPeriod(0)).
					End().
					Build()
			},
		},
		{
			name:    "DoTry DoCatch",
			wantErr: "period",
			build: func(ctx *CamelContext) (*Route, error) {
				return ctx.CreateRouteBuilder().
					From("direct:catch").
					DoTry().
					ProcessFunc(func(*Exchange) error { return nil }).
					DoCatch("").
					Process(NewSamplerPeriod(0)).
					End().
					Build()
			},
		},
		{
			name:    "DoTry DoFinally",
			wantErr: "period",
			build: func(ctx *CamelContext) (*Route, error) {
				return ctx.CreateRouteBuilder().
					From("direct:finally").
					DoTry().
					ProcessFunc(func(*Exchange) error { return nil }).
					DoFinally().
					Process(NewSamplerPeriod(0)).
					End().
					Build()
			},
		},
		{
			name:    "OnException",
			wantErr: "period",
			build: func(ctx *CamelContext) (*Route, error) {
				return ctx.CreateRouteBuilder().
					OnException(errors.New("boom")).
					Process(NewSamplerPeriod(0)).
					End().
					From("direct:on-exception").
					SetBody("ok").
					Build()
			},
		},
		{
			name:    "Nested RecipientList in Multicast",
			wantErr: "recipient list",
			build: func(ctx *CamelContext) (*Route, error) {
				oversize := "${header." + strings.Repeat("a", MaxSimpleInputBytes+1) + "}"
				return ctx.CreateRouteBuilder().
					From("direct:nested-rl").
					Multicast().
					Process(NewRecipientList(ctx).SetSimpleExpression(oversize)).
					End().
					Build()
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := NewCamelContext()
			route, err := tc.build(ctx)
			require.NoError(t, err)
			valErr := route.Validate()
			require.Error(t, valErr)
			assert.Contains(t, valErr.Error(), tc.wantErr)
		})
	}
}

type panickingSync struct {
	panicOnComplete bool
	panicOnFailure  bool
}

func (s panickingSync) OnComplete(*Exchange) {
	if s.panicOnComplete {
		panic("sync complete panic")
	}
}

func (s panickingSync) OnFailure(*Exchange) {
	if s.panicOnFailure {
		panic("sync failure panic")
	}
}

type recordingSync struct {
	completed atomic.Bool
	failed    atomic.Bool
}

func (s *recordingSync) OnComplete(*Exchange) { s.completed.Store(true) }
func (s *recordingSync) OnFailure(*Exchange)  { s.failed.Store(true) }

func TestExchangeDone_RecoversPanickingSynchronization(t *testing.T) {
	t.Run("OnComplete panic does not unwind or skip subsequent cleanup", func(t *testing.T) {
		ex := NewExchange(context.Background())
		ex.AddSynchronization(nil)
		ex.AddSynchronization(panickingSync{panicOnComplete: true})
		cleanup := &recordingSync{}
		ex.AddSynchronization(cleanup)

		require.NotPanics(t, func() {
			require.NoError(t, CompleteExchange(ex, nil))
		})
		assert.True(t, cleanup.completed.Load(), "cleanup synchronization must still run")
	})

	t.Run("OnFailure panic does not unwind or skip subsequent cleanup", func(t *testing.T) {
		ex := NewExchange(context.Background())
		ex.AddSynchronization(nil)
		ex.AddSynchronization(panickingSync{panicOnFailure: true})
		cleanup := &recordingSync{}
		ex.AddSynchronization(cleanup)

		sentinel := errors.New("boom")
		require.NotPanics(t, func() {
			err := CompleteExchange(ex, sentinel)
			assert.ErrorIs(t, err, sentinel)
		})
		assert.True(t, cleanup.failed.Load(), "cleanup failure synchronization must still run")
	})
}

type lpSlowStartComponent struct {
	inStart      chan struct{}
	releaseStart chan struct{}
	stoppedCount atomic.Int32
}

func (c *lpSlowStartComponent) CreateEndpoint(uri string) (Endpoint, error) {
	return &lpSlowStartEndpoint{uri: uri, comp: c}, nil
}

type lpSlowStartEndpoint struct {
	uri  string
	comp *lpSlowStartComponent
}

func (e *lpSlowStartEndpoint) URI() string { return e.uri }
func (e *lpSlowStartEndpoint) CreateProducer() (Producer, error) {
	return &lpSlowStartProducer{comp: e.comp}, nil
}
func (e *lpSlowStartEndpoint) CreateConsumer(Processor) (Consumer, error) {
	return nil, errors.New("not supported")
}

type lpSlowStartProducer struct {
	comp *lpSlowStartComponent
}

func (p *lpSlowStartProducer) Start(context.Context) error {
	close(p.comp.inStart)
	<-p.comp.releaseStart
	return nil
}
func (p *lpSlowStartProducer) Stop() error {
	p.comp.stoppedCount.Add(1)
	return nil
}
func (p *lpSlowStartProducer) Send(*Exchange) error { return nil }

func TestLazyProducer_StopDoesNotBlockOnSlowStart(t *testing.T) {
	ctx := NewCamelContext()
	comp := &lpSlowStartComponent{
		inStart:      make(chan struct{}),
		releaseStart: make(chan struct{}),
	}
	ctx.AddComponent("blockstart", comp)

	route, err := ctx.CreateRouteBuilder().
		From("direct:lp-lockfree").
		To("blockstart:target").
		Build()
	require.NoError(t, err)
	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })

	sendErrCh := make(chan error, 1)
	go func() {
		sendErrCh <- route.Process(NewExchange(context.Background()))
	}()

	select {
	case <-comp.inStart:
	case <-time.After(2 * time.Second):
		t.Fatal("producer did not enter Start")
	}

	stopDone := make(chan error, 1)
	go func() {
		stopDone <- route.Stop()
	}()

	select {
	case err := <-stopDone:
		require.NoError(t, err)
	case <-time.After(200 * time.Millisecond):
		close(comp.releaseStart)
		t.Fatal("route.Stop() blocked on slow lazyProducer Start()")
	}

	close(comp.releaseStart)
	select {
	case err := <-sendErrCh:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("send goroutine did not finish")
	}
	assert.EqualValues(t, 1, comp.stoppedCount.Load(), "late-started producer must be stopped and not leaked")
}

type failAfterSendComponent struct {
	producerStopped atomic.Int32
}

func (c *failAfterSendComponent) CreateEndpoint(uri string) (Endpoint, error) {
	return &failAfterSendEndpoint{uri: uri, comp: c}, nil
}

type failAfterSendEndpoint struct {
	uri  string
	comp *failAfterSendComponent
}

func (e *failAfterSendEndpoint) URI() string { return e.uri }
func (e *failAfterSendEndpoint) CreateProducer() (Producer, error) {
	return &failAfterSendProducer{comp: e.comp}, nil
}
func (e *failAfterSendEndpoint) CreateConsumer(p Processor) (Consumer, error) {
	return &failAfterSendConsumer{processor: p}, nil
}

type failAfterSendProducer struct{ comp *failAfterSendComponent }

func (p *failAfterSendProducer) Start(context.Context) error { return nil }
func (p *failAfterSendProducer) Stop() error {
	p.comp.producerStopped.Add(1)
	return nil
}
func (p *failAfterSendProducer) Send(*Exchange) error { return nil }

type failAfterSendConsumer struct{ processor Processor }

func (c *failAfterSendConsumer) Start(ctx context.Context) error {
	_ = c.processor.Process(NewExchange(ctx))
	return errors.New("consumer failed after dispatching")
}
func (c *failAfterSendConsumer) Stop() error { return nil }

func TestRouteStart_ConsumerFailureRollsBackLazyProducersAndWireTap(t *testing.T) {
	ctx := NewCamelContext()
	comp := &failAfterSendComponent{}
	ctx.AddComponent("failafter", comp)

	route, err := ctx.CreateRouteBuilder().
		From("failafter:in").
		WireTap("mock:tap").
		To("failafter:out").
		Build()
	require.NoError(t, err)

	err = ctx.Start()
	require.Error(t, err)
	assert.False(t, route.IsStarted())
	assert.EqualValues(t, 1, comp.producerStopped.Load(), "lazyProducer initialized during failed Start must be stopped on rollback")

	// ProducerCache on CamelContext must remain usable after failed Start rollback.
	_, cacheErr := ctx.GetProducer("mock:after-rollback")
	assert.NoError(t, cacheErr)
}

func TestRemoveHeadersAndProperties_MatchesNewlines(t *testing.T) {
	ctx := NewCamelContext()
	route, err := ctx.CreateRouteBuilder().
		From("direct:newline-remove").
		RemoveHeaders("Camel*", "CamelHttp*").
		RemoveProperties("Camel*", "CamelHttp*").
		Build()
	require.NoError(t, err)

	ex := NewExchange(context.Background())
	ex.GetIn().SetHeader("Camel\nLine", "remove")
	ex.GetIn().SetHeader("CamelHttp\nKeep", "keep")
	ex.SetProperty("Camel\nProp", "remove")
	ex.SetProperty("CamelHttp\nPropKeep", "keep")

	require.NoError(t, route.Process(ex))

	assert.False(t, ex.GetIn().HasHeader("Camel\nLine"), "RemoveHeaders '*' must match across newlines")
	assert.True(t, ex.GetIn().HasHeader("CamelHttp\nKeep"), "RemoveHeaders exclusion '*' must match across newlines")
	assert.False(t, ex.HasProperty("Camel\nProp"), "RemoveProperties '*' must match across newlines")
	assert.True(t, ex.HasProperty("CamelHttp\nPropKeep"), "RemoveProperties exclusion '*' must match across newlines")
}

func TestRouteBuilder_FailedBuildDetachesRouteFromContext(t *testing.T) {
	ctx := NewCamelContext()
	_, buildErr := ctx.CreateRouteBuilder().
		From("direct:broken-filter").
		SetID("broken-route").
		Filter("${header.x").
		To("mock:never").
		Build()
	require.Error(t, buildErr)

	// Even if caller ignores buildErr and starts ctx, the broken route must be detached and not start.
	assert.Empty(t, ctx.GetRoutes())
	assert.Nil(t, ctx.GetRoute("broken-route"))
	require.NoError(t, ctx.Start())
	t.Cleanup(func() { _ = ctx.Stop() })
	assert.Equal(t, 0, ctx.GetStartedRouteCount())

	// The failed route's ID must have been released so it can be reused without duplicate warnings.
	validRoute, err := ctx.CreateRouteBuilder().
		From("direct:reused-id").
		SetID("broken-route").
		To("mock:ok").
		Build()
	require.NoError(t, err)
	assert.Equal(t, "broken-route", validRoute.ID)
	assert.Len(t, ctx.GetRoutes(), 1)

	// Even if Build() is never called on a broken RouteBuilder, recordErr propagates onto route.err
	// so ctx.Start() fails instead of silently starting a partial route without the Filter.
	unbuiltCtx := NewCamelContext()
	unbuiltCtx.CreateRouteBuilder().
		From("direct:unbuilt-broken").
		Filter("${header.x")
	startErr := unbuiltCtx.Start()
	require.Error(t, startErr)
	assert.Contains(t, startErr.Error(), "malformed simple template")
}

func TestRouteBuilder_NilInputsAndPreservesFirstError(t *testing.T) {
	t.Run("nil DSL inputs fail safely at Build without panics", func(t *testing.T) {
		var nilFilterFn func(*Exchange) (bool, error)
		var nilIDFn func(*Exchange) (string, error)
		var nilDRFn func(*Exchange) (string, error)
		var nilRLFn func(*Exchange) ([]string, error)
		var nilProcFn ProcessorFunc

		cases := []struct {
			name  string
			build func(*CamelContext) (*Route, error)
		}{
			{"Aggregate(nil)", func(c *CamelContext) (*Route, error) {
				return c.CreateRouteBuilder().From("direct:a").Aggregate(nil).Build()
			}},
			{"Process(nil)", func(c *CamelContext) (*Route, error) {
				return c.CreateRouteBuilder().From("direct:a").Process(nil).Build()
			}},
			{"Process(ProcessorFunc(nil))", func(c *CamelContext) (*Route, error) {
				return c.CreateRouteBuilder().From("direct:a").Process(nilProcFn).Build()
			}},
			{"ProcessFunc(nil)", func(c *CamelContext) (*Route, error) {
				return c.CreateRouteBuilder().From("direct:a").ProcessFunc(nil).Build()
			}},
			{"SetHeadersFunc(nil)", func(c *CamelContext) (*Route, error) {
				return c.CreateRouteBuilder().From("direct:a").SetHeadersFunc(nil).Build()
			}},
			{"SetPropertyFunc(nil)", func(c *CamelContext) (*Route, error) {
				return c.CreateRouteBuilder().From("direct:a").SetPropertyFunc("k", nil).Build()
			}},
			{"Filter(nil)", func(c *CamelContext) (*Route, error) {
				return c.CreateRouteBuilder().From("direct:a").Filter(nil).Build()
			}},
			{"Filter(typed nil func)", func(c *CamelContext) (*Route, error) {
				return c.CreateRouteBuilder().From("direct:a").Filter(nilFilterFn).Build()
			}},
			{"IdempotentConsumer(x, nil)", func(c *CamelContext) (*Route, error) {
				return c.CreateRouteBuilder().From("direct:a").IdempotentConsumer("x", nil).Build()
			}},
			{"IdempotentConsumer(typed nil func)", func(c *CamelContext) (*Route, error) {
				return c.CreateRouteBuilder().From("direct:a").IdempotentConsumer(nilIDFn, NewMemoryIdempotentRepository()).Build()
			}},
			{"Split(nil)", func(c *CamelContext) (*Route, error) {
				return c.CreateRouteBuilder().From("direct:a").Split(nil).SetBody("x").End().Build()
			}},
			{"LoopFunc(nil)", func(c *CamelContext) (*Route, error) {
				return c.CreateRouteBuilder().From("direct:a").LoopFunc(nil).SetBody("x").End().Build()
			}},
			{"DynamicRouter(nil)", func(c *CamelContext) (*Route, error) {
				return c.CreateRouteBuilder().From("direct:a").DynamicRouter(nil).Build()
			}},
			{"DynamicRouter(typed nil func)", func(c *CamelContext) (*Route, error) {
				return c.CreateRouteBuilder().From("direct:a").DynamicRouter(nilDRFn).Build()
			}},
			{"RecipientList(nil)", func(c *CamelContext) (*Route, error) {
				return c.CreateRouteBuilder().From("direct:a").RecipientList(nil).End().Build()
			}},
			{"RecipientList(typed nil func)", func(c *CamelContext) (*Route, error) {
				return c.CreateRouteBuilder().From("direct:a").RecipientList(nilRLFn).End().Build()
			}},
			{"RoutingSlip(nil)", func(c *CamelContext) (*Route, error) {
				return c.CreateRouteBuilder().From("direct:a").RoutingSlip(nil).Build()
			}},
			{"RoutingSlip(typed nil func)", func(c *CamelContext) (*Route, error) {
				return c.CreateRouteBuilder().From("direct:a").RoutingSlip(nilRLFn).Build()
			}},
			{"Transform(nil)", func(c *CamelContext) (*Route, error) {
				return c.CreateRouteBuilder().From("direct:a").Transform(nil).Build()
			}},
			{"Intercept(nil)", func(c *CamelContext) (*Route, error) {
				return c.CreateRouteBuilder().From("direct:a").Intercept(nil).Build()
			}},
			{"Marshal(nil)", func(c *CamelContext) (*Route, error) {
				return c.CreateRouteBuilder().From("direct:a").Marshal(nil).Build()
			}},
			{"Unmarshal(nil)", func(c *CamelContext) (*Route, error) {
				return c.CreateRouteBuilder().From("direct:a").Unmarshal(nil).Build()
			}},
			{"Choice.Process(nil)", func(c *CamelContext) (*Route, error) {
				return c.CreateRouteBuilder().From("direct:a").Choice().When("${body} == 'a'").Process(nil).EndChoice().Build()
			}},
			{"Choice.ProcessFunc(nil)", func(c *CamelContext) (*Route, error) {
				return c.CreateRouteBuilder().From("direct:a").Choice().When("${body} == 'a'").ProcessFunc(nil).EndChoice().Build()
			}},
			{"Otherwise.Process(nil)", func(c *CamelContext) (*Route, error) {
				return c.CreateRouteBuilder().From("direct:a").Choice().When("${body} == 'a'").SetBody("ok").Otherwise().Process(nil).EndChoice().Build()
			}},
			{"Otherwise.ProcessFunc(nil)", func(c *CamelContext) (*Route, error) {
				return c.CreateRouteBuilder().From("direct:a").Choice().When("${body} == 'a'").SetBody("ok").Otherwise().ProcessFunc(nil).EndChoice().Build()
			}},
			{"Saga().ActionFunc(nil, nil)", func(c *CamelContext) (*Route, error) {
				return c.CreateRouteBuilder().From("direct:a").Saga().ActionFunc(nil, nil).End().Build()
			}},
			{"Saga().Action('', 'direct:c')", func(c *CamelContext) (*Route, error) {
				return c.CreateRouteBuilder().From("direct:a").Saga().Action("", "direct:c").End().Build()
			}},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				c := NewCamelContext()
				require.NotPanics(t, func() {
					r, err := tc.build(c)
					require.Error(t, err)
					assert.Nil(t, r)
					assert.Empty(t, c.GetRoutes())
				})
			})
		}

		t.Run("NewRouteBuilder(nil) does not panic", func(t *testing.T) {
			require.NotPanics(t, func() {
				_, err := NewRouteBuilder(nil).From("direct:a").Build()
				require.Error(t, err)
			})
		})

		t.Run("Saga Action with empty compensationURI is valid non-compensated step", func(t *testing.T) {
			c := NewCamelContext()
			repo := NewMemorySagaRepository()
			var step1Ran bool
			_, err := c.CreateRouteBuilder().From("direct:saga-ok").ProcessFunc(func(*Exchange) error {
				step1Ran = true
				return nil
			}).Build()
			require.NoError(t, err)

			route, err := c.CreateRouteBuilder().
				From("direct:saga-main").
				Saga().
				Repository(repo).
				Action("direct:saga-ok", "").
				ActionFunc(func(*Exchange) error { return errors.New("step2 failed") }, nil).
				End().
				Build()
			require.NoError(t, err)
			require.NoError(t, c.Start())
			t.Cleanup(func() { _ = c.Stop() })

			err = route.Process(NewExchange(context.Background()))
			require.Error(t, err)
			assert.True(t, step1Ran)
			incomplete, err := repo.ListIncomplete(context.Background())
			require.NoError(t, err)
			assert.Empty(t, incomplete, "non-compensated step must not record empty checkpoint")
		})
	})

	t.Run("direct nil processor guards in route.go and choice.go", func(t *testing.T) {
		c := NewCamelContext()
		r := c.CreateRoute().From("direct:nil-guards").EnableMessageHistory()
		r.AddProcessor(nil)
		r.AddProcessor((*Aggregator)(nil))
		cp := NewChoiceProcessor().When("${body} == 'a'", nil)
		r.AddProcessor(cp)

		require.NotPanics(t, func() {
			require.NoError(t, c.Start())
		})
		t.Cleanup(func() { _ = c.Stop() })

		ex := NewExchange(context.Background())
		ex.SetBody("a")
		require.NotPanics(t, func() {
			_ = r.Process(ex)
			_ = cp.Process(ex)
		})
	})

	t.Run("sub-DSL errors preserve the first build error", func(t *testing.T) {
		oversized := strings.Repeat("a", MaxSimpleInputBytes+1)

		t.Run("LoopSimple preserves prior error and isolates body", func(t *testing.T) {
			c := NewCamelContext()
			rb := c.CreateRouteBuilder().
				From("direct:loop-err").
				Filter(123).
				LoopSimple("${second_bad").
				SetBody("leaked").
				End()
			assert.Empty(t, rb.route.processors, "failed LoopSimple must not leak chained processors onto parent route")
			_, err := rb.Build()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unsupported expression type int")
			assert.NotContains(t, err.Error(), "loop count")
		})

		t.Run("LoopSimple first error preserved over subsequent nested and outer errors", func(t *testing.T) {
			c := NewCamelContext()
			_, err := c.CreateRouteBuilder().
				From("direct:loop-first").
				LoopSimple("${first_bad").
				SimpleSetBody(oversized).
				End().
				Filter(123).
				Build()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "loop count")
			assert.NotContains(t, err.Error(), "MaxSimpleInputBytes")
			assert.NotContains(t, err.Error(), "unsupported expression type")
		})

		t.Run("RecipientList preserves first error", func(t *testing.T) {
			c := NewCamelContext()
			_, err := c.CreateRouteBuilder().
				From("direct:rl-first").
				RecipientList("${first_bad").
				End().
				RecipientList(123).
				End().
				Build()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "recipient list")
			assert.NotContains(t, err.Error(), "unsupported RecipientList")

			_, err = c.CreateRouteBuilder().
				From("direct:rl-multicast").
				Multicast().
				RecipientList("${first_bad").
				RecipientList(123).
				End().
				Build()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "recipient list")
			assert.NotContains(t, err.Error(), "unsupported RecipientList")

			_, err = c.CreateRouteBuilder().
				From("direct:rl-pipeline").
				Multicast().
				Pipeline().
				RecipientList("${first_bad").
				RecipientList(123).
				End().
				End().
				Build()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "recipient list")
			assert.NotContains(t, err.Error(), "unsupported RecipientList")

			_, err = c.CreateRouteBuilder().
				From("direct:rl-split").
				Split(func(*Exchange) (any, error) { return []string{"a"}, nil }).
				RecipientList("${first_bad").
				RecipientList(123).
				End().
				Build()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "recipient list")
			assert.NotContains(t, err.Error(), "unsupported RecipientList")
		})

		t.Run("Choice empty When before Otherwise stays matched", func(t *testing.T) {
			c := NewCamelContext()
			route, err := c.CreateRouteBuilder().
				From("direct:choice-empty-when").
				Choice().
				When("${body} == 'a'").
				Otherwise().
				SetBody("otherwise").
				EndChoice().
				Build()
			require.NoError(t, err)

			ex := NewExchange(context.Background())
			ex.SetBody("a")
			require.NoError(t, route.Process(ex))
			assert.Equal(t, "a", ex.GetResponse().GetBody(), "empty When match must not fall through to Otherwise")
		})

		t.Run("Choice.Otherwise preserves first error", func(t *testing.T) {
			c := NewCamelContext()
			_, err := c.CreateRouteBuilder().
				From("direct:choice-first").
				Choice().
				When("${first_bad").
				Otherwise().
				SimpleSetBody(oversized).
				SimpleSetHeader("h", "${third_bad").
				EndChoice().
				Build()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "choice When")
			assert.NotContains(t, err.Error(), "MaxSimpleInputBytes")

			_, err = c.CreateRouteBuilder().
				From("direct:otherwise-first").
				Choice().
				When("${body} == 'a'").
				SetBody("ok").
				Otherwise().
				SimpleSetBody(oversized).
				SimpleSetHeader("h", "${second_bad").
				EndChoice().
				Build()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "MaxSimpleInputBytes")
			assert.NotContains(t, err.Error(), "unclosed or empty")

			_, err = c.CreateRouteBuilder().
				From("direct:otherwise-header-first").
				Choice().
				When("${body} == 'a'").
				SetBody("ok").
				Otherwise().
				SimpleSetHeader("h", oversized).
				SimpleSetBody("${second_bad").
				EndChoice().
				Build()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "MaxSimpleInputBytes")
			assert.NotContains(t, err.Error(), "unclosed or empty")
		})

		t.Run("DynamicRouter preserves first error", func(t *testing.T) {
			c := NewCamelContext()
			_, err := c.CreateRouteBuilder().
				From("direct:dr-first").
				DynamicRouter("${first_bad").
				DynamicRouter(123).
				Build()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "dynamic router: invalid simple expression")
			assert.NotContains(t, err.Error(), "unsupported expression type int")
		})
	})
}

func TestRouteBuilder_LogUsesFinalRouteIDAfterSetID(t *testing.T) {
	buf := withCapturedLogger(t)
	ctx := NewCamelContext()

	route, err := ctx.CreateRouteBuilder().
		From("direct:log-setid").
		Log("hi").
		LogBody("body-log").
		LogHeaders("hdr-log").
		LogSimple("simple-${body}").
		Split(func(*Exchange) (any, error) { return []string{"part"}, nil }).
		Log("split-log").
		End().
		SetID("orders").
		Build()
	require.NoError(t, err)

	ex := NewExchange(context.Background())
	ex.SetBody("payload")
	ex.SetHeader("k", "v")
	require.NoError(t, route.Process(ex))

	logs := buf.String()
	assert.Contains(t, logs, "route_id=orders")
	assert.NotContains(t, logs, "route_id=route-1")
	assert.Equal(t, 5, strings.Count(logs, "route_id=orders"))

	// Concurrent SetID vs Log/LogBody/LogHeaders must be race-free under -race.
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			if n%2 == 0 {
				route.SetID("orders-a")
			} else {
				route.SetID("orders-b")
			}
		}(i)
		go func() {
			defer wg.Done()
			_ = route.Process(NewExchange(context.Background()))
		}()
	}
	wg.Wait()
}

func TestRouteBuilder_SubBuildersDoNotEmbedRouteBuilder(t *testing.T) {
	disallowed := []string{
		"Build", "From", "SetID", "SetDescription", "Transacted",
		"Intercept", "EnableMessageHistory", "ErrorHandler",
		"DeadLetterChannel", "OnException",
	}
	for _, typ := range []reflect.Type{
		reflect.TypeFor[SplitDefinition](),
		reflect.TypeFor[MulticastDefinition](),
		reflect.TypeFor[PipelineDefinition](),
		reflect.TypeFor[LoopDefinition](),
	} {
		t.Run(typ.Name(), func(t *testing.T) {
			for i := 0; i < typ.NumField(); i++ {
				f := typ.Field(i)
				assert.Falsef(t, f.Anonymous, "%s must not embed %s anonymously", typ.Name(), f.Type)
			}
			ptrType := reflect.PointerTo(typ)
			for _, method := range disallowed {
				_, ok := ptrType.MethodByName(method)
				assert.Falsef(t, ok, "*%s must not expose %s()", typ.Name(), method)
			}
		})
	}

	// Verify forwarded step-level verbs on all four definitions keep compiling and preserve errors.
	c := NewCamelContext()
	_, err := c.CreateRouteBuilder().
		From("direct:subdsl-forward").
		Split(func(*Exchange) (any, error) { return []string{"a"}, nil }).
		DelayFunc(nil).
		SetBody("x").
		End().
		Build()
	require.Error(t, err)

	_, err = c.CreateRouteBuilder().
		From("direct:multicast-forward").
		Multicast().
		Filter("${bad").
		SetBody("x").
		End().
		Build()
	require.Error(t, err)

	_, err = c.CreateRouteBuilder().
		From("direct:pipeline-forward").
		Multicast().
		Pipeline().
		Filter("${bad").
		SetBody("x").
		End().
		End().
		Build()
	require.Error(t, err)

	_, err = c.CreateRouteBuilder().
		From("direct:loop-forward").
		Loop(2).
		Filter("${bad").
		SetBody("x").
		End().
		Build()
	require.Error(t, err)
}
