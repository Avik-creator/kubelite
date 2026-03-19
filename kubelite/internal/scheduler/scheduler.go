package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	kl "github.com/Avik-creator/pkg/types"
)

// Scheduler is the central control plane.  It owns the node registry, the
// state store, and the rollout controller, and drives the reconciliation loop.
type Scheduler struct {
	registry *NodeRegistry
	state    *StateStore
	rollouts *RolloutController

	httpClient *http.Client

	// Round-robin counter for node selection (accessed via atomic).
	rrIdx uint64
}

// New creates a fully wired Scheduler.
func New() *Scheduler {
	s := &Scheduler{
		registry:   newNodeRegistry(),
		state:      newStateStore(),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
	s.rollouts = newRolloutController(s)
	return s
}

// Start launches all background loops.  It is non-blocking; call it before
// starting the HTTP server.
func (s *Scheduler) Start(ctx context.Context) {
	go s.deadNodeLoop(ctx)
	go s.reconcileLoop(ctx)
	go s.rollouts.loop(ctx)
}

// ─── background loops ─────────────────────────────────────────────────────────

func (s *Scheduler) deadNodeLoop(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.registry.CheckDeadNodes()
		}
	}
}

func (s *Scheduler) reconcileLoop(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.reconcile(ctx)
		}
	}
}

// reconcile compares desired vs actual state for every workload and acts on
// any drift it finds.
func (s *Scheduler) reconcile(ctx context.Context) {
	for _, spec := range s.state.AllWorkloads() {
		s.reconcileWorkload(ctx, spec)
	}
}

func (s *Scheduler) reconcileWorkload(ctx context.Context, spec kl.WorkloadSpec) {
	effective := s.state.EffectiveCount(spec.ID) // running + pending
	desired := spec.Replicas

	if effective < desired {
		toStart := desired - effective
		log.Printf("[reconcile] %s: %d/%d — starting %d", spec.Name, effective, desired, toStart)
		for i := 0; i < toStart; i++ {
			if err := s.startReplica(ctx, spec, spec.Image); err != nil {
				log.Printf("[reconcile] start replica for %s: %v", spec.Name, err)
			}
		}
		return
	}

	// Check exited containers and apply restart policy
	for _, inst := range s.state.InstancesFor(spec.ID) {
		if inst.State != kl.ContainerExited && inst.State != kl.ContainerStopped {
			continue
		}
		switch spec.RestartPolicy {
		case kl.RestartAlways:
			log.Printf("[reconcile] %s: restarting exited container %s", spec.Name, inst.ID[:12])
			s.restartInstance(ctx, spec, inst)
		case kl.RestartOnFailure:
			if inst.ExitCode != 0 {
				log.Printf("[reconcile] %s: restarting failed container %s (exit %d)",
					spec.Name, inst.ID[:12], inst.ExitCode)
				s.restartInstance(ctx, spec, inst)
			}
		}
	}

	// Scale down if we are over-provisioned
	if effective > desired {
		running := s.state.RunningInstancesFor(spec.ID)
		excess := len(running) - desired
		for i := 0; i < excess && i < len(running); i++ {
			s.stopInstance(ctx, running[i])
		}
	}
}

// ─── core operations ──────────────────────────────────────────────────────────

// Deploy upserts a workload and immediately reconciles it.
func (s *Scheduler) Deploy(ctx context.Context, spec kl.WorkloadSpec) error {
	s.state.UpsertWorkload(spec)
	s.reconcileWorkload(ctx, spec)
	return nil
}

// Scale adjusts the replica count and reconciles immediately.
func (s *Scheduler) Scale(ctx context.Context, workloadID string, replicas int) error {
	spec, ok := s.state.GetWorkload(workloadID)
	if !ok {
		return fmt.Errorf("workload %s not found", workloadID)
	}
	spec.Replicas = replicas
	s.state.UpsertWorkload(spec)
	s.reconcileWorkload(ctx, spec)
	return nil
}

// Delete stops every running container for a workload and removes it.
func (s *Scheduler) Delete(ctx context.Context, workloadID string) error {
	for _, inst := range s.state.RunningInstancesFor(workloadID) {
		s.stopInstance(ctx, inst)
	}
	s.state.DeleteWorkload(workloadID)
	return nil
}

// ─── agent interactions ───────────────────────────────────────────────────────

