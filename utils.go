package gocamel

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// ParseURI parses an endpoint URI into a *url.URL
func ParseURI(uri string) (*url.URL, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid URI: %w", err)
	}
	return u, nil
}

// patternToRegex convertit un pattern avec '*' en Regexp
func patternToRegex(pattern string) *regexp.Regexp {
	// Échapper les caractères spéciaux de regex, puis remplacer '*' par '.*'
	quoted := regexp.QuoteMeta(pattern)
	regexStr := "^" + strings.ReplaceAll(quoted, "\\*", ".*") + "$"
	re, _ := regexp.Compile(regexStr)
	return re
}
