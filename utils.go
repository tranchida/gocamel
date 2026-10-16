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
// in a string with values from the Exchange. Values are inserted verbatim;
// use InterpolateURI when the result is an endpoint URI.
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
		return resolvePlaceholder(submatches[1], submatches[2], exchange)
	})
}

// resolvePlaceholder returns the string value of one ${kind.key} placeholder,
// or "" when it is not set (Camel behaviour).
func resolvePlaceholder(kind, key string, exchange *Exchange) string {
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
	return ""
}

// uriPathEscaper escapes the characters that would let a value placed in the
// path (or opaque) part of a URI start a query string or a fragment.
var uriPathEscaper = strings.NewReplacer("?", "%3F", "#", "%23")

// InterpolateURI is Interpolate for endpoint URIs (ToD). Exchange values are
// data, not URI syntax, so each substituted value is escaped according to
// where its placeholder sits in the template:
//
//   - in the query string (after a literal '?'): url.QueryEscape, so a value
//     cannot add endpoint options such as "&allowHeaderOverride=true";
//   - in the path or opaque part: '?' and '#' are escaped, so a value cannot
//     start a query string or a fragment.
//
// A placeholder that appears before the template's scheme separator (e.g. the
// whole-URI template "${header.target}") is inserted verbatim: the route author
// explicitly delegated the choice of endpoint to exchange data, which must
// then come from a trusted source.
func InterpolateURI(text string, exchange *Exchange) string {
	if len(text) > MaxSimpleInputBytes {
		LogError("InterpolateURI: input exceeds MaxSimpleInputBytes; returning unchanged",
			"size", len(text), "limit", MaxSimpleInputBytes)
		return text
	}
	matches := interpolateRegex.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text
	}

	var sb strings.Builder
	sawScheme, inQuery := false, false
	last := 0
	scanLiteral := func(literal string) {
		if !sawScheme && strings.Contains(literal, ":") {
			sawScheme = true
		}
		if strings.ContainsAny(literal, "?#") {
			inQuery = true
		}
	}
	for _, m := range matches {
		literal := text[last:m[0]]
		sb.WriteString(literal)
		scanLiteral(literal)

		key := ""
		if m[4] >= 0 {
			key = text[m[4]:m[5]]
		}
		value := resolvePlaceholder(text[m[2]:m[3]], key, exchange)
		switch {
		case !sawScheme:
			// Whole-URI or scheme placeholder: trusted by construction.
		case inQuery:
			value = url.QueryEscape(value)
		default:
			value = uriPathEscaper.Replace(value)
		}
		sb.WriteString(value)
		last = m[1]
	}
	sb.WriteString(text[last:])
	return sb.String()
}

// patternToRegex converts a pattern with '*' wildcards into a Regexp.
func patternToRegex(pattern string) *regexp.Regexp {
	// Escape special regex characters, then replace '*' with '.*'.
	quoted := regexp.QuoteMeta(pattern)
	regexStr := "^" + strings.ReplaceAll(quoted, "\\*", ".*") + "$"
	return regexp.MustCompile(regexStr)
}

// defaultSensitiveURIKeys are query parameter names whose values are masked
// by RedactURI when no explicit list is provided. The match is
// case-insensitive because components read options with mixed cases
// (apiKey vs apikey, authorizationToken vs AuthorizationToken).
var defaultSensitiveURIKeys = []string{
	"password", "passwd", "secret", "token", "passcode",
	"apikey", "apitoken", "authorizationtoken", "accesstoken",
	"privatekey", "clientsecret",
}

// RedactURI masks sensitive credentials in an endpoint URI so that it can be
// safely logged, included in error messages or returned by Endpoint.URI()
// without leaking secrets.
//
// Both the userinfo password (scheme://user:PASSWORD@host) and the values of
// sensitive query parameters are replaced with "xxxxx". When keys is nil,
// defaultSensitiveURIKeys is used; pass an explicit (case-insensitive) list
// to override it. Non-parseable URIs are returned unchanged — an URI that
// cannot be parsed was never accepted by a component in the first place.
func RedactURI(uri string, keys ...string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return uri
	}

	changed := false

	if u.User != nil {
		if _, hasPassword := u.User.Password(); hasPassword {
			u.User = url.UserPassword(u.User.Username(), "xxxxx")
			changed = true
		}
	}

	sensitive := defaultSensitiveURIKeys
	if len(keys) > 0 {
		sensitive = keys
	}
	q := u.Query()
	for name, values := range q {
		for _, s := range sensitive {
			if strings.EqualFold(name, s) {
				for i, v := range values {
					if v != "" {
						values[i] = "xxxxx"
						changed = true
					}
				}
			}
		}
	}
	if changed {
		u.RawQuery = q.Encode()
		return u.String()
	}
	return uri
}
