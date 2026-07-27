package gocamel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultMaxBodySize bounds how many bytes the HTTP component reads from an
// inbound request body (consumer) or an upstream response body (producer).
// Without a bound, a single oversized payload can exhaust process memory
// (CWE-400). Override per component with SetMaxBodySize; 0 means unlimited.
const DefaultMaxBodySize int64 = 32 << 20 // 32 MiB

// hopByHopHeaders must never be copied between an inbound and an outbound
// message. Connection-scoped headers (RFC 9110 §7.6.1) are meaningless on a
// different connection, and copying framing headers (Content-Length,
// Transfer-Encoding) corrupts the message the net/http stack generates.
var hopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
	"Content-Length":      true,
	"Host":                true,
}

func isHopByHopHeader(key string) bool {
	return hopByHopHeaders[http.CanonicalHeaderKey(key)]
}

// HTTPComponent represents the HTTP component.
//
// The optional consumer-side Middleware is applied to every incoming request
// handler created by HTTPConsumer.Start(). It is intended for authentication,
// TLS termination, rate limiting, or request logging — features the HTTP
// consumer does not provide out of the box.
type HTTPComponent struct {
	client                  *http.Client
	middleware              func(http.Handler) http.Handler
	maxBodySize             int64
	throwExceptionOnFailure bool
}

// NewHTTPComponent creates a new HTTPComponent instance with default timeouts.
func NewHTTPComponent() *HTTPComponent {
	return &HTTPComponent{
		client: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				DialContext: (&net.Dialer{
					Timeout:   10 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			},
		},
		maxBodySize:             DefaultMaxBodySize,
		throwExceptionOnFailure: true,
	}
}

// SetMiddleware installs an http.Handler middleware applied to every consumer
// handler. Use it to plug in authentication, TLS, rate limiting, etc.
// The HTTP consumer ships with NO authentication: hosts exposed to untrusted
// networks must set a middleware (e.g. token check, mTLS, OAuth) before Start().
func (c *HTTPComponent) SetMiddleware(mw func(http.Handler) http.Handler) {
	c.middleware = mw
}

// SetMaxBodySize bounds the number of bytes read from an inbound request body
// or an upstream response body. A value <= 0 disables the limit; only do that
// when both peers are trusted. Defaults to DefaultMaxBodySize.
func (c *HTTPComponent) SetMaxBodySize(n int64) {
	c.maxBodySize = n
}

// SetThrowExceptionOnFailure controls whether the producer turns an upstream
// response with status >= 300 into an error (the Apache Camel default).
// Set to false to treat every response as a success and inspect
// CamelHttpResponseCode in the route instead.
func (c *HTTPComponent) SetThrowExceptionOnFailure(v bool) {
	c.throwExceptionOnFailure = v
}

// CreateEndpoint creates a new HTTP endpoint
func (c *HTTPComponent) CreateEndpoint(uri string) (Endpoint, error) {
	if _, err := ParseURI(uri); err != nil {
		return nil, fmt.Errorf("invalid HTTP URI: %v", err)
	}

	return &HTTPEndpoint{
		uri:                     uri,
		client:                  c.client,
		middleware:              c.middleware,
		maxBodySize:             c.maxBodySize,
		throwExceptionOnFailure: c.throwExceptionOnFailure,
	}, nil
}

// HTTPEndpoint represents an HTTP endpoint
type HTTPEndpoint struct {
	uri                     string
	client                  *http.Client
	middleware              func(http.Handler) http.Handler
	maxBodySize             int64
	throwExceptionOnFailure bool
}

// URI returns the endpoint URI
func (e *HTTPEndpoint) URI() string {
	return e.uri
}

// CreateProducer creates an HTTP producer
func (e *HTTPEndpoint) CreateProducer() (Producer, error) {
	return &HTTPProducer{
		uri:                     e.uri,
		client:                  e.client,
		maxBodySize:             e.maxBodySize,
		throwExceptionOnFailure: e.throwExceptionOnFailure,
	}, nil
}

// CreateConsumer creates an HTTP consumer
func (e *HTTPEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return &HTTPConsumer{
		uri:         e.uri,
		processor:   processor,
		middleware:  e.middleware,
		maxBodySize: e.maxBodySize,
	}, nil
}

// HTTPProducer represents an HTTP producer
type HTTPProducer struct {
	uri                     string
	client                  *http.Client
	maxBodySize             int64
	throwExceptionOnFailure bool
}

// Start starts the HTTP producer
func (p *HTTPProducer) Start(ctx context.Context) error {
	return nil // No initialization needed for HTTP producer
}

// Stop stops the HTTP producer
func (p *HTTPProducer) Stop() error {
	return nil // No cleanup needed for HTTP producer
}

