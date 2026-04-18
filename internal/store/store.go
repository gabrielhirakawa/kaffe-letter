package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"kaffe-letter/internal/model"
)

type Store struct {
	db *sql.DB
}

var (
	sharedMu sync.Mutex
	sharedDB = map[string]*sql.DB{}
)

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	sharedMu.Lock()
	db, ok := sharedDB[absPath]
	sharedMu.Unlock()
	if ok {
		return &Store{db: db}, nil
	}

	db, err = sql.Open("sqlite", absPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	s := &Store{db: db}
	if err := s.configure(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}

	sharedMu.Lock()
	defer sharedMu.Unlock()
	if existing, ok := sharedDB[absPath]; ok {
		_ = db.Close()
		return &Store{db: existing}, nil
	}
	sharedDB[absPath] = db
	return s, nil
}

func (s *Store) Close() error {
	return nil
}

func (s *Store) configure() error {
	pragmas := []string{
		`PRAGMA journal_mode = WAL;`,
		`PRAGMA synchronous = NORMAL;`,
		`PRAGMA busy_timeout = 30000;`,
		`PRAGMA wal_autocheckpoint = 1000;`,
		`PRAGMA temp_store = MEMORY;`,
		`PRAGMA foreign_keys = ON;`,
	}
	for _, stmt := range pragmas {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			status TEXT NOT NULL,
			error_message TEXT,
			current_stage TEXT,
			progress_message TEXT,
			last_heartbeat_at DATETIME,
			created_at DATETIME NOT NULL,
			finished_at DATETIME
		);`,
		`CREATE TABLE IF NOT EXISTS items_raw (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id INTEGER NOT NULL,
			title TEXT NOT NULL,
			url TEXT NOT NULL,
			url_norm TEXT NOT NULL,
			domain TEXT NOT NULL,
			source_name TEXT,
			image_url TEXT,
			summary TEXT,
			published_at DATETIME,
			source_feed TEXT,
			category TEXT,
			item_hash TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			UNIQUE(item_hash),
			FOREIGN KEY(run_id) REFERENCES runs(id)
		);`,
		`CREATE TABLE IF NOT EXISTS items_curated (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id INTEGER NOT NULL,
			title TEXT NOT NULL,
			title_en TEXT NOT NULL,
			title_pt_br TEXT NOT NULL,
			category TEXT NOT NULL DEFAULT 'tendencias',
			url TEXT NOT NULL,
			domain TEXT NOT NULL,
			image_url TEXT,
			summary_en TEXT NOT NULL,
			summary_pt_br TEXT NOT NULL,
			why_it_matters_en TEXT NOT NULL,
			why_it_matters_pt_br TEXT NOT NULL,
			relevance_score REAL,
			novelty_score REAL,
			credibility_score REAL,
			target_match INTEGER NOT NULL,
			target_reason TEXT,
			final_score REAL,
			rank_position INTEGER,
			created_at DATETIME NOT NULL,
			FOREIGN KEY(run_id) REFERENCES runs(id)
		);`,
		`CREATE TABLE IF NOT EXISTS deliveries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id INTEGER NOT NULL,
			status TEXT NOT NULL,
			error_message TEXT,
			recipient_count INTEGER NOT NULL,
			created_at DATETIME NOT NULL,
			FOREIGN KEY(run_id) REFERENCES runs(id)
		);`,
		`CREATE TABLE IF NOT EXISTS run_metrics (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id INTEGER NOT NULL UNIQUE,
			rss_ms INTEGER NOT NULL,
			curation_ms INTEGER NOT NULL,
			translation_ms INTEGER NOT NULL,
			normalize_ms INTEGER NOT NULL,
			persist_ms INTEGER NOT NULL,
			render_ms INTEGER NOT NULL,
			send_ms INTEGER NOT NULL,
			telegram_ms INTEGER NOT NULL DEFAULT 0,
			total_ms INTEGER NOT NULL,
			created_at DATETIME NOT NULL,
			FOREIGN KEY(run_id) REFERENCES runs(id)
		);`,
		`CREATE TABLE IF NOT EXISTS app_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS categories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			slug TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			description TEXT,
			item_quota INTEGER NOT NULL DEFAULT 2,
			sort_order INTEGER NOT NULL DEFAULT 0,
			is_active INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS feeds (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			category_slug TEXT NOT NULL,
			name TEXT NOT NULL,
			url TEXT NOT NULL UNIQUE,
			site_domain TEXT,
			priority INTEGER NOT NULL DEFAULT 0,
			is_active INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(category_slug) REFERENCES categories(slug)
		);`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	// Backward-compatible migration for existing databases.
	if err := s.ensureColumn("runs", "current_stage", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("runs", "progress_message", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("runs", "last_heartbeat_at", "DATETIME"); err != nil {
		return err
	}
	if err := s.ensureColumn("items_curated", "title_en", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("items_curated", "title_pt_br", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("items_curated", "category", "TEXT NOT NULL DEFAULT 'tendencias'"); err != nil {
		return err
	}
	if err := s.ensureColumn("items_curated", "summary_en", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("items_raw", "source_name", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("items_raw", "image_url", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("items_raw", "category", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("items_curated", "image_url", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("items_curated", "why_it_matters_en", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("items_curated", "why_it_matters_pt_br", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("run_metrics", "telegram_ms", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureColumn(table, column, columnType string) error {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var (
		cid       int
		name      string
		typ       string
		notnull   int
		dfltValue sql.NullString
		pk        int
	)
	for rows.Next() {
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dfltValue, &pk); err != nil {
			return err
		}
		if strings.EqualFold(name, column) {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, columnType))
	return err
}

func (s *Store) EnsureSettings(ctx context.Context, defaults map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO app_settings(key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO NOTHING
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().UTC()
	for key, value := range defaults {
		if _, err := stmt.ExecContext(ctx, key, value, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) EnsureCategories(ctx context.Context, categories []model.CategoryConfig) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO categories(slug, name, description, item_quota, sort_order, is_active, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(slug) DO UPDATE SET
			name=excluded.name,
			description=excluded.description,
			item_quota=excluded.item_quota,
			sort_order=excluded.sort_order,
			is_active=excluded.is_active,
			updated_at=excluded.updated_at
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().UTC()
	for _, item := range categories {
		active := 0
		if item.IsActive {
			active = 1
		}
		if _, err := stmt.ExecContext(ctx, item.Slug, item.Name, item.Description, item.ItemQuota, item.SortOrder, active, now, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) EnsureFeeds(ctx context.Context, feeds []model.FeedConfig) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO feeds(category_slug, name, url, site_domain, priority, is_active, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(url) DO NOTHING
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().UTC()
	for _, item := range feeds {
		active := 0
		if item.IsActive {
			active = 1
		}
		if _, err := stmt.ExecContext(ctx, item.CategorySlug, item.Name, item.URL, item.SiteDomain, item.Priority, active, now, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListCategories(ctx context.Context, activeOnly bool) ([]model.CategoryConfig, error) {
	query := `
		SELECT id, slug, name, COALESCE(description, ''), item_quota, sort_order, is_active
		FROM categories
	`
	if activeOnly {
		query += ` WHERE is_active = 1`
	}
	query += ` ORDER BY sort_order ASC, id ASC`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.CategoryConfig, 0, 8)
	for rows.Next() {
		var item model.CategoryConfig
		var active int
		if err := rows.Scan(&item.ID, &item.Slug, &item.Name, &item.Description, &item.ItemQuota, &item.SortOrder, &active); err != nil {
			return nil, err
		}
		item.IsActive = active == 1
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) ListFeeds(ctx context.Context, activeOnly bool) ([]model.FeedConfig, error) {
	query := `
		SELECT id, category_slug, name, url, COALESCE(site_domain, ''), priority, is_active
		FROM feeds
	`
	if activeOnly {
		query += ` WHERE is_active = 1`
	}
	query += ` ORDER BY category_slug ASC, priority DESC, id ASC`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.FeedConfig, 0, 24)
	for rows.Next() {
		var item model.FeedConfig
		var active int
		if err := rows.Scan(&item.ID, &item.CategorySlug, &item.Name, &item.URL, &item.SiteDomain, &item.Priority, &active); err != nil {
			return nil, err
		}
		item.IsActive = active == 1
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) CreateCategory(ctx context.Context, item model.CategoryConfig) error {
	active := 0
	if item.IsActive {
		active = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO categories(slug, name, description, item_quota, sort_order, is_active, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, item.Slug, item.Name, item.Description, item.ItemQuota, item.SortOrder, active, time.Now().UTC(), time.Now().UTC())
	return err
}

func (s *Store) UpdateCategory(ctx context.Context, item model.CategoryConfig) error {
	active := 0
	if item.IsActive {
		active = 1
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE categories
		SET name = ?, description = ?, item_quota = ?, sort_order = ?, is_active = ?, updated_at = ?
		WHERE slug = ?
	`, item.Name, item.Description, item.ItemQuota, item.SortOrder, active, time.Now().UTC(), item.Slug)
	return err
}

