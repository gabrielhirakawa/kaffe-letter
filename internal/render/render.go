package render

import (
	"fmt"
	"html/template"
	"strings"
	"time"

	"rss-ai-newsletter/internal/model"
)

type Payload struct {
	Subject string
	Now     time.Time
	Items   []model.CuratedItem
	Usage   model.TokenUsage
	Model   string
	Metrics model.RunMetrics
}

type section struct {
	Title string
	Items []model.CuratedItem
}

func BuildHTML(p Payload) (string, error) {
	const tpl = `<!doctype html>
<html lang="pt-BR">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="font-family: -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif; color:#111; line-height:1.5;">
  <h2>{{.Subject}}</h2>
  <p><strong>Data:</strong> {{.Now.Format "02/01/2006 15:04"}}</p>
  <p>{{len .Items}} links selecionados em programação, tendências, leituras essenciais e economia.</p>
  <p><strong>Modelo:</strong> {{.Model}} | <strong>Tokens:</strong> prompt {{.Usage.PromptTokens}}, completion {{.Usage.CompletionTokens}}, total {{.Usage.TotalTokens}}</p>
  <table border="1" cellpadding="6" cellspacing="0" style="border-collapse:collapse; margin:8px 0 16px 0; font-size:13px;">
    <tr><th align="left">Etapa</th><th align="right">Tempo</th></tr>
    <tr><td>RSS</td><td align="right">{{ms .Metrics.RSSMS}}</td></tr>
    <tr><td>Curadoria IA</td><td align="right">{{ms .Metrics.CurationMS}}</td></tr>
    <tr><td>Tradução IA</td><td align="right">{{ms .Metrics.TranslationMS}}</td></tr>
    <tr><td>Normalização</td><td align="right">{{ms .Metrics.NormalizeMS}}</td></tr>
    <tr><td>Persistência</td><td align="right">{{ms .Metrics.PersistMS}}</td></tr>
    <tr><td>Render</td><td align="right">{{ms .Metrics.RenderMS}}</td></tr>
    <tr><td>Envio SMTP</td><td align="right">{{ms .Metrics.SendMS}}</td></tr>
    <tr><td>Envio Telegram</td><td align="right">{{ms .Metrics.TelegramMS}}</td></tr>
    <tr><td><strong>Total</strong></td><td align="right"><strong>{{ms .Metrics.TotalMS}}</strong></td></tr>
  </table>
  <hr/>
  {{range .Sections}}
    <h3 style="margin:20px 0 10px 0;">{{.Title}}</h3>
    {{range $idx, $it := .Items}}
    <h4 style="margin-bottom:6px;">{{countryFlag $it.Domain}} {{$it.TitlePTBR}}</h4>
    {{if $it.ImageURL}}
    <p style="margin:6px 0;"><img src="{{$it.ImageURL}}" alt="{{$it.TitlePTBR}}" style="max-width:560px; width:100%; height:auto; border-radius:8px;" /></p>
    {{end}}
    <p style="margin:4px 0;"><em>{{$it.SummaryPTBR}}</em></p>
    <p style="margin:4px 0;">Por que importa: {{$it.WhyItMattersPTBR}}</p>
    <p style="margin:4px 0;"><a href="{{$it.URL}}" target="_blank" rel="noopener noreferrer">Ler fonte</a> • <small>{{$it.Domain}}</small></p>
    <p style="margin:2px 0;"><small>Título original: {{$it.TitleEN}}</small></p>
    <br/>
    {{end}}
  {{end}}
</body>
</html>`

	funcMap := template.FuncMap{
		"ms":          func(v int64) string { return fmt.Sprintf("%.2fs", float64(v)/1000.0) },
		"countryFlag": countryFlag,
	}
	t, err := template.New("newsletter").Funcs(funcMap).Parse(tpl)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	view := struct {
		Payload
		Sections []section
	}{
		Payload:  p,
		Sections: buildSections(p.Items),
	}
	if err := t.Execute(&b, view); err != nil {
		return "", err
	}
	return b.String(), nil
}

