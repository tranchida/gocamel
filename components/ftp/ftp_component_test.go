package ftp

import (
	"testing"
	"time"
)

func TestFTPConsumer_StopWaitsForPollingWorker(t *testing.T) {
	consumer := &FTPConsumer{}
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

func TestFTPConsumer_ReadLockOptions(t *testing.T) {
	c := NewFTPComponent()
	ep, err := c.CreateEndpoint("ftp://example.com/incoming?readLock=rename&readLockTimeout=10s&readLockCheckInterval=1s")
	if err != nil {
		t.Fatalf("unexpected error creating endpoint: %v", err)
	}

	consumer, err := ep.CreateConsumer(nil)
	if err != nil {
		t.Fatalf("unexpected error creating consumer: %v", err)
	}

	ftpConsumer, ok := consumer.(*FTPConsumer)
	if !ok {
		t.Fatalf("expected *FTPConsumer, got %T", consumer)
	}

	if ftpConsumer.opts.ReadLock != "rename" {
		t.Errorf("expected readLock=rename, got %v", ftpConsumer.opts.ReadLock)
	}
	if ftpConsumer.opts.ReadLockTimeout != 10*time.Second {
		t.Errorf("expected readLockTimeout=10s, got %v", ftpConsumer.opts.ReadLockTimeout)
	}
	if ftpConsumer.opts.ReadLockCheckInterval != 1*time.Second {
		t.Errorf("expected readLockCheckInterval=1s, got %v", ftpConsumer.opts.ReadLockCheckInterval)
	}
}
