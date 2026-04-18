package webadmin

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rss-ai-newsletter/internal/config"
	"rss-ai-newsletter/internal/model"
	"rss-ai-newsletter/internal/pipeline"
	"rss-ai-newsletter/internal/secure"
	"rss-ai-newsletter/internal/store"
)

type Server struct {
	cfg config.Config
}

func Run(ctx context.Context, cfg config.Config) error {
	srv := &Server{cfg: cfg}
	mux := http.NewServeMux()
	assetsFS, err := fs.Sub(staticFS, "static")
	if err != nil {
		return err
	}
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assetsFS))))
	mux.HandleFunc("/", srv.handleDashboard)
	mux.HandleFunc("/admin", srv.handleDashboard)
	mux.HandleFunc("/admin/save/general", srv.handleSaveGeneral)
	mux.HandleFunc("/admin/save/ai", srv.handleSaveAI)
	mux.HandleFunc("/admin/save/delivery", srv.handleSaveDelivery)
	mux.HandleFunc("/admin/categories/create", srv.handleCreateCategory)
	mux.HandleFunc("/admin/categories/update", srv.handleUpdateCategory)
	mux.HandleFunc("/admin/categories/delete", srv.handleDeleteCategory)
	mux.HandleFunc("/admin/feeds/create", srv.handleCreateFeed)
	mux.HandleFunc("/admin/feeds/update", srv.handleUpdateFeed)
	mux.HandleFunc("/admin/feeds/delete", srv.handleDeleteFeed)
	mux.HandleFunc("/admin/status/current", srv.handleCurrentStatus)
	mux.HandleFunc("/admin/actions/run-now", srv.handleRunNow)
	mux.HandleFunc("/admin/actions/resend-latest", srv.handleResendLatest)

	server := &http.Server{
		Addr:    cfg.ServerAddr,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Printf("admin listening on %s", cfg.ServerAddr)
	err = server.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg, runs, currentRun, err := s.loadDashboardData(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := dashboardView{
		Config:         cfg,
		Categories:     cfg.Categories,
		Feeds:          cfg.Feeds,
		Runs:           runs,
		CurrentRun:     currentRun,
		ActiveTab:      normalizeTab(r.URL.Query().Get("tab")),
		SMTPPassSet:    strings.TrimSpace(cfg.SMTPPass) != "",
		OpenAIKeySet:   strings.TrimSpace(cfg.OpenAIAPIKey) != "",
		TelegramKeySet: strings.TrimSpace(cfg.TelegramBotToken) != "",
	}
	if err := pageTemplate.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleCurrentStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	st, err := store.Open(s.cfg.DatabasePath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer st.Close()

	currentRun, err := st.GetCurrentRun(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := statusTemplate.Execute(w, statusView{
		CurrentRun: formatRunSummary(currentRun),
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleSaveGeneral(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	values := map[string]string{
		"timezone":             r.FormValue("timezone"),
		"email_subject":        r.FormValue("email_subject"),
		"http_timeout_seconds": r.FormValue("http_timeout_seconds"),
	}
	s.saveSettings(w, r, values, "Configuracoes gerais salvas.")
}

func (s *Server) handleSaveAI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	values := map[string]string{
		"openai_api_key":      r.FormValue("openai_api_key"),
		"openai_model":        r.FormValue("openai_model"),
		"curation_chunk_size": r.FormValue("curation_chunk_size"),
		"weight_relevance":    r.FormValue("weight_relevance"),
		"weight_novelty":      r.FormValue("weight_novelty"),
		"weight_credibility":  r.FormValue("weight_credibility"),
		"weight_target":       r.FormValue("weight_target"),
	}
	s.saveSettings(w, r, values, "Configuracoes de IA salvas.")
}

func (s *Server) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	st, err := store.Open(s.cfg.DatabasePath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer st.Close()

	item := model.CategoryConfig{
		Slug:        normalizeSlug(r.FormValue("slug")),
		Name:        strings.TrimSpace(r.FormValue("name")),
		Description: strings.TrimSpace(r.FormValue("description")),
		ItemQuota:   parseIntOrDefault(r.FormValue("item_quota"), 2),
		SortOrder:   parseIntOrDefault(r.FormValue("sort_order"), 0),
		IsActive:    r.FormValue("is_active") == "on",
	}
	if item.Slug == "" || item.Name == "" {
		http.Error(w, "slug e name sao obrigatorios", http.StatusBadRequest)
		return
	}
	if err := st.CreateCategory(r.Context(), item); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeFlash(w, "Categoria criada.")
}

func (s *Server) handleCreateFeed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	st, err := store.Open(s.cfg.DatabasePath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer st.Close()

	item := model.FeedConfig{
		CategorySlug: normalizeSlug(r.FormValue("category_slug")),
		Name:         strings.TrimSpace(r.FormValue("name")),
		URL:          strings.TrimSpace(r.FormValue("url")),
		SiteDomain:   strings.TrimSpace(r.FormValue("site_domain")),
		Priority:     parseIntOrDefault(r.FormValue("priority"), 50),
		IsActive:     r.FormValue("is_active") == "on",
	}
	if item.CategorySlug == "" || item.Name == "" || item.URL == "" {
		http.Error(w, "category_slug, name e url sao obrigatorios", http.StatusBadRequest)
		return
	}
	if err := st.CreateFeed(r.Context(), item); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeFlash(w, "Feed criado.")
}

func (s *Server) handleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	st, err := store.Open(s.cfg.DatabasePath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer st.Close()

	item := model.CategoryConfig{
		Slug:        normalizeSlug(r.FormValue("slug")),
		Name:        strings.TrimSpace(r.FormValue("name")),
		Description: strings.TrimSpace(r.FormValue("description")),
		ItemQuota:   parseIntOrDefault(r.FormValue("item_quota"), 2),
		SortOrder:   parseIntOrDefault(r.FormValue("sort_order"), 0),
		IsActive:    r.FormValue("is_active") == "on",
	}
	if item.Slug == "" || item.Name == "" {
		http.Error(w, "slug e name sao obrigatorios", http.StatusBadRequest)
		return
	}
	if err := st.UpdateCategory(r.Context(), item); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin?tab=categorias", http.StatusSeeOther)
}

func (s *Server) handleUpdateFeed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	st, err := store.Open(s.cfg.DatabasePath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer st.Close()

	item := model.FeedConfig{
		ID:           int64(parseIntOrDefault(r.FormValue("id"), 0)),
		CategorySlug: normalizeSlug(r.FormValue("category_slug")),
		Name:         strings.TrimSpace(r.FormValue("name")),
		URL:          strings.TrimSpace(r.FormValue("url")),
		SiteDomain:   strings.TrimSpace(r.FormValue("site_domain")),
		Priority:     parseIntOrDefault(r.FormValue("priority"), 50),
		IsActive:     r.FormValue("is_active") == "on",
	}
	if item.ID <= 0 || item.CategorySlug == "" || item.Name == "" || item.URL == "" {
		http.Error(w, "id, category_slug, name e url sao obrigatorios", http.StatusBadRequest)
		return
	}
	if err := st.UpdateFeed(r.Context(), item); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin?tab=feeds", http.StatusSeeOther)
}

func (s *Server) handleDeleteCategory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	st, err := store.Open(s.cfg.DatabasePath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer st.Close()

	slug := normalizeSlug(r.FormValue("slug"))
	if slug == "" {
		http.Error(w, "slug obrigatorio", http.StatusBadRequest)
		return
	}
	if err := st.DeleteCategoryBySlug(r.Context(), slug); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/admin?tab=categorias", http.StatusSeeOther)
}

func (s *Server) handleDeleteFeed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	st, err := store.Open(s.cfg.DatabasePath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer st.Close()

	id := int64(parseIntOrDefault(r.FormValue("id"), 0))
	if id <= 0 {
		http.Error(w, "id obrigatorio", http.StatusBadRequest)
		return
	}
	if err := st.DeleteFeedByID(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/admin?tab=feeds", http.StatusSeeOther)
}

func (s *Server) handleSaveDelivery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	values := map[string]string{
		"smtp_host":                    r.FormValue("smtp_host"),
		"smtp_port":                    r.FormValue("smtp_port"),
		"smtp_user":                    r.FormValue("smtp_user"),
		"smtp_pass":                    r.FormValue("smtp_pass"),
		"email_from":                   r.FormValue("email_from"),
		"email_to":                     normalizeMultiline(r.FormValue("email_to")),
		"telegram_enabled":             strconv.FormatBool(r.FormValue("telegram_enabled") == "on"),
		"telegram_bot_token":           r.FormValue("telegram_bot_token"),
		"telegram_chat_ids":            normalizeMultiline(r.FormValue("telegram_chat_ids")),
		"telegram_disable_web_preview": strconv.FormatBool(r.FormValue("telegram_disable_web_preview") == "on"),
	}
	s.saveSettings(w, r, values, "Configuracoes de entrega salvas.")
}

func (s *Server) handleRunNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	go func() {
		cfg, err := config.Load()
		if err != nil {
			log.Printf("admin run-now load config failed: %v", err)
			return
		}
		if err := cfg.ValidateRuntime(); err != nil {
			log.Printf("admin run-now validation failed: %v", err)
			return
		}
		if err := pipeline.RunDaily(context.Background(), cfg); err != nil {
			log.Printf("admin run-now failed: %v", err)
		}
	}()
	writeFlash(w, "Execucao iniciada em background.")
}

func (s *Server) handleResendLatest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	go func() {
		cfg, err := config.Load()
		if err != nil {
			log.Printf("admin resend-latest load config failed: %v", err)
			return
		}
		if err := cfg.ValidateRuntime(); err != nil {
			log.Printf("admin resend-latest validation failed: %v", err)
			return
		}
		if err := pipeline.Resend(context.Background(), cfg, 0, true); err != nil {
			log.Printf("admin resend-latest failed: %v", err)
		}
	}()
	writeFlash(w, "Reenvio do ultimo sucesso iniciado em background.")
}

func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request, values map[string]string, okMessage string) {
	st, err := store.Open(s.cfg.DatabasePath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer st.Close()

	existing, err := st.GetSettings(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cryptoSvc, err := secure.New(config.MasterKeyPath(s.cfg.DatabasePath))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := encryptSensitiveValues(cryptoSvc, values, existing); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := st.UpsertSettings(r.Context(), values); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeFlash(w, okMessage)
}

func (s *Server) loadDashboardData(ctx context.Context) (config.Config, []runView, runView, error) {
	cfg, err := config.Load()
	if err != nil {
		return config.Config{}, nil, runView{}, err
	}
	st, err := store.Open(cfg.DatabasePath)
	if err != nil {
		return config.Config{}, nil, runView{}, err
	}
	defer st.Close()
	runs, err := st.ListRecentRuns(ctx, 12)
	if err != nil {
		return config.Config{}, nil, runView{}, err
	}
	currentRun, err := st.GetCurrentRun(ctx)
	if err != nil {
		return config.Config{}, nil, runView{}, err
	}

	out := make([]runView, 0, len(runs))
	for _, item := range runs {
		out = append(out, formatRunSummary(item))
	}
	return cfg, out, formatRunSummary(currentRun), nil
}

func normalizeMultiline(s string) string {
	lines := strings.FieldsFunc(s, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ','
	})
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func writeFlash(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<div style="padding:10px 12px;border:1px solid #bfd7bf;background:#eef9ee;border-radius:10px;">%s</div>`, template.HTMLEscapeString(message))
}

func encryptSensitiveValues(cryptoSvc secure.Service, values, existing map[string]string) error {
	for _, key := range []string{"openai_api_key", "smtp_pass", "telegram_bot_token"} {
		raw := strings.TrimSpace(values[key])
		if raw == "" {
			if current, ok := existing[key]; ok {
				values[key] = current
			}
			continue
		}
		encrypted, err := cryptoSvc.Encrypt(raw)
		if err != nil {
			return err
		}
		values[key] = encrypted
	}
	return nil
}

type dashboardView struct {
	Config         config.Config
	Categories     []model.CategoryConfig
	Feeds          []model.FeedConfig
	Runs           []runView
	CurrentRun     runView
	ActiveTab      string
	SMTPPassSet    bool
	OpenAIKeySet   bool
	TelegramKeySet bool
}

type runView struct {
	ID        int64
	Status    string
	Stage     string
	Progress  string
	Heartbeat string
	CreatedAt string
	Error     string
}

type statusView struct {
	CurrentRun runView
}

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

var pageTemplate = template.Must(template.New("admin.html").Funcs(template.FuncMap{
	"joinLines": func(items []string) string { return strings.Join(items, "\n") },
	"seconds":   func(v time.Duration) int { return int(v / time.Second) },
	"isActiveTab": func(current, tab string) bool {
		return normalizeTab(current) == normalizeTab(tab)
	},
	"statusLabel": statusLabel,
	"statusClass": statusClass,
	"stageLabel":  stageLabel,
	"isRunning":   isRunningStatus,
}).ParseFS(templateFS, "templates/admin.html"))

var statusTemplate = template.Must(template.New("status-card").Funcs(template.FuncMap{
	"statusLabel": statusLabel,
	"statusClass": statusClass,
	"stageLabel":  stageLabel,
	"isRunning":   isRunningStatus,
}).Parse(`
<section class="card" hx-get="/admin/status/current" hx-trigger="load, every 4s" hx-swap="outerHTML">
  <h2>Execução Atual</h2>
  {{if gt .CurrentRun.ID 0}}
  <table>
    <tbody>
      <tr><td>Run</td><td>#{{.CurrentRun.ID}}</td></tr>
      <tr><td>Status</td><td><span class="status-badge {{statusClass .CurrentRun.Status}}"><span class="spinner {{if isRunning .CurrentRun.Status}}running{{end}}"></span>{{statusLabel .CurrentRun.Status}}</span></td></tr>
      <tr><td>Etapa</td><td>{{stageLabel .CurrentRun.Stage}}</td></tr>
      <tr><td>Mensagem</td><td>{{.CurrentRun.Progress}}</td></tr>
      <tr><td>Heartbeat</td><td>{{.CurrentRun.Heartbeat}}</td></tr>
      <tr><td>Início</td><td>{{.CurrentRun.CreatedAt}}</td></tr>
      {{if .CurrentRun.Error}}<tr><td>Erro</td><td>{{.CurrentRun.Error}}</td></tr>{{end}}
    </tbody>
  </table>
  {{else}}
  <p class="muted">Nenhuma execução registrada.</p>
  {{end}}
</section>
`))

func normalizeSlug(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.ReplaceAll(s, " ", "_")
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func parseIntOrDefault(s string, def int) int {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return v
}

func normalizeTab(tab string) string {
	switch strings.TrimSpace(strings.ToLower(tab)) {
	case "dashboard", "categorias", "feeds", "ia", "entrega", "sistema":
		return strings.TrimSpace(strings.ToLower(tab))
	default:
		return "dashboard"
	}
}

func statusLabel(status string) string {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "running":
		return "Em execução"
	case "success", "sent":
		return "Sucesso"
	case "failed", "resend_failed":
		return "Falhou"
	case "resent":
		return "Reenviado"
	case "starting":
		return "Iniciando"
	default:
		return "Aguardando"
	}
}

func statusClass(status string) string {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "running":
		return "running"
	case "success", "sent", "resent":
		return "success"
	case "failed", "resend_failed":
		return "failed"
	default:
		return "idle"
	}
}

func stageLabel(stage string) string {
	switch strings.TrimSpace(strings.ToLower(stage)) {
	case "starting":
		return "Inicialização"
	case "rss":
		return "Coleta RSS"
	case "persist_raw":
		return "Persistência bruta"
	case "curation":
		return "Curadoria IA"
	case "translation":
		return "Tradução PT-BR"
	case "normalize":
		return "Normalização"
	case "persist_curated":
		return "Persistência final"
	case "delivery":
		return "Entrega"
	case "success":
		return "Concluído"
	case "failed":
		return "Falha"
	default:
		return "Aguardando"
	}
}

func isRunningStatus(status string) bool {
	return strings.EqualFold(strings.TrimSpace(status), "running")
}

func formatRunSummary(item model.RunSummary) runView {
	if item.ID == 0 {
		return runView{}
	}
	heartbeat := ""
	if !item.HeartbeatAt.IsZero() {
		heartbeat = item.HeartbeatAt.In(time.Local).Format("02/01/2006 15:04:05")
	}
	return runView{
		ID:        item.ID,
		Status:    item.Status,
		Stage:     item.CurrentStage,
		Progress:  item.ProgressMsg,
		Heartbeat: heartbeat,
		CreatedAt: item.CreatedAt.In(time.Local).Format("02/01/2006 15:04:05"),
		Error:     item.ErrorMessage,
	}
}
