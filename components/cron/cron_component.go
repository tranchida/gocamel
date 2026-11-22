package cron

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/robfig/cron/v3"

	gocamel "github.com/tranchida/gocamel"
)

// Cron headers set on each triggered Exchange.
// Match the headers of the Apache Camel Cron component.
const (
	CronFireTime          = "fireTime"          // Actual trigger fire time
	CronScheduledFireTime = "scheduledFireTime" // Scheduled trigger fire time
	CronNextFireTime      = "nextFireTime"      // Next scheduled trigger time
	CronPreviousFireTime  = "previousFireTime"  // Previous trigger time (zero if first)
	CronTriggerName       = "triggerName"       // Trigger name
	CronTriggerGroup      = "triggerGroup"      // Trigger group
	CronRefireCount       = "refireCount"       // Number of times the trigger has fired
)

// CronComponent implements a cron scheduler shared across all its routes,
// inspired by the Apache Camel Cron component.
//
// All CronEndpoints created from the same CronComponent share
// a single scheduler instance (Apache Camel behavior).
type CronComponent struct {
	mu         sync.Mutex
	scheduler  *cron.Cron
	started    bool
	activeJobs int
}

// NewCronComponent creates a CronComponent with a shared scheduler.
// The scheduler uses 6-field cron expressions (seconds included),
// compatible with the Java format:
//
//	┌───────────── seconds (0-59)
//	│ ┌───────────── minutes (0-59)
//	│ │ ┌───────────── hours (0-23)
//	│ │ │ ┌───────────── day of month (1-31)
//	│ │ │ │ ┌───────────── month (1-12)
//	│ │ │ │ │ ┌───────────── day of week (0-6, 0=Sunday)
//	│ │ │ │ │ │
//	* * * * * *
func NewCronComponent() *CronComponent {
	return &CronComponent{
		scheduler: cron.New(cron.WithSeconds()),
	}
}

// specParser matches the parser used by the shared scheduler
// (cron.WithSeconds()): 6 fields with mandatory seconds, plus descriptors.
// Used to validate cron expressions eagerly at endpoint creation time instead
// of failing open at job registration.
var specParser = cron.NewParser(cron.Second | cron.Minute | cron.Hour |
	cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// ensureStarted starts the shared scheduler if it has not been started yet.
func (c *CronComponent) ensureStarted() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		c.scheduler.Start()
		c.started = true
	}
}

func (c *CronComponent) addJob(spec string, cmd cron.Job) (*cron.Cron, cron.EntryID, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	sched := c.scheduler
	id, err := sched.AddJob(spec, cmd)
	if err != nil {
		return nil, 0, err
	}
	c.activeJobs++
	if !c.started {
		sched.Start()
		c.started = true
	}
	return sched, id, nil
}

func (c *CronComponent) removeJob(sched *cron.Cron, id cron.EntryID) {
	if sched == nil {
		return
	}
	sched.Remove(id)

	c.mu.Lock()
	if sched != c.scheduler {
		c.mu.Unlock()
		return
	}
	if c.activeJobs > 0 {
		c.activeJobs--
	}
	var stopCtx context.Context
	if c.activeJobs == 0 && c.started {
		// Rotate to a fresh scheduler and stop the idle one so no background
		// goroutine remains running once all jobs have been removed.
		c.scheduler = cron.New(cron.WithSeconds())
		c.started = false
		stopCtx = sched.Stop()
	}
	c.mu.Unlock()

	if stopCtx != nil {
		<-stopCtx.Done()
	}
}

func (c *CronComponent) entry(sched *cron.Cron, id cron.EntryID) cron.Entry {
	if sched == nil {
		return cron.Entry{}
	}
	return sched.Entry(id)
}

// Stop stops the shared scheduler if it was started.
func (c *CronComponent) Stop() error {
	c.mu.Lock()
	if !c.started {
		c.mu.Unlock()
		return nil
	}
	sched := c.scheduler
	c.scheduler = cron.New(cron.WithSeconds())
	c.started = false
	c.activeJobs = 0
	stopCtx := sched.Stop()
	c.mu.Unlock()

	<-stopCtx.Done()
	return nil
}