// Send sends a message via HTTP
func (p *HTTPProducer) Send(exchange *Exchange) error {
	// Creating the request
	var bodyReader io.Reader
	if body, ok := exchange.GetIn().GetBody().([]byte); ok {
		bodyReader = bytes.NewReader(body)
	} else if body, ok := exchange.GetIn().GetBody().(string); ok {
		bodyReader = bytes.NewReader([]byte(body))
	}

	// HTTP method: the CamelHttpMethod header overrides the default POST.
	method := http.MethodPost
	if v, ok := exchange.GetHeaderAsString(CamelHttpMethod); ok && v != "" {
		method = strings.ToUpper(v)
	}

	reqCtx := exchange.Context
	if reqCtx == nil {
		reqCtx = context.Background()
	}
	req, err := http.NewRequestWithContext(reqCtx, method, p.uri, bodyReader)
	if err != nil {
		return fmt.Errorf("error creating HTTP request: %w", err)
	}

	// Adding headers (CRLF-stripped to prevent header injection — CWE-113).
	// Hop-by-hop and framing headers are skipped: when this producer sits
	// downstream of the HTTP consumer, the In message still carries the
	// inbound request's Content-Length/Host, and forwarding those would
	// misframe the outbound request.
	for key, value := range exchange.GetIn().GetHeaders() {
		if isHopByHopHeader(key) {
			continue
		}
		if strValue, ok := value.(string); ok {
			if err := setSafeHTTPHeader(req.Header, key, strValue); err != nil {
				return fmt.Errorf("error setting HTTP request header: %w", err)
			}
		}
	}

	// Sending the request
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("error sending HTTP request: %w", err)
	}
	defer resp.Body.Close()

	// Reading the response body, bounded so a hostile or malfunctioning
	// upstream cannot exhaust memory.
	body, err := readLimited(resp.Body, p.maxBodySize)
	if err != nil {
		return fmt.Errorf("error reading HTTP response: %w", err)
	}

	// Updating the exchange with the response
	exchange.GetOut().SetBody(body)
	for key, value := range resp.Header {
		exchange.GetOut().SetHeader(key, value[0])
	}
	exchange.GetOut().SetHeader(CamelHttpResponseCode, resp.StatusCode)
	exchange.GetOut().SetHeader(CamelHttpResponseText, resp.Status)
	// Retained for backward compatibility with routes reading "Status-Code".
	exchange.GetOut().SetHeader("Status-Code", strconv.Itoa(resp.StatusCode))

	// Apache Camel semantics: a response >= 300 is a failure unless the
	// caller opted out via SetThrowExceptionOnFailure(false).
	if p.throwExceptionOnFailure && resp.StatusCode >= 300 {
		return fmt.Errorf("http: %s returned status %d: %w", p.uri, resp.StatusCode, ErrHTTPStatus)
	}

	return nil
}

// ErrHTTPStatus is wrapped by the error the HTTP producer returns when an
// upstream responds with status >= 300. Routes can branch on it with
// errors.Is; the response body and CamelHttpResponseCode header are still
// populated on the Out message.
var ErrHTTPStatus = errors.New("http status error")

// readLimited reads at most max bytes from r, returning an error if the source
// has more to give. max <= 0 disables the limit.
func readLimited(r io.Reader, max int64) ([]byte, error) {
	if max <= 0 {
		return io.ReadAll(r)
	}
	// Read one extra byte so an exactly-at-limit body is distinguishable
	// from an oversized one.
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("body exceeds the %d byte limit", max)
	}
	return data, nil
}

// HTTPConsumer represents an HTTP consumer
type HTTPConsumer struct {
	uri         string
	processor   Processor
	middleware  func(http.Handler) http.Handler
	maxBodySize int64
	server      *http.Server
	wg          sync.WaitGroup
	stopOnce    sync.Once
}

