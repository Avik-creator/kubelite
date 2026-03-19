package main

import (
	"log"
	"os"

	"github.com/Avik-creator/internal/agent"
)

// Configuration is read from environment variables so the binary can run
// as a Docker container or a plain process without code changes.
//
//	KL_NODE_ID        unique name for this agent (default: hostname)
//	KL_LISTEN         address the agent binds to (default: :8081)
//	KL_SCHEDULER      full URL of the central scheduler (default: http://localhost:8080)
func main() {
	nodeID := env("KL_NODE_ID", hostname())
	listenAddr := env("KL_LISTEN", ":8081")
	schedulerAddr := env("KL_SCHEDULER", "http://localhost:8080")

	log.Printf("starting kube-lite agent (node=%s, listen=%s, scheduler=%s)",
		nodeID, listenAddr, schedulerAddr)

	runner, err := agent.NewDockerRunner()
	if err != nil {
		log.Fatalf("could not connect to Docker: %v", err)
	}

	srv := agent.NewServer(nodeID, listenAddr, schedulerAddr, runner)
	if err := srv.Start(); err != nil {
		log.Fatalf("agent server error: %v", err)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "node-unknown"
	}
	return h
}
