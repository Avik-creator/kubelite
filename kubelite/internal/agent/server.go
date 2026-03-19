package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	kl "github.com/Avik-creator/pkg/types"
)

// Server is the HTTP server that runs on each worker node.
// The scheduler calls into it to run/stop containers and check status.
type Server struct {
	nodeID        string
	listenAddr    string
	schedulerAddr string
	runner        *DockerRunner
	health        *HealthChecker

	mu         sync.RWMutex
	containers map[string]*kl.ContainerInstance // containerID → instance
}

func NewServer(nodeID, listenAddr, schedulerAddr string, runner *DockerRunner) *Server {
	// Ensure schedulerAddr always has an http:// scheme so http.Post doesn't
	// reject it with "unsupported protocol scheme".
	if !strings.HasPrefix(schedulerAddr, "http://") && !strings.HasPrefix(schedulerAddr, "https://") {
		schedulerAddr = "http://" + schedulerAddr
	}
	srv := &Server{
		nodeID:        nodeID,
		listenAddr:    listenAddr,
		schedulerAddr: schedulerAddr,
		runner:        runner,
		containers:    make(map[string]*kl.ContainerInstance),
	}
	srv.health = NewHealthChecker(srv)
	return srv
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /run", s.handleRun)
	mux.HandleFunc("POST /stop/{containerID}", s.handleStop)
	mux.HandleFunc("GET /status/{containerID}", s.handleStatus)
	mux.HandleFunc("GET /logs/{containerID}", s.handleLogs)
	mux.HandleFunc("GET /health", s.handleHealth)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go s.registerAndHeartbeat()

	httpSrv := &http.Server{Addr: s.listenAddr, Handler: mux}

	go func() {
		<-ctx.Done()
		log.Printf("agent %s shutting down…", s.nodeID)
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		httpSrv.Shutdown(shutCtx)
	}()

	log.Printf("agent %s listening on %s", s.nodeID, s.listenAddr)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// handleRun starts a container as instructed by the scheduler.
func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	var req kl.RunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	containerID, err := s.runner.Run(r.Context(), req)
	if err != nil {
		log.Printf("run failed for workload %s: %v", req.WorkloadID, err)
		resp := kl.RunResponse{Error: err.Error()}
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(resp)
		return
	}

	initialHealth := kl.HealthUnknown
	if req.HealthCheck == nil {
		initialHealth = kl.HealthHealthy // no probe = always considered healthy
	}

	// Track this container locally
	s.mu.Lock()
	s.containers[containerID] = &kl.ContainerInstance{
		ID:         containerID,
		WorkloadID: req.WorkloadID,
		NodeID:     s.nodeID,
		Image:      req.Image,
		State:      kl.ContainerRunning,
		StartedAt:  time.Now(),
		Health:     initialHealth,
		HealthOK:   req.HealthCheck == nil, // true immediately if no probe
	}
	s.mu.Unlock()

	// Start health probing in background — results surface via next heartbeat
	s.health.StartProbe(containerID, req.HealthCheck)

	log.Printf("started container %s for workload %s", containerID[:12], req.WorkloadID)
	json.NewEncoder(w).Encode(kl.RunResponse{ContainerID: containerID})
}

// handleStop stops and removes a container.
func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	containerID := r.PathValue("containerID")

	// Cancel health probe before stopping — prevents probe errors after container dies
	s.health.StopProbe(containerID)

	if err := s.runner.Stop(r.Context(), containerID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.mu.Lock()
	delete(s.containers, containerID)
	s.mu.Unlock()

	log.Printf("stopped container %s", containerID[:12])
	w.WriteHeader(http.StatusNoContent)
}

// handleStatus returns the current state of a container.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	containerID := r.PathValue("containerID")

	state, exitCode, err := s.runner.Status(r.Context(), containerID)
	if err != nil {
		resp := kl.StatusResponse{ContainerID: containerID, State: kl.ContainerUnknown, Error: err.Error()}
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(resp)
		return
	}

	// Sync our local tracking with what Docker actually sees
	s.mu.Lock()
	if inst, ok := s.containers[containerID]; ok {
		inst.State = state
		inst.ExitCode = exitCode
	}
	s.mu.Unlock()

	json.NewEncoder(w).Encode(kl.StatusResponse{
		ContainerID: containerID,
		State:       state,
		ExitCode:    exitCode,
	})
}

