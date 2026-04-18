package rss

import (
	"context"
	"crypto/sha1"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mmcdole/gofeed"

	"kaffe-letter/internal/model"
)

var htmlTagRe = regexp.MustCompile(`<[^>]+>`)
var imgSrcRe = regexp.MustCompile(`(?i)<img[^>]+src=["']([^"']+)["']`)

type Collector struct {
	HTTPTimeout     time.Duration
	MaxItemsPerFeed int
	MaxItemsTotal   int
	BlockedDomains  map[string]struct{}
	TargetDomains   map[string]struct{}
	TargetKeywords  []string
}

func (c Collector) Fetch(ctx context.Context, feeds []string) ([]model.RawItem, error) {
	configs := make([]model.FeedConfig, 0, len(feeds))
	for _, feedURL := range feeds {
		configs = append(configs, model.FeedConfig{URL: feedURL, Name: feedURL})
	}
	return c.FetchConfigured(ctx, configs)
}

func (c Collector) FetchConfigured(ctx context.Context, feeds []model.FeedConfig) ([]model.RawItem, error) {
	parser := gofeed.NewParser()
	parser.Client = &http.Client{Timeout: c.HTTPTimeout}

	var wg sync.WaitGroup
	type result struct {
		items []model.RawItem
		err   error
	}
	results := make(chan result, len(feeds))

	for _, feedCfg := range feeds {
		feedCfg := feedCfg
		wg.Add(1)
		go func() {
			defer wg.Done()
			feed, err := parser.ParseURLWithContext(feedCfg.URL, ctx)
			if err != nil {
				results <- result{err: fmt.Errorf("feed %s: %w", feedCfg.URL, err)}
				return
			}
			items := make([]model.RawItem, 0, min(c.MaxItemsPerFeed, len(feed.Items)))
			for i, it := range feed.Items {
				if i >= c.MaxItemsPerFeed {
					break
				}
				raw, ok := toRawItem(feedCfg, it)
				if !ok {
					continue
				}
				if _, blocked := c.BlockedDomains[raw.Domain]; blocked {
					continue
				}
				raw.SeedScore = c.seedScore(raw)
				items = append(items, raw)
			}
			results <- result{items: items}
		}()
	}

	wg.Wait()
	close(results)

	combined := make([]model.RawItem, 0, c.MaxItemsTotal)
	seen := map[string]struct{}{}
	for res := range results {
		if res.err != nil {
			continue
		}
		for _, it := range res.items {
			if _, ok := seen[it.ItemHash]; ok {
				continue
			}
			seen[it.ItemHash] = struct{}{}
			combined = append(combined, it)
		}
	}

	sort.SliceStable(combined, func(i, j int) bool {
		if combined[i].SeedScore == combined[j].SeedScore {
			return combined[i].PublishedAt.After(combined[j].PublishedAt)
		}
		return combined[i].SeedScore > combined[j].SeedScore
	})

	if len(combined) > c.MaxItemsTotal {
		combined = combined[:c.MaxItemsTotal]
	}
	return combined, nil
}

func toRawItem(feed model.FeedConfig, it *gofeed.Item) (model.RawItem, bool) {
	u := normalizeURL(it.Link)
	if u == "" {
		return model.RawItem{}, false
	}
	domain := extractDomain(u)
	title := strings.TrimSpace(it.Title)
	if title == "" {
		return model.RawItem{}, false
	}
	summary := sanitizeSummary(it.Description)
	imageURL := extractImageURL(it)
	published := time.Now().UTC()
	if it.PublishedParsed != nil {
		published = it.PublishedParsed.UTC()
	}
	h := sha1.Sum([]byte(u + "|" + strings.ToLower(title)))
	return model.RawItem{
		Title:       title,
		URL:         it.Link,
		URLNorm:     u,
		Domain:      domain,
		SourceName:  feed.Name,
		ImageURL:    imageURL,
		Summary:     truncate(summary, 420),
		PublishedAt: published,
		SourceFeed:  feed.URL,
		Category:    feed.CategorySlug,
		ItemHash:    fmt.Sprintf("%x", h[:]),
	}, true
}

func sanitizeSummary(s string) string {
	s = html.UnescapeString(s)
	s = htmlTagRe.ReplaceAllString(s, " ")
	s = stripNewlines(s)
	return strings.TrimSpace(s)
}

func extractImageURL(it *gofeed.Item) string {
	if it == nil {
		return ""
	}
	if it.Image != nil {
		if u := sanitizeImageURL(it.Image.URL); u != "" {
			return u
		}
	}
	for _, enc := range it.Enclosures {
		if u := sanitizeImageURL(enc.URL); u != "" && isImageType(enc.Type, enc.URL) {
			return u
		}
	}
	if u := firstImageFromHTML(it.Content); u != "" {
		return u
	}
	if u := firstImageFromHTML(it.Description); u != "" {
		return u
	}
	if mediaURL := firstMediaURLFromExtensions(it); mediaURL != "" {
		return mediaURL
	}
	return ""
}

func firstImageFromHTML(htmlText string) string {
	if strings.TrimSpace(htmlText) == "" {
		return ""
	}
	m := imgSrcRe.FindStringSubmatch(htmlText)
	if len(m) < 2 {
		return ""
	}
	return sanitizeImageURL(m[1])
}

func firstMediaURLFromExtensions(it *gofeed.Item) string {
	if it.Extensions == nil {
		return ""
	}
	for namespace, groups := range it.Extensions {
		if !strings.EqualFold(namespace, "media") {
			continue
		}
		for tag, entries := range groups {
			if !strings.EqualFold(tag, "content") && !strings.EqualFold(tag, "thumbnail") {
				continue
			}
			for _, entry := range entries {
				if u := sanitizeImageURL(entry.Attrs["url"]); u != "" {
					return u
				}
			}
		}
	}
	return ""
}

func sanitizeImageURL(raw string) string {
	raw = strings.TrimSpace(html.UnescapeString(raw))
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	u.Fragment = ""
	return u.String()
}

func isImageType(contentType, urlValue string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if strings.HasPrefix(ct, "image/") {
		return true
	}
	l := strings.ToLower(urlValue)
	return strings.Contains(l, ".jpg") || strings.Contains(l, ".jpeg") || strings.Contains(l, ".png") || strings.Contains(l, ".webp") || strings.Contains(l, ".gif")
}

func normalizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	q := u.Query()
	for key := range q {
		lk := strings.ToLower(key)
		if strings.HasPrefix(lk, "utm_") || lk == "ref" || lk == "source" {
			q.Del(key)
		}
	}
	u.RawQuery = q.Encode()
	u.Fragment = ""
	u.Host = strings.ToLower(u.Host)
	u.Scheme = strings.ToLower(u.Scheme)
	return strings.TrimRight(u.String(), "/")
}

func extractDomain(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	h := strings.ToLower(u.Hostname())
	h = strings.TrimPrefix(h, "www.")
	return h
}

func stripNewlines(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.Join(strings.Fields(s), " ")
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (c Collector) seedScore(item model.RawItem) float64 {
	score := 0.0
	if _, ok := c.TargetDomains[item.Domain]; ok {
		score += 12
	}
	combined := strings.ToLower(item.Title + " " + item.Summary)
	for _, kw := range c.TargetKeywords {
		if strings.Contains(combined, strings.ToLower(kw)) {
			score += 3
		}
	}
	hoursAgo := time.Since(item.PublishedAt).Hours()
	if hoursAgo < 24 {
		score += 10 - (hoursAgo / 3)
	}
	if score < 0 {
		score = 0
	}
	return score
}
