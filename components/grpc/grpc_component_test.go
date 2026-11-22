package grpc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	grpcstd "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	gocamel "github.com/tranchida/gocamel"
)

func TestGRPCComponent_CreateEndpoint(t *testing.T) {
	component := NewGRPCComponent()

	uri := "grpc://localhost:9090/helloworld.Greeter/SayHello?insecure=true"
	endpoint, err := component.CreateEndpoint(uri)
	require.NoError(t, err)

	ge, ok := endpoint.(*GRPCEndpoint)
	require.True(t, ok)
	assert.Equal(t, "localhost:9090", ge.target)
	assert.Equal(t, "helloworld.Greeter", ge.service)
	assert.Equal(t, "SayHello", ge.method)
	assert.Equal(t, "/helloworld.Greeter/SayHello", ge.FullMethod())
	assert.True(t, ge.insecure)
}

func TestGRPCComponent_QueryServiceMethod(t *testing.T) {
	component := NewGRPCComponent()

	uri := "grpc://api.internal:50051?service=OrderService&method=CreateOrder"
	endpoint, err := component.CreateEndpoint(uri)
	require.NoError(t, err)

	ge, ok := endpoint.(*GRPCEndpoint)
	require.True(t, ok)
	assert.Equal(t, "api.internal:50051", ge.target)
	assert.Equal(t, "OrderService", ge.service)
	assert.Equal(t, "CreateOrder", ge.method)
	assert.Equal(t, "/OrderService/CreateOrder", ge.FullMethod())
}

func TestGRPCComponent_ClientServerIntegration(t *testing.T) {
	component := NewGRPCComponent()

	// 1. Setup server endpoint on 127.0.0.1:0
	serverEndpoint, err := component.CreateEndpoint("grpc://127.0.0.1:0/test.EchoService/Echo")
	require.NoError(t, err)

	consumer, err := serverEndpoint.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		req := string(e.In.GetBody().([]byte))
		e.Out.SetBody("Echo: " + req)
		return nil
	}))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = consumer.Start(ctx)
	require.NoError(t, err)
	defer consumer.Stop()

	serverConsumer := consumer.(*GRPCConsumer)
	serverAddr := serverConsumer.Address()
	require.NotEmpty(t, serverAddr)

	// Small pause to ensure listener is active
	time.Sleep(50 * time.Millisecond)

	// 2. Setup client producer targeting serverAddr
	clientEndpoint, err := component.CreateEndpoint("grpc://" + serverAddr + "/test.EchoService/Echo?insecure=true")
	require.NoError(t, err)

	producer, err := clientEndpoint.CreateProducer()
	require.NoError(t, err)

	err = producer.Start(ctx)
	require.NoError(t, err)
	defer producer.Stop()

	// 3. Send exchange
	exchange := gocamel.NewExchange(ctx)
	exchange.In.SetBody([]byte("Hello gRPC"))
	exchange.In.SetHeader("custom-header", "gocamel-grpc-test")

	err = producer.Send(exchange)
	require.NoError(t, err)

	respBody := exchange.Out.GetBody()
	require.NotNil(t, respBody)
	assert.Equal(t, []byte("Echo: Hello gRPC"), respBody.([]byte))

	statusCode, ok := exchange.GetOut().GetHeaderAsInt(CamelGrpcStatusCode)
	assert.True(t, ok)
	assert.Equal(t, 0, statusCode) // codes.OK == 0
}

// startMetadataEchoServer hosts a gRPC server returning the value of the
// requested incoming metadata key, so tests can observe which exchange
// headers the producer forwards.
func startMetadataEchoServer(t *testing.T, component *GRPCComponent, ctx context.Context, echoKey string) string {
	t.Helper()

	serverEndpoint, err := component.CreateEndpoint("grpc://127.0.0.1:0/test.EchoService/Echo")
	require.NoError(t, err)

	consumer, err := serverEndpoint.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		value, _ := e.In.GetHeaderAsString(echoKey)
		e.Out.SetBody(value)
		return nil
	}))
	require.NoError(t, err)
	require.NoError(t, consumer.Start(ctx))
	t.Cleanup(func() { _ = consumer.Stop() })

	time.Sleep(50 * time.Millisecond)
	return consumer.(*GRPCConsumer).Address()
}

