package stomp

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/go-stomp/stomp"
	"github.com/go-stomp/stomp/frame"
	"github.com/go-stomp/stomp/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gocamel "github.com/tranchida/gocamel"
)

// startTestBroker starts an in-process STOMP broker on an ephemeral port
// and returns its address. The listener is closed via t.Cleanup.
func startTestBroker(t *testing.T) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	srv := &server.Server{}
	go func() { _ = srv.Serve(l) }()

	// Wait for the broker to accept connections
	require.Eventually(t, func() bool {
		conn, err := net.Dial("tcp", l.Addr().String())
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, 5*time.Second, 10*time.Millisecond)

	return l.Addr().String()
}

// publish sends one message through a direct endpoint, mimicking an
// upstream route. The direct:pub route must already be built and started.
func publish(t *testing.T, camelCtx *gocamel.CamelContext, body string, headers map[string]any) error {
	t.Helper()

	endpoint, err := camelCtx.CreateEndpoint("direct:pub")
	require.NoError(t, err)
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	exchange := gocamel.NewExchange(context.Background())
	exchange.In.SetBody(body)
	for k, v := range headers {
		exchange.In.SetHeader(k, v)
	}
	return producer.Send(exchange)
}

func TestStompComponent_ProduceConsume(t *testing.T) {
	addr := startTestBroker(t)

	camelCtx := gocamel.NewCamelContext()
	camelCtx.AddComponent("direct", gocamel.NewDirectComponent())
	camelCtx.AddComponent("stomp", NewStompComponent())

	done := make(chan string, 1)
	_, err := camelCtx.CreateRouteBuilder().
		From("stomp://queue/orders?host=" + addr).
		ProcessFunc(func(e *gocamel.Exchange) error {
			body, _ := e.In.GetBodyAsString()
			if b, ok := e.In.GetBody().([]byte); ok {
				body = string(b)
			}
			done <- body
			return nil
		}).
		Build()
	require.NoError(t, err)

	_, err = camelCtx.CreateRouteBuilder().
		From("direct:pub").
		To("stomp://queue/orders?host=" + addr).
		Build()
	require.NoError(t, err)

	require.NoError(t, camelCtx.Start())
	t.Cleanup(func() { _ = camelCtx.Stop() })

	// Give subscription time to register on the broker
	time.Sleep(100 * time.Millisecond)

	require.NoError(t, publish(t, camelCtx, "Order #1234", nil))

	select {
	case val := <-done:
		assert.Equal(t, "Order #1234", val)
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for STOMP message in ProduceConsume test")
	}
}

func TestStompComponent_HeaderOverrideIgnoredByDefault(t *testing.T) {
	addr := startTestBroker(t)

	camelCtx := gocamel.NewCamelContext()
	camelCtx.AddComponent("direct", gocamel.NewDirectComponent())
	camelCtx.AddComponent("stomp", NewStompComponent())

	done := make(chan string, 1)
	_, err := camelCtx.CreateRouteBuilder().
		From("stomp://queue/alerts?host=" + addr).
		ProcessFunc(func(e *gocamel.Exchange) error {
			body, _ := e.In.GetBodyAsString()
			if b, ok := e.In.GetBody().([]byte); ok {
				body = string(b)
			}
			done <- body
			return nil
		}).
		Build()
	require.NoError(t, err)

	_, err = camelCtx.CreateRouteBuilder().
		From("direct:pub").
		To("stomp://queue/alerts?host=" + addr).
		Build()
	require.NoError(t, err)

	require.NoError(t, camelCtx.Start())
	t.Cleanup(func() { _ = camelCtx.Stop() })

	time.Sleep(100 * time.Millisecond)

	// The header attempts to redirect the send to another destination,
	// but it must be ignored: the message lands on the URI destination.
	err = publish(t, camelCtx, "Delivered To URI Destination", map[string]any{
		CamelStompDestination: "/queue/other",
	})
	require.NoError(t, err)

	select {
	case val := <-done:
		assert.Equal(t, "Delivered To URI Destination", val)
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for STOMP message in HeaderOverrideIgnoredByDefault test")
	}
}

