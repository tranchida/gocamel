package gocamel

import (
	"errors"
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
		// url.Parse reports the raw URI, and often the offending fragment, in
		// its error: both may hold a credential (a password with a stray '%').
		// Report the redacted URI and the kind of failure only.
		return nil, fmt.Errorf("invalid URI %q: %s", RedactURI(uri), uriParseFailure(err))
	}
	return u, nil
}

// uriParseFailure describes why url.Parse failed without quoting any part of
// the URI.
func uriParseFailure(err error) string {
	var escapeErr url.EscapeError
	var hostErr url.InvalidHostError
	switch {
	case errors.As(err, &escapeErr):
		return "invalid percent-encoding"
	case errors.As(err, &hostErr):
		return "invalid character in host"
	default:
		return "malformed URI"
	}
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
	var escaper uriValueEscaper
	last := 0
	for _, m := range matches {
		literal := text[last:m[0]]
		sb.WriteString(literal)
		escaper.observeLiteral(literal)

		key := ""
		if m[4] >= 0 {
			key = text[m[4]:m[5]]
		}
		value := resolvePlaceholder(text[m[2]:m[3]], key, exchange)
		sb.WriteString(escaper.escape(value))
		last = m[1]
	}
	sb.WriteString(text[last:])
	return sb.String()
}

// uriValueEscaper escapes exchange values substituted into an endpoint URI
// template. Its position is derived from the literal parts of the template,
// never from the values themselves, and is updated by observeLiteral as the
// template is walked left to right:
//
//   - before the scheme ':' has been seen, values are inserted verbatim (a
//     whole-URI or scheme placeholder is a deliberate delegation of the
//     endpoint choice to trusted data);
//   - once a literal '?' or '#' has been seen (query part), values are
//     url.QueryEscape'd;
//   - otherwise (path or opaque part), only '?' and '#' are escaped.
type uriValueEscaper struct {
	sawScheme bool
	inQuery   bool
}

// observeLiteral records the scheme and query delimiters found in a literal
// template part. Call it for every literal, in template order, before escaping
// the values that follow it.
func (e *uriValueEscaper) observeLiteral(literal string) {
	if !e.sawScheme && strings.Contains(literal, ":") {
		e.sawScheme = true
	}
	if strings.ContainsAny(literal, "?#") {
		e.inQuery = true
	}
}

// escape returns value escaped for the position reached so far.
func (e *uriValueEscaper) escape(value string) string {
	switch {
	case !e.sawScheme:
		return value
	case e.inQuery:
		return url.QueryEscape(value)
	default:
		return uriPathEscaper.Replace(value)
	}
}

// patternToRegex converts a pattern with '*' wildcards into a Regexp.
func patternToRegex(pattern string) *regexp.Regexp {
	// Escape special regex characters, then replace '*' with '.*'.
	quoted := regexp.QuoteMeta(pattern)
	regexStr := "^" + strings.ReplaceAll(quoted, "\\*", ".*") + "$"
	return regexp.MustCompile(regexStr)
}

// defaultSensitiveURIKeys are query parameter names whose values are masked
// by RedactURI when no explicit list is provided, and treated as credential
// keys by GetConfigValue when resolved from a URI query string. The match is
// case-insensitive because components read options with mixed cases
// (apiKey vs apikey, authorizationToken vs AuthorizationToken).
var defaultSensitiveURIKeys = []string{
	"password",
	"pass",
	"passwd",
	"secret",
	"token",
	"passcode",
	"apikey",
	"api_key",
	"apitoken",
	"authorizationtoken",
	"accesstoken",
	"access_token",
	"accesskey",
	"access_key",
	"clientsecret",
	"client_secret",
	"privatekey",
	"private_key",
}

var defaultSensitiveURIKeySet = func() map[string]struct{} {
	set := make(map[string]struct{}, len(defaultSensitiveURIKeys))
	for _, key := range defaultSensitiveURIKeys {
		set[key] = struct{}{}
	}
	return set
}()