func sendAndGetBody(t *testing.T, producer gocamel.Producer, ctx context.Context, headers map[string]string) string {
	t.Helper()
	exchange := gocamel.NewExchange(ctx)
	exchange.In.SetBody([]byte("ping"))
	for name, value := range headers {
		exchange.In.SetHeader(name, value)
	}
	require.NoError(t, producer.Send(exchange))
	return string(exchange.Out.GetBody().([]byte))
}

func TestGRPCProducer_SensitiveHeadersNotForwarded(t *testing.T) {
	component := NewGRPCComponent()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, sensitive := range []string{"authorization", "cookie", "proxy-authorization", "set-cookie", "www-authenticate"} {
		addr := startMetadataEchoServer(t, component, ctx, sensitive)
		producer := newStartedProducer(t, component, ctx, "grpc://"+addr+"/test.EchoService/Echo?insecure=true")
		assert.Empty(t, sendAndGetBody(t, producer, ctx, map[string]string{sensitive: "secret-token"}),
			"sensitive header %q must not be forwarded by default", sensitive)
	}

	// A business header is forwarded by default.
	addr := startMetadataEchoServer(t, component, ctx, "tenant-id")
	producer := newStartedProducer(t, component, ctx, "grpc://"+addr+"/test.EchoService/Echo?insecure=true")
	assert.Equal(t, "acme", sendAndGetBody(t, producer, ctx, map[string]string{"tenant-id": "acme"}))
}

func TestGRPCProducer_ForwardHeadersAllowlist(t *testing.T) {
	component := NewGRPCComponent()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr := startMetadataEchoServer(t, component, ctx, "tenant-id")
	producer := newStartedProducer(t, component, ctx,
		"grpc://"+addr+"/test.EchoService/Echo?insecure=true&forwardHeaders=tenant-id")

	assert.Equal(t, "acme", sendAndGetBody(t, producer, ctx, map[string]string{
		"tenant-id":     "acme",
		"Authorization": "Bearer secret-token",
	}))
}

func TestGRPCProducer_ForwardHeadersAllowlistDropsOthers(t *testing.T) {
	component := NewGRPCComponent()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr := startMetadataEchoServer(t, component, ctx, "x-custom-header")
	producer := newStartedProducer(t, component, ctx,
		"grpc://"+addr+"/test.EchoService/Echo?insecure=true&forwardHeaders=tenant-id")

	assert.Empty(t, sendAndGetBody(t, producer, ctx, map[string]string{
		"tenant-id":       "acme",
		"X-Custom-Header": "custom-value",
	}), "headers outside the allowlist must be dropped")
}

func TestGRPCComponent_SecureEndpointBuilds(t *testing.T) {
	component := NewGRPCComponent()

	// Building a secured endpoint must not fail: credentials are resolved at
	// dial time (lazy), so a system-pool TLS config is assembled here.
	endpoint, err := component.CreateEndpoint("grpc://api.internal:50051/Svc/Mth?insecure=false")
	require.NoError(t, err)
	assert.False(t, endpoint.(*GRPCEndpoint).insecure)

	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, producer.Start(context.Background()))
	t.Cleanup(func() { _ = producer.Stop() })
}

func TestGRPCComponent_CaCertMissingFileFailsFast(t *testing.T) {
	component := NewGRPCComponent()

	_, err := component.CreateEndpoint(
		"grpc://api.internal:50051/Svc/Mth?insecure=false&caCert=/does/not/exist/ca.pem")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "caCert")
}

func TestGRPCComponent_CaCertRejectsInsecureEndpoint(t *testing.T) {
	component := NewGRPCComponent()

	_, err := component.CreateEndpoint(
		"grpc://api.internal:50051/Svc/Mth?insecure=true&caCert=/tmp/ca.pem")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insecure=false")
}

