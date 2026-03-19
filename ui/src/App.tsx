import { useState } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { NodesView }     from "./views/NodesView";
import { WorkloadsView } from "./views/WorkloadsView";
import { DeployView }    from "./views/DeployView";
import { LogsView }      from "./views/LogsView";

const queryClient = new QueryClient({
  defaultOptions: { queries: { staleTime: 2000 } },
});

type Tab = "nodes" | "workloads" | "deploy";

const NAV: { id: Tab; label: string; icon: string }[] = [
  { id: "nodes",     label: "Nodes",     icon: "⬡" },
  { id: "workloads", label: "Workloads", icon: "▦" },
  { id: "deploy",    label: "Deploy",    icon: "+" },
];

function Shell() {
  const [tab, setTab]           = useState<Tab>("nodes");
  const [logsID, setLogsID]     = useState<string | null>(null);

  function openLogs(containerID: string) {
    setLogsID(containerID);
  }

  return (
    <div className="shell">
      {/* Sidebar */}
      <aside className="sidebar">
        <div className="sidebar__brand">
          <span className="brand-icon">⬡</span>
          <span className="brand-name">kube<strong>lite</strong></span>
        </div>

        <nav className="sidebar__nav">
          {NAV.map((n) => (
            <button
              key={n.id}
              className={`nav-item ${tab === n.id ? "nav-item--active" : ""}`}
              onClick={() => setTab(n.id)}
            >
              <span className="nav-item__icon">{n.icon}</span>
              <span>{n.label}</span>
            </button>
          ))}
        </nav>

        <div className="sidebar__footer">
          <a
            href="http://localhost:8080/health"
            target="_blank"
            rel="noreferrer"
            className="sidebar__status-link"
          >
            <span className="status-dot status-dot--scheduler" />
            Scheduler :8080
          </a>
        </div>
      </aside>

      {/* Main */}
      <main className="main">
        <div className="content-area">
          {tab === "nodes"     && <NodesView />}
          {tab === "workloads" && <WorkloadsView onLogs={openLogs} />}
          {tab === "deploy"    && <DeployView onDone={() => setTab("workloads")} />}
        </div>

        {/* Log drawer — slides up from the bottom */}
        {logsID && (
          <LogsView containerID={logsID} onClose={() => setLogsID(null)} />
        )}
      </main>
    </div>
  );
}

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <Shell />
    </QueryClientProvider>
  );
}
