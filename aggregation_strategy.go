package gocamel

// AggregationStrategy définit la stratégie pour agréger plusieurs messages (Exchanges) en un seul.
type AggregationStrategy interface {
	// Aggregate fusionne l'ancien échange avec le nouveau.
	// Si oldExchange est nil, cela signifie que c'est le premier échange pour cette clé d'agrégation.
	// Retourne l'échange fusionné (souvent oldExchange après modification, ou newExchange au premier appel).
	Aggregate(oldExchange *Exchange, newExchange *Exchange) *Exchange
}
