// Package mod parses Paradox mod metadata from the standard files written by
// the Steam Workshop publish flow (.metadata/metadata.json, descriptor.mod).
package mod

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
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
	return descriptorList(modPath, "tags")
}

// Dependencies returns the names of the mods this mod requires, from
// descriptor.mod's dependencies block, or nil if there are none.
func Dependencies(modPath string) []string {
	return descriptorList(modPath, "dependencies")
}

// descriptorList reads a block of quoted strings such as
// tags={ "Gameplay" "Fixes" }, written on one line or on several.
func descriptorList(modPath, key string) []string {
	data, err := os.ReadFile(filepath.Join(modPath, "descriptor.mod"))
	if err != nil {
		return nil
	}
	block := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `\s*=\s*\{([^}]*)\}`).FindSubmatch(data)
	if block == nil {
		return nil
	}
	var items []string
	for _, m := range quotedRe.FindAllSubmatch(block[1], -1) {
		if item := strings.TrimSpace(string(m[1])); item != "" {
			items = append(items, item)
		}
	}
	return items
}

var quotedRe = regexp.MustCompile(`"([^"]*)"`)

// versionRe matches version labels in mod titles: "[3.12]", "(v4.0.2)",
// "{1.9}", "- v2.1", "v1.0".
var versionRe = regexp.MustCompile(`(?i)\s*[\[({][^\])}]*\d[^\])}]*[\])}]|\s*[-–]?\s*\bv?\d+(\.\d+)+\w*\b`)

// CleanName strips version labels from a mod title, which only distract a
// translator: "[3.12] Gigastructural Engineering & More (4.0)" becomes
// "Gigastructural Engineering & More".
func CleanName(name string) string {
	cleaned := strings.Trim(versionRe.ReplaceAllString(name, ""), " -–|:")
	if cleaned == "" {
		return strings.TrimSpace(name)
	}
	return strings.Join(strings.Fields(cleaned), " ")
}

// genericTags say nothing about a mod's subject matter.
var genericTags = map[string]bool{
	"gameplay": true, "fixes": true, "utilities": true, "balance": true,
	"graphics": true, "sound": true, "translation": true, "loading screen": true,
	"user interface": true, "ui": true, "events": true, "historical": true,
	"overhaul": true, "map": true,
}

// ThemeTags keeps only the tags that describe what a mod is about.
func ThemeTags(tags []string) []string {
	var out []string
	for _, tag := range tags {
		if !genericTags[strings.ToLower(tag)] {
			out = append(out, tag)
		}
	}
	return out
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
