package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Avik-creator/internal/scheduler"
)

// KL_LISTEN sets the address the scheduler binds to (default: :8080).
func main() {
	listenAddr := env("KL_LISTEN", ":8080")

	log.Printf("starting kube-lite scheduler on %s", listenAddr)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	sched := scheduler.New()
	srv := scheduler.NewServer(listenAddr, sched)

	if err := srv.Start(ctx); err != nil {
		log.Fatalf("scheduler error: %v", err)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
