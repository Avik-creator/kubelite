package scheduler

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	kl "github.com/Avik-creator/pkg/types"
)

const (
	healthTimeout    = 60 * time.Second // max wait for a new container to become healthy
	rolloutTickRate  = 5 * time.Second
)

// rolloutEntry tracks an in-progress rolling update for one workload.
type rolloutEntry struct {
	state kl.RolloutState
	spec  kl.WorkloadSpec // spec with the OLD image (so we can find old containers)

	newImage       string
	maxUnavailable int
	maxSurge       int

	// Containers we started with the new image that we're waiting on health for.
	// key = containerID, value = deadline by which it must be healthy.
	waitingNew map[string]time.Time
	// Container IDs still running the old image that we plan to stop.
	oldContainerIDs []string
}

// RolloutController manages all in-progress rolling updates.
type RolloutController struct {
	sched *Scheduler

	mu      sync.Mutex
	entries map[string]*rolloutEntry // workloadID → entry
}

func newRolloutController(s *Scheduler) *RolloutController {
	return &RolloutController{
		sched:   s,
		entries: make(map[string]*rolloutEntry),
	}
}

// Start initiates a rolling update for workloadID.  Returns an error if one
// is already in progress for that workload.
func (rc *RolloutController) Start(workloadID, newImage string, maxUnavailable, maxSurge int) (kl.RolloutState, error) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	if e, ok := rc.entries[workloadID]; ok &&
		(e.state.Status == kl.RolloutPending || e.state.Status == kl.RolloutRunning) {
		return kl.RolloutState{}, fmt.Errorf("rollout already in progress for workload %s", workloadID)
	}

	spec, ok := rc.sched.state.GetWorkload(workloadID)
	if !ok {
		return kl.RolloutState{}, fmt.Errorf("workload %s not found", workloadID)
	}
	if newImage == spec.Image {
		return kl.RolloutState{}, fmt.Errorf("new image is the same as current image")
	}

	if maxUnavailable <= 0 {
		maxUnavailable = 1
	}
	if maxSurge <= 0 {
		maxSurge = 1
	}

	running := rc.sched.state.RunningInstancesFor(workloadID)
	oldIDs := make([]string, 0, len(running))
	for _, inst := range running {
		oldIDs = append(oldIDs, inst.ID)
	}

	now := time.Now()
	state := kl.RolloutState{
		ID:              fmt.Sprintf("rollout-%s-%d", workloadID, now.UnixMilli()),
		WorkloadID:      workloadID,
		OldImage:        spec.Image,
		NewImage:        newImage,
		Status:          kl.RolloutPending,
		DesiredReplicas: spec.Replicas,
		OldReplicas:     len(oldIDs),
		StartedAt:       now,
	}
	entry := &rolloutEntry{
		state:           state,
		spec:            spec,
		newImage:        newImage,
		maxUnavailable:  maxUnavailable,
		maxSurge:        maxSurge,
		waitingNew:      make(map[string]time.Time),
		oldContainerIDs: oldIDs,
	}
	rc.entries[workloadID] = entry
	log.Printf("[rollout] started for workload %s: %s → %s (%d replicas)",
		spec.Name, spec.Image, newImage, spec.Replicas)
	return state, nil
}

// Get returns the current rollout state for a workload.
func (rc *RolloutController) Get(workloadID string) (kl.RolloutState, bool) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	e, ok := rc.entries[workloadID]
	if !ok {
		return kl.RolloutState{}, false
	}
	return e.state, true
}

// loop is the background goroutine that advances every in-progress rollout.
func (rc *RolloutController) loop(ctx context.Context) {
	t := time.NewTicker(rolloutTickRate)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			rc.tickAll(ctx)
		}
	}
}

func (rc *RolloutController) tickAll(ctx context.Context) {
	rc.mu.Lock()
	active := make([]*rolloutEntry, 0)
	for _, e := range rc.entries {
		if e.state.Status == kl.RolloutPending || e.state.Status == kl.RolloutRunning {
			active = append(active, e)
		}
	}
	rc.mu.Unlock()

	for _, e := range active {
		rc.advance(ctx, e)
	}
}

