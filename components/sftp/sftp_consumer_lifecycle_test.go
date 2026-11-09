package sftp

import (
	"context"
	"fmt"
	"net"
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

func TestSFTPConsumer_StopUnblocksStalledHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	host, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split address: %v", err)
	}

	accepted := make(chan struct{})
	release := make(chan struct{})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		close(accepted)
		<-release
	}()
	defer func() {
		close(release)
		_ = ln.Close()
		select {
		case <-serverDone:
		case <-time.After(time.Second):
		}
	}()

	c := NewSFTPComponent()
	ep, err := c.CreateEndpoint(fmt.Sprintf(
		"sftp://%s:%s/inbox?connectTimeout=10000&initialDelay=0s&strictHostKeyChecking=false", host, port))
	if err != nil {
		t.Fatalf("create endpoint: %v", err)
	}
	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	if err != nil {
		t.Fatalf("create consumer: %v", err)
	}
	if err := consumer.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	select {
	case <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("consumer did not connect to the fake server")
	}

	stopped := make(chan error, 1)
	go func() { stopped <- consumer.Stop() }()

	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop blocked on a server that never completed the handshake")
	}
}
