package gocamel

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/robfig/cron/v3"
)

// En-têtes Quartz posés sur chaque Exchange déclenché.
// Correspondent aux en-têtes du composant Apache Camel Quartz.
const (
	QuartzFireTime          = "fireTime"          // Heure réelle de déclenchement
	QuartzScheduledFireTime = "scheduledFireTime"  // Heure planifiée de déclenchement
	QuartzNextFireTime      = "nextFireTime"       // Prochain déclenchement planifié
	QuartzPreviousFireTime  = "previousFireTime"   // Déclenchement précédent (zero si premier)
	QuartzTriggerName       = "triggerName"        // Nom du trigger
	QuartzTriggerGroup      = "triggerGroup"       // Groupe du trigger
	QuartzRefireCount       = "refireCount"        // Nombre de déclenchements effectués
)

// QuartzComponent implémente un scheduler cron partagé entre toutes ses routes,
// inspiré du composant Apache Camel Quartz.
//
// Tous les QuartzEndpoint créés depuis le même QuartzComponent partagent
// une seule instance de scheduler (comportement Apache Camel).
type QuartzComponent struct {
	mu        sync.Mutex
	scheduler *cron.Cron
	started   bool
}

// NewQuartzComponent crée un QuartzComponent avec un scheduler partagé.
// Le scheduler utilise des expressions cron à 6 champs (secondes incluses),
// compatibles avec le format Quartz Java :
//
//	┌───────────── secondes (0-59)
//	│ ┌───────────── minutes (0-59)
//	│ │ ┌───────────── heures (0-23)
//	│ │ │ ┌───────────── jour du mois (1-31)
//	│ │ │ │ ┌───────────── mois (1-12)
//	│ │ │ │ │ ┌───────────── jour de la semaine (0-6, 0=dimanche)
//	│ │ │ │ │ │
//	* * * * * *
func NewQuartzComponent() *QuartzComponent {
	return &QuartzComponent{
		scheduler: cron.New(cron.WithSeconds()),
	}
}

// ensureStarted démarre le scheduler partagé si ce n'est pas encore fait.
func (c *QuartzComponent) ensureStarted() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		c.scheduler.Start()
		c.started = true
	}
}

// CreateEndpoint crée un QuartzEndpoint à partir d'une URI.
//
// Formats supportés :
//
//	quartz://timerName?cron=0+*+*+*+*+?
//	quartz://groupName/timerName?cron=0+*+*+*+*+?
//	quartz://timerName?trigger.repeatInterval=5000
//
// Options URI :
//
//	cron                  Expression cron 6 champs (espaces encodés en "+")
//	trigger.timeZone      Timezone IANA (ex: Europe/Paris)
//	trigger.repeatInterval Intervalle en ms pour SimpleTrigger (sans cron)
//	trigger.repeatCount   Nombre de déclenchements max (-1 = infini, défaut)
//	triggerStartDelay     Délai en ms avant le premier déclenchement (défaut: 500)
//	deleteJob             Supprimer le job à l'arrêt (défaut: true)
//	pauseJob              Mettre en pause au lieu de supprimer (défaut: false)
//	stateful              Empêcher les exécutions concurrentes (défaut: false)
func (c *QuartzComponent) CreateEndpoint(uri string) (Endpoint, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("URI quartz invalide: %w", err)
	}

	// groupName/timerName depuis host + path
	group := "Camel"
	name := u.Host
	if path := strings.TrimPrefix(u.Path, "/"); path != "" {
		group = u.Host
		name = path
	}
	if name == "" {
		return nil, errors.New("le nom du trigger quartz est requis (ex: quartz://monTimer)")
	}

	q := u.Query()

	// triggerStartDelay en millisecondes (défaut 500ms)
	triggerStartDelay := 500 * time.Millisecond
	if s := q.Get("triggerStartDelay"); s != "" {
		if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
			triggerStartDelay = time.Duration(ms) * time.Millisecond
		}
	}

	// repeatCount : -1 = infini
	repeatCount := int64(-1)
	if s := q.Get("trigger.repeatCount"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			repeatCount = n
		}
	}

	// repeatInterval en millisecondes (SimpleTrigger)
	repeatInterval := int64(0)
	if s := q.Get("trigger.repeatInterval"); s != "" {
		if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
			repeatInterval = ms
		}
	}

	// Les espaces dans les expressions cron Quartz sont encodés en "+" dans les URI
	cronExpr := strings.ReplaceAll(q.Get("cron"), "+", " ")

	return &QuartzEndpoint{
		uri:               uri,
		group:             group,
		name:              name,
		cronExpr:          cronExpr,
		timezone:          q.Get("trigger.timeZone"),
		repeatInterval:    repeatInterval,
		repeatCount:       repeatCount,
		triggerStartDelay: triggerStartDelay,
		deleteJob:         q.Get("deleteJob") != "false",
		pauseJob:          q.Get("pauseJob") == "true",
		stateful:          q.Get("stateful") == "true",
		component:         c,
	}, nil
}

