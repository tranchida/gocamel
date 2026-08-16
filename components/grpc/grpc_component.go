package grpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	gocamel "gitlab.com/tranchida/gocamel"
)

// grpcFullMethodPattern validates a full gRPC method path, accepting both
// "/Service/Method" and "Service/Method" notations (the service part may be
// package-qualified, e.g. "helloworld.Greeter").
var grpcFullMethodPattern = regexp.MustCompile(`^/?[A-Za-z_][\w.]*/[A-Za-z_]\w*$`)

// Well-known Camel gRPC headers
const (
	// CamelGrpcMethod overrides the method configured in the URI. Honoured ONLY
	// when the endpoint sets allowHeaderOverride=true — see GRPCProducer.Send.
	CamelGrpcMethod            = "CamelGrpcMethod"
	CamelGrpcService           = "CamelGrpcService"
	CamelGrpcStatusCode        = "CamelGrpcStatusCode"
	CamelGrpcStatusDescription = "CamelGrpcStatusDescription"
	CamelGrpcMetadata          = "CamelGrpcMetadata"
)

// sensitiveOutboundHeaders are exchange headers carrying credentials or
// session state. They are never forwarded as outgoing RPC metadata (matching
// is case-insensitive), so a producer sitting downstream of a consumer does
// not leak a client's credentials to the remote server.
var sensitiveOutboundHeaders = map[string]bool{
	"authorization":       true,
	"cookie":              true,
	"proxy-authorization": true,
	"set-cookie":          true,
	"www-authenticate":    true,
}

// parseForwardHeaders builds the case-insensitive header allowlist from the
// forwardHeaders URI option. It returns nil when the option is unset or
// empty, meaning "forward all non-Camel, non-sensitive headers".
func parseForwardHeaders(value string) map[string]bool {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	allow := make(map[string]bool)
	for _, name := range strings.Split(value, ",") {
		if name = strings.TrimSpace(name); name != "" {
			allow[strings.ToLower(name)] = true
		}
	}
	if len(allow) == 0 {
		return nil
	}
	return allow
}

// RawBytesCodec encodes and decodes raw bytes or proto.Message values.
type RawBytesCodec struct{}

// Marshal encodes a message into bytes.
func (RawBytesCodec) Marshal(v any) ([]byte, error) {
	switch val := v.(type) {
	case []byte:
		return val, nil
	case *[]byte:
		if val != nil {
			return *val, nil
		}
		return []byte{}, nil
	case string:
		return []byte(val), nil
	case proto.Message:
		return proto.Marshal(val)
	default:
		return []byte(fmt.Sprintf("%v", val)), nil
	}
}

// Unmarshal decodes bytes into a message container.
func (RawBytesCodec) Unmarshal(data []byte, v any) error {
	switch target := v.(type) {
	case *[]byte:
		*target = append((*target)[0:0], data...)
		return nil
	case *string:
		*target = string(data)
		return nil
	case proto.Message:
		return proto.Unmarshal(data, target)
	default:
		return fmt.Errorf("grpc: unsupported unmarshal target type %T", v)
	}
}

// Name returns the codec name.
func (RawBytesCodec) Name() string {
	return "rawbytes"
}

// GRPCComponent implements the Camel gRPC component.
type GRPCComponent struct{}

// NewGRPCComponent creates a new GRPCComponent instance.
func NewGRPCComponent() *GRPCComponent {
	return &GRPCComponent{}
}

