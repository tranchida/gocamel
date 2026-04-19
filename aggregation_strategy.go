package gocamel

// AggregationStrategy defines the strategy for aggregating multiple messages (Exchanges) into one.
type AggregationStrategy interface {
	// Aggregate merges the old exchange with the new one.
	// If oldExchange is nil, it means this is the first exchange for this aggregation key.
	// Returns the merged exchange (often oldExchange after modification, or newExchange on first call).
	Aggregate(oldExchange *Exchange, newExchange *Exchange) *Exchange
}
