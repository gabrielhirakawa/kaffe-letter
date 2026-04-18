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

	"rss-ai-newsletter/internal/model"
)

var htmlTagRe = regexp.MustCompile(`<[^>]+>`)

type Collector struct {
	HTTPTimeout     time.Duration
	MaxItemsPerFeed int
	MaxItemsTotal   int
	BlockedDomains  map[string]struct{}
	TargetDomains   map[string]struct{}
	TargetKeywords  []string
}

func (c Collector) Fetch(ctx context.Context, feeds []string) ([]model.RawItem, error) {
	parser := gofeed.NewParser()
	parser.Client = &http.Client{Timeout: c.HTTPTimeout}

	var wg sync.WaitGroup
	type result struct {
		items []model.RawItem
		err   error
	}
	results := make(chan result, len(feeds))

	for _, feedURL := range feeds {
		feedURL := feedURL
		wg.Add(1)
		go func() {
			defer wg.Done()
			feed, err := parser.ParseURLWithContext(feedURL, ctx)
			if err != nil {
				results <- result{err: fmt.Errorf("feed %s: %w", feedURL, err)}
				return
			}
			items := make([]model.RawItem, 0, min(c.MaxItemsPerFeed, len(feed.Items)))
			for i, it := range feed.Items {
				if i >= c.MaxItemsPerFeed {
					break
				}
				raw, ok := toRawItem(feedURL, it)
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

func toRawItem(feedURL string, it *gofeed.Item) (model.RawItem, bool) {
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
		Summary:     truncate(summary, 420),
		PublishedAt: published,
		SourceFeed:  feedURL,
		ItemHash:    fmt.Sprintf("%x", h[:]),
	}, true
}

func sanitizeSummary(s string) string {
	s = html.UnescapeString(s)
	s = htmlTagRe.ReplaceAllString(s, " ")
	s = stripNewlines(s)
	return strings.TrimSpace(s)
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
