package webex

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	gocamel "github.com/tranchida/gocamel"
)

const (
	// DefaultBaseURL is the default Webex REST API v1 root URL.
	DefaultBaseURL = "https://webexapis.com/v1"

	defaultHTTPTimeout           = 15 * time.Second
	defaultConsumerHeaderTimeout = 30 * time.Second
	maxResponseBodyBytes         = gocamel.DefaultMaxBodySize
	maxErrorBytes                = 512
	maxRoomCacheEntries          = 10_000
	maxRoomListPages             = 100
	maxConsumerRedirects         = 10
)

type outboundMessage struct {
	RoomID        string `json:"roomId,omitempty"`
	ToPersonEmail string `json:"toPersonEmail,omitempty"`
	ToPersonID    string `json:"toPersonId,omitempty"`
	ParentID      string `json:"parentId,omitempty"`
	Text          string `json:"text,omitempty"`
	Markdown      string `json:"markdown,omitempty"`
}

type restRoom struct {
	ID string `json:"id"`
}

type restIDParts struct {
	cluster      string
	resourceType string
	uuid         string
}

// webexClient handles outbound Webex REST API requests and regional room ID
// resolution with a bounded in-memory cache.
type webexClient struct {
	token      string
	baseURL    string
	baseParsed *url.URL
	httpClient *http.Client

	cacheMu         sync.RWMutex // protects roomIDs and roomOrder
	roomIDs         map[string]string
	roomOrder       []string
	maxCacheEntries int
	maxPages        int

	// resolveSem serializes cache-miss GET /rooms pagination without holding
	// cacheMu across network I/O, allowing concurrent cache hits and context
	// cancellation while waiting.
	resolveSem chan struct{}
}

func newWebexClient(token, baseURL string, baseParsed *url.URL, httpClient *http.Client) *webexClient {
	return &webexClient{
		token:           token,
		baseURL:         baseURL,
		baseParsed:      baseParsed,
		httpClient:      httpClient,
		roomIDs:         make(map[string]string),
		maxCacheEntries: maxRoomCacheEntries,
		maxPages:        maxRoomListPages,
		resolveSem:      make(chan struct{}, 1),
	}
}

// sendMessage posts a message to POST <baseURL>/messages and returns the
// created Webex message ID if present in the JSON response.
func (c *webexClient) sendMessage(ctx context.Context, msg outboundMessage) (string, error) {
	payload, err := json.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("webex: failed to encode message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/messages", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("webex: failed to build message request: %w", redactWebexError(err, c.token))
	}
	c.authorize(req)
	req.Header.Set("Content-Type", "application/json")

	// #nosec G704 -- baseURL is validated as absolute http(s) without userinfo, query, or fragment at endpoint creation; redirects are disabled.
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("webex: failed to send message: %w", redactWebexError(err, c.token))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
		return "", fmt.Errorf("webex: API error (HTTP %d): %s", resp.StatusCode, redactWebexToken(strings.TrimSpace(string(snippet)), c.token))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("webex: failed to read message response: %w", redactWebexError(err, c.token))
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return "", nil
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		// Webex already accepted the message (HTTP 2xx); log instead of returning
		// an error so route redelivery does not post duplicate messages.
		gocamel.LogWarn("webex: message sent (HTTP 2xx) but response body was not valid JSON",
			"status", resp.StatusCode, "error", redactWebexError(err, c.token))
		return "", nil
	}

	return created.ID, nil
}

