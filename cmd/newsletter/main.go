package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"kaffe-letter/internal/config"
	"kaffe-letter/internal/pipeline"
	"kaffe-letter/internal/webadmin"
)

func main() {
	mode := flag.String("mode", "run", "run | resend | server")
	runID := flag.Int64("run-id", 0, "run id for resend mode")
	latest := flag.Bool("latest", false, "resend latest successful run")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	switch *mode {
	case "run":
		if err := cfg.ValidateRuntime(); err != nil {
			log.Fatalf("config error: %v", err)
		}
		if err := pipeline.RunDaily(ctx, cfg); err != nil {
			log.Fatalf("run failed: %v", err)
		}
	case "resend":
		if err := cfg.ValidateRuntime(); err != nil {
			log.Fatalf("config error: %v", err)
		}
		if err := pipeline.Resend(ctx, cfg, *runID, *latest); err != nil {
			log.Fatalf("resend failed: %v", err)
		}
	case "server":
		if err := webadmin.Run(ctx, cfg); err != nil {
			log.Fatalf("server failed: %v", err)
		}
	default:
		log.Fatalf("invalid mode: %s (use run, resend or server)", *mode)
	}
	log.Printf("%s completed", *mode)
}
