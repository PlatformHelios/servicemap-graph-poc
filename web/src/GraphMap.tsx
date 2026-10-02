import { useEffect, useState } from "react";
import {
  Background,
  BackgroundVariant,
  Controls,
  Handle,
  MarkerType,
  MiniMap,
  Position,
  ReactFlow,
  type Edge,
  type Node,
  type NodeProps,
} from "@xyflow/react";
import { ArrowDownLeft, ArrowUpRight, Search, X } from "lucide-react";
import dagre from "dagre";
import { getNodes, getRelationships } from "./api";
import type { GraphNode, GraphRelationship, Metadata, NodeKind } from "./types";
import "@xyflow/react/dist/style.css";

interface MapNodeData extends Record<string, unknown> {
  label: string;
  kind: NodeKind;
  status: "active" | "retired";
  detail: string;
  selected: boolean;
  onSelect: (id: string) => void;
}

type MapFlowNode = Node<MapNodeData, "mapRecord">;

interface RelationshipTrace {
  relationship: GraphRelationship;
  adjacentNode: GraphNode;
  depth: number;
  source: GraphNode;
  target: GraphNode;
}

const nodeWidth = 226;
const nodeHeight = 92;
const nodeTypes = { mapRecord: MapRecordNode };

const kindTones: Record<NodeKind, string> = {
  identity: "blue",
  "job-code": "slate",
  birthright: "yellow",
  role: "blue",
  entitlement: "yellow",
  ci: "blue",
  incident: "red",
  change: "yellow",
  event: "green",
};

function nodeID(node: GraphNode) {
  return `${node.kind}:${node.id}`;
}

function displayName(node: GraphNode) {
  const properties = node.properties ?? {};
  if (node.kind === "ci" && typeof properties.ciType === "string") {
    return `${String(properties.name || properties.title || node.id)} · ${properties.ciType}`;
  }
  return String(properties.name || properties.title || properties.department || node.id);
}

function titleCase(value: string) {
	return value.replaceAll("-", " ").replace(/\b\w/g, (character) => character.toUpperCase());
}

function layoutGraph(nodes: GraphNode[], relationships: GraphRelationship[], definitions: Metadata["relationships"], selectedNodeID: string | null, onSelect: (id: string) => void) {
  const known = new Map(nodes.map((node) => [nodeID(node), node]));
  const definitionByKind = new Map(definitions.map((definition) => [definition.kind, definition]));
  const dagreGraph = new dagre.graphlib.Graph();
  dagreGraph.setDefaultEdgeLabel(() => ({}));
  dagreGraph.setGraph({ rankdir: "LR", nodesep: 40, ranksep: 100, marginx: 28, marginy: 28 });

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
        onSelect,
      },
    };
  });

  const flowEdges: Edge[] = [];
  for (const relationship of relationships) {
    const definition = definitionByKind.get(relationship.kind);
    if (!definition) continue;
    const source = `${definition.from}:${relationship.fromId}`;
    const target = `${definition.to}:${relationship.toId}`;
    if (!known.has(source) || !known.has(target)) continue;
    const id = `${relationship.kind}:${source}:${target}`;
    dagreGraph.setEdge(source, target, { id });
    flowEdges.push({
      id,
      source,
      target,
      type: "smoothstep",
      label: relationship.kind,
      labelStyle: { fill: "#536b86", fontSize: 10, fontFamily: "Cascadia Code, Consolas, monospace" },
      labelBgStyle: { fill: "#f4f7fa", fillOpacity: 0.96 },
      markerEnd: { type: MarkerType.ArrowClosed, color: relationship.status === "retired" ? "#a5b0bf" : "#2871ba" },
      style: { stroke: relationship.status === "retired" ? "#a5b0bf" : "#2871ba", strokeWidth: 1.7 },
      animated: false,
    });
  }

  dagre.layout(dagreGraph);
  const positionedNodes = flowNodes.map((node) => {
    const position = dagreGraph.node(node.id);
    return { ...node, position: { x: position.x - nodeWidth / 2, y: position.y - nodeHeight / 2 } };
  });
  return { nodes: positionedNodes, edges: flowEdges };
}

