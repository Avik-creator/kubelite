import { useQuery } from "@tanstack/react-query";
import { getNodes, type Node } from "../api/client";
import { StatusBadge } from "../components/StatusBadge";

function timeSince(iso: string) {
  const diff = Math.floor((Date.now() - new Date(iso).getTime()) / 1000);
  if (diff < 60) return `${diff}s ago`;
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  return `${Math.floor(diff / 3600)}h ago`;
}

function NodeCard({ node }: { node: Node }) {
  const isAlive = node.status === "alive";
  return (
    <div className={`node-card ${isAlive ? "node-card--alive" : "node-card--dead"}`}>
      <div className="node-card__header">
        <span className={`node-dot node-dot--${node.status}`} />
        <span className="node-card__id">{node.id}</span>
        <StatusBadge status={node.status} />
      </div>
      <div className="node-card__addr">{node.address}</div>
      <div className="node-card__stats">
        <div className="node-stat">
          <span className="node-stat__label">CPU</span>
          <span className="node-stat__value">{node.cpu_cores} cores</span>
        </div>
        <div className="node-stat">
          <span className="node-stat__label">MEM</span>
          <span className="node-stat__value">{(node.memory_mb / 1024).toFixed(0)} GB</span>
        </div>
        <div className="node-stat">
          <span className="node-stat__label">BEAT</span>
          <span className="node-stat__value">{timeSince(node.last_heartbeat)}</span>
        </div>
      </div>
    </div>
  );
}

export function NodesView() {
  const { data: nodes, isLoading, error, dataUpdatedAt } = useQuery({
    queryKey: ["nodes"],
    queryFn: getNodes,
    refetchInterval: 3000,
  });

  if (isLoading) return <div className="state-msg">Loading nodes…</div>;
  if (error) return <div className="state-msg state-msg--err">Failed to load nodes: {String(error)}</div>;

  const alive = nodes?.filter((n) => n.status === "alive").length ?? 0;
  const total = nodes?.length ?? 0;

  return (
    <div className="view">
      <div className="view__header">
        <div>
          <h1 className="view__title">Nodes</h1>
          <p className="view__sub">{alive} / {total} alive · updated {new Date(dataUpdatedAt).toLocaleTimeString()}</p>
        </div>
      </div>

      {nodes?.length === 0 ? (
        <div className="empty-state">
          <p>No nodes registered yet.</p>
          <p className="empty-state__hint">Start an agent with <code>make run-agent</code></p>
        </div>
      ) : (
        <div className="node-grid">
          {nodes?.map((n) => <NodeCard key={n.id} node={n} />)}
        </div>
      )}
    </div>
  );
}