func TestStompComponent_DynamicDestination(t *testing.T) {
	addr := startTestBroker(t)

	camelCtx := gocamel.NewCamelContext()
	camelCtx.AddComponent("direct", gocamel.NewDirectComponent())
	camelCtx.AddComponent("stomp", NewStompComponent())

	done := make(chan string, 1)
	_, err := camelCtx.CreateRouteBuilder().
		From("stomp://queue/alerts?host=" + addr).
		ProcessFunc(func(e *gocamel.Exchange) error {
			body, _ := e.In.GetBodyAsString()
			if b, ok := e.In.GetBody().([]byte); ok {
				body = string(b)
			}
			done <- body
			return nil
		}).
		Build()
	require.NoError(t, err)

	// Generic producer route opting in to header-based destination override
	_, err = camelCtx.CreateRouteBuilder().
		From("direct:pub").
		To("stomp://queue/generic?host=" + addr + "&allowHeaderOverride=true").
		Build()
	require.NoError(t, err)

	require.NoError(t, camelCtx.Start())
	t.Cleanup(func() { _ = camelCtx.Stop() })

	time.Sleep(100 * time.Millisecond)

	// Send message dynamically targeting /queue/alerts via header
	err = publish(t, camelCtx, "System Overheat Alert!", map[string]any{
		CamelStompDestination: "/queue/alerts",
	})
	require.NoError(t, err)

	select {
	case val := <-done:
		assert.Equal(t, "System Overheat Alert!", val)
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for STOMP message in DynamicDestination test")
	}

	// An empty overridden destination is rejected.
	err = publish(t, camelCtx, "data", map[string]any{
		CamelStompDestination: "",
	})
	assert.ErrorContains(t, err, "invalid destination")
}

func TestStompComponent_AckRetry(t *testing.T) {
	addr := startTestBroker(t)

	camelCtx := gocamel.NewCamelContext()
	camelCtx.AddComponent("direct", gocamel.NewDirectComponent())
	camelCtx.AddComponent("stomp", NewStompComponent())

	var attempts int
	done := make(chan int, 1)
	_, err := camelCtx.CreateRouteBuilder().
		From("stomp://queue/billing?host=" + addr + "&ack=client-individual").
		ProcessFunc(func(e *gocamel.Exchange) error {
			attempts++
			if attempts < 2 {
				return assert.AnError // fail the first attempt
			}
			done <- attempts
			return nil
		}).
		Build()
	require.NoError(t, err)

	_, err = camelCtx.CreateRouteBuilder().
		From("direct:pub").
		To("stomp://queue/billing?host=" + addr).
		Build()
	require.NoError(t, err)

	require.NoError(t, camelCtx.Start())
	t.Cleanup(func() { _ = camelCtx.Stop() })

	time.Sleep(100 * time.Millisecond)

	require.NoError(t, publish(t, camelCtx, "Invoice #42", nil))

	select {
	case n := <-done:
		assert.Equal(t, 2, n) // first attempt failed, second succeeded
	case <-time.After(3 * time.Second):
		t.Fatal("Timed out waiting for STOMP message in AckRetry test")
	}
}

func TestStompComponent_SensitiveHeadersNotForwarded(t *testing.T) {
	addr := startTestBroker(t)

	camelCtx := gocamel.NewCamelContext()
	camelCtx.AddComponent("direct", gocamel.NewDirectComponent())
	camelCtx.AddComponent("stomp", NewStompComponent())

	var receivedAuthorization string
	done := make(chan string, 1)
	_, err := camelCtx.CreateRouteBuilder().
		From("stomp://queue/secure?host=" + addr).
		ProcessFunc(func(e *gocamel.Exchange) error {
			if v, ok := e.In.GetHeader("Authorization"); ok {
				receivedAuthorization, _ = v.(string)
			}
			body, _ := e.In.GetBodyAsString()
			if b, ok := e.In.GetBody().([]byte); ok {
				body = string(b)
			}
			done <- body
			return nil
		}).
		Build()
	require.NoError(t, err)

	_, err = camelCtx.CreateRouteBuilder().
		From("direct:pub").
		To("stomp://queue/secure?host=" + addr).
		Build()
	require.NoError(t, err)

	require.NoError(t, camelCtx.Start())
	t.Cleanup(func() { _ = camelCtx.Stop() })

	time.Sleep(100 * time.Millisecond)

	// The Authorization header must not be forwarded to the broker.
	err = publish(t, camelCtx, "Hello", map[string]any{
		"Authorization": "Bearer secret-token",
		"X-Custom":      "custom-value",
	})
	require.NoError(t, err)

	select {
	case val := <-done:
		assert.Equal(t, "Hello", val)
		assert.Empty(t, receivedAuthorization, "Authorization header must not be forwarded to the broker")
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for STOMP message in SensitiveHeadersNotForwarded test")
	}
}