// ---------------------------------------------------------------------------
// Endpoint
// ---------------------------------------------------------------------------

// QuartzEndpoint représente un endpoint Quartz configuré.
type QuartzEndpoint struct {
	uri               string
	group             string
	name              string
	cronExpr          string        // CronTrigger si non vide
	timezone          string        // IANA timezone (ex: "Europe/Paris")
	repeatInterval    int64         // SimpleTrigger : intervalle en ms
	repeatCount       int64         // SimpleTrigger : -1 = infini
	triggerStartDelay time.Duration // délai avant premier déclenchement
	deleteJob         bool          // supprimer le job à l'arrêt
	pauseJob          bool          // mettre en pause au lieu de supprimer
	stateful          bool          // empêcher les exécutions concurrentes
	component         *QuartzComponent
}

func (e *QuartzEndpoint) URI() string { return e.uri }

// CreateProducer retourne une erreur : Quartz ne supporte que les consommateurs.
func (e *QuartzEndpoint) CreateProducer() (Producer, error) {
	return nil, errors.New("le composant quartz ne supporte pas les producteurs")
}

// CreateConsumer crée un consommateur Quartz.
func (e *QuartzEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return &QuartzConsumer{
		endpoint:  e,
		processor: processor,
	}, nil
}

// ---------------------------------------------------------------------------
// Consumer
// ---------------------------------------------------------------------------

// QuartzConsumer déclenche le processor selon l'expression cron ou l'intervalle configuré.
type QuartzConsumer struct {
	endpoint  *QuartzEndpoint
	processor Processor

	// CronTrigger : entrée dans le scheduler partagé
	mu      sync.Mutex
	entryID cron.EntryID
	added   bool

	// État partagé
	fireCount    atomic.Int64             // compteur de déclenchements (refireCount header)
	paused       atomic.Bool              // true quand pauseJob=true et Stop() appelé
	prevFireTime atomic.Pointer[time.Time] // heure réelle du dernier déclenchement

	cancel context.CancelFunc // arrête la goroutine de démarrage ou le ticker SimpleTrigger
}

// buildSpec construit l'expression cron pour robfig/cron (CronTrigger uniquement).
// Retourne une erreur si ni cron ni repeatInterval n'est configuré.
// Préfixe "TZ=..." ajouté si trigger.timeZone est défini.
func (c *QuartzConsumer) buildSpec() (string, error) {
	ep := c.endpoint
	switch {
	case ep.cronExpr != "":
		spec := ep.cronExpr
		if ep.timezone != "" {
			spec = "TZ=" + ep.timezone + " " + spec
		}
		return spec, nil
	case ep.repeatInterval > 0:
		// SimpleTrigger n'utilise pas robfig/cron
		return "", errors.New("quartz: buildSpec() ne s'applique pas au SimpleTrigger")
	default:
		return "", errors.New("quartz: cron ou trigger.repeatInterval est requis")
	}
}

// Start démarre le consommateur Quartz.
func (c *QuartzConsumer) Start(ctx context.Context) error {
	ep := c.endpoint
	if ep.cronExpr != "" {
		return c.startCronTrigger(ctx)
	}
	if ep.repeatInterval > 0 {
		return c.startSimpleTrigger(ctx)
	}
	return errors.New("quartz: cron ou trigger.repeatInterval est requis")
}

