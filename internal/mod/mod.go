// Package mod parses Paradox mod metadata from the standard files written by
// the Steam Workshop publish flow (.metadata/metadata.json, descriptor.mod).
package mod

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// metadataFile mirrors the subset of .metadata/metadata.json that we read.
type metadataFile struct {
	Name string `json:"name"`
}

// Name returns the mod's display name. It first tries .metadata/metadata.json,
// then falls back to descriptor.mod's "name=" field, and finally to fallbackID.
func Name(modPath, fallbackID string) string {
	if name := nameFromMetadata(modPath); name != "" {
		return name
	}
	if name := nameFromDescriptor(modPath); name != "" {
		return name
	}
	return fallbackID
}

// Tags returns the mod's tags from descriptor.mod, or nil if none are present.
func Tags(modPath string) []string {
	data, err := os.ReadFile(filepath.Join(modPath, "descriptor.mod"))
	if err != nil {
		return nil
	}

	content := string(data)
	tagsStart := strings.Index(content, "tags={")
	if tagsStart < 0 {
		return nil
	}
	tagsEnd := strings.Index(content[tagsStart:], "}")
	if tagsEnd < 0 {
		return nil
	}

	tagsBlock := content[tagsStart+len("tags={") : tagsStart+tagsEnd]
	var tags []string
	for _, line := range strings.Split(tagsBlock, "\n") {
		line = strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "\""))
		if line != "" {
			tags = append(tags, line)
		}
	}
	return tags
}

func nameFromMetadata(modPath string) string {
	data, err := os.ReadFile(filepath.Join(modPath, ".metadata", "metadata.json"))
	if err != nil {
		return ""
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // strip BOM if present
	var meta metadataFile
	if err := json.Unmarshal(data, &meta); err != nil {
		return ""
	}
	return meta.Name
}

func nameFromDescriptor(modPath string) string {
	data, err := os.ReadFile(filepath.Join(modPath, "descriptor.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "name=") {
			continue
		}
		name := strings.Trim(strings.TrimPrefix(line, "name="), "\"")
		if name != "" {
			return name
		}
	}
	return ""
}
