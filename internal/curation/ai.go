package curation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"rss-ai-newsletter/internal/config"
	"rss-ai-newsletter/internal/model"
)

type Service struct {
	cfg        config.Config
	httpClient *http.Client
}

func NewService(cfg config.Config) Service {
	return Service{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: cfg.HTTPTimeout},
	}
}

type candidate struct {
	ID        int    `json:"id"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	Domain    string `json:"domain"`
	Summary   string `json:"summary"`
	Published string `json:"published"`
}

type aiResponse struct {
	Items []struct {
		CandidateID      int     `json:"candidate_id"`
		SummaryEN        string  `json:"summary_en"`
		WhyItMattersEN   string  `json:"why_it_matters_en"`
		RelevanceScore   float64 `json:"relevance_score"`
		NoveltyScore     float64 `json:"novelty_score"`
		CredibilityScore float64 `json:"credibility_score"`
		TargetMatch      bool    `json:"target_match"`
		TargetReason     string  `json:"target_reason"`
	} `json:"items"`
}

func (s Service) Curate(ctx context.Context, raw []model.RawItem) ([]model.CuratedItem, model.TokenUsage, error) {
	if len(raw) == 0 {
		return nil, model.TokenUsage{}, nil
	}

	cand := make([]candidate, 0, len(raw))
	idx := make(map[int]model.RawItem, len(raw))
	for i, item := range raw {
		id := i + 1
		cand = append(cand, candidate{
			ID:        id,
			Title:     item.Title,
			URL:       item.URL,
			Domain:    item.Domain,
			Summary:   item.Summary,
			Published: item.PublishedAt.Format(time.RFC3339),
		})
		idx[id] = item
	}

	chunkSize := s.cfg.CurationChunkSize
	if chunkSize <= 0 {
		chunkSize = 15
	}
	collected := make([]model.CuratedItem, 0, len(cand))
	totalUsage := model.TokenUsage{}

	for i := 0; i < len(cand); i += chunkSize {
		end := i + chunkSize
		if end > len(cand) {
			end = len(cand)
		}
		chunk := cand[i:end]
		payloadText, err := buildCurationPrompt(s.cfg, chunk)
		if err != nil {
			return nil, totalUsage, err
		}
		res, usage, err := s.callOpenAIForCuration(ctx, payloadText)
		if err != nil {
			return nil, totalUsage, err
		}
		totalUsage.Add(usage)

		for _, a := range res.Items {
			base, ok := idx[a.CandidateID]
			if !ok {
				continue
			}
			targetBoost := 0.0
			if a.TargetMatch {
				targetBoost = 100
			}
			final := (s.cfg.WeightRelevance * clamp(a.RelevanceScore)) +
				(s.cfg.WeightNovelty * clamp(a.NoveltyScore)) +
				(s.cfg.WeightCredibility * clamp(a.CredibilityScore)) +
				(s.cfg.WeightTarget * targetBoost)

			titleEN := truncate(strings.TrimSpace(base.Title), 180)
			summaryEN := truncate(strings.TrimSpace(a.SummaryEN), 280)
			whyEN := truncate(strings.TrimSpace(a.WhyItMattersEN), 240)
			if summaryEN == "" {
				summaryEN = truncate(strings.TrimSpace(base.Summary), 280)
			}
			if whyEN == "" {
				whyEN = "Relevant update for professionals in technology and business."
			}

			collected = append(collected, model.CuratedItem{
				Title:            titleEN,
				TitleEN:          titleEN,
				URL:              base.URL,
				Domain:           base.Domain,
				SummaryEN:        summaryEN,
				WhyItMattersEN:   whyEN,
				RelevanceScore:   clamp(a.RelevanceScore),
				NoveltyScore:     clamp(a.NoveltyScore),
				CredibilityScore: clamp(a.CredibilityScore),
				TargetMatch:      a.TargetMatch,
				TargetReason:     truncate(strings.TrimSpace(a.TargetReason), 160),
				FinalScore:       final,
			})
		}
	}

	sort.SliceStable(collected, func(i, j int) bool {
		return collected[i].FinalScore > collected[j].FinalScore
	})
	return collected, totalUsage, nil
}

func (s Service) TranslateForPTBR(ctx context.Context, items []model.CuratedItem) ([]model.CuratedItem, model.TokenUsage, error) {
	if len(items) == 0 {
		return items, model.TokenUsage{}, nil
	}
	chunkSize := s.cfg.CurationChunkSize
	if chunkSize <= 0 {
		chunkSize = 15
	}
	totalUsage := model.TokenUsage{}

	for i := 0; i < len(items); i += chunkSize {
		end := i + chunkSize
		if end > len(items) {
			end = len(items)
		}
		chunk := items[i:end]
		in := make([]map[string]any, 0, len(chunk))
		for j, it := range chunk {
			in = append(in, map[string]any{
				"id":                j + 1,
				"title_en":          it.TitleEN,
				"summary_en":        it.SummaryEN,
				"why_it_matters_en": it.WhyItMattersEN,
			})
		}
		b, err := json.Marshal(in)
		if err != nil {
			return items, totalUsage, err
		}

		prompt := fmt.Sprintf(`
Traduza para PT-BR mantendo sentido técnico e clareza.
Responda APENAS JSON válido no formato:
{"items":[{"id":1,"title_pt_br":"...","summary_pt_br":"...","why_it_matters_pt_br":"..."}]}

Itens:
%s
`, string(b))

		reqBody := map[string]any{
			"model":           s.cfg.OpenAIModel,
			"response_format": map[string]any{"type": "json_object"},
			"messages": []map[string]string{
				{
					"role":    "system",
					"content": "Você traduz conteúdo técnico para PT-BR com fidelidade.",
				},
				{
					"role":    "user",
					"content": prompt,
				},
			},
		}

		content, usage, err := s.callOpenAIContent(ctx, reqBody)
		if err != nil {
			return items, totalUsage, err
		}
		totalUsage.Add(usage)

		var out struct {
			Items []struct {
				ID               int    `json:"id"`
				TitlePTBR        string `json:"title_pt_br"`
				SummaryPTBR      string `json:"summary_pt_br"`
				WhyItMattersPTBR string `json:"why_it_matters_pt_br"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(content), &out); err != nil {
			return items, totalUsage, err
		}

		for _, t := range out.Items {
			if t.ID <= 0 || t.ID > len(chunk) {
				continue
			}
			idx := i + (t.ID - 1)
			title := truncate(strings.TrimSpace(t.TitlePTBR), 180)
			summary := truncate(strings.TrimSpace(t.SummaryPTBR), 280)
			why := truncate(strings.TrimSpace(t.WhyItMattersPTBR), 240)
			if title == "" || summary == "" || why == "" {
				return items, totalUsage, fmt.Errorf("translation incomplete for chunk starting at %d", i)
			}
			items[idx].TitlePTBR = title
			items[idx].SummaryPTBR = summary
			items[idx].WhyItMattersPTBR = why
			items[idx].Title = title
		}
	}
	return items, totalUsage, nil
}

