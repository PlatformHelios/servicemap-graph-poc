import { useEffect, useMemo, useState } from "react";
import { Background, BackgroundVariant, Controls, Handle, MarkerType, Position, ReactFlow, type Edge, type Node, type NodeProps } from "@xyflow/react";
import { CircleCheck, CircleX, ExternalLink, Info, RefreshCw, ScanSearch, TriangleAlert, X } from "lucide-react";
import dagre from "dagre";
import { analyzeCatalogManifest, getCatalogAnalysis, getCatalogAnalysisOverview } from "./api";
import { formatWhen } from "./workflows";
import type { AnalysisFinding, AnalysisNode, CatalogAnalysis, CatalogAnalysisSummary } from "./types";
import "@xyflow/react/dist/style.css";

// Workflow Analyzer: how a catalog item really runs, end to end. The server
// joins the item's manifest with the platform (groups, approvers, requests)
// and Temporal (workers, recent runs, activities) and returns a flow graph
// plus findings; this view lays the graph out and lets people explore it.

interface FlowData extends Record<string, unknown> {
  node: AnalysisNode;
  selected: boolean;
  findings: number;
  onSelect: (id: string) => void;
}
type FlowNode = Node<FlowData, "analysis">;

const nodeWidth = 250;
const kindLabels: Record<string, string> = {
  requesters: "REQUESTERS", form: "FORM", cmdb: "CMDB", approval: "APPROVAL", denied: "OUTCOME", handoff: "PLATFORM",
  workflow: "TEAM WORKFLOW", activity: "ACTIVITY", fulfilled: "OUTCOME", failed: "OUTCOME",
};

function nodeHeight(node: AnalysisNode) {
  return 64 + Math.min(node.lines.length, 6) * 17;
}

function AnalysisNodeView({ data }: NodeProps<FlowNode>) {
  const { node, selected, findings, onSelect } = data;
  return <div className={`analysis-node analysis-${node.kind} health-${node.health} ${selected ? "selected" : ""}`} style={{ width: nodeWidth }} onClick={() => onSelect(node.id)}>
    <Handle type="target" position={Position.Left} />
    <div className="analysis-node-kicker"><span>{kindLabels[node.kind] ?? node.kind.toUpperCase()}</span>{node.count !== undefined && <strong className="analysis-count" title="Requests">{node.count}</strong>}</div>
    <div className="analysis-node-title">{node.label}{findings > 0 && <span className="analysis-node-flag" title={`${findings} ${findings === 1 ? "finding" : "findings"}`}>{findings}</span>}</div>
    {node.detail && <div className="analysis-node-detail">{node.detail}</div>}
    {node.lines.length > 0 && <ul>{node.lines.slice(0, 6).map((line, index) => <li key={index}>{line}</li>)}{node.lines.length > 6 && <li>…</li>}</ul>}
    <Handle type="source" position={Position.Right} />
  </div>;
}
const nodeTypes = { analysis: AnalysisNodeView };

function layout(analysis: CatalogAnalysis, selected: string, onSelect: (id: string) => void): { nodes: FlowNode[]; edges: Edge[] } {
  const graph = new dagre.graphlib.Graph();
  graph.setGraph({ rankdir: "LR", nodesep: 28, ranksep: 70, marginx: 20, marginy: 20 });
  graph.setDefaultEdgeLabel(() => ({}));
  for (const node of analysis.nodes) graph.setNode(node.id, { width: nodeWidth, height: nodeHeight(node) });
  for (const edge of analysis.edges) graph.setEdge(edge.from, edge.to);
  dagre.layout(graph);
  const findingCounts = new Map<string, number>();
  for (const finding of analysis.findings) if (finding.nodeId && finding.severity !== "info") findingCounts.set(finding.nodeId, (findingCounts.get(finding.nodeId) ?? 0) + 1);
  const nodes: FlowNode[] = analysis.nodes.map((node) => {
    const position = graph.node(node.id);
    return { id: node.id, type: "analysis", position: { x: position.x - nodeWidth / 2, y: position.y - nodeHeight(node) / 2 }, data: { node, selected: node.id === selected, findings: findingCounts.get(node.id) ?? 0, onSelect } };
  });
  const edges: Edge[] = analysis.edges.map((edge, index) => {
    const quiet = edge.count === 0 || edge.to === "denied" || edge.to === "failed" || edge.label === "references";
    const label = [edge.label, edge.count !== undefined && edge.count !== null ? String(edge.count) : ""].filter(Boolean).join(" · ");
    return {
      id: `${edge.from}-${edge.to}-${index}`, source: edge.from, target: edge.to, label,
      markerEnd: { type: MarkerType.ArrowClosed, width: 16, height: 16 },
      className: quiet ? "analysis-edge-quiet" : "analysis-edge",
      labelBgPadding: [6, 3], labelBgBorderRadius: 0,
    };
  });
  return { nodes, edges };
}