// writeSelfSignedTLSCert generates a self-signed certificate valid for
// 127.0.0.1, writes the PEM cert/key files into t.TempDir() and returns the
// cert path together with the ready-to-use tls.Certificate.
func writeSelfSignedTLSCert(t *testing.T) (string, tls.Certificate) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "gocamel-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)

	dir := t.TempDir()
	certPath := filepath.Join(dir, "ca.pem")
	require.NoError(t, os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600))
	keyPath := filepath.Join(dir, "key.pem")
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0600))

	tlsCert, err := tls.LoadX509KeyPair(certPath, keyPath)
	require.NoError(t, err)
	return certPath, tlsCert
}

func TestGRPCProducer_TLSEchoWithCaCert(t *testing.T) {
	caCertPath, tlsCert := writeSelfSignedTLSCert(t)

	// TLS echo server using the standard gRPC server directly.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server := grpcstd.NewServer(
		grpcstd.Creds(credentials.NewServerTLSFromCert(&tlsCert)),
		grpcstd.ForceServerCodec(RawBytesCodec{}),
		grpcstd.UnknownServiceHandler(func(_ any, stream grpcstd.ServerStream) error {
			var reqBytes []byte
			if err := stream.RecvMsg(&reqBytes); err != nil {
				return err
			}
			md, _ := metadata.FromIncomingContext(stream.Context())
			resp := append([]byte("Echo: "), reqBytes...)
			if vals := md.Get("tenant-id"); len(vals) > 0 {
				resp = append(resp, []byte(" ["+vals[0]+"]")...)
			}
			return stream.SendMsg(&resp)
		}),
	)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = server.Serve(lis)
	}()
	t.Cleanup(func() {
		server.GracefulStop()
		_ = lis.Close()
		wg.Wait()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The producer verifies the self-signed server certificate against the
	// custom CA loaded from caCert.
	producer := newStartedProducer(t, NewGRPCComponent(), ctx,
		"grpc://"+lis.Addr().String()+"/test.EchoService/Echo?insecure=false&caCert="+caCertPath)

	exchange := gocamel.NewExchange(ctx)
	exchange.In.SetBody([]byte("Hello TLS"))
	exchange.In.SetHeader("tenant-id", "acme")
	require.NoError(t, producer.Send(exchange))
	assert.Equal(t, "Echo: Hello TLS [acme]", string(exchange.Out.GetBody().([]byte)))

	// The same cert is rejected when the endpoint does not trust it (system
	// pool), proving the caCert pool is what made verification succeed.
	brokenProducer := newStartedProducer(t, NewGRPCComponent(), ctx,
		"grpc://"+lis.Addr().String()+"/test.EchoService/Echo?insecure=false")
	brokenExchange := gocamel.NewExchange(ctx)
	brokenExchange.In.SetBody([]byte("ping"))
	assert.Error(t, brokenProducer.Send(brokenExchange))
}

func TestGRPCComponent_AllowHeaderOverrideParsing(t *testing.T) {
	component := NewGRPCComponent()

	endpoint, err := component.CreateEndpoint("grpc://localhost:9090/Svc/Mth")
	require.NoError(t, err)
	assert.False(t, endpoint.(*GRPCEndpoint).allowHeaderOverride, "allowHeaderOverride must default to false")

	endpoint, err = component.CreateEndpoint("grpc://localhost:9090/Svc/Mth?allowHeaderOverride=true")
	require.NoError(t, err)
	assert.True(t, endpoint.(*GRPCEndpoint).allowHeaderOverride)
}

// startEchoServer hosts a gRPC server returning the invoked full method name.
func startEchoServer(t *testing.T, component *GRPCComponent, ctx context.Context) (string, *GRPCConsumer) {
	t.Helper()

	serverEndpoint, err := component.CreateEndpoint("grpc://127.0.0.1:0/test.EchoService/Echo")
	require.NoError(t, err)

	consumer, err := serverEndpoint.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		method, _ := e.GetHeaderAsString(CamelGrpcMethod)
		e.Out.SetBody(method)
		return nil
	}))
	require.NoError(t, err)
	require.NoError(t, consumer.Start(ctx))
	t.Cleanup(func() { _ = consumer.Stop() })

	time.Sleep(50 * time.Millisecond)
	serverConsumer := consumer.(*GRPCConsumer)
	require.NotEmpty(t, serverConsumer.Address())
	return serverConsumer.Address(), serverConsumer
}