// CreateEndpoint creates a new GRPCEndpoint from a URI.
// Example URIs:
//
//	grpc://localhost:50051/helloworld.Greeter/SayHello?insecure=true
//	grpc://0.0.0.0:50051/helloworld.Greeter/SayHello
//	grpc://localhost:50051?service=Greeter&method=SayHello
//
// Options:
//
//	service             Service name (overrides the URI path)
//	method              Method name (overrides the URI path)
//	insecure            true to use insecure transport credentials (default: true).
//	                    When false, TLS with a minimum version of 1.2 is used.
//	caCert              Path to a PEM-encoded CA certificate trusted for server
//	                    verification when insecure=false (default: system pool)
//	forwardHeaders      Comma-separated allowlist of exchange headers the
//	                    producer forwards as outgoing RPC metadata. When set,
//	                    ONLY these headers are propagated. When unset, all
//	                    non-Camel headers except sensitive credentials
//	                    (Authorization, Cookie, Proxy-Authorization, Set-Cookie,
//	                    WWW-Authenticate) are forwarded.
//	allowHeaderOverride true to let the CamelGrpcMethod header replace the
//	                    configured method (default: false — see GRPCProducer.Send)
func (c *GRPCComponent) CreateEndpoint(uri string) (gocamel.Endpoint, error) {
	u, err := gocamel.ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid gRPC URI: %w", err)
	}

	host := u.Host
	if host == "" {
		host = "localhost:50051"
	}

	path := strings.TrimPrefix(u.Path, "/")
	var service, method string
	if path != "" {
		parts := strings.SplitN(path, "/", 2)
		if len(parts) == 2 {
			service = parts[0]
			method = parts[1]
		} else {
			method = parts[0]
		}
	}

	if s := gocamel.GetConfigValue(u, "service"); s != "" {
		service = s
	}
	if m := gocamel.GetConfigValue(u, "method"); m != "" {
		method = m
	}

	isInsecure := true
	if secStr := gocamel.GetConfigValue(u, "insecure"); secStr != "" {
		isInsecure = strings.EqualFold(secStr, "true")
	}

	caCert := gocamel.GetConfigValue(u, "caCert")
	if caCert != "" && isInsecure {
		return nil, fmt.Errorf("invalid gRPC URI: caCert requires insecure=false")
	}
	if !isInsecure {
		if _, err := transportCredentials(host, caCert); err != nil {
			return nil, fmt.Errorf("invalid gRPC URI: %w", err)
		}
	}

	allowHeaderOverride := false
	if v := gocamel.GetConfigValue(u, "allowHeaderOverride"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			allowHeaderOverride = b
		}
	}

	return &GRPCEndpoint{
		uri:                 uri,
		target:              host,
		service:             service,
		method:              method,
		insecure:            isInsecure,
		caCert:              caCert,
		allowHeaderOverride: allowHeaderOverride,
		forwardHeaders:      parseForwardHeaders(gocamel.GetConfigValue(u, "forwardHeaders")),
	}, nil
}

// transportCredentials builds the TLS transport credentials for a secure
// (insecure=false) client connection. serverName is derived from the target
// host; when caCertPath is set, the PEM file is loaded into a dedicated
// certificate pool, otherwise the system pool is used.
func transportCredentials(target, caCertPath string) (credentials.TransportCredentials, error) {
	serverName, _, err := net.SplitHostPort(target)
	if err != nil {
		serverName = target
	}

	var pool *x509.CertPool
	if caCertPath != "" {
		// caCertPath is trusted operator configuration from the endpoint URI
		// (like the SFTP private key path), never influenced by exchange data:
		// reading the file it names is the intended behaviour.
		pemBytes, err := os.ReadFile(caCertPath) // #nosec G304
		if err != nil {
			return nil, fmt.Errorf("failed to read caCert file %q: %w", caCertPath, err)
		}
		pool = x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemBytes) {
			return nil, fmt.Errorf("caCert file %q does not contain a valid PEM certificate", caCertPath)
		}
	}

	return credentials.NewTLS(&tls.Config{
		ServerName: serverName,
		MinVersion: tls.VersionTLS12,
		RootCAs:    pool,
	}), nil
}

// GRPCEndpoint represents a gRPC client or server endpoint.
type GRPCEndpoint struct {
	uri                 string
	target              string
	service             string
	method              string
	insecure            bool
	caCert              string
	allowHeaderOverride bool // opt-in: the CamelGrpcMethod header may replace the configured method
	// forwardHeaders is the optional header allowlist: when non-nil, ONLY the
	// listed headers are forwarded as outgoing metadata; when nil, every
	// non-Camel, non-sensitive header is forwarded.
	forwardHeaders map[string]bool

	clientConn *grpc.ClientConn
	consumer   *GRPCConsumer
	mu         sync.Mutex
}

// URI returns the endpoint URI.
func (e *GRPCEndpoint) URI() string {
	return e.uri
}

// FullMethod returns the full method path "/Service/Method" or "/Method".
func (e *GRPCEndpoint) FullMethod() string {
	if e.service != "" && e.method != "" {
		return fmt.Sprintf("/%s/%s", e.service, e.method)
	}
	if e.method != "" {
		if strings.HasPrefix(e.method, "/") {
			return e.method
		}
		return "/" + e.method
	}
	return ""
}

// CreateProducer creates a GRPCProducer for invoking RPC calls.
func (e *GRPCEndpoint) CreateProducer() (gocamel.Producer, error) {
	return &GRPCProducer{endpoint: e}, nil
}

