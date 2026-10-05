export type NodeKind =
  | "identity"
  | "job-code"
  | "birthright"
  | "role"
  | "entitlement"
  | "group"
  | "ci"
  | "incident"
  | "change"
  | "event"
  | "request"
  | "workflow"
  | "workflow-step"
  | "workflow-run"
  | "task";

// Catalog requests capture a CI before it exists. Drafts may be incomplete;
// submitting requires everything the target CI type needs. Submitting a form
// with an enabled workflow moves it to in-review; the last step fulfils it.
// Access requests ask for roles or entitlements; they are raised complete and
// end fulfilled (something provisioned) or denied (every item refused).
export type RequestType = "vendor" | "access";
export type RequestState = "draft" | "submitted" | "in-review" | "fulfilled" | "denied";

export type CIType = "server" | "printer" | "data-connector" | "application" | "service" | "process" | "function" | "location" | "contract" | "vendor";

export type CICategory = "business" | "technology" | "security";

export type ContractType = "basic-contract" | "msa" | "nda";
export type HostingModel = "internal" | "external";
export type Criticality = "critical" | "important" | "business-support";

// Forward relationship kinds. Each is stored once; the inverse label (how the
// edge reads from the "to" side) comes from Metadata.relationships[].inverse.
export type RelationshipKind =
  | "has-job-code"
  | "work-location"
  | "member"
  | "drafted-request"
  | "form-submitted"
  | "requested-for"
  | "requests-access"
  | "has-step"
  | "next-step"
  | "step-assigned-to"
  | "run-for"
  | "instance-of"
  | "task-for"
  | "task-step"
  | "task-item"
  | "task-assigned-to"
  | "acted-by"
  | "fulfilled-by"
  | "qualifies-for"
  | "grants"
  | "includes"
  | "has-role"
  | "entitled-by"
  | "permissions"
  | "affects"
  | "changes"
  | "assigned-to"
  | "observed-on"
  | "depends-on"
  | "hosts"
  | "uses"
  | "governs"
  | "provides"
  | "accountable"
  | "responsible"
  | "consulted"
  | "informed";

// Workflow definitions (built in the Workflow Creator) and the tasks their
// runs raise. Steps run in order; each exposes a slice of the request form to
// its assignees (identities or groups).
export type StepType = "review" | "approval";
export type ApprovalRule = "any" | "all";
// item-accountable: each task goes to the Accountable of the requested role or
// entitlement it is about (the access workflow's approval step).
export type AssigneeRule = "item-accountable";
export type TaskStatus = "pending" | "completed" | "approved" | "rejected";
export type TaskAction = "complete" | "approve" | "reject";
export type RunStatus = "active" | "completed" | "returned";

export interface Assignee {
  id: string;
  kind: "identity" | "group";
  name?: string;
}

export interface WorkflowStepDefinition {
  id?: string;
  order?: number;
  name: string;
  stepType: StepType;
  instructions?: string;
  approvalRule?: ApprovalRule;
  editableFields?: string[];
  requiredFields?: string[];
  assigneeIds: string[];
  assignees?: Assignee[];
  assigneeRule?: AssigneeRule;
  olaEnabled?: boolean;
  olaBusinessDays?: number;
}

// Access requests: where one requested role or entitlement has got to.
export type ItemDecision = "pending" | "approved" | "denied" | "fulfilled";
export type AccessItemKind = "role" | "entitlement";

export interface AccessItemView {
  id: string;
  kind: AccessItemKind;
  name?: string;
  applicationId?: string;
  applicationName?: string;
  note?: string;
  decision?: ItemDecision;
  decidedAt?: string;
  decidedBy?: string;
  fulfilledAt?: string;
}

export interface AccessOption {
  id: string;
  kind: AccessItemKind;
  name?: string;
  description?: string;
  owner: Assignee;
}

export interface AccessApplication {
  id: string;
  name?: string;
  roles: AccessOption[];
  entitlements: AccessOption[];
}

export interface AccessHolding {
  id: string;
  kind: AccessItemKind;
  name?: string;
  via: "birthright" | "role" | "direct" | "pending";
  source?: string;
}

export interface AccessOptions {
  identity: Assignee;
  applications: AccessApplication[];
  held: AccessHolding[];
}

// A named graph record reference (identity, birthright, role, or entitlement).
export interface AccessAnomalyRef {
  id: string;
  kind: string;
  name?: string;
}

// Identity holds the same role/entitlement through more than one path
// (birthright, included by a held role, and/or direct permissions).
export interface AccessAnomaly {
  identity: AccessAnomalyRef;
  item: AccessAnomalyRef;
  birthrights: AccessAnomalyRef[];
  roles: AccessAnomalyRef[];
  paths: string[];
}

// The stored result of the last anomaly check; computedAt is empty until the
// server has run it once.
export interface AccessAnomalyReport {
  computedAt?: string;
  anomalies: AccessAnomaly[];
}

// One page of a list plus how many records matched before paging.
export interface Page<T> {
  items: T[];
  total: number;
  active: number;
}

