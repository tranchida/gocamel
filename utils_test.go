package gocamel

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRedactURI verifies that credentials are masked both in the userinfo
// and in sensitive query parameters, while non-sensitive options and
// non-sensitive URIs are preserved untouched.
func TestRedactURI(t *testing.T) {
	tests := []struct {
		name  string
		uri   string
		keys  []string
		check func(t *testing.T, got string)
	}{
		{
			name: "userinfo password masked",
			uri:  "sftp://alice:s3cr3t@example.com:22/inbox",
			check: func(t *testing.T, got string) {
				assert.Contains(t, got, "alice:xxxxx@example.com")
				assert.NotContains(t, got, "s3cr3t")
			},
		},
		{
			name: "userinfo without password untouched",
			uri:  "sftp://alice@example.com/inbox",
			check: func(t *testing.T, got string) {
				assert.Equal(t, "sftp://alice@example.com/inbox", got)
			},
		},
		{
			name: "password query param masked",
			uri:  "smtp://mail.example.com?username=bob&password=hunter2&timeout=5000",
			check: func(t *testing.T, got string) {
				assert.Contains(t, got, "password=xxxxx")
				assert.Contains(t, got, "username=bob")
				assert.Contains(t, got, "timeout=5000")
				assert.NotContains(t, got, "hunter2")
			},
		},
		{
			name: "apiKey masked case-insensitively",
			uri:  "openai://gpt?apiKey=sk-live-key&model=gpt-4",
			check: func(t *testing.T, got string) {
				assert.Contains(t, got, "apiKey=xxxxx")
				assert.Contains(t, got, "model=gpt-4")
				assert.NotContains(t, got, "sk-live-key")
			},
		},
		{
			name: "authorizationToken masked",
			uri:  "telegram://bots?authorizationToken=123:ABC&chatId=42",
			check: func(t *testing.T, got string) {
				assert.Contains(t, got, "authorizationToken=xxxxx")
				assert.Contains(t, got, "chatId=42")
				assert.NotContains(t, got, "ABC")
			},
		},
		{
			name: "custom key list overrides defaults",
			uri:  "nats://srv?credsfile=/etc/secret&password=kept",
			keys: []string{"credsfile"},
			check: func(t *testing.T, got string) {
				assert.Contains(t, got, "credsfile=xxxxx")
				assert.Contains(t, got, "password=kept")
			},
		},
		{
			name: "no sensitive material unchanged",
			uri:  "kafka://broker:9092/mytopic?group=g1",
			check: func(t *testing.T, got string) {
				assert.Equal(t, "kafka://broker:9092/mytopic?group=g1", got)
			},
		},
		{
			name: "malformed uri returned unchanged",
			uri:  "://not a uri",
			check: func(t *testing.T, got string) {
				assert.Equal(t, "://not a uri", got)
			},
		},
		{
			name: "idempotent on already redacted uri",
			uri:  "smtp://h?password=xxxxx",
			check: func(t *testing.T, got string) {
				assert.Contains(t, got, "password=xxxxx")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RedactURI(tt.uri, tt.keys...)
			tt.check(t, got)
		})
	}
}
