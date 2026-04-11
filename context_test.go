package gocamel

import (
	"testing"
)

func TestDefaultRouteID(t *testing.T) {
	ctx := NewCamelContext()

	// Add a route without ID
	route1 := NewRoute()
	ctx.AddRoute(route1)
	if route1.ID != "route-1" {
		t.Errorf("Expected route-1, got %s", route1.ID)
	}

	// Add another route without ID
	route2 := NewRoute()
	ctx.AddRoute(route2)
	if route2.ID != "route-2" {
		t.Errorf("Expected route-2, got %s", route2.ID)
	}

	// Add a route with a custom ID
	routeCustom := NewRoute()
	routeCustom.ID = "my-custom-route"
	ctx.AddRoute(routeCustom)
	if routeCustom.ID != "my-custom-route" {
		t.Errorf("Expected my-custom-route, got %s", routeCustom.ID)
	}

	// Add another route without ID
	route3 := NewRoute()
	ctx.AddRoute(route3)
	if route3.ID != "route-3" {
		t.Errorf("Expected route-3, got %s", route3.ID)
	}
}

func TestDefaultRouteIDConflict(t *testing.T) {
	ctx := NewCamelContext()

	// Add a route with an ID that would normally be generated
	routeConflict := NewRoute()
	routeConflict.ID = "route-1"
	ctx.AddRoute(routeConflict)

	// Add a route without ID; should skip route-1 and use route-2
	routeNext := NewRoute()
	ctx.AddRoute(routeNext)
	if routeNext.ID != "route-2" {
		t.Errorf("Expected route-2 to avoid conflict, got %s", routeNext.ID)
	}
}
