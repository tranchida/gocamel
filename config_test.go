package gocamel

import (
	"bytes"
	"log/slog"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mustParseURL parses a URI for test setup and fails the test on error.
func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u
}

// withCapturedLogger swaps in a slog handler whose buffer the caller can
// inspect, then restores the original logger after the test. Returns the
// buffer.
func withCapturedLogger(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	captured := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	original := GetLogger()
	SetLogger(captured)
	t.Cleanup(func() { SetLogger(original) })
	return buf
}

// TestGetConfigValue_PrecedenceEnvBeatsAll verifies that an exact-name env
// var overrides everything else.
func TestGetConfigValue_PrecedenceEnvBeatsAll(t *testing.T) {
	t.Setenv("MY_TEST_TOKEN", "from-env-exact")
	u := mustParseURL(t, "scheme-precedence-1://user:from-userinfo@host?MY_TEST_TOKEN=from-query")

	assert.Equal(t, "from-env-exact", GetConfigValue(u, "MY_TEST_TOKEN"))
}

// TestGetConfigValue_PrecedenceSchemeEnvBeatsQuery verifies that the
// scheme-prefixed env var beats query and userinfo.
func TestGetConfigValue_PrecedenceSchemeEnvBeatsQuery(t *testing.T) {
	t.Setenv("SCHEMEPREC2_PASSWORD", "from-scheme-env")
	u := mustParseURL(t, "schemeprec2://user:from-userinfo@host?password=from-query")

	assert.Equal(t, "from-scheme-env", GetConfigValue(u, "password"))
}

// TestGetConfigValue_PrecedenceQueryBeatsUserinfo verifies that a query
// parameter is consulted before the URI userinfo.
func TestGetConfigValue_PrecedenceQueryBeatsUserinfo(t *testing.T) {
	u := mustParseURL(t, "schemeprec3://user:from-userinfo@host?password=from-query")

	assert.Equal(t, "from-query", GetConfigValue(u, "password"))
}

// TestGetConfigValue_UserinfoFallback verifies that userinfo is used when no
// env/query value is present.
func TestGetConfigValue_UserinfoFallback(t *testing.T) {
	u := mustParseURL(t, "schemeprec4://alice:secretpass@host")

	assert.Equal(t, "alice", GetConfigValue(u, "username"))
	assert.Equal(t, "secretpass", GetConfigValue(u, "password"))
}

// TestGetConfigValue_NotFound verifies the empty-string fallback.
func TestGetConfigValue_NotFound(t *testing.T) {
	u := mustParseURL(t, "schemeprec5://host")
	assert.Equal(t, "", GetConfigValue(u, "missing"))
}

// TestGetConfigValue_CredentialInQueryEmitsWarn verifies that resolving a
// credential-like key from the URI query string emits a WARN log.
func TestGetConfigValue_CredentialInQueryEmitsWarn(t *testing.T) {
	buf := withCapturedLogger(t)
	u := mustParseURL(t, "warn-test-a://host?password=should-warn")

	_ = GetConfigValue(u, "password")

	out := buf.String()
	assert.Contains(t, out, "level=WARN")
	assert.Contains(t, out, "credential resolved from URI query parameter")
	assert.Contains(t, out, "scheme=warn-test-a")
	assert.Contains(t, out, "key=password")
	// Critically: the value itself MUST NOT appear in the log.
	assert.NotContains(t, out, "should-warn")
}

// TestGetConfigValue_NonCredentialNoWarn verifies that an ordinary query
// parameter (e.g. a non-secret option) does NOT emit the warning.
func TestGetConfigValue_NonCredentialNoWarn(t *testing.T) {
	buf := withCapturedLogger(t)
	u := mustParseURL(t, "warn-test-b://host?folderName=INBOX")

	_ = GetConfigValue(u, "folderName")

	assert.Empty(t, buf.String(), "non-credential keys must not trigger a warning")
}

// TestGetConfigValue_CredentialWarnDedupes verifies that the credential
// warning fires at most once per (scheme,key) pairing within the process.
func TestGetConfigValue_CredentialWarnDedupes(t *testing.T) {
	buf := withCapturedLogger(t)
	u := mustParseURL(t, "warn-test-c://host?token=abc")

	_ = GetConfigValue(u, "token")
	_ = GetConfigValue(u, "token")
	_ = GetConfigValue(u, "token")

	count := strings.Count(buf.String(), "credential resolved from URI query parameter")
	assert.Equal(t, 1, count, "dedup should leave a single warning regardless of the call count")
}

// TestGetConfigValue_CredentialFromEnvNoWarn verifies that pulling a
// credential from an env var (the recommended source) does NOT trigger the
// query-string warning even though the key happens to be in credentialKeys.
func TestGetConfigValue_CredentialFromEnvNoWarn(t *testing.T) {
	buf := withCapturedLogger(t)
	t.Setenv("WARNTESTD_PASSWORD", "from-env")
	u := mustParseURL(t, "warntestd://host")

	assert.Equal(t, "from-env", GetConfigValue(u, "password"))
	assert.Empty(t, buf.String())
}

// TestGetConfigValue_CredentialFromUserinfoNoWarn verifies that the
// conventional userinfo form is not flagged (only query params are).
func TestGetConfigValue_CredentialFromUserinfoNoWarn(t *testing.T) {
	buf := withCapturedLogger(t)
	u := mustParseURL(t, "warn-test-e://alice:secret@host")

	assert.Equal(t, "secret", GetConfigValue(u, "password"))
	assert.Empty(t, buf.String())
}
