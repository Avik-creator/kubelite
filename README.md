# kube-lite

A ground-up implementation of the core ideas behind Kubernetes — written in Go, built phase by phase so every line of code is something you understand and can explain.

```
scheduler (port 8080)          agent (port 8081)
┌─────────────────────┐        ┌──────────────────────┐
│  reconciler loop    │◄──────►│  Docker runner       │
│  node registry      │  HTTP  │  health prober       │
│  rollout controller │        │  heartbeat sender    │
│  service discovery  │        └──────────────────────┘
└─────────────────────┘
         ▲
         │ REST API
    kl CLI / React UI
```

---

## Architecture

### Components

| Binary | Path | Role |
|---|---|---|
| `scheduler` | `cmd/scheduler/` | Central control plane. One per cluster. |
| `agent` | `cmd/agent/` | Worker node daemon. One per machine. |
| `kl` | `cmd/kl/` | CLI client (thin HTTP wrapper). |
| React UI | `ui/` | Dashboard (Vite + React Query). |

### Packages

| Package | What it does |
|---|---|
| `pkg/types` | All shared domain types (`WorkloadSpec`, `ContainerInstance`, etc.) |
| `internal/agent` | Docker SDK wrapper, HTTP health prober, agent HTTP server |
| `internal/scheduler` | Node registry, state store, scheduling, reconciler, rollout controller, HTTP server |

### Request flow — deploying a workload

```
kl deploy --name web --image nginx --replicas 2
  │
  └─► POST /deploy → scheduler
          │
          ├─ upsert WorkloadSpec in StateStore
          └─ reconcileWorkload()
               │
               ├─ AliveNodes() → round-robin pick
               └─ POST /run → agent
                      │
                      ├─ docker pull
                      ├─ docker container create
                      ├─ docker container start
                      └─ StartProbe() (health checker)
```

### Reconciliation loop

Runs every **5 seconds** in the scheduler. For each workload it computes:

```
drift = desired_replicas - (running_instances + pending_instances)
```

- `drift > 0` → start that many new containers  
- `drift < 0` → stop the excess  
- exited containers → apply `RestartPolicy` (`Always` / `OnFailure` / `Never`)

### Heartbeat & dead-node detection

Agents send `POST /heartbeat` every **3 seconds**. Each heartbeat carries a full snapshot of every container running on that node. The scheduler replaces the node's instance state atomically. If **3 consecutive heartbeats are missed (9 s)**, the node is marked `DEAD` and excluded from scheduling.

### Rolling updates

Wave-by-wave strategy:

```
start 1 new container (new image)
  └─ wait for health_ok (max 60 s)
       ├─ healthy → stop 1 old container → next wave
       └─ unhealthy / timeout → abort rollout
```

---

## Quick start

### Prerequisites

- Go ≥ 1.21  
- Docker running locally  
- Node.js ≥ 18 (UI only)

### Build everything

```bash
cd kubelite
make build          # compiles agent, scheduler, kl → bin/
```

### Run locally

```bash
# Terminal 1 — start the scheduler (control plane)
make run-scheduler

# Terminal 2 — start an agent (worker node)
make run-agent

# (or both at once in tmux)
make dev
```

### Deploy a workload

```bash
bin/kl deploy \
  --name nginx-web \
  --image nginx:latest \
  --replicas 2 \
  --port 8080:80

bin/kl workloads
bin/kl status <workload-id>
bin/kl logs <container-id>
```

### Run the dashboard

```bash
cd ui
npm install
npm run dev        # http://localhost:5173
```

---

## Configuration

All configuration is via environment variables — no config files.

### Scheduler

| Variable | Default | Description |
|---|---|---|
| `KL_LISTEN` | `:8080` | Address the scheduler binds to |

### Agent

| Variable | Default | Description |
|---|---|---|
| `KL_LISTEN` | `:8081` | Address the agent HTTP server binds to |
| `KL_SCHEDULER` | `localhost:8080` | Scheduler address (host:port, no scheme) |
| `KL_NODE_ID` | hostname | Unique name for this node |

### CLI

| Variable | Default | Description |
|---|---|---|
| `KL_SERVER` | `http://localhost:8080` | Scheduler address |

---

## CLI reference

```
kl [--server http://host:port] <command> [flags]
```

