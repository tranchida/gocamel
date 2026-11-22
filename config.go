package gocamel

import (
	"net/url"
	"os"
	"strings"
	"sync"
)

// GetConfigValue resolves a configuration value for a component endpoint.
//
// Precedence (highest to lowest):
//
//  1. Exact-name environment variable. The key is used verbatim.
//     Example: GetConfigValue(_, "OPENAI_API_KEY") reads os.Getenv("OPENAI_API_KEY").
//
//  2. Scheme-prefixed environment variable. Built by uppercasing
//     scheme + "_" + key with hyphens normalized to underscores (e.g.
//     sql-stored:// reads SQL_STORED_PROCEDURE; a legacy unnormalized name
//     such as SQL-STORED_PROCEDURE is checked as fallback if non-empty).
//     Example: for an ftp:// URI and key "password", reads os.Getenv("FTP_PASSWORD").
//
//  3. URI query parameter. Example: ftp://host?password=secret.
//     This is the LEAST secure source for credentials — the full URI
//     (including query) often ends up in connection-error logs, audit
//     trails, and crash reports. GetConfigValue emits a WARN log once
//     per (scheme,key) combination when a credential-like key is
//     resolved from the query string; redirect to env vars in
//     production.
//
//  4. URI userinfo, but only for the well-known credential keys
//     "username"/"user" and "password"/"pass". Example: ftp://user:pass@host.
//
// Returns the empty string when u is nil (after checking exact env var) or
// when none of the sources holds the key.
func GetConfigValue(u *url.URL, key string) string {
	// 1. Exact env var (e.g. "OPENAI_API_KEY")
	if val := os.Getenv(key); val != "" {
		return val
	}

	if u == nil {
		return ""
	}

	// 2. Scheme-prefixed env var (e.g. "FTP_PASSWORD", "SQL_STORED_PROCEDURE")
	if u.Scheme != "" {
		normalizedScheme := strings.ReplaceAll(u.Scheme, "-", "_")
		envKey := strings.ToUpper(normalizedScheme + "_" + key)
		if val := os.Getenv(envKey); val != "" {
			return val
		}
		if normalizedScheme != u.Scheme {
			legacyEnvKey := strings.ToUpper(u.Scheme + "_" + key)
			if val := os.Getenv(legacyEnvKey); val != "" {
				return val
			}
		}
	}

	// 3. URI query parameter (e.g. ?password=secret)
	if val := u.Query().Get(key); val != "" {
		if isCredentialKey(key) {
			warnCredentialInQuery(u.Scheme, key)
		}
		return val
	}

	// 4. URI userinfo (only for username/password)
	if u.User != nil {
		if strings.EqualFold(key, "username") || strings.EqualFold(key, "user") {
			return u.User.Username()
		}
		if strings.EqualFold(key, "password") || strings.EqualFold(key, "pass") {
			if pass, ok := u.User.Password(); ok {
				return pass
			}
		}
	}

	return ""
}

// isCredentialKey reports whether key is a known credential key, matched
// case-insensitively against the canonical sensitive key set in utils.go.
func isCredentialKey(key string) bool {
	return isSensitiveKey(key, nil)
}

// credentialWarningOnce dedupes the "credential in query param" warning so
// noisy components (a polling consumer reconnecting every second) do not
// flood the logs. The key is "scheme:key" — once per pairing for the
// lifetime of the process.
var credentialWarningOnce sync.Map

func warnCredentialInQuery(scheme, key string) {
	cacheKey := strings.ToLower(scheme) + ":" + strings.ToLower(key)
	if _, loaded := credentialWarningOnce.LoadOrStore(cacheKey, struct{}{}); loaded {
		return
	}
	LogWarn("config: credential resolved from URI query parameter; prefer environment variables "+
		"(URIs often leak into connection-error logs and audit trails)",
		"scheme", scheme,
		"key", key)
}
