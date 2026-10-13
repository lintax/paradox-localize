package translation

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/lintax/paradox-localize/internal/config"
)

// Stats tracks translation statistics
type Stats struct {
	ActualTranslations int
	CacheHits          int
	Skipped            int
}

// CacheHitRate returns the cache hit rate as a percentage
func (s *Stats) CacheHitRate() float64 {
	total := s.ActualTranslations + s.CacheHits + s.Skipped
	if total == 0 {
		return 0
	}
	return float64(s.CacheHits) / float64(total) * 100
}

// LogFunc is a function for logging messages to the GUI or console
type LogFunc func(format string, args ...interface{})

// cacheEntry is a single translation cache entry for JSONL format
type cacheEntry struct {
	K string `json:"k"`
	V string `json:"v"`
}

// Translator wraps a translation backend with caching, placeholder handling, and stats
type Translator struct {
	backend       Backend
	cache         map[string]string
	unsaved       []cacheEntry // new entries not yet written to disk
	stats         Stats
	sourceLang    string
	targetLang    string
	targetLangISO string
	gameID        string // for per-game cache files
	debug         bool
	logFn         LogFunc
}

// NewTranslator creates a new translator instance, instantiating the backend
// described by cfg. gameID is used for per-game cache files. logFn writes
// status; pass nil to log to stdout.
func NewTranslator(cfg BackendConfig, sourceLang, targetLangID, gameID string, debug bool, logFn LogFunc) (*Translator, error) {
	if logFn == nil {
		logFn = func(format string, args ...interface{}) {
			fmt.Printf(format+"\n", args...)
		}
	}

	targetLangISO, exists := config.ISO639Codes[targetLangID]
	if !exists {
		return nil, fmt.Errorf("unknown target language: %s", targetLangID)
	}
	targetLangName := config.Languages[targetLangID]

	logFn("  Using %s translation backend", cfg.Type)
	backend, err := NewBackend(cfg, targetLangName, logFn)
	if err != nil {
		return nil, fmt.Errorf("%s backend failed: %w", cfg.Type, err)
	}

	translator := &Translator{
		backend:       backend,
		cache:         make(map[string]string, 128),
		stats:         Stats{},
		sourceLang:    sourceLang,
		targetLang:    targetLangID,
		targetLangISO: targetLangISO,
		gameID:        gameID,
		debug:         debug,
		logFn:         logFn,
	}

	if err := translator.loadCacheFromFile(); err != nil {
		if debug {
			logFn("  Note: Could not load cache file: %v", err)
		}
	} else if len(translator.cache) > 0 {
		logFn("  Loaded %d cached translations", len(translator.cache))
	}

	return translator, nil
}

// SetContext passes game and mod context to the backend for better translations
func (t *Translator) SetContext(gameContext, modName string, modTags []string) {
	t.backend.SetContext(gameContext, modName, modTags)
}

// BackendName returns the name of the active translation backend
func (t *Translator) BackendName() string {
	return t.backend.Name()
}

// translatorChunkSize is how many items to translate before saving cache and reporting progress.
// With append-only JSONL cache, saving is cheap so we can update frequently.
const translatorChunkSize = 4 // matches parallel worker count for responsive updates