| Command | Description |
|---|---|
| `nodes` | List all registered worker nodes |
| `deploy` | Deploy (or update) a workload |
| `workloads` | List all workloads with replica counts |
| `status <id>` | Workload detail + running instances |
| `scale <id> --replicas N` | Change desired replica count |
| `delete <id>` | Stop all containers and remove workload |
| `logs <container-id>` | Stream container logs |
| `rollout <id> --image IMAGE` | Start a rolling image update |
| `rollout-status <id>` | Show current rollout progress |
| `discover <name>` | List live service endpoints by workload name |

#### `deploy` flags

```
--name            workload name (required)
--image           container image (required)
--id              workload ID (auto-generated if empty)
--replicas        desired replica count (default 1)
--restart-policy  Always | OnFailure | Never (default Always)
--port            HOST:CONTAINER  (repeatable)
--env             KEY=VALUE       (repeatable)
--health-path     HTTP probe path (e.g. /health)
--health-port     HTTP probe port
```

---

## Scheduler API

All endpoints accept and return JSON.

### Agent-facing

| Method | Path | Body | Description |
|---|---|---|---|
| `POST` | `/register` | `RegisterRequest` | One-time agent registration |
| `POST` | `/heartbeat` | `HeartbeatRequest` | Periodic state sync. Returns `422` if node is unknown → agent re-registers |

### User-facing

| Method | Path | Body | Description |
|---|---|---|---|
| `POST` | `/deploy` | `WorkloadSpec` | Create or update a workload |
| `GET` | `/workloads` | — | List all workloads (summary) |
| `GET` | `/workloads/:id` | — | Workload detail + live instances |
| `DELETE` | `/workloads/:id` | — | Stop all containers + remove |
| `PUT` | `/workloads/:id/scale` | `{"replicas":N}` | Adjust replica count |
| `GET` | `/nodes` | — | All nodes with status |
| `GET` | `/discover/:name` | — | Live endpoints for a workload name |
| `POST` | `/rollout` | `RolloutSpec` | Start a rolling update |
| `GET` | `/rollout/:workloadID` | — | Rollout state |
| `GET` | `/logs/:containerID` | — | Stream logs (proxied to owning agent) |
| `GET` | `/health` | — | Scheduler liveness probe |

### Agent API (direct access)

| Method | Path | Description |
|---|---|---|
| `POST` | `/run` | Start a container |
| `POST` | `/stop/:id` | Stop + remove a container |
| `GET` | `/status/:id` | Container state + exit code |
| `GET` | `/logs/:id` | Streaming stdout/stderr |
| `GET` | `/health` | Agent liveness probe |

---

## Key types

```go
// WorkloadSpec — desired state submitted by the user
type WorkloadSpec struct {
    ID            string
    Name          string
    Image         string
    Replicas      int
    Env           map[string]string
    Ports         []PortMapping
    RestartPolicy RestartPolicy     // Always | OnFailure | Never
    HealthCheck   *HealthCheckSpec
}

// ContainerInstance — actual state reported by an agent
type ContainerInstance struct {
    ID             string
    WorkloadID     string
    NodeID         string
    Image          string
    State          ContainerState  // running | stopped | exited | unknown
    Health         HealthStatus    // unknown | starting | healthy | unhealthy
    IP             string          // Docker bridge IP
    StartedAt      time.Time
}

// RolloutSpec — request a rolling image update
type RolloutSpec struct {
    WorkloadID     string
    NewImage       string
    MaxUnavailable int
    MaxSurge       int
}
```

---

## Make targets

```
make build            build all three binaries → bin/
make agent            build agent only
make scheduler        build scheduler only
make kl               build CLI only
make run-scheduler    start scheduler on :8080
make run-agent        start agent on :8081
make dev              scheduler + agent in a tmux session
make test             go test ./...
make test-race        go test -race ./...
make vet              go vet ./...
make tidy             go mod tidy
make lint             golangci-lint run
make check            vet + tidy + git-diff guard (for CI)
make install          go install all binaries to $GOPATH/bin
make clean            remove bin/
```

---

## Phase roadmap

| Phase | What you build | Core concept learned |
|---|---|---|
| 1 | Agent: POST /run, GET /status | Docker SDK, HTTP API |
| 2 | Scheduler + heartbeat | Distributed state, dead-node detection |
| 3 | Reconcile loop, scheduling | Desired vs actual, bin-packing |
| 4 | Health checks, restart policy | Idempotency, drift handling |
| 5 | Service discovery, log streaming | Name → endpoint resolution |
| 6 | Rolling updates, rollback | Controller pattern |
| 7 | React UI + CLI polish | Observability |
