package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"kaffe-letter/internal/model"
)

type Provider interface {
	Complete(ctx context.Context, systemPrompt, userPrompt string) (string, model.TokenUsage, error)
}

type Config struct {
	Provider    string
	Model       string
	BaseURL     string
	APIKey      string
	HTTPTimeout time.Duration
}

func New(cfg Config) (Provider, error) {
	provider := normalizeProvider(cfg.Provider)
	if provider == "" {
		provider = "openai"
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("llm model is required")
	}
	timeout := cfg.HTTPTimeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	switch provider {
	case "openai":
		return &openAICompatibleProvider{
			model:      cfg.Model,
			baseURL:    defaultBaseURL(provider, cfg.BaseURL),
			apiKey:     strings.TrimSpace(cfg.APIKey),
			strictJSON: true,
			client:     &http.Client{Timeout: timeout},
		}, nil
	case "local":
		return &openAICompatibleProvider{
			model:      cfg.Model,
			baseURL:    defaultBaseURL(provider, cfg.BaseURL),
			apiKey:     strings.TrimSpace(cfg.APIKey),
			noAuth:     true,
			strictJSON: false,
			client:     &http.Client{Timeout: timeout},
		}, nil
	case "anthropic":
		return &anthropicProvider{
			model:   cfg.Model,
			baseURL: defaultBaseURL(provider, cfg.BaseURL),
			apiKey:  strings.TrimSpace(cfg.APIKey),
			client:  &http.Client{Timeout: timeout},
		}, nil
	case "gemini":
		return &geminiProvider{
			model:   cfg.Model,
			baseURL: defaultBaseURL(provider, cfg.BaseURL),
			apiKey:  strings.TrimSpace(cfg.APIKey),
			client:  &http.Client{Timeout: timeout},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported llm provider: %s", cfg.Provider)
	}
}

func normalizeProvider(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func defaultBaseURL(provider, baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL != "" {
		return strings.TrimRight(baseURL, "/")
	}
	switch normalizeProvider(provider) {
	case "anthropic":
		return "https://api.anthropic.com/v1"
	case "gemini":
		return "https://generativelanguage.googleapis.com/v1beta"
	case "local":
		return "http://localhost:11434/v1"
	default:
		return "https://api.openai.com/v1"
	}
}

type openAICompatibleProvider struct {
	model      string
	baseURL    string
	apiKey     string
	noAuth     bool
	strictJSON bool
	client     *http.Client
}

func (p *openAICompatibleProvider) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, model.TokenUsage, error) {
	reqBody := map[string]any{
		"model": p.model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
	}
	if p.strictJSON {
		reqBody["response_format"] = map[string]any{"type": "json_object"}
	}
	content, _, err := p.postJSON(ctx, p.baseURL+"/chat/completions", reqBody, p.authHeader())
	if err != nil {
		return "", model.TokenUsage{}, err
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(content), &resp); err != nil {
		return "", model.TokenUsage{}, fmt.Errorf("parse openai response: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", model.TokenUsage{}, fmt.Errorf("openai returned no choices")
	}
	text := strings.TrimSpace(resp.Choices[0].Message.Content)
	if text == "" {
		return "", model.TokenUsage{}, fmt.Errorf("openai returned empty content")
	}
	return text, model.TokenUsage{
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
	}, nil
}

func (p *openAICompatibleProvider) authHeader() string {
	if p.noAuth || strings.TrimSpace(p.apiKey) == "" {
		return ""
	}
	return "Bearer " + p.apiKey
}

type anthropicProvider struct {
	model   string
	baseURL string
	apiKey  string
	client  *http.Client
}

func (p *anthropicProvider) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, model.TokenUsage, error) {
	reqBody := map[string]any{
		"model":      p.model,
		"max_tokens": 2048,
		"system":     systemPrompt,
		"messages":   []map[string]any{{"role": "user", "content": []map[string]string{{"type": "text", "text": userPrompt}}}},
	}
	content, _, err := p.postJSON(ctx, p.baseURL+"/messages", reqBody, p.apiKey)
	if err != nil {
		return "", model.TokenUsage{}, err
	}
	var resp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(content), &resp); err != nil {
		return "", model.TokenUsage{}, fmt.Errorf("parse anthropic response: %w", err)
	}
	var b strings.Builder
	for _, block := range resp.Content {
		if strings.EqualFold(block.Type, "text") {
			b.WriteString(block.Text)
		}
	}
	text := strings.TrimSpace(b.String())
	if text == "" {
		return "", model.TokenUsage{}, fmt.Errorf("anthropic returned empty content")
	}
	return text, model.TokenUsage{
		PromptTokens:     resp.Usage.InputTokens,
		CompletionTokens: resp.Usage.OutputTokens,
		TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
	}, nil
}

