package gocamel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagementServer_AuthTokenTable(t *testing.T) {
	tests := []struct {
		name          string
		authorization string
		wantStatus    int
	}{
		{"missing header", "", http.StatusUnauthorized},
		{"wrong token", "Bearer wrong", http.StatusUnauthorized},
		{"empty bearer token", "Bearer ", http.StatusUnauthorized},
		{"token prefix only", "Bearer s3cr", http.StatusUnauthorized},
		{"token with suffix", "Bearer s3cret-extra", http.StatusUnauthorized},
		{"basic scheme", "Basic czNjcmV0", http.StatusUnauthorized},
		{"raw token without scheme", "s3cret", http.StatusUnauthorized},
		{"valid token", "Bearer s3cret", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, route := newManagedContext(t)
			h := NewManagementServer(route.context).SetAuthToken("s3cret").handler()

			headers := map[string]string{}
			if tt.authorization != "" {
				headers["Authorization"] = tt.authorization
			}
			w := managementRequest(h, http.MethodPost, "/api/routes/managed/stop", headers)

			assert.Equal(t, tt.wantStatus, w.Code)
			if tt.wantStatus == http.StatusUnauthorized {
				assert.Equal(t, `Bearer realm="gocamel-management"`, w.Header().Get("WWW-Authenticate"))
				assert.True(t, route.IsStarted(), "an unauthorized request must not stop the route")
			} else {
				assert.False(t, route.IsStarted())
			}
		})
	}
}

func TestManagementServer_NoAuthTokenAllowsAnonymous(t *testing.T) {
	_, route := newManagedContext(t)
	h := NewManagementServer(route.context).handler()

	w := managementRequest(h, http.MethodGet, "/api/context", nil)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Header().Get("WWW-Authenticate"))
}

func TestManagementServer_HostCheckTable(t *testing.T) {
	tests := []struct {
		name       string
		host       string
		wantStatus int
	}{
		{"ipv4 literal with port", "127.0.0.1:8081", http.StatusOK},
		{"ipv6 literal with port", "[::1]:8081", http.StatusOK},
		{"localhost without port", "localhost", http.StatusOK},
		{"localhost with trailing dot", "localhost.:8081", http.StatusOK},
		{"localhost mixed case", "LocalHost:8081", http.StatusOK},
		{"allowed host", "mgmt.internal:8081", http.StatusOK},
		{"empty host", "", http.StatusForbidden},
		{"foreign domain", "attacker.example", http.StatusForbidden},
		{"localhost as subdomain prefix", "localhost.attacker.example:8081", http.StatusForbidden},
		{"allowed host as suffix", "evil-mgmt.internal", http.StatusForbidden},
		{"ip-looking domain", "127.0.0.1.attacker.example", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, route := newManagedContext(t)
			h := NewManagementServer(route.context).SetAllowedHosts("mgmt.internal").handler()

			w := managementRequest(h, http.MethodGet, "/api/routes", map[string]string{"Host": tt.host})
			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}
}

// The Host check runs before authentication, so a rebound domain is rejected
// even when the attacker somehow holds a valid token.
func TestManagementServer_HostCheckPrecedesAuth(t *testing.T) {
	_, route := newManagedContext(t)
	h := NewManagementServer(route.context).SetAuthToken("s3cret").handler()

	w := managementRequest(h, http.MethodPost, "/api/routes/managed/stop", map[string]string{
		"Host":          "attacker.example",
		"Authorization": "Bearer s3cret",
	})
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Empty(t, w.Header().Get("WWW-Authenticate"))
	assert.True(t, route.IsStarted())
}

func TestManagementServer_CrossOriginTable(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		target     string
		headers    map[string]string
		wantStatus int
	}{
		{"cross-site POST", http.MethodPost, "/api/routes/managed/stop",
			map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"cross-origin POST by Origin only", http.MethodPost, "/api/routes/managed/stop",
			map[string]string{"Origin": "https://attacker.example"}, http.StatusForbidden},
		{"same-origin POST", http.MethodPost, "/api/routes/managed/stop",
			map[string]string{"Sec-Fetch-Site": "same-origin"}, http.StatusOK},
		{"cross-site GET is a safe method", http.MethodGet, "/api/routes",
			map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://attacker.example"}, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, route := newManagedContext(t)
			h := NewManagementServer(route.context).handler()

			w := managementRequest(h, tt.method, tt.target, tt.headers)
			assert.Equal(t, tt.wantStatus, w.Code)
			if tt.method == http.MethodPost {
				assert.Equal(t, tt.wantStatus != http.StatusOK, route.IsStarted())
			}
		})
	}
}

// End-to-end over a real listener: the token check applies to the served handler.
func TestManagementServer_AuthTokenOverNetwork(t *testing.T) {
	m := NewManagementServer(NewCamelContext()).SetAuthToken("s3cret")
	require.NoError(t, m.Start("127.0.0.1:0"))
	t.Cleanup(func() { _ = m.Stop() })
	url := "http://" + m.Addr().String() + "/api/context"

	resp, err := http.Get(url)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer s3cret")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

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
	if err := ctx.Start(); err != nil {
		t.Fatalf("Failed to start context: %v", err)
	}
	defer ctx.Stop()

	// Test Stop
	req := httptest.NewRequest(http.MethodPost, "/api/routes/test-route/stop", nil)
	w := httptest.NewRecorder()
	mgmt.handleRouteAction(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status OK for stop, got %v: %s", w.Code, w.Body.String())
	}

	if route.IsStarted() {
		t.Errorf("Expected route to be stopped")
	}

	// Test Start
	req = httptest.NewRequest(http.MethodPost, "/api/routes/test-route/start", nil)
	w = httptest.NewRecorder()
	mgmt.handleRouteAction(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status OK for start, got %v: %s", w.Code, w.Body.String())
	}

	if !route.IsStarted() {
		t.Errorf("Expected route to be started")
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
