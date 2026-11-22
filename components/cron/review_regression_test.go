package cron

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gocamel "github.com/tranchida/gocamel"
)

func TestReviewStopJoinsActiveCron(t *testing.T) {
	comp := NewCronComponent()
	defer comp.Stop()
	ep, err := comp.CreateEndpoint("cron://review?cron=*+*+*+*+*+*&triggerStartDelay=0&stateful=true")
	require.NoError(t, err)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	c, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { entered <- struct{}{}; <-release; return nil }))
	require.NoError(t, err)
	require.NoError(t, c.Start(context.Background()))
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		close(release)
		c.Stop()
		t.Fatal("job did not fire")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- c.Stop() }()
	select {
	case err := <-stopped:
		require.NoError(t, err)
		close(release)
		t.Fatal("Stop returned while the cron processor is still running")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Stop did not finish after the job completed")
	}
}

func TestCronConsumer_PauseJobStopDuringStartDelayCancelsGoroutine(t *testing.T) {
	comp := NewCronComponent()
	defer comp.Stop()

	ep, err := comp.CreateEndpoint("cron://pauseDelay?cron=*+*+*+*+*+*&triggerStartDelay=5000&pauseJob=true&deleteJob=false")
	require.NoError(t, err)

	c, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	require.NoError(t, err)

	require.NoError(t, c.Start(context.Background()))

	done := make(chan error, 1)
	go func() { done <- c.Stop() }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Stop with pauseJob=true did not cancel triggerStartDelay goroutine promptly")
	}

	cc := c.(*CronConsumer)
	cc.mu.Lock()
	added := cc.added
	cc.mu.Unlock()
	require.False(t, added, "paused consumer stopped during start delay must not add job")
}

func TestCronComponent_ConcurrentStartAndStopRace(t *testing.T) {
	comp := NewCronComponent()
	defer comp.Stop()

	ep, err := comp.CreateEndpoint("cron://race?cron=*+*+*+*+*+*&triggerStartDelay=1")
	require.NoError(t, err)

	c, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	require.NoError(t, err)

	for i := 0; i < 10; i++ {
		require.NoError(t, c.Start(context.Background()))
		done := make(chan struct{})
		go func() {
			_ = comp.Stop()
			close(done)
		}()
		require.NoError(t, c.Stop())
		<-done
	}
}

func TestCronComponent_StopDoesNotLetStaleConsumerDeleteNewRouteJob(t *testing.T) {
	comp := NewCronComponent()
	defer comp.Stop()

	ep1, err := comp.CreateEndpoint("cron://job1?cron=*+*+*+*+*+*&triggerStartDelay=0")
	require.NoError(t, err)
	c1, err := ep1.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	require.NoError(t, err)
	require.NoError(t, c1.Start(context.Background()))

	cc1 := c1.(*CronConsumer)
	require.Eventually(t, func() bool {
		cc1.mu.Lock()
		defer cc1.mu.Unlock()
		return cc1.added
	}, time.Second, 10*time.Millisecond)

	// Stop the component scheduler (rotates comp.scheduler to a fresh *cron.Cron).
	require.NoError(t, comp.Stop())

	var c2Fired atomic.Int64
	ep2, err := comp.CreateEndpoint("cron://job2?cron=*+*+*+*+*+*&triggerStartDelay=0")
	require.NoError(t, err)
	c2, err := ep2.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error {
		c2Fired.Add(1)
		return nil
	}))
	require.NoError(t, err)
	require.NoError(t, c2.Start(context.Background()))
	defer c2.Stop()

	cc2 := c2.(*CronConsumer)
	require.Eventually(t, func() bool {
		cc2.mu.Lock()
		defer cc2.mu.Unlock()
		return cc2.added
	}, time.Second, 10*time.Millisecond)

	cc2.mu.Lock()
	c2ID := cc2.entryID
	cc2.mu.Unlock()

	// Stopping the stale consumer from the previous scheduler must NOT remove
	// c2's EntryID on the new scheduler!
	require.NoError(t, c1.Stop())

	comp.mu.Lock()
	entry := comp.scheduler.Entry(c2ID)
	comp.mu.Unlock()
	assert.True(t, entry.Valid(), "stale consumer Stop must not remove new route's job from rotated scheduler")

	require.Eventually(t, func() bool {
		return c2Fired.Load() >= 1
	}, 2*time.Second, 20*time.Millisecond)
}