// advance executes one tick of the rollout state machine for a single entry.
//
// State machine:
//
//	pending  → start first wave of new containers
//	running  → poll health; when healthy, stop an old container; repeat
//	done     → update workload spec to new image
//	failed   → optionally rollback
func (rc *RolloutController) advance(ctx context.Context, e *rolloutEntry) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	if e.state.Status == kl.RolloutPending {
		e.state.Status = kl.RolloutRunning
	}

	// ── 1. Check for timed-out new containers ────────────────────────────────
	now := time.Now()
	for cid, deadline := range e.waitingNew {
		if now.After(deadline) {
			log.Printf("[rollout] %s: container %s did not become healthy in time — aborting",
				e.spec.Name, cid[:12])
			rc.abort(ctx, e, false)
			return
		}
		// Check if it turned unhealthy
		inst, ok := rc.sched.state.GetInstance(cid)
		if ok && inst.Health == kl.HealthUnhealthy {
			log.Printf("[rollout] %s: new container %s is unhealthy — aborting",
				e.spec.Name, cid[:12])
			rc.abort(ctx, e, false)
			return
		}
	}

	// ── 2. Promote newly healthy containers and stop one old container each ──
	promoted := 0
	for cid := range e.waitingNew {
		inst, ok := rc.sched.state.GetInstance(cid)
		if !ok {
			continue
		}
		isHealthy := inst.Health == kl.HealthHealthy ||
			(e.spec.HealthCheck == nil && inst.State == kl.ContainerRunning)
		if !isHealthy {
			continue
		}
		delete(e.waitingNew, cid)
		e.state.UpdatedReplicas++
		promoted++

		// Stop one old container
		if len(e.oldContainerIDs) > 0 {
			oldID := e.oldContainerIDs[0]
			e.oldContainerIDs = e.oldContainerIDs[1:]
			if oldInst, ok := rc.sched.state.GetInstance(oldID); ok {
				log.Printf("[rollout] %s: new %s healthy — stopping old %s",
					e.spec.Name, cid[:12], oldID[:12])
				go rc.sched.stopInstance(ctx, oldInst)
				e.state.OldReplicas--
			}
		}
	}

	// ── 3. Check for completion ───────────────────────────────────────────────
	if len(e.oldContainerIDs) == 0 && len(e.waitingNew) == 0 {
		rc.finish(ctx, e)
		return
	}

	// ── 4. Start more new containers up to maxSurge ──────────────────────────
	canStart := e.maxSurge - len(e.waitingNew)
	for canStart > 0 && len(e.oldContainerIDs) > 0 {
		newSpec := e.spec // copy
		newSpec.Image = e.newImage
		if err := rc.sched.startReplica(ctx, newSpec, e.newImage); err != nil {
			log.Printf("[rollout] %s: could not start new replica: %v", e.spec.Name, err)
			break
		}
		// Find the container we just started (it will be the newest pending one)
		pending := rc.latestPending(e.spec.ID)
		if pending != "" {
			e.waitingNew[pending] = time.Now().Add(healthTimeout)
			e.state.Wave++
			log.Printf("[rollout] %s: wave %d — started new container %s",
				e.spec.Name, e.state.Wave, pending[:12])
		}
		canStart--
	}
}

// latestPending returns the most recently marked-pending container for a workload.
// This is a best-effort lookup — it scans instances and picks one we haven't
// registered in waitingNew yet.
func (rc *RolloutController) latestPending(workloadID string) string {
	// The state store's pending set is private, so we just look for a running
	// instance with the new image that we haven't logged yet.  Works because
	// we call this right after startReplica.
	for _, inst := range rc.sched.state.InstancesFor(workloadID) {
		if _, tracked := rc.entries[workloadID].waitingNew[inst.ID]; !tracked {
			if inst.WorkloadID == workloadID {
				return inst.ID
			}
		}
	}
	return ""
}

func (rc *RolloutController) finish(ctx context.Context, e *rolloutEntry) {
	now := time.Now()
	e.state.Status = kl.RolloutDone
	e.state.FinishedAt = &now
	e.state.Message = "rollout complete"
	log.Printf("[rollout] %s: DONE — all replicas now on %s", e.spec.Name, e.newImage)

	// Update the workload spec so future reconciler runs use the new image
	updated := e.spec
	updated.Image = e.newImage
	rc.sched.state.UpsertWorkload(updated)
}

// abort stops all containers we started with the new image and optionally
// restores the old desired count by starting old-image containers.
func (rc *RolloutController) abort(ctx context.Context, e *rolloutEntry, rollback bool) {
	now := time.Now()
	e.state.Status = kl.RolloutFailed
	e.state.FinishedAt = &now

	// Stop every new container we started
	for cid := range e.waitingNew {
		if inst, ok := rc.sched.state.GetInstance(cid); ok {
			go rc.sched.stopInstance(ctx, inst)
		}
	}
	e.waitingNew = make(map[string]time.Time)

	if rollback {
		e.state.Status = kl.RolloutRolledBack
		e.state.Message = "rollout failed — rolling back"
		// The reconciler will restore old-image replicas automatically because
		// the workload spec still carries the old image.
		go rc.sched.reconcileWorkload(ctx, e.spec)
	} else {
		e.state.Message = "rollout failed — manual intervention required"
	}

	log.Printf("[rollout] %s: ABORTED (%s)", e.spec.Name, e.state.Message)
}
