package types

import "time"

// NodeStatus represents the health of a worker node.
type NodeStatus string

const (
	NodeAlive   NodeStatus = "alive"
	NodeDead    NodeStatus = "dead"
	NodeUnknown NodeStatus = "unknown"
)

// RestartPolicy controls what the scheduler does when a container exits.
type RestartPolicy string

const (
	RestartAlways    RestartPolicy = "Always"
	RestartOnFailure RestartPolicy = "OnFailure"
	RestartNever     RestartPolicy = "Never"
)

// ContainerState is the observed state of a running container.
type ContainerState string

const (
	ContainerRunning ContainerState = "running"
	ContainerStopped ContainerState = "stopped"
	ContainerExited  ContainerState = "exited"
	ContainerUnknown ContainerState = "unknown"
)

// Node represents a worker node registered with the scheduler.
type Node struct {
	ID            string     `json:"id"`
	Address       string     `json:"address"` // host:port of the agent HTTP server
	Status        NodeStatus `json:"status"`
	LastHeartbeat time.Time  `json:"last_heartbeat"`
	CPUCores      int        `json:"cpu_cores"`
	MemoryMB      int        `json:"memory_mb"`
}

// WorkloadSpec is the desired state submitted by the user.
// This is what you want — not what's running.
type WorkloadSpec struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Image         string            `json:"image"`
	Replicas      int               `json:"replicas"`
	Env           map[string]string `json:"env,omitempty"`
	Ports         []PortMapping     `json:"ports,omitempty"`
	RestartPolicy RestartPolicy     `json:"restart_policy"`
	HealthCheck   *HealthCheckSpec  `json:"health_check,omitempty"`
}

// PortMapping maps a host port to a container port.
type PortMapping struct {
	HostPort      int    `json:"host_port"`
	ContainerPort int    `json:"container_port"`
	Protocol      string `json:"protocol"` // "tcp" or "udp"
}

// HealthCheckSpec defines how to probe a container.
type HealthCheckSpec struct {
	Path             string `json:"path"` // HTTP GET path, e.g. "/health"
	Port             int    `json:"port"`
	IntervalSeconds  int    `json:"interval_seconds"`
	TimeoutSeconds   int    `json:"timeout_seconds"`
	FailureThreshold int    `json:"failure_threshold"` // consecutive failures before unhealthy
}

// HealthStatus is the current health probe result for a container.
type HealthStatus string

const (
	HealthUnknown   HealthStatus = "unknown"   // not yet probed
	HealthHealthy   HealthStatus = "healthy"   // last probe succeeded
	HealthUnhealthy HealthStatus = "unhealthy" // consecutive failures >= threshold
	HealthStarting  HealthStatus = "starting"  // within initial grace period
)

// ContainerInstance is the actual state of one running container.
// This is what IS — compared against WorkloadSpec to find drift.
type ContainerInstance struct {
	ID         string         `json:"id"`          // Docker container ID
	WorkloadID string         `json:"workload_id"` // which WorkloadSpec this belongs to
	NodeID     string         `json:"node_id"`
	Image      string         `json:"image"`
	State      ContainerState `json:"state"`
	ExitCode   int            `json:"exit_code"`
	StartedAt  time.Time      `json:"started_at"`
	FinishedAt *time.Time     `json:"finished_at,omitempty"`
	IP         string         `json:"ip,omitempty"` // Docker bridge IP, populated by agent

	// Health probe fields — populated by the agent's health checker
	HealthOK        bool         `json:"health_ok"`
	Health          HealthStatus `json:"health"`
	HealthFailures  int          `json:"health_failures"`
	RestartCount    int          `json:"restart_count"`
	LastHealthCheck *time.Time   `json:"last_health_check,omitempty"`

	Ports []PortMapping `json:"ports,omitempty"`
}

// RunRequest is sent from the scheduler to an agent to start a container.
type RunRequest struct {
	WorkloadID  string            `json:"workload_id"`
	Image       string            `json:"image"`
	Name        string            `json:"name"`
	Env         map[string]string `json:"env,omitempty"`
	Ports       []PortMapping     `json:"ports,omitempty"`
	HealthCheck *HealthCheckSpec  `json:"health_check,omitempty"` // nil = no probing
}

// RunResponse is what the agent returns after starting a container.
type RunResponse struct {
	ContainerID string `json:"container_id"`
	Error       string `json:"error,omitempty"`
}

// StatusResponse is the agent's report of a single container's state.
type StatusResponse struct {
	ContainerID string         `json:"container_id"`
	State       ContainerState `json:"state"`
	ExitCode    int            `json:"exit_code"`
	Error       string         `json:"error,omitempty"`
}

// HeartbeatRequest is sent by agents to the scheduler periodically.
type HeartbeatRequest struct {
	NodeID     string              `json:"node_id"`
	Containers []ContainerInstance `json:"containers"` // full snapshot of what's running
}

// ServiceEndpoint is one live instance of a workload, as seen by service discovery.
// Derived from ContainerInstance — not a separate registration step.
type ServiceEndpoint struct {
	ContainerID  string        `json:"container_id"`
	WorkloadID   string        `json:"workload_id"`
	WorkloadName string        `json:"workload_name"`
	NodeID       string        `json:"node_id"`
	NodeAddress  string        `json:"node_address"` // host part of the agent address
	IP           string        `json:"ip"`           // container's internal Docker IP
	Ports        []PortMapping `json:"ports,omitempty"`
	Health       HealthStatus  `json:"health"`
	HealthOK     bool          `json:"health_ok"`
	StartedAt    time.Time     `json:"started_at"`
}

// RolloutStatus tracks the lifecycle of a rolling update.
type RolloutStatus string

const (
	RolloutPending    RolloutStatus = "pending"
	RolloutRunning    RolloutStatus = "running"
	RolloutDone       RolloutStatus = "done"
	RolloutFailed     RolloutStatus = "failed"
	RolloutRolledBack RolloutStatus = "rolled_back"
	RolloutAborted    RolloutStatus = "aborted"
)

// RolloutSpec describes a requested image update.
type RolloutSpec struct {
	WorkloadID string `json:"workload_id"`
	NewImage   string `json:"new_image"`
	// MaxUnavailable is the most old replicas that can be stopped before
	// a new one is confirmed healthy. 0 means zero-downtime (start first, stop after).
	MaxUnavailable int `json:"max_unavailable"`
	// MaxSurge is how many extra containers above desired can run during the update.
	MaxSurge int `json:"max_surge"`
}

// RolloutState is the live tracking record for an in-progress rollout.
type RolloutState struct {
	ID              string        `json:"id"`
	WorkloadID      string        `json:"workload_id"`
	OldImage        string        `json:"old_image"`
	NewImage        string        `json:"new_image"`
	Status          RolloutStatus `json:"status"`
	DesiredReplicas int           `json:"desired_replicas"`
	UpdatedReplicas int           `json:"updated_replicas"` // running new image, health_ok
	OldReplicas     int           `json:"old_replicas"`     // still running old image
	Message         string        `json:"message,omitempty"`
	StartedAt       time.Time     `json:"started_at"`
	FinishedAt      *time.Time    `json:"finished_at,omitempty"`
	// Wave tracks which step of the update we're on (0-indexed)
	Wave int `json:"wave"`
}

// RegisterRequest is sent once on agent startup.
type RegisterRequest struct {
	NodeID   string `json:"node_id"`
	Address  string `json:"address"`
	CPUCores int    `json:"cpu_cores"`
	MemoryMB int    `json:"memory_mb"`
}