// TranslateBatch translates multiple strings efficiently using backend batching.
// Cache is saved to disk after every chunk to avoid losing work on app close.
// onProgress receives (done, total, eta string).
func (t *Translator) TranslateBatch(texts []string, onProgress func(done, total int, eta string)) ([]string, error) {
	results := make([]string, len(texts))
	total := len(texts)

	type pendingItem struct {
		index        int
		cleanText    string
		placeholders map[string]string
		originalText string
	}
	var pending []pendingItem

	// Phase 1: filter — handle cache/skip, identify texts needing translation
	for i, text := range texts {
		if strings.TrimSpace(text) == "" {
			t.stats.Skipped++
			results[i] = text
			continue
		}

		cleanText, placeholders := ReplaceSyntaxWithPlaceholders(text)

		if IsOnlySyntax(cleanText, placeholders) {
			t.stats.Skipped++
			results[i] = text
			continue
		}

		if cached, found := t.cache[cleanText]; found {
			t.stats.CacheHits++
			results[i] = RestorePlaceholders(cached, placeholders)
			continue
		}

		pending = append(pending, pendingItem{
			index:        i,
			cleanText:    cleanText,
			placeholders: placeholders,
			originalText: text,
		})
	}

	skippedCount := total - len(pending)

	// Report progress after cache/skip filtering so UI doesn't appear stuck
	if onProgress != nil && skippedCount > 0 {
		onProgress(skippedCount, total, "")
	}

	if len(pending) == 0 {
		return results, nil
	}

	completedCount := 0
	batchStartTime := time.Now()

	// Phase 2+3: process pending items in chunks, saving cache after each
	for chunkStart := 0; chunkStart < len(pending); chunkStart += translatorChunkSize {
		chunkEnd := chunkStart + translatorChunkSize
		if chunkEnd > len(pending) {
			chunkEnd = len(pending)
		}
		chunk := pending[chunkStart:chunkEnd]

		// Collect texts for this chunk
		chunkTexts := make([]string, len(chunk))
		for i, p := range chunk {
			chunkTexts[i] = p.cleanText
		}

		// Translate chunk
		translated, err := t.backend.TranslateBatch(chunkTexts, nil)
		if err != nil {
			return nil, err
		}

		// Validate, cache, restore placeholders
		placeholderLost := 0
		for i, p := range chunk {
			translatedText := translated[i]

			if !validatePlaceholders(translatedText, p.placeholders) {
				recovered := false
				// Only retry if few placeholders — small LLMs can't handle many
				if len(p.placeholders) <= 3 {
					if retryText, err := t.backend.TranslateText(p.cleanText); err == nil && validatePlaceholders(retryText, p.placeholders) {
						translatedText = retryText
						recovered = true
					}
				}
				if !recovered {
					placeholderLost++
					t.stats.Skipped++
					results[p.index] = p.originalText
					continue
				}
			}

			t.cacheTranslation(p.cleanText, translatedText)
			t.stats.ActualTranslations++
			results[p.index] = RestorePlaceholders(translatedText, p.placeholders)
		}

		if placeholderLost > 0 {
			fmt.Printf("    ⚠ %d/%d items lost placeholders, using original text\n", placeholderLost, len(chunk))
		}

		// Save cache to disk after each chunk
		t.saveCacheToFile()

		completedCount += len(chunk)
		if onProgress != nil {
			eta := ""
			if completedCount > 0 {
				elapsed := time.Since(batchStartTime)
				perItem := elapsed / time.Duration(completedCount)
				remaining := perItem * time.Duration(len(pending)-completedCount)
				if remaining > time.Second {
					eta = formatDuration(remaining)
				}
			}
			onProgress(skippedCount+completedCount, total, eta)
		}
	}

	return results, nil
}