// CreateEndpoint creates a CronEndpoint from a URI.
//
// Supported formats:
//
//	cron://timerName?cron=0+*+*+*+*+?
//	cron://groupName/timerName?cron=0+*+*+*+*+?
//	cron://timerName?trigger.repeatInterval=5000
//
// URI options:
//
//	cron                  6-field cron expression (spaces encoded as "+")
//	trigger.timeZone      IANA timezone (e.g. Europe/Paris)
//	trigger.repeatInterval Interval in ms for SimpleTrigger (without cron)
//	trigger.repeatCount   Max number of triggers (-1 = infinite, default)
//	triggerStartDelay     Delay in ms before first trigger (default: 500)
//	deleteJob             Remove the job on stop (default: true)
//	pauseJob              Pause instead of removing (default: false)
//	stateful              Prevent concurrent executions (default: false)
const maxDurationMillis = math.MaxInt64 / int64(time.Millisecond)

func parseCronBoolOption(q url.Values, key string, defaultVal bool) (bool, error) {
	s := q.Get(key)
	if s == "" {
		return defaultVal, nil
	}
	b, err := strconv.ParseBool(s)
	if err != nil {
		return false, fmt.Errorf("invalid boolean value %q for %s: %w", s, key, err)
	}
	return b, nil
}

func hasTZPrefix(spec string) bool {
	return strings.HasPrefix(spec, "TZ=") || strings.HasPrefix(spec, "CRON_TZ=")
}

func hasUnspacedTZPrefix(spec string) bool {
	return hasTZPrefix(spec) && !strings.Contains(spec, " ")
}

func (c *CronComponent) CreateEndpoint(uri string) (gocamel.Endpoint, error) {
	u, err := gocamel.ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("URI cron invalid: %w", err)
	}

	// groupName/timerName from host + path
	group := "Camel"
	name := u.Host
	if path := strings.TrimPrefix(u.Path, "/"); path != "" {
		group = u.Host
		name = path
	}
	if name == "" {
		return nil, errors.New("cron trigger name is required (e.g. cron://myTimer)")
	}

	q := u.Query()

	// triggerStartDelay in milliseconds (default 500ms)
	triggerStartDelay := 500 * time.Millisecond
	if s := q.Get("triggerStartDelay"); s != "" {
		ms, err := strconv.ParseInt(s, 10, 64)
		if err != nil || ms < 0 || ms > maxDurationMillis {
			return nil, fmt.Errorf("invalid triggerStartDelay %q: must be a non-negative integer within duration range", s)
		}
		triggerStartDelay = time.Duration(ms) * time.Millisecond
	}

	// repeatCount: -1 = infinite
	repeatCount := int64(-1)
	if s := q.Get("trigger.repeatCount"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < -1 {
			return nil, fmt.Errorf("invalid trigger.repeatCount %q: must be an integer >= -1", s)
		}
		repeatCount = n
	}

	// repeatInterval in milliseconds (SimpleTrigger)
	repeatInterval := int64(0)
	if s := q.Get("trigger.repeatInterval"); s != "" {
		ms, err := strconv.ParseInt(s, 10, 64)
		if err != nil || ms <= 0 || ms > maxDurationMillis {
			return nil, fmt.Errorf("invalid trigger.repeatInterval %q: must be a positive integer within duration range", s)
		}
		repeatInterval = ms
	}

	deleteJob, err := parseCronBoolOption(q, "deleteJob", true)
	if err != nil {
		return nil, err
	}
	pauseJob, err := parseCronBoolOption(q, "pauseJob", false)
	if err != nil {
		return nil, err
	}
	stateful, err := parseCronBoolOption(q, "stateful", false)
	if err != nil {
		return nil, err
	}

	// Spaces in cron expressions are encoded as "+" in URIs
	cronExpr := strings.ReplaceAll(q.Get("cron"), "+", " ")

	// Validate the timezone eagerly. Whitespace is rejected explicitly: the
	// timezone is later prepended to the cron spec as "TZ=<tz> <spec>", where
	// a space would corrupt the expression.
	timezone := q.Get("trigger.timeZone")
	if timezone != "" {
		if strings.ContainsAny(timezone, " \t\r\n") {
			return nil, fmt.Errorf("invalid trigger.timeZone %q: whitespace not allowed", timezone)
		}
		if _, err := time.LoadLocation(timezone); err != nil {
			return nil, fmt.Errorf("invalid trigger.timeZone %q: %w", timezone, err)
		}
	}

	// Validate the combined cron expression eagerly so an invalid schedule or an
	// unspaced TZ=/CRON_TZ= prefix (which panics inside robfig/cron) fails the
	// route at Build time instead of panicking or failing open later.
	if cronExpr != "" {
		if hasUnspacedTZPrefix(cronExpr) {
			return nil, fmt.Errorf("invalid cron expression %q: missing space and schedule after timezone prefix", cronExpr)
		}
		spec := cronExpr
		if timezone != "" {
			if hasTZPrefix(cronExpr) {
				return nil, fmt.Errorf("invalid cron expression %q: cannot combine inline timezone with trigger.timeZone", cronExpr)
			}
			spec = "TZ=" + timezone + " " + cronExpr
		}
		if _, err := specParser.Parse(spec); err != nil {
			return nil, fmt.Errorf("invalid cron expression %q: %w", cronExpr, err)
		}
	}

	return &CronEndpoint{
		uri:               uri,
		group:             group,
		name:              name,
		cronExpr:          cronExpr,
		timezone:          timezone,
		repeatInterval:    repeatInterval,
		repeatCount:       repeatCount,
		triggerStartDelay: triggerStartDelay,
		deleteJob:         deleteJob,
		pauseJob:          pauseJob,
		stateful:          stateful,
		component:         c,
	}, nil
}

