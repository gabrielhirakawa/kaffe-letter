package webadmin

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"kaffe-letter/internal/config"
	"kaffe-letter/internal/model"
	"kaffe-letter/internal/pipeline"
	"kaffe-letter/internal/store"
)

var errExecutionInProgress = errors.New("execution already in progress")

func (s *Server) startDeliveryScheduler(ctx context.Context) {
	s.checkScheduledDelivery(ctx, true)

	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.checkScheduledDelivery(ctx, false)
		}
	}
}

func (s *Server) checkScheduledDelivery(ctx context.Context, startup bool) {
	cfg, err := config.Load()
	if err != nil {
		log.Printf("scheduler load config failed: %v", err)
		return
	}

	due, scheduledAt, latestRun, err := shouldRunScheduledDelivery(ctx, cfg)
	if err != nil {
		log.Printf("scheduler evaluation failed: %v", err)
		return
	}

	if startup {
		log.Printf("scheduler armed for daily delivery at %s (%s)", cfg.DeliveryTime, cfg.Timezone)
	}

	if !due {
		return
	}

	if latestRun.ID > 0 {
		log.Printf("scheduler launching daily delivery; last run #%d was on %s", latestRun.ID, latestRun.CreatedAt.In(safeLocation(cfg.Timezone)).Format(time.RFC3339))
	} else {
		log.Printf("scheduler launching first daily delivery at %s", scheduledAt.Format(time.RFC3339))
	}

	if err := s.launchExecution(ctx, "scheduled delivery", cfg, func() error {
		return pipeline.RunDaily(context.Background(), cfg)
	}); err != nil {
		if errors.Is(err, errExecutionInProgress) {
			log.Printf("scheduler skipped: execution already in progress")
			return
		}
		log.Printf("scheduler launch failed: %v", err)
	}
}

func (s *Server) launchExecution(ctx context.Context, label string, cfg config.Config, fn func() error) error {
	active, err := hasActiveRun(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	if active {
		return errExecutionInProgress
	}
	if err := cfg.ValidateRuntime(); err != nil {
		return err
	}
	if !s.tryAcquireExecution() {
		return errExecutionInProgress
	}

	go func() {
		defer s.releaseExecution()
		if err := fn(); err != nil {
			log.Printf("%s failed: %v", label, err)
			return
		}
		log.Printf("%s completed", label)
	}()

	return nil
}

func (s *Server) tryAcquireExecution() bool {
	s.executionMu.Lock()
	defer s.executionMu.Unlock()
	if s.executionActive {
		return false
	}
	s.executionActive = true
	return true
}

func (s *Server) releaseExecution() {
	s.executionMu.Lock()
	s.executionActive = false
	s.executionMu.Unlock()
}

func shouldRunScheduledDelivery(ctx context.Context, cfg config.Config) (bool, time.Time, model.RunSummary, error) {
	loc := safeLocation(cfg.Timezone)
	now := time.Now().In(loc)
	scheduledAt, err := scheduledDeliveryTime(now, cfg.DeliveryTime, loc)
	if err != nil {
		return false, time.Time{}, model.RunSummary{}, err
	}
	if now.Before(scheduledAt) {
		return false, scheduledAt, model.RunSummary{}, nil
	}

	st, err := store.Open(cfg.DatabasePath)
	if err != nil {
		return false, time.Time{}, model.RunSummary{}, err
	}
	defer st.Close()

	latestRun, err := st.GetCurrentRun(ctx)
	if err != nil {
		return false, time.Time{}, model.RunSummary{}, err
	}
	if strings.EqualFold(strings.TrimSpace(latestRun.Status), "running") {
		return false, scheduledAt, latestRun, nil
	}
	if latestRun.ID == 0 {
		return true, scheduledAt, latestRun, nil
	}
	if sameCalendarDay(latestRun.CreatedAt.In(loc), now) {
		return false, scheduledAt, latestRun, nil
	}
	return true, scheduledAt, latestRun, nil
}

func scheduledDeliveryTime(now time.Time, deliveryTime string, loc *time.Location) (time.Time, error) {
	parts := strings.Split(strings.TrimSpace(deliveryTime), ":")
	if len(parts) != 2 {
		return time.Time{}, fmt.Errorf("delivery_time must use HH:MM format")
	}
	hour, err := parseClockPart(parts[0])
	if err != nil {
		return time.Time{}, err
	}
	minute, err := parseClockPart(parts[1])
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, loc), nil
}

func parseClockPart(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("delivery_time must use HH:MM format")
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("delivery_time must use HH:MM format")
	}
	if n < 0 || n > 59 {
		return 0, fmt.Errorf("delivery_time must use HH:MM format")
	}
	return n, nil
}

func sameCalendarDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}

func hasActiveRun(ctx context.Context, databasePath string) (bool, error) {
	st, err := store.Open(databasePath)
	if err != nil {
		return false, err
	}
	defer st.Close()

	current, err := st.GetCurrentRun(ctx)
	if err != nil {
		return false, err
	}
	return current.ID > 0 && strings.EqualFold(strings.TrimSpace(current.Status), "running"), nil
}

func safeLocation(name string) *time.Location {
	loc, err := time.LoadLocation(strings.TrimSpace(name))
	if err != nil {
		return time.UTC
	}
	return loc
}
