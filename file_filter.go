package gocamel

import (
	"fmt"
	"regexp"
)

// FileFilter is a precompiled filename filter combining include and exclude
// regular expressions.
type FileFilter struct {
	include *regexp.Regexp
	exclude *regexp.Regexp
}

// NewFileFilter compiles optional include and exclude regular expressions into
// a reusable FileFilter. Empty patterns disable the corresponding check.
func NewFileFilter(include, exclude string) (*FileFilter, error) {
	var incRe, excRe *regexp.Regexp
	if include != "" {
		re, err := regexp.Compile(include)
		if err != nil {
			return nil, fmt.Errorf("file filter: invalid include regex %q: %w", include, err)
		}
		incRe = re
	}
	if exclude != "" {
		re, err := regexp.Compile(exclude)
		if err != nil {
			return nil, fmt.Errorf("file filter: invalid exclude regex %q: %w", exclude, err)
		}
		excRe = re
	}
	return &FileFilter{
		include: incRe,
		exclude: excRe,
	}, nil
}

// Match reports whether filename satisfies the compiled include and exclude
// regular expressions. Returns false on a nil filter (fail-closed).
func (f *FileFilter) Match(filename string) bool {
	if f == nil {
		return false
	}
	if f.include != nil && !f.include.MatchString(filename) {
		return false
	}
	if f.exclude != nil && f.exclude.MatchString(filename) {
		return false
	}
	return true
}

// MatchFileName checks whether a filename matches the include and exclude regular expressions.
// If include is provided, the filename must match.
// If exclude is provided, the filename must not match.
// Invalid include or exclude regular expressions fail closed (return false) and log an error.
func MatchFileName(filename, include, exclude string) bool {
	filter, err := NewFileFilter(include, exclude)
	if err != nil {
		LogError("file filter: invalid regular expression; failing closed",
			"include", include, "exclude", exclude, "error", err)
		return false
	}
	return filter.Match(filename)
}