func (s *Store) CreateFeed(ctx context.Context, item model.FeedConfig) error {
	active := 0
	if item.IsActive {
		active = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO feeds(category_slug, name, url, site_domain, priority, is_active, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, item.CategorySlug, item.Name, item.URL, item.SiteDomain, item.Priority, active, time.Now().UTC(), time.Now().UTC())
	return err
}

func (s *Store) UpdateFeed(ctx context.Context, item model.FeedConfig) error {
	active := 0
	if item.IsActive {
		active = 1
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE feeds
		SET category_slug = ?, name = ?, url = ?, site_domain = ?, priority = ?, is_active = ?, updated_at = ?
		WHERE id = ?
	`, item.CategorySlug, item.Name, item.URL, item.SiteDomain, item.Priority, active, time.Now().UTC(), item.ID)
	return err
}

func (s *Store) DeleteCategoryBySlug(ctx context.Context, slug string) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM feeds WHERE category_slug = ?`, slug).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("category has feeds associated")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM categories WHERE slug = ?`, slug)
	return err
}

func (s *Store) DeleteFeedByID(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM feeds WHERE id = ?`, id)
	return err
}

func (s *Store) GetSettings(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM app_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var key string
		var value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) UpsertSettings(ctx context.Context, values map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO app_settings(key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value=excluded.value,
			updated_at=excluded.updated_at
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().UTC()
	for key, value := range values {
		if _, err := stmt.ExecContext(ctx, key, value, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) StartRun(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO runs(status, current_stage, progress_message, last_heartbeat_at, created_at) VALUES(?, ?, ?, ?, ?)`,
		"running", "starting", "Inicializando execução", time.Now().UTC(), time.Now().UTC(),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) FinishRun(ctx context.Context, runID int64, status, errMsg string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE runs SET status=?, error_message=?, current_stage=?, progress_message=?, last_heartbeat_at=?, finished_at=? WHERE id=?`,
		status, nullIfEmpty(errMsg), status, nullIfEmpty(errMsg), time.Now().UTC(), time.Now().UTC(), runID,
	)
	return err
}

func (s *Store) UpdateRunProgress(ctx context.Context, runID int64, stage, message string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE runs SET current_stage=?, progress_message=?, last_heartbeat_at=? WHERE id=?`,
		stage, message, time.Now().UTC(), runID,
	)
	return err
}

func (s *Store) SaveRawItems(ctx context.Context, runID int64, items []model.RawItem) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO items_raw
		(run_id, title, url, url_norm, domain, source_name, image_url, summary, published_at, source_feed, category, item_hash, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().UTC()
	for _, it := range items {
		if _, err := stmt.ExecContext(ctx,
			runID, it.Title, it.URL, it.URLNorm, it.Domain, it.SourceName, it.ImageURL, it.Summary, it.PublishedAt, it.SourceFeed, it.Category, it.ItemHash, now,
		); err != nil {
			return fmt.Errorf("insert raw item: %w", err)
		}
	}
	return tx.Commit()
}

func (s *Store) SaveCuratedItems(ctx context.Context, runID int64, items []model.CuratedItem) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO items_curated
		(run_id, title, title_en, title_pt_br, category, url, domain, image_url, summary_en, summary_pt_br, why_it_matters_en, why_it_matters_pt_br, relevance_score, novelty_score, credibility_score, target_match, target_reason, final_score, rank_position, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().UTC()
	for i, it := range items {
		if strings.TrimSpace(it.TitleEN) == "" ||
			strings.TrimSpace(it.TitlePTBR) == "" ||
			strings.TrimSpace(it.SummaryEN) == "" ||
			strings.TrimSpace(it.SummaryPTBR) == "" ||
			strings.TrimSpace(it.WhyItMattersEN) == "" ||
			strings.TrimSpace(it.WhyItMattersPTBR) == "" {
			return fmt.Errorf("curated item %d missing bilingual fields", i+1)
		}
		target := 0
		if it.TargetMatch {
			target = 1
		}
		if _, err := stmt.ExecContext(ctx,
			runID, it.Title, it.TitleEN, it.TitlePTBR, it.Category, it.URL, it.Domain, it.ImageURL, it.SummaryEN, it.SummaryPTBR, it.WhyItMattersEN, it.WhyItMattersPTBR,
			it.RelevanceScore, it.NoveltyScore, it.CredibilityScore, target, it.TargetReason,
			it.FinalScore, i+1, now,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SaveDelivery(ctx context.Context, runID int64, status, errMsg string, recipientCount int) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO deliveries(run_id, status, error_message, recipient_count, created_at) VALUES (?, ?, ?, ?, ?)`,
		runID, status, nullIfEmpty(errMsg), recipientCount, time.Now().UTC(),
	)
	return err
}

func (s *Store) SaveRunMetrics(ctx context.Context, runID int64, m model.RunMetrics) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO run_metrics
		(run_id, rss_ms, curation_ms, translation_ms, normalize_ms, persist_ms, render_ms, send_ms, telegram_ms, total_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id) DO UPDATE SET
			rss_ms=excluded.rss_ms,
			curation_ms=excluded.curation_ms,
			translation_ms=excluded.translation_ms,
			normalize_ms=excluded.normalize_ms,
			persist_ms=excluded.persist_ms,
			render_ms=excluded.render_ms,
			send_ms=excluded.send_ms,
			telegram_ms=excluded.telegram_ms,
			total_ms=excluded.total_ms
	`, runID, m.RSSMS, m.CurationMS, m.TranslationMS, m.NormalizeMS, m.PersistMS, m.RenderMS, m.SendMS, m.TelegramMS, m.TotalMS, time.Now().UTC())
	return err
}

func (s *Store) GetRunMetrics(ctx context.Context, runID int64) (model.RunMetrics, error) {
	var m model.RunMetrics
	err := s.db.QueryRowContext(ctx, `
		SELECT rss_ms, curation_ms, translation_ms, normalize_ms, persist_ms, render_ms, send_ms, COALESCE(telegram_ms, 0), total_ms
		FROM run_metrics WHERE run_id = ?
	`, runID).Scan(&m.RSSMS, &m.CurationMS, &m.TranslationMS, &m.NormalizeMS, &m.PersistMS, &m.RenderMS, &m.SendMS, &m.TelegramMS, &m.TotalMS)
	if err != nil {
		if err == sql.ErrNoRows {
			return model.RunMetrics{}, nil
		}
		return model.RunMetrics{}, err
	}
	return m, nil
}

func (s *Store) GetLatestSuccessfulRunID(ctx context.Context) (int64, error) {
	var runID int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM runs WHERE status = 'success' ORDER BY id DESC LIMIT 1`).Scan(&runID)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, fmt.Errorf("no successful run found")
		}
		return 0, err
	}
	return runID, nil
}

