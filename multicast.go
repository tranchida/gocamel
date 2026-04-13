package gocamel

import (
	"errors"
	"fmt"
	"sync"
)

// Multicast est un Processor qui implémente l'EIP Multicast.
// Il envoie une copie du message à plusieurs destinations ou processeurs.
type Multicast struct {
	processors          []Processor
	AggregationStrategy AggregationStrategy
	ParallelProcessing  bool
}

// NewMulticast crée une nouvelle instance de Multicast
func NewMulticast() *Multicast {
	return &Multicast{
		processors: make([]Processor, 0),
	}
}

// AddProcessor ajoute un processeur (une branche) au multicast
func (m *Multicast) AddProcessor(processor Processor) {
	m.processors = append(m.processors, processor)
}

// SetAggregationStrategy définit la stratégie d'agrégation pour collecter les résultats
func (m *Multicast) SetAggregationStrategy(strategy AggregationStrategy) *Multicast {
	m.AggregationStrategy = strategy
	return m
}

// SetParallelProcessing active ou désactive le traitement parallèle
func (m *Multicast) SetParallelProcessing(parallel bool) *Multicast {
	m.ParallelProcessing = parallel
	return m
}

// Process exécute le multicast sur l'échange fourni
func (m *Multicast) Process(exchange *Exchange) error {
	if len(m.processors) == 0 {
		return nil
	}

	if m.ParallelProcessing {
		return m.processParallel(exchange)
	}

	return m.processSequential(exchange)
}

func (m *Multicast) processSequential(exchange *Exchange) error {
	var aggregatedExchange *Exchange

	for i, p := range m.processors {
		// Pour chaque branche, on crée une copie de l'échange original
		branchExchange := exchange.Copy()
		
		// On peut ajouter des propriétés spécifiques au multicast
		branchExchange.SetProperty("CamelMulticastIndex", i)
		branchExchange.SetProperty("CamelMulticastSize", len(m.processors))
		branchExchange.SetProperty("CamelMulticastComplete", i == len(m.processors)-1)

		if err := p.Process(branchExchange); err != nil {
			if !errors.Is(err, ErrStopRouting) {
				return err
			}
			// En cas de Stop EIP, on ignore l'erreur et on continue vers la branche suivante
		}

		if m.AggregationStrategy != nil {
			aggregatedExchange = m.AggregationStrategy.Aggregate(aggregatedExchange, branchExchange)
		}
	}

	if m.AggregationStrategy != nil && aggregatedExchange != nil {
		// Mettre à jour l'échange original avec le résultat de l'agrégation
		exchange.In = aggregatedExchange.In
		exchange.Out = aggregatedExchange.Out
		exchange.Properties = aggregatedExchange.Properties
	}

	return nil
}

func (m *Multicast) processParallel(exchange *Exchange) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var aggregatedExchange *Exchange
	var firstErr error

	for i, p := range m.processors {
		wg.Add(1)
		go func(index int, processor Processor) {
			defer wg.Done()

			branchExchange := exchange.Copy()
			branchExchange.SetProperty("CamelMulticastIndex", index)
			branchExchange.SetProperty("CamelMulticastSize", len(m.processors))
			branchExchange.SetProperty("CamelMulticastComplete", index == len(m.processors)-1)

			err := processor.Process(branchExchange)
			
			mu.Lock()
			defer mu.Unlock()

			if err != nil && !errors.Is(err, ErrStopRouting) {
				if firstErr == nil {
					firstErr = err
				}
				return
			}

			if m.AggregationStrategy != nil {
				aggregatedExchange = m.AggregationStrategy.Aggregate(aggregatedExchange, branchExchange)
			}
		}(i, p)
	}

	wg.Wait()

	if firstErr != nil {
		return fmt.Errorf("multicast parallel error: %w", firstErr)
	}

	if m.AggregationStrategy != nil && aggregatedExchange != nil {
		exchange.In = aggregatedExchange.In
		exchange.Out = aggregatedExchange.Out
		exchange.Properties = aggregatedExchange.Properties
	}

	return nil
}