// CreateConsumer creates a GRPCConsumer for hosting a gRPC server.
func (e *GRPCEndpoint) CreateConsumer(processor gocamel.Processor) (gocamel.Consumer, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.consumer != nil {
		return nil, fmt.Errorf("multiple consumers for the same gRPC endpoint are not allowed: %s", e.uri)
	}

	e.consumer = &GRPCConsumer{
		endpoint:  e,
		processor: processor,
	}
	return e.consumer, nil
}

// GRPCProducer invokes remote gRPC services.
type GRPCProducer struct {
	endpoint *GRPCEndpoint
}

// Start establishes the gRPC client connection.
func (p *GRPCProducer) Start(ctx context.Context) error {
	p.endpoint.mu.Lock()
	defer p.endpoint.mu.Unlock()

	if p.endpoint.clientConn != nil {
		return nil
	}

	var opts []grpc.DialOption
	if p.endpoint.insecure {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		creds, err := transportCredentials(p.endpoint.target, p.endpoint.caCert)
		if err != nil {
			return err
		}
		opts = append(opts, grpc.WithTransportCredentials(creds))
	}

	conn, err := grpc.NewClient(p.endpoint.target, opts...)
	if err != nil {
		return fmt.Errorf("grpc client connection error: %w", err)
	}

	p.endpoint.clientConn = conn
	return nil
}

// Stop closes the client connection.
func (p *GRPCProducer) Stop() error {
	p.endpoint.mu.Lock()
	defer p.endpoint.mu.Unlock()

	if p.endpoint.clientConn != nil {
		err := p.endpoint.clientConn.Close()
		p.endpoint.clientConn = nil
		return err
	}
	return nil
}

// Send executes the RPC call and puts response in the Exchange.
func (p *GRPCProducer) Send(exchange *gocamel.Exchange) error {
	p.endpoint.mu.Lock()
	conn := p.endpoint.clientConn
	p.endpoint.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("grpc producer is not started or connected")
	}

	// The CamelGrpcMethod header replaces the configured method, so it is
	// honoured only when the endpoint opts in with allowHeaderOverride=true.
	// Enable it only where headers cannot be influenced by untrusted input:
	// any component that maps external metadata onto headers would otherwise
	// hand an attacker control over which remote procedure gets invoked.
	fullMethod := p.endpoint.FullMethod()
	if p.endpoint.allowHeaderOverride {
		if hMethod, ok := exchange.GetHeaderAsString(CamelGrpcMethod); ok && hMethod != "" {
			if !grpcFullMethodPattern.MatchString(hMethod) {
				return fmt.Errorf("grpc: malformed method in header %s: %q", CamelGrpcMethod, hMethod)
			}
			if strings.HasPrefix(hMethod, "/") {
				fullMethod = hMethod
			} else {
				fullMethod = "/" + hMethod
			}
		}
	}

	if fullMethod == "" {
		return fmt.Errorf("grpc: method is required to invoke an RPC call")
	}

	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}

	// Propagate metadata headers: Camel-prefixed internal headers and
	// sensitive credential headers are never forwarded; when an explicit
	// forwardHeaders allowlist is configured, only the listed headers remain
	// (sensitive ones stay blocked).
	md := metadata.MD{}
	for name, value := range exchange.In.GetHeaders() {
		if strings.HasPrefix(name, "Camel") {
			continue
		}
		lower := strings.ToLower(name)
		if sensitiveOutboundHeaders[lower] {
			continue
		}
		if p.endpoint.forwardHeaders != nil && !p.endpoint.forwardHeaders[lower] {
			continue
		}
		md.Append(lower, fmt.Sprintf("%v", value))
	}
	if len(md) > 0 {
		ctx = metadata.NewOutgoingContext(ctx, md)
	}

	var reqBytes []byte
	body := exchange.In.GetBody()
	switch b := body.(type) {
	case []byte:
		reqBytes = b
	case string:
		reqBytes = []byte(b)
	case proto.Message:
		var err error
		reqBytes, err = proto.Marshal(b)
		if err != nil {
			return fmt.Errorf("failed to marshal proto body: %w", err)
		}
	case nil:
		reqBytes = []byte{}
	default:
		reqBytes = []byte(fmt.Sprintf("%v", b))
	}

	var respBytes []byte
	var headerMD, trailerMD metadata.MD

	callOpts := []grpc.CallOption{
		grpc.ForceCodec(RawBytesCodec{}),
		grpc.Header(&headerMD),
		grpc.Trailer(&trailerMD),
	}

	err := conn.Invoke(ctx, fullMethod, &reqBytes, &respBytes, callOpts...)
	if err != nil {
		st, _ := status.FromError(err)
		exchange.Out.SetHeader(CamelGrpcStatusCode, int(st.Code()))
		exchange.Out.SetHeader(CamelGrpcStatusDescription, st.Message())
		return fmt.Errorf("grpc invocation error for %s: %w", fullMethod, err)
	}

	exchange.Out.SetHeader(CamelGrpcStatusCode, int(codes.OK))
	exchange.Out.SetHeader(CamelGrpcStatusDescription, "OK")
	exchange.Out.SetBody(respBytes)

	for k, vals := range headerMD {
		if len(vals) > 0 {
			exchange.Out.SetHeader(k, vals[0])
		}
	}

	return nil
}

