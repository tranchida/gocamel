package gocamel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// HTTPComponent represents the HTTP component
type HTTPComponent struct {
	client *http.Client
}

// NewHTTPComponent creates a new HTTPComponent instance
func NewHTTPComponent() *HTTPComponent {
	return &HTTPComponent{
		client: &http.Client{},
	}
}

// CreateEndpoint creates a new HTTP endpoint
func (c *HTTPComponent) CreateEndpoint(uri string) (Endpoint, error) {
	if _, err := url.Parse(uri); err != nil {
		return nil, fmt.Errorf("invalid HTTP URI: %v", err)
	}

	return &HTTPEndpoint{
		uri:    uri,
		client: c.client,
	}, nil
}

// HTTPEndpoint represents an HTTP endpoint
type HTTPEndpoint struct {
	uri    string
	client *http.Client
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
		uri:       e.uri,
		processor: processor,
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

	// Adding headers
	for key, value := range exchange.GetIn().GetHeaders() {
		if strValue, ok := value.(string); ok {
			req.Header.Set(key, strValue)
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
	uri       string
	processor Processor
	server    *http.Server
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
		exchange := NewExchange(ctx)

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
		if err := c.processor.Process(exchange); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Response: Out if defined, otherwise In (InOut Apache Camel behavior)
		response := exchange.GetResponse()

		for key, value := range response.GetHeaders() {
			if strValue, ok := value.(string); ok {
				w.Header().Set(key, strValue)
			}
		}

		switch body := response.GetBody().(type) {
		case []byte:
			w.Write(body)
		case string:
			w.Write([]byte(body))
		}
	})

	c.server = &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	// Starting the server
	go func() {
		if err := c.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("HTTP server error: %v\n", err)
		}
	}()

	return nil
}

// Stop stops the HTTP consumer
func (c *HTTPConsumer) Stop() error {
	if c.server != nil {
		return c.server.Shutdown(context.Background())
	}
	return nil
}