// resolveRoomID maps a raw Mercury room UUID to its regional Webex REST room
// ID by paginating GET /rooms. Results are cached in memory up to
// maxCacheEntries. Never fabricates a hardcoded "us" cluster ID.
func (c *webexClient) resolveRoomID(ctx context.Context, rawRoomID string) (string, error) {
	key := strings.ToLower(strings.TrimSpace(rawRoomID))
	if key == "" {
		return "", fmt.Errorf("webex: empty Mercury room ID")
	}

	if restID, ok := c.lookupCachedRoom(key); ok {
		return restID, nil
	}

	select {
	case c.resolveSem <- struct{}{}:
		defer func() { <-c.resolveSem }()
	case <-ctx.Done():
		return "", ctx.Err()
	}

	// Re-check after acquiring the pagination semaphore in case an earlier
	// in-flight scan populated the cache.
	if restID, ok := c.lookupCachedRoom(key); ok {
		return restID, nil
	}

	nextURL := c.baseURL + "/rooms?max=1000"
	visited := make(map[string]struct{})
	maxPages := c.maxPages
	if maxPages <= 0 {
		maxPages = maxRoomListPages
	}

	for page := 0; nextURL != "" && page < maxPages; page++ {
		if _, seen := visited[nextURL]; seen {
			return "", fmt.Errorf("webex: pagination cycle detected in rooms Link header")
		}
		visited[nextURL] = struct{}{}

		rooms, next, err := c.listRooms(ctx, nextURL)
		if err != nil {
			return "", err
		}

		var found string
		var discovered []struct {
			uuid   string
			restID string
		}
		for _, r := range rooms {
			parts, err := decodeRestID(r.ID)
			if err != nil {
				continue
			}
			discovered = append(discovered, struct {
				uuid   string
				restID string
			}{uuid: parts.uuid, restID: r.ID})
			if parts.uuid == key {
				found = r.ID
			}
		}

		if len(discovered) > 0 || found != "" {
			c.cacheRooms(discovered, key, found)
		}
		if found != "" {
			return found, nil
		}

		if next != "" {
			if err := c.validateNextLink(next); err != nil {
				return "", err
			}
		}
		nextURL = next
	}

	if nextURL != "" {
		return "", fmt.Errorf("webex: exceeded maximum room pagination pages (%d) while resolving Mercury UUID %q", maxPages, rawRoomID)
	}
	return "", fmt.Errorf("webex: room with Mercury UUID %q not found in Webex room list", rawRoomID)
}

func (c *webexClient) lookupCachedRoom(key string) (string, bool) {
	c.cacheMu.RLock()
	restID, ok := c.roomIDs[key]
	c.cacheMu.RUnlock()
	return restID, ok
}

func (c *webexClient) cacheRooms(discovered []struct {
	uuid   string
	restID string
}, targetKey, targetRestID string) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()

	limit := c.maxCacheEntries
	if limit <= 0 {
		limit = maxRoomCacheEntries
	}

	for _, item := range discovered {
		if item.uuid == targetKey {
			continue // inserted last so targetKey is never evicted by this batch
		}
		c.insertCacheLocked(item.uuid, item.restID, limit)
	}
	if targetKey != "" && targetRestID != "" {
		c.insertCacheLocked(targetKey, targetRestID, limit)
	}
}

func (c *webexClient) insertCacheLocked(key, value string, limit int) {
	if _, exists := c.roomIDs[key]; exists {
		c.roomIDs[key] = value
		return
	}
	for len(c.roomIDs) >= limit && len(c.roomOrder) > 0 {
		oldest := c.roomOrder[0]
		c.roomOrder[0] = ""
		c.roomOrder = c.roomOrder[1:]
		delete(c.roomIDs, oldest)
	}
	// Compact backing array periodically so evicted prefix does not pin memory.
	if cap(c.roomOrder) > limit*2 && len(c.roomOrder) <= limit {
		compacted := make([]string, len(c.roomOrder), limit)
		copy(compacted, c.roomOrder)
		c.roomOrder = compacted
	}
	c.roomIDs[key] = value
	c.roomOrder = append(c.roomOrder, key)
}

