package pipeline

import (
	"context"
	"fmt"
	"html"
	"log"
	"regexp"
	"strings"
	"time"

	"rss-ai-newsletter/internal/config"
	"rss-ai-newsletter/internal/curation"
	"rss-ai-newsletter/internal/email"
	"rss-ai-newsletter/internal/model"
	"rss-ai-newsletter/internal/render"
	"rss-ai-newsletter/internal/rss"
	"rss-ai-newsletter/internal/store"
)

var htmlTagRe = regexp.MustCompile(`<[^>]+>`)

func RunDaily(ctx context.Context, cfg config.Config) error {
	runStart := time.Now()
	metrics := model.RunMetrics{}
	st, err := store.Open(cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer st.Close()

	runID, err := st.StartRun(ctx)
	if err != nil {
		return err
	}
	finish := func(status, msg string) {
		if ferr := st.FinishRun(ctx, runID, status, msg); ferr != nil {
			log.Printf("finish run failed: %v", ferr)
		}
	}

	collector := rss.Collector{
		HTTPTimeout:     cfg.HTTPTimeout,
		MaxItemsPerFeed: cfg.MaxItemsPerFeed,
		MaxItemsTotal:   cfg.MaxItemsTotal,
		BlockedDomains:  toSet(cfg.BlockedDomains),
		TargetDomains:   toSet(cfg.TargetDomains),
		TargetKeywords:  cfg.TargetKeywords,
	}

	t0 := time.Now()
	raw, err := collector.Fetch(ctx, cfg.RSSFeeds)
	metrics.RSSMS = time.Since(t0).Milliseconds()
	if err != nil {
		finish("failed", err.Error())
		return err
	}
	if len(raw) == 0 {
		err = fmt.Errorf("no RSS items collected")
		finish("failed", err.Error())
		return err
	}

	if len(raw) > cfg.CandidatePoolSize {
		raw = raw[:cfg.CandidatePoolSize]
	}
	tPersistStart := time.Now()
	if err := st.SaveRawItems(ctx, runID, raw); err != nil {
		finish("failed", err.Error())
		return err
	}
	metrics.PersistMS += time.Since(tPersistStart).Milliseconds()

	curationSvc := curation.NewService(cfg)
	t1 := time.Now()
	curated, usageCurate, err := curationSvc.Curate(ctx, raw)
	metrics.CurationMS = time.Since(t1).Milliseconds()
	if err != nil {
		finish("failed", "AI curation required: "+err.Error())
		return fmt.Errorf("AI curation required: %w", err)
	}

	curated = enforceDomainDiversity(cfg, curated)
	if len(curated) > cfg.CuratedItemsCount {
		curated = curated[:cfg.CuratedItemsCount]
	}
	if len(curated) == 0 {
		err = fmt.Errorf("no curated items available")
		finish("failed", err.Error())
		return err
	}

	t2 := time.Now()
	curated, usageTranslate, err := curationSvc.TranslateForPTBR(ctx, curated)
	metrics.TranslationMS = time.Since(t2).Milliseconds()
	if err != nil {
		finish("failed", "AI translation required: "+err.Error())
		return fmt.Errorf("AI translation required: %w", err)
	}
	usage := usageCurate
	usage.Add(usageTranslate)

	t3 := time.Now()
	curated = normalizeForEmail(curated)
	if err := validateBilingual(curated); err != nil {
		finish("failed", err.Error())
		return err
	}
	metrics.NormalizeMS = time.Since(t3).Milliseconds()
	tPersistStart = time.Now()
	if err := st.SaveCuratedItems(ctx, runID, curated); err != nil {
		finish("failed", err.Error())
		return err
	}
	metrics.PersistMS += time.Since(tPersistStart).Milliseconds()

	loc, lerr := time.LoadLocation(cfg.Timezone)
	if lerr != nil {
		loc = time.UTC
	}
	subject := fmt.Sprintf("%s - %s", cfg.EmailSubject, time.Now().In(loc).Format("02/01/2006"))
	renderMS, sendMS, err := sendNewsletter(cfg, subject, time.Now().In(loc), curated, usage, metrics)
	metrics.RenderMS = renderMS
	metrics.SendMS = sendMS
	metrics.TotalMS = time.Since(runStart).Milliseconds()
	if err != nil {
		_ = st.SaveDelivery(ctx, runID, "failed", err.Error(), len(cfg.EmailTo))
		finish("failed", err.Error())
		return err
	}
	if err := st.SaveRunMetrics(ctx, runID, metrics); err != nil {
		finish("failed", err.Error())
		return err
	}
	_ = st.SaveDelivery(ctx, runID, "sent", "", len(cfg.EmailTo))
	finish("success", "")
	log.Printf("newsletter sent with %d items", len(curated))
	return nil
}

func Resend(ctx context.Context, cfg config.Config, runID int64, latest bool) error {
	st, err := store.Open(cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer st.Close()

	if latest {
		runID, err = st.GetLatestSuccessfulRunID(ctx)
		if err != nil {
			return err
		}
	}
	if runID <= 0 {
		return fmt.Errorf("invalid run_id for resend")
	}

	items, err := st.GetCuratedItemsByRunID(ctx, runID)
	if err != nil {
		return err
	}
	items = normalizeForEmail(items)
	if err := validateBilingual(items); err != nil {
		return fmt.Errorf("resend blocked: %w", err)
	}
	metrics, err := st.GetRunMetrics(ctx, runID)
	if err != nil {
		return err
	}

	loc, lerr := time.LoadLocation(cfg.Timezone)
	if lerr != nil {
		loc = time.UTC
	}
	subject := fmt.Sprintf("REENVIO [%d] %s - %s", runID, cfg.EmailSubject, time.Now().In(loc).Format("02/01/2006"))
	if _, _, err := sendNewsletter(cfg, subject, time.Now().In(loc), items, model.TokenUsage{}, metrics); err != nil {
		_ = st.SaveDelivery(ctx, runID, "resend_failed", err.Error(), len(cfg.EmailTo))
		return err
	}
	_ = st.SaveDelivery(ctx, runID, "resent", "", len(cfg.EmailTo))
	log.Printf("newsletter resent from run_id=%d with %d items", runID, len(items))
	return nil
}

func sendNewsletter(cfg config.Config, subject string, now time.Time, items []model.CuratedItem, usage model.TokenUsage, metrics model.RunMetrics) (int64, int64, error) {
	payload := render.Payload{Subject: subject, Now: now, Items: items, Usage: usage, Model: cfg.OpenAIModel, Metrics: metrics}
	tRender := time.Now()
	htmlBody, err := render.BuildHTML(payload)
	if err != nil {
		return 0, 0, err
	}
	textBody := render.BuildText(payload)
	renderMS := time.Since(tRender).Milliseconds()
	sender := email.Sender{Host: cfg.SMTPHost, Port: cfg.SMTPPort, User: cfg.SMTPUser, Pass: cfg.SMTPPass, From: cfg.EmailFrom}
	tSend := time.Now()
	if err := sender.Send(cfg.EmailTo, subject, textBody, htmlBody); err != nil {
		return renderMS, time.Since(tSend).Milliseconds(), err
	}
	return renderMS, time.Since(tSend).Milliseconds(), nil
}

func normalizeForEmail(items []model.CuratedItem) []model.CuratedItem {
	out := make([]model.CuratedItem, 0, len(items))
	for _, it := range items {
		it.Title = cleanText(it.Title)
		it.TitleEN = cleanText(it.TitleEN)
		it.TitlePTBR = cleanText(it.TitlePTBR)
		it.SummaryEN = cleanText(it.SummaryEN)
		it.SummaryPTBR = cleanText(it.SummaryPTBR)
		it.WhyItMattersEN = cleanText(it.WhyItMattersEN)
		it.WhyItMattersPTBR = cleanText(it.WhyItMattersPTBR)
		if it.Title == "" {
			it.Title = it.TitlePTBR
		}
		out = append(out, it)
	}
	return out
}

func cleanText(s string) string {
	s = html.UnescapeString(s)
	s = htmlTagRe.ReplaceAllString(s, " ")
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}

func validateBilingual(items []model.CuratedItem) error {
	for i, it := range items {
		pos := i + 1
		if strings.TrimSpace(it.TitleEN) == "" {
			return fmt.Errorf("item %d missing title_en", pos)
		}
		if strings.TrimSpace(it.TitlePTBR) == "" {
			return fmt.Errorf("item %d missing title_pt_br", pos)
		}
		if strings.TrimSpace(it.SummaryEN) == "" {
			return fmt.Errorf("item %d missing summary_en", pos)
		}
		if strings.TrimSpace(it.SummaryPTBR) == "" {
			return fmt.Errorf("item %d missing summary_pt_br", pos)
		}
		if strings.TrimSpace(it.WhyItMattersEN) == "" {
			return fmt.Errorf("item %d missing why_it_matters_en", pos)
		}
		if strings.TrimSpace(it.WhyItMattersPTBR) == "" {
			return fmt.Errorf("item %d missing why_it_matters_pt_br", pos)
		}
	}
	return nil
}

func enforceDomainDiversity(cfg config.Config, items []model.CuratedItem) []model.CuratedItem {
	if cfg.MaxPerDomain <= 0 {
		return items
	}
	count := map[string]int{}
	out := make([]model.CuratedItem, 0, len(items))
	for _, it := range items {
		if count[it.Domain] >= cfg.MaxPerDomain {
			continue
		}
		count[it.Domain]++
		out = append(out, it)
	}
	return out
}

func toSet(items []string) map[string]struct{} {
	m := make(map[string]struct{}, len(items))
	for _, i := range items {
		k := strings.TrimSpace(strings.ToLower(i))
		if k != "" {
			m[k] = struct{}{}
		}
	}
	return m
}