func TestStompConsumer_StopReleasesEndpointRegistration(t *testing.T) {
	component := NewStompComponent()
	endpoint, err := component.CreateEndpoint("stomp://queue/restart")
	require.NoError(t, err)

	consumer, err := endpoint.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	require.NoError(t, err)
	assert.NoError(t, consumer.Stop())

	_, err = endpoint.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	require.NoError(t, err)
}

// generateTestCertificate creates a self-signed certificate valid for
// 127.0.0.1 and localhost, returning the PEM-encoded certificate and key.
// serverAuth selects between a server certificate (ExtKeyUsageServerAuth)
// and a client certificate (ExtKeyUsageClientAuth).
func generateTestCertificate(t *testing.T, serverAuth bool) (certPEM, keyPEM []byte) {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	extKeyUsage := []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	if serverAuth {
		extKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:  extKeyUsage,
		IsCA:         true,
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	require.NoError(t, err)

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(priv)
	require.NoError(t, err)
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return certPEM, keyPEM
}

// writePEMFile writes a PEM payload to a temp file and returns its path.
func writePEMFile(t *testing.T, name string, pem []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, pem, 0o600))
	return path
}

// startTestTLSBroker starts an in-process STOMP broker behind a TLS
// listener using a self-signed certificate, and returns its address plus
// the CA file path clients use to verify it.
func startTestTLSBroker(t *testing.T) (addr, caFile string) {
	t.Helper()

	certPEM, keyPEM := generateTestCertificate(t, true)
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)

	l, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	srv := &server.Server{}
	go func() { _ = srv.Serve(l) }()

	require.Eventually(t, func() bool {
		conn, err := net.Dial("tcp", l.Addr().String())
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, 5*time.Second, 10*time.Millisecond)

	caFile = writePEMFile(t, "ca.pem", certPEM)
	return l.Addr().String(), caFile
}

func TestStompComponent_TLS(t *testing.T) {
	addr, caFile := startTestTLSBroker(t)

	camelCtx := gocamel.NewCamelContext()
	camelCtx.AddComponent("direct", gocamel.NewDirectComponent())
	camelCtx.AddComponent("stomp", NewStompComponent())

	done := make(chan string, 1)
	_, err := camelCtx.CreateRouteBuilder().
		From("stomp://queue/orders?host=" + addr + "&ssl=true&sslCAFile=" + caFile).
		ProcessFunc(func(e *gocamel.Exchange) error {
			if b, ok := e.In.GetBody().([]byte); ok {
				done <- string(b)
			}
			return nil
		}).
		Build()
	require.NoError(t, err)

	_, err = camelCtx.CreateRouteBuilder().
		From("direct:pub").
		To("stomp://queue/orders?host=" + addr + "&ssl=true&sslCAFile=" + caFile).
		Build()
	require.NoError(t, err)

	require.NoError(t, camelCtx.Start())
	t.Cleanup(func() { _ = camelCtx.Stop() })

	time.Sleep(100 * time.Millisecond)

	require.NoError(t, publish(t, camelCtx, "TLS Order #1234", nil))

	select {
	case val := <-done:
		assert.Equal(t, "TLS Order #1234", val)
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for STOMP message over TLS")
	}
}

func TestStompComponent_TLSVerificationFailure(t *testing.T) {
	addr, _ := startTestTLSBroker(t)

	// Without the CA file the self-signed certificate cannot be verified:
	// endpoint creation succeeds but the connection must fail loudly.
	component := NewStompComponent()
	endpoint, err := component.CreateEndpoint("stomp://queue/orders?host=" + addr + "&ssl=true")
	require.NoError(t, err)

	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)
	err = producer.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TLS handshake")
}

