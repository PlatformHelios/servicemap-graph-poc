import { useEffect, useRef, useState } from "react";
import {
  Background,
  BackgroundVariant,
  BaseEdge,
  Controls,
  EdgeLabelRenderer,
  Handle,
  MarkerType,
  MiniMap,
  Position,
  ReactFlow,
  type Edge,
  type EdgeProps,
  type Node,
  type NodeProps,
  useReactFlow,
} from "@xyflow/react";
import { ArrowDown, ArrowDownLeft, ArrowUpRight, BriefcaseBusiness, CircleAlert, RefreshCw, Search, SquareArrowOutUpRight, X } from "lucide-react";
import dagre from "dagre";
import { getAccessAnomalies, getNodes, getRelationships, nodeDisplayName, relationshipLabel, relationshipSourceKey, relationshipTargetKey } from "./api";
import type { CIType, GraphNode, GraphRelationship, Metadata, NodeKind, RelationshipKind } from "./types";
import "@xyflow/react/dist/style.css";

interface MapNodeData extends Record<string, unknown> {
  label: string;
  kind: NodeKind;
  status: "active" | "retired";
  detail: string;
  selected: boolean;
  dimmed: boolean; // true when another record is selected and this one is not linked to it
  anomaly: boolean; // identity holds overlapping access paths
  down: boolean; // CI is down from a nodeDown incident, directly or by impact
  onSelect: (id: string) => void;
}

type MapFlowNode = Node<MapNodeData, "mapRecord">;

interface MapEdgeData extends Record<string, unknown> {
  label: string;
  // Waypoints dagre routed the edge through (one per rank crossed, including the
  // label slot), in flow coordinates; the ends come from the node handles.
  waypoints: { x: number; y: number }[];
  labelX: number;
  labelY: number;
  linked: boolean; // touches the selected record
  dimmed: boolean;
  retired: boolean;
}

type MapFlowEdge = Edge<MapEdgeData, "mapLink">;

// ReactFlow only fits the view on mount; re-fit whenever the set of visible
// records changes (search, type filter, a save, or a selection pulling in neighbours).
function FitToVisible({ signature }: { signature: string }) {
  const { fitView, getNodes } = useReactFlow();
  useEffect(() => {
    if (!signature) return;
    let attempts = 0;
    let timer = 0;
    const run = () => {
      const pane = document.querySelector(".map-canvas .react-flow") as HTMLElement | null;
      const ready = getNodes().length > 0 && Boolean(pane && pane.clientWidth > 0 && pane.clientHeight > 0);
      if (!ready && attempts < 12) {
        attempts += 1;
        timer = window.setTimeout(run, 50);
        return;
      }
      if (getNodes().length > 0) void fitView({ padding: 0.18, maxZoom: 1.15, duration: 250 });
    };
    const frame = window.requestAnimationFrame(run);
    return () => {
      window.cancelAnimationFrame(frame);
      window.clearTimeout(timer);
    };
  }, [signature, fitView, getNodes]);
  return null;
}

interface RelationshipTrace {
  relationship: GraphRelationship;
  adjacentNode: GraphNode;
  depth: number;
  source: GraphNode;
  target: GraphNode;
}

const nodeWidth = 226;
const nodeHeight = 92;
// Approximate box of an edge label (10–11px monospace plus padding) so dagre
// can reserve room for it between the ranks instead of letting labels pile up.
const labelCharWidth = 6.8;
const labelPadding = 18;
const labelHeight = 22;
const nodeTypes = { mapRecord: MapRecordNode };
const edgeTypes = { mapLink: MapLinkEdge };

const kindTones: Record<NodeKind, string> = {
  identity: "blue",
  "job-code": "slate",
  birthright: "yellow",
  role: "blue",
  entitlement: "yellow",
  group: "slate",
  ci: "blue",
  incident: "red",
  change: "yellow",
  event: "green",
  request: "slate",
  workflow: "yellow",
  "workflow-step": "yellow",
  "workflow-run": "green",
  task: "green",
};

