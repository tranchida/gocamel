package gocamel

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ManagementServer exposes a REST API for monitoring and controlling routes.
//
// SECURITY: the API allows starting or stopping any route by ID. Every request
// goes through these checks:
//   - Host: only IP literals, "localhost" and the names given to
//     SetAllowedHosts are accepted, which defeats DNS rebinding (a web page
//     resolving its own domain to 127.0.0.1 to reach a loopback server);
//   - cross-origin POSTs from a browser are rejected
//     (http.CrossOriginProtection), which defeats CSRF;
//   - with SetAuthToken, an "Authorization: Bearer <token>" header is
//     required.
//
// Without a token the API is unauthenticated: bind it to a loopback address
// (e.g. "127.0.0.1:8081") or put it behind a reverse proxy enforcing
// authentication before exposing it beyond localhost.
type ManagementServer struct {
	context      *CamelContext
	authToken    string   // set before Start; empty = no authentication
	allowedHosts []string // set before Start; extra accepted Host names
	server       *http.Server
	listener     net.Listener
	wg           sync.WaitGroup
	stopOnce     sync.Once
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

// SetAuthToken requires every request to carry "Authorization: Bearer <token>".
// It must be called before Start; an empty token disables authentication.
func (m *ManagementServer) SetAuthToken(token string) *ManagementServer {
	m.authToken = token
	return m
}

// SetAllowedHosts adds host names (without port) accepted in the Host header,
// e.g. the name of a reverse proxy in front of the API. IP literals and
// "localhost" are always accepted. It must be called before Start.
func (m *ManagementServer) SetAllowedHosts(hosts ...string) *ManagementServer {
	m.allowedHosts = append(m.allowedHosts, hosts...)
	return m
}

// Start starts the management REST server on the given address (e.g.
// "127.0.0.1:8081"). It returns an error when the address cannot be bound.
func (m *ManagementServer) Start(addr string) error {
	if m.server != nil {
		return errors.New("management server: already started")
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("management server: error listening on %s: %w", addr, err)
	}
	m.listener = listener
	m.server = &http.Server{
		Handler: m.handler(),
		// Bound the time a client may take to send its headers (slowloris).
		ReadHeaderTimeout: 10 * time.Second,
	}

	// The WaitGroup is signalled when Serve returns, so Stop() can wait for
	// the goroutine to fully unwind before returning — mirroring the HTTP
	// consumer shutdown pattern.
	m.wg.Go(func() {
		if err := m.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			LogError("Management REST server error", "error", err)
		}
	})
	return nil
}

// Addr returns the address the server listens on (useful with port 0), or
// nil before Start.
func (m *ManagementServer) Addr() net.Addr {
	if m.listener == nil {
		return nil
	}
	return m.listener.Addr()
}

// handler returns the API routes wrapped in the security checks described on
// ManagementServer.
func (m *ManagementServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/context", m.handleContext)
	mux.HandleFunc("/api/routes", m.handleRoutes)
	mux.HandleFunc("/api/routes/", m.handleRouteAction)

	csrf := http.NewCrossOriginProtection()
	protected := csrf.Handler(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !m.isAllowedHost(r.Host) {
			http.Error(w, "Forbidden host", http.StatusForbidden)
			return
		}
		if m.authToken != "" && !m.isAuthorized(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="gocamel-management"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		protected.ServeHTTP(w, r)
	})
}

// isAllowedHost reports whether the Host header names this server rather than
// a domain rebound to it.
func (m *ManagementServer) isAllowedHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.Trim(host, "[]"), ".")
	if host == "" {
		return false
	}
	if net.ParseIP(host) != nil || strings.EqualFold(host, "localhost") {
		return true
	}
	for _, allowed := range m.allowedHosts {
		if strings.EqualFold(host, allowed) {
			return true
		}
	}
	return false
}

func (m *ManagementServer) isAuthorized(r *http.Request) bool {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && subtle.ConstantTimeCompare([]byte(token), []byte(m.authToken)) == 1
}

// Stop stops the management REST server. Safe to call multiple times: server
// shutdown is invoked at most once via sync.Once and Stop blocks until the
// ListenAndServe goroutine has fully returned (no leaked goroutine). Shutdown
// is graceful: in-flight requests are given up to 10s to complete before the
// connections are closed.
func (m *ManagementServer) Stop() error {
	if m.server == nil {
		return nil
	}
	var err error
	m.stopOnce.Do(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err = m.server.Shutdown(shutdownCtx)
	})
	m.wg.Wait()
	return err
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
	// A route started while the context is stopped would never be stopped:
	// CamelContext.Stop returns early when the context is not started.
	if !m.context.IsStarted() {
		http.Error(w, "Camel context not started", http.StatusConflict)
		return
	}
	if err := route.Start(m.context.GetContext()); err != nil {
		// Details stay in the logs: they may expose hosts or credentials.
		LogError("Management API: failed to start route", "route", route.ID, "error", err)
		http.Error(w, "Failed to start route", http.StatusInternalServerError)
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
		LogError("Management API: failed to stop route", "route", route.ID, "error", err)
		http.Error(w, "Failed to stop route", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"stopped"}`))
}
