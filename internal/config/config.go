package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"kaffe-letter/internal/model"
	"kaffe-letter/internal/secure"
	"kaffe-letter/internal/store"
)

type Config struct {
	LLMProvider string
	LLMAPIKey   string
	LLMModel    string
	LLMBaseURL  string

	Categories        []model.CategoryConfig
	Feeds             []model.FeedConfig
	RSSFeeds          []string
	MaxItemsPerFeed   int
	MaxItemsTotal     int
	CandidatePoolSize int
	CuratedItemsCount int
	CurationChunkSize int

	TargetDomains  []string
	TargetKeywords []string
	BlockedDomains []string

	WeightRelevance   float64
	WeightNovelty     float64
	WeightCredibility float64
	WeightTarget      float64
	MaxPerDomain      int

	Timezone string

	SMTPURL                string
	SMTPHost               string
	SMTPPort               int
	SMTPUser               string
	SMTPPass               string
	EmailFrom              string
	EmailTo                []string
	EmailSubject           string
	TelegramEnabled        bool
	TelegramBotToken       string
	TelegramChatIDs        []string
	TelegramDisablePreview bool

	DatabasePath string
	LogLevel     string
	RequireAI    bool

	HTTPTimeout time.Duration

	ServerAddr string
}

func Load() (Config, error) {
	_ = godotenv.Load()

	cfg := Config{
		LLMProvider: "openai",
		LLMModel:    "gpt-5-nano",
		LLMBaseURL:  "https://api.openai.com/v1",

		Categories:        defaultCategories(),
		Feeds:             defaultFeedConfigs(),
		RSSFeeds:          defaultFeeds(),
		MaxItemsPerFeed:   25,
		MaxItemsTotal:     220,
		CandidatePoolSize: 80,
		CuratedItemsCount: 8,
		CurationChunkSize: 15,

		TargetDomains:  []string{"github.blog", "stackoverflow.blog", "news.ycombinator.com", "techcrunch.com", "wired.com", "theverge.com", "queue.acm.org", "spectrum.ieee.org", "martinfowler.com", "ft.com", "economist.com", "asia.nikkei.com"},
		TargetKeywords: []string{"programming", "software engineering", "developer tools", "cloud", "ai", "startups", "big tech", "architecture", "economy", "markets", "semiconductors", "macroeconomics"},
		BlockedDomains: nil,

		WeightRelevance:   0.45,
		WeightNovelty:     0.25,
		WeightCredibility: 0.20,
		WeightTarget:      0.10,
		MaxPerDomain:      2,

		Timezone: "America/Sao_Paulo",

		SMTPHost:               "smtp.gmail.com",
		SMTPPort:               587,
		EmailSubject:           "Newsletter diária de programação, tendências, leituras essenciais e economia",
		TelegramEnabled:        false,
		TelegramDisablePreview: true,

		DatabasePath: getEnv("DATABASE_PATH", "./data/newsletter.db"),
		LogLevel:     getEnv("LOG_LEVEL", "info"),
		RequireAI:    false,
		HTTPTimeout:  20 * time.Second,
		ServerAddr:   getEnv("SERVER_ADDR", ":8080"),
	}

	if err := overlayPersistedSettings(context.Background(), &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (cfg Config) ValidateRuntime() error {
	switch strings.ToLower(strings.TrimSpace(cfg.LLMProvider)) {
	case "", "openai", "anthropic", "gemini":
		if strings.TrimSpace(cfg.LLMAPIKey) == "" {
			return fmt.Errorf("llm_api_key is required for provider=%s", defaultIfEmpty(cfg.LLMProvider, "openai"))
		}
	case "local":
		// local providers may run without a key if the endpoint is open
	default:
		return fmt.Errorf("unsupported llm_provider: %s", cfg.LLMProvider)
	}
	if strings.TrimSpace(cfg.LLMModel) == "" {
		return fmt.Errorf("llm_model is required")
	}
	if cfg.EmailFrom == "" {
		return fmt.Errorf("email_from is required")
	}
	if len(cfg.EmailTo) == 0 {
		return fmt.Errorf("email_to is required")
	}
	if cfg.SMTPHost == "" || cfg.SMTPPort <= 0 || cfg.SMTPUser == "" || cfg.SMTPPass == "" {
		return fmt.Errorf("smtp config is incomplete")
	}
	if cfg.TelegramEnabled {
		if strings.TrimSpace(cfg.TelegramBotToken) == "" {
			return fmt.Errorf("telegram_bot_token is required when telegram_enabled=true")
		}
		if len(cfg.TelegramChatIDs) == 0 {
			return fmt.Errorf("telegram_chat_ids is required when telegram_enabled=true")
		}
	}
	return nil
}

func defaultFeeds() []string {
	feeds := defaultFeedConfigs()
	out := make([]string, 0, len(feeds))
	for _, item := range feeds {
		out = append(out, item.URL)
	}
	return out
}

func defaultCategories() []model.CategoryConfig {
	return []model.CategoryConfig{
		{Slug: "programacao", Name: "Programação", Description: "Software engineering, linguagens, frameworks e tooling.", ItemQuota: 2, SortOrder: 1, IsActive: true},
		{Slug: "tendencias", Name: "Tendências", Description: "Movimentos do setor, IA, produtos e Big Tech.", ItemQuota: 2, SortOrder: 2, IsActive: true},
		{Slug: "leituras_essenciais", Name: "Leituras Essenciais", Description: "Arquitetura, sistemas e análises técnicas mais densas.", ItemQuota: 2, SortOrder: 3, IsActive: true},
		{Slug: "economia", Name: "Economia", Description: "Mercado, macro e supply chain com impacto em tecnologia.", ItemQuota: 2, SortOrder: 4, IsActive: true},
	}
}

func defaultFeedConfigs() []model.FeedConfig {
	return []model.FeedConfig{
		{CategorySlug: "programacao", Name: "Hacker News Frontpage", URL: "https://hnrss.org/frontpage", SiteDomain: "news.ycombinator.com", Priority: 90, IsActive: true},
		{CategorySlug: "programacao", Name: "GitHub Blog", URL: "https://github.blog/feed/", SiteDomain: "github.blog", Priority: 100, IsActive: true},
		{CategorySlug: "programacao", Name: "Stack Overflow Blog", URL: "https://stackoverflow.blog/feed/", SiteDomain: "stackoverflow.blog", Priority: 100, IsActive: true},
		{CategorySlug: "tendencias", Name: "TechCrunch", URL: "https://techcrunch.com/feed/", SiteDomain: "techcrunch.com", Priority: 100, IsActive: true},
		{CategorySlug: "tendencias", Name: "Wired", URL: "https://www.wired.com/feed/rss", SiteDomain: "wired.com", Priority: 90, IsActive: true},
		{CategorySlug: "tendencias", Name: "The Verge", URL: "https://www.theverge.com/rss/tech/index.xml", SiteDomain: "theverge.com", Priority: 90, IsActive: true},
		{CategorySlug: "leituras_essenciais", Name: "ACM Queue", URL: "https://queue.acm.org/rss/feeds/queuecontent.xml", SiteDomain: "queue.acm.org", Priority: 90, IsActive: true},
		{CategorySlug: "leituras_essenciais", Name: "IEEE Spectrum AI", URL: "https://spectrum.ieee.org/feeds/topic/artificial-intelligence.rss", SiteDomain: "spectrum.ieee.org", Priority: 85, IsActive: true},
		{CategorySlug: "leituras_essenciais", Name: "IEEE Spectrum Computing", URL: "https://spectrum.ieee.org/feeds/topic/computing.rss", SiteDomain: "spectrum.ieee.org", Priority: 85, IsActive: true},
		{CategorySlug: "leituras_essenciais", Name: "Martin Fowler", URL: "https://martinfowler.com/feed.atom", SiteDomain: "martinfowler.com", Priority: 90, IsActive: true},
		{CategorySlug: "economia", Name: "The Guardian Business", URL: "https://www.theguardian.com/business/rss", SiteDomain: "theguardian.com", Priority: 90, IsActive: true},
		{CategorySlug: "economia", Name: "MarketWatch Top Stories", URL: "https://feeds.marketwatch.com/marketwatch/topstories/", SiteDomain: "marketwatch.com", Priority: 90, IsActive: true},
		{CategorySlug: "economia", Name: "Yahoo Finance News", URL: "https://finance.yahoo.com/news/rssindex", SiteDomain: "finance.yahoo.com", Priority: 90, IsActive: true},
	}
}

func overlayPersistedSettings(ctx context.Context, cfg *Config) error {
	st, err := store.Open(cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer st.Close()

	cryptoSvc, err := secure.New(MasterKeyPath(cfg.DatabasePath))
	if err != nil {
		return err
	}

	defaults := map[string]string{
		"llm_provider":                 cfg.LLMProvider,
		"llm_model":                    cfg.LLMModel,
		"llm_base_url":                 cfg.LLMBaseURL,
		"llm_api_key":                  "",
		"rss_feeds":                    strings.Join(cfg.RSSFeeds, "\n"),
		"max_items_per_feed":           strconv.Itoa(cfg.MaxItemsPerFeed),
		"max_items_total":              strconv.Itoa(cfg.MaxItemsTotal),
		"candidate_pool_size":          strconv.Itoa(cfg.CandidatePoolSize),
		"curated_items_count":          strconv.Itoa(cfg.CuratedItemsCount),
		"curation_chunk_size":          strconv.Itoa(cfg.CurationChunkSize),
		"target_domains":               strings.Join(cfg.TargetDomains, "\n"),
		"target_keywords":              strings.Join(cfg.TargetKeywords, "\n"),
		"blocked_domains":              strings.Join(cfg.BlockedDomains, "\n"),
		"weight_relevance":             fmt.Sprintf("%.2f", cfg.WeightRelevance),
		"weight_novelty":               fmt.Sprintf("%.2f", cfg.WeightNovelty),
		"weight_credibility":           fmt.Sprintf("%.2f", cfg.WeightCredibility),
		"weight_target":                fmt.Sprintf("%.2f", cfg.WeightTarget),
		"max_per_domain":               strconv.Itoa(cfg.MaxPerDomain),
		"timezone":                     cfg.Timezone,
		"smtp_host":                    cfg.SMTPHost,
		"smtp_port":                    strconv.Itoa(cfg.SMTPPort),
		"smtp_user":                    cfg.SMTPUser,
		"smtp_pass":                    "",
		"email_from":                   cfg.EmailFrom,
		"email_to":                     strings.Join(cfg.EmailTo, "\n"),
		"email_subject":                cfg.EmailSubject,
		"telegram_enabled":             strconv.FormatBool(cfg.TelegramEnabled),
		"telegram_bot_token":           "",
		"telegram_chat_ids":            strings.Join(cfg.TelegramChatIDs, "\n"),
		"telegram_disable_web_preview": strconv.FormatBool(cfg.TelegramDisablePreview),
		"http_timeout_seconds":         strconv.Itoa(int(cfg.HTTPTimeout / time.Second)),
	}
	if err := applyBootstrapEnvDefaults(defaults, cryptoSvc); err != nil {
		return err
	}
	if err := st.EnsureSettings(ctx, defaults); err != nil {
		return err
	}
	if err := st.EnsureCategories(ctx, cfg.Categories); err != nil {
		return err
	}
	if err := st.EnsureFeeds(ctx, cfg.Feeds); err != nil {
		return err
	}
	categories, err := st.ListCategories(ctx, true)
	if err != nil {
		return err
	}
	feeds, err := st.ListFeeds(ctx, true)
	if err != nil {
		return err
	}
	cfg.Categories = categories
	cfg.Feeds = feeds
	cfg.RSSFeeds = make([]string, 0, len(feeds))
	for _, item := range feeds {
		cfg.RSSFeeds = append(cfg.RSSFeeds, item.URL)
	}

	values, err := st.GetSettings(ctx)
	if err != nil {
		return err
	}
	if shouldRefreshFeeds(values["rss_feeds"]) {
		updated := map[string]string{
			"rss_feeds":       strings.Join(cfg.RSSFeeds, "\n"),
			"target_domains":  strings.Join(cfg.TargetDomains, "\n"),
			"target_keywords": strings.Join(cfg.TargetKeywords, "\n"),
		}
		if err := st.UpsertSettings(ctx, updated); err != nil {
			return err
		}
		values, err = st.GetSettings(ctx)
		if err != nil {
			return err
		}
	}
	cfg.LLMAPIKey, err = cryptoSvc.Decrypt(firstNonEmpty(values, "llm_api_key", "openai_api_key"))
	if err != nil {
		return fmt.Errorf("decrypt llm_api_key: %w", err)
	}
	cfg.LLMProvider = getString(values, "llm_provider", defaultIfEmpty(cfg.LLMProvider, "openai"))
	cfg.LLMModel = getString(values, "llm_model", cfg.LLMModel)
	cfg.LLMBaseURL = getString(values, "llm_base_url", cfg.LLMBaseURL)
	if cfg.LLMModel == "" {
		cfg.LLMModel = getString(values, "openai_model", cfg.LLMModel)
	}
	if cfg.LLMAPIKey == "" {
		cfg.LLMAPIKey, err = cryptoSvc.Decrypt(getString(values, "openai_api_key", ""))
		if err != nil {
			return fmt.Errorf("decrypt openai_api_key: %w", err)
		}
	}
	if cfg.LLMBaseURL == "" {
		cfg.LLMBaseURL = getString(values, "openai_url", cfg.LLMBaseURL)
	}
	cfg.RSSFeeds = splitLines(getString(values, "rss_feeds", strings.Join(cfg.RSSFeeds, "\n")))
	cfg.MaxItemsPerFeed = parseInt(getString(values, "max_items_per_feed", ""), cfg.MaxItemsPerFeed)
	cfg.MaxItemsTotal = parseInt(getString(values, "max_items_total", ""), cfg.MaxItemsTotal)
	cfg.CandidatePoolSize = parseInt(getString(values, "candidate_pool_size", ""), cfg.CandidatePoolSize)
	cfg.CuratedItemsCount = parseInt(getString(values, "curated_items_count", ""), cfg.CuratedItemsCount)
	cfg.CurationChunkSize = parseInt(getString(values, "curation_chunk_size", ""), cfg.CurationChunkSize)
	cfg.TargetDomains = splitLines(getString(values, "target_domains", strings.Join(cfg.TargetDomains, "\n")))
	cfg.TargetKeywords = splitLines(getString(values, "target_keywords", strings.Join(cfg.TargetKeywords, "\n")))
	cfg.BlockedDomains = splitLines(getString(values, "blocked_domains", strings.Join(cfg.BlockedDomains, "\n")))
	cfg.WeightRelevance = parseFloat(getString(values, "weight_relevance", ""), cfg.WeightRelevance)
	cfg.WeightNovelty = parseFloat(getString(values, "weight_novelty", ""), cfg.WeightNovelty)
	cfg.WeightCredibility = parseFloat(getString(values, "weight_credibility", ""), cfg.WeightCredibility)
	cfg.WeightTarget = parseFloat(getString(values, "weight_target", ""), cfg.WeightTarget)
	cfg.MaxPerDomain = parseInt(getString(values, "max_per_domain", ""), cfg.MaxPerDomain)
	cfg.Timezone = getString(values, "timezone", cfg.Timezone)
	cfg.SMTPHost = getString(values, "smtp_host", cfg.SMTPHost)
	cfg.SMTPPort = parseInt(getString(values, "smtp_port", ""), cfg.SMTPPort)
	cfg.SMTPUser = getString(values, "smtp_user", cfg.SMTPUser)
	cfg.SMTPPass, err = cryptoSvc.Decrypt(getString(values, "smtp_pass", ""))
	if err != nil {
		return fmt.Errorf("decrypt smtp_pass: %w", err)
	}
	cfg.EmailFrom = getString(values, "email_from", cfg.EmailFrom)
	cfg.EmailTo = splitLines(getString(values, "email_to", strings.Join(cfg.EmailTo, "\n")))
	cfg.EmailSubject = getString(values, "email_subject", cfg.EmailSubject)
	cfg.TelegramEnabled = parseBool(getString(values, "telegram_enabled", ""), cfg.TelegramEnabled)
	cfg.TelegramBotToken, err = cryptoSvc.Decrypt(getString(values, "telegram_bot_token", ""))
	if err != nil {
		return fmt.Errorf("decrypt telegram_bot_token: %w", err)
	}
	cfg.TelegramChatIDs = splitLines(getString(values, "telegram_chat_ids", strings.Join(cfg.TelegramChatIDs, "\n")))
	cfg.TelegramDisablePreview = parseBool(getString(values, "telegram_disable_web_preview", ""), cfg.TelegramDisablePreview)
	cfg.HTTPTimeout = time.Duration(parseInt(getString(values, "http_timeout_seconds", ""), int(cfg.HTTPTimeout/time.Second))) * time.Second
	return nil
}

func applyBootstrapEnvDefaults(defaults map[string]string, cryptoSvc secure.Service) error {
	setString := func(key, envName string) {
		if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
			defaults[key] = value
		}
	}
	setStringFallback := func(key, primaryEnv, fallbackEnv string) {
		if value := strings.TrimSpace(os.Getenv(primaryEnv)); value != "" {
			defaults[key] = value
			return
		}
		if value := strings.TrimSpace(os.Getenv(fallbackEnv)); value != "" {
			defaults[key] = value
		}
	}
	setLines := func(key, envName string) {
		if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
			defaults[key] = strings.Join(splitLines(value), "\n")
		}
	}
	setSecret := func(key, envName string) error {
		value := strings.TrimSpace(os.Getenv(envName))
		if value == "" {
			return nil
		}
		encrypted, err := cryptoSvc.Encrypt(value)
		if err != nil {
			return err
		}
		defaults[key] = encrypted
		return nil
	}

	setString("llm_provider", "LLM_PROVIDER")
	setStringFallback("llm_model", "LLM_MODEL", "OPENAI_MODEL")
	setStringFallback("llm_base_url", "LLM_BASE_URL", "OPENAI_URL")
	if err := func() error {
		if value := strings.TrimSpace(os.Getenv("LLM_API_KEY")); value != "" {
			encrypted, err := cryptoSvc.Encrypt(value)
			if err != nil {
				return err
			}
			defaults["llm_api_key"] = encrypted
			return nil
		}
		if value := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); value != "" {
			encrypted, err := cryptoSvc.Encrypt(value)
			if err != nil {
				return err
			}
			defaults["llm_api_key"] = encrypted
		}
		return nil
	}(); err != nil {
		return err
	}
	setString("openai_model", "OPENAI_MODEL")
	if err := setSecret("openai_api_key", "OPENAI_API_KEY"); err != nil {
		return err
	}
	setString("smtp_host", "SMTP_HOST")
	setString("smtp_port", "SMTP_PORT")
	setString("smtp_user", "SMTP_USER")
	if err := setSecret("smtp_pass", "SMTP_PASS"); err != nil {
		return err
	}
	setString("email_from", "EMAIL_FROM")
	setLines("email_to", "EMAIL_TO")
	setString("email_subject", "EMAIL_SUBJECT")
	setString("telegram_enabled", "TELEGRAM_ENABLED")
	if err := setSecret("telegram_bot_token", "TELEGRAM_BOT_TOKEN"); err != nil {
		return err
	}
	setLines("telegram_chat_ids", "TELEGRAM_CHAT_IDS")
	setString("telegram_disable_web_preview", "TELEGRAM_DISABLE_WEB_PREVIEW")
	setString("timezone", "TIMEZONE")
	setString("http_timeout_seconds", "HTTP_TIMEOUT_SECONDS")
	setString("max_items_per_feed", "MAX_ITEMS_PER_FEED")
	setString("max_items_total", "MAX_ITEMS_TOTAL")
	setString("candidate_pool_size", "CANDIDATE_POOL_SIZE")
	setString("curated_items_count", "CURATED_ITEMS_COUNT")
	setString("curation_chunk_size", "CURATION_CHUNK_SIZE")
	setLines("target_domains", "TARGET_DOMAINS")
	setLines("target_keywords", "TARGET_KEYWORDS")
	setLines("blocked_domains", "BLOCKED_DOMAINS")
	setString("weight_relevance", "WEIGHT_RELEVANCE")
	setString("weight_novelty", "WEIGHT_NOVELTY")
	setString("weight_credibility", "WEIGHT_CREDIBILITY")
	setString("weight_target", "WEIGHT_TARGET")
	setString("max_per_domain", "MAX_PER_DOMAIN")
	return nil
}

