import { useEffect, useRef, useState } from "react";

interface Props {
  containerID: string | null;
  onClose: () => void;
}

export function LogsView({ containerID, onClose }: Props) {
  const [lines, setLines]   = useState<string[]>([]);
  const [error, setError]   = useState<string | null>(null);
  const [tail, setTail]     = useState("200");
  const [follow, setFollow] = useState(true);
  const [active, setActive] = useState(false);
  const bottomRef = useRef<HTMLDivElement>(null);
  const abortRef  = useRef<AbortController | null>(null);

  // Auto-scroll when following
  useEffect(() => {
    if (follow) {
      bottomRef.current?.scrollIntoView({ behavior: "smooth" });
    }
  }, [lines, follow]);

  // Start/stop streaming when containerID or settings change
  useEffect(() => {
    if (!containerID) return;

    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    setLines([]);
    setError(null);
    setActive(true);

    const url = `/api/logs/${containerID}?tail=${tail}&follow=${follow}`;

    (async () => {
      try {
        const res = await fetch(url, { signal: ctrl.signal });
        if (!res.ok) {
          const text = await res.text();
          setError(`HTTP ${res.status}: ${text.trim()}`);
          setActive(false);
          return;
        }
        const reader = res.body!.getReader();
        const dec    = new TextDecoder();
        let   buf    = "";

        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          buf += dec.decode(value, { stream: true });
          const parts = buf.split("\n");
          buf = parts.pop() ?? "";
          setLines((prev) => [...prev, ...parts.filter(Boolean)]);
        }
      } catch (e: unknown) {
        if ((e as Error).name !== "AbortError") {
          setError(String(e));
        }
      } finally {
        setActive(false);
      }
    })();

    return () => ctrl.abort();
  }, [containerID, tail, follow]);

  if (!containerID) return null;

  const short = containerID.length > 12 ? containerID.slice(0, 12) : containerID;

  return (
    <div className="logs-view">
      <div className="logs-view__header">
        <div className="logs-view__title">
          <span className={`log-dot ${active ? "log-dot--live" : "log-dot--idle"}`} />
          <code>{short}</code>
          {active && <span className="log-live-tag">LIVE</span>}
        </div>
        <div className="logs-view__controls">
          <label className="log-control">
            Tail
            <select
              className="input input--xs"
              value={tail}
              onChange={(e) => setTail(e.target.value)}
            >
              <option value="50">50</option>
              <option value="200">200</option>
              <option value="500">500</option>
              <option value="all">all</option>
            </select>
          </label>
          <label className="log-control">
            <input
              type="checkbox"
              checked={follow}
              onChange={(e) => setFollow(e.target.checked)}
            />
            Follow
          </label>
          <button className="btn btn--ghost btn--icon" onClick={onClose} title="Close">✕</button>
        </div>
      </div>

      {error && <div className="log-error">{error}</div>}

      <div className="logs-view__body">
        {lines.length === 0 && !error && (
          <span className="log-placeholder">Waiting for output…</span>
        )}
        {lines.map((line, i) => (
          <div key={i} className="log-line">
            <span className="log-line__num">{i + 1}</span>
            <span className="log-line__text">{line}</span>
          </div>
        ))}
        <div ref={bottomRef} />
      </div>
    </div>
  );
}
