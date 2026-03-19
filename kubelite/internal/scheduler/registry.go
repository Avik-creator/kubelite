package scheduler

import (
	"fmt"
	"log"
	"sync"
	"time"

	kl "github.com/Avik-creator/pkg/types"
)

const (
	heartbeatInterval = 3 * time.Second
	deadThreshold     = 3 * heartbeatInterval // 9 s without a beat → DEAD
)

type nodeEntry struct {
	node          kl.Node
	lastHeartbeat time.Time
}

// NodeRegistry tracks worker nodes that have registered with the scheduler.
type NodeRegistry struct {
	mu    sync.RWMutex
	nodes map[string]*nodeEntry // nodeID → entry
}

func newNodeRegistry() *NodeRegistry {
	return &NodeRegistry{nodes: make(map[string]*nodeEntry)}
}

// Register adds or refreshes a node.
func (r *NodeRegistry) Register(req kl.RegisterRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	r.nodes[req.NodeID] = &nodeEntry{
		node: kl.Node{
			ID:            req.NodeID,
			Address:       req.Address,
			Status:        kl.NodeAlive,
			LastHeartbeat: now,
			CPUCores:      req.CPUCores,
			MemoryMB:      req.MemoryMB,
		},
		lastHeartbeat: now,
	}
	log.Printf("[registry] node %s registered at %s", req.NodeID, req.Address)
}

// Heartbeat refreshes the last-seen timestamp.
// Returns an error when the node is unknown — the agent should re-register.
func (r *NodeRegistry) Heartbeat(nodeID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.nodes[nodeID]
	if !ok {
		return fmt.Errorf("unknown node %s", nodeID)
	}
	now := time.Now()
	e.lastHeartbeat = now
	e.node.Status = kl.NodeAlive
	e.node.LastHeartbeat = now
	return nil
}

// AliveNodes returns a consistent snapshot of all nodes currently marked alive.
func (r *NodeRegistry) AliveNodes() []kl.Node {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]kl.Node, 0, len(r.nodes))
	for _, e := range r.nodes {
		if e.node.Status == kl.NodeAlive {
			out = append(out, e.node)
		}
	}
	return out
}

// AllNodes returns every node regardless of status.
func (r *NodeRegistry) AllNodes() []kl.Node {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]kl.Node, 0, len(r.nodes))
	for _, e := range r.nodes {
		out = append(out, e.node)
	}
	return out
}

// GetNode returns a single node by ID.
func (r *NodeRegistry) GetNode(nodeID string) (kl.Node, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.nodes[nodeID]
	if !ok {
		return kl.Node{}, false
	}
	return e.node, true
}

// CheckDeadNodes marks any node that has missed deadThreshold of beats as dead.
// Designed to be called from a background goroutine.
func (r *NodeRegistry) CheckDeadNodes() {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for _, e := range r.nodes {
		if e.node.Status == kl.NodeAlive && now.Sub(e.lastHeartbeat) > deadThreshold {
			log.Printf("[registry] node %s marked DEAD (silent for %s)",
				e.node.ID, now.Sub(e.lastHeartbeat).Round(time.Second))
			e.node.Status = kl.NodeDead
		}
	}
}