function nodeID(node: GraphNode) {
  return `${node.kind}:${node.id}`;
}

function displayName(node: GraphNode) {
  const properties = node.properties ?? {};
  if (node.kind === "ci" && typeof properties.ciType === "string") {
    return `${nodeDisplayName(node) ?? node.id} · ${properties.ciType}`;
  }
  return nodeDisplayName(node) ?? node.id;
}

function titleCase(value: string) {
	return value.replaceAll("-", " ").replace(/\b\w/g, (character) => character.toUpperCase());
}

// Outage spreads along these CI links: hosts/provides flow with the edge;
// depends-on/uses flow toward the consumer (reverse of the stored edge).
const impactPropagation: { kind: RelationshipKind; direction: "forward" | "reverse" }[] = [
  { kind: "hosts", direction: "forward" },
  { kind: "provides", direction: "forward" },
  { kind: "depends-on", direction: "reverse" },
  { kind: "uses", direction: "reverse" },
];

/** CI IDs marked down by an active nodeDown incident, plus CIs impacted through hosts / provides / depends-on / uses. */
function downCIIds(nodes: GraphNode[], relationships: GraphRelationship[]): Set<string> {
  const knownCIs = new Set(nodes.filter((node) => node.kind === "ci" && node.status === "active").map((node) => node.id));
  const nodeDownIncidents = new Set(
    nodes
      .filter((node) => node.kind === "incident" && node.status === "active" && node.properties?.nodeDown === true)
      .map((node) => node.id),
  );
  const down = new Set<string>();
  for (const relationship of relationships) {
    if (relationship.kind !== "affects" || relationship.status === "retired") continue;
    if (nodeDownIncidents.has(relationship.fromId) && knownCIs.has(relationship.toId)) {
      down.add(relationship.toId);
    }
  }
  const impactEdges = new Map<string, string[]>();
  const link = (from: string, to: string) => {
    if (!knownCIs.has(from) || !knownCIs.has(to)) return;
    const list = impactEdges.get(from) ?? [];
    list.push(to);
    impactEdges.set(from, list);
  };
  for (const relationship of relationships) {
    if (relationship.status === "retired") continue;
    const rule = impactPropagation.find((item) => item.kind === relationship.kind);
    if (!rule) continue;
    if (rule.direction === "forward") link(relationship.fromId, relationship.toId);
    else link(relationship.toId, relationship.fromId);
  }
  const queue = [...down];
  for (let index = 0; index < queue.length; index += 1) {
    for (const next of impactEdges.get(queue[index]) ?? []) {
      if (down.has(next)) continue;
      down.add(next);
      queue.push(next);
    }
  }
  return down;
}

