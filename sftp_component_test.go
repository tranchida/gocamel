package gocamel

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
