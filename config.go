package gocamel

import (
	"net/url"
	"os"
	"strings"
)

// GetConfigValue tries to read a configuration value.
// It checks environment variables first, then URI query parameters, and finally URI user info.
// The environment variable checked is usually upper-case component name + "_" + key.
// e.g. For "ftp" component and "password", it might check "FTP_PASSWORD".
func GetConfigValue(u *url.URL, key string) string {
	// 1. Check environment variable exactly as the key (e.g. "OPENAI_API_KEY")
	if val := os.Getenv(key); val != "" {
		return val
	}

	// 2. Check environment variable based on scheme and key (e.g. "FTP_PASSWORD")
	envKey := strings.ToUpper(u.Scheme + "_" + key)
	if val := os.Getenv(envKey); val != "" {
		return val
	}

	// 3. Check query parameters (e.g. ?password=secret)
	if val := u.Query().Get(key); val != "" {
		return val
	}

	// 4. Check URL user info (for username and password)
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