function layoutGraph(nodes: GraphNode[], relationships: GraphRelationship[], definitions: Metadata["relationships"], selectedNodeID: string | null, anomalyIdentityIDs: Set<string>, downIDs: Set<string>, onSelect: (id: string) => void) {
  const known = new Map(nodes.map((node) => [nodeID(node), node]));
  const definitionByKind = new Map(definitions.map((definition) => [definition.kind, definition]));
  // A multigraph keeps two relationships between the same pair of records as
  // separate edges, so dagre routes and labels each on its own.
  const dagreGraph = new dagre.graphlib.Graph({ multigraph: true });
  dagreGraph.setDefaultEdgeLabel(() => ({}));
  dagreGraph.setGraph({ rankdir: "LR", nodesep: 44, ranksep: 72, edgesep: 26, marginx: 28, marginy: 28 });

  const flowNodes: MapFlowNode[] = nodes.map((node) => {
    const id = nodeID(node);
    dagreGraph.setNode(id, { width: nodeWidth, height: nodeHeight });
    return {
      id,
      type: "mapRecord",
      position: { x: 0, y: 0 },
      data: {
        label: displayName(node),
        kind: node.kind,
        status: node.status,
        detail: node.properties?.externalId ? String(node.properties.externalId) : node.id,
        selected: id === selectedNodeID,
        dimmed: false, // settled below once the selected record's neighbours are known
        anomaly: node.kind === "identity" && anomalyIdentityIDs.has(node.id),
        down: node.kind === "ci" && downIDs.has(node.id),
        onSelect,
      },
    };
  });

  const flowEdges: MapFlowEdge[] = [];
  // Records directly linked to the selected one stay at full strength; every
  // other node and edge dims so the selected record's connections stand out.
  const neighbourIDs = new Set<string>();
  for (const relationship of relationships) {
    const definition = definitionByKind.get(relationship.kind);
    if (!definition) continue;
    const source = relationshipSourceKey(definition, relationship.fromId, nodes);
    const target = relationshipTargetKey(definition, relationship.toId, nodes);
    if (!known.has(source) || !known.has(target)) continue;
    const id = `${relationship.kind}:${source}:${target}`;
    // Label the edge from the selected node's perspective: forward when it is the
    // source (downstream), inverse when it is the target (upstream). Unselected
    // edges keep the forward label.
    const touchesSelected = selectedNodeID !== null && (source === selectedNodeID || target === selectedNodeID);
    if (touchesSelected) {
      neighbourIDs.add(source);
      neighbourIDs.add(target);
    }
    const dimmed = selectedNodeID !== null && !touchesSelected;
    const viewedFromSource = !touchesSelected || source === selectedNodeID;
    const label = relationshipLabel(relationship, viewedFromSource, definitions);
    // Reserve room for whichever reading is longer so selecting a record (which
    // flips some labels to their inverse) does not reshuffle the layout.
    const longestLabel = Math.max(label.length, relationshipLabel(relationship, !viewedFromSource, definitions).length);
    dagreGraph.setEdge(source, target, { width: longestLabel * labelCharWidth + labelPadding, height: labelHeight, labelpos: "c" }, id);
    const strokeColor = relationship.status === "retired" ? "#a5b0bf" : touchesSelected ? "#174d8a" : "#2871ba";
    const arrow = { type: MarkerType.ArrowClosed, color: strokeColor };
    flowEdges.push({
      id,
      source,
      target,
      type: "mapLink",
      className: dimmed ? "map-edge-dimmed" : undefined,
      data: { label, waypoints: [], labelX: 0, labelY: 0, linked: touchesSelected, dimmed, retired: relationship.status === "retired" },
      // The arrow points away from the viewed node: forward edges point at the
      // target; when the selected node is the target, the inverse reading points
      // back at the source instead.
      markerEnd: viewedFromSource ? arrow : undefined,
      markerStart: viewedFromSource ? undefined : arrow,
      style: { stroke: strokeColor, strokeWidth: touchesSelected ? 2.2 : 1.7 },
      animated: false,
    });
  }

  dagre.layout(dagreGraph);
  const positionedNodes = flowNodes.map((node) => {
    const position = dagreGraph.node(node.id);
    const dimmed = selectedNodeID !== null && node.id !== selectedNodeID && !neighbourIDs.has(node.id);
    return { ...node, data: { ...node.data, dimmed }, position: { x: position.x - nodeWidth / 2, y: position.y - nodeHeight / 2 } };
  });
  // Node centres are the same in both coordinate spaces, so dagre's routing
  // points and label slots can be used as-is. The first and last points are the
  // node borders, which the edge replaces with the actual handle positions.
  const routedEdges = flowEdges.map((edge) => {
    const routed = dagreGraph.edge({ v: edge.source, w: edge.target, name: edge.id });
    const waypoints = (routed?.points ?? []).slice(1, -1);
    const slot = routed && typeof routed.x === "number" && typeof routed.y === "number"
      ? { x: routed.x, y: routed.y }
      : waypoints[Math.floor(waypoints.length / 2)] ?? { x: 0, y: 0 };
    return { ...edge, data: { ...edge.data!, waypoints, labelX: slot.x, labelY: slot.y } };
  });
  return { nodes: positionedNodes, edges: routedEdges };
}

