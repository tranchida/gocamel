package gocamel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ManagementServer exposes a REST API for monitoring and controlling routes.
type ManagementServer struct {
	context *CamelContext
	server  *http.Server
}

// NewManagementServer creates a new ManagementServer instance.
func NewManagementServer(context *CamelContext) *ManagementServer {
	return &ManagementServer{
		context: context,
	}
}

// RouteInfo represents the public information about a route for the REST API.
type RouteInfo struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
	Group       string `json:"group,omitempty"`
	Started     bool   `json:"started"`
}

// ContextInfo represents the context information for the REST API.
type ContextInfo struct {
	Started       bool `json:"started"`
	TotalRoutes   int  `json:"totalRoutes"`
	StartedRoutes int  `json:"startedRoutes"`
}

// Start starts the management REST server on the given address (e.g. ":8081").
func (m *ManagementServer) Start(addr string) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/context", m.handleContext)
	mux.HandleFunc("/api/routes", m.handleRoutes)
	mux.HandleFunc("/api/routes/", m.handleRouteAction)

	m.server = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		if err := m.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			LogError("Management REST server error", "error", err)
		}
	}()

	return nil
}

// Stop stops the management REST server.
func (m *ManagementServer) Stop() error {
	if m.server != nil {
		return m.server.Close()
	}
	return nil
}

func (m *ManagementServer) handleContext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	info := ContextInfo{
		Started:       m.context.IsStarted(),
		TotalRoutes:   m.context.GetRouteCount(),
		StartedRoutes: m.context.GetStartedRouteCount(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

func (m *ManagementServer) handleRoutes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	routes := m.context.GetRoutes()
	routesInfo := make([]RouteInfo, 0, len(routes))

	for _, route := range routes {
		routesInfo = append(routesInfo, RouteInfo{
			ID:          route.ID,
			Description: route.Description,
			Group:       route.Group,
			Started:     route.IsStarted(),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(routesInfo)
}

func (m *ManagementServer) handleRouteAction(w http.ResponseWriter, r *http.Request) {
	// Expects a path of the form /api/routes/{id}/start or /api/routes/{id}/stop
	path := strings.TrimPrefix(r.URL.Path, "/api/routes/")
	parts := strings.Split(path, "/")

	if len(parts) != 2 {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	routeID := parts[0]
	action := parts[1]

	route := m.context.GetRoute(routeID)
	if route == nil {
		http.Error(w, "Route not found", http.StatusNotFound)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	switch action {
	case "start":
		m.handleStartAction(w, route)

	case "stop":
		m.handleStopAction(w, route)

	default:
		http.Error(w, "Unknown action", http.StatusBadRequest)
	}
}

func (m *ManagementServer) handleStartAction(w http.ResponseWriter, route *Route) {
	if route.IsStarted() {
		http.Error(w, "Route already started", http.StatusBadRequest)
		return
	}
	if err := route.Start(m.context.GetContext()); err != nil {
		http.Error(w, fmt.Sprintf("Failed to start route: %v", err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"started"}`))
}

func (m *ManagementServer) handleStopAction(w http.ResponseWriter, route *Route) {
	if !route.IsStarted() {
		http.Error(w, "Route not started", http.StatusBadRequest)
		return
	}
	if err := route.Stop(); err != nil {
		http.Error(w, fmt.Sprintf("Failed to stop route: %v", err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"stopped"}`))
}
