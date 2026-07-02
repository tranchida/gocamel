package gocamel

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

// ensureStarted starts the shared scheduler if it has not been started yet.
func (c *CronComponent) ensureStarted() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		c.scheduler.Start()
		c.started = true
	}
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
func (c *CronComponent) CreateEndpoint(uri string) (Endpoint, error) {
	u, err := ParseURI(uri)
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
func (e *CronEndpoint) CreateProducer() (Producer, error) {
	return nil, errors.New("the cron component does not support producers")
}

// CreateConsumer creates a cron consumer.
func (e *CronEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
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
	processor Processor

	// CronTrigger: entry in the shared scheduler
	mu      sync.Mutex
	entryID cron.EntryID
	added   bool

	// Shared state
	fireCount    atomic.Int64              // fire count (refireCount header)
	paused       atomic.Bool               // true when pauseJob=true and Stop() was called
	prevFireTime atomic.Pointer[time.Time] // actual time of last trigger

	cancel context.CancelFunc // cancels the startup goroutine or SimpleTrigger ticker
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

		if err := completeExchange(exchange, c.processor.Process(exchange)); err != nil {
			logError("Cron processing error", "group", ep.group, "name", ep.name, "error", err)
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
			logError("Cron job add error", "group", ep.group, "name", ep.name, "error", err)
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

// fireSimpleJob executes the processor for a SimpleTrigger fire.
func (c *CronConsumer) fireSimpleJob(ctx, gCtx context.Context, now time.Time) {
	if c.paused.Load() {
		return
	}

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

	exchange := NewExchange(ctx)
	exchange.GetIn().SetHeader(CronFireTime, now)
	exchange.GetIn().SetHeader(CronScheduledFireTime, now)
	exchange.GetIn().SetHeader(CronNextFireTime, nextFireTime)
	exchange.GetIn().SetHeader(CronPreviousFireTime, prevTime)
	exchange.GetIn().SetHeader(CronTriggerName, ep.name)
	exchange.GetIn().SetHeader(CronTriggerGroup, ep.group)
	exchange.GetIn().SetHeader(CronRefireCount, n)

	if err := completeExchange(exchange, c.processor.Process(exchange)); err != nil {
		logError("Cron processing error", "group", ep.group, "name", ep.name, "error", err)
	}

	t := now
	c.prevFireTime.Store(&t)
}

// Stop stops the consumer according to the deleteJob / pauseJob options.
func (c *CronConsumer) Stop() error {
	ep := c.endpoint

	if ep.pauseJob {
		// Pause: the scheduler continues but jobs become no-ops
		c.paused.Store(true)
		return nil
	}

	// Full stop: cancel the startup goroutine or the ticker
	if c.cancel != nil {
		c.cancel()
	}

	// For CronTrigger, remove the entry from the scheduler
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.added {
		ep.component.scheduler.Remove(c.entryID)
		c.added = false
	}

	return nil
}