// startReplica picks a node via round-robin and instructs its agent to run
// a new container for the given workload.
func (s *Scheduler) startReplica(ctx context.Context, spec kl.WorkloadSpec, image string) error {
	nodes := s.registry.AliveNodes()
	if len(nodes) == 0 {
		return fmt.Errorf("no alive nodes")
	}
	idx := atomic.AddUint64(&s.rrIdx, 1) % uint64(len(nodes))
	node := nodes[idx]

	req := kl.RunRequest{
		WorkloadID:  spec.ID,
		Image:       image,
		Name:        fmt.Sprintf("%s-%d", spec.Name, time.Now().UnixMilli()),
		Env:         spec.Env,
		Ports:       autoAssignHostPorts(spec.Ports, spec.Replicas),
		HealthCheck: spec.HealthCheck,
	}
	var resp kl.RunResponse
	if err := s.agentPost(ctx, node.Address, "/run", req, &resp); err != nil {
		return fmt.Errorf("node %s: %w", node.ID, err)
	}
	if resp.Error != "" {
		return fmt.Errorf("node %s: %s", node.ID, resp.Error)
	}
	// Mark as pending so the reconciler doesn't immediately try to start another
	s.state.MarkPending(spec.ID, resp.ContainerID)
	log.Printf("[scheduler] started %s on %s (container %s)", spec.Name, node.ID, resp.ContainerID[:12])
	return nil
}

// stopInstance tells the owning agent to stop and remove a container.
func (s *Scheduler) stopInstance(ctx context.Context, inst kl.ContainerInstance) {
	node, ok := s.registry.GetNode(inst.NodeID)
	if !ok {
		return
	}
	url := fmt.Sprintf("http://%s/stop/%s", node.Address, inst.ID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		log.Printf("[scheduler] stop %s on %s: %v", inst.ID[:12], inst.NodeID, err)
		return
	}
	resp.Body.Close()
	s.state.RemoveInstance(inst.ID)
}

// restartInstance stops a container then starts a fresh replica in its place.
func (s *Scheduler) restartInstance(ctx context.Context, spec kl.WorkloadSpec, inst kl.ContainerInstance) {
	s.stopInstance(ctx, inst)
	if err := s.startReplica(ctx, spec, spec.Image); err != nil {
		log.Printf("[scheduler] restart for %s: %v", spec.Name, err)
	}
}

// Discover returns live service endpoints for a workload identified by name.
func (s *Scheduler) Discover(workloadName string) []kl.ServiceEndpoint {
	// Find matching workload spec
	var spec kl.WorkloadSpec
	found := false
	for _, w := range s.state.AllWorkloads() {
		if w.Name == workloadName {
			spec = w
			found = true
			break
		}
	}
	if !found {
		return nil
	}

	instances := s.state.RunningInstancesFor(spec.ID)
	out := make([]kl.ServiceEndpoint, 0, len(instances))
	for _, inst := range instances {
		node, ok := s.registry.GetNode(inst.NodeID)
		var nodeHost string
		if ok {
			// Extract host portion of "host:port"
			h := node.Address
			for i, c := range h {
				if c == ':' {
					h = h[:i]
					break
				}
			}
			nodeHost = h
		}
		out = append(out, kl.ServiceEndpoint{
			ContainerID:  inst.ID,
			WorkloadID:   inst.WorkloadID,
			WorkloadName: spec.Name,
			NodeID:       inst.NodeID,
			NodeAddress:  nodeHost,
			IP:           inst.IP,
			Ports:        inst.Ports,
			Health:       inst.Health,
			HealthOK:     inst.HealthOK,
			StartedAt:    inst.StartedAt,
		})
	}
	return out
}

// agentPost sends a JSON POST to an agent at http://{addr}{path} and decodes
// the JSON response body into out (if out is non-nil).
func (s *Scheduler) agentPost(ctx context.Context, addr, path string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+addr+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// autoAssignHostPorts returns port mappings safe to use across multiple replicas.
//
// When replicas == 1 the mappings are returned unchanged — the single replica
// gets exactly the host ports the user specified.
//
// When replicas > 1, host ports are zeroed out so Docker picks a unique
// available port for each container.  This mirrors Kubernetes behaviour: you
// don't statically bind the same host port on multiple replicas of a workload;
// you let the platform assign ports and use service-discovery to find them.
//
// Example: spec has [{HostPort:3000, ContainerPort:80}] with replicas=3.
//   - Replica 1 → Docker auto-assigns e.g. 49312:80
//   - Replica 2 → Docker auto-assigns e.g. 49313:80
//   - Replica 3 → Docker auto-assigns e.g. 49314:80
//   Run `kl discover <name>` to see the actual assigned ports.
func autoAssignHostPorts(ports []kl.PortMapping, replicas int) []kl.PortMapping {
	if replicas <= 1 || len(ports) == 0 {
		return ports
	}
	out := make([]kl.PortMapping, len(ports))
	for i, p := range ports {
		out[i] = kl.PortMapping{
			HostPort:      0, // 0 → Docker picks an available ephemeral port
			ContainerPort: p.ContainerPort,
			Protocol:      p.Protocol,
		}
	}
	return out
}