// startTestMTLSBroker starts an in-process STOMP broker behind a TLS
// listener that requires client certificate authentication, and returns
// its address plus the CA file and client certificate/key files.
func startTestMTLSBroker(t *testing.T) (addr, caFile, certFile, keyFile string) {
	t.Helper()

	serverCertPEM, serverKeyPEM := generateTestCertificate(t, true)
	serverCert, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
	require.NoError(t, err)

	clientCertPEM, clientKeyPEM := generateTestCertificate(t, false)

	clientCA := x509.NewCertPool()
	require.True(t, clientCA.AppendCertsFromPEM(clientCertPEM))

	l, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCA,
		MinVersion:   tls.VersionTLS12,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	srv := &server.Server{}
	go func() { _ = srv.Serve(l) }()

	require.Eventually(t, func() bool {
		conn, err := net.Dial("tcp", l.Addr().String())
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, 5*time.Second, 10*time.Millisecond)

	return l.Addr().String(),
		writePEMFile(t, "ca.pem", serverCertPEM),
		writePEMFile(t, "client-cert.pem", clientCertPEM),
		writePEMFile(t, "client-key.pem", clientKeyPEM)
}

func TestStompComponent_MutualTLS(t *testing.T) {
	addr, caFile, certFile, keyFile := startTestMTLSBroker(t)

	uri := "stomp://queue/orders?host=" + addr + "&ssl=true&sslCAFile=" + caFile +
		"&sslCertFile=" + certFile + "&sslKeyFile=" + keyFile

	camelCtx := gocamel.NewCamelContext()
	camelCtx.AddComponent("direct", gocamel.NewDirectComponent())
	camelCtx.AddComponent("stomp", NewStompComponent())

	done := make(chan string, 1)
	_, err := camelCtx.CreateRouteBuilder().
		From(uri).
		ProcessFunc(func(e *gocamel.Exchange) error {
			if b, ok := e.In.GetBody().([]byte); ok {
				done <- string(b)
			}
			return nil
		}).
		Build()
	require.NoError(t, err)

	_, err = camelCtx.CreateRouteBuilder().
		From("direct:pub").
		To(uri).
		Build()
	require.NoError(t, err)

	require.NoError(t, camelCtx.Start())
	t.Cleanup(func() { _ = camelCtx.Stop() })

	time.Sleep(100 * time.Millisecond)

	require.NoError(t, publish(t, camelCtx, "mTLS Order #1234", nil))

	select {
	case val := <-done:
		assert.Equal(t, "mTLS Order #1234", val)
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for STOMP message over mutual TLS")
	}
}

func TestStompComponent_TLSInvalidCAFile(t *testing.T) {
	component := NewStompComponent()

	// A traversal in the CA file option is rejected at endpoint creation.
	_, err := component.CreateEndpoint("stomp://queue/orders?ssl=true&sslCAFile=../../etc/passwd")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sslCAFile")

	// A missing CA file surfaces at endpoint creation too.
	_, err = component.CreateEndpoint("stomp://queue/orders?ssl=true&sslCAFile=/nonexistent/ca.pem")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sslCAFile")

	// A PEM file with no certificate is rejected.
	notACert := writePEMFile(t, "not-a-cert.pem", []byte("-----BEGIN CERTIFICATE-----\nnot a certificate\n-----END CERTIFICATE-----\n"))
	_, err = component.CreateEndpoint("stomp://queue/orders?ssl=true&sslCAFile=" + notACert)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sslCAFile")

	// Mutual TLS requires both the certificate and the key.
	_, err = component.CreateEndpoint("stomp://queue/orders?ssl=true&sslCertFile=cert.pem")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sslCertFile")
}

func TestStompEndpoint_URI_RedactsCredentials(t *testing.T) {
	component := NewStompComponent()
	ep, err := component.CreateEndpoint("stomp://queue/orders?host=localhost:61613&login=admin&passcode=super-secret-passcode")
	require.NoError(t, err)

	uri := ep.URI()
	assert.NotContains(t, uri, "super-secret-passcode")
	assert.Contains(t, uri, "passcode=xxxxx")
}

func TestStompEndpoint_SharedConnectionReferenceCounting(t *testing.T) {
	addr := startTestBroker(t)
	component := NewStompComponent()
	ep, err := component.CreateEndpoint("stomp://queue/shared?host=" + addr)
	require.NoError(t, err)
	se := ep.(*StompEndpoint)

	p1, err := ep.CreateProducer()
	require.NoError(t, err)
	p2, err := ep.CreateProducer()
	require.NoError(t, err)
	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, p1.Start(ctx))
	require.NoError(t, p2.Start(ctx))
	require.NoError(t, consumer.Start(ctx))
	// Producers share se.conn (connUsers == 2); the consumer uses its own
	// dedicated connection so stopping or reconnecting it never disrupts producers.
	assert.Equal(t, 2, se.connUsers)
	sc := consumer.(*StompConsumer)
	sc.mu.Lock()
	consConn := sc.conn
	sc.mu.Unlock()
	require.NotNil(t, consConn)
	assert.NotSame(t, se.conn, consConn)

	// Stopping p1 (even twice) and consumer leaves p2's shared connection active.
	require.NoError(t, p1.Stop())
	require.NoError(t, p1.Stop())
	assert.Equal(t, 1, se.connUsers)

	require.NoError(t, consumer.Stop())
	require.NoError(t, consumer.Stop())
	assert.Equal(t, 1, se.connUsers)
	assert.NotNil(t, se.conn)

	ex := gocamel.NewExchange(ctx)
	ex.In.SetBody("still works")
	require.NoError(t, p2.Send(ex))

	require.NoError(t, p2.Stop())
	assert.Equal(t, 0, se.connUsers)
	assert.Nil(t, se.conn)
}

