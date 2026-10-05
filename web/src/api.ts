import type {
  AccessAnomalyReport,
  AccessOptions,
  AccessRequestInput,
  ActorAccess,
  GraphNode,
  GraphRelationship,
  Metadata,
  NodeKind,
  Page,
  Properties,
  RelationshipDefinition,
  RelationshipKind,
  TaskActionInput,
  TaskView,
  WorkflowDefinition,
  Holiday,
} from "./types";
import { storedActingAs, superAdmin } from "./session";

// Who the API should authorize as. The UI sets this whenever the mocked
// sign-in or act-as identity changes; blank means the platform super admin.
let actorId = storedActingAs() || superAdmin.id;

export function setApiActor(id: string) {
  actorId = id.trim() || superAdmin.id;
}

export function currentApiActor(): string {
  return actorId;
}

interface NodeCreate {
  properties: Properties;
  ciIds?: string[];
}

interface RelationshipCreate {
  fromId: string;
  toId: string;
  properties: Properties;
}

// Human-readable name for a node, or undefined when it has none. Job codes
// combine their Job ID and title as "ID - Title".
export function nodeDisplayName(node: GraphNode): string | undefined {
  const properties = node.properties ?? {};
  if (node.kind === "job-code") {
    const parts = [properties.jobId, properties.name]
      .filter((part) => typeof part === "string" && part.trim())
      .map((part) => String(part).trim());
    if (parts.length > 0) return parts.join(" - ");
  }
  const name = properties.name || properties.title || properties.department;
  return name === undefined || name === "" ? undefined : String(name);
}

// How a relationship reads from one endpoint's point of view: the forward kind
// when viewed from the "from" node, the inverse label when viewed from the "to" node.
export function relationshipLabel(
  relationship: Pick<GraphRelationship, "kind">,
  viewedFromSource: boolean,
  definitions: RelationshipDefinition[] | undefined,
): string {
  if (viewedFromSource) return relationship.kind;
  return definitions?.find((definition) => definition.kind === relationship.kind)?.inverse ?? relationship.kind;
}

// Every node kind a relationship's "to" end may have (most kinds have exactly one).
export function relationshipToKinds(definition: Pick<RelationshipDefinition, "to" | "toKinds">): NodeKind[] {
  return definition.toKinds && definition.toKinds.length > 0 ? definition.toKinds : [definition.to];
}

// Every node kind a relationship's "from" end may have (RACI kinds and permissions have several).
export function relationshipFromKinds(definition: Pick<RelationshipDefinition, "from" | "fromKinds">): NodeKind[] {
  return definition.fromKinds && definition.fromKinds.length > 0 ? definition.fromKinds : [definition.from];
}

// Graph key ("kind:id") for a relationship's endpoint. When the endpoint may be
// one of several kinds, pick the kind that actually has a node with that id.
function endpointKey(kinds: NodeKind[], id: string, nodes: GraphNode[]): string {
  const kind = kinds.length === 1 ? kinds[0] : kinds.find((candidate) => nodes.some((node) => node.kind === candidate && node.id === id)) ?? kinds[0];
  return `${kind}:${id}`;
}

export function relationshipTargetKey(definition: RelationshipDefinition, toId: string, nodes: GraphNode[]): string {
  return endpointKey(relationshipToKinds(definition), toId, nodes);
}

export function relationshipSourceKey(definition: RelationshipDefinition, fromId: string, nodes: GraphNode[]): string {
  return endpointKey(relationshipFromKinds(definition), fromId, nodes);
}

