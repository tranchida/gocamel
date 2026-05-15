package gocamel

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestCamelContext_GracefulStop_NotStarted verifies that GracefulStop is a
// no-op on a context that was never started.
func TestCamelContext_GracefulStop_NotStarted(t *testing.T) {
	ctx := NewCamelContext()
	require.NoError(t, ctx.GracefulStop(time.Second))
}

// TestCamelContext_GracefulStop_Empty verifies that GracefulStop on a started
// context with no routes returns immediately.
func TestCamelContext_GracefulStop_Empty(t *testing.T) {
	ctx := NewCamelContext()
	require.NoError(t, ctx.Start())
	require.NoError(t, ctx.GracefulStop(time.Second))
	assert.False(t, ctx.IsStarted())
}

// TestRoute_Validate_NoFrom surfaces missing source endpoint at validate time.
func TestRoute_Validate_NoFrom(t *testing.T) {
	ctx := NewCamelContext()
	r := ctx.CreateRoute()
	err := r.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no source endpoint")
}

// TestRoute_Validate_BadChoiceExpression surfaces a malformed Simple
// expression in a Choice when-clause at validate time instead of waiting
// for the first matching message.
func TestRoute_Validate_BadChoiceExpression(t *testing.T) {
	ctx := NewCamelContext()
	ctx.AddComponent("direct", NewDirectComponent())

	r := ctx.CreateRoute().From("direct:start")
	// Force the unparseable template by injecting an oversized expression
	// that ParseSimpleTemplate rejects via the MaxSimpleInputBytes guard.
	oversize := "${header." + strings.Repeat("a", MaxSimpleInputBytes+1) + "}"
	choice := NewChoiceProcessor().When(oversize, ProcessorFunc(func(*Exchange) error { return nil }))
	r.AddProcessor(choice)

	err := r.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "choice when")
}
