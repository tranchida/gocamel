package cron

import (
	"context"
	"errors"
	"fmt"
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
	mu        sync.Mutex
	scheduler *cron.Cron
	started   bool
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

// Stop stops the shared scheduler if it was started.
func (c *CronComponent) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		ctx := c.scheduler.Stop()
		<-ctx.Done()
		c.started = false
		c.scheduler = cron.New(cron.WithSeconds())
	}
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
		if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
			triggerStartDelay = time.Duration(ms) * time.Millisecond
		}
	}

	// repeatCount: -1 = infinite
	repeatCount := int64(-1)
	if s := q.Get("trigger.repeatCount"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			repeatCount = n
		}
	}

	// repeatInterval in milliseconds (SimpleTrigger)
	repeatInterval := int64(0)
	if s := q.Get("trigger.repeatInterval"); s != "" {
		if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
			repeatInterval = ms
		}
	}

	// Spaces in cron expressions are encoded as "+" in URIs
	cronExpr := strings.ReplaceAll(q.Get("cron"), "+", " ")

	// Validate the cron expression eagerly so an invalid schedule fails the
	// route at Build time instead of silently failing when the job is added
	// to the scheduler (fail-fast, no fail-open).
	if cronExpr != "" {
		if _, err := specParser.Parse(cronExpr); err != nil {
			return nil, fmt.Errorf("invalid cron expression %q: %w", cronExpr, err)
		}
	}

	// Validate the timezone eagerly. Whitespace is rejected explicitly: the
	// timezone is later prepended to the cron spec as "TZ=<tz> <spec>", where
	// a space would corrupt the expression.
	timezone := q.Get("trigger.timeZone")
	if timezone != "" {
		if strings.ContainsAny(timezone, " \t") {
			return nil, fmt.Errorf("invalid trigger.timeZone %q: whitespace not allowed", timezone)
		}
		if _, err := time.LoadLocation(timezone); err != nil {
			return nil, fmt.Errorf("invalid trigger.timeZone %q: %w", timezone, err)
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
		deleteJob:         q.Get("deleteJob") != "false",
		pauseJob:          q.Get("pauseJob") == "true",
		stateful:          q.Get("stateful") == "true",
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
	mu      sync.Mutex
	entryID cron.EntryID
	added   bool

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

// Start starts the cron consumer.
func (c *CronConsumer) Start(ctx context.Context) error {
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
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	spec, err := c.buildSpec()
	if err != nil {
		cancel()
		return err
	}

	ep := c.endpoint
	sched := ep.component.scheduler

	jobFn := func() {
		c.mu.Lock()
		if c.stopped || c.paused.Load() {
			c.mu.Unlock()
			return
		}
		c.jobsWG.Add(1)
		c.mu.Unlock()
		defer c.jobsWG.Done()

		now := time.Now()

		c.mu.Lock()
		entry := sched.Entry(c.entryID)
		c.mu.Unlock()

		var prevTime time.Time
		if p := c.prevFireTime.Load(); p != nil {
			prevTime = *p
		}

		n := c.fireCount.Add(1)

		exchange := gocamel.NewExchange(ctx)
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

	gCtx := ctx

	c.startWG.Add(1)
	go func() {
		defer c.startWG.Done()

		select {
		case <-time.After(ep.triggerStartDelay):
		case <-gCtx.Done():
			return
		}

		// The add and the stopped flag are decided under the same mutex Stop()
		// holds, so a Stop() that raced this goroutine either removed a job
		// that was never added (impossible) or is honoured here.
		c.mu.Lock()
		if c.stopped {
			c.mu.Unlock()
			return
		}
		entryID, err := sched.AddJob(spec, job)
		if err != nil {
			gocamel.LogError("Cron job add error", "group", ep.group, "name", ep.name, "error", err)
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

// startSimpleTrigger starts a Go ticker for fixed-interval triggers.
// Unlike robfig/cron, this approach supports sub-second intervals.
func (c *CronConsumer) startSimpleTrigger(ctx context.Context) error {
	ep := c.endpoint
	interval := time.Duration(ep.repeatInterval) * time.Millisecond

	gCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	c.startWG.Add(1)
	go func() {
		defer c.startWG.Done()
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

// fireSimpleJob executes the processor for a SimpleTrigger fire.
func (c *CronConsumer) fireSimpleJob(ctx, gCtx context.Context, now time.Time) {
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
		return
	}
	if ep.repeatCount >= 0 && n == ep.repeatCount {
		// Last authorized trigger: stop the ticker after execution
		defer func() {
			select {
			case <-gCtx.Done(): // already cancelled
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

	exchange := gocamel.NewExchange(ctx)
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
	c.mu.Lock()
	pause := c.endpoint.pauseJob
	if pause {
		c.paused.Store(true)
	} else {
		c.stopped = true
	}
	c.mu.Unlock()
	if !pause {
		if c.cancel != nil {
			c.cancel()
		}
		c.startWG.Wait()
		c.mu.Lock()
		added, entryID := c.added, c.entryID
		c.added = false
		c.mu.Unlock()
		if added {
			c.endpoint.component.scheduler.Remove(entryID)
		}
	} else if c.endpoint.repeatInterval > 0 {
		if c.cancel != nil {
			c.cancel()
		}
		c.startWG.Wait()
	}
	c.jobsWG.Wait()
	return nil
}