func BuildText(p Payload) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s\n", p.Subject))
	b.WriteString(fmt.Sprintf("Data: %s\n\n", p.Now.Format("02/01/2006 15:04")))
	b.WriteString(fmt.Sprintf("Modelo: %s | Tokens: prompt %d, completion %d, total %d\n\n", p.Model, p.Usage.PromptTokens, p.Usage.CompletionTokens, p.Usage.TotalTokens))
	b.WriteString("Tempos por etapa:\n")
	b.WriteString(fmt.Sprintf("- RSS: %.2fs\n", float64(p.Metrics.RSSMS)/1000.0))
	b.WriteString(fmt.Sprintf("- Curadoria IA: %.2fs\n", float64(p.Metrics.CurationMS)/1000.0))
	b.WriteString(fmt.Sprintf("- Tradução IA: %.2fs\n", float64(p.Metrics.TranslationMS)/1000.0))
	b.WriteString(fmt.Sprintf("- Normalização: %.2fs\n", float64(p.Metrics.NormalizeMS)/1000.0))
	b.WriteString(fmt.Sprintf("- Persistência: %.2fs\n", float64(p.Metrics.PersistMS)/1000.0))
	b.WriteString(fmt.Sprintf("- Render: %.2fs\n", float64(p.Metrics.RenderMS)/1000.0))
	b.WriteString(fmt.Sprintf("- Envio SMTP: %.2fs\n", float64(p.Metrics.SendMS)/1000.0))
	b.WriteString(fmt.Sprintf("- Envio Telegram: %.2fs\n", float64(p.Metrics.TelegramMS)/1000.0))
	b.WriteString(fmt.Sprintf("- Total: %.2fs\n\n", float64(p.Metrics.TotalMS)/1000.0))

	for _, sec := range buildSections(p.Items) {
		b.WriteString(fmt.Sprintf("%s\n", sec.Title))
		for i, it := range sec.Items {
			b.WriteString(fmt.Sprintf("%d) %s %s\n", i+1, countryFlag(it.Domain), it.TitlePTBR))
			b.WriteString(fmt.Sprintf("Resumo: %s\n", it.SummaryPTBR))
			b.WriteString(fmt.Sprintf("Por que importa: %s\n", it.WhyItMattersPTBR))
			b.WriteString(fmt.Sprintf("Título original: %s\n", it.TitleEN))
			if strings.TrimSpace(it.ImageURL) != "" {
				b.WriteString(fmt.Sprintf("Imagem: %s\n", it.ImageURL))
			}
			b.WriteString(fmt.Sprintf("Link: %s\n", it.URL))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func buildSections(items []model.CuratedItem) []section {
	order := []struct {
		Key   string
		Title string
	}{
		{Key: "programacao", Title: "Programação"},
		{Key: "tendencias", Title: "Tendências"},
		{Key: "leituras_essenciais", Title: "Leituras Essenciais"},
		{Key: "economia", Title: "Economia"},
	}

	grouped := make(map[string][]model.CuratedItem, len(order))
	for _, it := range items {
		grouped[it.Category] = append(grouped[it.Category], it)
	}

	sections := make([]section, 0, len(order))
	for _, item := range order {
		if len(grouped[item.Key]) == 0 {
			continue
		}
		sections = append(sections, section{
			Title: item.Title,
			Items: grouped[item.Key],
		})
	}
	return sections
}

func countryFlag(domain string) string {
	domain = strings.ToLower(strings.TrimSpace(domain))
	switch {
	case strings.Contains(domain, "ft.com"), strings.Contains(domain, "economist.com"), strings.Contains(domain, "wired.co.uk"):
		return "🇬🇧"
	case strings.Contains(domain, "nikkei.com"), strings.Contains(domain, "nikkei.co.jp"), strings.Contains(domain, "nikkeiasia.com"), strings.Contains(domain, "asia.nikkei.com"):
		return "🇯🇵"
	case strings.Contains(domain, "ieee.org"):
		return "🇺🇸"
	case strings.Contains(domain, "acm.org"):
		return "🇺🇸"
	case strings.Contains(domain, "martinfowler.com"):
		return "🇺🇸"
	case strings.Contains(domain, "github.blog"), strings.Contains(domain, "stackoverflow.blog"), strings.Contains(domain, "techcrunch.com"), strings.Contains(domain, "wired.com"), strings.Contains(domain, "theverge.com"), strings.Contains(domain, "infoq.com"), strings.Contains(domain, "arstechnica.com"), strings.Contains(domain, "ycombinator.com"), strings.Contains(domain, "hnrss.org"), strings.Contains(domain, "nytimes.com"):
		return "🇺🇸"
	default:
		return "🌍"
	}
}