type geminiProvider struct {
	model   string
	baseURL string
	apiKey  string
	client  *http.Client
}

func (p *geminiProvider) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, model.TokenUsage, error) {
	reqBody := map[string]any{
		"systemInstruction": map[string]any{
			"parts": []map[string]string{{"text": systemPrompt}},
		},
		"contents": []map[string]any{
			{
				"role":  "user",
				"parts": []map[string]string{{"text": userPrompt}},
			},
		},
		"generationConfig": map[string]any{
			"responseMimeType": "application/json",
			"maxOutputTokens":  2048,
		},
	}
	content, _, err := p.postJSON(ctx, p.baseURL+"/models/"+url.PathEscape(p.model)+":generateContent", reqBody, p.apiKey)
	if err != nil {
		return "", model.TokenUsage{}, err
	}
	var resp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
			TotalTokenCount      int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal([]byte(content), &resp); err != nil {
		return "", model.TokenUsage{}, fmt.Errorf("parse gemini response: %w", err)
	}
	if len(resp.Candidates) == 0 {
		return "", model.TokenUsage{}, fmt.Errorf("gemini returned no candidates")
	}
	var b strings.Builder
	for _, part := range resp.Candidates[0].Content.Parts {
		b.WriteString(part.Text)
	}
	text := strings.TrimSpace(b.String())
	if text == "" {
		return "", model.TokenUsage{}, fmt.Errorf("gemini returned empty content")
	}
	return text, model.TokenUsage{
		PromptTokens:     resp.UsageMetadata.PromptTokenCount,
		CompletionTokens: resp.UsageMetadata.CandidatesTokenCount,
		TotalTokens:      resp.UsageMetadata.TotalTokenCount,
	}, nil
}

func (p *geminiProvider) authHeader() string {
	if strings.TrimSpace(p.apiKey) == "" {
		return ""
	}
	return p.apiKey
}

func (p *geminiProvider) postJSON(ctx context.Context, endpoint string, reqBody any, auth string) (string, model.TokenUsage, error) {
	return postJSON(ctx, p.client, endpoint, reqBody, auth, map[string]string{
		"Content-Type":   "application/json",
		"X-Goog-Api-Key": auth,
	})
}

func (p *anthropicProvider) postJSON(ctx context.Context, endpoint string, reqBody any, auth string) (string, model.TokenUsage, error) {
	return postJSON(ctx, p.client, endpoint, reqBody, auth, map[string]string{
		"Content-Type":      "application/json",
		"x-api-key":         auth,
		"anthropic-version": "2023-06-01",
	})
}

func (p *openAICompatibleProvider) postJSON(ctx context.Context, endpoint string, reqBody any, auth string) (string, model.TokenUsage, error) {
	return postJSON(ctx, p.client, endpoint, reqBody, auth, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": auth,
	})
}

func postJSON(ctx context.Context, client *http.Client, endpoint string, reqBody any, auth string, headers map[string]string) (string, model.TokenUsage, error) {
	b, err := json.Marshal(reqBody)
	if err != nil {
		return "", model.TokenUsage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return "", model.TokenUsage{}, err
	}
	for k, v := range headers {
		if strings.TrimSpace(v) == "" {
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", model.TokenUsage{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", model.TokenUsage{}, fmt.Errorf("llm status %d: %s", resp.StatusCode, string(body))
	}
	return string(body), model.TokenUsage{}, nil
}
