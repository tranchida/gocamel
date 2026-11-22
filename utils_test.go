package gocamel

import (
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestRedactURI_MalformedQueryAndSpecialUserinfo(t *testing.T) {
	t.Run("malformed percent encoding in query", func(t *testing.T) {
		got := RedactURI("ftp://host/dir?password=my%pass&mode=x")
		assert.NotContains(t, got, "my%pass")
		assert.Contains(t, got, "password=xxxxx")
		assert.Contains(t, got, "mode=x")
	})

	t.Run("semicolon inside password value in query", func(t *testing.T) {
		got := RedactURI("smtp://host?password=sec;ret")
		assert.NotContains(t, got, "sec")
		assert.NotContains(t, got, "ret")
		assert.Contains(t, got, "password=xxxxx")
	})

	t.Run("semicolon separated query parameters", func(t *testing.T) {
		got := RedactURI("smtp://host?mode=x;password=s3cr3t;timeout=5")
		assert.NotContains(t, got, "s3cr3t")
		assert.Contains(t, got, "mode=x")
		assert.Contains(t, got, "password=xxxxx")
		assert.Contains(t, got, "timeout=5")
	})

	t.Run("userinfo password containing hash", func(t *testing.T) {
		got := RedactURI("ftp://user:p#ss@host/dir")
		assert.NotContains(t, got, "p#ss")
		assert.NotContains(t, got, "ss@")
		assert.Equal(t, "ftp://user:xxxxx@host/dir", got)
	})

	t.Run("userinfo password containing question mark", func(t *testing.T) {
		got := RedactURI("ftp://user:p?ss@host/dir")
		assert.NotContains(t, got, "p?ss")
		assert.NotContains(t, got, "ss@")
		assert.Equal(t, "ftp://user:xxxxx@host/dir", got)
	})

	t.Run("userinfo password containing slash", func(t *testing.T) {
		got := RedactURI("ftp://user:p/ss@host/dir")
		assert.NotContains(t, got, "p/ss")
		assert.NotContains(t, got, "ss@")
		assert.Equal(t, "ftp://user:xxxxx@host/dir", got)
	})

	t.Run("userinfo with special char plus query email and secret", func(t *testing.T) {
		for _, uri := range []string{
			"ftp://user:pa%ss@host/dir?email=a@b&password=s3cr3t",
			"ftp://user:p/ss@host/dir?email=a@b&password=s3cr3t",
			"ftp://user:p?ss@host/dir?email=a@b&password=s3cr3t",
		} {
			got := RedactURI(uri)
			assert.NotContains(t, got, "pa%ss")
			assert.NotContains(t, got, "p/ss")
			assert.NotContains(t, got, "p?ss")
			assert.NotContains(t, got, "s3cr3t")
			assert.Contains(t, got, "user:xxxxx@host")
			assert.Contains(t, got, "email=a@b")
			assert.Contains(t, got, "password=xxxxx")
		}

		gotPort := RedactURI("ftp://host%zz:8080/dir?email=a@b&password=s3cr3t")
		assert.NotContains(t, gotPort, "s3cr3t")
		assert.Contains(t, gotPort, "host%zz:8080")
		assert.Contains(t, gotPort, "password=xxxxx")
	})

	t.Run("unified credential query keys", func(t *testing.T) {
		got := RedactURI("?pass=s&api_key=k&accesskey=a&access_key=b&passcode=p&authorizationToken=t")
		for _, bad := range []string{"=s", "=k", "=a", "=b", "=p", "=t"} {
			assert.NotContains(t, got, bad)
		}
		q, err := url.ParseQuery(strings.TrimPrefix(got, "?"))
		require.NoError(t, err)
		for _, k := range []string{"pass", "api_key", "accesskey", "access_key", "passcode", "authorizationToken"} {
			assert.Equal(t, "xxxxx", q.Get(k), "expected %s to be redacted", k)
		}

		got2 := RedactURI("svc://host?passwd=sec1&apitoken=sec2&accesstoken=sec3&access_token=sec4&client_secret=sec5&private_key=sec6")
		for _, sec := range []string{"sec1", "sec2", "sec3", "sec4", "sec5", "sec6"} {
			assert.NotContains(t, got2, sec)
		}
	})
}
