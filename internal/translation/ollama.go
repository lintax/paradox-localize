package translation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	defaultOllamaModel    = "qwen2.5:1.5b"
	defaultOllamaBaseURL  = "http://localhost:11434"
	ollamaParallelWorkers = 4
)

// OllamaBackend uses Ollama for translation via its HTTP API.
type OllamaBackend struct {
	baseURL          string
	model            string
	targetLang       string
	client           *http.Client
	systemPrompt     string
	baseSystemPrompt string
	logFn            LogFunc
}

type ollamaChatRequest struct {
	Model    string                 `json:"model"`
	Messages []ollamaMessage        `json:"messages"`
	Stream   bool                   `json:"stream"`
	Options  map[string]interface{} `json:"options,omitempty"`
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatResponse struct {
	Message ollamaMessage `json:"message"`
}

type ollamaTagsResponse struct {
	Models []ollamaModelInfo `json:"models"`
}

type ollamaModelInfo struct {
	Name string `json:"name"`
}

type ollamaPullRequest struct {
	Name   string `json:"name"`
	Stream bool   `json:"stream"`
}

// resolveOllamaURL picks the base URL with explicit-config > env-var > default precedence.
func resolveOllamaURL(explicit string) string {
	if explicit != "" {
		return normalizeURL(explicit)
	}
	if host := os.Getenv("OLLAMA_HOST"); host != "" {
		return normalizeURL(host)
	}
	return defaultOllamaBaseURL
}

func normalizeURL(u string) string {
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return "http://" + u
	}
	return u
}

// IsOllamaAvailable checks if an Ollama server is reachable at the given URL.
// Pass an empty string to use the default (env or localhost).
func IsOllamaAvailable(baseURL string) bool {
	url := resolveOllamaURL(baseURL)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url + "/api/tags")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

// NewOllamaBackend creates a new Ollama translation backend.
func NewOllamaBackend(cfg BackendConfig, targetLangName string, logFn LogFunc) (*OllamaBackend, error) {
	baseURL := resolveOllamaURL(cfg.BaseURL)
	model := cfg.Model
	if model == "" {
		model = defaultOllamaModel
	}
	logFn("  Server: %s", baseURL)
	logFn("  Model:  %s", model)

	client := &http.Client{
		Timeout: 5 * time.Minute,
		Transport: &http.Transport{
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: ollamaParallelWorkers + 1,
			MaxConnsPerHost:     ollamaParallelWorkers + 1,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	backend := &OllamaBackend{
		baseURL:    baseURL,
		model:      model,
		targetLang: targetLangName,
		client:     client,
		logFn:      logFn,
	}
	backend.baseSystemPrompt = buildBaseSystemPrompt(targetLangName)
	backend.systemPrompt = backend.baseSystemPrompt

	if err := backend.ensureModel(); err != nil {
		return nil, err
	}

	logFn("  ✓ Ollama backend ready")
	return backend, nil
}

// TranslateText sends text to Ollama for translation.
func (b *OllamaBackend) TranslateText(text string) (string, error) {
	reqBody := ollamaChatRequest{
		Model: b.model,
		Messages: []ollamaMessage{
			{Role: "system", Content: b.systemPrompt},
			{Role: "user", Content: text},
		},
		Stream: false,
		Options: map[string]interface{}{
			"temperature": 0.1,
		},
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := b.client.Post(b.baseURL+"/api/chat", "application/json", bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama returned status %d: %s", resp.StatusCode, string(body))
	}

	var chatResp ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	return cleanLLMResponse(chatResp.Message.Content), nil
}

// TranslateBatch translates multiple strings using parallel workers.
func (b *OllamaBackend) TranslateBatch(texts []string, onProgress func(done, total int)) ([]string, error) {
	return runParallelWorkers(texts, ollamaParallelWorkers, b.TranslateText, onProgress)
}

// SetContext updates the system prompt with game and mod context.
func (b *OllamaBackend) SetContext(gameContext, modName string, modTags []string) {
	b.systemPrompt = applyContextToPrompt(b.baseSystemPrompt, gameContext, modName, modTags)
}

func (b *OllamaBackend) Name() string {
	return fmt.Sprintf("Ollama (%s)", b.model)
}

func (b *OllamaBackend) Close() error { return nil }

// ensureModel checks if the model is available and pulls it if needed.
func (b *OllamaBackend) ensureModel() error {
	resp, err := b.client.Get(b.baseURL + "/api/tags")
	if err != nil {
		return fmt.Errorf("failed to connect to Ollama: %w", err)
	}
	defer resp.Body.Close()

	var tags ollamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return fmt.Errorf("failed to parse Ollama models list: %w", err)
	}

	for _, m := range tags.Models {
		if m.Name == b.model || strings.HasPrefix(m.Name, b.model) {
			return nil
		}
	}

	b.logFn("  Model %s not found locally. Downloading (this may take several minutes)...", b.model)

	pullReq := ollamaPullRequest{Name: b.model, Stream: false}
	data, err := json.Marshal(pullReq)
	if err != nil {
		return fmt.Errorf("failed to marshal pull request: %w", err)
	}

	pullClient := &http.Client{Timeout: 30 * time.Minute}
	pullResp, err := pullClient.Post(b.baseURL+"/api/pull", "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to pull model: %w", err)
	}
	defer pullResp.Body.Close()

	if pullResp.StatusCode != 200 {
		body, _ := io.ReadAll(pullResp.Body)
		return fmt.Errorf("failed to pull model (status %d): %s", pullResp.StatusCode, string(body))
	}

	io.ReadAll(pullResp.Body)
	b.logFn("  ✓ Model %s downloaded successfully", b.model)
	return nil
}

// thinkRe strips <think>...</think> blocks emitted by reasoning models.
var thinkRe = regexp.MustCompile(`(?s)<think>.*?</think>`)

// cleanLLMResponse strips reasoning blocks, surrounding quotes, and code fences from
// LLM output. Shared by Ollama and OpenAI backends.
func cleanLLMResponse(response string) string {
	response = thinkRe.ReplaceAllString(response, "")
	response = strings.TrimSpace(response)

	if len(response) >= 2 {
		first := rune(response[0])
		last := rune(response[len(response)-1])
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			response = response[1 : len(response)-1]
		}
	}
	if strings.HasPrefix(response, "«") && strings.HasSuffix(response, "»") {
		response = strings.TrimPrefix(response, "«")
		response = strings.TrimSuffix(response, "»")
	}
	if strings.HasPrefix(response, "„") && strings.HasSuffix(response, "“") {
		response = strings.TrimPrefix(response, "„")
		response = strings.TrimSuffix(response, "“")
	}

	response = strings.TrimPrefix(response, "```\n")
	response = strings.TrimSuffix(response, "\n```")
	response = strings.TrimPrefix(response, "```")
	response = strings.TrimSuffix(response, "```")

	return strings.TrimSpace(response)
}