func newStartedProducer(t *testing.T, component *GRPCComponent, ctx context.Context, uri string) gocamel.Producer {
	t.Helper()
	endpoint, err := component.CreateEndpoint(uri)
	require.NoError(t, err)
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, producer.Start(ctx))
	t.Cleanup(func() { _ = producer.Stop() })
	return producer
}

func sendAndGetMethod(t *testing.T, producer gocamel.Producer, ctx context.Context, headerMethod string) (string, error) {
	t.Helper()
	exchange := gocamel.NewExchange(ctx)
	exchange.In.SetBody([]byte("ping"))
	if headerMethod != "" {
		exchange.In.SetHeader(CamelGrpcMethod, headerMethod)
	}
	err := producer.Send(exchange)
	if err != nil {
		return "", err
	}
	return string(exchange.Out.GetBody().([]byte)), nil
}

func TestGRPCProducer_HeaderOverrideIgnoredByDefault(t *testing.T) {
	component := NewGRPCComponent()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, _ := startEchoServer(t, component, ctx)
	producer := newStartedProducer(t, component, ctx, "grpc://"+addr+"/test.EchoService/Echo?insecure=true")

	// Without allowHeaderOverride, CamelGrpcMethod must be ignored.
	method, err := sendAndGetMethod(t, producer, ctx, "/test.OtherService/Other")
	require.NoError(t, err)
	assert.Equal(t, "/test.EchoService/Echo", method)
}

func TestGRPCProducer_HeaderOverrideApplied(t *testing.T) {
	component := NewGRPCComponent()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, _ := startEchoServer(t, component, ctx)
	producer := newStartedProducer(t, component, ctx,
		"grpc://"+addr+"/test.EchoService/Echo?insecure=true&allowHeaderOverride=true")

	// With allowHeaderOverride=true, CamelGrpcMethod replaces the configured method.
	method, err := sendAndGetMethod(t, producer, ctx, "/helloworld.Greeter/SayHello")
	require.NoError(t, err)
	assert.Equal(t, "/helloworld.Greeter/SayHello", method)

	// The "Service/Method" notation (without leading slash) is also accepted.
	method, err = sendAndGetMethod(t, producer, ctx, "helloworld.Greeter/SayHello")
	require.NoError(t, err)
	assert.Equal(t, "/helloworld.Greeter/SayHello", method)
}

func TestGRPCProducer_HeaderOverrideRejectsMalformedMethod(t *testing.T) {
	component := NewGRPCComponent()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, _ := startEchoServer(t, component, ctx)
	producer := newStartedProducer(t, component, ctx,
		"grpc://"+addr+"/test.EchoService/Echo?insecure=true&allowHeaderOverride=true")

	for _, bad := range []string{"no-slash", "9bad/Method", "/Service/", "Service/Method/", "Service//Method", "//Service/Method"} {
		_, err := sendAndGetMethod(t, producer, ctx, bad)
		require.Error(t, err, "malformed method %q must be rejected", bad)
		assert.Contains(t, err.Error(), "malformed method")
	}
}

func TestGRPCConsumer_StopReleasesEndpointRegistration(t *testing.T) {
	component := NewGRPCComponent()
	endpoint, err := component.CreateEndpoint("grpc://127.0.0.1:50059/Service/Method")
	require.NoError(t, err)

	consumer, err := endpoint.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	require.NoError(t, err)

	// Second consumer on same endpoint should fail
	_, err = endpoint.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	assert.Error(t, err)

	assert.NoError(t, consumer.Stop())

	// Re-register after stop should succeed
	_, err = endpoint.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	assert.NoError(t, err)
}