function MapRecordNode({ id, data }: NodeProps<MapFlowNode>) {
  return <div className={`map-record-node tone-${kindTones[data.kind]} ${data.status === "retired" ? "map-record-retired" : ""} ${data.selected ? "map-record-selected" : ""}`} onClick={(event) => { event.stopPropagation(); data.onSelect(id); }}>
    <Handle type="target" position={Position.Left} isConnectable={false} />
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
      const sourceID = `${definition.from}:${relationship.fromId}`;
      const targetID = `${definition.to}:${relationship.toId}`;
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
  const name = properties.name || properties.title || properties.department || node.id;
  const type = node.kind === "ci" && typeof properties.ciType === "string"
    ? `CI · ${titleCase(properties.ciType)}`
    : titleCase(node.kind);
  return { name: String(name), type };
}

function RelationshipTraceList({ direction, traces, onSelect }: {
  direction: "upstream" | "downstream";
  traces: RelationshipTrace[];
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
      return <button className="trace-item" key={`${relationship.kind}:${relationship.fromId}:${relationship.toId}`} onClick={() => onSelect(adjacentNode)}>
        <span className="trace-item-icon"><Icon size={14} /></span>
        <span className="trace-item-content">
          <small>{relationship.kind} · {depth === 1 ? "DIRECT" : `DEPTH ${depth}`}</small>
          <strong>{sourceLabel.name} <span>→</span> {targetLabel.name}</strong>
          <em>{adjacentLabel.type} · {adjacentNode.id}</em>
        </span>
      </button>;
    })}
  </div>;
}

function SelectedNodePanel({ node, upstream, downstream, onClose, onSelect }: {
  node: GraphNode;
  upstream: RelationshipTrace[];
  downstream: RelationshipTrace[];
  onClose: () => void;
  onSelect: (node: GraphNode) => void;
}) {
  const label = nodeLabel(node);
  return <aside className="map-inspector" aria-label="Selected record relationships">
    <header className="map-inspector-header">
      <div><p className="eyebrow">SELECTED RECORD</p><h2>{label.name}</h2></div>
      <button className="close-button" type="button" onClick={onClose} aria-label="Close relationship details"><X size={16} /></button>
    </header>
    <div className="map-inspector-meta"><span>{label.type}</span><span className={`status-pill status-${node.status}`}>{node.status}</span></div>
    <code className="map-inspector-id">{node.id}</code>
    <section className="trace-section">
      <div className="trace-section-heading"><h3>Upstream</h3><span>{upstream.length}</span></div>
      <RelationshipTraceList direction="upstream" traces={upstream} onSelect={onSelect} />
    </section>
    <section className="trace-section">
      <div className="trace-section-heading"><h3>Downstream</h3><span>{downstream.length}</span></div>
      <RelationshipTraceList direction="downstream" traces={downstream} onSelect={onSelect} />
    </section>
  </aside>;
}