func TestCronConsumer_SerializedStartStopAndNoDuplicateJob(t *testing.T) {
	comp := NewCronComponent()
	defer comp.Stop()

	ep, err := comp.CreateEndpoint("cron://dup?cron=*+*+*+*+*+*&triggerStartDelay=0")
	require.NoError(t, err)

	c, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	require.NoError(t, err)

	// Duplicate Start calls must not create duplicate entries in the scheduler.
	require.NoError(t, c.Start(context.Background()))
	require.NoError(t, c.Start(context.Background()))

	cc := c.(*CronConsumer)
	require.Eventually(t, func() bool {
		cc.mu.Lock()
		defer cc.mu.Unlock()
		return cc.added
	}, time.Second, 10*time.Millisecond)

	comp.mu.Lock()
	entries := comp.scheduler.Entries()
	comp.mu.Unlock()
	assert.Len(t, entries, 1, "duplicate Start must not schedule duplicate cron jobs")

	require.NoError(t, c.Stop())

	// Cancelled context with zero triggerStartDelay must not add a job.
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, c.Start(cancelledCtx))
	time.Sleep(30 * time.Millisecond)
	require.NoError(t, c.Stop())

	comp.mu.Lock()
	entriesAfterCancel := comp.scheduler.Entries()
	comp.mu.Unlock()
	assert.Len(t, entriesAfterCancel, 0, "cancelled context must not leave a job in scheduler")

	// Concurrent Start/Stop calls must serialize cleanly without races or duplicates.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = c.Start(context.Background())
		}()
		go func() {
			defer wg.Done()
			_ = c.Stop()
		}()
	}
	wg.Wait()
	require.NoError(t, c.Stop())

	comp.mu.Lock()
	finalEntries := comp.scheduler.Entries()
	comp.mu.Unlock()
	assert.Len(t, finalEntries, 0)
}

func TestCronConsumer_SimpleTriggerRepeatZeroAndRestartAndCancelsCtx(t *testing.T) {
	t.Run("repeatCount zero fires nothing and returns immediately", func(t *testing.T) {
		comp := NewCronComponent()
		defer comp.Stop()

		ep, err := comp.CreateEndpoint("cron://zero?trigger.repeatInterval=10&trigger.repeatCount=0&triggerStartDelay=0")
		require.NoError(t, err)

		var calls atomic.Int64
		c, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error {
			calls.Add(1)
			return nil
		}))
		require.NoError(t, err)

		require.NoError(t, c.Start(context.Background()))
		time.Sleep(80 * time.Millisecond)
		cc := c.(*CronConsumer)
		assert.Equal(t, int64(0), calls.Load())
		assert.Equal(t, int64(0), cc.fireCount.Load(), "repeatCount=0 must not run ticker/increment fireCount")
		require.NoError(t, c.Stop())
	})

	t.Run("repeatCount resets on fresh Start and cancels exchange ctx on final fire", func(t *testing.T) {
		comp := NewCronComponent()
		defer comp.Stop()

		ep, err := comp.CreateEndpoint("cron://repeat?trigger.repeatInterval=15&trigger.repeatCount=2&triggerStartDelay=0")
		require.NoError(t, err)

		var mu sync.Mutex
		var contexts []context.Context
		var refires []int64
		c, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
			mu.Lock()
			contexts = append(contexts, ex.Context)
			if v, ok := ex.GetIn().GetHeader(CronRefireCount); ok {
				refires = append(refires, v.(int64))
			}
			mu.Unlock()
			return nil
		}))
		require.NoError(t, err)

		require.NoError(t, c.Start(context.Background()))
		require.Eventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(contexts) == 2
		}, time.Second, 10*time.Millisecond)

		// Final fire must cancel the SimpleTrigger's gCtx (the exchange's context).
		mu.Lock()
		lastCtx := contexts[1]
		mu.Unlock()
		select {
		case <-lastCtx.Done():
		case <-time.After(500 * time.Millisecond):
			t.Fatal("SimpleTrigger did not cancel gCtx after reaching repeatCount")
		}

		require.NoError(t, c.Stop())

		// Restarting must reset fireCount and fire 2 more times cleanly.
		require.NoError(t, c.Start(context.Background()))
		require.Eventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(contexts) == 4
		}, time.Second, 10*time.Millisecond)
		require.NoError(t, c.Stop())

		mu.Lock()
		assert.Equal(t, []int64{1, 2, 1, 2}, refires)
		mu.Unlock()
	})

	t.Run("Stop cancels in-flight SimpleTrigger exchange context", func(t *testing.T) {
		comp := NewCronComponent()
		defer comp.Stop()

		ep, err := comp.CreateEndpoint("cron://cancelctx?trigger.repeatInterval=10&triggerStartDelay=0")
		require.NoError(t, err)

		entered := make(chan struct{}, 1)
		c, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-ex.Context.Done()
			return nil
		}))
		require.NoError(t, err)

		require.NoError(t, c.Start(context.Background()))
		select {
		case <-entered:
		case <-time.After(time.Second):
			_ = c.Stop()
			t.Fatal("SimpleTrigger did not fire")
		}

		stopped := make(chan error, 1)
		go func() { stopped <- c.Stop() }()
		select {
		case err := <-stopped:
			require.NoError(t, err)
		case <-time.After(500 * time.Millisecond):
			t.Fatal("Stop did not cancel SimpleTrigger exchange context")
		}
	})
}

