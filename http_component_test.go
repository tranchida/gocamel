package gocamel

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestHTTPConsumer_StartReturnsBindError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	consumer := &HTTPConsumer{
		uri:       "http://" + listener.Addr().String() + "/test",
		processor: ProcessorFunc(func(*Exchange) error { return nil }),
	}
	err = consumer.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "error listening")
}

func TestHTTPConsumer_BindsConfiguredHost(t *testing.T) {
	consumer := &HTTPConsumer{
		uri:       "http://127.0.0.1:0/test",
		processor: ProcessorFunc(func(*Exchange) error { return nil }),
	}
	require.NoError(t, consumer.Start(context.Background()))
	assert.Equal(t, "127.0.0.1:0", consumer.server.Addr)
	require.NoError(t, consumer.Stop())
}

func TestHTTPConsumer_RecoversProcessorPanic(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	consumer := &HTTPConsumer{
		uri: "http://" + addr + "/",
		processor: ProcessorFunc(func(*Exchange) error {
			panic("boom")
		}),
	}
	require.NoError(t, consumer.Start(context.Background()))
	t.Cleanup(func() { _ = consumer.Stop() })

	var resp *http.Response
	require.Eventually(t, func() bool {
		r, err := http.Get("http://" + addr + "/")
		if err != nil {
			return false
		}
		resp = r
		return true
	}, 2*time.Second, 20*time.Millisecond)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestHTTPProducer_UsesExchangeContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var requestContext context.Context
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		requestContext = req.Context()
		return nil, req.Context().Err()
	})}
	producer := &HTTPProducer{uri: "http://example.test", client: client}
	exchange := NewExchange(ctx)

	err := producer.Send(exchange)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.True(t, errors.Is(requestContext.Err(), context.Canceled))
}