func TestStompProducer_SendRejectsUnstartedProducerWithSharedConn(t *testing.T) {
	component := NewStompComponent()
	ep, err := component.CreateEndpoint("stomp://queue/shared")
	require.NoError(t, err)
	se := ep.(*StompEndpoint)
	// Simulate a shared connection held by a consumer.
	se.conn = &stomp.Conn{}

	p, err := ep.CreateProducer()
	require.NoError(t, err)

	ex := gocamel.NewExchange(context.Background())
	ex.In.SetBody("rejected")
	err = p.Send(ex)
	require.Error(t, err)
	assert.ErrorContains(t, err, "stomp producer is not started")
}

type sliceSubReader struct {
	msgs []*stomp.Message
	idx  int
}

func (s *sliceSubReader) Read() (*stomp.Message, error) {
	if s.idx >= len(s.msgs) {
		return nil, stomp.ErrCompletedSubscription
	}
	m := s.msgs[s.idx]
	s.idx++
	return m, nil
}

func TestStompConsumer_AckPermanentFailureDoesNotSpinForever(t *testing.T) {
	component := NewStompComponent()
	ep, err := component.CreateEndpoint("stomp://queue/acktest?ack=client-individual")
	require.NoError(t, err)

	received := make(chan string, 2)
	cons, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
		b, _ := ex.In.GetBody().([]byte)
		received <- string(b)
		return nil
	}))
	require.NoError(t, err)
	sc := cons.(*StompConsumer)

	// First message requires ack (Subscription != nil with AckClientIndividual, via Conn == &stomp.Conn{} and Header)
	// but has no valid subscription/ack headers on Conn so Conn.Ack returns ErrNotReceivedMessage immediately.
	msg1 := &stomp.Message{
		Destination:  "/queue/acktest",
		Body:         []byte("msg-1"),
		Conn:         &stomp.Conn{},
		Header:       frame.NewHeader(),
		Subscription: &stomp.Subscription{},
	}
	msg2 := &stomp.Message{
		Destination: "/queue/acktest",
		Body:        []byte("msg-2"),
		Header:      frame.NewHeader(),
	}
	sc.setSubscription(&sliceSubReader{msgs: []*stomp.Message{msg1, msg2}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sc.wg.Add(1)
	go func() {
		sc.run(ctx)
	}()

	select {
	case got := <-received:
		assert.Equal(t, "msg-1", got)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for msg-1")
	}

	select {
	case got := <-received:
		assert.Equal(t, "msg-2", got)
	case <-time.After(time.Second):
		t.Fatal("consumer spun on failed Ack instead of advancing to msg-2")
	}
	cancel()
	sc.wg.Wait()
}

// startCapturingBroker starts a minimal STOMP 1.2 TCP server that records
// received CONNECT and SEND frames for inspection in tests.
func startCapturingBroker(t *testing.T) (addr string, connectFrames <-chan *frame.Frame, sendFrames <-chan *frame.Frame) {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	connects := make(chan *frame.Frame, 8)
	sends := make(chan *frame.Frame, 32)

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
			go func() {
				r, w := frame.NewReader(conn), frame.NewWriter(conn)
				cf, err := r.Read()
				if err != nil || cf == nil {
					return
				}
				connects <- cf
				if w.Write(frame.New(frame.CONNECTED, frame.Version, "1.2")) != nil {
					return
				}
				for {
					f, err := r.Read()
					if err != nil || f == nil {
						return
					}
					switch f.Command {
					case frame.SEND:
						sends <- f
					case frame.DISCONNECT:
						_ = w.Write(frame.New(frame.RECEIPT, frame.ReceiptId, f.Header.Get(frame.Receipt)))
						return
					}
				}
			}()
		}
	}()

	return l.Addr().String(), connects, sends
}

