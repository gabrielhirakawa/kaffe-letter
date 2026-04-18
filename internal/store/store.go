package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"rss-ai-newsletter/internal/model"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			status TEXT NOT NULL,
			error_message TEXT,
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
			image_url TEXT,
			summary TEXT,
			published_at DATETIME,
			source_feed TEXT,
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
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	// Backward-compatible migration for existing databases.
	if err := s.ensureColumn("items_curated", "title_en", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("items_curated", "title_pt_br", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("items_curated", "summary_en", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("items_raw", "image_url", "TEXT"); err != nil {
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

func (s *Store) StartRun(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO runs(status, created_at) VALUES(?, ?)`,
		"running", time.Now().UTC(),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) FinishRun(ctx context.Context, runID int64, status, errMsg string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE runs SET status=?, error_message=?, finished_at=? WHERE id=?`,
		status, nullIfEmpty(errMsg), time.Now().UTC(), runID,
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
		(run_id, title, url, url_norm, domain, image_url, summary, published_at, source_feed, item_hash, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().UTC()
	for _, it := range items {
		if _, err := stmt.ExecContext(ctx,
			runID, it.Title, it.URL, it.URLNorm, it.Domain, it.ImageURL, it.Summary, it.PublishedAt, it.SourceFeed, it.ItemHash, now,
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
		(run_id, title, title_en, title_pt_br, url, domain, image_url, summary_en, summary_pt_br, why_it_matters_en, why_it_matters_pt_br, relevance_score, novelty_score, credibility_score, target_match, target_reason, final_score, rank_position, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
			runID, it.Title, it.TitleEN, it.TitlePTBR, it.URL, it.Domain, it.ImageURL, it.SummaryEN, it.SummaryPTBR, it.WhyItMattersEN, it.WhyItMattersPTBR,
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

func (s *Store) GetCuratedItemsByRunID(ctx context.Context, runID int64) ([]model.CuratedItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			COALESCE(title, ''),
			COALESCE(title_en, ''),
			COALESCE(title_pt_br, ''),
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
