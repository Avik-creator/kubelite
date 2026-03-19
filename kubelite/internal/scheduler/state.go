package scheduler

import (
	"sync"

	kl "github.com/Avik-creator/pkg/types"
)

// workloadEntry holds a WorkloadSpec plus a set of container IDs we have
// asked agents to start but that have not yet appeared in a heartbeat.
type workloadEntry struct {
	spec    kl.WorkloadSpec
	pending map[string]struct{} // container IDs started but not yet confirmed
}

// StateStore is the in-memory source of truth for desired and actual state.
//
//   - desired state  →  workloads map (what the user submitted)
//   - actual state   →  instances map (what agents report via heartbeats)
type StateStore struct {
	mu        sync.RWMutex
	workloads map[string]*workloadEntry       // workloadID → entry
	instances map[string]*kl.ContainerInstance // containerID → instance
}

func newStateStore() *StateStore {
	return &StateStore{
		workloads: make(map[string]*workloadEntry),
		instances: make(map[string]*kl.ContainerInstance),
	}
}

// UpsertWorkload creates or replaces a workload spec.
func (s *StateStore) UpsertWorkload(spec kl.WorkloadSpec) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.workloads[spec.ID]; ok {
		e.spec = spec // preserve pending set
	} else {
		s.workloads[spec.ID] = &workloadEntry{spec: spec, pending: make(map[string]struct{})}
	}
}

// GetWorkload returns the spec for a workload.
func (s *StateStore) GetWorkload(id string) (kl.WorkloadSpec, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.workloads[id]
	if !ok {
		return kl.WorkloadSpec{}, false
	}
	return e.spec, true
}

// AllWorkloads returns a snapshot of every known workload spec.
func (s *StateStore) AllWorkloads() []kl.WorkloadSpec {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]kl.WorkloadSpec, 0, len(s.workloads))
	for _, e := range s.workloads {
		out = append(out, e.spec)
	}
	return out
}

// DeleteWorkload removes the workload entry entirely.
func (s *StateStore) DeleteWorkload(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.workloads, id)
}

// MarkPending records a container ID as started-but-not-yet-heartbeated.
func (s *StateStore) MarkPending(workloadID, containerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.workloads[workloadID]; ok {
		e.pending[containerID] = struct{}{}
	}
}

// SyncHeartbeat merges the container list reported by one node into the
// instance map.  Containers on that node that are no longer reported are
// removed (the agent has already cleaned them up).
func (s *StateStore) SyncHeartbeat(nodeID string, containers []kl.ContainerInstance) {
	s.mu.Lock()
	defer s.mu.Unlock()

	reported := make(map[string]struct{}, len(containers))
	for i := range containers {
		c := containers[i]
		c.NodeID = nodeID
		s.instances[c.ID] = &c
		reported[c.ID] = struct{}{}

		// Promote out of pending once confirmed by a heartbeat
		if e, ok := s.workloads[c.WorkloadID]; ok {
			delete(e.pending, c.ID)
		}
	}

	// Drop stale entries for this node
	for id, inst := range s.instances {
		if inst.NodeID == nodeID {
			if _, ok := reported[id]; !ok {
				// Also remove from pending if it vanished
				if e, ok2 := s.workloads[inst.WorkloadID]; ok2 {
					delete(e.pending, id)
				}
				delete(s.instances, id)
			}
		}
	}
}

// RemoveInstance deletes a single container from the instance map.
func (s *StateStore) RemoveInstance(containerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if inst, ok := s.instances[containerID]; ok {
		if e, ok2 := s.workloads[inst.WorkloadID]; ok2 {
			delete(e.pending, containerID)
		}
	}
	delete(s.instances, containerID)
}

// EffectiveCount returns the number of running + pending containers for a
// workload.  This is what the reconciler uses to avoid over-provisioning in
// the gap between starting a container and the first confirming heartbeat.
func (s *StateStore) EffectiveCount(workloadID string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.workloads[workloadID]
	if !ok {
		return 0
	}
	count := len(e.pending)
	for _, inst := range s.instances {
		if inst.WorkloadID == workloadID && inst.State == kl.ContainerRunning {
			count++
		}
	}
	return count
}

// InstancesFor returns all container instances belonging to a workload.
func (s *StateStore) InstancesFor(workloadID string) []kl.ContainerInstance {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []kl.ContainerInstance
	for _, inst := range s.instances {
		if inst.WorkloadID == workloadID {
			out = append(out, *inst)
		}
	}
	return out
}

// RunningInstancesFor returns only running containers for a workload.
func (s *StateStore) RunningInstancesFor(workloadID string) []kl.ContainerInstance {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []kl.ContainerInstance
	for _, inst := range s.instances {
		if inst.WorkloadID == workloadID && inst.State == kl.ContainerRunning {
			out = append(out, *inst)
		}
	}
	return out
}

// AllInstances returns a snapshot of every known container instance.
func (s *StateStore) AllInstances() []kl.ContainerInstance {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]kl.ContainerInstance, 0, len(s.instances))
	for _, inst := range s.instances {
		out = append(out, *inst)
	}
	return out
}

// GetInstance returns a single container instance by ID.
func (s *StateStore) GetInstance(containerID string) (kl.ContainerInstance, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	inst, ok := s.instances[containerID]
	if !ok {
		return kl.ContainerInstance{}, false
	}
	return *inst, true
}
