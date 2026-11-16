package gocamel

import (
	"bytes"
	"context"
	"crypto/sha256"
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

// responseOnlyHeaders describe an HTTP response and have no meaning in a
// request. The producer stores the upstream response headers on the message,
// so in a chain such as To("http://a").To("http://b") they used to be sent to
// b: A's session cookie (Set-Cookie), server banner and status leaked to
// another service. They are never sent upstream.
var responseOnlyHeaders = map[string]bool{
	"Accept-Ranges":                    true,
	"Access-Control-Allow-Credentials": true,
	"Access-Control-Allow-Headers":     true,
	"Access-Control-Allow-Methods":     true,
	"Access-Control-Allow-Origin":      true,
	"Access-Control-Expose-Headers":    true,
	"Access-Control-Max-Age":           true,
	"Age":                              true,
	"Alt-Svc":                          true,
	"Content-Security-Policy":          true,
	"Date":                             true,
	"Etag":                             true,
	"Expires":                          true,
	"Last-Modified":                    true,
	"Location":                         true,
	"Retry-After":                      true,
	"Server":                           true,
	"Set-Cookie":                       true,
	"Set-Cookie2":                      true,
	"Status-Code":                      true,
	"Strict-Transport-Security":        true,
	"Vary":                             true,
	"Www-Authenticate":                 true,
	"X-Powered-By":                     true,
}

func isResponseOnlyHeader(key string) bool {
	return responseOnlyHeaders[http.CanonicalHeaderKey(key)]
}

// authorizationHeaders are request headers that carry a client's credentials
// (cookie, bearer token). When the HTTP producer is configured with
// SetSkipAuthorizationHeaders(true), these are not forwarded to the upstream,
// so a producer sitting downstream of the HTTP consumer does not leak a
// client's credentials to a third-party upstream.
var authorizationHeaders = map[string]bool{
	"Authorization": true,
	"Cookie":        true,
}

// IsCamelHeader reports whether key is an internal Camel* header. These carry
// routing metadata (request path and query, file names, SQL row counts...)
// and must never cross a transport boundary in either direction: an inbound
// message (HTTP request, Kafka record, STOMP frame...) cannot set them, and
// neither a response nor an outbound message exposes them. The match is
// case-insensitive because transports canonicalize or lowercase header names
// ("Camelfilename", "camelfilename"). Consumers that map transport headers
// onto the exchange must skip the keys for which it returns true.
func IsCamelHeader(key string) bool {
	return len(key) >= 5 && strings.EqualFold(key[:5], "camel")
}

// httpInboundCredentialsProperty records fingerprints of the credential
// headers (Authorization, Cookie) received by the HTTP consumer, so the
// producer can recognize and drop them when they would be forwarded unchanged
// to an upstream. Only SHA-256 digests are stored, never the secrets.
const httpInboundCredentialsProperty = "CamelHttpInboundCredentials"

func credentialFingerprint(key, value string) [sha256.Size]byte {
	return sha256.Sum256([]byte(http.CanonicalHeaderKey(key) + "\x00" + value))
}

// isForwardedInboundCredential reports whether key/value is a credential
// header the HTTP consumer received and the route did not change.
func isForwardedInboundCredential(exchange *Exchange, key, value string) bool {
	if !authorizationHeaders[http.CanonicalHeaderKey(key)] {
		return false
	}
	v, ok := exchange.GetProperty(httpInboundCredentialsProperty)
	if !ok {
		return false
	}
	fingerprints, ok := v.(map[[sha256.Size]byte]struct{})
	if !ok {
		return false
	}
	_, found := fingerprints[credentialFingerprint(key, value)]
	return found
}

// headerValues returns the string values of a header set on a message: a
// string, or a []string for a multi-valued header such as Set-Cookie. Other
// types are not representable as HTTP headers and are skipped.
func headerValues(value any) ([]string, bool) {
	switch v := value.(type) {
	case string:
		return []string{v}, true
	case []string:
		return v, len(v) > 0
	default:
		return nil, false
	}
}

// messageHeaderValue converts received HTTP header values to a message header
// value: a string for the common single-valued case, a []string when the
// header was repeated (Set-Cookie, Via...) so no value is lost.
func messageHeaderValue(values []string) any {
	if len(values) == 1 {
		return values[0]
	}
	return append([]string(nil), values...)
}

// HTTPComponent represents the HTTP component.
//
// The optional consumer-side Middleware is applied to every incoming request
// handler created by HTTPConsumer.Start(). It is intended for authentication,
// TLS termination, rate limiting, or request logging — features the HTTP
// consumer does not provide out of the box.
type HTTPComponent struct {
	client                   *http.Client
	middleware               func(http.Handler) http.Handler
	maxBodySize              int64
	throwExceptionOnFailure  bool
	skipAuthorizationHeaders bool
	forwardInboundCreds      bool
	servers                  *httpServers // listeners shared by this component's consumers
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
		servers:                 newHTTPServers(),
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

// SetSkipAuthorizationHeaders controls whether the producer strips
// Authorization and Cookie headers before sending. Disabled by default to
// preserve the ability to call an authenticated upstream; enable it when the
// producer sits downstream of the HTTP consumer so a client's credentials are
// not forwarded to a third-party upstream.
func (c *HTTPComponent) SetSkipAuthorizationHeaders(v bool) {
	c.skipAuthorizationHeaders = v
}

// SetForwardInboundCredentials controls whether the producer forwards the
// Authorization and Cookie headers a client sent to the HTTP consumer when the
// route passes them on unchanged. Disabled by default so a consumer -> producer
// route does not leak a client's credentials to the upstream; credentials the
// route sets itself are always sent (unless SetSkipAuthorizationHeaders).
// Enable it for a deliberate authenticating pass-through proxy.
func (c *HTTPComponent) SetForwardInboundCredentials(v bool) {
	c.forwardInboundCreds = v
}

// CreateEndpoint creates a new HTTP endpoint
func (c *HTTPComponent) CreateEndpoint(uri string) (Endpoint, error) {
	if _, err := ParseURI(uri); err != nil {
		return nil, fmt.Errorf("invalid HTTP URI: %v", err)
	}

	return &HTTPEndpoint{
		uri:                      uri,
		client:                   c.client,
		middleware:               c.middleware,
		maxBodySize:              c.maxBodySize,
		throwExceptionOnFailure:  c.throwExceptionOnFailure,
		skipAuthorizationHeaders: c.skipAuthorizationHeaders,
		forwardInboundCreds:      c.forwardInboundCreds,
		servers:                  c.servers,
	}, nil
}

// HTTPEndpoint represents an HTTP endpoint
type HTTPEndpoint struct {
	servers                  *httpServers
	uri                      string
	client                   *http.Client
	middleware               func(http.Handler) http.Handler
	maxBodySize              int64
	throwExceptionOnFailure  bool
	skipAuthorizationHeaders bool
	forwardInboundCreds      bool
}

// URI returns the endpoint URI
func (e *HTTPEndpoint) URI() string {
	return e.uri
}

// CreateProducer creates an HTTP producer
func (e *HTTPEndpoint) CreateProducer() (Producer, error) {
	return &HTTPProducer{
		uri:                      e.uri,
		client:                   e.client,
		maxBodySize:              e.maxBodySize,
		throwExceptionOnFailure:  e.throwExceptionOnFailure,
		skipAuthorizationHeaders: e.skipAuthorizationHeaders,
		forwardInboundCreds:      e.forwardInboundCreds,
	}, nil
}

func (e *HTTPEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	parsed, err := url.Parse(e.uri)
	if err != nil {
		return nil, fmt.Errorf("invalid HTTP URI: %v", err)
	}
	if strings.EqualFold(parsed.Scheme, "https") {
		return nil, fmt.Errorf("http consumer does not terminate TLS; use an http:// URI and terminate TLS in front of the listener")
	}
	return &HTTPConsumer{
		uri:         e.uri,
		processor:   processor,
		middleware:  e.middleware,
		maxBodySize: e.maxBodySize,
		servers:     e.servers,
	}, nil
}

// HTTPProducer represents an HTTP producer
type HTTPProducer struct {
	uri                      string
	client                   *http.Client
	maxBodySize              int64
	throwExceptionOnFailure  bool
	skipAuthorizationHeaders bool
	forwardInboundCreds      bool
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
	// Creating the request. Any other body type used to be sent as an empty
	// body without error; marshal it first (e.g. MarshalJSON) instead.
	var bodyReader io.Reader
	switch body := exchange.GetIn().GetBody().(type) {
	case nil:
	case []byte:
		bodyReader = bytes.NewReader(body)
	case string:
		bodyReader = strings.NewReader(body)
	case io.Reader:
		bodyReader = body
	default:
		return fmt.Errorf("http: unsupported body type %T; marshal it to []byte or string first", body)
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
	// Internal Camel* headers stay inside the route, response-only headers
	// left by a previous HTTP call are not replayed as request headers, and a
	// client's credentials received by the HTTP consumer are not relayed
	// unchanged to the upstream unless SetForwardInboundCredentials(true).
	for key, value := range exchange.GetIn().GetHeaders() {
		if isHopByHopHeader(key) || IsCamelHeader(key) || isResponseOnlyHeader(key) {
			continue
		}
		if p.skipAuthorizationHeaders && authorizationHeaders[http.CanonicalHeaderKey(key)] {
			continue
		}
		values, ok := headerValues(value)
		if !ok {
			continue
		}
		for i, v := range values {
			if !p.forwardInboundCreds && isForwardedInboundCredential(exchange, key, v) {
				continue
			}
			if err := addSafeHTTPHeader(req.Header, key, v, i == 0); err != nil {
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
	body, err := ReadLimited(resp.Body, p.maxBodySize)
	if err != nil {
		return fmt.Errorf("error reading HTTP response: %w", err)
	}

	// Updating the exchange with the response
	exchange.GetOut().SetBody(body)
	for key, values := range resp.Header {
		if len(values) > 0 {
			exchange.GetOut().SetHeader(key, messageHeaderValue(values))
		}
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

// ReadLimited reads at most max bytes from r, returning an error if the source
// has more to give. max <= 0 disables the limit. It is the shared bound for
// every component that reads a remote body (HTTP, mail, FTP, SFTP, SMB) so a
// hostile or oversized payload cannot exhaust process memory.
func ReadLimited(r io.Reader, max int64) ([]byte, error) {
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
	mu          sync.Mutex
	// transition is non-nil while a Start or Stop is in progress. It serializes
	// the two operations so a Stop racing a Start cannot observe running=false
	// and return before the server is up (and vice versa). Guarded by mu.
	transition chan struct{}
	running    bool
	stopping   bool
	servers    *httpServers      // registry of shared listeners; nil: private to this consumer
	shared     *sharedHTTPServer // listener this consumer is registered on while running
	pattern    string            // path pattern registered on shared
	server     *http.Server      // shared.server, while running
	handlerWG  sync.WaitGroup
	cancel     context.CancelFunc
}

// acquireTransition waits for an in-progress Start/Stop and claims the next
// transition. It never holds c.mu while waiting.
func (c *HTTPConsumer) acquireTransition() {
	for {
		c.mu.Lock()
		if c.transition != nil {
			done := c.transition
			c.mu.Unlock()
			<-done
			continue
		}
		c.transition = make(chan struct{})
		c.mu.Unlock()
		return
	}
}

// releaseTransition publishes the final state and wakes Start/Stop waiters.
func (c *HTTPConsumer) releaseTransition() {
	c.mu.Lock()
	close(c.transition)
	c.transition = nil
	c.mu.Unlock()
}

// Start starts the HTTP consumer
func (c *HTTPConsumer) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	c.acquireTransition()
	defer c.releaseTransition()

	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return errors.New("http consumer is already running")
	}
	c.mu.Unlock()

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
	reqCtx, cancel := context.WithCancel(ctx)
	// http.ServeMux panics on an empty pattern, so a URI without a path
	// ("http://host:8080") must still register a valid route.
	pattern := parsedURL.Path
	if pattern == "" {
		pattern = "/"
	}

	handle := func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		if !c.running || c.stopping {
			c.mu.Unlock()
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}
		c.handlerWG.Add(1)
		c.mu.Unlock()
		defer c.handlerWG.Done()

		// The listener is shared by every consumer on this host:port, so the
		// request context cannot be the consumer's: tie the two together here
		// so that stopping this consumer (or the CamelContext) cancels its
		// in-flight requests only.
		requestCtx, cancelRequest := context.WithCancel(r.Context())
		defer cancelRequest()
		defer context.AfterFunc(reqCtx, cancelRequest)()

		exchange := NewExchange(requestCtx)

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
			LogError("HTTP consumer: error reading request body", "uri", c.uri, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		exchange.SetBody(body)
		exchange.SetHeader(CamelHttpMethod, r.Method)
		exchange.SetHeader(CamelHttpPath, r.URL.Path)
		exchange.SetHeader(CamelHttpQuery, r.URL.RawQuery)
		if r.URL.String() != "" {
			exchange.SetHeader(CamelHttpUrl, r.URL.String())
		}
		requestHeaders := make(map[string]string, len(r.Header))
		var credentials map[[sha256.Size]byte]struct{}
		for key, values := range r.Header {
			// A client must not be able to set internal Camel* headers
			// (e.g. CamelFileName, CamelSqlQuery) on the exchange.
			if len(values) == 0 || IsCamelHeader(key) {
				continue
			}
			exchange.SetHeader(key, values[0])
			requestHeaders[key] = values[0]
			if authorizationHeaders[key] {
				if credentials == nil {
					credentials = make(map[[sha256.Size]byte]struct{})
				}
				credentials[credentialFingerprint(key, values[0])] = struct{}{}
			}
		}
		if credentials != nil {
			exchange.SetProperty(httpInboundCredentialsProperty, credentials)
		}

		if err := CompleteExchange(exchange, ProcessSafely(c.processor, exchange)); err != nil {
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
			// Internal Camel* headers (request path and query, file names...)
			// are routing metadata, never response headers: echoing them
			// leaked query-string secrets and internal paths. The status code
			// headers drive WriteHeader below.
			if isHopByHopHeader(key) || IsCamelHeader(key) || key == "Status-Code" {
				continue
			}
			values, ok := headerValues(value)
			if !ok {
				continue
			}
			// Compare canonically: SetHeader does not canonicalize keys, so a
			// route that re-sets a header under a non-canonical key
			// (e.g. "authorization" vs the "Authorization" recorded in
			// requestHeaders) must still be recognized as unchanged rather than
			// bypassing the echo guard and leaking the credential.
			//
			// Content-Type is exempt: it describes the response body, and a
			// route answering JSON to a JSON request sets it to the very value
			// the request carried. Dropping it there made net/http sniff the
			// body and answer "text/plain".
			canonical := http.CanonicalHeaderKey(key)
			if prev, echoed := requestHeaders[canonical]; echoed && canonical != "Content-Type" && len(values) == 1 && prev == values[0] {
				continue
			}
			for i, v := range values {
				if err := addSafeHTTPHeader(w.Header(), key, v, i == 0); err != nil {
					LogError("HTTP consumer: invalid response header", "uri", c.uri, "error", err)
					http.Error(w, "internal server error", http.StatusInternalServerError)
					return
				}
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
	}

	// The middleware wraps this consumer's handler: it runs for the requests
	// routed to it, not for the paths of other consumers sharing the listener.
	var handler http.Handler = http.HandlerFunc(handle)
	if c.middleware != nil {
		handler = c.middleware(handler)
	}

	c.mu.Lock()
	if c.servers == nil {
		c.servers = newHTTPServers()
	}
	servers := c.servers
	// Published before the handler is registered: the listener may already be
	// serving other consumers, and a request must not find this one "stopped".
	c.running = true
	c.cancel = cancel
	c.mu.Unlock()

	shared, err := servers.acquire(addr, port != "0", pattern, handler)
	if err != nil {
		cancel()
		c.mu.Lock()
		c.running = false
		c.cancel = nil
		c.mu.Unlock()
		return err
	}

	c.mu.Lock()
	c.shared = shared
	c.pattern = pattern
	c.server = shared.server
	c.mu.Unlock()
	return nil
}

// Stop stops the HTTP consumer. Safe to call multiple times: Stop blocks until
// the Serve goroutine and all active handlers have fully returned to avoid
// leaking goroutines past CamelContext.Stop(). A Stop racing a Start waits for
// the server to be fully started and then shuts it down.
func (c *HTTPConsumer) Stop() error {
	c.acquireTransition()
	defer c.releaseTransition()

	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return nil
	}
	c.stopping = true
	shared := c.shared
	pattern := c.pattern
	servers := c.servers
	cancel := c.cancel
	c.mu.Unlock()

	// Stop routing new requests here, give the in-flight ones the same grace
	// period http.Server.Shutdown would, then cancel them.
	_ = shared.setHandler(pattern, nil)
	handlersDone := make(chan struct{})
	go func() {
		c.handlerWG.Wait()
		close(handlersDone)
	}()
	grace := time.NewTimer(httpShutdownTimeout)
	select {
	case <-handlersDone:
	case <-grace.C:
	}
	grace.Stop()
	if cancel != nil {
		cancel()
	}
	<-handlersDone

	// The listener closes with its last consumer.
	err := servers.release(shared)

	c.mu.Lock()
	c.shared = nil
	c.server = nil
	c.cancel = nil
	c.running = false
	c.stopping = false
	c.mu.Unlock()
	return err
}

// httpShutdownTimeout bounds how long a stopping HTTP consumer waits for its
// in-flight requests, and a closing listener for its connections.
const httpShutdownTimeout = 30 * time.Second

// httpServers owns the listeners of an HTTPComponent. Consumers whose URIs
// share a host:port share one listener and are told apart by their path, so
// several routes can be exposed on the same port:
//
//	From("http://0.0.0.0:8080/orders")
//	From("http://0.0.0.0:8080/health")
type httpServers struct {
	mu      sync.Mutex                   // Protects servers and every sharedHTTPServer.refs.
	servers map[string]*sharedHTTPServer // keyed by host:port
}

func newHTTPServers() *httpServers {
	return &httpServers{servers: make(map[string]*sharedHTTPServer)}
}

// sharedHTTPServer is one listener and the handlers registered on it.
type sharedHTTPServer struct {
	addr   string
	server *http.Server
	served chan struct{} // closed when Serve has returned
	refs   int           // consumers registered; guarded by httpServers.mu

	mu       sync.RWMutex            // Protects handlers and mux.
	handlers map[string]http.Handler // by path pattern
	mux      *http.ServeMux          // built from handlers; replaced, never mutated
}

// ServeHTTP dispatches to the consumer registered for the request path.
func (s *sharedHTTPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	mux := s.mux
	s.mu.RUnlock()
	mux.ServeHTTP(w, r)
}

// setHandler registers handler for pattern, or removes the pattern when handler
// is nil. http.ServeMux cannot unregister a pattern, so a new mux is built from
// the handler set and swapped in.
func (s *sharedHTTPServer) setHandler(pattern string, handler http.Handler) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.handlers[pattern]; exists && handler != nil {
		return fmt.Errorf("http consumer: path %q is already served on %s", pattern, s.addr)
	}
	handlers := make(map[string]http.Handler, len(s.handlers)+1)
	for p, h := range s.handlers {
		handlers[p] = h
	}
	if handler != nil {
		handlers[pattern] = handler
	} else {
		delete(handlers, pattern)
	}
	mux, err := buildServeMux(handlers)
	if err != nil {
		return fmt.Errorf("http consumer: cannot serve path %q on %s: %w", pattern, s.addr, err)
	}
	s.handlers, s.mux = handlers, mux
	return nil
}

// buildServeMux registers handlers on a new mux. ServeMux panics on an invalid
// or conflicting pattern; that is reported as an error.
func buildServeMux(handlers map[string]http.Handler) (mux *http.ServeMux, err error) {
	defer func() {
		if r := recover(); r != nil {
			mux, err = nil, fmt.Errorf("invalid path pattern: %v", r)
		}
	}()
	mux = http.NewServeMux()
	for pattern, handler := range handlers {
		mux.Handle(pattern, handler)
	}
	return mux, nil
}

// acquire registers handler for pattern on the listener of addr, opening it if
// this is its first consumer. shareable is false for an ephemeral port (":0"),
// which designates a different listener on every call.
func (hs *httpServers) acquire(addr string, shareable bool, pattern string, handler http.Handler) (*sharedHTTPServer, error) {
	hs.mu.Lock()
	defer hs.mu.Unlock()

	if shareable {
		if s, ok := hs.servers[addr]; ok {
			if err := s.setHandler(pattern, handler); err != nil {
				return nil, err
			}
			s.refs++
			return s, nil
		}
	}

	// The listener is created synchronously so bind failures are returned
	// from the consumer's Start.
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("error listening on %s: %w", addr, err)
	}
	s := &sharedHTTPServer{addr: addr, served: make(chan struct{}), refs: 1}
	if err := s.setHandler(pattern, handler); err != nil {
		_ = listener.Close()
		return nil, err
	}
	s.server = &http.Server{
		Addr:    addr,
		Handler: s,
		// Bound the time a client may take to send its headers (slowloris).
		ReadHeaderTimeout: 10 * time.Second,
		// Bound the total time to read the whole request (headers + body) and
		// the idle time of a keep-alive connection, so a client that sends its
		// headers promptly then trickles the body cannot pin a handler
		// goroutine (and its MaxBytesReader) open indefinitely (slow body DoS).
		ReadTimeout: 60 * time.Second,
		IdleTimeout: 60 * time.Second,
	}
	if shareable {
		hs.servers[addr] = s
	}
	go func() {
		defer close(s.served)
		if err := s.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			LogError("HTTP server error", "addr", addr, "error", err)
		}
	}()
	return s, nil
}

// release drops one consumer's reference and, for the last one, shuts the
// listener down and waits for Serve to return. The registry stays locked
// meanwhile so a consumer starting on the same address waits for the port to
// be free instead of failing to bind; by then the consumer's own requests are
// over, so only idle connections remain.
func (hs *httpServers) release(s *sharedHTTPServer) error {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	s.refs--
	if s.refs > 0 {
		return nil
	}
	if hs.servers[s.addr] == s {
		delete(hs.servers, s.addr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
	defer cancel()
	err := s.server.Shutdown(ctx)
	if err != nil {
		_ = s.server.Close()
	}
	<-s.served
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

// addSafeHTTPHeader sets an HTTP header after rejecting CRLF in the key or
// value. CRLF in either field allows an attacker to inject arbitrary headers
// or split the HTTP response (CWE-113, HTTP Response Splitting).
//
// For multi-valued headers, replace sets the first value and later values are
// appended.
func addSafeHTTPHeader(h http.Header, key, value string, replace bool) error {
	if strings.ContainsAny(key, "\r\n") {
		return fmt.Errorf("header name contains CRLF: %q", key)
	}
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("header %q value contains CRLF", key)
	}
	if replace {
		h.Set(key, value)
	} else {
		h.Add(key, value)
	}
	return nil
}