func TestStompProducer_BlocksProtocolHeadersAndNullBytes(t *testing.T) {
	addr, _, sendFrames := startCapturingBroker(t)
	component := NewStompComponent()

	// Reject NUL bytes in URI destination / host.
	_, err := component.CreateEndpoint("stomp://queue/bad%00dest?host=" + addr)
	require.Error(t, err)

	ep, err := component.CreateEndpoint("stomp://queue/orders?host=" + addr + "&allowHeaderOverride=true&forwardHeaders=receipt,transaction,x-trace,camelFileName")
	require.NoError(t, err)

	producer, err := ep.CreateProducer()
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, producer.Start(ctx))
	t.Cleanup(func() { _ = producer.Stop() })

	epAll, err := component.CreateEndpoint("stomp://queue/orders?host=" + addr)
	require.NoError(t, err)
	pAll, err := epAll.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, pAll.Start(ctx))
	t.Cleanup(func() { _ = pAll.Stop() })

	// 1. NUL bytes in destination, contentType, forwarded header key, and header value must be rejected.
	badDest := gocamel.NewExchange(ctx)
	badDest.In.SetBody("hello")
	badDest.In.SetHeader(CamelStompDestination, "/queue/evil\x00/drop")
	require.Error(t, producer.Send(badDest))

	badCT := gocamel.NewExchange(ctx)
	badCT.In.SetBody("hello")
	badCT.In.SetHeader(CamelStompContentType, "text/plain\x00evil")
	require.Error(t, producer.Send(badCT))

	badHdrKey := gocamel.NewExchange(ctx)
	badHdrKey.In.SetBody("hello")
	badHdrKey.In.SetHeader("x-bad\x00key", "val")
	require.Error(t, pAll.Send(badHdrKey))

	badHdrVal := gocamel.NewExchange(ctx)
	badHdrVal.In.SetBody("hello")
	badHdrVal.In.SetHeader("x-trace", "val\x00evil")
	require.Error(t, producer.Send(badHdrVal))

	// 2. All STOMP protocol headers and case-insensitive Camel* headers must be
	// blocked even when listed in forwardHeaders.

	for _, p := range []gocamel.Producer{producer, pAll} {
		ex := gocamel.NewExchange(ctx)
		ex.In.SetBody("hello")
		blockedHeaders := map[string]string{
			"receipt":        "sub-0",
			"Receipt":        "sub-1",
			"transaction":    "tx-1",
			"destination":    "/queue/hijack",
			"content-length": "999",
			"content-type":   "application/evil",
			"id":             "sub-99",
			"ack":            "client",
			"subscription":   "sub-99",
			"message-id":     "msg-99",
			"receipt-id":     "rcpt-99",
			"host":           "evil-vhost",
			"accept-version": "1.0",
			"version":        "1.0",
			"login":          "attacker",
			"passcode":       "attacker-pw",
			"heart-beat":     "0,0",
			"session":        "sess-1",
			"server":         "evil",
			"message":        "boom",
			"camelFileName":  "../../etc/passwd",
			"CamelCustom":    "internal",
		}
		for k, v := range blockedHeaders {
			ex.In.SetHeader(k, v)
		}
		ex.In.SetHeader("x-trace", "trace-123")

		require.NoError(t, p.Send(ex))

		var f *frame.Frame
		select {
		case f = <-sendFrames:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for SEND frame")
		}

		assert.Equal(t, "/queue/orders", f.Header.Get(frame.Destination))
		assert.Equal(t, "5", f.Header.Get(frame.ContentLength))
		assert.Empty(t, f.Header.Get(frame.ContentType))
		assert.Equal(t, "trace-123", f.Header.Get("x-trace"))

		for _, blocked := range []string{
			"receipt", "Receipt", "transaction", "id", "ack", "subscription",
			"message-id", "receipt-id", "host", "accept-version", "version",
			"login", "passcode", "heart-beat", "session", "server", "message",
			"camelFileName", "CamelCustom",
		} {
			_, present := f.Header.Contains(blocked)
			assert.False(t, present, "header %q must not be forwarded in STOMP SEND frame", blocked)
		}
		assert.Equal(t, []string{"/queue/orders"}, f.Header.GetAll(frame.Destination))
		assert.Equal(t, []string{"5"}, f.Header.GetAll(frame.ContentLength))
	}
}

