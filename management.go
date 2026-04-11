package gocamel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ManagementServer expose une API REST pour monitorer et contrôler les routes
type ManagementServer struct {
	context *CamelContext
	server  *http.Server
}

// NewManagementServer crée une nouvelle instance de ManagementServer
func NewManagementServer(context *CamelContext) *ManagementServer {
	return &ManagementServer{
		context: context,
	}
}

// RouteInfo représente les informations publiques d'une route pour l'API REST
type RouteInfo struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
	Group       string `json:"group,omitempty"`
	Started     bool   `json:"started"`
}

// ContextInfo représente les informations du contexte pour l'API REST
type ContextInfo struct {
	Started          bool `json:"started"`
	TotalRoutes      int  `json:"totalRoutes"`
	StartedRoutes    int  `json:"startedRoutes"`
}

// Start démarre le serveur REST de management sur l'adresse spécifiée (ex: ":8081")
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
			fmt.Printf("Erreur du serveur de management REST: %v\n", err)
		}
	}()

	return nil
}

// Stop arrête le serveur REST de management
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
	// Attend un chemin de la forme /api/routes/{id}/start ou /api/routes/{id}/stop
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

	case "stop":
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

	default:
		http.Error(w, "Unknown action", http.StatusBadRequest)
	}
}