// buildBaseSystemPrompt is shared between backends to keep prompting consistent.
func buildBaseSystemPrompt(targetLangName string) string {
	return fmt.Sprintf(
		"You are a translation engine for video game content. Translate English text to %s.\n\n"+
			"CRITICAL: You MUST preserve ALL placeholders in the translation. Placeholders look like {0}, {1}, {2}, etc. "+
			"They MUST appear in your output exactly as they appear in the input. Never translate, remove, or modify placeholders.\n\n"+
			"Rules:\n"+
			"- Output ONLY the translated text\n"+
			"- Keep ALL {N} placeholders exactly as-is\n"+
			"- Keep the same tone and style\n"+
			"- Do not add quotes, explanations, notes, or any extra text\n"+
			"- Do not use markdown formatting\n"+
			"- If input is a single word, output a single word",
		targetLangName,
	)
}

func applyContextToPrompt(base, gameContext, modName string, modTags []string) string {
	out := base
	if gameContext != "" {
		out += fmt.Sprintf("\n\nGame context: This is %s.", gameContext)
	}
	if modName != "" {
		out += fmt.Sprintf("\nCurrent mod: \"%s\"", modName)
		if len(modTags) > 0 {
			out += fmt.Sprintf(" (tags: %s)", strings.Join(modTags, ", "))
		}
		out += "."
	}
	return out
}

// runParallelWorkers fans `texts` out across N workers calling translate(text).
// On the first error, remaining workers exit early. Used by both backends.
func runParallelWorkers(texts []string, workers int, translate func(string) (string, error), onProgress func(done, total int)) ([]string, error) {
	total := len(texts)
	if total == 0 {
		return nil, nil
	}
	if workers < 1 {
		workers = 1
	}

	results := make([]string, total)
	type workItem struct {
		index int
		text  string
	}
	work := make(chan workItem, total)
	for i, text := range texts {
		work <- workItem{index: i, text: text}
	}
	close(work)

	var mu sync.Mutex
	var firstErr error
	done := 0

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range work {
				mu.Lock()
				if firstErr != nil {
					mu.Unlock()
					return
				}
				mu.Unlock()

				translated, err := translate(item.text)

				mu.Lock()
				if err != nil {
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					return
				}
				results[item.index] = translated
				done++
				if onProgress != nil {
					onProgress(done, total)
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}