export interface AccessRequestInput {
  requestedById: string;
  requestForId: string;
  items: { id: string; kind: AccessItemKind; note?: string }[];
  slaEnabled?: boolean;
  slaBusinessDays?: number;
}

// Resolved platform access for the signed-in actor (from held Anthos entitlements).
export interface ActorAccess {
  actorId: string;
  superAdmin: boolean;
  entitlements: string[];
  capabilities: string[];
}

export interface WorkflowDefinition {
  id?: string;
  name: string;
  description?: string;
  requestType: RequestType;
  enabled: boolean;
  status?: "active" | "retired";
  slaEnabled?: boolean;
  slaBusinessDays?: number;
  steps: WorkflowStepDefinition[];
}

export interface TaskActionRecord {
  actorId: string;
  actorName?: string;
  action: TaskStatus;
  comment?: string;
  actedAt: string;
}

export interface TaskView {
  id: string;
  status: TaskStatus;
  createdAt: string;
  completedAt?: string;
  retiredAt?: string; // the task record is retired (its run completed, or it was retired by hand)
  step: WorkflowStepDefinition;
  finalStep: boolean;
  runId: string;
  runStatus: RunStatus;
  workflowId: string;
  workflowName: string;
  request: GraphNode;
  requester?: Assignee;
  assignees: Assignee[];
  eligibleActors: Assignee[];
  actions: TaskActionRecord[];
  // Access requests only: who the access is for, the item this task decides or
  // provisions, and every item on the request with its decision.
  requestedFor?: Assignee;
  item?: AccessItemView;
  items?: AccessItemView[];
  sla?: AgreementClock;
  ola?: AgreementClock;
}

export interface TaskActionInput {
  actorId: string;
  action: TaskAction;
  comment?: string;
  properties?: Properties;
}

export type PropertyValue = string | number | boolean;

export type Properties = Record<string, PropertyValue>;

export type AgreementKind = "sla" | "ola";
export type AgreementStatus = "none" | "on-track" | "due-today" | "overdue" | "met" | "missed";

export interface AgreementClock {
  kind: AgreementKind;
  enabled: boolean;
  businessDays?: number;
  startedAt?: string;
  dueAt?: string;
  elapsedDays: number;
  remainingDays: number;
  progress: number;
  status: AgreementStatus;
  overdue: boolean;
}

export interface Holiday {
  date: string;
  name?: string;
}

export interface GraphRelationship {
  kind: RelationshipKind;
  fromId: string;
  toId: string;
  fromName?: string; // endpoint display names, supplied with a node's direct relationships
  toName?: string;
  properties?: Properties;
  status: "active" | "retired";
  retiredAt?: string;
}

export interface GraphNode {
  kind: NodeKind;
  id: string;
  properties?: Properties;
  status: "active" | "retired";
  retiredAt?: string;
  relationships?: GraphRelationship[];
}

export interface RelationshipDefinition {
  kind: RelationshipKind;
  inverse: string;
  from: NodeKind;
  to: NodeKind;
  fromCiTypes?: CIType[];
  toCiTypes?: CIType[];
  fromKinds?: NodeKind[]; // present when the from node may be more than one kind
  toKinds?: NodeKind[]; // present when the to node may be more than one kind
  ciTypeRules?: CITypeRule[]; // present when the allowed to types depend on the from type
}

export interface CITypeRule {
  from: CIType[]; // empty = any CI type
  to: CIType[]; // empty = any CI type
}

export interface Metadata {
  nodeKinds: NodeKind[];
  ciTypes: CIType[];
  ciCategories?: CICategory[];
  contractTypes?: ContractType[];
  hostingModels?: HostingModel[];
  criticalities?: Criticality[];
  requestTypes?: RequestType[];
  requestStates?: RequestState[];
  requestFields?: Partial<Record<RequestType, string[]>>; // form fields a step may expose
  fulfilmentFields?: Partial<Record<RequestType, string[]>>; // CI fields a workflow must collect
  stepTypes?: StepType[];
  approvalRules?: ApprovalRule[];
  taskActions?: TaskAction[];
  raciKinds?: NodeKind[]; // kinds that carry RACI ownership
  accessItemKinds?: NodeKind[]; // kinds an access request can ask for
  accessWorkflowTemplate?: WorkflowDefinition; // the fixed access workflow, ready for fulfilment assignees
  relationships: RelationshipDefinition[];
}

export type Resource = NodeKind | "home" | "relationships" | "map" | "location-map" | "workflow-creator" | "workflow-tasks" | "access-request";

export type RecordEditorTarget =
  | { type: "node"; kind: NodeKind; record?: GraphNode; defaults?: Properties; ciIds?: string[]; assignedTo?: string[] }
  | { type: "relationship"; kind?: RelationshipKind; record?: GraphRelationship };

export type RetireTarget =
  | { type: "node"; kind: NodeKind; record: GraphNode }
  | { type: "relationship"; kind: RelationshipKind; record: GraphRelationship };