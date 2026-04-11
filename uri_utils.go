package gocamel

import (
	"fmt"
	"net/url"
)

// ParseURI parses an endpoint URI into a *url.URL
func ParseURI(uri string) (*url.URL, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid URI: %w", err)
	}
	return u, nil
}
