package gocamel

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuartzComponent_CreateEndpoint_CronTrigger(t *testing.T) {
	comp := NewQuartzComponent()
	ep, err := comp.CreateEndpoint("quartz://myGroup/myTimer?cron=0+*+*+*+*+*")
	require.NoError(t, err)

	qep := ep.(*QuartzEndpoint)
	assert.Equal(t, "myGroup", qep.group)
	assert.Equal(t, "myTimer", qep.name)
	assert.Equal(t, "0 * * * * *", qep.cronExpr)
	assert.True(t, qep.deleteJob)
	assert.False(t, qep.pauseJob)
	assert.False(t, qep.stateful)
}

func TestQuartzComponent_CreateEndpoint_SimpleTrigger(t *testing.T) {
	comp := NewQuartzComponent()
	ep, err := comp.CreateEndpoint("quartz://myTimer?trigger.repeatInterval=2000&trigger.repeatCount=3")
	require.NoError(t, err)

	qep := ep.(*QuartzEndpoint)
	assert.Equal(t, "Camel", qep.group)
	assert.Equal(t, "myTimer", qep.name)
	assert.Equal(t, "", qep.cronExpr)
	assert.Equal(t, int64(2000), qep.repeatInterval)
	assert.Equal(t, int64(3), qep.repeatCount)
}

func TestQuartzComponent_CreateEndpoint_Options(t *testing.T) {
	comp := NewQuartzComponent()
	ep, err := comp.CreateEndpoint(
		"quartz://myTimer?cron=0+*+*+*+*+*" +
			"&trigger.timeZone=Europe/Paris" +
			"&triggerStartDelay=1000" +
			"&deleteJob=false" +
			"&pauseJob=true" +
			"&stateful=true",
	)
	require.NoError(t, err)

	qep := ep.(*QuartzEndpoint)
	assert.Equal(t, "Europe/Paris", qep.timezone)
	assert.Equal(t, 1000*time.Millisecond, qep.triggerStartDelay)
	assert.False(t, qep.deleteJob)
	assert.True(t, qep.pauseJob)
	assert.True(t, qep.stateful)
}

func TestQuartzComponent_CreateEndpoint_MissingName(t *testing.T) {
	comp := NewQuartzComponent()
	_, err := comp.CreateEndpoint("quartz://?cron=0+*+*+*+*+*")
	assert.Error(t, err)
}

func TestQuartzEndpoint_NoProducer(t *testing.T) {
	comp := NewQuartzComponent()
	ep, _ := comp.CreateEndpoint("quartz://myTimer?cron=0+*+*+*+*+*")
	_, err := ep.CreateProducer()
	assert.Error(t, err)
}

func TestQuartzConsumer_BuildSpec_NoCronNoInterval(t *testing.T) {
	comp := NewQuartzComponent()
	ep, _ := comp.CreateEndpoint("quartz://myTimer?triggerStartDelay=0")
	consumer, _ := ep.CreateConsumer(ProcessorFunc(func(e *Exchange) error { return nil }))
	qc := consumer.(*QuartzConsumer)
	_, err := qc.buildSpec()
	assert.Error(t, err)
}

func TestQuartzConsumer_SimpleTrigger_Fires(t *testing.T) {
	var count atomic.Int64

	comp := NewQuartzComponent()
	// @every 100ms, triggerStartDelay=0
	ep, err := comp.CreateEndpoint("quartz://test?trigger.repeatInterval=100&triggerStartDelay=0")
	require.NoError(t, err)

	consumer, err := ep.CreateConsumer(ProcessorFunc(func(e *Exchange) error {
		count.Add(1)
		// Vérifier que les en-têtes sont présents
		_, hasFireTime := e.GetIn().GetHeader(QuartzFireTime)
		_, hasTriggerName := e.GetIn().GetHeader(QuartzTriggerName)
		_, hasTriggerGroup := e.GetIn().GetHeader(QuartzTriggerGroup)
		if !hasFireTime || !hasTriggerName || !hasTriggerGroup {
			t.Errorf("en-têtes Quartz manquants")
		}
		return nil
	}))
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, consumer.Start(ctx))
	defer consumer.Stop()

	// Attendre au moins 3 déclenchements
	assert.Eventually(t, func() bool {
		return count.Load() >= 3
	}, 2*time.Second, 50*time.Millisecond)
}