func TestGRPCProducer_SharedEndpointReferenceCounting(t *testing.T) {
	component := NewGRPCComponent()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, _ := startEchoServer(t, component, ctx)
	endpoint, err := component.CreateEndpoint("grpc://" + addr + "/test.EchoService/Echo?insecure=true")
	require.NoError(t, err)
	ge := endpoint.(*GRPCEndpoint)

	p1, err := endpoint.CreateProducer()
	require.NoError(t, err)
	p2, err := endpoint.CreateProducer()
	require.NoError(t, err)

	require.NoError(t, p1.Start(ctx))
	require.NoError(t, p2.Start(ctx))
	assert.Equal(t, 2, ge.connUsers)

	// Stopping p1 (even twice) must leave the connection open for p2.
	require.NoError(t, p1.Stop())
	require.NoError(t, p1.Stop())
	assert.Equal(t, 1, ge.connUsers)
	assert.NotNil(t, ge.clientConn)

	method, err := sendAndGetMethod(t, p2, ctx, "")
	require.NoError(t, err)
	assert.Equal(t, "/test.EchoService/Echo", method)

	// Stopping p2 releases the last reference and closes the connection.
	require.NoError(t, p2.Stop())
	assert.Equal(t, 0, ge.connUsers)
	assert.Nil(t, ge.clientConn)
}

func TestGRPCConsumer_ContextCancelAndConcurrentAddressStop(t *testing.T) {
	component := NewGRPCComponent()
	endpoint, err := component.CreateEndpoint("grpc://127.0.0.1:0/test.EchoService/Echo")
	require.NoError(t, err)

	consumer, err := endpoint.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		e.Out.SetBody("ok")
		return nil
	}))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, consumer.Start(ctx))
	gc := consumer.(*GRPCConsumer)

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for range 50 {
			assert.NotEmpty(t, gc.Address())
		}
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

	require.Eventually(t, func() bool {
		ge := endpoint.(*GRPCEndpoint)
		ge.mu.Lock()
		defer ge.mu.Unlock()
		return ge.consumer == nil
	}, 2*time.Second, 10*time.Millisecond)
}

func TestGRPCConsumer_StopTimeoutForcesShutdown(t *testing.T) {
	component := NewGRPCComponent()
	serverEndpoint, err := component.CreateEndpoint("grpc://127.0.0.1:0/test.EchoService/Echo?timeout=100ms")
	require.NoError(t, err)

	inHandler := make(chan struct{})
	releaseHandler := make(chan struct{})
	consumer, err := serverEndpoint.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		close(inHandler)
		select {
		case <-e.Context.Done():
			return e.Context.Err()
		case <-releaseHandler:
			return nil
		}
	}))
	require.NoError(t, err)

	bufLis := bufconn.Listen(1024 * 1024)
	consumer.(*GRPCConsumer).listener = bufLis

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, consumer.Start(ctx))
	defer close(releaseHandler)

	conn, err := grpcstd.NewClient(
		"passthrough:///bufnet",
		grpcstd.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return bufLis.DialContext(ctx)
		}),
		grpcstd.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	defer conn.Close()

	callDone := make(chan error, 1)
	go func() {
		req := []byte("hang")
		var resp []byte
		callDone <- conn.Invoke(ctx, "/test.EchoService/Echo", &req, &resp, grpcstd.ForceCodec(RawBytesCodec{}))
	}()

	select {
	case <-inHandler:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handler to be entered")
	}

	stopStart := time.Now()
	require.NoError(t, consumer.Stop())
	assert.Less(t, time.Since(stopStart), 2*time.Second)

	select {
	case err := <-callDone:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("client call did not abort after forced server stop")
	}
}

