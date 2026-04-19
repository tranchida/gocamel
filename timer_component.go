package gocamel

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// TimerComponent implémente Component for la gestion des timers
type TimerComponent struct{}

// NewTimerComponent creates a new TimerComponent
func NewTimerComponent() *TimerComponent {
	return &TimerComponent{}
}

// CreateEndpoint crée un TimerEndpoint à partir de l'URI
func (c *TimerComponent) CreateEndpoint(uri string) (Endpoint, error) {
	parsedURL, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("error during l'analyse de l'URI timer: %w", err)
	}

	timerName := parsedURL.Host
	if timerName == "" {
		timerName = parsedURL.Opaque // Handle format like timer:foo
		if timerName == "" && parsedURL.Path != "" {
			timerName = parsedURL.Path
		}
	}

	if timerName == "" {
		return nil, errors.New("le nom du timer est required")
	}

	endpoint := &TimerEndpoint{
		uri:         uri,
		timerName:   timerName,
		period:      1000,
		delay:       1000,
		repeatCount: 0,
		fixedRate:   false,
	}

	query := parsedURL.Query()

	if val := query.Get("period"); val != "" {
		if v, err := strconv.ParseInt(val, 10, 64); err == nil {
			endpoint.period = v
		}
	}

	if val := query.Get("delay"); val != "" {
		if v, err := strconv.ParseInt(val, 10, 64); err == nil {
			endpoint.delay = v
		}
	}

	if val := query.Get("repeatCount"); val != "" {
		if v, err := strconv.ParseInt(val, 10, 64); err == nil {
			endpoint.repeatCount = v
		}
	}

	if val := query.Get("fixedRate"); val != "" {
		if v, err := strconv.ParseBool(val); err == nil {
			endpoint.fixedRate = v
		}
	}

	return endpoint, nil
}

// TimerEndpoint represents a point de terminaison de type timer
type TimerEndpoint struct {
	uri         string
	timerName   string
	period      int64
	delay       int64
	repeatCount int64
	fixedRate   bool
}

// URI returns the URI de l'endpoint
func (e *TimerEndpoint) URI() string {
	return e.uri
}

// CreateProducer returns an error car Timer ne supporte que les consommateurs
func (e *TimerEndpoint) CreateProducer() (Producer, error) {
	return nil, errors.New("le composant timer ne supporte pas les producteurs, seulement les consommateurs")
}

// CreateConsumer crée un consommateur for le timer
func (e *TimerEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return &TimerConsumer{
		endpoint:  e,
		processor: processor,
		stopChan:  make(chan struct{}),
	}, nil
}

// TimerConsumer déclenche des événements périodiquement
type TimerConsumer struct {
	endpoint  *TimerEndpoint
	processor Processor
	stopChan  chan struct{}
	running   bool
	mu        sync.Mutex
}

// Start démarre la génération de messages par le timer
func (c *TimerConsumer) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running {
		return errors.New("le timer est déjà démarré")
	}

	c.running = true
	c.stopChan = make(chan struct{})

	go c.run(ctx)

	return nil
}

// Stop stops the timer
func (c *TimerConsumer) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.running {
		return nil
	}

	c.running = false
	close(c.stopChan)
	return nil
}

func (c *TimerConsumer) run(ctx context.Context) {
	// Attente initiale (delay)
	delayDuration := time.Duration(c.endpoint.delay) * time.Millisecond

	if delayDuration > 0 {
		select {
		case <-time.After(delayDuration):
		case <-c.stopChan:
			return
		case <-ctx.Done():
			return
		}
	} else if delayDuration < 0 {
		// Démarrage immédiat
	}

	periodDuration := time.Duration(c.endpoint.period) * time.Millisecond
	counter := int64(0)

	var ticker *time.Ticker
	if c.endpoint.fixedRate && periodDuration > 0 {
		ticker = time.NewTicker(periodDuration)
		defer ticker.Stop()
	}

	for {
		// Vérification de la limite
		if c.endpoint.repeatCount > 0 && counter >= c.endpoint.repeatCount {
			return
		}

		counter++
		firedTime := time.Now()

		exchange := NewExchange(ctx)
		exchange.SetProperty(CamelTimerName, c.endpoint.timerName)
		exchange.SetProperty(CamelTimerPeriod, c.endpoint.period)
		exchange.SetProperty(CamelTimerCounter, counter)
		exchange.GetIn().SetHeader(CamelTimerFiredTime, firedTime)

		err := c.processor.Process(exchange)
		if err != nil && err != ErrStopRouting {
			// Log l'error (in une vraie implémentation)
			// fmt.Printf("error during traitement de l'événement timer: %v\n", err)
		}

		if c.endpoint.repeatCount > 0 && counter >= c.endpoint.repeatCount {
			return
		}

		if ticker != nil {
			select {
			case <-ticker.C:
			case <-c.stopChan:
				return
			case <-ctx.Done():
				return
			}
		} else {
			// mode period (attente après exécution)
			if periodDuration > 0 {
				select {
				case <-time.After(periodDuration):
				case <-c.stopChan:
					return
				case <-ctx.Done():
					return
				}
			} else {
				// si period est <= 0, on continue with un léger délai for éviter la boucle infinie CPU
				select {
				case <-time.After(1 * time.Millisecond):
				case <-c.stopChan:
					return
				case <-ctx.Done():
					return
				}
			}
		}
	}
}