// handleLogs streams stdout+stderr from a container back to the caller.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	containerID := r.PathValue("containerID")
	tail := r.URL.Query().Get("tail")
	if tail == "" {
		tail = "100"
	}
	follow := r.URL.Query().Get("follow") != "false" // default: follow=true

	logs, err := s.runner.Logs(r.Context(), containerID, tail, follow)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer logs.Close()

	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	flusher, canFlush := w.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		n, err := logs.Read(buf)
		if n > 0 {
			// Docker multiplexes stdout/stderr with an 8-byte header; strip it
			data := buf[:n]
			if len(data) >= 8 {
				data = data[8:]
			}
			w.Write(data)
			if canFlush {
				flusher.Flush()
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("log stream error for %s: %v", containerID[:12], err)
			break
		}
	}
}

// handleHealth is a simple liveness probe for the agent itself.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "ok")
}

// updateHealth is called by the HealthChecker to record a probe result.
// Writes directly into the container instance so the next heartbeat carries it.
func (s *Server) updateHealth(containerID string, status kl.HealthStatus, failures int) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.containers[containerID]
	if !ok {
		return // container was removed between probe firing and this callback
	}
	inst.Health = status
	inst.HealthOK = status == kl.HealthHealthy
	inst.HealthFailures = failures
	inst.LastHealthCheck = &now
}

// syncContainerStates polls Docker and updates our local container map.
// Called before building each heartbeat payload.
func (s *Server) syncContainerStates(ctx context.Context) {
	s.mu.RLock()
	ids := make([]string, 0, len(s.containers))
	for id := range s.containers {
		ids = append(ids, id)
	}
	s.mu.RUnlock()

	for _, id := range ids {
		state, exitCode, err := s.runner.Status(ctx, id)
		if err != nil {
			s.mu.Lock()
			if inst, ok := s.containers[id]; ok {
				inst.State = kl.ContainerUnknown
			}
			s.mu.Unlock()
			continue
		}

		// Fetch the container's IP so service discovery works without a separate call
		ip, _ := s.runner.GetContainerIP(ctx, id)

		s.mu.Lock()
		if inst, ok := s.containers[id]; ok {
			inst.State = state
			inst.ExitCode = exitCode
			if ip != "" {
				inst.IP = ip
			}
		}
		s.mu.Unlock()
	}
}

// registerAndHeartbeat registers with the scheduler once, then sends heartbeats
// on a fixed interval forever. If the scheduler is unreachable at startup,
// it retries every 5 seconds until it succeeds.
func (s *Server) registerAndHeartbeat() {
	// ":8081" is a valid listen address but not a routable one — the scheduler
	// needs a host it can actually dial. Default to 127.0.0.1 when the listen
	// address has no host (single-machine setup). Override KL_ADVERTISE_ADDR
	// for multi-host deployments.
	advertise := s.listenAddr
	if strings.HasPrefix(advertise, ":") {
		advertise = "127.0.0.1" + advertise
	}
	regPayload := kl.RegisterRequest{
		NodeID:  s.nodeID,
		Address: advertise,
		// CPUCores and MemoryMB could be read from /proc/cpuinfo and /proc/meminfo
		CPUCores: 4,
		MemoryMB: 8192,
	}

	for {
		if err := s.postJSON(s.schedulerAddr+"/register", regPayload); err != nil {
			log.Printf("registration failed, retrying in 5s: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}
		log.Printf("registered with scheduler at %s", s.schedulerAddr)
		break
	}
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		s.syncContainerStates(context.Background())

		s.mu.RLock()
		instances := make([]kl.ContainerInstance, 0, len(s.containers))
		for _, inst := range s.containers {
			instances = append(instances, *inst)
		}
		s.mu.RUnlock()

		hb := kl.HeartbeatRequest{
			NodeID:     s.nodeID,
			Containers: instances,
		}
		if err := s.postJSON(s.schedulerAddr+"/heartbeat", hb); err != nil {
			if err == errReregister {
				log.Printf("scheduler doesn't know us — re-registering")
				if rerr := s.postJSON(s.schedulerAddr+"/register", regPayload); rerr != nil {
					log.Printf("re-registration failed: %v", rerr)
				}
			} else {
				log.Printf("heartbeat failed: %v", err)
			}
		}
	}
}

// postJSON POSTs a JSON payload. Returns a sentinelReregister error when the
// scheduler responds 422 (unknown node), so the heartbeat loop can re-register.
var errReregister = fmt.Errorf("scheduler asked us to re-register")

func (s *Server) postJSON(url string, payload any) error {
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(json.NewEncoder(pw).Encode(payload))
	}()

	resp, err := http.Post(url, "application/json", pr)
	if err != nil {
		return err
	}
	resp.Body.Close()

	if resp.StatusCode == http.StatusUnprocessableEntity {
		return errReregister
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}
	return nil
}