// Edges follow dagre's routing: a smooth curve through each rank it crosses, so
// long links bend around intermediate records instead of cutting across them,
// with the label sitting in the slot dagre reserved for it.
function MapLinkEdge({ id, sourceX, sourceY, targetX, targetY, data, style, markerEnd, markerStart }: EdgeProps<MapFlowEdge>) {
  const points = [{ x: sourceX, y: sourceY }, ...(data?.waypoints ?? []), { x: targetX, y: targetY }];
  let path = `M ${points[0].x} ${points[0].y}`;
  for (let index = 1; index < points.length; index += 1) {
    const from = points[index - 1];
    const to = points[index];
    const pull = Math.max(Math.abs(to.x - from.x) / 2, 28);
    path += ` C ${from.x + pull} ${from.y}, ${to.x - pull} ${to.y}, ${to.x} ${to.y}`;
  }
  const labelClass = ["map-edge-label", data?.linked ? "map-edge-label-linked" : "", data?.dimmed ? "map-edge-label-dimmed" : "", data?.retired ? "map-edge-label-retired" : ""].filter(Boolean).join(" ");
  return <>
    <BaseEdge id={id} path={path} style={style} markerEnd={markerEnd} markerStart={markerStart} />
    <EdgeLabelRenderer>
      <div className={labelClass} style={{ transform: `translate(-50%, -50%) translate(${data?.labelX ?? 0}px, ${data?.labelY ?? 0}px)` }}>{data?.label}</div>
    </EdgeLabelRenderer>
  </>;
}

function MapRecordNode({ id, data }: NodeProps<MapFlowNode>) {
  return <div className={`map-record-node tone-${kindTones[data.kind]} ${data.status === "retired" ? "map-record-retired" : ""} ${data.selected ? "map-record-selected" : ""} ${data.dimmed ? "map-record-dimmed" : ""} ${data.anomaly ? "map-record-anomaly" : ""} ${data.down ? "map-record-down" : ""}`} onClick={(event) => { event.stopPropagation(); data.onSelect(id); }}>
    <Handle type="target" position={Position.Left} isConnectable={false} />
    {data.down && <span className="map-record-down-badge" title="Node is down (open incident or impacted by one)" aria-label="Node is down"><ArrowDown size={16} strokeWidth={2.6} /></span>}
    {data.anomaly && <span className="map-record-anomaly-badge" title="Access anomaly: overlapping birthright, role, or direct permission paths" aria-label="Access anomaly"><CircleAlert size={16} strokeWidth={2.4} /></span>}
    <div className="map-record-heading">
      <span className="map-record-kind">{data.kind === "ci" ? "CI" : data.kind.toUpperCase()}</span>
      <span className={`map-record-status ${data.status}`}>{data.status}</span>
    </div>
    <strong className="map-record-label">{data.label}</strong>
    <span className="map-record-detail">{data.detail}</span>
    <Handle type="source" position={Position.Right} isConnectable={false} />
  </div>;
}

function traceRelationships(
  selected: GraphNode,
  direction: "upstream" | "downstream",
  nodes: GraphNode[],
  relationships: GraphRelationship[],
  definitions: Metadata["relationships"],
): RelationshipTrace[] {
  const nodesByID = new Map(nodes.map((node) => [nodeID(node), node]));
  const definitionsByKind = new Map(definitions.map((definition) => [definition.kind, definition]));
  const startID = nodeID(selected);
  const visitedNodes = new Set([startID]);
  const visitedRelationships = new Set<string>();
  const queue = [{ id: startID, depth: 0 }];
  const traces: RelationshipTrace[] = [];

  for (let index = 0; index < queue.length; index += 1) {
    const current = queue[index];
    for (const relationship of relationships) {
      const definition = definitionsByKind.get(relationship.kind);
      if (!definition) continue;
      const sourceID = relationshipSourceKey(definition, relationship.fromId, nodes);
      const targetID = relationshipTargetKey(definition, relationship.toId, nodes);
      const nextID = direction === "upstream"
        ? targetID === current.id ? sourceID : null
        : sourceID === current.id ? targetID : null;
      const edgeID = `${relationship.kind}:${sourceID}:${targetID}`;
      if (!nextID || visitedRelationships.has(edgeID)) continue;

      const source = nodesByID.get(sourceID);
      const target = nodesByID.get(targetID);
      const adjacentNode = nodesByID.get(nextID);
      if (!source || !target || !adjacentNode) continue;

      visitedRelationships.add(edgeID);
      traces.push({ relationship, adjacentNode, depth: current.depth + 1, source, target });
      if (!visitedNodes.has(nextID)) {
        visitedNodes.add(nextID);
        queue.push({ id: nextID, depth: current.depth + 1 });
      }
    }
  }
  return traces;
}

