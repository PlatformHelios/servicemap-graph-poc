import type {
  GraphNode,
  GraphRelationship,
  Metadata,
  NodeKind,
  Properties,
  RelationshipKind,
} from "./types";

interface NodeCreate {
  id: string;
  properties: Properties;
}

interface RelationshipCreate {
  fromId: string;
  toId: string;
  properties: Properties;
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: {
      ...(init.body ? { "Content-Type": "application/json" } : {}),
      ...init.headers,
    },
  });
  const result: unknown = await response.json().catch(() => ({}));
  if (!response.ok) {
    const message = typeof result === "object" && result !== null && "error" in result
      ? String(result.error)
      : `Request failed (${response.status})`;
    throw new Error(message);
  }
  return result as T;
}

export function getMetadata(): Promise<Metadata> {
  return request<Metadata>("/api/meta");
}

export function getNodes(kind: NodeKind, includeRetired: boolean): Promise<GraphNode[]> {
  const query = includeRetired ? "?includeRetired=true" : "";
  return request<GraphNode[]>(`/api/nodes/${encodeURIComponent(kind)}${query}`);
}

export function getRelationships(kind: RelationshipKind, includeRetired: boolean): Promise<GraphRelationship[]> {
  const query = includeRetired ? "?includeRetired=true" : "";
  return request<GraphRelationship[]>(`/api/relationships/${encodeURIComponent(kind)}${query}`);
}

export function createNode(kind: NodeKind, input: NodeCreate): Promise<GraphNode> {
  return request<GraphNode>(`/api/nodes/${encodeURIComponent(kind)}`, {
    method: "POST",
    body: JSON.stringify(input),
  });
}

export function updateNode(kind: NodeKind, id: string, properties: Properties): Promise<GraphNode> {
  return request<GraphNode>(`/api/nodes/${encodeURIComponent(kind)}/${encodeURIComponent(id)}`, {
    method: "PATCH",
    body: JSON.stringify({ properties }),
  });
}

export function createRelationship(kind: RelationshipKind, input: RelationshipCreate): Promise<GraphRelationship> {
  return request<GraphRelationship>(`/api/relationships/${encodeURIComponent(kind)}`, {
    method: "POST",
    body: JSON.stringify(input),
  });
}

export function updateRelationship(
  kind: RelationshipKind,
  fromId: string,
  toId: string,
  properties: Properties,
): Promise<GraphRelationship> {
  return request<GraphRelationship>(
    `/api/relationships/${encodeURIComponent(kind)}/${encodeURIComponent(fromId)}/${encodeURIComponent(toId)}`,
    { method: "PATCH", body: JSON.stringify({ properties }) },
  );
}

export function retireNode(kind: NodeKind, id: string): Promise<GraphNode> {
  return request<GraphNode>(`/api/nodes/${encodeURIComponent(kind)}/${encodeURIComponent(id)}`, { method: "DELETE" });
}

export function retireRelationship(kind: RelationshipKind, fromId: string, toId: string): Promise<GraphRelationship> {
  return request<GraphRelationship>(
    `/api/relationships/${encodeURIComponent(kind)}/${encodeURIComponent(fromId)}/${encodeURIComponent(toId)}`,
    { method: "DELETE" },
  );
}