package gocamel

import (
	"bytes"
	"context"
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

// HTTPComponent represents the HTTP component.
//
// The optional consumer-side Middleware is applied to every incoming request
// handler created by HTTPConsumer.Start(). It is intended for authentication,
// TLS termination, rate limiting, or request logging — features the HTTP
// consumer does not provide out of the box.
type HTTPComponent struct {
	client     *http.Client
	middleware func(http.Handler) http.Handler
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
	}
}

// SetMiddleware installs an http.Handler middleware applied to every consumer
// handler. Use it to plug in authentication, TLS, rate limiting, etc.
// The HTTP consumer ships with NO authentication: hosts exposed to untrusted
// networks must set a middleware (e.g. token check, mTLS, OAuth) before Start().
func (c *HTTPComponent) SetMiddleware(mw func(http.Handler) http.Handler) {
	c.middleware = mw
}

// CreateEndpoint creates a new HTTP endpoint
func (c *HTTPComponent) CreateEndpoint(uri string) (Endpoint, error) {
	if _, err := ParseURI(uri); err != nil {
		return nil, fmt.Errorf("invalid HTTP URI: %v", err)
	}

	return &HTTPEndpoint{
		uri:        uri,
		client:     c.client,
		middleware: c.middleware,
	}, nil
}

// HTTPEndpoint represents an HTTP endpoint
type HTTPEndpoint struct {
	uri        string
	client     *http.Client
	middleware func(http.Handler) http.Handler
}

// URI returns the endpoint URI
func (e *HTTPEndpoint) URI() string {
	return e.uri
}

// CreateProducer creates an HTTP producer
func (e *HTTPEndpoint) CreateProducer() (Producer, error) {
	return &HTTPProducer{
		uri:    e.uri,
		client: e.client,
	}, nil
}

// CreateConsumer creates an HTTP consumer
func (e *HTTPEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return &HTTPConsumer{
		uri:        e.uri,
		processor:  processor,
		middleware: e.middleware,
	}, nil
}

// HTTPProducer represents an HTTP producer
type HTTPProducer struct {
	uri    string
	client *http.Client
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

	req, err := http.NewRequest("POST", p.uri, bodyReader)
	if err != nil {
		return fmt.Errorf("error creating HTTP request: %v", err)
	}

	// Adding headers (CRLF-stripped to prevent header injection — CWE-113)
	for key, value := range exchange.GetIn().GetHeaders() {
		if strValue, ok := value.(string); ok {
			if err := setSafeHTTPHeader(req.Header, key, strValue); err != nil {
				return fmt.Errorf("error setting HTTP request header: %w", err)
			}
		}
	}

	// Sending the request
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("error sending HTTP request: %v", err)
	}
	defer resp.Body.Close()

	// Reading the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading HTTP response: %v", err)
	}

	// Updating the exchange with the response
	exchange.GetOut().SetBody(body)
	for key, value := range resp.Header {
		exchange.GetOut().SetHeader(key, value[0])
	}
	exchange.GetOut().SetHeader("Status-Code", strconv.Itoa(resp.StatusCode))

	return nil
}

// HTTPConsumer represents an HTTP consumer
type HTTPConsumer struct {
	uri        string
	processor  Processor
	middleware func(http.Handler) http.Handler
	server     *http.Server
	wg         sync.WaitGroup
	stopOnce   sync.Once
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

	mux := http.NewServeMux()
	mux.HandleFunc(parsedURL.Path, func(w http.ResponseWriter, r *http.Request) {
		exchange := NewExchange(r.Context())

		// Reading the request body
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Configuring the exchange
		exchange.SetBody(body)
		for key, values := range r.Header {
			if len(values) > 0 {
				exchange.SetHeader(key, values[0])
			}
		}

		// Processing the message
		if err := CompleteExchange(exchange, c.processor.Process(exchange)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Response: Out if defined, otherwise In (InOut Apache Camel behavior)
		response := exchange.GetResponse()

		// Set response headers (CRLF-stripped to prevent header injection — CWE-113)
		for key, value := range response.GetHeaders() {
			if strValue, ok := value.(string); ok {
				if err := setSafeHTTPHeader(w.Header(), key, strValue); err != nil {
					http.Error(w, "invalid response header", http.StatusInternalServerError)
					return
				}
			}
		}

		switch body := response.GetBody().(type) {
		case []byte:
			w.Write(body)
		case string:
			w.Write([]byte(body))
		case nil:
			// no body
		default:
			// Fall back to the string representation rather than silently
			// returning an empty response for non-string bodies.
			fmt.Fprintf(w, "%v", body)
		}
	})

	var handler http.Handler = mux
	if c.middleware != nil {
		handler = c.middleware(mux)
	}

	c.server = &http.Server{
		Addr:    ":" + port,
		Handler: handler,
		// Bound the time a client may take to send its headers (slowloris).
		ReadHeaderTimeout: 10 * time.Second,
		// BaseContext ties every inbound request to the consumer's parent ctx
		// so that CamelContext.Stop() cancels in-flight requests.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	// Starting the server. The WaitGroup is signalled when ListenAndServe
	// returns, so Stop() can wait for the goroutine to fully unwind before
	// the CamelContext continues its shutdown.
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		if err := c.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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