function nodeLabel(node: GraphNode) {
  const properties = node.properties ?? {};
  const name = nodeDisplayName(node) ?? node.id;
  const type = node.kind === "ci" && typeof properties.ciType === "string"
    ? `CI · ${titleCase(properties.ciType)}`
    : titleCase(node.kind);
  return { name: String(name), type };
}

function RelationshipTraceList({ direction, traces, definitions, onSelect }: {
  direction: "upstream" | "downstream";
  traces: RelationshipTrace[];
  definitions: Metadata["relationships"];
  onSelect: (node: GraphNode) => void;
}) {
  const Icon = direction === "upstream" ? ArrowDownLeft : ArrowUpRight;
  if (traces.length === 0) {
    return <p className="trace-empty">No {direction} relationships.</p>;
  }
  return <div className="trace-list">
    {traces.map(({ relationship, adjacentNode, depth, source, target }) => {
      const adjacentLabel = nodeLabel(adjacentNode);
      const sourceLabel = nodeLabel(source);
      const targetLabel = nodeLabel(target);
      // Downstream traces are walked from the source side, upstream from the target
      // side, so the sentence reads "<viewed node> <label> <adjacent node>".
      const viewedFromSource = direction === "downstream";
      const label = relationshipLabel(relationship, viewedFromSource, definitions);
      const subject = viewedFromSource ? sourceLabel : targetLabel;
      const object = viewedFromSource ? targetLabel : sourceLabel;
      return <button className="trace-item" key={`${relationship.kind}:${relationship.fromId}:${relationship.toId}`} onClick={() => onSelect(adjacentNode)}>
        <span className="trace-item-icon"><Icon size={14} /></span>
        <span className="trace-item-content">
          <small>{label} · {depth === 1 ? "DIRECT" : `DEPTH ${depth}`}</small>
          <strong>{subject.name} <span>{label}</span> {object.name}</strong>
          <em>{adjacentLabel.type} · {adjacentNode.id}</em>
        </span>
      </button>;
    })}
  </div>;
}

type LinkedRecordKind = "incident" | "change";