// startCronTrigger enregistre le job dans le scheduler robfig/cron partagé.
func (c *QuartzConsumer) startCronTrigger(ctx context.Context) error {
	spec, err := c.buildSpec()
	if err != nil {
		return err
	}

	ep := c.endpoint
	sched := ep.component.scheduler

	jobFn := func() {
		if c.paused.Load() {
			return
		}

		now := time.Now()

		c.mu.Lock()
		entry := sched.Entry(c.entryID)
		c.mu.Unlock()

		var prevTime time.Time
		if p := c.prevFireTime.Load(); p != nil {
			prevTime = *p
		}

		n := c.fireCount.Add(1)

		exchange := NewExchange(ctx)
		exchange.GetIn().SetHeader(QuartzFireTime, now)
		exchange.GetIn().SetHeader(QuartzScheduledFireTime, entry.Prev)
		exchange.GetIn().SetHeader(QuartzNextFireTime, entry.Next)
		exchange.GetIn().SetHeader(QuartzPreviousFireTime, prevTime)
		exchange.GetIn().SetHeader(QuartzTriggerName, ep.name)
		exchange.GetIn().SetHeader(QuartzTriggerGroup, ep.group)
		exchange.GetIn().SetHeader(QuartzRefireCount, n)

		if err := c.processor.Process(exchange); err != nil && !errors.Is(err, ErrStopRouting) {
			fmt.Printf("Erreur lors du traitement Quartz [%s/%s]: %v\n", ep.group, ep.name, err)
		}

		t := now
		c.prevFireTime.Store(&t)
	}

	var job cron.Job = cron.FuncJob(jobFn)
	if ep.stateful {
		job = cron.NewChain(cron.SkipIfStillRunning(cron.DefaultLogger)).Then(job)
	}

	gCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	go func() {
		select {
		case <-time.After(ep.triggerStartDelay):
		case <-gCtx.Done():
			return
		}

		c.mu.Lock()
		entryID, err := sched.AddJob(spec, job)
		if err != nil {
			fmt.Printf("Erreur ajout job quartz [%s/%s]: %v\n", ep.group, ep.name, err)
			c.mu.Unlock()
			return
		}
		c.entryID = entryID
		c.added = true
		c.mu.Unlock()

		ep.component.ensureStarted()
	}()

	return nil
}

// startSimpleTrigger démarre un ticker Go pour les déclenchements à intervalle fixe.
// Contrairement à robfig/cron, cette approche supporte les intervalles sub-secondes.
func (c *QuartzConsumer) startSimpleTrigger(ctx context.Context) error {
	ep := c.endpoint
	interval := time.Duration(ep.repeatInterval) * time.Millisecond

	gCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	go func() {
		defer cancel()

		select {
		case <-time.After(ep.triggerStartDelay):
		case <-gCtx.Done():
			return
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case tickTime := <-ticker.C:
				c.fireSimpleJob(ctx, gCtx, tickTime)
			case <-gCtx.Done():
				return
			}
		}
	}()

	return nil
}

// fireSimpleJob exécute le processor pour un déclenchement SimpleTrigger.
func (c *QuartzConsumer) fireSimpleJob(ctx, gCtx context.Context, now time.Time) {
	if c.paused.Load() {
		return
	}

	ep := c.endpoint

	// Gestion du repeatCount
	n := c.fireCount.Add(1)
	if ep.repeatCount >= 0 && n > ep.repeatCount {
		return
	}
	if ep.repeatCount >= 0 && n == ep.repeatCount {
		// Dernier déclenchement autorisé : arrêter le ticker après exécution
		defer func() {
			select {
			case <-gCtx.Done(): // déjà annulé
			default:
				c.cancel()
			}
		}()
	}

	var prevTime time.Time
	if p := c.prevFireTime.Load(); p != nil {
		prevTime = *p
	}

	nextFireTime := now.Add(time.Duration(ep.repeatInterval) * time.Millisecond)

	exchange := NewExchange(ctx)
	exchange.GetIn().SetHeader(QuartzFireTime, now)
	exchange.GetIn().SetHeader(QuartzScheduledFireTime, now)
	exchange.GetIn().SetHeader(QuartzNextFireTime, nextFireTime)
	exchange.GetIn().SetHeader(QuartzPreviousFireTime, prevTime)
	exchange.GetIn().SetHeader(QuartzTriggerName, ep.name)
	exchange.GetIn().SetHeader(QuartzTriggerGroup, ep.group)
	exchange.GetIn().SetHeader(QuartzRefireCount, n)

	if err := c.processor.Process(exchange); err != nil && !errors.Is(err, ErrStopRouting) {
		fmt.Printf("Erreur lors du traitement Quartz [%s/%s]: %v\n", ep.group, ep.name, err)
	}

	t := now
	c.prevFireTime.Store(&t)
}

// Stop arrête le consommateur selon les options deleteJob / pauseJob.
func (c *QuartzConsumer) Stop() error {
	ep := c.endpoint

	if ep.pauseJob {
		// Mettre en pause : le scheduler continue mais les jobs sont des no-ops
		c.paused.Store(true)
		return nil
	}

	// Arrêt complet : annuler la goroutine de démarrage ou le ticker
	if c.cancel != nil {
		c.cancel()
	}

	// Pour CronTrigger, supprimer l'entrée du scheduler
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.added {
		ep.component.scheduler.Remove(c.entryID)
		c.added = false
	}

	return nil
}
