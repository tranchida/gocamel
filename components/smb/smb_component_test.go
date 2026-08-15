package smb

import (
	"testing"
	"time"
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
