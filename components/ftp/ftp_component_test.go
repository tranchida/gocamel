package ftp

import (
	"strings"
	"testing"
	"time"

	gocamel "github.com/tranchida/gocamel"
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

func TestFTPEndpoint_RedactsCredentialsInURI(t *testing.T) {
	c := NewFTPComponent()
	ep, err := c.CreateEndpoint("ftp://admin:topsecret@example.com/incoming?password=querysecret")
	if err != nil {
		t.Fatalf("unexpected error creating endpoint: %v", err)
	}
	got := ep.URI()
	if strings.Contains(got, "topsecret") || strings.Contains(got, "querysecret") {
		t.Errorf("expected credentials to be redacted in URI(), got %q", got)
	}
}

func TestFTPEndpoint_RejectsMoveAndMoveFailedTraversal(t *testing.T) {
	c := NewFTPComponent()
	for _, uri := range []string{
		"ftp://example.com/incoming?move=../outside",
		"ftp://example.com/incoming?move=a/../../outside",
		"ftp://example.com/incoming?moveFailed=../failed",
		"ftp://example.com/incoming?moveFailed=a/b/../../../failed",
	} {
		if _, err := c.CreateEndpoint(uri); err == nil {
			t.Errorf("expected CreateEndpoint(%q) to reject path traversal", uri)
		}
	}

	ep, err := c.CreateEndpoint("ftp://example.com/incoming?move=.done&moveFailed=.error")
	if err != nil {
		t.Fatalf("expected valid move/moveFailed to be accepted: %v", err)
	}
	if _, err := ep.CreateConsumer(nil); err != nil {
		t.Fatalf("expected CreateConsumer with valid move/moveFailed to succeed: %v", err)
	}
}

func TestFTPEndpoint_DefaultMaxMessageSize(t *testing.T) {
	c := NewFTPComponent()
	ep, err := c.CreateEndpoint("ftp://example.com/incoming")
	if err != nil {
		t.Fatalf("unexpected error creating endpoint: %v", err)
	}
	if ep.(*FTPEndpoint).maxMessageSize != gocamel.DefaultMaxBodySize {
		t.Errorf("expected default maxMessageSize=%d, got %d", gocamel.DefaultMaxBodySize, ep.(*FTPEndpoint).maxMessageSize)
	}
}

