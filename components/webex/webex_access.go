package webex

import (
	"fmt"
	"strings"
)

// accessList restricts inbound Webex messages by sender email address or
// domain. When both lists are empty, all senders are allowed.
type accessList struct {
	domains []string
	emails  []string
}

// parseAccessList parses comma-separated allowedDomains and allowedEmails URI
// options. Empty strings denote no restriction for that dimension; explicit
// non-empty values containing no valid entries (such as ",") are rejected to
// avoid silently failing open on a misconfigured URI.
func parseAccessList(rawDomains, rawEmails string) (accessList, error) {
	var list accessList

	if rawDomains != "" {
		domains, err := parseAllowedDomains(rawDomains)
		if err != nil {
			return accessList{}, err
		}
		list.domains = domains
	}

	if rawEmails != "" {
		emails, err := parseAllowedEmails(rawEmails)
		if err != nil {
			return accessList{}, err
		}
		list.emails = emails
	}

	return list, nil
}

func parseAllowedDomains(raw string) ([]string, error) {
	var result []string
	for _, item := range strings.Split(raw, ",") {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == "" {
			continue
		}
		item = strings.TrimPrefix(item, "@")
		if item == "" || strings.Contains(item, "@") || containsWhitespace(item) {
			return nil, fmt.Errorf("webex: invalid domain in allowedDomains: %q", item)
		}
		result = append(result, item)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("webex: allowedDomains must contain at least one valid domain")
	}
	return result, nil
}

func parseAllowedEmails(raw string) ([]string, error) {
	var result []string
	for _, item := range strings.Split(raw, ",") {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == "" {
			continue
		}
		local, domain, ok := splitEmail(item)
		if !ok || local == "" || domain == "" || containsWhitespace(item) {
			return nil, fmt.Errorf("webex: invalid email in allowedEmails: %q", item)
		}
		result = append(result, item)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("webex: allowedEmails must contain at least one valid email")
	}
	return result, nil
}

// allowed reports whether the given sender email passes the access list.
func (l accessList) allowed(email string) bool {
	if len(l.domains) == 0 && len(l.emails) == 0 {
		return true
	}

	normalized := strings.ToLower(strings.TrimSpace(email))
	if normalized == "" {
		return false
	}

	for _, allowedEmail := range l.emails {
		if allowedEmail == normalized {
			return true
		}
	}

	if len(l.domains) == 0 {
		return false
	}

	local, domain, ok := splitEmail(normalized)
	if !ok || local == "" || domain == "" {
		return false
	}
	for _, allowedDomain := range l.domains {
		if allowedDomain == domain {
			return true
		}
	}
	return false
}

func splitEmail(email string) (local, domain string, ok bool) {
	idx := strings.IndexByte(email, '@')
	if idx < 0 || strings.IndexByte(email[idx+1:], '@') >= 0 {
		return "", "", false
	}
	return email[:idx], email[idx+1:], true
}

func containsWhitespace(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] <= 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}
