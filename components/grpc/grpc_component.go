package grpc

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	gocamel "gitlab.com/tranchida/gocamel"
)

// Well-known Camel gRPC headers
const (
	CamelGrpcMethod            = "CamelGrpcMethod"
	CamelGrpcService           = "CamelGrpcService"
	CamelGrpcStatusCode        = "CamelGrpcStatusCode"
	CamelGrpcStatusDescription = "CamelGrpcStatusDescription"
	CamelGrpcMetadata          = "CamelGrpcMetadata"
)

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

	return &GRPCEndpoint{
		uri:      uri,
		target:   host,
		service:  service,
		method:   method,
		insecure: isInsecure,
	}, nil
}

// GRPCEndpoint represents a gRPC client or server endpoint.
type GRPCEndpoint struct {
	uri      string
	target   string
	service  string
	method   string
	insecure bool

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

	fullMethod := p.endpoint.FullMethod()
	if hMethod, ok := exchange.GetHeaderAsString(CamelGrpcMethod); ok && hMethod != "" {
		if strings.HasPrefix(hMethod, "/") {
			fullMethod = hMethod
		} else {
			fullMethod = "/" + hMethod
		}
	}

	if fullMethod == "" {
		return fmt.Errorf("grpc: method is required to invoke an RPC call")
	}

	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}

	// Propagate metadata headers
	md := metadata.MD{}
	for k, v := range exchange.In.GetHeaders() {
		if !strings.HasPrefix(k, "Camel") {
			md.Append(k, fmt.Sprintf("%v", v))
		}
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