// Start starts the HTTP consumer
func (c *HTTPConsumer) Start(ctx context.Context) error {
	parsedURL, err := url.Parse(c.uri)
	if err != nil {
		return fmt.Errorf("invalid HTTP URI: %v", err)
	}

	port := parsedURL.Port()
	if port == "" {
		port = "8080" // Default port
	}
	host := parsedURL.Hostname()
	addr := net.JoinHostPort(host, port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("error listening on %s: %w", addr, err)
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// http.ServeMux panics on an empty pattern, so a URI without a path
	// ("http://host:8080") must still register a valid route.
	pattern := parsedURL.Path
	if pattern == "" {
		pattern = "/"
	}

	mux := http.NewServeMux()
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		exchange := NewExchange(r.Context())

		// Reading the request body, bounded so an oversized upload cannot
		// exhaust process memory (CWE-400).
		reqBody := r.Body
		if c.maxBodySize > 0 {
			reqBody = http.MaxBytesReader(w, r.Body, c.maxBodySize)
		}
		body, err := io.ReadAll(reqBody)
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				LogWarn("HTTP consumer: request body too large", "uri", c.uri, "limit", c.maxBodySize)
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			// Internal detail goes to the log, not to the client.
			LogError("HTTP consumer: error reading request body", "uri", c.uri, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		// Configuring the exchange. requestHeaders records what arrived so
		// that unmodified inbound headers are not echoed back in the response.
		exchange.SetBody(body)
		requestHeaders := make(map[string]string, len(r.Header))
		for key, values := range r.Header {
			if len(values) > 0 {
				exchange.SetHeader(key, values[0])
				requestHeaders[key] = values[0]
			}
		}

		// Processing the message
		if err := CompleteExchange(exchange, c.processor.Process(exchange)); err != nil {
			// The error may carry internal detail (SQL text, file paths,
			// driver errors); log it and return a generic message.
			LogError("HTTP consumer: route processing failed",
				"uri", c.uri, "exchange_id", exchange.ID, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		// Response: Out if defined, otherwise In (InOut Apache Camel behavior)
		response := exchange.GetResponse()

		// Set response headers (CRLF-stripped to prevent header injection — CWE-113).
		// Headers that merely came back unchanged from the request are skipped:
		// echoing them leaks credentials (Cookie, Authorization) and reinjects
		// the request's Content-Length, which corrupts the response framing.
		for key, value := range response.GetHeaders() {
			// The status-carrying headers drive WriteHeader below; emitting
			// them as literal response headers too would be noise.
			if isHopByHopHeader(key) || key == CamelHttpResponseCode || key == "Status-Code" {
				continue
			}
			strValue, ok := value.(string)
			if !ok {
				continue
			}
			if prev, echoed := requestHeaders[key]; echoed && prev == strValue {
				continue
			}
			if err := setSafeHTTPHeader(w.Header(), key, strValue); err != nil {
				LogError("HTTP consumer: invalid response header", "uri", c.uri, "error", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
		}

		// A route may set CamelHttpResponseCode on the response message to
		// control the status code; it must be written before the body.
		if code, ok := responseStatusCode(response); ok {
			w.WriteHeader(code)
		}

		switch body := response.GetBody().(type) {
		case []byte:
			writeResponseBody(w, body, c.uri)
		case string:
			writeResponseBody(w, []byte(body), c.uri)
		case nil:
			// no body
		default:
			// Fall back to the string representation rather than silently
			// returning an empty response for non-string bodies.
			writeResponseBody(w, []byte(fmt.Sprintf("%v", body)), c.uri)
		}
	})

	var handler http.Handler = mux
	if c.middleware != nil {
		handler = c.middleware(mux)
	}

	c.server = &http.Server{
		Addr:    addr,
		Handler: handler,
		// Bound the time a client may take to send its headers (slowloris).
		ReadHeaderTimeout: 10 * time.Second,
		// BaseContext ties every inbound request to the consumer's parent ctx
		// so that CamelContext.Stop() cancels in-flight requests.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	// Starting the server. The listener is created synchronously above so bind
	// failures are returned from Start. The WaitGroup is signalled when Serve
	// returns, so Stop() can wait for the goroutine to fully unwind before
	// the CamelContext continues its shutdown.
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		if err := c.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			LogError("HTTP server error", "error", err)
		}
	}()

	return nil
}

// Stop stops the HTTP consumer. Safe to call multiple times: server.Shutdown
// is invoked at most once via sync.Once and Stop blocks until the
// ListenAndServe goroutine has fully returned to avoid leaking goroutines
// past CamelContext.Stop().
func (c *HTTPConsumer) Stop() error {
	if c.server == nil {
		return nil
	}
	var err error
	c.stopOnce.Do(func() {
		// Shutdown blocks until idle connections close; bounded by a 30s
		// deadline to prevent a stalled client from hanging the whole
		// CamelContext shutdown.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err = c.server.Shutdown(shutdownCtx)
	})
	c.wg.Wait()
	return err
}

// responseStatusCode reads an explicit HTTP status from the response message.
// Both CamelHttpResponseCode and the legacy "Status-Code" header are accepted,
// as int or as a numeric string. Values outside the valid HTTP range are
// ignored so a bad value cannot make net/http panic.
func responseStatusCode(response *Message) (int, bool) {
	for _, key := range []string{CamelHttpResponseCode, "Status-Code"} {
		value, exists := response.GetHeader(key)
		if !exists {
			continue
		}
		var code int
		switch v := value.(type) {
		case int:
			code = v
		case int64:
			code = int(v)
		case float64:
			code = int(v)
		case string:
			parsed, err := strconv.Atoi(v)
			if err != nil {
				continue
			}
			code = parsed
		default:
			continue
		}
		if code < 100 || code > 599 {
			continue
		}
		return code, true
	}
	return 0, false
}

// writeResponseBody writes the body and logs a failed write, which otherwise
// disappears silently (a client disconnecting mid-response is the common case).
func writeResponseBody(w http.ResponseWriter, body []byte, uri string) {
	if _, err := w.Write(body); err != nil {
		LogWarn("HTTP consumer: error writing response body", "uri", uri, "error", err)
	}
}

// setSafeHTTPHeader sets an HTTP header after rejecting CRLF in the key or
// value. CRLF in either field allows an attacker to inject arbitrary headers
// or split the HTTP response (CWE-113, HTTP Response Splitting).
func setSafeHTTPHeader(h http.Header, key, value string) error {
	if strings.ContainsAny(key, "\r\n") {
		return fmt.Errorf("header name contains CRLF: %q", key)
	}
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("header %q value contains CRLF", key)
	}
	h.Set(key, value)
	return nil
}