func TestCronComponent_RejectsUnspacedTZAndIntervalOverflowAndStopsSchedulerWhenIdle(t *testing.T) {
	comp := NewCronComponent()
	defer comp.Stop()

	invalidURIs := []string{
		"cron://tz1?cron=TZ=Europe/Paris",
		"cron://tz2?cron=CRON_TZ=UTC",
		"cron://tz3?cron=TZ=UTC+0+*+*+*+*+*&trigger.timeZone=Europe/Paris",
		"cron://num1?cron=0+*+*+*+*+*&triggerStartDelay=abc",
		"cron://num2?cron=0+*+*+*+*+*&triggerStartDelay=-5",
		"cron://num3?cron=0+*+*+*+*+*&triggerStartDelay=9223372036855",
		"cron://num4?trigger.repeatInterval=abc",
		"cron://num5?trigger.repeatInterval=0",
		"cron://num6?trigger.repeatInterval=-10",
		"cron://num7?trigger.repeatInterval=9223372036855",
		"cron://num8?trigger.repeatInterval=100&trigger.repeatCount=abc",
		"cron://num9?trigger.repeatInterval=100&trigger.repeatCount=-2",
	}

	for _, uri := range invalidURIs {
		t.Run(uri, func(t *testing.T) {
			require.NotPanics(t, func() {
				_, err := comp.CreateEndpoint(uri)
				assert.Error(t, err, "expected invalid cron URI %q to fail", uri)
			})
		})
	}

	t.Run("stops shared scheduler when last job removed", func(t *testing.T) {
		ep, err := comp.CreateEndpoint("cron://idle?cron=*+*+*+*+*+*&triggerStartDelay=0")
		require.NoError(t, err)
		c, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
		require.NoError(t, err)
		require.NoError(t, c.Start(context.Background()))

		cc := c.(*CronConsumer)
		require.Eventually(t, func() bool {
			cc.mu.Lock()
			defer cc.mu.Unlock()
			return cc.added
		}, time.Second, 10*time.Millisecond)

		comp.mu.Lock()
		assert.True(t, comp.started)
		comp.mu.Unlock()

		require.NoError(t, c.Stop())

		comp.mu.Lock()
		assert.False(t, comp.started, "shared scheduler must stop when active job count reaches 0")
		comp.mu.Unlock()
	})

	t.Run("honors deleteJob=false and cleans up pauseJob across restarts", func(t *testing.T) {
		epKeep, err := comp.CreateEndpoint("cron://keep?cron=*+*+*+*+*+*&triggerStartDelay=0&deleteJob=false")
		require.NoError(t, err)
		cKeep, err := epKeep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
		require.NoError(t, err)
		require.NoError(t, cKeep.Start(context.Background()))

		ccKeep := cKeep.(*CronConsumer)
		require.Eventually(t, func() bool {
			ccKeep.mu.Lock()
			defer ccKeep.mu.Unlock()
			return ccKeep.added
		}, time.Second, 10*time.Millisecond)

		require.NoError(t, cKeep.Stop())
		comp.mu.Lock()
		entriesAfterStop := comp.scheduler.Entries()
		comp.mu.Unlock()
		assert.Len(t, entriesAfterStop, 1, "deleteJob=false must keep job entry on Stop")

		// Restarting must remove old retained job rather than duplicating it.
		require.NoError(t, cKeep.Start(context.Background()))
		require.Eventually(t, func() bool {
			ccKeep.mu.Lock()
			defer ccKeep.mu.Unlock()
			return ccKeep.added
		}, time.Second, 10*time.Millisecond)
		comp.mu.Lock()
		entriesAfterRestart := comp.scheduler.Entries()
		comp.mu.Unlock()
		assert.Len(t, entriesAfterRestart, 1, "restart must replace retained job without duplicating")

		// Clean up
		require.NoError(t, cKeep.Stop())
		require.NoError(t, comp.Stop())
	})

	t.Run("rejects invalid booleans and cleans up pauseJob cron entry on restart", func(t *testing.T) {
		for _, badURI := range []string{
			"cron://b1?cron=0+*+*+*+*+*&deleteJob=maybe",
			"cron://b2?cron=0+*+*+*+*+*&pauseJob=maybe",
			"cron://b3?cron=0+*+*+*+*+*&stateful=maybe",
		} {
			_, err := comp.CreateEndpoint(badURI)
			assert.Error(t, err)
		}

		epPause, err := comp.CreateEndpoint("cron://pauseCron?cron=*+*+*+*+*+*&triggerStartDelay=0&pauseJob=true")
		require.NoError(t, err)
		assert.Contains(t, epPause.URI(), "cron://pauseCron")

		cPause, err := epPause.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
		require.NoError(t, err)
		require.NoError(t, cPause.Start(context.Background()))

		ccPause := cPause.(*CronConsumer)
		require.Eventually(t, func() bool {
			ccPause.mu.Lock()
			defer ccPause.mu.Unlock()
			return ccPause.added
		}, time.Second, 10*time.Millisecond)

		require.NoError(t, cPause.Stop())
		require.NoError(t, cPause.Start(context.Background()))
		require.Eventually(t, func() bool {
			ccPause.mu.Lock()
			defer ccPause.mu.Unlock()
			return ccPause.added
		}, time.Second, 10*time.Millisecond)

		comp.mu.Lock()
		entries := comp.scheduler.Entries()
		comp.mu.Unlock()
		assert.Len(t, entries, 1, "restarting a pauseJob consumer must replace previous entry without duplicating")

		require.NoError(t, cPause.Stop())
		require.NoError(t, comp.Stop())
	})
}

