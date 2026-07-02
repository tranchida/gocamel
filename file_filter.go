package gocamel

import (
	"regexp"
)

// MatchFileName checks whether a filename matches the include and exclude regular expressions.
// If include is provided, the filename must match.
// If exclude is provided, the filename must not match.
func MatchFileName(filename, include, exclude string) bool {
	if include != "" {
		matched, err := regexp.MatchString(include, filename)
		if err != nil || !matched {
			return false
		}
	}
	if exclude != "" {
		matched, err := regexp.MatchString(exclude, filename)
		if err == nil && matched {
			return false
		}
	}
	return true
}