// startAckTrackingBroker starts a minimal STOMP 1.2 broker that delivers one
// queued message upon SUBSCRIBE (ack: ack-1, message-id: msg-1) and reports
// received ACK frames and connection closes.
func startAckTrackingBroker(t *testing.T, body string) (addr string, acks <-chan *frame.Frame, connClosed <-chan struct{}) {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	ackCh := make(chan *frame.Frame, 8)
	closedCh := make(chan struct{}, 8)

	go func() {
		delivered := false
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
			go func(c net.Conn) {
				defer func() {
					closedCh <- struct{}{}
				}()
				r, w := frame.NewReader(c), frame.NewWriter(c)
				if _, err := r.Read(); err != nil { // CONNECT
					return
				}
				if w.Write(frame.New(frame.CONNECTED, frame.Version, "1.2")) != nil {
					return
				}
				for {
					f, err := r.Read()
					if err != nil || f == nil {
						return
					}
					switch f.Command {
					case frame.SUBSCRIBE:
						if !delivered {
							delivered = true
							subID := f.Header.Get(frame.Id)
							dest := f.Header.Get(frame.Destination)
							msg := frame.New(frame.MESSAGE,
								frame.Destination, dest,
								frame.MessageId, "msg-1",
								frame.Ack, "ack-1",
								frame.Subscription, subID)
							msg.Body = []byte(body)
							if w.Write(msg) != nil {
								return
							}
						}
					case frame.ACK:
						ackCh <- f
					case frame.DISCONNECT:
						_ = w.Write(frame.New(frame.RECEIPT, frame.ReceiptId, f.Header.Get(frame.Receipt)))
						return
					}
				}
			}(conn)
		}
	}()

	return l.Addr().String(), ackCh, closedCh
}