// isSensitiveKey reports whether name matches keys (case-insensitively), or
// defaultSensitiveURIKeySet when keys is empty.
func isSensitiveKey(name string, keys []string) bool {
	if len(keys) == 0 {
		_, ok := defaultSensitiveURIKeySet[strings.ToLower(name)]
		return ok
	}
	for _, k := range keys {
		if strings.EqualFold(name, k) {
			return true
		}
	}
	return false
}

// RedactURI masks sensitive credentials in an endpoint URI so that it can be
// safely logged, included in error messages or returned by Endpoint.URI()
// without leaking secrets.
//
// Both the userinfo password (scheme://user:PASSWORD@host) and the values of
// sensitive query parameters are replaced with "xxxxx". When keys is nil,
// defaultSensitiveURIKeys is used; pass an explicit (case-insensitive) list
// to override it. A URI that cannot be parsed is masked textually on a best
// effort basis (see redactUnparsedURI): it is exactly the one reported in a
// "invalid URI" error, so it must not carry its credentials there.
func RedactURI(uri string, keys ...string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return redactUnparsedURI(uri, keys)
	}

	changed := false

	if u.User != nil {
		if _, hasPassword := u.User.Password(); hasPassword {
			u.User = url.UserPassword(u.User.Username(), "xxxxx")
			changed = true
		}
	}

	q, qErr := url.ParseQuery(u.RawQuery)
	if qErr != nil {
		if redactedQuery, queryChanged := redactRawQuery(u.RawQuery, keys); queryChanged {
			u.RawQuery = redactedQuery
			changed = true
		}
		if changed {
			return u.String()
		}
		return uri
	}

	for name, values := range q {
		if isSensitiveKey(name, keys) {
			for i, v := range values {
				if v != "" {
					values[i] = "xxxxx"
					changed = true
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

// redactURIs returns a copy of uris with each element masked via RedactURI.
func redactURIs(uris []string) []string {
	if uris == nil {
		return nil
	}
	redacted := make([]string, len(uris))
	for i, uri := range uris {
		redacted[i] = RedactURI(uri)
	}
	return redacted
}

// redactUnparsedURI masks the userinfo password and the sensitive query
// parameters of a URI that url.Parse rejects, working on the raw text.
func redactUnparsedURI(uri string, keys []string) string {
	masked := redactUnparsedUserinfo(uri)

	rest, fragment, hasFragment := strings.Cut(masked, "#")
	base, query, hasQuery := strings.Cut(rest, "?")

	if hasQuery {
		if redactedQuery, changed := redactRawQuery(query, keys); changed {
			query = redactedQuery
		}
		base += "?" + query
	}
	if hasFragment {
		base += "#" + fragment
	}
	return base
}

// redactUnparsedUserinfo masks the password in scheme://user:PASSWORD@host even
// when unescaped '/', '?', or '#' in PASSWORD shift '@' past them.
func redactUnparsedUserinfo(uri string) string {
	i := strings.Index(uri, "://")
	if i < 0 {
		return uri
	}
	afterScheme := uri[i+3:]
	at := findUnparsedUserinfoEnd(afterScheme)
	if at < 0 {
		return uri
	}
	delim := strings.IndexAny(afterScheme, "/?#")
	colon := strings.IndexByte(afterScheme[:at], ':')
	if colon < 0 || (delim >= 0 && colon > delim) {
		return uri
	}
	return uri[:i+3] + afterScheme[:colon+1] + "xxxxx" + afterScheme[at:]
}

func findUnparsedUserinfoEnd(afterScheme string) int {
	delim := strings.IndexAny(afterScheme, "/?#")
	if delim < 0 {
		return strings.LastIndexByte(afterScheme, '@')
	}
	// If '@' already appears before the first '/', '?', or '#', the authority
	// boundary is unambiguous and later '@' signs belong to path/query/fragment.
	if prefixAt := strings.LastIndexByte(afterScheme[:delim], '@'); prefixAt >= 0 {
		return prefixAt
	}
	// When '@' appears only after '/', '?', or '#', a password may contain those
	// delimiters (e.g. user:p#ss@host/dir). Avoid crossing into a trailing
	// '?key=val@...' query parameter or a '?query'/'#fragment' after '@host'.
	if q := strings.IndexAny(afterScheme, "?#"); q >= 0 {
		if strings.IndexByte(afterScheme[:q], '@') >= 0 {
			return strings.LastIndexByte(afterScheme[:q], '@')
		}
		firstAt := strings.IndexByte(afterScheme[q:], '@')
		if firstAt < 0 || strings.Contains(afterScheme[q:q+firstAt], "=") {
			return -1
		}
	}
	searchEnd := len(afterScheme)
	if firstAt := strings.IndexByte(afterScheme, '@'); firstAt >= 0 {
		if q := strings.IndexAny(afterScheme[firstAt+1:], "?#"); q >= 0 {
			searchEnd = firstAt + 1 + q
		}
	}
	return strings.LastIndexByte(afterScheme[:searchEnd], '@')
}

// redactRawQuery masks sensitive values in a raw query string split on '&' and ';'.
func redactRawQuery(query string, keys []string) (string, bool) {
	if query == "" {
		return query, false
	}
	changed := false
	ampParts := strings.Split(query, "&")
	for i, ampPart := range ampParts {
		if !strings.ContainsRune(ampPart, ';') {
			if redacted, partChanged := redactQueryPair(ampPart, keys); partChanged {
				ampParts[i] = redacted
				changed = true
			}
			continue
		}
		if redacted, partChanged := redactSemicolonQueryPart(ampPart, keys); partChanged {
			ampParts[i] = redacted
			changed = true
		}
	}
	if !changed {
		return query, false
	}
	return strings.Join(ampParts, "&"), true
}

func redactSemicolonQueryPart(ampPart string, keys []string) (string, bool) {
	subParts := strings.Split(ampPart, ";")
	kept := make([]string, 0, len(subParts))
	changed := false
	inSensitive := false
	pendingEmptyName := ""
	pendingEmptyIdx := -1

	for _, sub := range subParts {
		name, value, hasEq, sensitive := parseSensitiveQueryPair(sub, keys)
		if sensitive && value != "" {
			kept = append(kept, name+"=xxxxx")
			changed = true
			inSensitive = true
			pendingEmptyName = ""
			continue
		}
		if !hasEq {
			if pendingEmptyName != "" {
				// A valueless ';token' immediately following a sensitive 'name=' is
				// the remainder of a value starting with ';' (e.g. password=;secret).
				kept[pendingEmptyIdx] = pendingEmptyName + "=xxxxx"
				changed = true
				pendingEmptyName = ""
				inSensitive = true
				continue
			}
			if inSensitive {
				// Trailing ';token' without '=' after a redacted parameter is the
				// remainder of that parameter's unescaped value (e.g. password=sec;ret).
				changed = true
				continue
			}
		}
		kept = append(kept, sub)
		inSensitive = false
		if sensitive && value == "" {
			pendingEmptyName = name
			pendingEmptyIdx = len(kept) - 1
		} else {
			pendingEmptyName = ""
		}
	}
	if !changed {
		return ampPart, false
	}
	return strings.Join(kept, ";"), true
}

func redactQueryPair(pair string, keys []string) (string, bool) {
	name, value, _, sensitive := parseSensitiveQueryPair(pair, keys)
	if !sensitive || value == "" {
		return pair, false
	}
	return name + "=xxxxx", true
}

func parseSensitiveQueryPair(pair string, keys []string) (name, value string, hasEq, sensitive bool) {
	name, value, hasEq = strings.Cut(pair, "=")
	if !hasEq {
		return "", "", false, false
	}
	lookupName := name
	if decoded, err := url.QueryUnescape(name); err == nil {
		lookupName = decoded
	}
	return name, value, true, isSensitiveKey(lookupName, keys)
}
