package sftp

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	gocamel "github.com/tranchida/gocamel"
)

func TestSFTPConsumer_StopWaitsForPollingWorker(t *testing.T) {
	consumer := &SFTPConsumer{}
	consumer.wg.Add(1)
	stopped := make(chan struct{})
	go func() {
		_ = consumer.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		t.Fatal("Stop returned while polling worker was still running")
	case <-time.After(20 * time.Millisecond):
	}

	consumer.wg.Done()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return after polling worker exited")
	}
}

func TestSFTPProducer_SendAfterStopFailsWithoutDialing(t *testing.T) {
	c := NewSFTPComponent()
	ep, err := c.CreateEndpoint("sftp://127.0.0.1:1/file.txt?strictHostKeyChecking=false&connectTimeout=100ms")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	prod, err := ep.CreateProducer()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := prod.Stop(); err != nil {
		t.Fatalf("unexpected Stop error: %v", err)
	}
	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("data")
	err = prod.Send(ex)
	if err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("expected stopped producer error, got %v", err)
	}
}

func TestSFTPConsumer_ConcurrentStartStop(t *testing.T) {
	c := NewSFTPComponent()
	ep, err := c.CreateEndpoint("sftp://127.0.0.1:1/incoming?strictHostKeyChecking=false&initialDelay=5s")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = consumer.Start(context.Background())
		}()
		go func() {
			defer wg.Done()
			_ = consumer.Stop()
		}()
	}
	wg.Wait()
	_ = consumer.Stop()
}
