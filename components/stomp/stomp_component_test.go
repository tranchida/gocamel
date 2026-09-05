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
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-stomp/stomp/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gocamel "gitlab.com/tranchida/gocamel"
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