function SelectedNodePanel({ node, upstream, downstream, definitions, hasAnomaly, isDown, onClose, onSelect, onCreateLinked, onEdit }: {
  node: GraphNode;
  upstream: RelationshipTrace[];
  downstream: RelationshipTrace[];
  definitions: Metadata["relationships"];
  hasAnomaly?: boolean;
  isDown?: boolean;
  onClose: () => void;
  onSelect: (node: GraphNode) => void;
  onCreateLinked?: (kind: LinkedRecordKind, ci: GraphNode) => void;
  onEdit?: (node: GraphNode) => void;
}) {
  const label = nodeLabel(node);
  // Incidents and changes attach to CIs (affects / changes), so only offer them for active CI records.
  const canCreateLinked = Boolean(onCreateLinked) && node.kind === "ci" && node.status === "active";
  // Every record on the map opens in the same editor the list views use.
  const canEdit = Boolean(onEdit);
  return <aside className="map-inspector" aria-label="Selected record relationships">
    <header className="map-inspector-header">
      <div><p className="eyebrow">SELECTED RECORD</p><h2>{label.name}</h2></div>
      <div className="map-inspector-controls">
        {canEdit && <button className="close-button" type="button" onClick={() => onEdit?.(node)} aria-label={`Open ${label.name} in the editor`} title="Open in editor"><SquareArrowOutUpRight size={16} /></button>}
        <button className="close-button" type="button" onClick={onClose} aria-label="Close relationship details"><X size={16} /></button>
      </div>
    </header>
    <div className="map-inspector-meta"><span>{label.type}</span><span className={`status-pill status-${node.status}`}>{node.status}</span>{isDown && <span className="status-pill status-down" title="Marked down by an open node-down incident, or impacted by one">down</span>}{hasAnomaly && <span className="status-pill anomaly-path anomaly-path-direct" title="Overlapping access paths">anomaly</span>}</div>
    {isDown && <p className="form-error map-inspector-anomaly" role="status">This CI is down: an open incident has Node is down checked on it, or it is impacted through hosts, provides, depends-on, or uses links.</p>}
    {hasAnomaly && <p className="form-error map-inspector-anomaly" role="status">This identity holds the same access through more than one path. Review Access anomalies on the Birthrights page.</p>}
    <code className="map-inspector-id">{node.id}</code>
    {canCreateLinked && <div className="map-inspector-actions">
      <button className="button button-quiet icon-button" type="button" onClick={() => onCreateLinked?.("incident", node)}><BriefcaseBusiness size={14} />Create incident</button>
      <button className="button button-quiet icon-button" type="button" onClick={() => onCreateLinked?.("change", node)}><RefreshCw size={14} />Create change</button>
    </div>}
    <section className="trace-section">
      <div className="trace-section-heading"><h3>Upstream</h3><span>{upstream.length}</span></div>
      <RelationshipTraceList direction="upstream" traces={upstream} definitions={definitions} onSelect={onSelect} />
    </section>
    <section className="trace-section">
      <div className="trace-section-heading"><h3>Downstream</h3><span>{downstream.length}</span></div>
      <RelationshipTraceList direction="downstream" traces={downstream} definitions={definitions} onSelect={onSelect} />
    </section>
  </aside>;
}