// ---------------------------------------------------------------------------
// Endpoint
// ---------------------------------------------------------------------------

// CronEndpoint represents a configured cron endpoint.
type CronEndpoint struct {
	uri               string
	group             string
	name              string
	cronExpr          string        // CronTrigger if non-empty
	timezone          string        // IANA timezone (e.g. "Europe/Paris")
	repeatInterval    int64         // SimpleTrigger: interval in ms
	repeatCount       int64         // SimpleTrigger: -1 = infinite
	triggerStartDelay time.Duration // delay before first trigger
	deleteJob         bool          // remove job on stop
	pauseJob          bool          // pause instead of removing
	stateful          bool          // prevent concurrent executions
	component         *CronComponent
}

func (e *CronEndpoint) URI() string { return e.uri }

// CreateProducer returns an error: the cron component only supports consumers.
func (e *CronEndpoint) CreateProducer() (gocamel.Producer, error) {
	return nil, errors.New("the cron component does not support producers")
}

// CreateConsumer creates a cron consumer.
func (e *CronEndpoint) CreateConsumer(processor gocamel.Processor) (gocamel.Consumer, error) {
	return &CronConsumer{
		endpoint:  e,
		processor: processor,
	}, nil
}

// ---------------------------------------------------------------------------
// Consumer
// ---------------------------------------------------------------------------

// CronConsumer triggers the processor according to the cron expression or configured interval.
type CronConsumer struct {
	endpoint  *CronEndpoint
	processor gocamel.Processor

	// CronTrigger: entry in the shared scheduler
	mu         sync.Mutex
	transition chan struct{}
	started    bool
	sched      *cron.Cron
	entryID    cron.EntryID
	added      bool

	// Shared state
	fireCount    atomic.Int64              // fire count (refireCount header)
	paused       atomic.Bool               // true when pauseJob=true and Stop() was called
	prevFireTime atomic.Pointer[time.Time] // actual time of last trigger

	cancel context.CancelFunc // cancels the startup goroutine or SimpleTrigger ticker

	// startWG tracks the delayed-registration goroutine started by
	// startCronTrigger. Stop() must wait for it to unwind before inspecting
	// c.added: without the join, a Stop() racing the goroutine's own
	// c.mu.Lock() would observe added == false, remove nothing, and let the
	// goroutine register the job on the SHARED component scheduler a moment
	// later — where it would fire forever with no owner left to remove it.
	startWG sync.WaitGroup
	jobsWG  sync.WaitGroup // Add is guarded by mu and stopped/paused

	// stopped is set (under mu) by the first Stop() once deleteJob semantics
	// apply. The registration goroutine re-checks it under the same mutex
	// before calling AddJob, so a Stop() that lands inside the start-delay
	// window reliably wins the race.
	stopped bool
}

// buildSpec builds the cron expression for robfig/cron (CronTrigger only).
// Returns an error if neither cron nor repeatInterval is configured.
// Prepends "TZ=..." if trigger.timeZone is set.
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
		// SimpleTrigger does not use robfig/cron
		return "", errors.New("cron: buildSpec() does not apply to SimpleTrigger")
	default:
		return "", errors.New("cron: cron or trigger.repeatInterval is required")
	}
}