func (c *webexClient) listRooms(ctx context.Context, requestURL string) ([]restRoom, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("webex: failed to build room list request: %w", redactWebexError(err, c.token))
	}
	c.authorize(req)

	// #nosec G704 -- requestURL is either validated baseURL+"/rooms?max=1000" or a Link next URL verified to match baseURL's exact scheme, host, and port.
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("webex: failed to list rooms: %w", redactWebexError(err, c.token))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
		return nil, "", fmt.Errorf("webex: room list API error (HTTP %d): %s", resp.StatusCode, redactWebexToken(strings.TrimSpace(string(snippet)), c.token))
	}

	var result struct {
		Items []restRoom `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, "", fmt.Errorf("webex: failed to decode rooms response: %w", redactWebexError(err, c.token))
	}

	return result.Items, parseNextLink(resp.Header.Values("Link")), nil
}

func (c *webexClient) authorize(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
}

func (c *webexClient) validateNextLink(rawNext string) error {
	u, err := url.Parse(rawNext)
	if err != nil {
		return fmt.Errorf("webex: invalid rooms Link next URL: %w", redactWebexError(err, c.token))
	}
	if !u.IsAbs() || u.Opaque != "" || u.Hostname() == "" {
		return fmt.Errorf("webex: rooms Link next URL must be absolute")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("webex: rooms Link next URL has unsupported scheme %q", u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("webex: rooms Link next URL must not contain credentials")
	}
	if !sameOrigin(c.baseParsed, u) {
		return fmt.Errorf("webex: refusing to follow cross-origin rooms Link next URL")
	}
	return nil
}

// parseNextLink extracts the target URL for rel="next" from RFC 5988 Link
// response headers, inspecting all Link header lines and all semicolon-separated
// parameters case-insensitively.
func parseNextLink(linkValues []string) string {
	for _, header := range linkValues {
		for _, entry := range splitLinkEntries(header) {
			entry = strings.TrimSpace(entry)
			if !strings.HasPrefix(entry, "<") {
				continue
			}
			end := strings.IndexByte(entry, '>')
			if end <= 1 {
				continue
			}
			target := strings.TrimSpace(entry[1:end])
			params := entry[end+1:]
			if hasNextRel(params) {
				return target
			}
		}
	}
	return ""
}

// splitLinkEntries splits a Link header on top-level commas outside <...> and "...".
func splitLinkEntries(header string) []string {
	var entries []string
	inAngles := false
	inQuotes := false
	start := 0
	for i := 0; i < len(header); i++ {
		switch header[i] {
		case '<':
			if !inQuotes {
				inAngles = true
			}
		case '>':
			if !inQuotes {
				inAngles = false
			}
		case '"':
			if !inAngles {
				inQuotes = !inQuotes
			}
		case ',':
			if !inAngles && !inQuotes {
				entries = append(entries, header[start:i])
				start = i + 1
			}
		}
	}
	entries = append(entries, header[start:])
	return entries
}

func hasNextRel(params string) bool {
	for _, param := range strings.Split(params, ";") {
		param = strings.TrimSpace(param)
		if param == "" {
			continue
		}
		eq := strings.IndexByte(param, '=')
		if eq <= 0 {
			continue
		}
		name := strings.TrimSpace(param[:eq])
		if !strings.EqualFold(name, "rel") {
			continue
		}
		val := strings.TrimSpace(param[eq+1:])
		if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
			val = val[1 : len(val)-1]
		}
		for _, token := range strings.Fields(val) {
			if strings.EqualFold(token, "next") {
				return true
			}
		}
	}
	return false
}

// decodeRestID decodes a Webex REST API base64 ID (padded, unpadded, standard,
// or URL-safe) of the form "ciscospark://<cluster>/<resourceType>/<uuid>".
func decodeRestID(restID string) (restIDParts, error) {
	if restID == "" {
		return restIDParts{}, fmt.Errorf("empty REST ID")
	}
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, enc := range encodings {
		decoded, err := enc.DecodeString(restID)
		if err != nil {
			continue
		}
		s := string(decoded)
		if !strings.HasPrefix(s, "ciscospark://") {
			continue
		}
		rest := strings.TrimPrefix(s, "ciscospark://")
		parts := strings.Split(rest, "/")
		if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
			return restIDParts{}, fmt.Errorf("invalid ciscospark URI format in REST ID")
		}
		return restIDParts{
			cluster:      parts[0],
			resourceType: parts[1],
			uuid:         strings.ToLower(parts[2]),
		}, nil
	}
	return restIDParts{}, fmt.Errorf("invalid Webex REST ID")
}

// encodeRestID encodes a Webex regional REST API ID preserving the given
// cluster prefix (e.g. "eu", "us", "urn:TEAM:eu-central-1_k").
func encodeRestID(cluster, resourceType, uuid string) string {
	uri := fmt.Sprintf("ciscospark://%s/%s/%s", cluster, resourceType, uuid)
	return base64.StdEncoding.EncodeToString([]byte(uri))
}

// isUUID reports whether s is a standard 36-character RFC 4122 UUID
// (8-4-4-4-12 hexadecimal digits).
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch i {
		case 8, 13, 18, 23:
			if s[i] != '-' {
				return false
			}
		default:
			if !isHexDigit(s[i]) {
				return false
			}
		}
	}
	return true
}

func isHexDigit(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

// validateBaseURL validates an absolute http(s) Webex API base URL without
// userinfo, query string, or fragment, and returns the parsed URL plus its
// slash-trimmed canonical form.
func validateBaseURL(raw string) (*url.URL, string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(trimmed)
	if err != nil {
		return nil, "", fmt.Errorf("parse: %w", err)
	}
	if !u.IsAbs() {
		return nil, "", fmt.Errorf("baseURL must be absolute: %s", raw)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, "", fmt.Errorf("baseURL scheme must be http or https, got %q", u.Scheme)
	}
	if u.Hostname() == "" || u.Opaque != "" {
		return nil, "", fmt.Errorf("baseURL must include a host: %s", raw)
	}
	if u.User != nil {
		return nil, "", fmt.Errorf("baseURL must not contain credentials")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, "", fmt.Errorf("baseURL must not contain query parameters or fragments")
	}
	return u, trimmed, nil
}

func sameOrigin(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) &&
		effectivePort(a) == effectivePort(b)
}

func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	default:
		return ""
	}
}

func validateAccessToken(token string) error {
	if token == "" {
		return fmt.Errorf("webex: access token missing (set WEBEX_ACCESS_TOKEN, WEBEX_ACCESSTOKEN, WEBEX_AUTHORIZATIONTOKEN, or accessToken URI parameter)")
	}
	for i := 0; i < len(token); i++ {
		if token[i] <= 0x20 || token[i] >= 0x7f {
			return fmt.Errorf("webex: invalid access token: whitespace, control, and non-ASCII characters are not allowed")
		}
	}
	return nil
}

type redactedWebexError struct {
	msg string
	err error
}

func (e *redactedWebexError) Error() string { return e.msg }
func (e *redactedWebexError) Unwrap() error { return e.err }

func redactWebexToken(s, token string) string {
	if token == "" {
		return s
	}
	s = strings.ReplaceAll(s, token, "xxxxx")
	if escaped := url.PathEscape(token); escaped != "" && escaped != token {
		s = strings.ReplaceAll(s, escaped, "xxxxx")
	}
	if queryEscaped := url.QueryEscape(token); queryEscaped != "" && queryEscaped != token {
		s = strings.ReplaceAll(s, queryEscaped, "xxxxx")
	}
	return s
}

func redactWebexError(err error, token string) error {
	if err == nil || token == "" {
		return err
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		cp := *urlErr
		cp.URL = redactWebexToken(cp.URL, token)
		if cp.Err != nil {
			cp.Err = redactWebexError(cp.Err, token)
		}
		return &cp
	}
	msg := redactWebexToken(err.Error(), token)
	if msg == err.Error() {
		return err
	}
	var cause error
	switch {
	case errors.Is(err, context.Canceled):
		cause = context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		cause = context.DeadlineExceeded
	}
	return &redactedWebexError{msg: msg, err: cause}
}

func cloneDefaultTransport() http.RoundTripper {
	if dt, ok := http.DefaultTransport.(*http.Transport); ok {
		return dt.Clone()
	}
	if http.DefaultTransport != nil {
		return http.DefaultTransport
	}
	return &http.Transport{}
}

func newSafeProducerHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &limitedResponseTransport{
			base:     cloneDefaultTransport(),
			maxBytes: maxResponseBodyBytes,
		},
	}
}

// newSafeConsumerHTTPClient builds an HTTP client suitable for
// webex-message-handler: no global Client.Timeout (which would kill long-lived
// upgraded Mercury WebSocket connections), bounded ResponseHeaderTimeout on
// the transport so WDM/U2C/KMS/people calls cannot hang forever, redirect
// protection against HTTPS->HTTP downgrades and cross-origin token leaks, and
// response body size limiting that preserves HTTP 101 WebSocket upgrades.
func newSafeConsumerHTTPClient() *http.Client {
	base := cloneDefaultTransport()
	if tr, ok := base.(*http.Transport); ok && tr.ResponseHeaderTimeout == 0 {
		tr.ResponseHeaderTimeout = defaultConsumerHeaderTimeout
	}
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxConsumerRedirects {
				return fmt.Errorf("webex: stopped after %d redirects", maxConsumerRedirects)
			}
			prev := via[len(via)-1].URL
			if strings.EqualFold(prev.Scheme, "https") && !strings.EqualFold(req.URL.Scheme, "https") {
				return fmt.Errorf("webex: refusing redirect downgrade from https to %s", req.URL.Scheme)
			}
			if !sameOrigin(prev, req.URL) {
				return fmt.Errorf("webex: refusing cross-origin redirect")
			}
			return nil
		},
		Transport: &limitedResponseTransport{
			base:                   base,
			maxBytes:               maxResponseBodyBytes,
			skipSwitchingProtocols: true,
		},
	}
}

type limitedResponseTransport struct {
	base                   http.RoundTripper
	maxBytes               int64
	skipSwitchingProtocols bool
}

func (t *limitedResponseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if t.skipSwitchingProtocols && resp.StatusCode == http.StatusSwitchingProtocols {
		return resp, nil
	}
	if resp.ContentLength > t.maxBytes {
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, fmt.Errorf("webex: response body exceeds the %d byte limit", t.maxBytes)
	}
	if resp.Body != nil && resp.Body != http.NoBody {
		resp.Body = &limitedReadCloser{
			rc:        resp.Body,
			remaining: t.maxBytes,
			limit:     t.maxBytes,
		}
	}
	return resp, nil
}

func (t *limitedResponseTransport) CloseIdleConnections() {
	type idleCloser interface {
		CloseIdleConnections()
	}
	if ic, ok := t.base.(idleCloser); ok {
		ic.CloseIdleConnections()
	}
}

type limitedReadCloser struct {
	rc        io.ReadCloser
	remaining int64
	limit     int64
	exceeded  bool
}

func (l *limitedReadCloser) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if l.exceeded {
		return 0, fmt.Errorf("webex: response body exceeds the %d byte limit", l.limit)
	}
	if l.remaining <= 0 {
		var probe [1]byte
		n, err := l.rc.Read(probe[:])
		if n > 0 {
			l.exceeded = true
			return 0, fmt.Errorf("webex: response body exceeds the %d byte limit", l.limit)
		}
		if err != nil {
			return 0, err
		}
		return 0, io.EOF
	}
	if int64(len(p)) > l.remaining {
		p = p[:l.remaining]
	}
	n, err := l.rc.Read(p)
	l.remaining -= int64(n)
	return n, err
}

func (l *limitedReadCloser) Close() error {
	return l.rc.Close()
}