func TestStompConsumer_StopDoesNotStallSharedProducerAndAcksInFlight(t *testing.T) {
	t.Run("SoloConsumerAcksInFlightBeforeClosingConn", func(t *testing.T) {
		addr, acks, connClosed := startAckTrackingBroker(t, "inflight-msg")
		component := NewStompComponent()

		ep, err := component.CreateEndpoint("stomp://queue/inflight?host=" + addr + "&ack=client-individual")
		require.NoError(t, err)

		inHandler := make(chan context.Context, 1)
		release := make(chan struct{})
		consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
			inHandler <- e.Context
			<-release
			return nil
		}))
		require.NoError(t, err)
		require.NoError(t, consumer.Start(context.Background()))

		var exCtx context.Context
		select {
		case exCtx = <-inHandler:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for inflight message")
		}

		stopErr := make(chan error, 1)
		go func() {
			stopErr <- consumer.Stop()
		}()

		// Wait deterministically until Stop has cancelled the consumer context.
		select {
		case <-exCtx.Done():
		case <-time.After(3 * time.Second):
			t.Fatal("consumer.Stop did not cancel exchange context")
		}

		close(release)
		select {
		case err := <-stopErr:
			require.NoError(t, err)
		case <-time.After(3 * time.Second):
			t.Fatal("consumer.Stop timed out")
		}

		select {
		case <-connClosed:
		case <-time.After(3 * time.Second):
			t.Fatal("broker did not observe consumer disconnect")
		}

		select {
		case ackFrame := <-acks:
			assert.Equal(t, "ack-1", ackFrame.Header.Get(frame.Id))
		default:
			t.Fatal("inflight message was not ACKed before Stop closed connection")
		}
	})

	t.Run("StopDoesNotStallSharedProducer", func(t *testing.T) {
		addr := startTestBroker(t)
		component := NewStompComponent()

		ep, err := component.CreateEndpoint("stomp://queue/sharedstop?host=" + addr)
		require.NoError(t, err)

		producer, err := ep.CreateProducer()
		require.NoError(t, err)
		require.NoError(t, producer.Start(context.Background()))
		t.Cleanup(func() { _ = producer.Stop() })

		gotOne := make(chan struct{}, 1)
		consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
			select {
			case gotOne <- struct{}{}:
			default:
			}
			return nil
		}))
		require.NoError(t, err)
		require.NoError(t, consumer.Start(context.Background()))

		ex := gocamel.NewExchange(context.Background())
		ex.In.SetBody("warmup")
		require.NoError(t, producer.Send(ex))
		select {
		case <-gotOne:
		case <-time.After(3 * time.Second):
			t.Fatal("warmup message not received")
		}

		require.NoError(t, consumer.Stop())

		// Sending > 16+20+readCh messages totaling more than the TCP socket buffer
		// after consumer.Stop() must not stall or time out the shared producer.
		sendDone := make(chan error, 1)
		go func() {
			for i := range 200 {
				msg := gocamel.NewExchange(context.Background())
				buf := make([]byte, 8192)
				copy(buf, fmt.Sprintf("after-stop-%d", i))
				msg.In.SetBody(buf)
				if err := producer.Send(msg); err != nil {
					sendDone <- err
					return
				}
			}
			sendDone <- nil
		}()

		select {
		case err := <-sendDone:
			require.NoError(t, err)
		case <-time.After(3 * time.Second):
			t.Fatal("shared StompProducer stalled after StompConsumer.Stop()")
		}
	})
}

func TestStomp_UserinfoLoginPasscodeFallback(t *testing.T) {
	addr, connectFrames, _ := startCapturingBroker(t)
	component := NewStompComponent()

	ep, err := component.CreateEndpoint("stomp://alice:s3cr3t@queue/auth?host=" + addr)
	require.NoError(t, err)
	se := ep.(*StompEndpoint)
	assert.Equal(t, "alice", se.login)
	assert.Equal(t, "s3cr3t", se.passcode)
	assert.NotContains(t, ep.URI(), "s3cr3t")
	assert.Contains(t, ep.URI(), "alice:xxxxx@")

	producer, err := ep.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, producer.Start(context.Background()))
	t.Cleanup(func() { _ = producer.Stop() })

	select {
	case cf := <-connectFrames:
		assert.Equal(t, "alice", cf.Header.Get(frame.Login))
		assert.Equal(t, "s3cr3t", cf.Header.Get(frame.Passcode))
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for CONNECT frame")
	}

	// Explicit login/passcode override userinfo credentials.
	ep2, err := component.CreateEndpoint("stomp://alice:s3cr3t@queue/auth?host=" + addr + "&login=bob&passcode=pw2")
	require.NoError(t, err)
	se2 := ep2.(*StompEndpoint)
	assert.Equal(t, "bob", se2.login)
	assert.Equal(t, "pw2", se2.passcode)
}

func TestStompConsumer_ConcurrentStartStopNoRace(t *testing.T) {
	addr := startTestBroker(t)
	component := NewStompComponent()

	ep, err := component.CreateEndpoint("stomp://queue/concurrent?host=" + addr)
	require.NoError(t, err)

	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = consumer.Stop() })

	for range 8 {
		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		wg.Add(4)
		go func() {
			defer wg.Done()
			_ = consumer.Start(ctx)
		}()
		go func() {
			defer wg.Done()
			_ = consumer.Start(ctx)
		}()
		go func() {
			defer wg.Done()
			cancel()
		}()
		go func() {
			defer wg.Done()
			_ = consumer.Stop()
		}()
		wg.Wait()
		require.NoError(t, consumer.Stop())
	}

	liveCtx, liveCancel := context.WithCancel(context.Background())
	defer liveCancel()
	require.NoError(t, consumer.Start(liveCtx))
	require.NoError(t, consumer.Stop())
}