// acquireTransition waits for an in-progress Start/Stop and claims the next
// transition. It never holds c.mu while waiting.
func (c *CronConsumer) acquireTransition() {
	for {
		c.mu.Lock()
		if c.transition != nil {
			done := c.transition
			c.mu.Unlock()
			<-done
			continue
		}
		c.transition = make(chan struct{})
		c.mu.Unlock()
		return
	}
}

// releaseTransition wakes any Start/Stop waiters after the transition finishes.
func (c *CronConsumer) releaseTransition() {
	c.mu.Lock()
	close(c.transition)
	c.transition = nil
	c.mu.Unlock()
}

// Start starts the cron consumer.
func (c *CronConsumer) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	c.acquireTransition()
	defer c.releaseTransition()

	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	ep := c.endpoint
	if ep.cronExpr != "" {
		return c.startCronTrigger(ctx)
	}
	if ep.repeatInterval > 0 {
		return c.startSimpleTrigger(ctx)
	}
	return errors.New("cron: cron or trigger.repeatInterval is required")
}

// startCronTrigger registers a job in the shared robfig/cron scheduler.
func (c *CronConsumer) startCronTrigger(ctx context.Context) error {
	spec, err := c.buildSpec()
	if err != nil {
		return err
	}

	gCtx, cancel := context.WithCancel(ctx)
	ep := c.endpoint

	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	c.cancel = cancel
	c.stopped = false
	c.paused.Store(false)
	c.fireCount.Store(0)
	c.prevFireTime.Store(nil)
	c.started = true
	var (
		oldSched *cron.Cron
		oldID    cron.EntryID
	)
	wasAdded := c.added
	if wasAdded {
		oldSched = c.sched
		oldID = c.entryID
		c.sched = nil
		c.entryID = 0
		c.added = false
	}
	c.startWG.Add(1)
	c.mu.Unlock()

	if wasAdded {
		ep.component.removeJob(oldSched, oldID)
	}

	jobFn := func() {
		c.mu.Lock()
		if c.stopped || c.paused.Load() {
			c.mu.Unlock()
			return
		}
		sched := c.sched
		entryID := c.entryID
		c.jobsWG.Add(1)
		c.mu.Unlock()
		defer c.jobsWG.Done()

		now := time.Now()
		entry := ep.component.entry(sched, entryID)

		var prevTime time.Time
		if p := c.prevFireTime.Load(); p != nil {
			prevTime = *p
		}

		n := c.fireCount.Add(1)

		exchange := gocamel.NewExchange(gCtx)
		exchange.GetIn().SetHeader(CronFireTime, now)
		exchange.GetIn().SetHeader(CronScheduledFireTime, entry.Prev)
		exchange.GetIn().SetHeader(CronNextFireTime, entry.Next)
		exchange.GetIn().SetHeader(CronPreviousFireTime, prevTime)
		exchange.GetIn().SetHeader(CronTriggerName, ep.name)
		exchange.GetIn().SetHeader(CronTriggerGroup, ep.group)
		exchange.GetIn().SetHeader(CronRefireCount, n)

		if err := gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange)); err != nil {
			gocamel.LogError("Cron processing error", "group", ep.group, "name", ep.name, "error", err)
		}

		t := now
		c.prevFireTime.Store(&t)
	}

	var job cron.Job = cron.FuncJob(jobFn)
	if ep.stateful {
		job = cron.NewChain(cron.SkipIfStillRunning(cron.DefaultLogger)).Then(job)
	}

	go func() {
		defer c.startWG.Done()

		if ep.triggerStartDelay > 0 {
			timer := time.NewTimer(ep.triggerStartDelay)
			select {
			case <-timer.C:
			case <-gCtx.Done():
				timer.Stop()
				return
			}
		} else if gCtx.Err() != nil {
			return
		}

		// The add and the stopped/paused/ctx state are decided under the same
		// mutex Stop() holds, so a Stop() or cancelled ctx racing this goroutine
		// is honoured before registering on the shared scheduler.
		c.mu.Lock()
		if c.stopped || c.paused.Load() || gCtx.Err() != nil {
			c.mu.Unlock()
			return
		}
		sched, entryID, err := ep.component.addJob(spec, job)
		if err != nil {
			gocamel.LogError("Cron job add error", "group", ep.group, "name", ep.name, "error", err)
			c.mu.Unlock()
			return
		}
		c.sched = sched
		c.entryID = entryID
		c.added = true
		c.mu.Unlock()
	}()

	return nil
}

