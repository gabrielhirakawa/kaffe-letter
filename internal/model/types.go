package model

import "time"

type RawItem struct {
	Title       string
	URL         string
	URLNorm     string
	Domain      string
	SourceName  string
	ImageURL    string
	Summary     string
	PublishedAt time.Time
	SourceFeed  string
	Category    string
	ItemHash    string
	SeedScore   float64
}

type CuratedItem struct {
	Title            string
	TitleEN          string
	TitlePTBR        string
	Category         string
	URL              string
	Domain           string
	ImageURL         string
	SummaryEN        string
	SummaryPTBR      string
	WhyItMattersEN   string
	WhyItMattersPTBR string
	RelevanceScore   float64
	NoveltyScore     float64
	CredibilityScore float64
	TargetMatch      bool
	TargetReason     string
	FinalScore       float64
}

type TokenUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

func (u *TokenUsage) Add(other TokenUsage) {
	u.PromptTokens += other.PromptTokens
	u.CompletionTokens += other.CompletionTokens
	u.TotalTokens += other.TotalTokens
}

type RunMetrics struct {
	RSSMS         int64
	CurationMS    int64
	TranslationMS int64
	NormalizeMS   int64
	PersistMS     int64
	RenderMS      int64
	SendMS        int64
	TelegramMS    int64
	TotalMS       int64
}

type RunSummary struct {
	ID           int64
	Status       string
	ErrorMessage string
	CurrentStage string
	ProgressMsg  string
	HeartbeatAt  time.Time
	CreatedAt    time.Time
	FinishedAt   time.Time
}

type CategoryConfig struct {
	ID          int64
	Slug        string
	Name        string
	Description string
	ItemQuota   int
	SortOrder   int
	IsActive    bool
}

type FeedConfig struct {
	ID           int64
	CategorySlug string
	Name         string
	URL          string
	SiteDomain   string
	Priority     int
	IsActive     bool
}
