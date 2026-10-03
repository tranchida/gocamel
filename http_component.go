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
	}, nil
}

// HTTPEndpoint represents an HTTP endpoint
type HTTPEndpoint struct {
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
	server      *http.Server
	wg          sync.WaitGroup
	handlerWG   sync.WaitGroup
	cancel      context.CancelFunc
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
	ctx, c.cancel = context.WithCancel(ctx)
	// http.ServeMux panics on an empty pattern, so a URI without a path
	// ("http://host:8080") must still register a valid route.
	pattern := parsedURL.Path
	if pattern == "" {
		pattern = "/"
	}

	mux := http.NewServeMux()
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		c.handlerWG.Add(1)
		defer c.handlerWG.Done()

		exchange := NewExchange(r.Context())

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
			if prev, echoed := requestHeaders[http.CanonicalHeaderKey(key)]; echoed && len(values) == 1 && prev == values[0] {
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
		// Bound the total time to read the whole request (headers + body) and
		// the idle time of a keep-alive connection, so a client that sends its
		// headers promptly then trickles the body cannot pin a handler
		// goroutine (and its MaxBytesReader) open indefinitely (slow body DoS).
		ReadTimeout: 60 * time.Second,
		IdleTimeout: 60 * time.Second,
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
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err = c.server.Shutdown(shutdownCtx)
		if c.cancel != nil {
			c.cancel()
		}
	})
	c.handlerWG.Wait()
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