func writeSelfSignedTLSKeyPair(t *testing.T) (string, string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "gocamel-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)

	dir := t.TempDir()
	certPath := filepath.Join(dir, "server.pem")
	require.NoError(t, os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	keyPath := filepath.Join(dir, "server-key.pem")
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600))

	return certPath, keyPath
}

func TestGRPCConsumer_RejectsInsecureFalseWithoutServerTLS(t *testing.T) {
	component := NewGRPCComponent()

	// Client endpoints may still use insecure=false without certFile/keyFile,
	// so CreateEndpoint succeeds while Consumer.Start rejects missing server certs.
	endpoint, err := component.CreateEndpoint("grpc://127.0.0.1:0/test.EchoService/Echo?insecure=false")
	require.NoError(t, err)

	consumer, err := endpoint.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		return nil
	}))
	require.NoError(t, err)

	err = consumer.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "certFile")

	// Partial or mismatched certFile/keyFile options fail at CreateEndpoint.
	certPath, keyPath := writeSelfSignedTLSKeyPair(t)
	_, err = component.CreateEndpoint("grpc://127.0.0.1:0/Svc/Mth?insecure=false&certFile=" + certPath)
	require.Error(t, err)
	_, err = component.CreateEndpoint("grpc://127.0.0.1:0/Svc/Mth?insecure=true&certFile=" + certPath + "&keyFile=" + keyPath)
	require.Error(t, err)

	// Server TLS works end-to-end when certFile and keyFile are provided.
	tlsEndpoint, err := component.CreateEndpoint(
		"grpc://127.0.0.1:0/test.EchoService/Echo?insecure=false&certFile=" + certPath + "&keyFile=" + keyPath)
	require.NoError(t, err)
	tlsConsumer, err := tlsEndpoint.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		req, _ := e.In.GetBody().([]byte)
		e.In.SetBody("TLS Echo: " + string(req))
		return nil
	}))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, tlsConsumer.Start(ctx))
	t.Cleanup(func() { _ = tlsConsumer.Stop() })

	addr := tlsConsumer.(*GRPCConsumer).Address()
	producer := newStartedProducer(t, component, ctx,
		"grpc://"+addr+"/test.EchoService/Echo?insecure=false&caCert="+certPath)

	ex := gocamel.NewExchange(ctx)
	ex.In.SetBody([]byte("secret"))
	require.NoError(t, producer.Send(ex))
	assert.Equal(t, "TLS Echo: secret", string(ex.Out.GetBody().([]byte)))
}

func TestGRPCConsumer_ErrorWithZeroStatusCodeStillErrors(t *testing.T) {
	component := NewGRPCComponent()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Case 1: Upstream set CamelGrpcStatusCode=0 (codes.OK) & "OK" before downstream failed.
	ep1, err := component.CreateEndpoint("grpc://127.0.0.1:0/test.EchoService/Echo")
	require.NoError(t, err)
	c1, err := ep1.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		e.In.SetHeader(CamelGrpcStatusCode, int(codes.OK))
		e.In.SetHeader(CamelGrpcStatusDescription, "OK")
		return errors.New("sql error: password=topsecret")
	}))
	require.NoError(t, err)
	require.NoError(t, c1.Start(ctx))
	t.Cleanup(func() { _ = c1.Stop() })

	p1 := newStartedProducer(t, component, ctx,
		"grpc://"+c1.(*GRPCConsumer).Address()+"/test.EchoService/Echo?insecure=true")
	ex1 := gocamel.NewExchange(ctx)
	ex1.In.SetBody([]byte("hi"))
	err = p1.Send(ex1)
	require.Error(t, err, "route error with CamelGrpcStatusCode=0 must still return a gRPC error")
	st, ok := status.FromError(errors.Unwrap(err))
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Equal(t, "internal server error", st.Message())
	assert.NotContains(t, st.Message(), "topsecret")
	assert.NotContains(t, st.Message(), "sql error")
	assert.NotEqual(t, "OK", st.Message())

	// Case 2: Route sets explicit non-zero CamelGrpcStatusCode and CamelGrpcStatusDescription.
	ep2, err := component.CreateEndpoint("grpc://127.0.0.1:0/test.EchoService/Echo")
	require.NoError(t, err)
	c2, err := ep2.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		e.Out.SetHeader(CamelGrpcStatusCode, int(codes.InvalidArgument))
		e.Out.SetHeader(CamelGrpcStatusDescription, "missing field id")
		return errors.New("internal stack trace")
	}))
	require.NoError(t, err)
	require.NoError(t, c2.Start(ctx))
	t.Cleanup(func() { _ = c2.Stop() })

	p2 := newStartedProducer(t, component, ctx,
		"grpc://"+c2.(*GRPCConsumer).Address()+"/test.EchoService/Echo?insecure=true")
	ex2 := gocamel.NewExchange(ctx)
	ex2.In.SetBody([]byte("hi"))
	err = p2.Send(ex2)
	require.Error(t, err)
	st2, ok := status.FromError(errors.Unwrap(err))
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st2.Code())
	assert.Equal(t, "missing field id", st2.Message())
}

