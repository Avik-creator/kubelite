import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  getWorkloads,
  getWorkload,
  scaleWorkload,
  deleteWorkload,
  startRollout,
  getRollout,
  type WorkloadSummary,
} from "../api/client";
import { StatusBadge } from "../components/StatusBadge";

function short(id: string) {
  return id.length > 12 ? id.slice(0, 12) : id;
}

// ─── Workload detail panel ────────────────────────────────────────────────────

function WorkloadDetail({
  id,
  onClose,
  onLogs,
}: {
  id: string;
  onClose: () => void;
  onLogs: (containerID: string) => void;
}) {
  const qc = useQueryClient();
  const [scaleVal, setScaleVal] = useState("");
  const [rolloutImg, setRolloutImg] = useState("");
  const [rolloutOpen, setRolloutOpen] = useState(false);

  const { data, isLoading, error } = useQuery({
    queryKey: ["workload", id],
    queryFn: () => getWorkload(id),
    refetchInterval: 3000,
  });

  const { data: rolloutState } = useQuery({
    queryKey: ["rollout", id],
    queryFn: () => getRollout(id),
    refetchInterval: 4000,
    retry: false,
  });

  const scaleMut = useMutation({
    mutationFn: (replicas: number) => scaleWorkload(id, replicas),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["workloads"] });
      qc.invalidateQueries({ queryKey: ["workload", id] });
      setScaleVal("");
    },
  });

  const deleteMut = useMutation({
    mutationFn: () => deleteWorkload(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["workloads"] });
      onClose();
    },
  });

  const rolloutMut = useMutation({
    mutationFn: () =>
      startRollout({ workload_id: id, new_image: rolloutImg, max_unavailable: 1, max_surge: 1 }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["rollout", id] });
      setRolloutOpen(false);
      setRolloutImg("");
    },
  });

  if (isLoading) return <div className="detail-panel"><div className="state-msg">Loading…</div></div>;
  if (error || !data) return <div className="detail-panel"><div className="state-msg state-msg--err">Failed to load workload.</div></div>;

  const { spec, instances } = data;

  return (
    <div className="detail-panel">
      <div className="detail-panel__header">
        <div>
          <h2 className="detail-panel__title">{spec.name}</h2>
          <span className="detail-panel__id">{spec.id}</span>
        </div>
        <button className="btn btn--ghost btn--icon" onClick={onClose} title="Close">✕</button>
      </div>

      <div className="detail-meta">
        <div className="detail-meta__item"><label>Image</label><code>{spec.image}</code></div>
        <div className="detail-meta__item"><label>Replicas</label><span>{spec.replicas} desired</span></div>
        <div className="detail-meta__item"><label>Restart</label><span>{spec.restart_policy}</span></div>
      </div>

      {/* Instances table */}
      <h3 className="section-title">Instances ({instances.length})</h3>
      {instances.length === 0 ? (
        <p className="empty-state__hint">No instances running.</p>
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>Container</th>
                <th>Node</th>
                <th>State</th>
                <th>Health</th>
                <th>IP</th>
                <th>Started</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {instances.map((inst) => (
                <tr key={inst.id}>
                  <td><code>{short(inst.id)}</code></td>
                  <td>{inst.node_id}</td>
                  <td><StatusBadge status={inst.state} /></td>
                  <td><StatusBadge status={inst.health} /></td>
                  <td>{inst.ip || "—"}</td>
                  <td>{new Date(inst.started_at).toLocaleTimeString()}</td>
                  <td>
                    <button
                      className="btn btn--ghost btn--xs"
                      onClick={() => onLogs(inst.id)}
                    >
                      logs
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* Rollout status */}
      {rolloutState && (
        <div className="rollout-status">
          <h3 className="section-title">Rollout</h3>
          <div className="detail-meta">
            <div className="detail-meta__item"><label>Status</label><StatusBadge status={rolloutState.status} /></div>
            <div className="detail-meta__item"><label>Image</label><code>{rolloutState.old_image} → {rolloutState.new_image}</code></div>
            <div className="detail-meta__item">
              <label>Progress</label>
              <span>{rolloutState.updated_replicas}/{rolloutState.desired_replicas} updated · {rolloutState.old_replicas} old · wave {rolloutState.wave}</span>
            </div>
            {rolloutState.message && (
              <div className="detail-meta__item"><label>Message</label><span>{rolloutState.message}</span></div>
            )}
          </div>
        </div>
      )}

      {/* Actions */}
      <h3 className="section-title">Actions</h3>
      <div className="actions-row">
        <div className="action-group">
          <input
            className="input input--sm"
            type="number"
            min="0"
            placeholder="New replica count"
            value={scaleVal}
            onChange={(e) => setScaleVal(e.target.value)}
          />
          <button
            className="btn btn--primary btn--sm"
            disabled={!scaleVal || scaleMut.isPending}
            onClick={() => scaleMut.mutate(Number(scaleVal))}
          >
            {scaleMut.isPending ? "Scaling…" : "Scale"}
          </button>
        </div>

        <button
          className="btn btn--outline btn--sm"
          onClick={() => setRolloutOpen((p) => !p)}
        >
          Rolling update
        </button>

        <button
          className="btn btn--danger btn--sm"
          disabled={deleteMut.isPending}
          onClick={() => {
            if (confirm(`Delete workload "${spec.name}"?`)) deleteMut.mutate();
          }}
        >
          {deleteMut.isPending ? "Deleting…" : "Delete"}
        </button>
      </div>

      {rolloutOpen && (
        <div className="rollout-form">
          <input
            className="input"
            placeholder="New image (e.g. nginx:1.27)"
            value={rolloutImg}
            onChange={(e) => setRolloutImg(e.target.value)}
          />
          <button
            className="btn btn--primary btn--sm"
            disabled={!rolloutImg || rolloutMut.isPending}
            onClick={() => rolloutMut.mutate()}
          >
            {rolloutMut.isPending ? "Starting…" : "Start rollout"}
          </button>
          {rolloutMut.isError && (
            <p className="form-error">{String(rolloutMut.error)}</p>
          )}
        </div>
      )}
    </div>
  );
}

// ─── Workloads list ───────────────────────────────────────────────────────────

export function WorkloadsView({ onLogs }: { onLogs: (containerID: string) => void }) {
  const [selectedID, setSelectedID] = useState<string | null>(null);

  const { data: workloads, isLoading, error } = useQuery({
    queryKey: ["workloads"],
    queryFn: getWorkloads,
    refetchInterval: 3000,
  });

  if (isLoading) return <div className="state-msg">Loading workloads…</div>;
  if (error) return <div className="state-msg state-msg--err">Failed to load workloads: {String(error)}</div>;

  return (
    <div className="view view--split">
      <div className={`view__list ${selectedID ? "view__list--narrow" : ""}`}>
        <div className="view__header">
          <div>
            <h1 className="view__title">Workloads</h1>
            <p className="view__sub">{workloads?.length ?? 0} total</p>
          </div>
        </div>

        {workloads?.length === 0 ? (
          <div className="empty-state">
            <p>No workloads deployed yet.</p>
            <p className="empty-state__hint">Use the Deploy tab or run <code>kl deploy</code></p>
          </div>
        ) : (
          <div className="table-wrap">
            <table className="table table--interactive">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Image</th>
                  <th>Replicas</th>
                  <th>Running</th>
                </tr>
              </thead>
              <tbody>
                {workloads?.map((w: WorkloadSummary) => (
                  <tr
                    key={w.id}
                    className={selectedID === w.id ? "row--selected" : ""}
                    onClick={() => setSelectedID(selectedID === w.id ? null : w.id)}
                  >
                    <td><strong>{w.name}</strong></td>
                    <td><code>{w.image}</code></td>
                    <td>{w.replicas}</td>
                    <td>
                      <span className={w.running === w.replicas ? "count--ok" : "count--warn"}>
                        {w.running}/{w.replicas}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {selectedID && (
        <WorkloadDetail
          id={selectedID}
          onClose={() => setSelectedID(null)}
          onLogs={onLogs}
        />
      )}
    </div>
  );
}
