package translation

import "fmt"

// Backend performs raw text translation without caching or placeholder handling.
type Backend interface {
	// TranslateText translates a single plain text string.
	TranslateText(text string) (string, error)
	// TranslateBatch translates multiple strings at once. onProgress is called
	// with (completed, total) counts after each item if non-nil.
	TranslateBatch(texts []string, onProgress func(done, total int)) ([]string, error)
	// SetContext provides optional context about the game and current mod to
	// improve translation quality. Backends without contextual support may ignore.
	SetContext(gameContext, modName string, modTags []string)
	// Name returns the backend name for display.
	Name() string
	// Close releases backend resources.
	Close() error
}

// BackendType identifies which translation backend to instantiate.
type BackendType string

const (
	BackendOllama BackendType = "ollama"
	BackendOpenAI BackendType = "openai" // OpenAI-compatible HTTP API
)

// BackendConfig holds the user-configurable settings for a translation backend.
// Empty fields fall back to defaults appropriate for the chosen Type.
type BackendConfig struct {
	Type    BackendType
	BaseURL string // override server URL (optional for Ollama, required for OpenAI)
	Model   string // model name (optional, defaults per backend)
	APIKey  string // OpenAI-compatible API key (ignored by Ollama)
}

// NewBackend constructs a Backend from a config.
func NewBackend(cfg BackendConfig, targetLangName string, logFn LogFunc) (Backend, error) {
	switch cfg.Type {
	case "", BackendOllama:
		return NewOllamaBackend(cfg, targetLangName, logFn)
	case BackendOpenAI:
		return NewOpenAIBackend(cfg, targetLangName, logFn)
	default:
		return nil, fmt.Errorf("unknown backend type: %q", cfg.Type)
	}
}
