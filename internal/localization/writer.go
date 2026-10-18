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
	// linked under a temporary name and renamed over the old copy, so the
	// copy is never missing
	linkTmp := secondPath + ".tmp"
	os.Remove(linkTmp)
	if err := os.Link(firstPath, linkTmp); err == nil {
		if err := os.Rename(linkTmp, secondPath); err != nil {
			os.Remove(linkTmp)
			return err
		}
	} else {
		// Hardlink failed (e.g. Windows or cross-device), fall back to copy
		if err := copyFile(firstPath, secondPath); err != nil {
			return err
		}
	}

	return nil
}

// writeLocalizationFile writes a single localization .yml file. It writes a
// temporary file and renames it into place, so an interrupted write (the
// window closed, a crash) leaves the previous file intact, never half of one.
func writeLocalizationFile(filePath, targetLangCode string, translations map[string]string) error {
	tmp := filePath + ".tmp"
	file, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	err = writeLocalization(file, targetLangCode, translations)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		// also replaces the hard link "zz_" shares with it: that is relinked next
		err = os.Rename(tmp, filePath)
	}
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("failed to write %s: %w", filepath.Base(filePath), err)
	}
	return nil
}

func writeLocalization(file io.Writer, targetLangCode string, translations map[string]string) error {
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

	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.Remove(tmp)
	}
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
