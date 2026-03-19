package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	kl "github.com/Avik-creator/pkg/types"
)

// Server wraps the Scheduler and exposes it over HTTP.
type Server struct {
	sched *Scheduler
	addr  string
}

// NewServer creates an HTTP server bound to addr.
func NewServer(addr string, sched *Scheduler) *Server {
	return &Server{sched: sched, addr: addr}
}

// Start wires the mux, starts background loops, and blocks on ListenAndServe.
func (srv *Server) Start(ctx context.Context) error {
	srv.sched.Start(ctx)

	mux := http.NewServeMux()

	// Agent-facing
	mux.HandleFunc("POST /register", srv.handleRegister)
	mux.HandleFunc("POST /heartbeat", srv.handleHeartbeat)

	// User-facing
	mux.HandleFunc("POST /deploy", srv.handleDeploy)
	mux.HandleFunc("GET /workloads", srv.handleListWorkloads)
	mux.HandleFunc("GET /workloads/{id}", srv.handleGetWorkload)
	mux.HandleFunc("DELETE /workloads/{id}", srv.handleDeleteWorkload)
	mux.HandleFunc("PUT /workloads/{id}/scale", srv.handleScale)

	mux.HandleFunc("GET /nodes", srv.handleNodes)
	mux.HandleFunc("GET /discover/{name}", srv.handleDiscover)

	mux.HandleFunc("POST /rollout", srv.handleStartRollout)
	mux.HandleFunc("GET /rollout/{workloadID}", srv.handleGetRollout)

	mux.HandleFunc("GET /logs/{containerID}", srv.handleLogs)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	log.Printf("[scheduler] listening on %s", srv.addr)
	return http.ListenAndServe(srv.addr, mux)
}

// ─── agent-facing handlers ────────────────────────────────────────────────────

func (srv *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req kl.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	srv.sched.registry.Register(req)
	w.WriteHeader(http.StatusOK)
}

func (srv *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req kl.HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := srv.sched.registry.Heartbeat(req.NodeID); err != nil {
		// 422 tells the agent to re-register
		http.Error(w, "unknown node — please re-register", http.StatusUnprocessableEntity)
		return
	}
	srv.sched.state.SyncHeartbeat(req.NodeID, req.Containers)
	w.WriteHeader(http.StatusOK)
}

// ─── workload handlers ────────────────────────────────────────────────────────

func (srv *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	var spec kl.WorkloadSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Assign an ID if the caller didn't provide one
	if spec.ID == "" {
		spec.ID = newID()
	}
	if spec.Replicas <= 0 {
		spec.Replicas = 1
	}
	if spec.RestartPolicy == "" {
		spec.RestartPolicy = kl.RestartAlways
	}
	if err := srv.sched.Deploy(r.Context(), spec); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"id": spec.ID})
}

type workloadDetailResponse struct {
	Spec      kl.WorkloadSpec         `json:"spec"`
	Instances []kl.ContainerInstance  `json:"instances"`
}

func (srv *Server) handleListWorkloads(w http.ResponseWriter, r *http.Request) {
	workloads := srv.sched.state.AllWorkloads()
	type row struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Image    string `json:"image"`
		Replicas int    `json:"replicas"`
		Running  int    `json:"running"`
	}
	out := make([]row, 0, len(workloads))
	for _, s := range workloads {
		out = append(out, row{
			ID:       s.ID,
			Name:     s.Name,
			Image:    s.Image,
			Replicas: s.Replicas,
			Running:  len(srv.sched.state.RunningInstancesFor(s.ID)),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (srv *Server) handleGetWorkload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	spec, ok := srv.sched.state.GetWorkload(id)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(workloadDetailResponse{
		Spec:      spec,
		Instances: srv.sched.state.InstancesFor(id),
	})
}

func (srv *Server) handleDeleteWorkload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := srv.sched.Delete(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (srv *Server) handleScale(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Replicas int `json:"replicas"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := srv.sched.Scale(r.Context(), id, body.Replicas); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ─── nodes & discovery ────────────────────────────────────────────────────────

func (srv *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(srv.sched.registry.AllNodes())
}

func (srv *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	endpoints := srv.sched.Discover(name)
	if endpoints == nil {
		endpoints = []kl.ServiceEndpoint{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(endpoints)
}

// ─── rollout handlers ─────────────────────────────────────────────────────────

func (srv *Server) handleStartRollout(w http.ResponseWriter, r *http.Request) {
	var spec kl.RolloutSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	state, err := srv.sched.rollouts.Start(spec.WorkloadID, spec.NewImage, spec.MaxUnavailable, spec.MaxSurge)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(state)
}

func (srv *Server) handleGetRollout(w http.ResponseWriter, r *http.Request) {
	workloadID := r.PathValue("workloadID")
	state, ok := srv.sched.rollouts.Get(workloadID)
	if !ok {
		http.Error(w, "no rollout found for workload "+workloadID, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(state)
}

// ─── logs proxy ───────────────────────────────────────────────────────────────

// handleLogs proxies GET /logs/{containerID} to the owning agent, so callers
// never need to know which node a container lives on.
func (srv *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	containerID := r.PathValue("containerID")
	inst, ok := srv.sched.state.GetInstance(containerID)
	if !ok {
		http.Error(w, "container not found: "+containerID, http.StatusNotFound)
		return
	}
	node, ok := srv.sched.registry.GetNode(inst.NodeID)
	if !ok {
		http.Error(w, "node not found: "+inst.NodeID, http.StatusNotFound)
		return
	}

	// Forward query params (tail, follow) verbatim
	agentURL := fmt.Sprintf("http://%s/logs/%s?%s", node.Address, containerID, r.URL.RawQuery)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, agentURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp, err := srv.sched.httpClient.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(resp.StatusCode)

	flusher, canFlush := w.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			if canFlush {
				flusher.Flush()
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
