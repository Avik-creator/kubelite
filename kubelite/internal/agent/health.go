package agent

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	kl "github.com/Avik-creator/pkg/types"
)

// HealthChecker manages per-container HTTP health probes.
// Each probe runs in its own goroutine and is cancelled via StopProbe.
type HealthChecker struct {
	srv     *Server
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func NewHealthChecker(srv *Server) *HealthChecker {
	return &HealthChecker{
		srv:     srv,
		cancels: make(map[string]context.CancelFunc),
	}
}

// StartProbe begins a recurring health probe for containerID.
// If spec is nil the container is considered permanently healthy; nothing is scheduled.
func (h *HealthChecker) StartProbe(containerID string, spec *kl.HealthCheckSpec) {
	if spec == nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())

	h.mu.Lock()
	if old, ok := h.cancels[containerID]; ok {
		old() // stop any previous probe for this ID
	}
	h.cancels[containerID] = cancel
	h.mu.Unlock()

	interval := time.Duration(spec.IntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 10 * time.Second
	}
	timeout := time.Duration(spec.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	threshold := spec.FailureThreshold
	if threshold <= 0 {
		threshold = 3
	}

	url := fmt.Sprintf("http://localhost:%d%s", spec.Port, spec.Path)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		// Mark as "starting" immediately so the first heartbeat reflects intent.
		h.srv.updateHealth(containerID, kl.HealthStarting, 0)

		failures := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := httpProbe(ctx, url, timeout); err != nil {
					failures++
					log.Printf("health probe failed for %s (%s): %v [%d/%d]",
						containerID[:12], url, err, failures, threshold)
					if failures >= threshold {
						h.srv.updateHealth(containerID, kl.HealthUnhealthy, failures)
					}
				} else {
					failures = 0
					h.srv.updateHealth(containerID, kl.HealthHealthy, 0)
				}
			}
		}
	}()
}

// StopProbe cancels the probe goroutine for containerID.
func (h *HealthChecker) StopProbe(containerID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cancel, ok := h.cancels[containerID]; ok {
		cancel()
		delete(h.cancels, containerID)
	}
}

func httpProbe(ctx context.Context, url string, timeout time.Duration) error {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("probe returned HTTP %d", resp.StatusCode)
	}
	return nil
}
