package localization

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lintax/paradox-localize/internal/config"
)

const (
	UnixLineEnding = "\n" // Paradox uses Unix line endings (verified from vanilla EU5 files)
)

// WriteConsolidatedFile writes consolidated translation files.
// Two copies are written with different prefixes to ensure priority regardless of
// which load order the game engine uses:
//   - "0_..." sorts first in ASCII order (wins in FIOS games like Stellaris)
//   - "zz_..." sorts last in ASCII order (wins in reverse-alpha games like EU5)
func WriteConsolidatedFile(localizationRoot string, targetLangCode string, modID string, translations map[string]string) error {
	if len(translations) == 0 {
		return nil
	}

	langFolder := filepath.Join(localizationRoot, targetLangCode)
	if err := os.MkdirAll(langFolder, 0755); err != nil {
		return fmt.Errorf("failed to create language folder: %w", err)
	}

	baseName := modID + "_" + config.ConsolidatedFilePrefix + "_l_" + targetLangCode + ".yml"

	// Remove old-format file (no prefix) from previous versions
	oldPath := filepath.Join(langFolder, baseName)
	os.Remove(oldPath) // ignore error — file may not exist

	// Write the first copy (sorts first in ASCII — digits before letters)
	firstPath := filepath.Join(langFolder, "0_"+baseName)
	if err := writeLocalizationFile(firstPath, targetLangCode, translations); err != nil {
		return err
	}

	// Create second file (sorts last — 'z' after all letters)
	// Try hardlink first (zero I/O), fall back to copy
	secondPath := filepath.Join(langFolder, "zz_"+baseName)
	os.Remove(secondPath) // remove existing before linking
	if err := os.Link(firstPath, secondPath); err != nil {
		// Hardlink failed (e.g. Windows or cross-device), fall back to copy
		if err := copyFile(firstPath, secondPath); err != nil {
			return err
		}
	}

	return nil
}

// writeLocalizationFile writes a single localization .yml file
func writeLocalizationFile(filePath, targetLangCode string, translations map[string]string) error {
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)

	// Write UTF-8 BOM
	writer.WriteString(BOM)

	// Write language header
	fmt.Fprintf(writer, "l_%s:%s", targetLangCode, UnixLineEnding)

	// Sort keys for consistent output
	keys := make([]string, 0, len(translations))
	for key := range translations {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	// Write key-value pairs
	for _, key := range keys {
		value := escapeQuotes(translations[key])

		// Key with version suffix (e.g. "KEY:0") uses space, otherwise colon-space
		separator := ": "
		if strings.Contains(key, ":") {
			separator = " "
		}

		fmt.Fprintf(writer, " %s%s\"%s\"%s", key, separator, value, UnixLineEnding)
	}

	return writer.Flush()
}

// copyFile copies src to dst
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// escapeQuotes escapes double quotes in localization values
func escapeQuotes(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i, ch := range s {
		if ch == '"' {
			if i == 0 || s[i-1] != '\\' {
				b.WriteString("\\\"")
				continue
			}
		}
		b.WriteRune(ch)
	}
	return b.String()
}
