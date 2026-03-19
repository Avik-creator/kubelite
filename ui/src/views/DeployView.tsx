import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { deployWorkload, type WorkloadSpec, type PortMapping } from "../api/client";

interface PortRow { host: string; container: string }
interface EnvRow  { key: string; value: string }

export function DeployView({ onDone }: { onDone: () => void }) {
  const qc = useQueryClient();

  const [name, setName]       = useState("");
  const [image, setImage]     = useState("");
  const [replicas, setReplicas] = useState("1");
  const [restart, setRestart] = useState<"Always" | "OnFailure" | "Never">("Always");
  const [ports, setPorts]     = useState<PortRow[]>([{ host: "", container: "" }]);
  const [envs, setEnvs]       = useState<EnvRow[]>([{ key: "", value: "" }]);
  const [healthPath, setHealthPath] = useState("");
  const [healthPort, setHealthPort] = useState("");

  const mut = useMutation({
    mutationFn: (spec: WorkloadSpec) => deployWorkload(spec),
    onSuccess: (data) => {
      qc.invalidateQueries({ queryKey: ["workloads"] });
      // reset
      setName(""); setImage(""); setReplicas("1"); setRestart("Always");
      setPorts([{ host: "", container: "" }]);
      setEnvs([{ key: "", value: "" }]);
      setHealthPath(""); setHealthPort("");
      onDone();
      alert(`Deployed! Workload ID: ${data.id}`);
    },
  });

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    const validPorts: PortMapping[] = ports
      .filter((p) => p.host && p.container)
      .map((p) => ({
        host_port: Number(p.host),
        container_port: Number(p.container),
        protocol: "tcp",
      }));

    const envMap: Record<string, string> = {};
    envs.filter((e) => e.key).forEach((e) => { envMap[e.key] = e.value; });

    const spec: WorkloadSpec = {
      name,
      image,
      replicas: Number(replicas),
      restart_policy: restart,
      ports: validPorts.length ? validPorts : undefined,
      env: Object.keys(envMap).length ? envMap : undefined,
    };
    if (healthPath && healthPort) {
      spec.health_check = {
        path: healthPath,
        port: Number(healthPort),
        interval_seconds: 10,
        timeout_seconds: 5,
        failure_threshold: 3,
      };
    }
    mut.mutate(spec);
  }

  function addPort()    { setPorts((p) => [...p, { host: "", container: "" }]); }
  function removePort(i: number) { setPorts((p) => p.filter((_, j) => j !== i)); }
  function addEnv()     { setEnvs((e) => [...e, { key: "", value: "" }]); }
  function removeEnv(i: number) { setEnvs((e) => e.filter((_, j) => j !== i)); }

  return (
    <div className="view">
      <div className="view__header">
        <div>
          <h1 className="view__title">Deploy Workload</h1>
          <p className="view__sub">Create a new workload or update an existing one</p>
        </div>
      </div>

      <form className="deploy-form" onSubmit={handleSubmit}>
        {/* Core */}
        <section className="form-section">
          <h3 className="section-title">Basic</h3>
          <div className="form-grid">
            <label className="field">
              <span>Name <span className="required">*</span></span>
              <input className="input" required value={name} onChange={(e) => setName(e.target.value)} placeholder="nginx-web" />
            </label>
            <label className="field">
              <span>Image <span className="required">*</span></span>
              <input className="input" required value={image} onChange={(e) => setImage(e.target.value)} placeholder="nginx:latest" />
            </label>
            <label className="field">
              <span>Replicas</span>
              <input className="input" type="number" min="0" value={replicas} onChange={(e) => setReplicas(e.target.value)} />
            </label>
            <label className="field">
              <span>Restart policy</span>
              <select className="input" value={restart} onChange={(e) => setRestart(e.target.value as typeof restart)}>
                <option value="Always">Always</option>
                <option value="OnFailure">OnFailure</option>
                <option value="Never">Never</option>
              </select>
            </label>
          </div>
        </section>

        {/* Ports */}
        <section className="form-section">
          <div className="section-header">
            <h3 className="section-title">Port mappings</h3>
            <button type="button" className="btn btn--ghost btn--xs" onClick={addPort}>+ add</button>
          </div>
          {ports.map((p, i) => (
            <div key={i} className="kv-row">
              <input className="input" placeholder="Host port" value={p.host}
                onChange={(e) => setPorts((prev) => prev.map((r, j) => j === i ? { ...r, host: e.target.value } : r))} />
              <span className="kv-sep">:</span>
              <input className="input" placeholder="Container port" value={p.container}
                onChange={(e) => setPorts((prev) => prev.map((r, j) => j === i ? { ...r, container: e.target.value } : r))} />
              {ports.length > 1 && (
                <button type="button" className="btn btn--ghost btn--icon btn--xs" onClick={() => removePort(i)}>✕</button>
              )}
            </div>
          ))}
        </section>

        {/* Env vars */}
        <section className="form-section">
          <div className="section-header">
            <h3 className="section-title">Environment variables</h3>
            <button type="button" className="btn btn--ghost btn--xs" onClick={addEnv}>+ add</button>
          </div>
          {envs.map((env, i) => (
            <div key={i} className="kv-row">
              <input className="input" placeholder="KEY" value={env.key}
                onChange={(e) => setEnvs((prev) => prev.map((r, j) => j === i ? { ...r, key: e.target.value } : r))} />
              <span className="kv-sep">=</span>
              <input className="input" placeholder="VALUE" value={env.value}
                onChange={(e) => setEnvs((prev) => prev.map((r, j) => j === i ? { ...r, value: e.target.value } : r))} />
              {envs.length > 1 && (
                <button type="button" className="btn btn--ghost btn--icon btn--xs" onClick={() => removeEnv(i)}>✕</button>
              )}
            </div>
          ))}
        </section>

        {/* Health check */}
        <section className="form-section">
          <h3 className="section-title">Health check <span className="optional">(optional)</span></h3>
          <div className="form-grid">
            <label className="field">
              <span>Path</span>
              <input className="input" value={healthPath} onChange={(e) => setHealthPath(e.target.value)} placeholder="/health" />
            </label>
            <label className="field">
              <span>Port</span>
              <input className="input" type="number" value={healthPort} onChange={(e) => setHealthPort(e.target.value)} placeholder="8080" />
            </label>
          </div>
        </section>

        {mut.isError && <p className="form-error">{String(mut.error)}</p>}

        <div className="form-footer">
          <button type="submit" className="btn btn--primary" disabled={mut.isPending}>
            {mut.isPending ? "Deploying…" : "Deploy"}
          </button>
        </div>
      </form>
    </div>
  );
}
