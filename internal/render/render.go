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

func BuildHTML(p Payload) (string, error) {
	const tpl = `<!doctype html>
<html lang="pt-BR">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="font-family: -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif; color:#111; line-height:1.5;">
  <h2>{{.Subject}}</h2>
  <p><strong>Data:</strong> {{.Now.Format "02/01/2006 15:04"}}</p>
  <p>Top {{len .Items}} links do dia em tecnologia, programação e economia.</p>
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
    <tr><td><strong>Total</strong></td><td align="right"><strong>{{ms .Metrics.TotalMS}}</strong></td></tr>
  </table>
  <hr/>
  {{range $idx, $it := .Items}}
    <h3 style="margin-bottom:6px;">{{$idx | add1}}. {{$it.TitlePTBR}}</h3>
    <p style="margin:4px 0;"><em>{{$it.SummaryPTBR}}</em></p>
    <p style="margin:4px 0;">Por que importa: {{$it.WhyItMattersPTBR}}</p>
    <p style="margin:4px 0;"><a href="{{$it.URL}}" target="_blank" rel="noopener noreferrer">Ler fonte</a> • <small>{{$it.Domain}}</small></p>
    <p style="margin:2px 0;"><small>Título original: {{$it.TitleEN}}</small></p>
    <br/>
  {{end}}
</body>
</html>`

	funcMap := template.FuncMap{
		"add1": func(i int) int { return i + 1 },
		"ms":   func(v int64) string { return fmt.Sprintf("%.2fs", float64(v)/1000.0) },
	}
	t, err := template.New("newsletter").Funcs(funcMap).Parse(tpl)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, p); err != nil {
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
	b.WriteString(fmt.Sprintf("- Total: %.2fs\n\n", float64(p.Metrics.TotalMS)/1000.0))

	for i, it := range p.Items {
		b.WriteString(fmt.Sprintf("%d) %s\n", i+1, it.TitlePTBR))
		b.WriteString(fmt.Sprintf("Resumo: %s\n", it.SummaryPTBR))
		b.WriteString(fmt.Sprintf("Por que importa: %s\n", it.WhyItMattersPTBR))
		b.WriteString(fmt.Sprintf("Título original: %s\n", it.TitleEN))
		b.WriteString(fmt.Sprintf("Link: %s\n", it.URL))
		b.WriteString("\n")
	}
	return b.String()
}
