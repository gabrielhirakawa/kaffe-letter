package curation

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"kaffe-letter/internal/config"
	"kaffe-letter/internal/llm"
	"kaffe-letter/internal/model"
)

type Service struct {
	cfg    config.Config
	client llm.Provider
}

func NewService(cfg config.Config) (Service, error) {
	client, err := llm.New(llm.Config{
		Provider:    cfg.LLMProvider,
		Model:       cfg.LLMModel,
		BaseURL:     cfg.LLMBaseURL,
		APIKey:      cfg.LLMAPIKey,
		HTTPTimeout: cfg.HTTPTimeout,
	})
	if err != nil {
		return Service{}, err
	}
	return Service{cfg: cfg, client: client}, nil
}

type candidate struct {
	ID        int    `json:"id"`
	Category  string `json:"category"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	Domain    string `json:"domain"`
	Summary   string `json:"summary"`
	Published string `json:"published"`
}

type aiResponse struct {
	Items []struct {
		CandidateID      int     `json:"candidate_id"`
		Category         string  `json:"category"`
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
			Category:  item.Category,
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
		content, usage, err := s.callStructuredJSON(ctx,
			"Você é um editor técnico rigoroso. Responda APENAS JSON válido.",
			payloadText)
		if err != nil {
			return nil, totalUsage, err
		}
		totalUsage.Add(usage)

		var res aiResponse
		if err := json.Unmarshal([]byte(content), &res); err != nil {
			return nil, totalUsage, fmt.Errorf("parse curation json: %w content=%s", err, content)
		}

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

			category := normalizeCategory(a.Category)
			if strings.TrimSpace(base.Category) != "" {
				category = normalizeCategory(base.Category)
			}
			collected = append(collected, model.CuratedItem{
				Title:            titleEN,
				TitleEN:          titleEN,
				Category:         category,
				URL:              base.URL,
				Domain:           base.Domain,
				ImageURL:         base.ImageURL,
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

		content, usage, err := s.callStructuredJSON(
			ctx,
			"Você traduz conteúdo técnico para PT-BR com fidelidade. Responda APENAS JSON válido.",
			prompt,
		)
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

func (s Service) GenerateIssueTitle(ctx context.Context, items []model.CuratedItem) (string, error) {
	if len(items) == 0 {
		return "", fmt.Errorf("no items to title")
	}
	top := make([]model.CuratedItem, len(items))
	copy(top, items)
	sort.SliceStable(top, func(i, j int) bool {
		return top[i].FinalScore > top[j].FinalScore
	})
	if len(top) > 6 {
		top = top[:6]
	}

	type titleCandidate struct {
		Title    string `json:"title"`
		Category string `json:"category"`
		Domain   string `json:"domain"`
	}
	payload := make([]titleCandidate, 0, len(top))
	for _, it := range top {
		payload = append(payload, titleCandidate{
			Title:    strings.TrimSpace(it.TitlePTBR),
			Category: strings.TrimSpace(it.Category),
			Domain:   strings.TrimSpace(it.Domain),
		})
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	prompt := fmt.Sprintf(`
Crie um título curto, chamativo e profissional para esta edição de newsletter.
Regras:
- responda APENAS JSON válido no formato {"title":"..."}
- use PT-BR
- máximo de 6 palavras
- não cite nomes de categorias
- não use emoji
- não termine com ponto final
- o título precisa resumir o conjunto da edição, não um item isolado

Itens de referência:
%s
`, string(b))

	content, _, err := s.callStructuredJSON(ctx,
		"Você cria títulos curtos e chamativos para newsletters técnicas. Responda apenas JSON válido.",
		prompt,
	)
	if err != nil {
		return "", err
	}
	var out struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		return "", fmt.Errorf("parse issue title json: %w content=%s", err, content)
	}
	title := strings.TrimSpace(out.Title)
	if title == "" {
		return "", fmt.Errorf("empty issue title")
	}
	if len(strings.Fields(title)) > 8 {
		return "", fmt.Errorf("issue title too long: %q", title)
	}
	return title, nil
}

func (s Service) callStructuredJSON(ctx context.Context, systemPrompt, userPrompt string) (string, model.TokenUsage, error) {
	if s.client == nil {
		return "", model.TokenUsage{}, fmt.Errorf("llm provider is not configured")
	}
	return s.client.Complete(ctx, systemPrompt, userPrompt)
}

func buildCurationPrompt(cfg config.Config, items []candidate) (string, error) {
	data, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`
You are curating a daily newsletter with four editorial sections:
- programacao
- tendencias
- leituras_essenciais
- economia

Target keywords: %s
Target domains: %s

For EACH item, return JSON in field "items" with:
- candidate_id (int)
- category (should usually preserve the provided category unless the item is clearly miscategorized)
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

func normalizeCategory(s string) string {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case "programacao":
		return "programacao"
	case "tendencias":
		return "tendencias"
	case "leituras_essenciais":
		return "leituras_essenciais"
	case "economia":
		return "economia"
	default:
		return "tendencias"
	}
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