export default function GraphMap({ metadata, includeRetired, reload, onToggleIncludeRetired }: {
  metadata: Metadata | null;
  includeRetired: boolean;
  reload: number;
  onToggleIncludeRetired: () => void;
}) {
  const [nodes, setNodes] = useState<GraphNode[]>([]);
  const [relationships, setRelationships] = useState<GraphRelationship[]>([]);
  const [kindFilter, setKindFilter] = useState<NodeKind | "all">("all");
  const [search, setSearch] = useState("");
  const [selectedNodeID, setSelectedNodeID] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!metadata) return;
    let active = true;
    setLoading(true);
    Promise.all([
      Promise.all(metadata.nodeKinds.map((kind) => getNodes(kind, includeRetired))),
      Promise.all(metadata.relationships.map((relationship) => getRelationships(relationship.kind, includeRetired))),
    ]).then(([nodeGroups, relationshipGroups]) => {
      if (!active) return;
      setNodes(nodeGroups.flat());
      setRelationships(relationshipGroups.flat());
      setError("");
    }).catch((requestError: unknown) => {
      if (!active) return;
      setError(requestError instanceof Error ? requestError.message : "Could not load graph data.");
      setNodes([]);
      setRelationships([]);
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [metadata, includeRetired, reload]);

  const query = search.trim().toLowerCase();
  const filteredNodes = nodes.filter((node) => {
    if (kindFilter !== "all" && node.kind !== kindFilter) return false;
    if (!query) return true;
    return JSON.stringify(node).toLowerCase().includes(query);
  });
  const visibleIDs = new Set(filteredNodes.map(nodeID));
  const filteredRelationships = relationships.filter((relationship) => {
    const definition = metadata?.relationships.find((item) => item.kind === relationship.kind);
    return definition && visibleIDs.has(`${definition.from}:${relationship.fromId}`) && visibleIDs.has(`${definition.to}:${relationship.toId}`);
  });
  const selectedNode = nodes.find((node) => nodeID(node) === selectedNodeID) ?? null;
  const upstream = selectedNode && metadata ? traceRelationships(selectedNode, "upstream", nodes, relationships, metadata.relationships) : [];
  const downstream = selectedNode && metadata ? traceRelationships(selectedNode, "downstream", nodes, relationships, metadata.relationships) : [];
  const graph = metadata ? layoutGraph(filteredNodes, filteredRelationships, metadata.relationships, selectedNodeID, setSelectedNodeID) : { nodes: [], edges: [] };

  return <section className="map-section" aria-label="Configuration graph map">
    <div className="map-toolbar">
      <label className="search-box map-search"><Search aria-hidden="true" size={17} /><input value={search} onChange={(event) => setSearch(event.target.value)} type="search" placeholder="Find a record or attribute" /></label>
      <label className="map-kind-filter"><span>Record type</span><select value={kindFilter} onChange={(event) => setKindFilter(event.target.value as NodeKind | "all")}><option value="all">All types</option>{(metadata?.nodeKinds ?? []).map((kind) => <option key={kind} value={kind}>{kind === "ci" ? "CI" : kind}</option>)}</select></label>
      <label className="retired-toggle"><input type="checkbox" checked={includeRetired} onChange={onToggleIncludeRetired} /><span>Include retired</span></label>
      <span className="map-totals">{nodes.length} nodes · {filteredRelationships.length} links</span>
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
          fitView
          fitViewOptions={{ padding: 0.18, maxZoom: 1.15 }}
          minZoom={0.15}
          maxZoom={1.6}
          nodesConnectable={false}
          proOptions={{ hideAttribution: false }}
          aria-label="Interactive configuration graph"
        >
          <Background variant={BackgroundVariant.Dots} gap={20} size={1} color="#d5e0ec" />
          <MiniMap nodeColor={(node) => {
            const kind = (node.data as MapNodeData).kind;
            return kindTones[kind] === "yellow" ? "#f2c84b" : kindTones[kind] === "red" ? "#d9685f" : kindTones[kind] === "green" ? "#59a779" : "#2871ba";
          }} maskColor="rgba(244,247,250,0.72)" />
          <Controls showInteractive={false} />
        </ReactFlow>
      </div>
      {selectedNode && <SelectedNodePanel
        node={selectedNode}
        upstream={upstream}
        downstream={downstream}
        onClose={() => setSelectedNodeID(null)}
        onSelect={(node) => setSelectedNodeID(nodeID(node))}
      />}
    </div>
    <div className="map-legend" aria-label="Map legend">
      <span><i className="legend-swatch ci" />CI</span>
      <span><i className="legend-swatch incident" />Incident</span>
      <span><i className="legend-swatch change" />Change</span>
      <span><i className="legend-swatch event" />Event</span>
      <span className="map-count">{filteredNodes.length} visible records</span>
    </div>
  </section>;
}