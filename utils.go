package gocamel

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// MaxSimpleInputBytes caps the size of input strings passed to Interpolate
// and to the Simple Language parser. Inputs above this size are left
// unchanged by Interpolate (and rejected by Simple Language parsing) to
// bound the cost of regex evaluation and prevent ReDoS-style denial of
// service via crafted Exchange bodies or headers. The default of 1 MiB is
// far above any reasonable interpolation use case; raise it deliberately if
// your routes legitimately interpolate large templates.
var MaxSimpleInputBytes = 1 << 20

// ParseURI parses an endpoint URI into a *url.URL.
// Returns an error if the URI is malformed.
func ParseURI(uri string) (*url.URL, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid URI: %w", err)
	}
	return u, nil
}

// ParseInt64Option parses a URI option value as a non-negative int64, returning
// 0 (and no error) when the value is empty. It is used by components to parse
// byte-size options such as maxMessageSize.
func ParseInt64Option(value string) int64 {
	if value == "" {
		return 0
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

var interpolateRegex = regexp.MustCompile(`\${(header|property|body)(?:\.([^}]+))?}`)

// Interpolate replaces ${header.name}, ${property.name} and ${body} placeholders
// in a string with values from the Exchange. Used by ToD (To Dynamic) for
// runtime URI resolution.
//
// SECURITY: inputs larger than MaxSimpleInputBytes are returned unchanged.
// This caps the worst-case cost of the substitution regex and prevents an
// attacker-controlled Exchange body from triggering pathological backtracking.
func Interpolate(text string, exchange *Exchange) string {
	if len(text) > MaxSimpleInputBytes {
		LogError("Interpolate: input exceeds MaxSimpleInputBytes; returning unchanged",
			"size", len(text), "limit", MaxSimpleInputBytes)
		return text
	}
	return interpolateRegex.ReplaceAllStringFunc(text, func(match string) string {
		submatches := interpolateRegex.FindStringSubmatch(match)
		if len(submatches) < 2 {
			return match
		}

		kind := submatches[1]
		key := ""
		if len(submatches) > 2 {
			key = submatches[2]
		}

		switch kind {
		case "header":
			if val, ok := exchange.In.GetHeader(key); ok {
				return fmt.Sprintf("%v", val)
			}
		case "property":
			if val, ok := exchange.GetProperty(key); ok {
				return fmt.Sprintf("%v", val)
			}
		case "body":
			if body := exchange.In.GetBody(); body != nil {
				return fmt.Sprintf("%v", body)
			}
		}

		return "" // Return empty string when the placeholder is not found (Camel behaviour).
	})
}

// patternToRegex converts a pattern with '*' wildcards into a Regexp.
func patternToRegex(pattern string) *regexp.Regexp {
	// Escape special regex characters, then replace '*' with '.*'.
	quoted := regexp.QuoteMeta(pattern)
	regexStr := "^" + strings.ReplaceAll(quoted, "\\*", ".*") + "$"
	re, _ := regexp.Compile(regexStr)
	return re
}