export default function GraphMap({ metadata, includeRetired, reload, onToggleIncludeRetired, onCreateLinked, onEdit }: {
  metadata: Metadata | null;
  includeRetired: boolean;
  reload: number;
  onToggleIncludeRetired: () => void;
  onCreateLinked?: (kind: LinkedRecordKind, ci: GraphNode) => void;
  onEdit?: (node: GraphNode) => void;
}) {
  const [nodes, setNodes] = useState<GraphNode[]>([]);
  const [relationships, setRelationships] = useState<GraphRelationship[]>([]);
  const [anomalyIdentityIDs, setAnomalyIdentityIDs] = useState<Set<string>>(() => new Set());
  const [kindFilter, setKindFilter] = useState<NodeKind | "all">("ci");
  const [ciTypeFilter, setCITypeFilter] = useState<CIType | "all">("service");
  const [downOnly, setDownOnly] = useState(false);
  const [search, setSearch] = useState("");
  const [selectedNodeID, setSelectedNodeID] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const loaded = useRef(false);
  const impactedCIIds = downCIIds(nodes, relationships);

  useEffect(() => {
    if (!metadata) return;
    let cancelled = false;
    if (!loaded.current) setLoading(true);
    Promise.all([
      Promise.allSettled(metadata.nodeKinds.map((kind) => getNodes(kind, includeRetired))),
      Promise.allSettled(metadata.relationships.map((relationship) => getRelationships(relationship.kind, includeRetired))),
      getAccessAnomalies().catch(() => ({ anomalies: [] })),
    ]).then(([nodeGroups, relationshipGroups, report]) => {
      if (cancelled) return;
      const nextNodes = nodeGroups.flatMap((result) => result.status === "fulfilled" ? result.value : []);
      const nextRelationships = relationshipGroups.flatMap((result) => result.status === "fulfilled" ? result.value : []);
      if (nextNodes.length === 0 && nodeGroups.every((result) => result.status === "rejected")) {
        const first = nodeGroups.find((result) => result.status === "rejected");
        throw first && first.status === "rejected" ? first.reason : new Error("Could not load graph data.");
      }
      setNodes(nextNodes);
      setRelationships(nextRelationships);
      setAnomalyIdentityIDs(new Set(report.anomalies.map((anomaly) => anomaly.identity.id)));
      loaded.current = true;
      setError("");
    }).catch((requestError: unknown) => {
      if (cancelled) return;
      if (loaded.current) return;
      setError(requestError instanceof Error ? requestError.message : "Could not load graph data.");
      setNodes([]);
      setRelationships([]);
      setAnomalyIdentityIDs(new Set());
    }).finally(() => {
      if (!cancelled) setLoading(false);
    });
    return () => { cancelled = true; };
  }, [metadata, includeRetired, reload]);

  const query = search.trim().toLowerCase();
  const filtersCIs = kindFilter === "all" || kindFilter === "ci";
  const matchedNodes = nodes.filter((node) => {
    if (kindFilter !== "all" && node.kind !== kindFilter) return false;
    if (filtersCIs && ciTypeFilter !== "all" && node.kind === "ci" && node.properties?.ciType !== ciTypeFilter) return false;
    if (downOnly) {
      // Down-only view is CI-centric: hide everything that is not currently down.
      if (node.kind !== "ci" || !impactedCIIds.has(node.id)) return false;
    }
    if (!query) return true;
    return JSON.stringify(node).toLowerCase().includes(query);
  });
  // A search or type filter narrows the map to matching records, but once a
  // record is selected its direct connections are pulled back in so the
  // selection is never shown as an isolated node. Down-only keeps the view
  // limited to impacted CIs.
  const visibleIDs = new Set(matchedNodes.map(nodeID));
  if (selectedNodeID && nodes.some((node) => nodeID(node) === selectedNodeID) && !downOnly) {
    visibleIDs.add(selectedNodeID);
    for (const relationship of relationships) {
      const definition = metadata?.relationships.find((item) => item.kind === relationship.kind);
      if (!definition) continue;
      const fromID = relationshipSourceKey(definition, relationship.fromId, nodes);
      const toID = relationshipTargetKey(definition, relationship.toId, nodes);
      if (fromID === selectedNodeID) visibleIDs.add(toID);
      else if (toID === selectedNodeID) visibleIDs.add(fromID);
    }
  }
  const filteredNodes = nodes.filter((node) => visibleIDs.has(nodeID(node)));
  const filteredRelationships = relationships.filter((relationship) => {
    const definition = metadata?.relationships.find((item) => item.kind === relationship.kind);
    return definition && visibleIDs.has(relationshipSourceKey(definition, relationship.fromId, nodes)) && visibleIDs.has(relationshipTargetKey(definition, relationship.toId, nodes));
  });
  const selectedNode = nodes.find((node) => nodeID(node) === selectedNodeID) ?? null;
  const upstream = selectedNode && metadata ? traceRelationships(selectedNode, "upstream", nodes, relationships, metadata.relationships) : [];
  const downstream = selectedNode && metadata ? traceRelationships(selectedNode, "downstream", nodes, relationships, metadata.relationships) : [];
  const graph = metadata ? layoutGraph(filteredNodes, filteredRelationships, metadata.relationships, selectedNodeID, anomalyIdentityIDs, impactedCIIds, setSelectedNodeID) : { nodes: [], edges: [] };

  return <section className="map-section" aria-label="Configuration graph map">
    <div className="map-toolbar">
      <label className="search-box map-search"><Search aria-hidden="true" size={17} /><input value={search} onChange={(event) => setSearch(event.target.value)} type="search" placeholder="Find a record or attribute" /></label>
      <label className="map-kind-filter"><span>Record type</span><select value={kindFilter} onChange={(event) => { setKindFilter(event.target.value as NodeKind | "all"); setSelectedNodeID(null); }}><option value="all">All types</option>{(metadata?.nodeKinds ?? []).map((kind) => <option key={kind} value={kind}>{kind === "ci" ? "CI" : kind}</option>)}</select></label>
      {filtersCIs ? <label className="map-kind-filter"><span>CI type</span><select value={ciTypeFilter} onChange={(event) => { setCITypeFilter(event.target.value as CIType | "all"); setSelectedNodeID(null); }}><option value="all">All CI types</option>{(metadata?.ciTypes ?? []).map((ciType) => <option key={ciType} value={ciType}>{titleCase(ciType)}</option>)}</select></label> : null}
      <label className="retired-toggle"><input type="checkbox" checked={includeRetired} onChange={onToggleIncludeRetired} /><span>Include retired</span></label>
      <label className="retired-toggle"><input type="checkbox" checked={downOnly} onChange={(event) => {
        const enabled = event.target.checked;
        setDownOnly(enabled);
        setSelectedNodeID(null);
        // Show every impacted CI type when focusing on outages.
        if (enabled) { setKindFilter("ci"); setCITypeFilter("all"); }
      }} /><span>Down only</span></label>
      <span className="map-totals">{impactedCIIds.size > 0 ? `${impactedCIIds.size} down · ` : ""}{nodes.length} nodes · {filteredRelationships.length} links</span>
    </div>
    <div className={`map-workspace ${selectedNode ? "has-inspector" : ""}`}>
      <div className="map-canvas">
        {loading && <div className="map-overlay">Loading graph…</div>}
        {!loading && error && <div className="map-overlay map-error">{error}</div>}
        {!loading && !error && graph.nodes.length === 0 && <div className="map-overlay">No graph records match this view.</div>}
        <ReactFlow
          nodes={graph.nodes}
          edges={graph.edges}
          nodeTypes={nodeTypes}
          edgeTypes={edgeTypes}
          fitView
          fitViewOptions={{ padding: 0.18, maxZoom: 1.15 }}
          minZoom={0.15}
          maxZoom={1.6}
          nodesConnectable={false}
          proOptions={{ hideAttribution: false }}
          aria-label="Interactive configuration graph"
        >
          <FitToVisible signature={`${reload}:${loading ? "loading" : "ready"}:${graph.nodes.map((node) => node.id).join("|")}`} />
          <Background variant={BackgroundVariant.Dots} gap={20} size={1} color="#d5e0ec" />
          <MiniMap nodeColor={(node) => {
            const data = node.data as MapNodeData;
            if (data.down) return "#c0392b";
            return kindTones[data.kind] === "yellow" ? "#f2c84b" : kindTones[data.kind] === "red" ? "#d9685f" : kindTones[data.kind] === "green" ? "#59a779" : "#2871ba";
          }} maskColor="rgba(244,247,250,0.72)" />
          <Controls showInteractive={false} />
        </ReactFlow>
      </div>
      {selectedNode && <SelectedNodePanel
        node={selectedNode}
        upstream={upstream}
        downstream={downstream}
        definitions={metadata?.relationships ?? []}
        hasAnomaly={selectedNode.kind === "identity" && anomalyIdentityIDs.has(selectedNode.id)}
        isDown={selectedNode.kind === "ci" && impactedCIIds.has(selectedNode.id)}
        onClose={() => setSelectedNodeID(null)}
        onSelect={(node) => setSelectedNodeID(nodeID(node))}
        onCreateLinked={onCreateLinked}
        onEdit={onEdit}
      />}
    </div>
    <div className="map-legend" aria-label="Map legend">
      <span><i className="legend-swatch ci" />CI</span>
      <span><i className="legend-swatch incident" />Incident</span>
      <span><i className="legend-swatch change" />Change</span>
      <span><i className="legend-swatch event" />Event</span>
      <span className="map-legend-down"><ArrowDown size={12} strokeWidth={2.6} aria-hidden="true" />Node down</span>
      <span className="map-legend-anomaly"><CircleAlert size={12} strokeWidth={2.4} aria-hidden="true" />Access anomaly</span>
      <span className="map-count">{filteredNodes.length} visible records</span>
    </div>
  </section>;
}