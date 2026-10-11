package cron

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gocamel "github.com/tranchida/gocamel"
)

func TestCronComponent_CreateEndpoint_CronTrigger(t *testing.T) {
	comp := NewCronComponent()
	ep, err := comp.CreateEndpoint("cron://myGroup/myTimer?cron=0+*+*+*+*+*")
	require.NoError(t, err)

	qep := ep.(*CronEndpoint)
	assert.Equal(t, "myGroup", qep.group)
	assert.Equal(t, "myTimer", qep.name)
	assert.Equal(t, "0 * * * * *", qep.cronExpr)
	assert.True(t, qep.deleteJob)
	assert.False(t, qep.pauseJob)
	assert.False(t, qep.stateful)
}

func TestCronComponent_CreateEndpoint_SimpleTrigger(t *testing.T) {
	comp := NewCronComponent()
	ep, err := comp.CreateEndpoint("cron://myTimer?trigger.repeatInterval=2000&trigger.repeatCount=3")
	require.NoError(t, err)

	qep := ep.(*CronEndpoint)
	assert.Equal(t, "Camel", qep.group)
	assert.Equal(t, "myTimer", qep.name)
	assert.Equal(t, "", qep.cronExpr)
	assert.Equal(t, int64(2000), qep.repeatInterval)
	assert.Equal(t, int64(3), qep.repeatCount)
}

func TestCronComponent_CreateEndpoint_Options(t *testing.T) {
	comp := NewCronComponent()
	ep, err := comp.CreateEndpoint(
		"cron://myTimer?cron=0+*+*+*+*+*" +
			"&trigger.timeZone=Europe/Paris" +
			"&triggerStartDelay=1000" +
			"&deleteJob=false" +
			"&pauseJob=true" +
			"&stateful=true",
	)
	require.NoError(t, err)

	qep := ep.(*CronEndpoint)
	assert.Equal(t, "Europe/Paris", qep.timezone)
	assert.Equal(t, 1000*time.Millisecond, qep.triggerStartDelay)
	assert.False(t, qep.deleteJob)
	assert.True(t, qep.pauseJob)
	assert.True(t, qep.stateful)
}

func TestCronComponent_CreateEndpoint_MissingName(t *testing.T) {
	comp := NewCronComponent()
	_, err := comp.CreateEndpoint("cron://?cron=0+*+*+*+*+*")
	assert.Error(t, err)
}

func TestCronComponent_CreateEndpoint_InvalidCron(t *testing.T) {
	comp := NewCronComponent()

	tests := []struct {
		name string
		uri  string
	}{
		{"not enough fields", "cron://myTimer?cron=0+*+*+*"},
		{"too many fields", "cron://myTimer?cron=0+*+*+*+*+*+*"},
		{"invalid value", "cron://myTimer?cron=99+*+*+*+*+*"},
		{"garbage", "cron://myTimer?cron=notacron"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := comp.CreateEndpoint(tt.uri)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid cron expression")
		})
	}
}

func TestCronComponent_CreateEndpoint_InvalidTimeZone(t *testing.T) {
	comp := NewCronComponent()

	_, err := comp.CreateEndpoint("cron://myTimer?cron=0+*+*+*+*+*&trigger.timeZone=Not/AZone")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid trigger.timeZone")

	// A timezone with a space would corrupt the "TZ=<tz> <spec>" expression.
	_, err = comp.CreateEndpoint("cron://myTimer?cron=0+*+*+*+*+*&trigger.timeZone=Europe%20Paris")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "whitespace not allowed")
}

func TestCronComponent_CreateEndpoint_ValidCronAndTimeZone(t *testing.T) {
	comp := NewCronComponent()

	tests := []string{
		"cron://myTimer?cron=0/5+*+*+*+*+*",
		"cron://myTimer?cron=0+0+12+*+*+?",
		"cron://myTimer?cron=@hourly",
		"cron://myTimer?cron=0+*+*+*+*+*&trigger.timeZone=Europe/Paris",
		"cron://myTimer?trigger.repeatInterval=5000", // SimpleTrigger: no cron to validate
	}

	for _, uri := range tests {
		t.Run(uri, func(t *testing.T) {
			ep, err := comp.CreateEndpoint(uri)
			require.NoError(t, err)
			assert.NotNil(t, ep)
		})
	}
}

func TestCronEndpoint_NoProducer(t *testing.T) {
	comp := NewCronComponent()
	ep, _ := comp.CreateEndpoint("cron://myTimer?cron=0+*+*+*+*+*")
	_, err := ep.CreateProducer()
	assert.Error(t, err)
}

func TestCronConsumer_BuildSpec_NoCronNoInterval(t *testing.T) {
	comp := NewCronComponent()
	ep, _ := comp.CreateEndpoint("cron://myTimer?triggerStartDelay=0")
	consumer, _ := ep.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error { return nil }))
	qc := consumer.(*CronConsumer)
	_, err := qc.buildSpec()
	assert.Error(t, err)
}

