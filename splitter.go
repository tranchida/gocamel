package gocamel

import (
	"errors"
	"fmt"
	"reflect"
)

// Splitter est un Processor qui implémente l'EIP Split.
// Il divise un message en plusieurs et les traite individuellement.
type Splitter struct {
	Expression          func(*Exchange) (any, error)
	processors          []Processor
	AggregationStrategy AggregationStrategy
}

// NewSplitter crée une nouvelle instance de Splitter
func NewSplitter(expression func(*Exchange) (any, error)) *Splitter {
	return &Splitter{
		Expression: expression,
		processors: make([]Processor, 0),
	}
}

// AddProcessor ajoute un processeur au traitement de chaque partie du message splité
func (s *Splitter) AddProcessor(processor Processor) {
	s.processors = append(s.processors, processor)
}

// SetAggregationStrategy définit la stratégie d'agrégation pour collecter les résultats
func (s *Splitter) SetAggregationStrategy(strategy AggregationStrategy) *Splitter {
	s.AggregationStrategy = strategy
	return s
}

// Process exécute le split sur l'échange fourni
func (s *Splitter) Process(exchange *Exchange) error {
	parts, err := s.Expression(exchange)
	if err != nil {
		return fmt.Errorf("split expression error: %w", err)
	}

	if parts == nil {
		return nil
	}

	// Déterminer comment itérer sur 'parts'
	v := reflect.ValueOf(parts)
	
	// Si ce n'est pas une slice ou un tableau, on le traite comme un seul élément
	if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
		partExchange := exchange.Copy()
		partExchange.In.SetBody(parts)
		partExchange.SetProperty("CamelSplitIndex", 0)
		partExchange.SetProperty("CamelSplitSize", 1)
		partExchange.SetProperty("CamelSplitComplete", true)

		if err := s.processPart(partExchange, parts, 0, 1); err != nil {
			if !errors.Is(err, ErrStopRouting) {
				return err
			}
			// En cas de Stop EIP, on continue vers l'agrégation
		}

		if s.AggregationStrategy != nil {
			aggregatedExchange := s.AggregationStrategy.Aggregate(nil, partExchange)
			if aggregatedExchange != nil {
				exchange.In = aggregatedExchange.In
				exchange.Out = aggregatedExchange.Out
				exchange.Properties = aggregatedExchange.Properties
			}
		}
		return nil
	}

	length := v.Len()
	if length == 0 {
		return nil
	}

	var aggregatedExchange *Exchange
	for i := 0; i < length; i++ {
		part := v.Index(i).Interface()
		
		// Pour chaque partie, on crée une copie de l'échange original
		partExchange := exchange.Copy()
		partExchange.In.SetBody(part)
		
		// On peut ajouter des propriétés spécifiques au split (index, total, etc.)
		partExchange.SetProperty("CamelSplitIndex", i)
		partExchange.SetProperty("CamelSplitSize", length)
		partExchange.SetProperty("CamelSplitComplete", i == length-1)

		if err := s.processPart(partExchange, part, i, length); err != nil {
			if !errors.Is(err, ErrStopRouting) {
				return err
			}
			// En cas de Stop EIP, on ignore l'erreur et on continue vers la partie suivante
		}

		if s.AggregationStrategy != nil {
			aggregatedExchange = s.AggregationStrategy.Aggregate(aggregatedExchange, partExchange)
		}
	}

	if s.AggregationStrategy != nil && aggregatedExchange != nil {
		// Mettre à jour l'échange original avec le résultat de l'agrégation
		exchange.In = aggregatedExchange.In
		exchange.Out = aggregatedExchange.Out
		exchange.Properties = aggregatedExchange.Properties
	}

	return nil
}

func (s *Splitter) processPart(exchange *Exchange, part any, index, size int) error {
	for _, p := range s.processors {
		if err := p.Process(exchange); err != nil {
			return err
		}
	}
	return nil
}
