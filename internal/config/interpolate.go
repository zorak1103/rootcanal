package config

import (
	"fmt"
	"os"
	"regexp"
)

// envPattern matches ${VAR} and ${VAR:-default}.
var envPattern = regexp.MustCompile(`\$\{([^}:]+)(?::-(.*?))?\}`)

// Expand replaces ${VAR} and ${VAR:-default} in raw bytes using environment
// variables. Returns an error if a required variable (no :- default) is unset.
// Expansion runs on raw bytes before YAML parsing so it applies to any field.
func Expand(raw []byte) ([]byte, error) {
	var firstErr error
	result := envPattern.ReplaceAllFunc(raw, func(match []byte) []byte {
		if firstErr != nil {
			return match
		}
		parts := envPattern.FindSubmatch(match)
		// parts[1] = variable name, parts[2] = default value (nil if no :-)
		name := string(parts[1])
		hasDefault := parts[2] != nil

		val, ok := os.LookupEnv(name)
		if !ok {
			if hasDefault {
				return parts[2]
			}
			firstErr = fmt.Errorf("environment variable %q is not set (no default provided)", name)
			return match
		}
		return []byte(val)
	})
	if firstErr != nil {
		return nil, firstErr
	}
	return result, nil
}