func TestCronComponent_StopWaitsForConcurrentRemoveJobDrain(t *testing.T) {
	comp := NewCronComponent()
	ep, err := comp.CreateEndpoint("cron://drainRace?cron=*+*+*+*+*+*&triggerStartDelay=0")
	require.NoError(t, err)

	jobEntered := make(chan struct{})
	releaseJob := make(chan struct{})
	var closeReleaseOnce sync.Once
	release := func() {
		closeReleaseOnce.Do(func() { close(releaseJob) })
	}
	t.Cleanup(release)

	var jobOnce sync.Once
	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error {
		jobOnce.Do(func() {
			close(jobEntered)
			<-releaseJob
		})
		return nil
	}))
	require.NoError(t, err)
	require.NoError(t, consumer.Start(context.Background()))

	select {
	case <-jobEntered:
	case <-time.After(3 * time.Second):
		release()
		_ = consumer.Stop()
		_ = comp.Stop()
		t.Fatal("cron job did not fire in time")
	}

	consumerStopDone := make(chan error, 1)
	go func() {
		consumerStopDone <- consumer.Stop()
	}()

	// Wait until removeJob has rotated the scheduler and cleared comp.started,
	// while still parked waiting on <-stopCtx.Done().
	require.Eventually(t, func() bool {
		comp.mu.Lock()
		defer comp.mu.Unlock()
		return !comp.started
	}, 2*time.Second, 5*time.Millisecond)

	compStopDone := make(chan error, 1)
	go func() {
		compStopDone <- comp.Stop()
	}()

	select {
	case err := <-compStopDone:
		release()
		<-consumerStopDone
		t.Fatalf("CronComponent.Stop returned (%v) while concurrent removeJob was still draining in-flight cron job", err)
	case <-time.After(100 * time.Millisecond):
	}

	release()
	require.NoError(t, <-consumerStopDone)
	select {
	case err := <-compStopDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("CronComponent.Stop did not complete after in-flight cron job finished")
	}
}