func TestHTTP_RedactsURIAndBlocksDowngradeRedirectAndStopsWithSlowReaderAndGzipDecompression(t *testing.T) {
	t.Run("redacts_uri_in_endpoint_errors_and_logs", func(t *testing.T) {
		logBuf := &syncBuffer{}
		prev := GetLogger()
		t.Cleanup(func() { SetLogger(prev) })
		SetLogger(slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelWarn})))

		comp := NewHTTPComponent()
		rawURI := "http://alice:s3cr3tPass@127.0.0.1:1/api?apiKey=secretKey123&mode=test"
		ep, err := comp.CreateEndpoint(rawURI)
		require.NoError(t, err)
		assert.NotContains(t, ep.URI(), "s3cr3tPass")
		assert.NotContains(t, ep.URI(), "secretKey123")
		assert.Contains(t, ep.URI(), "xxxxx")

		// *url.Error redaction on transport error
		prodErrClient := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return nil, &url.Error{Op: "Get", URL: req.URL.String(), Err: errors.New("dial failed")}
		})}
		prodErr := &HTTPProducer{uri: rawURI, client: prodErrClient, throwExceptionOnFailure: true}
		err = prodErr.Send(NewExchange(context.Background()))
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "s3cr3tPass")
		assert.NotContains(t, err.Error(), "secretKey123")
		var urlErr *url.Error
		require.ErrorAs(t, err, &urlErr)
		assert.NotContains(t, urlErr.URL, "s3cr3tPass")
		assert.NotContains(t, urlErr.URL, "secretKey123")

		// ErrHTTPStatus redaction on status >= 300
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}))
		defer srv.Close()
		u, err := url.Parse(srv.URL)
		require.NoError(t, err)
		u.User = url.UserPassword("alice", "s3cr3tPass")
		u.RawQuery = "apiKey=secretKey123&token=tok999"
		epStatus, err := comp.CreateEndpoint(u.String())
		require.NoError(t, err)
		prodStatus, err := epStatus.CreateProducer()
		require.NoError(t, err)
		err = prodStatus.Send(NewExchange(context.Background()))
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrHTTPStatus)
		assert.NotContains(t, err.Error(), "s3cr3tPass")
		assert.NotContains(t, err.Error(), "secretKey123")
		assert.NotContains(t, err.Error(), "tok999")

		// Invalid URI in CreateConsumer does not leak credentials
		badEp := &HTTPEndpoint{uri: "http://alice:s3cr3tPass@127.0.0.1:bad/x?apiKey=secretKey123"}
		_, err = badEp.CreateConsumer(ProcessorFunc(func(*Exchange) error { return nil }))
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "s3cr3tPass")
		assert.NotContains(t, err.Error(), "secretKey123")

		// Consumer logs redact URI credentials
		addr := getFreePort(t)
		consURI := "http://alice:s3cr3tPass@" + addr + "/fail?apiKey=secretKey123"
		consEp, err := comp.CreateEndpoint(consURI)
		require.NoError(t, err)
		cons, err := consEp.CreateConsumer(ProcessorFunc(func(*Exchange) error {
			return errors.New("route boom")
		}))
		require.NoError(t, err)
		require.NoError(t, cons.Start(context.Background()))
		t.Cleanup(func() { _ = cons.Stop() })
		waitForHTTP(t, "http://"+addr+"/fail")

		logged := logBuf.String()
		assert.NotContains(t, logged, "s3cr3tPass")
		assert.NotContains(t, logged, "secretKey123")
	})

	t.Run("blocks_https_to_http_downgrade_and_strips_cross_origin_auth", func(t *testing.T) {
		var downgradedHits atomic.Int32
		plainTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			downgradedHits.Add(1)
			w.WriteHeader(http.StatusOK)
		}))
		defer plainTarget.Close()

		tlsSource := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, plainTarget.URL+"/leak", http.StatusFound)
		}))
		defer tlsSource.Close()

		comp := NewHTTPComponent()
		tlsTransport := tlsSource.Client().Transport.(*http.Transport).Clone()
		comp.client.Transport = tlsTransport

		epDown, err := comp.CreateEndpoint(tlsSource.URL + "/start")
		require.NoError(t, err)
		prodDown, err := epDown.CreateProducer()
		require.NoError(t, err)

		exDown := NewExchange(context.Background())
		exDown.SetHeader(CamelHttpMethod, http.MethodGet)
		exDown.SetHeader("Authorization", "Bearer secret-token")
		exDown.SetHeader("X-Api-Key", "api-secret")
		err = prodDown.Send(exDown)
		require.Error(t, err, "HTTPS->HTTP redirect downgrade must be blocked")
		assert.Equal(t, int32(0), downgradedHits.Load(), "downgraded HTTP target must not be contacted")

		// Cross-origin redirect strips Authorization, Cookie, X-Api-Key, Api-Key, X-Auth-Token
		var mu sync.Mutex
		var gotHeaders http.Header
		destServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			gotHeaders = r.Header.Clone()
			mu.Unlock()
			_, _ = w.Write([]byte("arrived"))
		}))
		defer destServer.Close()

		originServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/same" {
				http.Redirect(w, r, "/same-dest", http.StatusFound)
				return
			}
			if r.URL.Path == "/same-dest" {
				mu.Lock()
				gotHeaders = r.Header.Clone()
				mu.Unlock()
				_, _ = w.Write([]byte("same-origin"))
				return
			}
			http.Redirect(w, r, destServer.URL+"/target", http.StatusFound)
		}))
		defer originServer.Close()

		comp2 := NewHTTPComponent()
		epCross, err := comp2.CreateEndpoint(originServer.URL + "/cross")
		require.NoError(t, err)
		prodCross, err := epCross.CreateProducer()
		require.NoError(t, err)

		exCross := NewExchange(context.Background())
		exCross.SetHeader(CamelHttpMethod, http.MethodGet)
		exCross.SetHeader("Authorization", "Bearer top-secret")
		exCross.SetHeader("Cookie", "sid=xyz")
		exCross.SetHeader("X-Api-Key", "key-1")
		exCross.SetHeader("Api-Key", "key-2")
		exCross.SetHeader("X-Auth-Token", "tok-3")
		exCross.SetHeader("X-Trace", "keep-me")
		require.NoError(t, prodCross.Send(exCross))

		mu.Lock()
		crossHdr := gotHeaders.Clone()
		mu.Unlock()
		for _, h := range []string{"Authorization", "Cookie", "X-Api-Key", "Api-Key", "X-Auth-Token"} {
			assert.Empty(t, crossHdr.Get(h), "%s must be stripped on cross-origin redirect", h)
		}
		assert.Equal(t, "keep-me", crossHdr.Get("X-Trace"))

		// Same-origin redirect keeps X-Api-Key
		epSame, err := comp2.CreateEndpoint(originServer.URL + "/same")
		require.NoError(t, err)
		prodSame, err := epSame.CreateProducer()
		require.NoError(t, err)
		exSame := NewExchange(context.Background())
		exSame.SetHeader(CamelHttpMethod, http.MethodGet)
		exSame.SetHeader("X-Api-Key", "same-key")
		require.NoError(t, prodSame.Send(exSame))

		mu.Lock()
		sameHdr := gotHeaders.Clone()
		mu.Unlock()
		assert.Equal(t, "same-key", sameHdr.Get("X-Api-Key"))

		// SetCheckRedirect overrides policy, and nil restores the default policy
		comp2.SetCheckRedirect(func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		})
		comp2.SetThrowExceptionOnFailure(false)
		epCustom, err := comp2.CreateEndpoint(originServer.URL + "/cross")
		require.NoError(t, err)
		prodCustom, err := epCustom.CreateProducer()
		require.NoError(t, err)
		exCustom := NewExchange(context.Background())
		exCustom.SetHeader(CamelHttpMethod, http.MethodGet)
		require.NoError(t, prodCustom.Send(exCustom))
		code, _ := exCustom.GetOut().GetHeader(CamelHttpResponseCode)
		assert.Equal(t, http.StatusFound, code)

		comp2.SetCheckRedirect(nil)
		comp2.SetThrowExceptionOnFailure(true)
		exRestored := NewExchange(context.Background())
		exRestored.SetHeader(CamelHttpMethod, http.MethodGet)
		exRestored.SetHeader("X-Api-Key", "restored-secret")
		require.NoError(t, prodCustom.Send(exRestored))
		mu.Lock()
		restoredHdr := gotHeaders.Clone()
		mu.Unlock()
		assert.Empty(t, restoredHdr.Get("X-Api-Key"), "SetCheckRedirect(nil) must restore default header stripping")
	})

	t.Run("stops_promptly_against_stalled_tcp_reader", func(t *testing.T) {
		addr := getFreePort(t)
		comp := NewHTTPComponent()
		ep, err := comp.CreateEndpoint("http://" + addr + "/slow")
		require.NoError(t, err)

		entered := make(chan struct{})
		largePayload := bytes.Repeat([]byte("A"), 16<<20) // exceeds loopback TCP buffers
		cons, err := ep.CreateConsumer(ProcessorFunc(func(e *Exchange) error {
			close(entered)
			e.GetOut().SetBody(largePayload)
			return nil
		}))
		require.NoError(t, err)
		hc := cons.(*HTTPConsumer)
		hc.shutdownTimeout = 50 * time.Millisecond

		require.NoError(t, hc.Start(context.Background()))

		conn, err := net.Dial("tcp", addr)
		require.NoError(t, err)
		defer conn.Close()
		_, err = conn.Write([]byte("GET /slow HTTP/1.1\r\nHost: " + addr + "\r\n\r\n"))
		require.NoError(t, err)

		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("handler did not start processing stalled request")
		}

		stopDone := make(chan error, 1)
		go func() { stopDone <- hc.Stop() }()
		select {
		case err := <-stopDone:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("HTTPConsumer.Stop hung against stalled TCP reader")
		}
	})

	t.Run("write_response_deadline_unblocks_slow_reader", func(t *testing.T) {
		addr := getFreePort(t)
		comp := NewHTTPComponent()
		ep, err := comp.CreateEndpoint("http://" + addr + "/deadline")
		require.NoError(t, err)

		logBuf := &syncBuffer{}
		prev := GetLogger()
		t.Cleanup(func() { SetLogger(prev) })
		SetLogger(slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelWarn})))

		largePayload := bytes.Repeat([]byte("B"), 16<<20)
		cons, err := ep.CreateConsumer(ProcessorFunc(func(e *Exchange) error {
			e.GetOut().SetBody(largePayload)
			return nil
		}))
		require.NoError(t, err)
		hc := cons.(*HTTPConsumer)
		hc.writeTimeout = 50 * time.Millisecond
		hc.shutdownTimeout = 200 * time.Millisecond

		require.NoError(t, hc.Start(context.Background()))
		t.Cleanup(func() { _ = hc.Stop() })

		conn, err := net.Dial("tcp", addr)
		require.NoError(t, err)
		defer conn.Close()
		_, err = conn.Write([]byte("GET /deadline HTTP/1.1\r\nHost: " + addr + "\r\n\r\n"))
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			return bytes.Contains([]byte(logBuf.String()), []byte("error writing response body"))
		}, 2*time.Second, 10*time.Millisecond)
	})

	t.Run("consumer_to_producer_decompresses_gzip_and_strips_response_headers", func(t *testing.T) {
		var mu sync.Mutex
		var upstreamAcceptEncoding string
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			upstreamAcceptEncoding = r.Header.Get("Accept-Encoding")
			mu.Unlock()

			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Set("CamelFileName", "../../etc/passwd")
			w.Header().Set("CamelSqlQuery", "DROP TABLE users")
			w.Header().Set("Connection", "keep-alive")
			w.Header().Set("Keep-Alive", "timeout=5")
			w.Header().Set("X-Upstream", "ok")
			w.WriteHeader(http.StatusOK)

			gz := gzip.NewWriter(w)
			_, _ = gz.Write([]byte("hello-from-upstream"))
			_ = gz.Close()
		}))
		defer upstream.Close()

		addr := getFreePort(t)
		ctx := NewCamelContext()
		var afterBody string
		var afterHeaders map[string]any
		_, err := ctx.CreateRouteBuilder().
			From("http://" + addr + "/proxy").
			To(upstream.URL).
			ProcessFunc(func(e *Exchange) error {
				switch b := e.GetIn().GetBody().(type) {
				case []byte:
					afterBody = string(b)
				case string:
					afterBody = b
				}
				afterHeaders = e.GetIn().GetHeaders()
				return nil
			}).
			Build()
		require.NoError(t, err)
		require.NoError(t, ctx.Start())
		t.Cleanup(func() { _ = ctx.Stop() })
		waitForHTTP(t, "http://"+addr+"/proxy")

		req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/proxy", nil)
		require.NoError(t, err)
		req.Header.Set("Accept-Encoding", "gzip")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		respBytes, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "hello-from-upstream", afterBody, "upstream gzip response must be transparently decompressed")
		assert.Equal(t, "hello-from-upstream", string(respBytes))
		assert.Equal(t, "ok", afterHeaders["X-Upstream"])
		assert.NotContains(t, afterHeaders, "CamelFileName")
		assert.NotContains(t, afterHeaders, "Camelfilename")
		assert.NotContains(t, afterHeaders, "CamelSqlQuery")
		assert.NotContains(t, afterHeaders, "Camelsqlquery")
		assert.NotContains(t, afterHeaders, "Connection")
		assert.NotContains(t, afterHeaders, "Keep-Alive")

		mu.Lock()
		gotAE := upstreamAcceptEncoding
		mu.Unlock()
		assert.Equal(t, "gzip", gotAE, "http.Transport should manage Accept-Encoding automatically")
	})
}
