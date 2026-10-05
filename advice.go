package gocamel

import "fmt"

// AdviceWith substitutes endpoint URIs in a built route, typically to replace
// real endpoints with mock: endpoints in tests without rebuilding the route:
//
//	route, _ := builder.From("direct:in").To("kafka:orders").Build()
//	_ = route.AdviceWith(map[string]string{"kafka:orders": "mock:orders"})
//	_ = ctx.Start()
//
// Keys must match the URI exactly as written in a static To() — including in
// multicast To(a, b), Choice/LoadBalancer/Saga branches — since those
// producers are resolved lazily at Start. Not substituted:
//   - the From() endpoint (already resolved at build time),
//   - ToD templates and other dynamic sends resolved per exchange (RecipientList,
//     RoutingSlip, DynamicRouter, WireTap, Enrich, DeadLetterChannel).
//
// Keys that match no producer are ignored without error. Substitution is a
// single, non-transitive pass ({a: b, b: a} swaps the two), so the map itself
// cannot create a cycle; whether the replacement endpoint routes back into
// the route (e.g. a direct: loop) is NOT checked.
//
// AdviceWith must be called before Start (or after Stop) and not concurrently
// with Start; it returns an error if the route is started or changing
// lifecycle state, leaving the route unchanged.
func (r *Route) AdviceWith(substitutions map[string]string) error {
	r.startLock.Lock()
	defer r.startLock.Unlock()
	if r.started || r.starting || r.stopping {
		return fmt.Errorf("route %s: advice must be applied before Start", r.ID)
	}
	for _, lp := range r.lazyProducers {
		lp.mu.Lock()
		if replacement, ok := substitutions[lp.uri]; ok {
			lp.uri = replacement
		}
		lp.mu.Unlock()
	}
	return nil
}