// ClearCache removes the translation cache file from disk for a specific game+language
func ClearCache(sourceLang, targetLangID, gameID string) error {
	targetLangISO, exists := config.ISO639Codes[targetLangID]
	if !exists {
		return fmt.Errorf("unknown target language: %s", targetLangID)
	}
	cacheDir := getCacheDir()
	filename := fmt.Sprintf("translation_cache_%s_%s_%s.json", gameID, sourceLang, targetLangISO)
	cachePath := filepath.Join(cacheDir, filename)

	if err := os.Remove(cachePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// cacheTranslation stores a translation in the in-memory cache and marks it as unsaved
func (t *Translator) cacheTranslation(key, value string) {
	t.cache[key] = value
	t.unsaved = append(t.unsaved, cacheEntry{K: key, V: value})
}

// validatePlaceholders checks if all placeholders are present in the translated text
func validatePlaceholders(text string, placeholders map[string]string) bool {
	for placeholder := range placeholders {
		if !strings.Contains(text, placeholder) {
			return false
		}
	}
	return true
}

// formatDuration formats a duration as "Xm Ys" or "Ys"
func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// GetStats returns current translation statistics
func (t *Translator) GetStats() Stats {
	return t.stats
}

// Close closes the translator and releases resources
func (t *Translator) Close() error {
	// Save cache to disk before closing
	if err := t.saveCacheToFile(); err != nil {
		fmt.Printf("Warning: Failed to save translation cache: %v\n", err)
	}
	return t.backend.Close()
}

// CacheDir returns the platform-specific cache directory of the application.
func CacheDir() string {
	return getCacheDir()
}

// getCacheDir returns the platform-specific cache directory for translation cache files.
func getCacheDir() string {
	var cacheDir string

	switch runtime.GOOS {
	case "windows":
		cacheDir = os.Getenv("LOCALAPPDATA")
		if cacheDir == "" {
			cacheDir = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
		}
		cacheDir = filepath.Join(cacheDir, "paradox-localize")
	case "darwin":
		home, err := os.UserHomeDir()
		if err == nil {
			cacheDir = filepath.Join(home, "Library", "Caches", "paradox-localize")
		}
	default:
		cacheDir = os.Getenv("XDG_CACHE_HOME")
		if cacheDir == "" {
			home, err := os.UserHomeDir()
			if err == nil {
				cacheDir = filepath.Join(home, ".cache")
			}
		}
		if cacheDir != "" {
			cacheDir = filepath.Join(cacheDir, "paradox-localize")
		}
	}

	if cacheDir == "" {
		cacheDir = "cache"
	}

	os.MkdirAll(cacheDir, 0755)
	return cacheDir
}

// getCacheFilePath returns the path to the translation cache file for this game+language pair
func (t *Translator) getCacheFilePath() string {
	cacheDir := getCacheDir()
	filename := fmt.Sprintf("translation_cache_%s_%s_%s.json", t.gameID, t.sourceLang, t.targetLangISO)
	return filepath.Join(cacheDir, filename)
}

// loadCacheFromFile loads the translation cache from disk.
// Supports both old JSON map format and new JSONL (one entry per line) format.
func (t *Translator) loadCacheFromFile() error {
	cachePath := t.getCacheFilePath()

	data, err := os.ReadFile(cachePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	if len(data) == 0 {
		return nil
	}

	// Try old JSON map format first: {"key": "value", ...}
	if data[0] == '{' {
		oldCache := make(map[string]string)
		if json.Unmarshal(data, &oldCache) == nil {
			t.cache = oldCache
			// Migrate: rewrite as JSONL on next save by marking all as unsaved
			for k, v := range oldCache {
				t.unsaved = append(t.unsaved, cacheEntry{K: k, V: v})
			}
			return nil
		}
	}

	// Parse JSONL format: one {"k":"...","v":"..."} per line
	// Use bufio.Scanner to avoid allocating a massive string array for large files
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // up to 1MB lines
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var entry cacheEntry
		if json.Unmarshal(line, &entry) == nil && entry.K != "" {
			t.cache[entry.K] = entry.V
		}
	}

	return nil
}

// saveCacheToFile appends only new (unsaved) entries to the cache file in JSONL format.
func (t *Translator) saveCacheToFile() error {
	if len(t.unsaved) == 0 {
		return nil
	}

	cachePath := t.getCacheFilePath()

	f, err := os.OpenFile(cachePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open cache file: %w", err)
	}
	defer f.Close()

	for _, entry := range t.unsaved {
		line, err := json.Marshal(entry)
		if err != nil {
			continue
		}
		f.Write(line)
		f.Write([]byte("\n"))
	}

	t.unsaved = t.unsaved[:0] // clear unsaved buffer
	return nil
}
