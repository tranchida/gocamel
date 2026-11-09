package smb

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tranchida/gocamel"
)

func TestSMBConsumer_StopWaitsForPollingWorker(t *testing.T) {
	consumer := &SMBConsumer{}
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

func TestSMBConsumer_ReadLockOptions(t *testing.T) {
	c := NewSMBComponent()
	ep, err := c.CreateEndpoint("smb://example.com/share/folder?readLock=markerFile&readLockTimeout=30s&readLockCheckInterval=2s&readLockMinLength=100")
	if err != nil {
		t.Fatalf("unexpected error creating endpoint: %v", err)
	}

	consumer, err := ep.CreateConsumer(nil)
	if err != nil {
		t.Fatalf("unexpected error creating consumer: %v", err)
	}

	smbConsumer, ok := consumer.(*SMBConsumer)
	if !ok {
		t.Fatalf("expected *SMBConsumer, got %T", consumer)
	}

	if smbConsumer.opts.ReadLock != "markerFile" {
		t.Errorf("expected readLock=markerFile, got %v", smbConsumer.opts.ReadLock)
	}
	if smbConsumer.opts.ReadLockTimeout != 30*time.Second {
		t.Errorf("expected readLockTimeout=30s, got %v", smbConsumer.opts.ReadLockTimeout)
	}
	if smbConsumer.opts.ReadLockCheckInterval != 2*time.Second {
		t.Errorf("expected readLockCheckInterval=2s, got %v", smbConsumer.opts.ReadLockCheckInterval)
	}
	if smbConsumer.opts.ReadLockMinLength != 100 {
		t.Errorf("expected readLockMinLength=100, got %v", smbConsumer.opts.ReadLockMinLength)
	}
}

func TestSMBEndpoint_ConnectHandshakeDeadline(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer ln.Close()

	// Accept connection and hold it open without responding so SMB handshake stalls
	done := make(chan struct{})
	defer close(done)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		<-done
	}()

	c := NewSMBComponent()
	ep, err := c.CreateEndpoint("smb://" + ln.Addr().String() + "/share/folder?connectTimeout=100ms")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	smbEp := ep.(*SMBEndpoint)

	start := time.Now()
	_, err = smbEp.connect()
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected connect to fail on stalled SMB handshake")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("connect took too long (%v), deadline was not applied", elapsed)
	}
}

func TestSMBProducer_SendAfterStopFailsWithoutDialing(t *testing.T) {
	c := NewSMBComponent()
	ep, err := c.CreateEndpoint("smb://127.0.0.1:1/share/out?disconnect=false&connectTimeout=50ms")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	prod, err := ep.CreateProducer()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := prod.Start(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := prod.Stop(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("hello")
	ex.SetHeader(gocamel.CamelFileName, "test.txt")
	err = prod.Send(ex)
	if err == nil || !strings.Contains(err.Error(), "smb producer is stopped") {
		t.Fatalf("expected 'smb producer is stopped' error, got: %v", err)
	}
}

func TestSMBConsumer_ConcurrentStartStop(t *testing.T) {
	c := NewSMBComponent()
	ep, err := c.CreateEndpoint("smb://127.0.0.1:1/share/in?initialDelay=1h&delay=1h")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cons, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
		return nil
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = cons.Start(context.Background())
		}()
		go func() {
			defer wg.Done()
			_ = cons.Stop()
		}()
	}
	wg.Wait()
	_ = cons.Stop()
}

func TestSMBConsumer_StopUnblocksStalledHandshake(t *testing.T) {
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

	c := NewSMBComponent()
	ep, err := c.CreateEndpoint(fmt.Sprintf(
		"smb://%s:%s/inbox?connectTimeout=10000&initialDelay=0s", host, port))
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
