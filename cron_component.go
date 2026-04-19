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

// En-têtes Cron posés sur chaque Exchange déclenché.
// Correspondent aux en-têtes du composant Apache Camel Cron.
const (
	CronFireTime          = "fireTime"          // Heure réelle de déclenchement
	CronScheduledFireTime = "scheduledFireTime"  // Heure planifiée de déclenchement
	CronNextFireTime      = "nextFireTime"       // Prochain déclenchement planifié
	CronPreviousFireTime  = "previousFireTime"   // Déclenchement précédent (zero si premier)
	CronTriggerName       = "triggerName"        // Nom du trigger
	CronTriggerGroup      = "triggerGroup"       // Groupe du trigger
	CronRefireCount       = "refireCount"        // Nombre de déclenchements effectués
)

// CronComponent implémente un scheduler cron partagé entre toutes ses routes,
// inspiré du composant Apache Camel .
//
// Tous les CronEndpoint créés depuis le même CronComponent partagent
// une seule instance de scheduler (comportement Apache Camel).
type CronComponent struct {
	mu        sync.Mutex
	scheduler *cron.Cron
	started   bool
}

// NewCronComponent crée un CronComponent avec un scheduler partagé.
// Le scheduler utilise des expressions cron à 6 champs (secondes incluses),
// compatibles avec le format  Java :
//
//	┌───────────── secondes (0-59)
//	│ ┌───────────── minutes (0-59)
//	│ │ ┌───────────── heures (0-23)
//	│ │ │ ┌───────────── jour du mois (1-31)
//	│ │ │ │ ┌───────────── mois (1-12)
//	│ │ │ │ │ ┌───────────── jour de la semaine (0-6, 0=dimanche)
//	│ │ │ │ │ │
//	* * * * * *
func NewCronComponent() *CronComponent {
	return &CronComponent{
		scheduler: cron.New(cron.WithSeconds()),
	}
}

// ensureStarted démarre le scheduler partagé si ce n'est pas encore fait.
func (c *CronComponent) ensureStarted() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		c.scheduler.Start()
		c.started = true
	}
}

// CreateEndpoint crée un CronEndpoint à partir d'une URI.
//
// Formats supportés :
//
//	cron://timerName?cron=0+*+*+*+*+?
//	cron://groupName/timerName?cron=0+*+*+*+*+?
//	cron://timerName?trigger.repeatInterval=5000
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
func (c *CronComponent) CreateEndpoint(uri string) (Endpoint, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("URI cron invalide: %w", err)
	}

	// groupName/timerName depuis host + path
	group := "Camel"
	name := u.Host
	if path := strings.TrimPrefix(u.Path, "/"); path != "" {
		group = u.Host
		name = path
	}
	if name == "" {
		return nil, errors.New("le nom du trigger cron est requis (ex: cron://monTimer)")
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

	// Les espaces dans les expressions cron  sont encodés en "+" dans les URI
	cronExpr := strings.ReplaceAll(q.Get("cron"), "+", " ")

	return &CronEndpoint{
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

// CronEndpoint représente un endpoint  configuré.
type CronEndpoint struct {
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
	component         *CronComponent
}

func (e *CronEndpoint) URI() string { return e.uri }

// CreateProducer retourne une erreur :  ne supporte que les consommateurs.
func (e *CronEndpoint) CreateProducer() (Producer, error) {
	return nil, errors.New("le composant cron ne supporte pas les producteurs")
}

// CreateConsumer crée un consommateur .
func (e *CronEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return &CronConsumer{
		endpoint:  e,
		processor: processor,
	}, nil
}

// ---------------------------------------------------------------------------
// Consumer
// ---------------------------------------------------------------------------

// CronConsumer déclenche le processor selon l'expression cron ou l'intervalle configuré.
type CronConsumer struct {
	endpoint  *CronEndpoint
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
func (c *CronConsumer) buildSpec() (string, error) {
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
		return "", errors.New("cron: buildSpec() ne s'applique pas au SimpleTrigger")
	default:
		return "", errors.New("cron: cron ou trigger.repeatInterval est requis")
	}
}

// Start démarre le consommateur .
func (c *CronConsumer) Start(ctx context.Context) error {
	ep := c.endpoint
	if ep.cronExpr != "" {
		return c.startCronTrigger(ctx)
	}
	if ep.repeatInterval > 0 {
		return c.startSimpleTrigger(ctx)
	}
	return errors.New("cron: cron ou trigger.repeatInterval est requis")
}

// startCronTrigger enregistre le job dans le scheduler robfig/cron partagé.
func (c *CronConsumer) startCronTrigger(ctx context.Context) error {
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
		exchange.GetIn().SetHeader(CronFireTime, now)
		exchange.GetIn().SetHeader(CronScheduledFireTime, entry.Prev)
		exchange.GetIn().SetHeader(CronNextFireTime, entry.Next)
		exchange.GetIn().SetHeader(CronPreviousFireTime, prevTime)
		exchange.GetIn().SetHeader(CronTriggerName, ep.name)
		exchange.GetIn().SetHeader(CronTriggerGroup, ep.group)
		exchange.GetIn().SetHeader(CronRefireCount, n)

		if err := c.processor.Process(exchange); err != nil && !errors.Is(err, ErrStopRouting) {
			fmt.Printf("Erreur lors du traitement  [%s/%s]: %v\n", ep.group, ep.name, err)
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
			fmt.Printf("Erreur ajout job cron [%s/%s]: %v\n", ep.group, ep.name, err)
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
func (c *CronConsumer) startSimpleTrigger(ctx context.Context) error {
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
func (c *CronConsumer) fireSimpleJob(ctx, gCtx context.Context, now time.Time) {
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
	exchange.GetIn().SetHeader(CronFireTime, now)
	exchange.GetIn().SetHeader(CronScheduledFireTime, now)
	exchange.GetIn().SetHeader(CronNextFireTime, nextFireTime)
	exchange.GetIn().SetHeader(CronPreviousFireTime, prevTime)
	exchange.GetIn().SetHeader(CronTriggerName, ep.name)
	exchange.GetIn().SetHeader(CronTriggerGroup, ep.group)
	exchange.GetIn().SetHeader(CronRefireCount, n)

	if err := c.processor.Process(exchange); err != nil && !errors.Is(err, ErrStopRouting) {
		fmt.Printf("Erreur lors du traitement  [%s/%s]: %v\n", ep.group, ep.name, err)
	}

	t := now
	c.prevFireTime.Store(&t)
}

// Stop arrête le consommateur selon les options deleteJob / pauseJob.
func (c *CronConsumer) Stop() error {
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
