package gocamel

import (
	"regexp"
)

// matchFileName vérifie si un nom de fichier correspond aux expressions régulières include et exclude
// Si include est fourni, le nom de fichier doit correspondre.
// Si exclude est fourni, le nom de fichier ne doit pas correspondre.
func matchFileName(filename, include, exclude string) bool {
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
