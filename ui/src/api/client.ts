// All types mirror the Go domain types in pkg/types/types.go

export type NodeStatus = "alive" | "dead" | "unknown";
export type ContainerState = "running" | "stopped" | "exited" | "unknown";
export type HealthStatus = "unknown" | "starting" | "healthy" | "unhealthy";
export type RestartPolicy = "Always" | "OnFailure" | "Never";
export type RolloutStatus =
  | "pending"
  | "running"
  | "done"
  | "failed"
  | "rolled_back"
  | "aborted";

export interface Node {
  id: string;
  address: string;
  status: NodeStatus;
  last_heartbeat: string;
  cpu_cores: number;
  memory_mb: number;
}

export interface PortMapping {
  host_port: number;
  container_port: number;
  protocol: string;
}

export interface HealthCheckSpec {
  path: string;
  port: number;
  interval_seconds: number;
  timeout_seconds: number;
  failure_threshold: number;
}

export interface WorkloadSpec {
  id?: string;
  name: string;
  image: string;
  replicas: number;
  env?: Record<string, string>;
  ports?: PortMapping[];
  restart_policy: RestartPolicy;
  health_check?: HealthCheckSpec;
}

export interface WorkloadSummary {
  id: string;
  name: string;
  image: string;
  replicas: number;
  running: number;
}

export interface ContainerInstance {
  id: string;
  workload_id: string;
  node_id: string;
  image: string;
  state: ContainerState;
  exit_code: number;
  started_at: string;
  finished_at?: string;
  ip: string;
  health_ok: boolean;
  health: HealthStatus;
  health_failures: number;
  restart_count: number;
  last_health_check?: string;
  ports?: PortMapping[];
}

export interface WorkloadDetail {
  spec: WorkloadSpec & { id: string };
  instances: ContainerInstance[];
}

export interface ServiceEndpoint {
  container_id: string;
  workload_id: string;
  workload_name: string;
  node_id: string;
  node_address: string;
  ip: string;
  ports?: PortMapping[];
  health: HealthStatus;
  health_ok: boolean;
  started_at: string;
}

export interface RolloutState {
  id: string;
  workload_id: string;
  old_image: string;
  new_image: string;
  status: RolloutStatus;
  desired_replicas: number;
  updated_replicas: number;
  old_replicas: number;
  message?: string;
  started_at: string;
  finished_at?: string;
  wave: number;
}

export interface RolloutSpec {
  workload_id: string;
  new_image: string;
  max_unavailable: number;
  max_surge: number;
}

// ─── base ─────────────────────────────────────────────────────────────────────

const BASE = "/api";

async function req<T>(
  method: string,
  path: string,
  body?: unknown
): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    method,
    headers: body ? { "Content-Type": "application/json" } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(`HTTP ${res.status}: ${text.trim()}`);
  }
  if (res.status === 204) return undefined as T;
  return res.json();
}

// ─── nodes ────────────────────────────────────────────────────────────────────

export const getNodes = (): Promise<Node[]> => req("GET", "/nodes");

// ─── workloads ────────────────────────────────────────────────────────────────

export const getWorkloads = (): Promise<WorkloadSummary[]> =>
  req("GET", "/workloads");

export const getWorkload = (id: string): Promise<WorkloadDetail> =>
  req("GET", `/workloads/${id}`);

export const deployWorkload = (
  spec: WorkloadSpec
): Promise<{ id: string }> => req("POST", "/deploy", spec);

export const scaleWorkload = (
  id: string,
  replicas: number
): Promise<void> => req("PUT", `/workloads/${id}/scale`, { replicas });

export const deleteWorkload = (id: string): Promise<void> =>
  req("DELETE", `/workloads/${id}`);

// ─── discovery ────────────────────────────────────────────────────────────────

export const discover = (name: string): Promise<ServiceEndpoint[]> =>
  req("GET", `/discover/${name}`);

// ─── rollout ──────────────────────────────────────────────────────────────────

export const startRollout = (spec: RolloutSpec): Promise<RolloutState> =>
  req("POST", "/rollout", spec);

export const getRollout = (workloadID: string): Promise<RolloutState> =>
  req("GET", `/rollout/${workloadID}`);