// startSimpleTrigger starts a Go ticker for fixed-interval triggers.
// Unlike robfig/cron, this approach supports sub-second intervals.
func (c *CronConsumer) startSimpleTrigger(ctx context.Context) error {
	ep := c.endpoint
	if ep.repeatCount == 0 {
		c.mu.Lock()
		c.stopped = false
		c.paused.Store(false)
		c.fireCount.Store(0)
		c.prevFireTime.Store(nil)
		c.started = true
		c.mu.Unlock()
		return nil
	}

	interval := time.Duration(ep.repeatInterval) * time.Millisecond

	gCtx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	c.cancel = cancel
	c.stopped = false
	c.paused.Store(false)
	c.fireCount.Store(0)
	c.prevFireTime.Store(nil)
	c.started = true
	c.startWG.Add(1)
	c.mu.Unlock()

	go func() {
		defer c.startWG.Done()
		defer cancel()

		if ep.triggerStartDelay > 0 {
			timer := time.NewTimer(ep.triggerStartDelay)
			select {
			case <-timer.C:
			case <-gCtx.Done():
				timer.Stop()
				return
			}
		} else if gCtx.Err() != nil {
			return
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			if gCtx.Err() != nil {
				return
			}
			select {
			case tickTime := <-ticker.C:
				c.fireSimpleJob(gCtx, cancel, tickTime)
			case <-gCtx.Done():
				return
			}
		}
	}()

	return nil
}

// fireSimpleJob executes the processor for a SimpleTrigger fire.
func (c *CronConsumer) fireSimpleJob(gCtx context.Context, cancel context.CancelFunc, now time.Time) {
	c.mu.Lock()
	if c.stopped || c.paused.Load() {
		c.mu.Unlock()
		return
	}
	c.jobsWG.Add(1)
	c.mu.Unlock()
	defer c.jobsWG.Done()

	ep := c.endpoint

	// Handle repeatCount
	n := c.fireCount.Add(1)
	if ep.repeatCount >= 0 && n > ep.repeatCount {
		cancel()
		return
	}
	if ep.repeatCount >= 0 && n == ep.repeatCount {
		// Last authorized trigger: stop the ticker after execution via this run's local cancel.
		defer cancel()
	}

	var prevTime time.Time
	if p := c.prevFireTime.Load(); p != nil {
		prevTime = *p
	}

	nextFireTime := now.Add(time.Duration(ep.repeatInterval) * time.Millisecond)

	exchange := gocamel.NewExchange(gCtx)
	exchange.GetIn().SetHeader(CronFireTime, now)
	exchange.GetIn().SetHeader(CronScheduledFireTime, now)
	exchange.GetIn().SetHeader(CronNextFireTime, nextFireTime)
	exchange.GetIn().SetHeader(CronPreviousFireTime, prevTime)
	exchange.GetIn().SetHeader(CronTriggerName, ep.name)
	exchange.GetIn().SetHeader(CronTriggerGroup, ep.group)
	exchange.GetIn().SetHeader(CronRefireCount, n)

	if err := gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange)); err != nil {
		gocamel.LogError("Cron processing error", "group", ep.group, "name", ep.name, "error", err)
	}

	t := now
	c.prevFireTime.Store(&t)
}

// Stop stops the consumer according to the deleteJob / pauseJob options.
func (c *CronConsumer) Stop() error {
	c.acquireTransition()
	defer c.releaseTransition()

	c.mu.Lock()
	if !c.started {
		c.mu.Unlock()
		return nil
	}
	pause := c.endpoint.pauseJob
	deleteOnStop := c.endpoint.deleteJob
	if pause {
		c.paused.Store(true)
	} else {
		c.stopped = true
	}
	cancel := c.cancel
	c.cancel = nil
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	c.startWG.Wait()

	if !pause && deleteOnStop {
		c.mu.Lock()
		added, sched, entryID := c.added, c.sched, c.entryID
		c.added = false
		c.sched = nil
		c.entryID = 0
		c.mu.Unlock()
		if added {
			c.endpoint.component.removeJob(sched, entryID)
		}
	}
	c.jobsWG.Wait()

	c.mu.Lock()
	c.started = false
	c.mu.Unlock()
	return nil
}
