package cron

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	gocamel "gitlab.com/tranchida/gocamel"
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