func (s *Store) ListRecentRuns(ctx context.Context, limit int) ([]model.RunSummary, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, status, COALESCE(error_message, ''), COALESCE(current_stage, ''), COALESCE(progress_message, ''), COALESCE(last_heartbeat_at, created_at), created_at, COALESCE(finished_at, created_at)
		FROM runs
		ORDER BY id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.RunSummary, 0, limit)
	for rows.Next() {
		var item model.RunSummary
		var heartbeatAt string
		var createdAt string
		var finishedAt string
		if err := rows.Scan(&item.ID, &item.Status, &item.ErrorMessage, &item.CurrentStage, &item.ProgressMsg, &heartbeatAt, &createdAt, &finishedAt); err != nil {
			return nil, err
		}
		item.HeartbeatAt = parseSQLiteTime(heartbeatAt)
		item.CreatedAt = parseSQLiteTime(createdAt)
		item.FinishedAt = parseSQLiteTime(finishedAt)
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) GetCurrentRun(ctx context.Context) (model.RunSummary, error) {
	var item model.RunSummary
	var heartbeatAt string
	var createdAt string
	var finishedAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, status, COALESCE(error_message, ''), COALESCE(current_stage, ''), COALESCE(progress_message, ''), COALESCE(last_heartbeat_at, created_at), created_at, COALESCE(finished_at, created_at)
		FROM runs
		ORDER BY id DESC
		LIMIT 1
	`).Scan(&item.ID, &item.Status, &item.ErrorMessage, &item.CurrentStage, &item.ProgressMsg, &heartbeatAt, &createdAt, &finishedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return model.RunSummary{}, nil
		}
		return model.RunSummary{}, err
	}
	item.HeartbeatAt = parseSQLiteTime(heartbeatAt)
	item.CreatedAt = parseSQLiteTime(createdAt)
	item.FinishedAt = parseSQLiteTime(finishedAt)
	return item, nil
}

func parseSQLiteTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, value); err == nil {
			return ts
		}
	}
	return time.Time{}
}

func (s *Store) GetCuratedItemsByRunID(ctx context.Context, runID int64) ([]model.CuratedItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			COALESCE(title, ''),
			COALESCE(title_en, ''),
			COALESCE(title_pt_br, ''),
			COALESCE(category, 'tendencias'),
			COALESCE(url, ''),
			COALESCE(domain, ''),
			COALESCE(image_url, ''),
			COALESCE(summary_en, ''),
			COALESCE(summary_pt_br, ''),
			COALESCE(why_it_matters_en, ''),
			COALESCE(why_it_matters_pt_br, ''),
			relevance_score, novelty_score, credibility_score, target_match, COALESCE(target_reason, ''), final_score
		FROM items_curated
		WHERE run_id = ?
		ORDER BY rank_position ASC, final_score DESC
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.CuratedItem, 0, 16)
	for rows.Next() {
		var it model.CuratedItem
		var target int
		if err := rows.Scan(
			&it.Title,
			&it.TitleEN,
			&it.TitlePTBR,
			&it.Category,
			&it.URL,
			&it.Domain,
			&it.ImageURL,
			&it.SummaryEN,
			&it.SummaryPTBR,
			&it.WhyItMattersEN,
			&it.WhyItMattersPTBR,
			&it.RelevanceScore,
			&it.NoveltyScore,
			&it.CredibilityScore,
			&target,
			&it.TargetReason,
			&it.FinalScore,
		); err != nil {
			return nil, err
		}
		it.TargetMatch = target == 1
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no curated items found for run_id=%d", runID)
	}
	return out, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