// GRPCConsumer hosts a gRPC server that dispatches incoming calls to a GoCamel route.
type GRPCConsumer struct {
	endpoint  *GRPCEndpoint
	processor gocamel.Processor
	server    *grpc.Server
	listener  net.Listener
	wg        sync.WaitGroup
}

// Start begins listening on the configured address.
func (c *GRPCConsumer) Start(ctx context.Context) error {
	c.endpoint.mu.Lock()
	defer c.endpoint.mu.Unlock()

	target := c.endpoint.target
	if !strings.Contains(target, ":") {
		target = ":" + target
	}

	lis, err := net.Listen("tcp", target)
	if err != nil {
		return fmt.Errorf("grpc consumer failed to listen on %s: %w", target, err)
	}
	c.listener = lis

	// Use unknown service handler to receive all RPC calls and forward them to GoCamel
	c.server = grpc.NewServer(
		grpc.ForceServerCodec(RawBytesCodec{}),
		grpc.UnknownServiceHandler(c.handler),
	)

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		_ = c.server.Serve(lis)
	}()

	return nil
}

// Stop gracefully shuts down the gRPC server.
func (c *GRPCConsumer) Stop() error {
	if c.server != nil {
		c.server.GracefulStop()
	}
	if c.listener != nil {
		_ = c.listener.Close()
	}
	c.wg.Wait()

	c.endpoint.mu.Lock()
	defer c.endpoint.mu.Unlock()
	if c.endpoint.consumer == c {
		c.endpoint.consumer = nil
	}
	return nil
}

// Address returns the actual listening address (useful when port 0 is used in tests).
func (c *GRPCConsumer) Address() string {
	if c.listener != nil {
		return c.listener.Addr().String()
	}
	return c.endpoint.target
}

func (c *GRPCConsumer) handler(srv any, stream grpc.ServerStream) error {
	ctx := stream.Context()
	fullMethod, _ := grpc.MethodFromServerStream(stream)

	var reqBytes []byte
	if err := stream.RecvMsg(&reqBytes); err != nil {
		return status.Errorf(codes.Internal, "failed to receive request: %v", err)
	}

	exchange := gocamel.NewExchange(ctx)
	exchange.In.SetBody(reqBytes)
	exchange.In.SetHeader(CamelGrpcMethod, fullMethod)

	if md, ok := metadata.FromIncomingContext(ctx); ok {
		for k, vals := range md {
			if len(vals) > 0 {
				exchange.In.SetHeader(k, vals[0])
			}
		}
	}

	err := gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange))
	if err != nil {
		gocamel.LogError("grpc consumer processing error",
			"uri", c.endpoint.uri,
			"method", fullMethod,
			"error", err,
			"exchange_id", exchange.ID,
		)
		code := codes.Internal
		if cCode, ok := exchange.GetHeaderAsInt(CamelGrpcStatusCode); ok && cCode >= 0 && cCode <= int(codes.Unauthenticated) {
			code = codes.Code(cCode) // #nosec G115
		}
		return status.Errorf(code, "route error: %v", err)
	}

	respBody := exchange.GetOut().GetBody()
	var respBytes []byte
	switch b := respBody.(type) {
	case []byte:
		respBytes = b
	case string:
		respBytes = []byte(b)
	case proto.Message:
		var err error
		respBytes, err = proto.Marshal(b)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to marshal response proto: %v", err)
		}
	case nil:
		respBytes = []byte{}
	default:
		respBytes = []byte(fmt.Sprintf("%v", b))
	}

	if err := stream.SendMsg(&respBytes); err != nil {
		return status.Errorf(codes.Internal, "failed to send response: %v", err)
	}

	return nil
}
