package sftp

import (
	"net/url"
	"os"
	"testing"
)

func TestGetHostKeyCallback(t *testing.T) {
	e := &SFTPEndpoint{}

	// Case 1: strictHostKeyChecking=false
	u1, _ := url.Parse("sftp://example.com?strictHostKeyChecking=false")
	cb1, err := e.getHostKeyCallback(u1)
	if err != nil {
		t.Errorf("Unexpected error for strictHostKeyChecking=false: %v", err)
	}
	if cb1 == nil {
		t.Error("Expected callback to be non-nil for strictHostKeyChecking=false")
	}

	// Case 2: strictHostKeyChecking=true (default) but no knownHostsFile
	u2, _ := url.Parse("sftp://example.com")
	_, err = e.getHostKeyCallback(u2)
	if err == nil {
		t.Error("Expected error for strictHostKeyChecking=true without knownHostsFile")
	}

	// Case 3: strictHostKeyChecking=true with valid knownHostsFile
	tmpFile, err := os.CreateTemp("", "known_hosts")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	u3, _ := url.Parse("sftp://example.com?knownHostsFile=" + tmpFile.Name())
	cb3, err := e.getHostKeyCallback(u3)
	if err != nil {
		t.Errorf("Unexpected error for strictHostKeyChecking=true with knownHostsFile: %v", err)
	}
	if cb3 == nil {
		t.Error("Expected callback to be non-nil for strictHostKeyChecking=true with knownHostsFile")
	}
}

func TestSFTPConsumer_ReadLockOptions(t *testing.T) {
	c := NewSFTPComponent()
	ep, err := c.CreateEndpoint("sftp://example.com/incoming?readLock=changed&readLockTimeout=20s&readLockCheckInterval=500ms&readLockMinLength=512&readLockMinAge=3s")
	if err != nil {
		t.Fatalf("unexpected error creating endpoint: %v", err)
	}

	consumer, err := ep.CreateConsumer(nil)
	if err != nil {
		t.Fatalf("unexpected error creating consumer: %v", err)
	}

	sftpConsumer, ok := consumer.(*SFTPConsumer)
	if !ok {
		t.Fatalf("expected *SFTPConsumer, got %T", consumer)
	}

	if sftpConsumer.opts.ReadLock != "changed" {
		t.Errorf("expected readLock=changed, got %v", sftpConsumer.opts.ReadLock)
	}
	if sftpConsumer.opts.ReadLockTimeout != 20*1000*1000*1000 { // 20s
		t.Errorf("expected readLockTimeout=20s, got %v", sftpConsumer.opts.ReadLockTimeout)
	}
	if sftpConsumer.opts.ReadLockCheckInterval != 500*1000*1000 { // 500ms
		t.Errorf("expected readLockCheckInterval=500ms, got %v", sftpConsumer.opts.ReadLockCheckInterval)
	}
	if sftpConsumer.opts.ReadLockMinLength != 512 {
		t.Errorf("expected readLockMinLength=512, got %v", sftpConsumer.opts.ReadLockMinLength)
	}
	if sftpConsumer.opts.ReadLockMinAge != 3*1000*1000*1000 { // 3s
		t.Errorf("expected readLockMinAge=3s, got %v", sftpConsumer.opts.ReadLockMinAge)
	}
}
