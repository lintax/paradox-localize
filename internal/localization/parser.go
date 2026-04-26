package localization

import (
	"bufio"
	"os"
	"regexp"
	"strings"
)

const (
	BOM = "\ufeff" // UTF-8 BOM
)

var (
	// Header pattern: l_english: or l_russian: etc.
	headerPattern = regexp.MustCompile(`^\s*l_[a-z_]+:\s*$`)

	// Key-value pattern: key:0 "value" or key: "value"
	keyValuePattern = regexp.MustCompile(`^\s*([a-zA-Z0-9_\.]+)(:\d*)?\s*"((?:[^"\\]|\\.)*)"\s*$`)
)

// ParseLocalizationFile parses a Paradox localization .yml file
// Returns a map of key -> value
func ParseLocalizationFile(filePath string) (map[string]string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	result := make(map[string]string)
	scanner := bufio.NewScanner(file)
	inHeader := false

	for scanner.Scan() {
		line := scanner.Text()

		// Skip BOM if present
		line = strings.TrimPrefix(line, BOM)

		// Check for language header
		if headerPattern.MatchString(line) {
			inHeader = true
			continue
		}

		// Skip lines before header
		if !inHeader {
			continue
		}

		// Try to parse key-value pair
		matches := keyValuePattern.FindStringSubmatch(line)
		if matches != nil && len(matches) >= 4 {
			key := matches[1]
			versionSuffix := matches[2] // :0, :1, etc. (may be empty)
			value := matches[3]
			// Store key WITH version suffix to preserve it
			fullKey := key + versionSuffix
			result[fullKey] = value
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return result, nil
}
