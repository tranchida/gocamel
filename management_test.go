package gocamel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestManagementServer_ContextInfo(t *testing.T) {
	ctx := NewCamelContext()
	mgmt := NewManagementServer(ctx)

	req := httptest.NewRequest(http.MethodGet, "/api/context", nil)
	w := httptest.NewRecorder()

	mgmt.handleContext(w, req)

	res := w.Result()
	if res.StatusCode != http.StatusOK {
		t.Errorf("Expected status OK, got %v", res.StatusCode)
	}

	var info ContextInfo
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil {
		t.Errorf("Failed to decode response: %v", err)
	}

	if info.Started {
		t.Errorf("Expected context to not be started")
	}
}

func TestManagementServer_Routes(t *testing.T) {
	ctx := NewCamelContext()
	mgmt := NewManagementServer(ctx)

	// Add a dummy route
	route := ctx.CreateRoute()
	route.ID = "test-route-1"

	req := httptest.NewRequest(http.MethodGet, "/api/routes", nil)
	w := httptest.NewRecorder()

	mgmt.handleRoutes(w, req)

	res := w.Result()
	if res.StatusCode != http.StatusOK {
		t.Errorf("Expected status OK, got %v", res.StatusCode)
	}

	var routes []RouteInfo
	if err := json.NewDecoder(res.Body).Decode(&routes); err != nil {
		t.Errorf("Failed to decode response: %v", err)
	}

	if len(routes) != 1 || routes[0].ID != "test-route-1" {
		t.Errorf("Expected 1 route with ID test-route-1, got %v", routes)
	}
}

// MockEndpoint is a dummy endpoint for testing
type MockEndpoint struct {
	uri string
}

func (e *MockEndpoint) URI() string { return e.uri }
func (e *MockEndpoint) CreateProducer() (Producer, error) {
	return &MockProducer{}, nil
}
func (e *MockEndpoint) CreateConsumer(p Processor) (Consumer, error) {
	return &MockConsumer{}, nil
}

type MockProducer struct{}

func (p *MockProducer) Start(ctx context.Context) error { return nil }
func (p *MockProducer) Stop() error                     { return nil }
func (p *MockProducer) Send(e *Exchange) error          { return nil }

type MockConsumer struct{}

func (c *MockConsumer) Start(ctx context.Context) error { return nil }
func (c *MockConsumer) Stop() error                     { return nil }

type MockComponent struct{}

func (c *MockComponent) CreateEndpoint(uri string) (Endpoint, error) {
	return &MockEndpoint{uri: uri}, nil
}

func TestManagementServer_RouteAction(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("mock", &MockComponent{})
	mgmt := NewManagementServer(ctx)

	route := ctx.CreateRoute()
	route.ID = "test-route"
	route.From("mock:source")

	// Test Start
	req := httptest.NewRequest(http.MethodPost, "/api/routes/test-route/start", nil)
	w := httptest.NewRecorder()
	mgmt.handleRouteAction(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status OK for start, got %v: %s", w.Code, w.Body.String())
	}

	if !route.IsStarted() {
		t.Errorf("Expected route to be started")
	}

	// Test Stop
	req = httptest.NewRequest(http.MethodPost, "/api/routes/test-route/stop", nil)
	w = httptest.NewRecorder()
	mgmt.handleRouteAction(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status OK for stop, got %v: %s", w.Code, w.Body.String())
	}

	if route.IsStarted() {
		t.Errorf("Expected route to be stopped")
	}

	// Test Invalid Action
	req = httptest.NewRequest(http.MethodPost, "/api/routes/test-route/invalid", nil)
	w = httptest.NewRecorder()
	mgmt.handleRouteAction(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status BadRequest for invalid action, got %v", w.Code)
	}

	// Test Route Not Found
	req = httptest.NewRequest(http.MethodPost, "/api/routes/unknown/start", nil)
	w = httptest.NewRecorder()
	mgmt.handleRouteAction(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status NotFound for unknown route, got %v", w.Code)
	}
}