func firstNonEmpty(values map[string]string, keys ...string) string {
	for _, key := range keys {
		if v := strings.TrimSpace(values[key]); v != "" {
			return v
		}
	}
	return ""
}

func defaultIfEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func MasterKeyPath(databasePath string) string {
	return filepath.Join(filepath.Dir(databasePath), "master.key")
}

func shouldRefreshFeeds(saved string) bool {
	saved = strings.TrimSpace(saved)
	if saved == "" {
		return true
	}
	if strings.Contains(strings.ToLower(saved), "news.google.com") {
		return true
	}
	current := strings.Join(defaultFeeds(), "\n")
	if saved == current {
		return false
	}
	legacy := strings.Join([]string{
		"https://hnrss.org/frontpage",
		"https://hnrss.org/newest?q=golang+OR+python+OR+javascript",
		"http://feeds.arstechnica.com/arstechnica/index",
		"https://feed.infoq.com/",
		"https://github.blog/feed/",
		"https://stackoverflow.blog/feed/",
		"https://rss.nytimes.com/services/xml/rss/nyt/Technology.xml",
		"https://rss.nytimes.com/services/xml/rss/nyt/Business.xml",
		"https://rss.nytimes.com/services/xml/rss/nyt/Economy.xml",
	}, "\n")
	return saved == legacy
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func splitLines(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ','
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

func getString(values map[string]string, key, def string) string {
	v := strings.TrimSpace(values[key])
	if v == "" {
		return def
	}
	return v
}

func parseInt(s string, def int) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}

func parseFloat(s string, def float64) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return def
	}
	return v
}

func parseBool(s string, def bool) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return def
	}
	switch s {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

func getEnv(k, def string) string {
	v := strings.TrimSpace(strings.TrimSpace(strings.Trim(os.Getenv(k), `"`)))
	if v == "" {
		return def
	}
	return v
}
