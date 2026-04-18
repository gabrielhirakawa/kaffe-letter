package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"rss-ai-newsletter/internal/model"
)

type Client struct {
	BotToken          string
	DisableWebPreview bool
	HTTPClient        *http.Client
}

func NewClient(botToken string, disableWebPreview bool, timeout time.Duration) Client {
	return Client{
		BotToken:          strings.TrimSpace(botToken),
		DisableWebPreview: disableWebPreview,
		HTTPClient:        &http.Client{Timeout: timeout},
	}
}

func (c Client) SendDigest(ctx context.Context, chatIDs []string, subject string, now time.Time, items []model.CuratedItem, usage model.TokenUsage, metrics model.RunMetrics) error {
	if strings.TrimSpace(c.BotToken) == "" {
		return fmt.Errorf("telegram bot token is empty")
	}
	if len(chatIDs) == 0 {
		return fmt.Errorf("telegram chat IDs are empty")
	}

	messages := splitTelegramMessage(buildDigestMessage(subject, now, items, usage, metrics), 3800)
	for _, chatID := range chatIDs {
		chatID = strings.TrimSpace(chatID)
		if chatID == "" {
			continue
		}
		for _, msg := range messages {
			if err := c.sendMessage(ctx, chatID, msg); err != nil {
				return fmt.Errorf("telegram chat_id=%s: %w", chatID, err)
			}
		}
	}
	return nil
}

func (c Client) sendMessage(ctx context.Context, chatID, text string) error {
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", c.BotToken)
	payload := map[string]any{
		"chat_id":                  chatID,
		"text":                     text,
		"disable_web_page_preview": c.DisableWebPreview,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("status=%d body=%s", resp.StatusCode, string(body))
	}
	var out struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return err
	}
	if !out.OK {
		return fmt.Errorf("telegram response not ok: %s", string(body))
	}
	return nil
}

func buildDigestMessage(subject string, now time.Time, items []model.CuratedItem, usage model.TokenUsage, metrics model.RunMetrics) string {
	var b strings.Builder
	b.WriteString(subject)
	b.WriteString("\n")
	b.WriteString("Data: ")
	b.WriteString(now.Format("02/01/2006 15:04"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("Tokens: prompt %d, completion %d, total %d\n", usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens))
	b.WriteString(fmt.Sprintf("Tempo total: %.2fs (curadoria %.2fs, traducao %.2fs)\n\n", float64(metrics.TotalMS)/1000, float64(metrics.CurationMS)/1000, float64(metrics.TranslationMS)/1000))

	for i, it := range items {
		b.WriteString(fmt.Sprintf("%d) %s\n", i+1, strings.TrimSpace(it.TitlePTBR)))
		b.WriteString(fmt.Sprintf("%s\n", strings.TrimSpace(it.URL)))
		if strings.TrimSpace(it.SummaryPTBR) != "" {
			b.WriteString(fmt.Sprintf("Resumo: %s\n", strings.TrimSpace(it.SummaryPTBR)))
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func splitTelegramMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}
	lines := strings.Split(text, "\n")
	chunks := make([]string, 0, 4)
	var current strings.Builder
	for _, line := range lines {
		line = strings.TrimRight(line, " ")
		cand := line + "\n"
		if current.Len()+len(cand) > maxLen && current.Len() > 0 {
			chunks = append(chunks, strings.TrimSpace(current.String()))
			current.Reset()
		}
		if len(cand) > maxLen {
			for len(cand) > maxLen {
				part := cand[:maxLen]
				chunks = append(chunks, strings.TrimSpace(part))
				cand = cand[maxLen:]
			}
		}
		current.WriteString(cand)
	}
	if current.Len() > 0 {
		chunks = append(chunks, strings.TrimSpace(current.String()))
	}
	return chunks
}
