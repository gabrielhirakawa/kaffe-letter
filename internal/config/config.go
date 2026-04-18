package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	OpenAIAPIKey string
	OpenAIModel  string
	OpenAIURL    string

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

	SMTPURL      string
	SMTPHost     string
	SMTPPort     int
	SMTPUser     string
	SMTPPass     string
	EmailFrom    string
	EmailTo      []string
	EmailSubject string

	DatabasePath string
	LogLevel     string
	RequireAI    bool

	HTTPTimeout time.Duration
}

func Load() (Config, error) {
	_ = godotenv.Load()

	cfg := Config{
		OpenAIAPIKey: getEnv("OPENAI_API_KEY", ""),
		OpenAIModel:  getEnv("OPENAI_MODEL", "gpt-5-nano"),
		OpenAIURL:    getEnv("OPENAI_API_URL", "https://api.openai.com/v1/chat/completions"),

		RSSFeeds:          splitCSV(getEnv("RSS_FEEDS", strings.Join(defaultFeeds(), ","))),
		MaxItemsPerFeed:   mustInt("MAX_ITEMS_PER_FEED", 25),
		MaxItemsTotal:     mustInt("MAX_ITEMS_TOTAL", 220),
		CandidatePoolSize: mustInt("CANDIDATE_POOL_SIZE", 80),
		CuratedItemsCount: mustInt("CURATED_ITEMS_COUNT", 10),
		CurationChunkSize: mustInt("CURATION_CHUNK_SIZE", 15),

		TargetDomains:  splitCSV(getEnv("TARGET_DOMAINS", "github.blog,stackoverflow.blog,infoq.com,arstechnica.com,nytimes.com")),
		TargetKeywords: splitCSV(getEnv("TARGET_KEYWORDS", "ai,openai,programming,devtools,golang,python,javascript,economy,macroeconomics,startups")),
		BlockedDomains: splitCSV(getEnv("BLOCKED_DOMAINS", "")),

		WeightRelevance:   mustFloat("WEIGHT_RELEVANCE", 0.45),
		WeightNovelty:     mustFloat("WEIGHT_NOVELTY", 0.25),
		WeightCredibility: mustFloat("WEIGHT_CREDIBILITY", 0.20),
		WeightTarget:      mustFloat("WEIGHT_TARGET", 0.10),
		MaxPerDomain:      mustInt("MAX_PER_DOMAIN", 2),

		Timezone: getEnv("TIMEZONE", "America/Sao_Paulo"),

		SMTPURL:      getEnv("SMTP_URL", ""),
		SMTPHost:     getEnv("SMTP_HOST", "smtp.gmail.com"),
		SMTPPort:     mustInt("SMTP_PORT", 587),
		SMTPUser:     getEnv("SMTP_USER", ""),
		SMTPPass:     getEnv("SMTP_PASS", ""),
		EmailFrom:    getEnv("EMAIL_FROM", ""),
		EmailTo:      splitCSV(getEnv("EMAIL_TO", "")),
		EmailSubject: getEnv("EMAIL_SUBJECT", "Newsletter diária de tecnologia, programação e economia"),

		DatabasePath: getEnv("DATABASE_PATH", "./data/newsletter.db"),
		LogLevel:     getEnv("LOG_LEVEL", "info"),
		RequireAI:    mustBool("REQUIRE_AI_PTBR", false),
		HTTPTimeout:  time.Duration(mustInt("HTTP_TIMEOUT_SECONDS", 20)) * time.Second,
	}
	if err := applySMTPURL(&cfg); err != nil {
		return cfg, err
	}

	if cfg.OpenAIAPIKey == "" {
		return cfg, fmt.Errorf("OPENAI_API_KEY is required")
	}
	if cfg.EmailFrom == "" {
		return cfg, fmt.Errorf("EMAIL_FROM is required")
	}
	if len(cfg.EmailTo) == 0 {
		return cfg, fmt.Errorf("EMAIL_TO is required")
	}
	if cfg.SMTPHost == "" || cfg.SMTPPort <= 0 || cfg.SMTPUser == "" || cfg.SMTPPass == "" {
		return cfg, fmt.Errorf("SMTP config is required (use SMTP_URL or SMTP_HOST/SMTP_PORT/SMTP_USER/SMTP_PASS)")
	}
	return cfg, nil
}

func applySMTPURL(cfg *Config) error {
	if strings.TrimSpace(cfg.SMTPURL) == "" {
		return nil
	}
	u, err := url.Parse(cfg.SMTPURL)
	if err != nil {
		return fmt.Errorf("invalid SMTP_URL: %w", err)
	}
	if u.Scheme != "smtp" {
		return fmt.Errorf("SMTP_URL must use smtp:// scheme")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("SMTP_URL missing host")
	}
	port := 587
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 {
			return fmt.Errorf("SMTP_URL invalid port")
		}
		port = n
	}
	user := ""
	pass := ""
	if u.User != nil {
		user = u.User.Username()
		pass, _ = u.User.Password()
	}
	if user == "" {
		return fmt.Errorf("SMTP_URL missing username")
	}
	if pass == "" {
		return fmt.Errorf("SMTP_URL missing password")
	}
	cfg.SMTPHost = host
	cfg.SMTPPort = port
	cfg.SMTPUser = user
	cfg.SMTPPass = pass
	return nil
}

func defaultFeeds() []string {
	return []string{
		"https://hnrss.org/frontpage",
		"https://hnrss.org/newest?q=golang+OR+python+OR+javascript",
		"http://feeds.arstechnica.com/arstechnica/index",
		"https://feed.infoq.com/",
		"https://github.blog/feed/",
		"https://stackoverflow.blog/feed/",
		"https://rss.nytimes.com/services/xml/rss/nyt/Technology.xml",
		"https://rss.nytimes.com/services/xml/rss/nyt/Business.xml",
		"https://rss.nytimes.com/services/xml/rss/nyt/Economy.xml",
		"https://news.google.com/rss/search?q=tecnologia",
		"https://news.google.com/rss/search?q=programacao",
		"https://news.google.com/rss/search?q=economia",
	}
}

func getEnv(k, def string) string {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}
	return v
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

func mustInt(k string, def int) int {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func mustFloat(k string, def float64) float64 {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return n
}

func mustBool(k string, def bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(k)))
	if v == "" {
		return def
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}
