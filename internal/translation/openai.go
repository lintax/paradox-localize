package translation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultOpenAIModel    = "gpt-4o-mini"
	defaultOpenAIBaseURL  = "https://api.openai.com/v1"
	openAIParallelWorkers = 8 // cloud APIs handle higher concurrency than local Ollama
)

// OpenAIBackend talks to any OpenAI-compatible chat-completions endpoint
// (OpenAI, OpenRouter, Groq, Together, DeepInfra, local vLLM, llama.cpp server, etc.).
type OpenAIBackend struct {
	baseURL          string
	model            string
	apiKey           string
	targetLang       string
	client           *http.Client
	systemPrompt     string
	baseSystemPrompt string
	logFn            LogFunc
}

type openAIChatRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Temperature float64         `json:"temperature"`
	Stream      bool            `json:"stream"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message openAIMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// NewOpenAIBackend constructs an OpenAI-compatible backend.
func NewOpenAIBackend(cfg BackendConfig, targetLangName string, logFn LogFunc) (*OpenAIBackend, error) {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultOpenAIBaseURL
	}
	model := cfg.Model
	if model == "" {
		model = defaultOpenAIModel
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("OpenAI-compatible backend requires an API key")
	}

	logFn("  Server: %s", baseURL)
	logFn("  Model:  %s", model)

	backend := &OpenAIBackend{
		baseURL:    baseURL,
		model:      model,
		apiKey:     cfg.APIKey,
		targetLang: targetLangName,
		client: &http.Client{
			Timeout: 2 * time.Minute,
			Transport: &http.Transport{
				MaxIdleConns:        20,
				MaxIdleConnsPerHost: openAIParallelWorkers + 1,
				MaxConnsPerHost:     openAIParallelWorkers + 1,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		logFn: logFn,
	}
	backend.baseSystemPrompt = buildBaseSystemPrompt(targetLangName)
	backend.systemPrompt = backend.baseSystemPrompt

	logFn("  ✓ OpenAI-compatible backend ready")
	return backend, nil
}

func (b *OpenAIBackend) TranslateText(text string) (string, error) {
	reqBody := openAIChatRequest{
		Model: b.model,
		Messages: []openAIMessage{
			{Role: "system", Content: b.systemPrompt},
			{Role: "user", Content: text},
		},
		Temperature: 0.1,
		Stream:      false,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", b.baseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.apiKey)

	resp, err := b.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("openai returned status %d: %s", resp.StatusCode, string(body))
	}

	var chatResp openAIChatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}
	if chatResp.Error != nil {
		return "", fmt.Errorf("openai error: %s", chatResp.Error.Message)
	}
	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("openai response had no choices")
	}

	return cleanLLMResponse(chatResp.Choices[0].Message.Content), nil
}

func (b *OpenAIBackend) TranslateBatch(texts []string, onProgress func(done, total int)) ([]string, error) {
	return runParallelWorkers(texts, openAIParallelWorkers, b.TranslateText, onProgress)
}

func (b *OpenAIBackend) SetContext(gameContext, modName string, modTags []string) {
	b.systemPrompt = applyContextToPrompt(b.baseSystemPrompt, gameContext, modName, modTags)
}

func (b *OpenAIBackend) Name() string {
	return fmt.Sprintf("OpenAI-compatible (%s)", b.model)
}

func (b *OpenAIBackend) Close() error { return nil }
