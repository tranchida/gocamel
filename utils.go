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

// Interpolate replaces variables of type ${header.name}, ${property.name} or ${body}
// in a string with corresponding values from the exchange.
func Interpolate(text string, exchange *Exchange) string {
	re := regexp.MustCompile(`\${(header|property|body)(?:\.([^}]+))?}`)
	return re.ReplaceAllStringFunc(text, func(match string) string {
		submatches := re.FindStringSubmatch(match)
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

		return "" // Ou retourner le match original si on veut garder ${...} quand non trouvé ? Camel laisse vide ou erreur ?
	})
}

// patternToRegex convertit un pattern avec '*' en Regexp
func patternToRegex(pattern string) *regexp.Regexp {
	// Échapper les caractères spéciaux de regex, puis remplacer '*' par '.*'
	quoted := regexp.QuoteMeta(pattern)
	regexStr := "^" + strings.ReplaceAll(quoted, "\\*", ".*") + "$"
	re, _ := regexp.Compile(regexStr)
	return re
}