func TestGRPCConsumer_ReadsInBodyViaGetResponse(t *testing.T) {
	component := NewGRPCComponent()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ep, err := component.CreateEndpoint("grpc://127.0.0.1:0/test.EchoService/Echo")
	require.NoError(t, err)

	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		// Standard GoCamel DSL processors (e.g. SetBody, Transform) update In.Body
		// while leaving Out.Body nil.
		req, _ := e.In.GetBody().([]byte)
		e.In.SetBody("InReply: " + string(req))
		e.Out.SetHeader("x-ignored-only-header", "1")
		return nil
	}))
	require.NoError(t, err)
	require.NoError(t, consumer.Start(ctx))
	t.Cleanup(func() { _ = consumer.Stop() })

	producer := newStartedProducer(t, component, ctx,
		"grpc://"+consumer.(*GRPCConsumer).Address()+"/test.EchoService/Echo?insecure=true")

	ex := gocamel.NewExchange(ctx)
	ex.In.SetBody([]byte("world"))
	require.NoError(t, producer.Send(ex))
	assert.Equal(t, "InReply: world", string(ex.Out.GetBody().([]byte)))
}

func TestGRPCConsumer_ConcurrentStopStartNoWaitGroupRace(t *testing.T) {
	component := NewGRPCComponent()
	ep, err := component.CreateEndpoint("grpc://127.0.0.1:0/test.EchoService/Echo")
	require.NoError(t, err)

	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		e.In.SetBody("ok")
		return nil
	}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = consumer.Stop() })

	for range 15 {
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

	// Restarting after Stop must work cleanly even after previous contexts cancelled.
	liveCtx, liveCancel := context.WithCancel(context.Background())
	defer liveCancel()
	require.NoError(t, consumer.Start(liveCtx))
	producer := newStartedProducer(t, component, liveCtx,
		"grpc://"+consumer.(*GRPCConsumer).Address()+"/test.EchoService/Echo?insecure=true")
	ex := gocamel.NewExchange(liveCtx)
	ex.In.SetBody([]byte("ping"))
	require.NoError(t, producer.Send(ex))
	assert.Equal(t, "ok", string(ex.Out.GetBody().([]byte)))
}