func TestCronConsumer_SimpleTrigger_Fires(t *testing.T) {
	var count atomic.Int64

	comp := NewCronComponent()
	// @every 100ms, triggerStartDelay=0
	ep, err := comp.CreateEndpoint("cron://test?trigger.repeatInterval=100&triggerStartDelay=0")
	require.NoError(t, err)

	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		count.Add(1)
		// Verify that headers are present
		_, hasFireTime := e.GetIn().GetHeader(CronFireTime)
		_, hasTriggerName := e.GetIn().GetHeader(CronTriggerName)
		_, hasTriggerGroup := e.GetIn().GetHeader(CronTriggerGroup)
		if !hasFireTime || !hasTriggerName || !hasTriggerGroup {
			t.Errorf("missing Cron headers")
		}
		return nil
	}))
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, consumer.Start(ctx))
	defer consumer.Stop()

	// Wait for at least 3 triggers
	assert.Eventually(t, func() bool {
		return count.Load() >= 3
	}, 2*time.Second, 50*time.Millisecond)
}

func TestCronConsumer_RepeatCount(t *testing.T) {
	var count atomic.Int64

	comp := NewCronComponent()
	ep, err := comp.CreateEndpoint("cron://test?trigger.repeatInterval=50&trigger.repeatCount=3&triggerStartDelay=0")
	require.NoError(t, err)

	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		count.Add(1)
		return nil
	}))
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, consumer.Start(ctx))
	defer consumer.Stop()

	// Wait for the 3 triggers to be completed
	time.Sleep(500 * time.Millisecond)

	// Must not exceed repeatCount=3
	assert.LessOrEqual(t, count.Load(), int64(3))
}

func TestCronConsumer_PauseJob(t *testing.T) {
	var count atomic.Int64

	comp := NewCronComponent()
	ep, err := comp.CreateEndpoint("cron://test?trigger.repeatInterval=50&triggerStartDelay=0&pauseJob=true&deleteJob=false")
	require.NoError(t, err)

	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		count.Add(1)
		return nil
	}))
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, consumer.Start(ctx))

	// Let it run for a bit
	time.Sleep(200 * time.Millisecond)
	countBefore := count.Load()
	assert.Greater(t, countBefore, int64(0))

	// Pause
	require.NoError(t, consumer.Stop())
	time.Sleep(200 * time.Millisecond)
	countAfterStop := count.Load()

	// Allow time to pass to verify that it no longer triggers
	time.Sleep(200 * time.Millisecond)

	// The counter must not increase further
	assert.Equal(t, countAfterStop, count.Load())
}

func TestCronConsumer_Headers(t *testing.T) {
	done := make(chan *gocamel.Exchange, 1)

	comp := NewCronComponent()
	ep, err := comp.CreateEndpoint("cron://grp/myTimer?trigger.repeatInterval=50&triggerStartDelay=0")
	require.NoError(t, err)

	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		select {
		case done <- e:
		default:
		}
		return nil
	}))
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, consumer.Start(ctx))
	defer consumer.Stop()

	var exchange *gocamel.Exchange
	select {
	case exchange = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: no Cron exchange received")
	}

	name, ok := exchange.GetIn().GetHeader(CronTriggerName)
	assert.True(t, ok)
	assert.Equal(t, "myTimer", name)

	group, ok := exchange.GetIn().GetHeader(CronTriggerGroup)
	assert.True(t, ok)
	assert.Equal(t, "grp", group)

	_, ok = exchange.GetIn().GetHeader(CronFireTime)
	assert.True(t, ok)

	_, ok = exchange.GetIn().GetHeader(CronNextFireTime)
	assert.True(t, ok)
}

func TestCronComponent_SharedScheduler(t *testing.T) {
	// Two routes using the same component share the same scheduler
	comp := NewCronComponent()

	var count1, count2 atomic.Int64

	ep1, _ := comp.CreateEndpoint("cron://t1?trigger.repeatInterval=50&triggerStartDelay=0")
	ep2, _ := comp.CreateEndpoint("cron://t2?trigger.repeatInterval=50&triggerStartDelay=0")

	c1, _ := ep1.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error { count1.Add(1); return nil }))
	c2, _ := ep2.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error { count2.Add(1); return nil }))

	ctx := context.Background()
	require.NoError(t, c1.Start(ctx))
	require.NoError(t, c2.Start(ctx))
	defer c1.Stop()
	defer c2.Stop()

	// Both routes must trigger
	assert.Eventually(t, func() bool {
		return count1.Load() >= 2 && count2.Load() >= 2
	}, 2*time.Second, 50*time.Millisecond)
}

func TestCronComponent_Stop(t *testing.T) {
	comp := NewCronComponent()
	comp.ensureStarted()
	assert.True(t, comp.started)
	require.NoError(t, comp.Stop())
	assert.False(t, comp.started)
}
