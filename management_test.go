package gocamel

import (
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
