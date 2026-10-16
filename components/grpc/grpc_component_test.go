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
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"

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