func TestQuartzConsumer_RepeatCount(t *testing.T) {
	var count atomic.Int64

	comp := NewQuartzComponent()
	ep, err := comp.CreateEndpoint("quartz://test?trigger.repeatInterval=50&trigger.repeatCount=3&triggerStartDelay=0")
	require.NoError(t, err)

	consumer, err := ep.CreateConsumer(ProcessorFunc(func(e *Exchange) error {
		count.Add(1)
		return nil
	}))
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, consumer.Start(ctx))
	defer consumer.Stop()

	// Attendre que les 3 déclenchements soient effectués
	time.Sleep(500 * time.Millisecond)

	// Ne doit pas dépasser repeatCount=3
	assert.LessOrEqual(t, count.Load(), int64(3))
}

func TestQuartzConsumer_PauseJob(t *testing.T) {
	var count atomic.Int64

	comp := NewQuartzComponent()
	ep, err := comp.CreateEndpoint("quartz://test?trigger.repeatInterval=50&triggerStartDelay=0&pauseJob=true&deleteJob=false")
	require.NoError(t, err)

	consumer, err := ep.CreateConsumer(ProcessorFunc(func(e *Exchange) error {
		count.Add(1)
		return nil
	}))
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, consumer.Start(ctx))

	// Laisser tourner un peu
	time.Sleep(200 * time.Millisecond)
	countBefore := count.Load()
	assert.Greater(t, countBefore, int64(0))

	// Pause
	require.NoError(t, consumer.Stop())
	time.Sleep(200 * time.Millisecond)

	// Le compteur ne doit plus augmenter
	assert.Equal(t, countBefore, count.Load())
}

func TestQuartzConsumer_Headers(t *testing.T) {
	done := make(chan *Exchange, 1)

	comp := NewQuartzComponent()
	ep, err := comp.CreateEndpoint("quartz://grp/myTimer?trigger.repeatInterval=50&triggerStartDelay=0")
	require.NoError(t, err)

	consumer, err := ep.CreateConsumer(ProcessorFunc(func(e *Exchange) error {
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

	var exchange *Exchange
	select {
	case exchange = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: aucun échange Quartz reçu")
	}

	name, ok := exchange.GetIn().GetHeader(QuartzTriggerName)
	assert.True(t, ok)
	assert.Equal(t, "myTimer", name)

	group, ok := exchange.GetIn().GetHeader(QuartzTriggerGroup)
	assert.True(t, ok)
	assert.Equal(t, "grp", group)

	_, ok = exchange.GetIn().GetHeader(QuartzFireTime)
	assert.True(t, ok)

	_, ok = exchange.GetIn().GetHeader(QuartzNextFireTime)
	assert.True(t, ok)
}

func TestQuartzComponent_SharedScheduler(t *testing.T) {
	// Deux routes utilisant le même composant partagent le même scheduler
	comp := NewQuartzComponent()

	var count1, count2 atomic.Int64

	ep1, _ := comp.CreateEndpoint("quartz://t1?trigger.repeatInterval=50&triggerStartDelay=0")
	ep2, _ := comp.CreateEndpoint("quartz://t2?trigger.repeatInterval=50&triggerStartDelay=0")

	c1, _ := ep1.CreateConsumer(ProcessorFunc(func(e *Exchange) error { count1.Add(1); return nil }))
	c2, _ := ep2.CreateConsumer(ProcessorFunc(func(e *Exchange) error { count2.Add(1); return nil }))

	ctx := context.Background()
	require.NoError(t, c1.Start(ctx))
	require.NoError(t, c2.Start(ctx))
	defer c1.Stop()
	defer c2.Stop()

	// Les deux routes doivent se déclencher
	assert.Eventually(t, func() bool {
		return count1.Load() >= 2 && count2.Load() >= 2
	}, 2*time.Second, 50*time.Millisecond)
}