func (s Service) callOpenAIForCuration(ctx context.Context, prompt string) (aiResponse, model.TokenUsage, error) {
	reqBody := map[string]any{
		"model":           s.cfg.OpenAIModel,
		"response_format": map[string]any{"type": "json_object"},
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "Você é um editor técnico rigoroso. Responda APENAS JSON válido.",
			},
			{
				"role":    "user",
				"content": prompt,
			},
		},
	}
	content, usage, err := s.callOpenAIContent(ctx, reqBody)
	if err != nil {
		return aiResponse{}, usage, err
	}

	var out aiResponse
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		return aiResponse{}, usage, fmt.Errorf("parse curation json: %w content=%s", err, content)
	}
	return out, usage, nil
}

func (s Service) callOpenAIContent(ctx context.Context, reqBody map[string]any) (string, model.TokenUsage, error) {
	b, err := json.Marshal(reqBody)
	if err != nil {
		return "", model.TokenUsage{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.OpenAIURL, bytes.NewReader(b))
	if err != nil {
		return "", model.TokenUsage{}, err
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.OpenAIAPIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", model.TokenUsage{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", model.TokenUsage{}, fmt.Errorf("openai status %d: %s", resp.StatusCode, string(body))
	}

	var chat struct {
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
	if err := json.Unmarshal(body, &chat); err != nil {
		return "", model.TokenUsage{}, fmt.Errorf("parse chat completion: %w", err)
	}
	if len(chat.Choices) == 0 {
		return "", model.TokenUsage{}, fmt.Errorf("openai returned no choices")
	}
	content := strings.TrimSpace(chat.Choices[0].Message.Content)
	if content == "" {
		return "", model.TokenUsage{}, fmt.Errorf("openai returned empty content")
	}
	usage := model.TokenUsage{
		PromptTokens:     chat.Usage.PromptTokens,
		CompletionTokens: chat.Usage.CompletionTokens,
		TotalTokens:      chat.Usage.TotalTokens,
	}
	return content, usage, nil
}

func buildCurationPrompt(cfg config.Config, items []candidate) (string, error) {
	data, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`
You are curating a daily newsletter for technology, programming, and economics.

Target keywords: %s
Target domains: %s

For EACH item, return JSON in field "items" with:
- candidate_id (int)
- summary_en (max 240 chars)
- why_it_matters_en (1 sentence, max 180 chars)
- relevance_score (0-100)
- novelty_score (0-100)
- credibility_score (0-100)
- target_match (bool)
- target_reason (short text)

Do not invent facts. Return ONLY valid JSON.

Items:
%s
`, strings.Join(cfg.TargetKeywords, ", "), strings.Join(cfg.TargetDomains, ", "), string(data)), nil
}

func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