async function requestWithHeaders<T>(path: string, init: RequestInit = {}): Promise<{ result: T; headers: Headers }> {
  const headers = new Headers(init.headers);
  if (init.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  headers.set("X-Actor-Id", actorId);
  const response = await fetch(path, { ...init, headers });
  const result: unknown = await response.json().catch(() => ({}));
  if (!response.ok) {
    const message = typeof result === "object" && result !== null && "error" in result
      ? String(result.error)
      : `Request failed (${response.status})`;
    throw new Error(message);
  }
  return { result: result as T, headers: response.headers };
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  return (await requestWithHeaders<T>(path, init)).result;
}

// A paged list: the body is the page, the totals ride in X-Total-Count /
// X-Active-Count. Without a limit the server returns everything.
async function requestPage<T>(path: string): Promise<Page<T>> {
  const { result, headers } = await requestWithHeaders<T[]>(path);
  const total = Number(headers.get("X-Total-Count") ?? result.length);
  const active = Number(headers.get("X-Active-Count") ?? total);
  return { items: result, total: Number.isNaN(total) ? result.length : total, active: Number.isNaN(active) ? result.length : active };
}

// Server-side list filters shared by node and relationship lists. `q` is a
// case-insensitive substring over ids and property values; limit/offset page
// the result (omit limit to read everything); involvedId and requestType
// narrow request lists to one identity's requests or one catalog form.
export interface ListQuery {
  includeRetired?: boolean;
  limit?: number;
  offset?: number;
  q?: string;
  involvedId?: string;
  requestType?: "vendor" | "access";
}

function listQueryString(query: ListQuery): string {
  const params = new URLSearchParams();
  if (query.includeRetired) params.set("includeRetired", "true");
  if (query.limit) params.set("limit", String(query.limit));
  if (query.offset) params.set("offset", String(query.offset));
  if (query.q?.trim()) params.set("q", query.q.trim());
  if (query.involvedId) params.set("involvedId", query.involvedId);
  if (query.requestType) params.set("requestType", query.requestType);
  const text = params.toString();
  return text ? `?${text}` : "";
}

export function getMetadata(): Promise<Metadata> {
  return request<Metadata>("/api/meta");
}

// Every record of a kind (pickers, the graph map). Lists shown as tables use getNodePage.
export function getNodes(kind: NodeKind, includeRetired: boolean): Promise<GraphNode[]> {
  return request<GraphNode[]>(`/api/nodes/${encodeURIComponent(kind)}${listQueryString({ includeRetired })}`);
}

export function getNodePage(kind: NodeKind, query: ListQuery): Promise<Page<GraphNode>> {
  return requestPage<GraphNode>(`/api/nodes/${encodeURIComponent(kind)}${listQueryString(query)}`);
}

export function getNode(kind: NodeKind, id: string, includeRetired = false): Promise<GraphNode> {
  const query = includeRetired ? "?includeRetired=true" : "";
  return request<GraphNode>(`/api/nodes/${encodeURIComponent(kind)}/${encodeURIComponent(id)}${query}`);
}

// Platform capabilities for the current X-Actor-Id (entitlement-driven, or full access for the super admin).
export function getCapabilities(): Promise<ActorAccess> {
  return request<ActorAccess>("/api/capabilities");
}

export function getRelationships(kind: RelationshipKind, includeRetired: boolean): Promise<GraphRelationship[]> {
  return request<GraphRelationship[]>(`/api/relationships/${encodeURIComponent(kind)}${listQueryString({ includeRetired })}`);
}

export function getRelationshipPage(kind: RelationshipKind, query: ListQuery): Promise<Page<GraphRelationship>> {
  return requestPage<GraphRelationship>(`/api/relationships/${encodeURIComponent(kind)}${listQueryString(query)}`);
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

export function getWorkflows(includeRetired: boolean): Promise<WorkflowDefinition[]> {
  const query = includeRetired ? "?includeRetired=true" : "";
  return request<WorkflowDefinition[]>(`/api/workflows${query}`);
}

// Creates the workflow when it has no id, otherwise replaces the saved definition.
export function saveWorkflow(workflow: WorkflowDefinition): Promise<WorkflowDefinition> {
  if (!workflow.id) {
    return request<WorkflowDefinition>("/api/workflows", { method: "POST", body: JSON.stringify(workflow) });
  }
  return request<WorkflowDefinition>(`/api/workflows/${encodeURIComponent(workflow.id)}`, {
    method: "PUT",
    body: JSON.stringify(workflow),
  });
}

export function retireWorkflow(id: string): Promise<WorkflowDefinition> {
  return request<WorkflowDefinition>(`/api/workflows/${encodeURIComponent(id)}`, { method: "DELETE" });
}

export interface TaskQuery {
  actorId?: string;
  requestId?: string;
  includeDone?: boolean;
  limit?: number;
  offset?: number;
}

function taskQueryString(filter: TaskQuery): string {
  const params = new URLSearchParams();
  if (filter.actorId) params.set("actorId", filter.actorId);
  if (filter.requestId) params.set("requestId", filter.requestId);
  if (filter.includeDone) params.set("includeDone", "true");
  if (filter.limit) params.set("limit", String(filter.limit));
  if (filter.offset) params.set("offset", String(filter.offset));
  const query = params.toString();
  return query ? `?${query}` : "";
}

export function getTasks(filter: TaskQuery): Promise<TaskView[]> {
  return request<TaskView[]>(`/api/tasks${taskQueryString(filter)}`);
}

export function getTaskPage(filter: TaskQuery): Promise<Page<TaskView>> {
  return requestPage<TaskView>(`/api/tasks${taskQueryString(filter)}`);
}

export function actOnTask(id: string, input: TaskActionInput): Promise<TaskView> {
  return request<TaskView>(`/api/tasks/${encodeURIComponent(id)}/actions`, {
    method: "POST",
    body: JSON.stringify(input),
  });
}

// What an identity may ask for on the Access Request Form, and what it already holds.
export function getAccessOptions(identityId: string): Promise<AccessOptions> {
  return request<AccessOptions>(`/api/access-options?identityId=${encodeURIComponent(identityId)}`);
}

// The stored snapshot of identities that hold the same access through more
// than one path. The server refreshes it on a schedule.
export function getAccessAnomalies(): Promise<AccessAnomalyReport> {
  return request<AccessAnomalyReport>("/api/access-anomalies");
}

// Reruns the anomaly check now (a whole-graph walk); offered in development only.
export function refreshAccessAnomalies(): Promise<AccessAnomalyReport> {
  return request<AccessAnomalyReport>("/api/access-anomalies/refresh", { method: "POST" });
}

// Raises an access request; there is no draft, it goes straight to its approvers.
export function createAccessRequest(input: AccessRequestInput): Promise<GraphNode> {
  return request<GraphNode>("/api/access-requests", { method: "POST", body: JSON.stringify(input) });
}

export function getFormSLA(requestType: "vendor" | "access"): Promise<{ enabled: boolean; businessDays: number }> {
  return request<{ enabled: boolean; businessDays: number }>(`/api/form-sla?requestType=${encodeURIComponent(requestType)}`);
}

export function getHolidays(): Promise<Holiday[]> {
  return request<Holiday[]>("/api/holidays");
}

export function saveHolidays(holidays: Holiday[]): Promise<Holiday[]> {
  return request<Holiday[]>("/api/holidays", { method: "PUT", body: JSON.stringify({ holidays }) });
}