function hours(value: number | undefined) {
  if (value === undefined || value === null) return "—";
  if (value < 1 / 60) return `${Math.max(1, Math.round(value * 3600))}s`;
  if (value < 1) return `${Math.round(value * 60)} min`;
  return `${value.toFixed(1)} h`;
}

function SeverityIcon({ severity }: { severity: AnalysisFinding["severity"] }) {
  return severity === "error" ? <CircleX size={15} /> : severity === "warning" ? <TriangleAlert size={15} /> : <Info size={15} />;
}

export default function WorkflowAnalyzer({ reload, canPublish }: { reload: number; canPublish: boolean }) {
  const [summaries, setSummaries] = useState<CatalogAnalysisSummary[]>([]);
  const [overviewError, setOverviewError] = useState("");
  const [loadingOverview, setLoadingOverview] = useState(false);
  const [selectedItem, setSelectedItem] = useState("");
  const [analysis, setAnalysis] = useState<CatalogAnalysis | null>(null);
  const [analysisError, setAnalysisError] = useState("");
  const [loading, setLoading] = useState(false);
  const [refresh, setRefresh] = useState(0);
  const [selectedNode, setSelectedNode] = useState("");
  const [draftOpen, setDraftOpen] = useState(false);
  const [draftText, setDraftText] = useState("");
  const [draftError, setDraftError] = useState("");

  useEffect(() => {
    let active = true;
    setLoadingOverview(true);
    getCatalogAnalysisOverview().then((result) => {
      if (!active) return;
      setSummaries(result);
      setOverviewError("");
      setSelectedItem((current) => current || result[0]?.item.id || "");
    }).catch((error: unknown) => {
      if (active) setOverviewError(error instanceof Error ? error.message : "Could not load the catalog.");
    }).finally(() => {
      if (active) setLoadingOverview(false);
    });
    return () => { active = false; };
  }, [reload, refresh]);

  useEffect(() => {
    if (!selectedItem) return;
    let active = true;
    setLoading(true);
    getCatalogAnalysis(selectedItem).then((result) => {
      if (!active) return;
      setAnalysis(result);
      setAnalysisError("");
    }).catch((error: unknown) => {
      if (active) setAnalysisError(error instanceof Error ? error.message : "Could not analyze this item.");
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [selectedItem, reload, refresh]);

  async function analyzeDraft() {
    let manifest: unknown;
    try {
      manifest = JSON.parse(draftText);
    } catch {
      setDraftError("Paste the manifest as JSON, for example the output of `go run ./examples/network-team manifest`.");
      return;
    }
    setDraftError("");
    setLoading(true);
    try {
      const result = await analyzeCatalogManifest(manifest);
      setAnalysis(result);
      setSelectedItem("");
      setSelectedNode("");
      setDraftOpen(false);
    } catch (error) {
      setDraftError(error instanceof Error ? error.message : "Could not analyze the manifest.");
    } finally {
      setLoading(false);
    }
  }

  const graph = useMemo(() => analysis ? layout(analysis, selectedNode, (id) => setSelectedNode((current) => current === id ? "" : id)) : { nodes: [], edges: [] }, [analysis, selectedNode]);
  const focused = analysis?.nodes.find((node) => node.id === selectedNode);
  const findings = analysis ? (selectedNode ? analysis.findings.filter((finding) => finding.nodeId === selectedNode) : analysis.findings) : [];
  const runs = analysis?.runs;
  const requests = analysis?.requests;

  return <>
    <section className="records-section request-form-panel" aria-label="Catalog items">
      <div className="ci-picker-heading saved-requests-heading"><div><h3>Catalog items</h3></div><span className="ci-selection-count">Pick an item to see how its requests flow through the platform and Temporal, and what needs attention.</span></div>
      <div className="analysis-toolbar">
        {canPublish && <button className="button button-quiet" type="button" onClick={() => setDraftOpen(true)}><ScanSearch size={15} />Check a draft manifest</button>}
        <button className="button button-quiet" type="button" onClick={() => setRefresh((value) => value + 1)}><RefreshCw size={15} />Re-analyze</button>
      </div>
      {overviewError ? <p className="form-error" role="alert">{overviewError}</p>
        : !loadingOverview && summaries.length === 0 ? <p className="ci-empty">No catalog items are offered to you yet.</p>
        : <div className="analysis-items">{summaries.map((summary) => <button type="button" key={summary.item.id} className={`analysis-item health-${summary.health} ${summary.item.id === selectedItem && !analysis?.draft ? "selected" : ""}`} onClick={() => { setSelectedItem(summary.item.id); setSelectedNode(""); }}>
          <span className="analysis-item-title">{summary.health === "ok" ? <CircleCheck size={15} /> : <SeverityIcon severity={summary.health === "error" ? "error" : "warning"} />}{summary.item.title}</span>
          <small>{summary.item.name} v{summary.item.version} · {summary.item.owner.name ?? summary.item.owner.id}</small>
          <small>{summary.errors} {summary.errors === 1 ? "error" : "errors"} · {summary.warnings} {summary.warnings === 1 ? "warning" : "warnings"} · {summary.requests} {summary.requests === 1 ? "request" : "requests"}</small>
        </button>)}</div>}
    </section>

    {analysisError && <p className="form-error" role="alert">{analysisError}</p>}
    {analysis && <section className="records-section analysis-panel" aria-label="Analysis">
      <div className="analysis-heading">
        <div><p className="eyebrow">{analysis.draft ? "DRAFT MANIFEST · NOT PUBLISHED" : `ANALYSIS · ${analysis.item.id}`}</p><h2>{analysis.item.title} <small>v{analysis.item.version}</small></h2>
          <p className="page-subtitle">{analysis.item.name} · owned by {analysis.item.owner.name ?? analysis.item.owner.id} · {analysis.item.target.workflowType} on {analysis.item.target.taskQueue}{analysis.item.source ? ` · from ${analysis.item.source}` : ""} · analyzed {formatWhen(analysis.generatedAt)}{loading ? " · refreshing…" : ""}</p></div>
        {analysis.draft && <button className="button button-quiet" type="button" onClick={() => { setAnalysis(null); setSelectedItem(summaries[0]?.item.id ?? ""); }}><X size={15} />Close draft</button>}
      </div>

      <div className="analysis-stats">
        <div><span>Requests</span><strong>{requests?.total ?? 0}</strong><small>{Object.entries(requests?.byState ?? {}).map(([state, count]) => `${count} ${state}`).join(" · ") || "none yet"}</small></div>
        <div><span>Time in approval</span><strong>{hours(requests?.averageApprovalHours)}</strong><small>average, submit to last approval</small></div>
        <div><span>Time to fulfil</span><strong>{hours(requests?.averageFulfilmentHours)}</strong><small>average, approval to fulfilled</small></div>
        <div><span>Temporal runs</span><strong>{runs ? runs.total : "—"}</strong><small>{runs ? (runs.error ? "could not be read" : Object.entries(runs.statusCounts).map(([status, count]) => `${count} ${status}`).join(" · ") || "none yet") : analysis.temporalConfigured ? "" : "Temporal not connected"}</small></div>
        <div><span>Findings</span><strong className={analysis.findings.some((finding) => finding.severity === "error") ? "stat-error" : analysis.findings.some((finding) => finding.severity === "warning") ? "stat-warning" : "stat-ok"}>{analysis.findings.filter((finding) => finding.severity !== "info").length}</strong><small>{analysis.findings.filter((finding) => finding.severity === "error").length} errors · {analysis.findings.filter((finding) => finding.severity === "warning").length} warnings · {analysis.findings.filter((finding) => finding.severity === "info").length} notes</small></div>
      </div>

      <div className="analysis-body">
        <div className="map-canvas analysis-canvas">
          <ReactFlow nodes={graph.nodes} edges={graph.edges} nodeTypes={nodeTypes} fitView fitViewOptions={{ padding: 0.12 }} minZoom={0.2} maxZoom={1.5} nodesDraggable={false} nodesConnectable={false} proOptions={{ hideAttribution: true }} onPaneClick={() => setSelectedNode("")}>
            <Background variant={BackgroundVariant.Dots} gap={24} size={1} />
            <Controls showInteractive={false} />
          </ReactFlow>
          <div className="analysis-legend"><span className="legend ok">healthy</span><span className="legend warning">needs a look</span><span className="legend error">blocks requests</span><span className="legend unknown">unknown</span></div>
        </div>

        <aside className="analysis-side">
          <section>
            <div className="ci-picker-heading"><div><h3>{focused ? focused.label : "Findings"}</h3></div>{focused && <button type="button" className="link-button" onClick={() => setSelectedNode("")}>Show all</button>}</div>
            {focused && <div className="analysis-focus"><p className="field-hint">{focused.detail}</p><ul>{focused.lines.map((line, index) => <li key={index}>{line}</li>)}</ul></div>}
            {findings.length === 0 ? <p className="ci-empty">{focused ? "Nothing to flag here." : "No findings: requests for this item can flow end to end."}</p>
              : <ol className="analysis-findings">{findings.map((finding, index) => <li key={index} className={`finding-${finding.severity}`}>
                <button type="button" onClick={() => finding.nodeId && setSelectedNode(finding.nodeId)} disabled={!finding.nodeId}><SeverityIcon severity={finding.severity} /><span>{finding.message}</span></button>
              </li>)}</ol>}
          </section>

          {analysis.approvers.length > 0 && <section>
            <div className="ci-picker-heading"><div><h3>Who can approve</h3></div></div>
            {analysis.approvers.map((approver, index) => <div className="analysis-approver" key={index}><strong>{index + 1}. {approver.step}</strong><small>{approver.rule === "all" ? "every eligible approver" : "any one approver"}</small><span>{approver.eligible.map((person) => person.name ?? person.id).join(", ") || "nobody"}</span></div>)}
          </section>}

          {analysis.workers.length > 0 && <section>
            <div className="ci-picker-heading"><div><h3>Workers</h3></div></div>
            {analysis.workers.map((worker) => <div className="analysis-worker" key={worker.taskQueue}>
              <span className={`status-pill ${worker.error ? "status-draft" : worker.workflowPollers > 0 && worker.activityPollers > 0 ? "status-active" : "status-retired"}`}>{worker.error ? "unknown" : worker.workflowPollers > 0 && worker.activityPollers > 0 ? "polling" : "no worker"}</span>
              <div><strong>{worker.taskQueue}</strong><small>{worker.error ? worker.error : `${worker.workers.length} ${worker.workers.length === 1 ? "worker" : "workers"}${worker.lastSeen ? ` · last seen ${formatWhen(worker.lastSeen)}` : ""}`}</small></div>
            </div>)}
          </section>}

          {runs && runs.recent.length > 0 && <section>
            <div className="ci-picker-heading"><div><h3>Recent runs</h3></div><span className="ci-selection-count">{runs.workflowType}</span></div>
            <ol className="analysis-runs">{runs.recent.map((run) => <li key={run.runId}>
              <span className={`status-pill ${run.status === "completed" ? "status-active" : run.status === "running" ? "status-in-review" : "status-retired"}`}>{run.status}</span>
              <span className="analysis-run-id">{analysis.temporalUiUrl ? <a href={`${analysis.temporalUiUrl}/workflows/${encodeURIComponent(run.workflowId)}/${encodeURIComponent(run.runId)}`} target="_blank" rel="noreferrer">{run.workflowId}<ExternalLink size={11} /></a> : run.workflowId}<small>{formatWhen(run.startedAt)}{run.seconds ? ` · ${Math.round(run.seconds)}s` : ""}</small></span>
            </li>)}</ol>
          </section>}
        </aside>
      </div>
    </section>}

    {draftOpen && <div className="modal-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) setDraftOpen(false); }}>
      <section className="editor-dialog task-dialog" role="dialog" aria-modal="true" aria-labelledby="draft-title">
        <form onSubmit={(event) => { event.preventDefault(); void analyzeDraft(); }}>
          <div className="dialog-heading"><div><p className="eyebrow">BEFORE YOU PUBLISH</p><h2 id="draft-title">Check a draft manifest</h2><p className="page-subtitle">Paste a manifest generated by the catalog SDK. It is checked exactly as publishing would check it, against the live groups, CMDB, and Temporal workers, but nothing is stored.</p></div><button className="close-button" type="button" onClick={() => setDraftOpen(false)} aria-label="Close"><X size={17} /></button></div>
          <div className="form-field"><label htmlFor="draft-manifest">Manifest JSON</label><textarea id="draft-manifest" className="analysis-draft" rows={16} spellCheck={false} value={draftText} onChange={(event) => setDraftText(event.target.value)} placeholder='{"name": "server-request", "version": "1.1.0", ...}' /></div>
          {draftError && <p className="form-error" role="alert">{draftError}</p>}
          <div className="dialog-actions"><button className="button button-quiet" type="button" onClick={() => setDraftOpen(false)}>Cancel</button><button className="button button-primary" type="submit" disabled={loading || !draftText.trim()}>Analyze</button></div>
        </form>
      </section>
    </div>}
  </>;
}