func TestGRPCProducer_ChecksStartedAndAppliesTimeoutAndFiltersCamelMetadata(t *testing.T) {
	component := NewGRPCComponent()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Checks p.started even while another producer shares clientConn.
	addr, _ := startEchoServer(t, component, ctx)
	sharedEp, err := component.CreateEndpoint("grpc://" + addr + "/test.EchoService/Echo?insecure=true")
	require.NoError(t, err)
	p1, err := sharedEp.CreateProducer()
	require.NoError(t, err)
	p2, err := sharedEp.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, p1.Start(ctx))
	require.NoError(t, p2.Start(ctx))
	t.Cleanup(func() {
		_ = p1.Stop()
		_ = p2.Stop()
	})

	require.NoError(t, p1.Stop())
	stoppedEx := gocamel.NewExchange(ctx)
	stoppedEx.In.SetBody([]byte("should-fail"))
	err = p1.Send(stoppedEx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not started")

	// p2 still works on the shared endpoint.
	liveEx := gocamel.NewExchange(ctx)
	liveEx.In.SetBody([]byte("ok"))
	require.NoError(t, p2.Send(liveEx))

	// 2. Applies endpoint timeout via context.WithTimeout.
	slowEp, err := component.CreateEndpoint("grpc://127.0.0.1:0/test.SlowService/Wait")
	require.NoError(t, err)
	slowRelease := make(chan struct{})
	defer close(slowRelease)
	slowConsumer, err := slowEp.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		select {
		case <-e.Context.Done():
			return e.Context.Err()
		case <-slowRelease:
			return nil
		}
	}))
	require.NoError(t, err)
	require.NoError(t, slowConsumer.Start(ctx))
	t.Cleanup(func() { _ = slowConsumer.Stop() })

	timeoutProducer := newStartedProducer(t, component, ctx,
		"grpc://"+slowConsumer.(*GRPCConsumer).Address()+"/test.SlowService/Wait?insecure=true&timeout=60ms")
	timeoutEx := gocamel.NewExchange(ctx)
	timeoutEx.In.SetBody([]byte("wait"))
	start := time.Now()
	err = timeoutProducer.Send(timeoutEx)
	elapsed := time.Since(start)
	require.Error(t, err)
	assert.Less(t, elapsed, time.Second)
	st, ok := status.FromError(errors.Unwrap(err))
	require.True(t, ok)
	assert.Equal(t, codes.DeadlineExceeded, st.Code())

	// 3. Filters Camel* headers case-insensitively on both outbound & inbound metadata.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var receivedMD metadata.MD
	var mdMu sync.Mutex
	srv := grpcstd.NewServer(
		grpcstd.ForceServerCodec(RawBytesCodec{}),
		grpcstd.UnknownServiceHandler(func(_ any, stream grpcstd.ServerStream) error {
			var req []byte
			if err := stream.RecvMsg(&req); err != nil {
				return err
			}
			md, _ := metadata.FromIncomingContext(stream.Context())
			mdMu.Lock()
			receivedMD = md.Copy()
			mdMu.Unlock()

			_ = stream.SendHeader(metadata.Pairs(
				"camelfilename", "/etc/shadow",
				"camelsqlquery", "DROP TABLE users",
				"x-server-reply", "safe",
			))
			resp := []byte("done")
			return stream.SendMsg(&resp)
		}),
	)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.Stop()
		_ = lis.Close()
	})

	mdProducer := newStartedProducer(t, component, ctx,
		"grpc://"+lis.Addr().String()+"/test.EchoService/Echo?insecure=true")
	mdEx := gocamel.NewExchange(ctx)
	mdEx.In.SetBody([]byte("hello"))
	mdEx.In.SetHeader("camelFileName", "../../etc/passwd")
	mdEx.In.SetHeader("CAMELCustom", "internal")
	mdEx.In.SetHeader("x-trace", "trace-123")
	require.NoError(t, mdProducer.Send(mdEx))

	mdMu.Lock()
	assert.Empty(t, receivedMD.Get("camelfilename"))
	assert.Empty(t, receivedMD.Get("camelcustom"))
	assert.Equal(t, []string{"trace-123"}, receivedMD.Get("x-trace"))
	mdMu.Unlock()

	_, hasCamelFile := mdEx.Out.GetHeader("camelfilename")
	_, hasCamelSQL := mdEx.Out.GetHeader("camelsqlquery")
	assert.False(t, hasCamelFile, "inbound response Camel* metadata must be filtered")
	assert.False(t, hasCamelSQL, "inbound response Camel* metadata must be filtered")
	replyVal, ok := mdEx.Out.GetHeaderAsString("x-server-reply")
	assert.True(t, ok)
	assert.Equal(t, "safe", replyVal)
}
