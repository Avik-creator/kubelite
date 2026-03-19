import type { ContainerState, HealthStatus, NodeStatus, RolloutStatus } from "../api/client";

type AnyStatus = NodeStatus | ContainerState | HealthStatus | RolloutStatus;

const COLOR: Record<string, string> = {
  // node
  alive: "badge--green",
  dead: "badge--red",
  // container state
  running: "badge--green",
  stopped: "badge--yellow",
  exited: "badge--red",
  // health
  healthy: "badge--green",
  unhealthy: "badge--red",
  starting: "badge--yellow",
  // rollout
  done: "badge--green",
  failed: "badge--red",
  aborted: "badge--red",
  rolled_back: "badge--yellow",
  pending: "badge--yellow",
  // fallback
  unknown: "badge--grey",
};

export function StatusBadge({ status }: { status: AnyStatus | string }) {
  const cls = COLOR[status] ?? "badge--grey";
  return <span className={`badge ${cls}`}>{status}</span>;
}
