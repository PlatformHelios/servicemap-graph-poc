import { Fragment, lazy, Suspense, useEffect, useRef, useState } from "react";
import {
  Archive,
  BriefcaseBusiness,
  ClipboardList,
  House,
  KeyRound,
  Link2,
  ListChecks,
  MapPin,
  Network,
  PackageOpen,
  Pencil,
  Plus,
  RefreshCw,
  Search,
  ShieldCheck,
  Users,
  UsersRound,
  Workflow,
  X,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import {
  createNode,
  createRelationship,
  getAccessAnomalies,
  getCapabilities,
  getMetadata,
  getNode,
  getNodePage,
  getNodes,
  getRelationshipPage,
  getTasks,
  getFormSLA,
  refreshAccessAnomalies,
  nodeDisplayName,
  relationshipFromKinds,
  relationshipToKinds,
  retireNode,
  retireRelationship,
  setApiActor,
  updateNode,
  updateRelationship,
} from "./api";
import { clampPage, defaultPageSize, Pager } from "./Pager";
import type {
  AccessAnomalyReport,
  ActorAccess,
  CIType,
  GraphNode,
  GraphRelationship,
  Metadata,
  NodeKind,
  Properties,
  RecordEditorTarget,
  RelationshipDefinition,
  RelationshipKind,
  RequestState,
  Resource,
  RetireTarget,
  TaskView,
} from "./types";

import { canCreateResource, canRetireResource, canUpdateResource, canViewResource } from "./capabilities";
import { formatDailyHours, hoursClosed, hoursKey, hoursStateFrom, locationCoordinates, locationOpenNow, weekdays, type HoursState } from "./locations";
import RecordPicker, { titleCase } from "./RecordPicker";
import { formatWhen, requestStateOf, requestStatePillClass, requestTypeOf, taskStatusPillClass } from "./workflows";
import { AgreementMeter, SLANotice } from "./AgreementMeter";
import { sessionUser, storeActingAs, storedActingAs, superAdmin } from "./session";
import UserMenu from "./UserMenu";

const hoursPropertyKeys = new Set(weekdays.map(hoursKey));

// RACI ownership on every CI, job code, birthright, role, and entitlement.
// Accountable is a single identity; the other roles accept identities and
// groups. Each role is a relationship kind from the owned record. The form
// keeps it on its own RACI tab: once set it is rarely revisited.
const raciOwnedKinds: NodeKind[] = ["ci", "job-code", "birthright", "role", "entitlement"];
type RACIRole = "accountable" | "responsible" | "consulted" | "informed";
type RACIAssignments = Record<RACIRole, string[]>;
const raciRoles: { role: RACIRole; label: string; hint: string }[] = [
  { role: "accountable", label: "Accountable", hint: "One identity who owns the outcome." },
  { role: "responsible", label: "Responsible", hint: "Identities or groups doing the work." },
  { role: "consulted", label: "Consulted", hint: "Identities or groups asked for input." },
  { role: "informed", label: "Informed", hint: "Identities or groups kept up to date." },
];
const emptyRACI: RACIAssignments = { accountable: [], responsible: [], consulted: [], informed: [] };

// Links a record owns (relationships leaving it) that a form edits alongside the
// record's properties, keyed by relationship kind. Saving reconciles these
// against what is stored.
type LinkAssignments = Partial<Record<RelationshipKind, string[]>>;
const incidentAssignmentKind: RelationshipKind = "assigned-to";

// Catalog requests are tied to their requester: drafted-request while a draft,
// swapped for form-submitted by the server when the request is submitted. Both
// point from the identity at the request, so the record is the link's target.
const draftedRequestKind: RelationshipKind = "drafted-request";
const formSubmittedKind: RelationshipKind = "form-submitted";
// A role or entitlement is offered on the Access Request Form through the one
// application CI it belongs to: the application has-role a role (role-for the
// application) and is entitled-by an entitlement (entitlement-for). Both links
// point from the application.
const hasRoleKind: RelationshipKind = "has-role";
const entitledByKind: RelationshipKind = "entitled-by";
function applicationLinkKind(kind: NodeKind): RelationshipKind {
  return kind === "entitlement" ? entitledByKind : hasRoleKind;
}
// An identity's birthrights come through its job code (has-job-code →
// qualifies-for → grants), so the identity form stores job codes, not birthrights.
const hasJobCodeKind: RelationshipKind = "has-job-code";
const qualifiesForKind: RelationshipKind = "qualifies-for";
const grantsKind: RelationshipKind = "grants";
// A role includes entitlements (includes / included-by).
const includesKind: RelationshipKind = "includes";
// A role or entitlement permissions an identity directly (permissions / permissioned-by).
const permissionsKind: RelationshipKind = "permissions";
// A group *member* identity; from the identity side the same edge reads member-of.
const memberKind: RelationshipKind = "member";
const incomingLinkKinds = new Set<RelationshipKind>([draftedRequestKind, formSubmittedKind, hasRoleKind, entitledByKind]);
const requestSystemKeys = ["requestType", "state", "submittedAt", "returnComment", "returnedAt", "fulfilledAt", "slaEnabled", "slaBusinessDays", "slaDueAt"];
const requestSLAKeys = ["slaEnabled", "slaBusinessDays", "slaDueAt"];

// Most form links are stored outgoing from the record being edited. A few are
// stored the other way (the picker is the "from" end); qualifies-for is both,
// depending on whether the job code or the birthright is open.
function isIncomingLink(kind: RelationshipKind, recordKind?: NodeKind) {
  return incomingLinkKinds.has(kind) || (kind === qualifiesForKind && recordKind === "birthright");
}

function linksFromRecord(record: GraphNode | undefined, kinds: RelationshipKind[]): Record<string, string[]> {
  const assignments: Record<string, string[]> = Object.fromEntries(kinds.map((kind) => [kind, []]));
  for (const relationship of record?.relationships ?? []) {
    if (relationship.status === "retired" || !(relationship.kind in assignments)) continue;
    if (isIncomingLink(relationship.kind, record?.kind)) {
      if (relationship.toId === record?.id) assignments[relationship.kind].push(relationship.fromId);
    } else if (relationship.fromId === record?.id) assignments[relationship.kind].push(relationship.toId);
  }
  return assignments;
}

// Who should work an incident raised against a CI: the CI's Responsible RACI.
// Groups are the assignment groups; identities are used when no group is set.
function assignmentFromResponsible(record: GraphNode | undefined): string[] {
  const ids = linksFromRecord(record, ["responsible"]).responsible;
  const groups = ids.filter((id) => id.startsWith("GRP-"));
  return groups.length > 0 ? groups : ids;
}

// The active records at the far end of a node's direct relationships of one kind.
function linkedEnds(record: GraphNode | undefined, kind: RelationshipKind, end: "from" | "to"): { id: string; name: string }[] {
  return (record?.relationships ?? [])
    .filter((relationship) => relationship.kind === kind && relationship.status !== "retired" && (end === "from" ? relationship.toId === record?.id : relationship.fromId === record?.id))
    .map((relationship) => end === "from" ? { id: relationship.fromId, name: relationship.fromName ?? relationship.fromId } : { id: relationship.toId, name: relationship.toName ?? relationship.toId });
}

// The list views show one kind-specific column in place of every direct
// relationship, which grows without bound (an application providing dozens of
// entitlements, a role including many). Each kind picks the few links that
// identify the record at a glance; the full set lives on the Graph Map inspector.
const summaryHeadings: Record<NodeKind, string> = {
  identity: "Job code and location",
  "job-code": "Birthrights and owner",
  birthright: "Grants and owner",
  role: "Application and owner",
  entitlement: "Application and owner",
  group: "Members",
  ci: "Type and ownership",
  incident: "Assignment",
  change: "Changes",
  event: "Observed on",
  request: "Requester",
  workflow: "Steps and runs",
  "workflow-step": "Assignment",
  "workflow-run": "Request",
  task: "Assignment",
};

function summaryHeading(resource: Resource) {
  return resource in summaryHeadings ? summaryHeadings[resource as NodeKind] : "Details";
}

// "A, B" or "A, B +3 more"; a bare count when there are no names to show.
function nameList(ends: { id: string; name: string }[], noun: string, max = 2) {
  if (ends.length === 0) return "";
  if (ends.length > max) return `${ends.slice(0, max).map((end) => end.name).join(", ")} +${ends.length - max} more`;
  return ends.map((end) => end.name).join(", ") || `${ends.length} ${noun}`;
}

function countOf(ends: { id: string }[], singular: string, plural = `${singular}s`) {
  return ends.length === 0 ? "" : `${ends.length} ${ends.length === 1 ? singular : plural}`;
}

function recordSummary(record: GraphNode): { label: string; value: string }[] {
  const properties = record.properties ?? {};
  const ends = (kind: RelationshipKind, end: "from" | "to") => linkedEnds(record, kind, end);
  const owner = nameList(ends("accountable", "to"), "owner");
  const lines: { label: string; value: string }[] = [];
  const add = (label: string, value: string) => { if (value) lines.push({ label, value }); };
  switch (record.kind) {
    case "identity":
      add("Job code", nameList(ends(hasJobCodeKind, "to"), "job code"));
      add("Location", nameList(ends("work-location", "to"), "location") || (typeof properties.location === "string" ? properties.location : ""));
      add("Groups", nameList(ends("member", "from"), "group"));
      break;
    case "job-code":
      add("Qualifies for", nameList(ends(qualifiesForKind, "to"), "birthright"));
      add("Owner", owner);
      break;
    case "birthright": {
      const granted = ends(grantsKind, "to");
      add("Roles", nameList(granted.filter((end) => end.id.startsWith("ROLE-")), "role"));
      add("Entitlements", nameList(granted.filter((end) => end.id.startsWith("ENT-")), "entitlement"));
      add("Job codes", countOf(ends(qualifiesForKind, "from"), "job code"));
      add("Owner", owner);
      break;
    }
    case "role":
      add("Role for", nameList(ends(hasRoleKind, "from"), "application"));
      add("Includes", countOf(ends("includes", "to"), "entitlement"));
      add("Granted by", countOf(ends(grantsKind, "from"), "birthright"));
      add("Owner", owner);
      break;
    case "entitlement":
      add("Entitlement for", nameList(ends(entitledByKind, "from"), "application"));
      add("Owner", owner);
      add("Included in", countOf(ends("includes", "from"), "role"));
      add("Held directly", countOf(ends("permissions", "to"), "identity", "identities"));
      break;
    case "group":
      add("Members", nameList(ends("member", "to"), "member", 3));
      break;
    case "ci": {
      const ciType = typeof properties.ciType === "string" ? titleCase(properties.ciType) : "";
      const qualifier = typeof properties.hosted === "string" ? titleCase(properties.hosted) : typeof properties.siteId === "string" ? properties.siteId : typeof properties.contractType === "string" ? contractTypeLabels[properties.contractType] ?? titleCase(properties.contractType) : "";
      add("Type", [ciType, qualifier].filter(Boolean).join(" · "));
      if (properties.critical === true) add("Critical", "Yes");
      add("Owner", owner);
      add("Has roles", countOf(ends(hasRoleKind, "to"), "role"));
      add("Entitled by", countOf(ends(entitledByKind, "to"), "entitlement"));
      add("Incidents", countOf(ends("affects", "from"), "open incident"));
      break;
    }
    case "incident":
      if (properties.nodeDown === true) add("Node down", "Yes");
      add("Assigned to", nameList(ends(incidentAssignmentKind, "to"), "assignee"));
      add("Affects", nameList(ends("affects", "to"), "CI"));
      break;
    case "change":
      add("Changes", nameList(ends("changes", "to"), "CI"));
      break;
    case "event":
      add("Observed on", nameList(ends("observed-on", "to"), "CI"));
      break;
    case "request":
      add("Requested by", nameList([...ends(formSubmittedKind, "from"), ...ends(draftedRequestKind, "from")], "requester"));
      add("For", nameList(ends("requested-for", "to"), "identity"));
      add("Fulfilled by", nameList(ends("fulfilled-by", "to"), "CI"));
      break;
    case "workflow":
      add("Steps", countOf(ends("has-step", "to"), "step"));
      add("Runs", countOf(ends("instance-of", "from"), "run"));
      break;
    case "workflow-step":
      add("Workflow", nameList(ends("has-step", "from"), "workflow"));
      add("Assigned to", nameList(ends("step-assigned-to", "to"), "assignee"));
      break;
    case "workflow-run":
      add("Request", nameList(ends("run-for", "to"), "request"));
      add("Workflow", nameList(ends("instance-of", "to"), "workflow"));
      add("Tasks", countOf(ends("task-for", "from"), "task"));
      break;
    case "task":
      add("Assigned to", nameList(ends("task-assigned-to", "to"), "assignee"));
      add("Item", nameList(ends("task-item", "to"), "item"));
      break;
  }
  return lines;
}

function raciFromRecord(record: GraphNode | undefined): RACIAssignments {
  return linksFromRecord(record, raciRoles.map(({ role }) => role)) as RACIAssignments;
}

const GraphMap = lazy(() => import("./GraphMap"));
const LocationMap = lazy(() => import("./LocationMap"));
const WorkflowCreator = lazy(() => import("./WorkflowCreator"));
const WorkflowTasks = lazy(() => import("./WorkflowTasks"));
const AccessRequestForm = lazy(() => import("./AccessRequestForm"));
const Home = lazy(() => import("./Home"));

const resourceInfo: Record<Resource, { title: string; singular: string; subtitle: string; kicker: string }> = {
  home: { title: "Home", singular: "user", subtitle: "Your profile and the work waiting on you: approvals, tasks, and open requests.", kicker: "MY WORKSPACE" },
  identity: { title: "Identities", singular: "identity", subtitle: "People and their assigned job codes.", kicker: "IDENTITY DIRECTORY" },
  "job-code": { title: "Job codes", singular: "job code", subtitle: "Positions that qualify for access packages.", kicker: "WORKFORCE CLASSIFICATION" },
  birthright: { title: "Birthrights", singular: "birthright", subtitle: "Automatic access bundles derived from workforce attributes.", kicker: "AUTOMATIC ACCESS" },
  role: { title: "Roles", singular: "role", subtitle: "Permission groupings used to grant system access.", kicker: "ACCESS GROUPS" },
  entitlement: { title: "Entitlements", singular: "entitlement", subtitle: "Concrete permissions within connected systems.", kicker: "SYSTEM PERMISSIONS" },
  group: { title: "Groups", singular: "group", subtitle: "Collections of identities linked through member / member-of.", kicker: "GROUP MEMBERSHIP" },
  relationships: { title: "Relationships", singular: "relationship", subtitle: "Directed links that describe how access is derived.", kicker: "GRAPH CONNECTIONS" },
  map: { title: "Graph map", singular: "record", subtitle: "Explore configuration items and their connected records.", kicker: "RELATIONSHIP MAP" },
  "location-map": { title: "Location maps", singular: "location", subtitle: "Sites plotted from their addresses; green when open, grey when closed.", kicker: "LOCATIONS" },
  ci: { title: "Configuration items", singular: "configuration item", subtitle: "Managed assets with governed CI types.", kicker: "CONFIGURATION ITEMS" },
  incident: { title: "Incidents", singular: "incident", subtitle: "Operational incidents associated with configuration items.", kicker: "INCIDENT MANAGEMENT" },
  change: { title: "Changes", singular: "change", subtitle: "Controlled changes that affect configuration items.", kicker: "CHANGE MANAGEMENT" },
  event: { title: "Events", singular: "event", subtitle: "Observed events associated with configuration items.", kicker: "EVENT STREAM" },
  request: { title: "Vendor Request Form", singular: "vendor request", subtitle: "Request a new vendor. Save a draft to finish later; submit once every required field is complete.", kicker: "CATALOG REQUESTS" },
  "access-request": { title: "Access Request Form", singular: "access request", subtitle: "Request roles or entitlements an identity does not already hold. Each item goes to its Accountable owner, then to the fulfilment team.", kicker: "CATALOG REQUESTS" },
  "workflow-creator": { title: "Workflow Creator", singular: "workflow", subtitle: "Define the review and approval steps a submitted catalog form passes through before its record is created.", kicker: "CATALOG REQUESTS" },
  "workflow-tasks": { title: "Workflow Tasks", singular: "task", subtitle: "Review, fill in, approve, or return the requests waiting on you.", kicker: "CATALOG REQUESTS" },
  // Workflow records are managed through the Workflow Creator and Workflow Tasks views; these entries name them on the map.
  workflow: { title: "Workflows", singular: "workflow", subtitle: "Workflow definitions.", kicker: "CATALOG REQUESTS" },
  "workflow-step": { title: "Workflow steps", singular: "workflow step", subtitle: "Steps within a workflow.", kicker: "CATALOG REQUESTS" },
  "workflow-run": { title: "Workflow runs", singular: "workflow run", subtitle: "Each submitted request's passage through its workflow. Runs retire with their tasks when the workflow completes; finished runs from earlier can be retired here.", kicker: "CATALOG REQUESTS" },
  task: { title: "Tasks", singular: "task", subtitle: "Work raised by a workflow run.", kicker: "CATALOG REQUESTS" },
};

// A named form field; `group` starts a labelled section (rendered once per run of fields).
type FormField = { key: string; label: string; options?: string[]; group?: string; checkbox?: boolean; hint?: string };

const nodeFields: Record<NodeKind, FormField[]> = {
  identity: [
    { key: "name", label: "Display name" },
    { key: "department", label: "Department" },
    { key: "location", label: "Location" },
  ],
  "job-code": [
    { key: "jobId", label: "Job ID" },
    { key: "name", label: "Job title" },
    { key: "description", label: "Description" },
  ],
  birthright: [
    { key: "name", label: "Birthright name" },
    { key: "description", label: "Description" },
  ],
  role: [
    { key: "name", label: "Role name" },
    { key: "description", label: "Description" },
  ],
  entitlement: [
    { key: "name", label: "Entitlement name" },
    { key: "description", label: "Description" },
  ],
  group: [
    { key: "name", label: "Group name" },
    { key: "description", label: "Description" },
  ],
  ci: [{ key: "ciType", label: "CI type", options: ["server", "printer", "data-connector", "application", "service", "process", "function", "location", "contract"] }],
  incident: [
    { key: "name", label: "Summary" },
    { key: "nodeDown", label: "Node is down", checkbox: true, hint: "Mark when the affected configuration item is currently unavailable." },
  ],
  change: [{ key: "name", label: "Summary" }],
  event: [{ key: "name", label: "Summary" }],
  request: [], // the request type's CI fields are appended by nodeFieldsFor
  // Workflow records are read-only here; they are built in the Workflow Creator and acted on under Workflow Tasks.
  workflow: [{ key: "name", label: "Workflow name" }],
  "workflow-step": [{ key: "name", label: "Step name" }],
  "workflow-run": [{ key: "name", label: "Workflow" }],
  task: [{ key: "name", label: "Step" }],
};

const categorizedCITypes = new Set<string>(["service", "process", "function"]);
const categoryOptions = ["business", "technology", "security"];
const criticalCIField: FormField = { key: "critical", label: "Critical", checkbox: true, hint: "Mark when this configuration item is business-critical." };
const categorizedCIFields: FormField[] = [
  { key: "name", label: "Name" },
  { key: "description", label: "Description" },
  { key: "category", label: "Category", options: categoryOptions },
  criticalCIField,
];

// Hardware/software CIs: server, printer, data-connector, application.
const describedCITypes = new Set<string>(["server", "printer", "data-connector", "application"]);
const describedCIFields: FormField[] = [
  { key: "name", label: "Name" },
  { key: "description", label: "Description" },
  criticalCIField,
];
const hostingOptions = ["internal", "external"];
const applicationFields: FormField[] = [
  { key: "name", label: "Name" },
  { key: "description", label: "Description" },
  { key: "hosted", label: "Hosted", options: hostingOptions },
  criticalCIField,
];

const contractTypeOptions = ["basic-contract", "msa", "nda"];
const contractTypeLabels: Record<string, string> = { "basic-contract": "Basic Contract", msa: "MSA", nda: "NDA" };
const contractFields: FormField[] = [
  { key: "name", label: "Name" },
  { key: "description", label: "Description" },
  { key: "contractType", label: "Type", options: contractTypeOptions },
  criticalCIField,
];

const criticalityOptions = ["critical", "important", "business-support"];
// Vendor contact details sit under their own heading on both vendor forms.
const vendorContactFields: FormField[] = [
  { key: "contactName", label: "Vendor Contact Name", group: "Contact information" },
  { key: "contactPhone", label: "Contact phone number", group: "Contact information" },
  { key: "contactEmail", label: "Contact Email Address", group: "Contact information" },
];
const vendorFields: FormField[] = [
  { key: "name", label: "Vendor Name" },
  { key: "description", label: "Description" },
  { key: "criticality", label: "Criticality", options: criticalityOptions },
  criticalCIField,
  ...vendorContactFields,
];
// The request form keeps parity with the vendor CI but only exposes what the
// requester can know: criticality and the critical flag are decided when the CI is created.
const vendorRequestFields: FormField[] = vendorFields.filter((field) => field.key !== "criticality" && field.key !== "critical");
const vendorRequestRequiredKeys = ["name"];

/** Digits of a phone number, with a flag for an explicit +country code. Country code 1 is folded into the domestic form. */
function phoneDigits(text: string): { digits: string; international: boolean } {
  const trimmed = text.trim();
  let international = trimmed.startsWith("+");
  let digits = trimmed.replace(/\D/g, "");
  if (digits.length === 11 && digits.startsWith("1")) { digits = digits.slice(1); international = false; }
  return { digits, international };
}

/** Formats a phone number as the user types: "(555) 010-0100" for North American numbers, "+" and digits otherwise. */
function formatPhoneInput(text: string): string {
  const parsed = phoneDigits(text);
  if (parsed.international) return `+${parsed.digits.slice(0, 15)}`;
  const digits = parsed.digits.slice(0, 10);
  if (digits.length === 0) return "";
  if (digits.length < 4) return `(${digits}`;
  if (digits.length < 7) return `(${digits.slice(0, 3)}) ${digits.slice(3)}`;
  return `(${digits.slice(0, 3)}) ${digits.slice(3, 6)}-${digits.slice(6)}`;
}

/** Mirrors the server's canonical phone format; returns null when the text is not a complete phone number. */
function normalizePhoneNumber(text: string): string | null {
  if (/[^\d\s().+-]/.test(text)) return null;
  const { digits, international } = phoneDigits(text);
  if (!international && digits.length === 10) return formatPhoneInput(digits);
  if (international && digits.length >= 7 && digits.length <= 15) return `+${digits}`;
  return null;
}

const usStateCodes = ["AL", "AK", "AZ", "AR", "CA", "CO", "CT", "DE", "DC", "FL", "GA", "HI", "ID", "IL", "IN", "IA", "KS", "KY", "LA", "ME", "MD", "MA", "MI", "MN", "MS", "MO", "MT", "NE", "NV", "NH", "NJ", "NM", "NY", "NC", "ND", "OH", "OK", "OR", "PA", "RI", "SC", "SD", "TN", "TX", "UT", "VT", "VA", "WA", "WV", "WI", "WY"];
const fixedCountry = "United States of America";

const locationFields: FormField[] = [
  { key: "name", label: "Name" },
  { key: "siteId", label: "Site ID" },
  { key: "addressLine1", label: "Address line 1" },
  { key: "addressLine2", label: "Address line 2" },
  { key: "city", label: "City" },
  { key: "state", label: "State / Province", options: usStateCodes },
  { key: "postalCode", label: "Postal code" },
  { key: "country", label: "Country" },
  { key: "timezone", label: "Time zone", options: [] },
  criticalCIField,
];
const timeZoneLabels: Record<string, string> = {
  "America/New_York": "Eastern",
  "America/Chicago": "Central",
  "America/Denver": "Mountain",
  "America/Phoenix": "Arizona (no DST)",
  "America/Los_Angeles": "Pacific",
  "America/Anchorage": "Alaska",
  "Pacific/Honolulu": "Hawaii",
  "America/Puerto_Rico": "Atlantic (Puerto Rico)",
  "Pacific/Guam": "Guam",
  "Pacific/Pago_Pago": "American Samoa",
};
const timeZoneOptions = Object.keys(timeZoneLabels);

function timeZoneChoices(current: unknown) {
  return typeof current === "string" && current && !timeZoneOptions.includes(current) ? [current, ...timeZoneOptions] : timeZoneOptions;
}

function timeZoneLabel(zone: string) {
  return timeZoneLabels[zone] ?? zone.replaceAll("_", " ");
}

// Geocoded coordinates are maintained by the server from the address; keep them
// out of the editable attribute list and let the server refresh them on save.
const locationSystemKeys = ["latitude", "longitude", "geoPrecision"];

function selectedCIType(properties: Properties, metadata: Metadata | null) {
  return String(properties.ciType ?? metadata?.ciTypes[0] ?? "server");
}

function nodeFieldsFor(kind: NodeKind, properties: Properties, metadata: Metadata | null) {
  const fields = nodeFields[kind];
  // A request asks for what the requester can supply for its CI type (vendor is the only type so far).
  if (kind === "request") return [...fields, ...vendorRequestFields];
  if (kind !== "ci") return fields;
  const ciType = selectedCIType(properties, metadata);
  if (categorizedCITypes.has(ciType)) return [...fields, ...categorizedCIFields];
  if (ciType === "location") return [...fields, ...locationFields];
  if (ciType === "contract") return [...fields, ...contractFields];
  if (ciType === "vendor") return [...fields, ...vendorFields];
  if (ciType === "application") return [...fields, ...applicationFields];
  if (describedCITypes.has(ciType)) return [...fields, ...describedCIFields];
  return [...fields, criticalCIField];
}

function knownKeysFor(kind: NodeKind, properties: Properties, metadata: Metadata | null) {
  const keys = nodeFieldsFor(kind, properties, metadata).map((field) => field.key);
  if (kind === "request") return new Set([...keys, ...requestSystemKeys, ...requestSLAKeys]);
  return new Set(kind === "ci" && selectedCIType(properties, metadata) === "location" ? [...keys, ...weekdays.map(hoursKey), ...locationSystemKeys] : keys);
}

const navigation: { label: string; items: { kind: Resource; title: string; Icon: LucideIcon }[] }[] = [
  { label: "MY WORKSPACE", items: [
    { kind: "home", title: "Home", Icon: House },
  ] },
  { label: "APPS / TOOLS", items: [] },
  { label: "IDENTITY AND ACCESS", items: [
    { kind: "identity", title: "Identities", Icon: Users },
    { kind: "job-code", title: "Job codes", Icon: BriefcaseBusiness },
    { kind: "birthright", title: "Birthrights", Icon: PackageOpen },
    { kind: "role", title: "Roles", Icon: ShieldCheck },
    { kind: "entitlement", title: "Entitlements", Icon: KeyRound },
    { kind: "group", title: "Groups", Icon: UsersRound },
  ] },
  { label: "CASES / TICKETS", items: [
    { kind: "incident", title: "Incidents", Icon: BriefcaseBusiness },
    { kind: "change", title: "Changes", Icon: RefreshCw },
    { kind: "event", title: "Events", Icon: Link2 },
  ] },
  { label: "CMDB", items: [
    { kind: "relationships", title: "Relationships", Icon: Link2 },
    { kind: "map", title: "Map", Icon: Network },
    { kind: "ci", title: "Configuration items", Icon: PackageOpen },
  ] },
  { label: "LOCATIONS", items: [
    { kind: "location-map", title: "Location Maps", Icon: MapPin },
  ] },
  { label: "CATALOG REQUESTS", items: [
    { kind: "request", title: "Vendor Request Form", Icon: ClipboardList },
    { kind: "access-request", title: "Access Request Form", Icon: KeyRound },
    { kind: "workflow-creator", title: "Workflow Creator", Icon: Workflow },
    { kind: "workflow-tasks", title: "Workflow Tasks", Icon: ListChecks },
    { kind: "workflow-run", title: "Workflow Runs", Icon: RefreshCw },
  ] },
];

function chromeFor(resource: Resource) {
  const section = navigation.find((group) => group.items.some((item) => item.kind === resource))?.label ?? resourceInfo[resource].kicker;
  return { section, page: resourceInfo[resource].title.toUpperCase() };
}

// Views that render their own panel instead of a record table.
const canvasResources = new Set<Resource>(["home", "map", "location-map", "workflow-creator", "workflow-tasks", "access-request"]);
// Workflow records opened from the map go to the view that manages them rather than the record editor.
const workflowNodeKinds = new Set<NodeKind>(["workflow", "workflow-step", "workflow-run", "task"]);

function scalarProperties(properties: Properties | undefined = {}) {
  // Stored hours are 24-hour; show them as 12-hour AM/PM like the rest of the UI.
  return Object.entries(properties).map(([key, value]) => `${key}: ${hoursPropertyKeys.has(key) ? formatDailyHours(value, String(value)) : value}`).join(" · ") || "No additional properties";
}

// Relationship endpoints may be limited to certain CI types (e.g. provides: service or function).
function filterByCITypes(nodes: GraphNode[], ciTypes: string[] | undefined) {
  return ciTypes && ciTypes.length > 0 ? nodes.filter((node) => typeof node.properties?.ciType === "string" && ciTypes.includes(node.properties.ciType)) : nodes;
}

function ciTypesLabel(ciTypes: string[] | undefined) {
  return ciTypes && ciTypes.length > 0 ? `${ciTypes.map(titleCase).join(" / ")} CI` : null;
}

// Narrows the To CI types to the pairing rules that match the chosen From record
// (e.g. provides: a vendor may only provide an application, server, or printer).
// Falls back to the definition's overall toCiTypes until a From record is picked.
function allowedToCITypes(definition: RelationshipDefinition | undefined, fromNode: GraphNode | undefined): string[] | undefined {
  const rules = definition?.ciTypeRules;
  if (!definition || !rules || rules.length === 0) return definition?.toCiTypes;
  const fromType = typeof fromNode?.properties?.ciType === "string" ? fromNode.properties.ciType : undefined;
  if (!fromType) return definition.toCiTypes;
  const matching = rules.filter((rule) => rule.from.length === 0 || rule.from.includes(fromType as CIType));
  if (matching.some((rule) => rule.to.length === 0)) return undefined;
  return [...new Set(matching.flatMap((rule) => rule.to))];
}

function nodeOptionLabel(node: GraphNode) {
  const properties = node.properties ?? {};
  const name = nodeDisplayName(node) ?? node.id;
  const kind = node.kind === "ci" && typeof properties.ciType === "string" ? `CI · ${titleCase(properties.ciType)}` : titleCase(node.kind);
  return `${name} · ${node.id} · ${kind}`;
}

function App() {
  // The signed-in user lands on their home page. Until single sign-on arrives
  // the user is the platform super admin, optionally acting as a configured
  // identity (remembered for the browser session).
  const [resource, setResource] = useState<Resource>("home");
  const [actingAs, setActingAs] = useState(storedActingAs);
  const [identities, setIdentities] = useState<GraphNode[]>([]);
  const [identitiesError, setIdentitiesError] = useState("");
  const [access, setAccess] = useState<ActorAccess | null>(null);
  const [relationshipKind, setRelationshipKind] = useState<RelationshipKind>("has-job-code");
  const [metadata, setMetadata] = useState<Metadata | null>(null);
  const [records, setRecords] = useState<(GraphNode | GraphRelationship)[]>([]);
  // The list is one page of the server's result; totals ride alongside it.
  const [totals, setTotals] = useState({ total: 0, active: 0 });
  const [page, setPage] = useState(0);
  const [includeRetired, setIncludeRetired] = useState(false);
  const [search, setSearch] = useState("");
  // The filter is applied by the server, so typing waits a beat before fetching.
  const [debouncedSearch, setDebouncedSearch] = useState("");
  const [loading, setLoading] = useState(true);
  const [online, setOnline] = useState(false);
  const [error, setError] = useState("");
  const [editor, setEditor] = useState<RecordEditorTarget | null>(null);
  const [retiring, setRetiring] = useState<RetireTarget | null>(null);
  const [toast, setToast] = useState("");
  const [reload, setReload] = useState(0);
  // The Vendor Request Form view is the form itself: a blank request ready for
  // entry, or a saved request reopened from the list beneath it. The version
  // bumps after each save so the panel remounts blank without a page refresh
  // wiping entries in progress.
  const [requestRecord, setRequestRecord] = useState<GraphNode | null>(null);
  const [requestFormVersion, setRequestFormVersion] = useState(0);

  useEffect(() => {
    let active = true;
    getMetadata().then((result) => {
      if (!active) return;
      setMetadata(result);
      if (result.relationships.length > 0) setRelationshipKind(result.relationships[0].kind);
    }).catch(() => {
      if (active) setError("Could not connect to the API.");
    });
    return () => { active = false; };
  }, []);

  useEffect(() => {
    setApiActor(actingAs || superAdmin.id);
    storeActingAs(actingAs);
  }, [actingAs]);

  useEffect(() => {
    let active = true;
    getCapabilities().then((result) => {
      if (active) setAccess(result);
    }).catch(() => {
      if (active) setAccess(null);
    });
    return () => { active = false; };
  }, [actingAs, reload]);

  useEffect(() => {
    let active = true;
    // Super admin loads every identity for act-as; an identity session loads only itself.
    const load = actingAs
      ? getNode("identity", actingAs).then((node) => {
        if (!active) return;
        setIdentities((current) => {
          const others = current.filter((item) => item.id !== node.id);
          return [...others, node];
        });
        setIdentitiesError("");
      })
      : getNodes("identity", false).then((items) => {
        if (!active) return;
        setIdentities(items);
        setIdentitiesError("");
      });
    load.catch((loadError: unknown) => {
      if (active) setIdentitiesError(loadError instanceof Error ? loadError.message : "Could not load identities.");
    });
    return () => { active = false; };
  }, [actingAs, reload]);
  const user = sessionUser(actingAs, identities);
  // Switching who you are acting as returns to that user's home page.
  const actAs = (identityId: string) => {
    setApiActor(identityId || superAdmin.id);
    // Avoid a flash of an empty rail while capabilities reload for the new actor.
    setAccess(identityId ? null : { actorId: superAdmin.id, superAdmin: true, entitlements: [], capabilities: [] });
    setActingAs(identityId);
    setResource("home");
    setRequestRecord(null);
    setSearch("");
  };

  useEffect(() => {
    if (resource === "home" || canViewResource(access, resource)) return;
    setResource("home");
    setRequestRecord(null);
  }, [access, resource]);

  useEffect(() => {
    const timeout = window.setTimeout(() => setDebouncedSearch(search.trim()), 300);
    return () => window.clearTimeout(timeout);
  }, [search]);

  // Changing what is listed starts over from the first page.
  useEffect(() => { setPage(0); }, [resource, relationshipKind, includeRetired, debouncedSearch]);

  useEffect(() => {
    let active = true;
    if (canvasResources.has(resource)) {
      setLoading(false);
      return () => { active = false; };
    }
    setLoading(true);
    const listQuery = { includeRetired, q: debouncedSearch, limit: defaultPageSize, offset: page * defaultPageSize };
    const query = resource === "relationships"
      ? getRelationshipPage(relationshipKind, listQuery)
      // Each catalog form lists only its own requests beneath it.
      : getNodePage(resource as NodeKind, resource === "request" ? { ...listQuery, requestType: "vendor" } : listQuery);
    query.then((result) => {
      if (!active) return;
      setRecords(result.items);
      setTotals({ total: result.total, active: result.active });
      // A refresh after retiring the last record on a page steps back to the previous one.
      const clamped = clampPage(page, result.total);
      if (clamped !== page) setPage(clamped);
      setOnline(true);
      setError("");
    }).catch((requestError: unknown) => {
      if (!active) return;
      setRecords([]);
      setTotals({ total: 0, active: 0 });
      setOnline(false);
      setError(requestError instanceof Error ? requestError.message : "Request failed.");
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [resource, relationshipKind, includeRetired, debouncedSearch, page, reload]);

  useEffect(() => {
    if (!toast) return;
    const timeout = window.setTimeout(() => setToast(""), 3000);
    return () => window.clearTimeout(timeout);
  }, [toast]);

  const currentInfo = resourceInfo[resource];
  const chrome = chromeFor(resource);
  // The server applies the form, retired, and search filters; the page is what it returned.
  const filteredRecords = records;
  // A saved request opens on the form it was raised from.
  const openRequest = (record: GraphNode) => {
    setResource(requestTypeOf(record) === "access" ? "access-request" : "request");
    setRequestRecord(record);
  };
  // A listed record carries only a sample of its links; the editor reconciles
  // links on save, so it opens on the full record.
  const editNode = (record: GraphNode) => {
    getNode(record.kind, record.id, true)
      .then((full) => setEditor({ type: "node", kind: record.kind, record: full }))
      .catch(() => setEditor({ type: "node", kind: record.kind, record }));
  };
  const retiredCount = totals.total - totals.active;
  const activeCount = totals.active;
  // Direct links are counted over the page shown (each listed record carries a
  // bounded sample of its links); the relationship list's total is the real count.
  const relationshipCount = resource === "relationships"
    ? totals.total
    : (records as GraphNode[]).reduce((total, node) => total + (node.relationships?.length ?? 0), 0);
  const editorRecordKey = editor?.record
    ? ("id" in editor.record ? editor.record.id : `${editor.record.fromId}-${editor.record.toId}`)
    : editor?.type === "node" && editor.ciIds?.length ? `new-${editor.kind}-${editor.ciIds.join("-")}` : "new";

  async function saveRecord(target: RecordEditorTarget, fromId: string, toId: string, properties: Properties, ciIDs: string[], links?: LinkAssignments) {
    try {
      // Submitting a request is a draft save followed by the state change, so the
      // requester link exists before the server swaps it for form-submitted.
      const submittingRequest = target.type === "node" && target.kind === "request" && properties.state === "submitted";
      if (target.type === "node") {
        const draftProperties = { ...properties };
        if (submittingRequest) delete draftProperties.state;
        let saved: GraphNode;
        if (target.record) saved = Object.keys(draftProperties).length > 0 ? await updateNode(target.kind, target.record.id, draftProperties) : target.record;
        else {
          saved = await createNode(target.kind, { properties: draftProperties, ...(ciIDs.length > 0 ? { ciIds: ciIDs } : {}) });
        }
        // RACI roles, incident assignment, and the request's requester are
        // relationships on the record; reconcile what the form shows against
        // what is stored by creating new links and retiring removed ones.
        if (links) {
          const kinds = Object.keys(links) as RelationshipKind[];
          const current = linksFromRecord(target.record, kinds);
          for (const kind of kinds) {
            const wanted = links[kind] ?? [];
            const incoming = isIncomingLink(kind, target.kind);
            for (const id of wanted) if (!current[kind].includes(id)) await createRelationship(kind, incoming ? { fromId: id, toId: saved.id, properties: {} } : { fromId: saved.id, toId: id, properties: {} });
            for (const id of current[kind]) if (!wanted.includes(id)) await (incoming ? retireRelationship(kind, id, saved.id) : retireRelationship(kind, saved.id, id));
          }
        }
        if (submittingRequest) await updateNode(target.kind, saved.id, { state: "submitted" });
      } else {
        const kind = target.record?.kind ?? target.kind;
        if (!kind) throw new Error("Choose a relationship type.");
        if (target.record) await updateRelationship(kind, target.record.fromId, target.record.toId, properties);
        else await createRelationship(kind, { fromId, toId, properties });
      }
      setEditor(null);
      if (target.type === "node" && target.kind === "request") {
        setRequestRecord(null);
        setRequestFormVersion((value) => value + 1);
      }
      setToast(submittingRequest ? "Request submitted." : target.record ? "Changes saved." : target.type === "node" && target.kind === "request" ? "Draft saved." : "Record created.");
      setReload((value) => value + 1);
    } catch (saveError) {
      setToast(saveError instanceof Error ? saveError.message : "Could not save record.");
    }
  }

  async function confirmRetirement() {
    if (!retiring) return;
    try {
      if (retiring.type === "node") await retireNode(retiring.kind, retiring.record.id);
      else await retireRelationship(retiring.kind, retiring.record.fromId, retiring.record.toId);
      setRetiring(null);
      setToast("Record retired.");
      setReload((value) => value + 1);
    } catch (retireError) {
      setToast(retireError instanceof Error ? retireError.message : "Could not retire record.");
    }
  }

  return (
    <div className="app-shell">
      <aside className="rail">
        <a className="brand" href="/" aria-label="Anthos home">
          <img className="brand-mark" src="/sunflower.jpg" alt="" />
          <span><strong>Anthos</strong><small>POWER PLATFORM</small></span>
        </a>
        <UserMenu user={user} identities={identities} identitiesError={identitiesError} onActAs={actAs} onGoHome={() => { setResource("home"); setSearch(""); setRequestRecord(null); }} />
        {navigation.map(({ label, items }) => {
          const visible = items.filter((item) => canViewResource(access, item.kind));
          if (visible.length === 0) return null;
          return (
          <div className="rail-group" key={label}>
            <div className="rail-section-label">{label}</div>
            <nav className="resource-nav" aria-label={label}>
              {visible.map(({ kind, title, Icon }) => (
                <button className={`nav-item ${resource === kind ? "active" : ""}`} key={kind} onClick={() => { setResource(kind); setSearch(""); setRequestRecord(null); }}>
                  <Icon aria-hidden="true" size={17} strokeWidth={1.7} />{title}
                </button>
              ))}
            </nav>
          </div>
          );
        })}
        <div className="rail-footer">
          <span className={`connection-dot ${online ? "online" : error ? "offline" : ""}`} />
          <div><strong>{online ? "Database connected" : error ? "Database unavailable" : "Checking database"}</strong><small>Neo4j connection</small></div>
        </div>
      </aside>

      <div className="content-shell">
        <header className="topbar">
          <span>{chrome.section} <span className="crumb-separator">/</span> {chrome.page}</span>
          <span className="environment-tag">LOCAL WORKSPACE</span>
        </header>
        <main>
          <section className="page-heading">
            <div>
              <p className="eyebrow">{currentInfo.kicker}</p>
              <h1>{resource === "home" ? user.name : currentInfo.title}</h1>
              <p className="page-subtitle">{currentInfo.subtitle}</p>
            </div>
            <div className="heading-actions">
              <button className="button button-quiet icon-button" onClick={() => setReload((value) => value + 1)} title="Refresh records" aria-label="Refresh records">
                <RefreshCw size={16} />Refresh
              </button>
              {resource === "location-map" && canCreateResource(access, "location-map") && <button className="button button-primary" onClick={() => setEditor({ type: "node", kind: "ci", defaults: { ciType: "location" } })}>
                <Plus size={16} />Add location
              </button>}
              {!canvasResources.has(resource) && resource !== "request" && !workflowNodeKinds.has(resource as NodeKind) && canCreateResource(access, resource) && <button className="button button-primary" onClick={() => setEditor(resource === "relationships"
                ? { type: "relationship", kind: relationshipKind }
                : { type: "node", kind: resource as NodeKind })}>
                <Plus size={16} />Add {currentInfo.singular}
              </button>}
            </div>
          </section>

          {!canvasResources.has(resource) && <section className="metric-strip" aria-label="Current view totals">
            <Metric label="RECORDS" value={loading ? "—" : totals.total} />
            <Metric label="ACTIVE" value={loading ? "—" : activeCount} />
            <Metric label="RETIRED" value={loading ? "—" : retiredCount} />
            <Metric label={resource === "relationships" ? "LINK RECORDS" : "DIRECT LINKS (PAGE)"} value={loading ? "—" : relationshipCount} />
          </section>}

          {resource === "home" ? <Suspense fallback={<div className="editor-dialog editor-inline workflow-loading">Loading your workspace…</div>}><Home user={user} metadata={metadata} reload={reload} onNotify={setToast} onOpenRequest={openRequest} onOpenTasks={() => { setResource("workflow-tasks"); setSearch(""); }} /></Suspense>
          : resource === "map" ? <Suspense fallback={<div className="map-canvas map-overlay">Loading graph tools…</div>}><GraphMap metadata={metadata} includeRetired={includeRetired} reload={reload} onToggleIncludeRetired={() => setIncludeRetired((value) => !value)} onCreateLinked={(kind, ci) => {
            const open = (record: GraphNode) => setEditor({
              type: "node",
              kind,
              ciIds: [record.id],
              ...(kind === "incident" ? { assignedTo: assignmentFromResponsible(record) } : {}),
            });
            getNode(ci.kind, ci.id, true).then(open).catch(() => open(ci));
          }} onEdit={(record) => {
            if (record.kind === "request") openRequest(record);
            else if (workflowNodeKinds.has(record.kind)) setResource(record.kind === "workflow" || record.kind === "workflow-step" ? "workflow-creator" : "workflow-tasks");
            else editNode(record);
          }} /></Suspense>
          : resource === "location-map" ? <Suspense fallback={<div className="map-canvas map-overlay">Loading location map…</div>}><LocationMap reload={reload} onEdit={editNode} /></Suspense>
          : resource === "workflow-creator" ? <Suspense fallback={<div className="editor-dialog editor-inline workflow-loading">Loading Workflow Creator…</div>}><WorkflowCreator metadata={metadata} reload={reload} onNotify={setToast} /></Suspense>
          : resource === "workflow-tasks" ? <Suspense fallback={<div className="editor-dialog editor-inline workflow-loading">Loading Workflow Tasks…</div>}><WorkflowTasks user={user} metadata={metadata} reload={reload} onNotify={setToast} onOpenRequest={openRequest} onRetire={(task) => setRetiring({ type: "node", kind: "task", record: { kind: "task", id: task.id, status: "active", properties: { name: task.step.name } } })} /></Suspense>
          : resource === "access-request" ? <Suspense fallback={<div className="editor-dialog editor-inline workflow-loading">Loading Access Request Form…</div>}><AccessRequestForm key={user.id} user={user} metadata={metadata} reload={reload} onNotify={setToast} openRequest={requestRecord} onOpenRequest={setRequestRecord} /></Suspense>
          : <>{resource === "request" && <section className="records-section request-form-panel" aria-label="Vendor Request Form">
            <RecordDialog key={`request-${requestRecord?.id ?? "new"}-${requestFormVersion}`} inline target={{ type: "node", kind: "request", record: requestRecord ?? undefined }} metadata={metadata} onClose={() => setRequestRecord(null)} onSave={saveRecord} />
          </section>}
          <section className="records-section" aria-label={resource === "request" ? "Saved requests" : "Records"}>
            {resource === "request" && <div className="ci-picker-heading saved-requests-heading"><div><h3>Saved requests</h3></div><span className="ci-selection-count">Reopen a draft to finish it, or review what has been submitted. Fulfilled requests are retired; tick Include retired to see them.</span></div>}
            <div className="records-toolbar">
              <label className="search-box">
                <Search aria-hidden="true" size={17} />
                <input value={search} onChange={(event) => setSearch(event.target.value)} type="search" placeholder="Filter this list" autoComplete="off" />
              </label>
              {resource === "relationships" && (
                <label className="relationship-filter">
                  <span>Relationship</span>
                  <select value={relationshipKind} onChange={(event) => setRelationshipKind(event.target.value as RelationshipKind)}>
                    {(metadata?.relationships ?? []).map((item) => <option key={item.kind} value={item.kind}>{item.kind} / {item.inverse}</option>)}
                  </select>
                </label>
              )}
              <label className="retired-toggle"><input type="checkbox" checked={includeRetired} onChange={(event) => setIncludeRetired(event.target.checked)} /><span>Include retired</span></label>
            </div>
            <div className="table-frame">
              <table>
                <thead><tr>{resource === "relationships"
                  ? <><th>Relationship</th><th>From</th><th>To</th><th>Properties</th><th>Status</th><th>Actions</th></>
                  : <><th>Identifier</th><th>Name</th><th>Properties</th><th>{summaryHeading(resource)}</th><th>Status</th><th>Actions</th></>}
                </tr></thead>
                <tbody>
                  {resource === "relationships"
                    ? (filteredRecords as GraphRelationship[]).map((record, index) => <RelationshipRow key={`${record.kind}-${record.fromId ?? index}-${record.toId ?? index}`} record={record} canEdit={canUpdateResource(access, "relationships")} canRetire={canRetireResource(access, "relationships")} onEdit={() => setEditor({ type: "relationship", record })} onRetire={() => setRetiring({ type: "relationship", kind: record.kind, record })} />)
                    : (filteredRecords as GraphNode[]).map((record, index) => <NodeRow key={`${record.kind}-${record.id ?? index}`} record={record} canEdit={canUpdateResource(access, resource)} canRetire={canRetireResource(access, resource)} onEdit={() => {
                      if (record.kind === "request") { setRequestRecord(record); document.querySelector(".request-form-panel")?.scrollIntoView({ behavior: "smooth", block: "start" }); }
                      else if (workflowNodeKinds.has(record.kind)) setResource("workflow-tasks"); // runs and tasks are worked, not edited
                      else editNode(record);
                    }} onRetire={() => setRetiring({ type: "node", kind: record.kind, record })} />)}
                </tbody>
              </table>
              {!loading && filteredRecords.length === 0 && <div className="empty-state"><span className="empty-rule" /><strong>{error || (search ? "No matching records" : `No ${currentInfo.title.toLowerCase()} found`)}</strong><p>{search ? "Try another filter." : "Create a record to add it to this inventory."}</p></div>}
            </div>
            <div className="table-footer"><Pager page={page} total={totals.total} loading={loading} noun="record" onPage={setPage} /><span>Sorted by identifier</span></div>
          </section>
          {resource === "birthright" && <AccessAnomaliesPanel reload={reload} />}
          </>}
          {toast && <div className="toast" role="status" aria-live="polite">{toast}</div>}
        </main>
        <footer className="page-footer"><span>{chrome.section}</span><span>{chrome.page} · API V1</span></footer>
      </div>

      {editor && <RecordDialog key={`${editor.type}-${editor.kind ?? editor.record?.kind}-${editorRecordKey}`} target={editor} metadata={metadata} onClose={() => setEditor(null)} onSave={saveRecord} />}
      {retiring && <RetireDialog target={retiring} onClose={() => setRetiring(null)} onConfirm={confirmRetirement} />}
    </div>
  );
}

// Overlaps of birthright access and direct permissions, shown under the
// birthrights list. The check walks the whole graph, so the API serves the
// snapshot its scheduler last wrote; re-running it on demand is a development
// convenience and the button is left out of production builds.
function AccessAnomaliesPanel({ reload }: { reload: number }) {
  const [report, setReport] = useState<AccessAnomalyReport>({ anomalies: [] });
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState("");
  const anomalies = report.anomalies;
  useEffect(() => {
    let active = true;
    setLoading(true);
    getAccessAnomalies().then((result) => {
      if (!active) return;
      setReport(result);
      setError("");
    }).catch((loadError: unknown) => {
      if (!active) return;
      setError(loadError instanceof Error ? loadError.message : "Could not load access anomalies.");
      setReport({ anomalies: [] });
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [reload]);
  async function recompute() {
    setRefreshing(true);
    try {
      setReport(await refreshAccessAnomalies());
      setError("");
    } catch (refreshError) {
      setError(refreshError instanceof Error ? refreshError.message : "Could not re-run the anomaly check.");
    } finally {
      setRefreshing(false);
    }
  }
  const asOf = report.computedAt ? formatWhen(report.computedAt) : "";
  return <section className="records-section anomalies-section" aria-label="Access anomalies">
    <div className="ci-picker-heading saved-requests-heading">
      <div>
        <h3>Access anomalies</h3>
        <p className="field-hint">Identities that hold the same role or entitlement more than once: through a birthright, through a held role that includes it, and/or through a direct permissions link. The check runs on a schedule; this is the latest result.</p>
      </div>
      <div className="anomalies-heading-actions">
        <span className="ci-selection-count">{loading || refreshing ? "Checking…" : `${anomalies.length} overlap${anomalies.length === 1 ? "" : "s"}`}{!loading && !refreshing && <span className="snapshot-note"> · {asOf ? `as of ${asOf}` : "not checked yet"}</span>}</span>
        {import.meta.env.DEV && <button className="button button-quiet icon-button" type="button" onClick={recompute} disabled={loading || refreshing} title="Re-run the check now (development only)"><RefreshCw size={14} />Refresh</button>}
      </div>
    </div>
    {error && <p className="form-error" role="alert">{error}</p>}
    {!loading && !error && anomalies.length === 0 && <div className="empty-state anomalies-empty"><span className="empty-rule" /><strong>No overlapping access paths</strong><p>{asOf ? "No identity held the same item through both an automatic path and a direct permission at the last check." : "The scheduled check has not run yet; results appear here once it has."}</p></div>}
    {(loading || anomalies.length > 0) && <div className="table-frame">
      <table>
        <thead><tr><th>Identity</th><th>Item</th><th>Through</th><th>Paths</th></tr></thead>
        <tbody>
          {loading && anomalies.length === 0 ? <tr><td colSpan={4}><span className="secondary-value">Checking for overlaps…</span></td></tr>
            : anomalies.map((anomaly) => <tr key={`${anomaly.identity.id}:${anomaly.item.id}`}>
              <td><div className="primary-value">{anomaly.identity.name || anomaly.identity.id}</div><div className="secondary-value"><span className="identifier">{anomaly.identity.id}</span></div></td>
              <td><div className="primary-value">{anomaly.item.name || anomaly.item.id}</div><div className="secondary-value">{titleCase(anomaly.item.kind)} · <span className="identifier">{anomaly.item.id}</span></div></td>
              <td><div className="relationship-list">
                {(anomaly.birthrights ?? []).map((birthright) => <div className="relationship-line" key={`birthright:${birthright.id}`}><strong>{birthright.name || birthright.id}</strong> · birthright · <span className="identifier">{birthright.id}</span></div>)}
                {(anomaly.roles ?? []).map((role) => <div className="relationship-line" key={`role:${role.id}`}><strong>{role.name || role.id}</strong> · role · <span className="identifier">{role.id}</span></div>)}
              </div></td>
              <td><div className="anomaly-paths">{anomaly.paths.map((path) => <span className={`status-pill anomaly-path anomaly-path-${path}`} key={path}>{path}</span>)}</div></td>
            </tr>)}
        </tbody>
      </table>
    </div>}
  </section>;
}

function Metric({ label, value }: { label: string; value: string | number }) {
  return <div className="metric"><span className="metric-label">{label}</span><strong>{value}</strong></div>;
}

function StatusPill({ status }: { status: "active" | "retired" }) {
  return <span className={`status-pill status-${status}`}>{status}</span>;
}

// Incidents are closed out rather than archived, so their retire action reads "Resolve/Retire".
function retireVerb(kind?: NodeKind) {
  return kind === "incident" ? "Resolve/Retire" : "Retire";
}

function RowActions({ status, kind, canEdit = true, canRetire = true, onEdit, onRetire }: { status: "active" | "retired"; kind?: NodeKind; canEdit?: boolean; canRetire?: boolean; onEdit: () => void; onRetire: () => void }) {
  const verb = retireVerb(kind);
  if (!canEdit && !(canRetire && status !== "retired")) return <span className="secondary-value">—</span>;
  return <div className="row-actions">
    {canEdit && <button className="action-button" onClick={onEdit} title="Edit record"><Pencil size={14} /><span>Edit</span></button>}
    {canRetire && status !== "retired" && <button className="action-button retire" onClick={onRetire} title={`${verb} record`}><Archive size={14} /><span>{verb}</span></button>}
  </div>;
}

function NodeRow({ record, canEdit, canRetire, onEdit, onRetire }: { record: GraphNode; canEdit?: boolean; canRetire?: boolean; onEdit: () => void; onRetire: () => void }) {
  const properties = record.properties ?? {};
  const displayName = nodeDisplayName(record) ?? (record.kind === "ci" && typeof properties.ciType === "string" ? titleCase(properties.ciType) : "—");
  const recordType = record.kind === "ci" && typeof properties.ciType === "string" ? `CI · ${titleCase(properties.ciType)}` : record.kind === "request" ? `${titleCase(String(properties.requestType ?? "catalog"))} request` : titleCase(record.kind);
  const summary = recordSummary(record);
  const openState = record.kind === "ci" && properties.ciType === "location" ? locationOpenNow(properties) : null;
  const requestState = record.kind === "request" ? requestStateOf(record) : null;
  return <tr>
    <td><span className="identifier">{record.id}</span></td>
    <td><div className="primary-value">{displayName}</div><div className="secondary-value">{recordType}{properties.siteId ? ` · ${properties.siteId}` : ""}</div>{openState && <span className={`status-pill open-state ${openState === "open" ? "status-active" : "status-retired"}`}>{openState === "open" ? "Open now" : "Closed now"}</span>}{requestState && <span className={`status-pill open-state ${requestStatePillClass(requestState)}`}>{requestState}</span>}</td>
    <td><div className="property-list">{scalarProperties(properties)}</div></td>
    <td><div className="relationship-list">{summary.length === 0
      ? <span className="secondary-value">—</span>
      : summary.map((line) => <div className="relationship-line" key={line.label}><strong>{line.label}</strong> → {line.value}</div>)}</div></td>
    <td><StatusPill status={record.status} /></td>
    <td><RowActions status={record.status} kind={record.kind} canEdit={canEdit} canRetire={canRetire} onEdit={onEdit} onRetire={onRetire} /></td>
  </tr>;
}

function RelationshipRow({ record, canEdit, canRetire, onEdit, onRetire }: { record: GraphRelationship; canEdit?: boolean; canRetire?: boolean; onEdit: () => void; onRetire: () => void }) {
  return <tr>
    <td><span className="identifier">{record.kind}</span></td>
    <td><span className="identifier">{record.fromId}</span></td>
    <td><span className="identifier">{record.toId}</span></td>
    <td><div className="property-list">{scalarProperties(record.properties)}</div></td>
    <td><StatusPill status={record.status} /></td>
    <td><RowActions status={record.status} canEdit={canEdit} canRetire={canRetire} onEdit={onEdit} onRetire={onRetire} /></td>
  </tr>;
}

function RecordDialog({ target, metadata, inline, onClose, onSave }: {
  target: RecordEditorTarget;
  metadata: Metadata | null;
  inline?: boolean; // render as a page panel (the Vendor Request Form view) instead of a modal
  onClose: () => void;
  onSave: (target: RecordEditorTarget, fromId: string, toId: string, properties: Properties, ciIDs: string[], links?: LinkAssignments) => Promise<void>;
}) {
  const nodeRecord = target.type === "node" ? target.record : undefined;
  const relationshipRecord = target.type === "relationship" ? target.record : undefined;
  const record = target.record;
  const initialProperties = target.record?.properties ?? (target.type === "node" ? target.defaults : undefined) ?? {};
  const knownNames = target.type === "node" ? knownKeysFor(target.kind, initialProperties, metadata) : new Set<string>();
  const requiresCIs = target.type === "node" && (target.kind === "incident" || target.kind === "change");
  const isIdentity = target.type === "node" && target.kind === "identity";
  const generatedID = target.type === "node" && !record;
  const ciRelationshipKind: RelationshipKind = target.type === "node" && target.kind === "change" ? "changes" : "affects";
  const [fromId, setFromId] = useState(relationshipRecord?.fromId ?? "");
  const [toId, setToId] = useState(relationshipRecord?.toId ?? "");
  const [relationshipKind, setRelationshipKind] = useState<RelationshipKind>(relationshipRecord?.kind ?? (target.type === "relationship" ? target.kind : undefined) ?? "has-job-code");
  const [properties, setProperties] = useState<Properties>(() => {
    const known = Object.fromEntries(Object.entries(initialProperties).filter(([key]) => knownNames.has(key)));
    // Boolean form fields default unchecked on create so save always sends a value.
    if (target.type === "node" && target.kind === "ci" && typeof known.critical !== "boolean") known.critical = false;
    if (target.type === "node" && target.kind === "incident" && typeof known.nodeDown !== "boolean") known.nodeDown = false;
    return known;
  });
  const [attributes, setAttributes] = useState(() => Object.entries(initialProperties).filter(([key]) => !knownNames.has(key)).map(([name, value]) => ({ name, value: String(value), type: typeof value === "boolean" ? "boolean" : typeof value === "number" ? "number" : "text" })));
  const [hours, setHours] = useState<HoursState>(() => hoursStateFrom(initialProperties));
  const [formError, setFormError] = useState("");
  const [availableCIs, setAvailableCIs] = useState<GraphNode[]>([]);
  const [loadingCIs, setLoadingCIs] = useState(false);
  const [ciLoadError, setCILoadError] = useState("");
  const [availableFromNodes, setAvailableFromNodes] = useState<GraphNode[]>([]);
  const [availableToNodes, setAvailableToNodes] = useState<GraphNode[]>([]);
  const [loadingEndpoints, setLoadingEndpoints] = useState(false);
  const [endpointLoadError, setEndpointLoadError] = useState("");
  // Pre-selected CIs come from the record's existing links when editing, or from the
  // launching view (e.g. "Create incident" on a map record) when creating.
  const [selectedCIIDs, setSelectedCIIDs] = useState<string[]>(() => nodeRecord?.relationships?.filter((relationship) => relationship.kind === ciRelationshipKind).map((relationship) => relationship.toId) ?? (target.type === "node" ? target.ciIds : undefined) ?? []);
  const editing = Boolean(record);
  const isIncident = target.type === "node" && target.kind === "incident";
  const hasRACI = target.type === "node" && (metadata?.raciKinds ?? raciOwnedKinds).includes(target.kind);
  const [raci, setRACI] = useState<RACIAssignments>(() => hasRACI ? raciFromRecord(nodeRecord) : emptyRACI);
  // Details and RACI live on separate tabs; both stay mounted so nothing typed is lost when switching.
  const [tab, setTab] = useState<"details" | "raci">("details");
  // Roles and entitlements name the application that provides access to them.
  const isAccessItem = target.type === "node" && (metadata?.accessItemKinds ?? ["role", "entitlement"]).includes(target.kind);
  const providedByKind = target.type === "node" ? applicationLinkKind(target.kind) : hasRoleKind;
  const [providedBy, setProvidedBy] = useState<string[]>(() => isAccessItem ? linksFromRecord(nodeRecord, [providedByKind])[providedByKind] : []);
  // Exception access: identities permissioned to this role or entitlement directly.
  const [directAssignees, setDirectAssignees] = useState<string[]>(() => isAccessItem ? linksFromRecord(nodeRecord, [permissionsKind])[permissionsKind] : []);
  // Incident assignment is a single identity or group, stored as an assigned-to link.
  const [assignedTo, setAssignedTo] = useState<string[]>(() => {
    if (!isIncident) return [];
    const existing = linksFromRecord(nodeRecord, [incidentAssignmentKind])[incidentAssignmentKind];
    if (existing.length > 0) return existing;
    return target.type === "node" ? target.assignedTo ?? [] : [];
  });
  // Group membership is a set of identities, stored as member links from the group.
  const isGroup = target.type === "node" && target.kind === "group";
  const [members, setMembers] = useState<string[]>(() => isGroup ? linksFromRecord(nodeRecord, [memberKind])[memberKind] : []);
  // A birthright grants roles and entitlements (grants / granted-by to either kind).
  const isBirthright = target.type === "node" && target.kind === "birthright";
  const grantedIDs = isBirthright ? linksFromRecord(nodeRecord, [grantsKind])[grantsKind] : [];
  const [roleOptions, setRoleOptions] = useState<GraphNode[]>([]);
  const [entitlementOptions, setEntitlementOptions] = useState<GraphNode[]>([]);
  const [loadingGrants, setLoadingGrants] = useState(isBirthright);
  const [grantsLoadError, setGrantsLoadError] = useState("");
  const [birthrightRoles, setBirthrightRoles] = useState<string[]>([]);
  const [birthrightEntitlements, setBirthrightEntitlements] = useState<string[]>([]);
  const grantsSeeded = useRef(false);
  const [qualifyingJobCodes, setQualifyingJobCodes] = useState<string[]>(() => isBirthright ? linksFromRecord(nodeRecord, [qualifiesForKind])[qualifiesForKind] : []);
  const [birthrightJobOptions, setBirthrightJobOptions] = useState<GraphNode[]>([]);
  const [loadingBirthrightJobs, setLoadingBirthrightJobs] = useState(isBirthright);
  const [birthrightJobLoadError, setBirthrightJobLoadError] = useState("");
  // A role includes entitlements (includes / included-by).
  const isRole = target.type === "node" && target.kind === "role";
  const includedIDs = isRole ? linksFromRecord(nodeRecord, [includesKind])[includesKind] : [];
  const [loadingIncludes, setLoadingIncludes] = useState(isRole);
  const [includesLoadError, setIncludesLoadError] = useState("");
  const [roleIncludes, setRoleIncludes] = useState<string[]>([]);
  const includesSeeded = useRef(false);
  // A job code qualifies for birthrights (qualifies-for / qualified-by).
  const isJobCode = target.type === "node" && target.kind === "job-code";
  const [qualifiedBirthrights, setQualifiedBirthrights] = useState<string[]>(() => isJobCode ? linksFromRecord(nodeRecord, [qualifiesForKind])[qualifiesForKind] : []);
  const [jobBirthrightOptions, setJobBirthrightOptions] = useState<GraphNode[]>([]);
  const [loadingJobBirthrights, setLoadingJobBirthrights] = useState(isJobCode);
  const [jobBirthrightLoadError, setJobBirthrightLoadError] = useState("");
  // A catalog request belongs to one requester. While a draft the link is
  // drafted-request and can be changed; once submitted it reads form-submitted
  // and is fixed to whoever submitted it.
  const isRequest = target.type === "node" && target.kind === "request";
  const requestState: RequestState = isRequest ? requestStateOf(nodeRecord) : "draft";
  // Once submitted the form belongs to the workflow: the requester is fixed and
  // changes come from the step assignees, so the requester's view is read-only.
  const requestSubmitted = isRequest && requestState !== "draft";
  const [requester, setRequester] = useState<string[]>(() => isRequest ? linksFromRecord(nodeRecord, [draftedRequestKind, formSubmittedKind])[requestSubmitted ? formSubmittedKind : draftedRequestKind] : []);
  // Which button sent the form: a draft save or a submission.
  const requestAction = useRef<"draft" | "submit">("draft");
  // Progress through the workflow: the tasks raised for this request, done and pending.
  const returnComment = isRequest && typeof nodeRecord?.properties?.returnComment === "string" ? nodeRecord.properties.returnComment : "";
  const showsWorkflow = isRequest && Boolean(nodeRecord) && (requestSubmitted || Boolean(returnComment));
  const [formSLA, setFormSLA] = useState<{ enabled: boolean; days: number }>(() => ({
    enabled: Boolean(nodeRecord?.properties?.slaEnabled),
    days: typeof nodeRecord?.properties?.slaBusinessDays === "number" ? nodeRecord.properties.slaBusinessDays : Number(nodeRecord?.properties?.slaBusinessDays) || 0,
  }));
  useEffect(() => {
    if (!isRequest) return;
    if (editing && requestSubmitted) {
      setFormSLA({
        enabled: Boolean(nodeRecord?.properties?.slaEnabled),
        days: typeof nodeRecord?.properties?.slaBusinessDays === "number" ? nodeRecord.properties.slaBusinessDays : Number(nodeRecord?.properties?.slaBusinessDays) || 0,
      });
      return;
    }
    getFormSLA("vendor").then((sla) => {
      setFormSLA({ enabled: Boolean(sla.enabled), days: sla.businessDays || 0 });
    }).catch(() => setFormSLA({ enabled: false, days: 0 }));
  }, [isRequest, editing, requestSubmitted, nodeRecord]);
  const [requestTasks, setRequestTasks] = useState<TaskView[]>([]);
  const [requestTasksError, setRequestTasksError] = useState("");
  useEffect(() => {
    if (!showsWorkflow || !nodeRecord) return;
    let active = true;
    getTasks({ requestId: nodeRecord.id, includeDone: true }).then((items) => {
      if (active) setRequestTasks(items);
    }).catch((loadError: unknown) => {
      if (active) setRequestTasksError(loadError instanceof Error ? loadError.message : "Could not load workflow progress.");
    });
    return () => { active = false; };
  }, [showsWorkflow, nodeRecord]);
  // Identities and groups back the RACI, assignment, requester, and group-member pickers.
  const needsPeople = hasRACI || isIncident || isRequest || isGroup;
  const [raciOptions, setRACIOptions] = useState<GraphNode[]>([]);
  const [loadingRACI, setLoadingRACI] = useState(false);
  const [raciLoadError, setRACILoadError] = useState("");
  const knownFields = target.type === "node" ? nodeFieldsFor(target.kind, properties, metadata) : [];
  const isLocation = target.type === "node" && target.kind === "ci" && selectedCIType(properties, metadata) === "location";
  const kindLabel = target.type === "node" ? resourceInfo[target.kind].singular : "relationship";

  useEffect(() => {
    if (!isLocation || typeof properties.timezone === "string") return;
    const browserZone = Intl.DateTimeFormat().resolvedOptions().timeZone;
    const defaultZone = timeZoneOptions.includes(browserZone) ? browserZone : timeZoneOptions[0];
    setProperties((current) => typeof current.timezone === "string" ? current : { ...current, timezone: defaultZone });
  }, [isLocation, properties.timezone]);
  const relationshipDefinition = target.type === "relationship"
    ? metadata?.relationships.find((definition) => definition.kind === relationshipKind)
    : undefined;
  // The To list follows the chosen From record when the kind has per-pairing rules.
  const toCITypes = allowedToCITypes(relationshipDefinition, availableFromNodes.find((node) => node.id === fromId));
  const toOptions = filterByCITypes(availableToNodes, toCITypes);
  const chooseFromId = (nextFromId: string) => {
    setFromId(nextFromId);
    const nextToTypes = allowedToCITypes(relationshipDefinition, availableFromNodes.find((node) => node.id === nextFromId));
    if (toId && !filterByCITypes(availableToNodes, nextToTypes).some((node) => node.id === toId)) setToId("");
  };

  useEffect(() => {
    if (!requiresCIs && !isIdentity && !isAccessItem) return;
    let active = true;
    setLoadingCIs(true);
    getNodes("ci", false).then((items) => {
      if (active) setAvailableCIs(items);
    }).catch((loadError: unknown) => {
      if (active) setCILoadError(loadError instanceof Error ? loadError.message : "Could not load CIs.");
    }).finally(() => {
      if (active) setLoadingCIs(false);
    });
    return () => { active = false; };
  }, [requiresCIs, isIdentity, isAccessItem]);
  const applicationCIs = availableCIs.filter((ci) => ci.properties?.ciType === "application");
  useEffect(() => {
    if (!needsPeople) return;
    let active = true;
    setLoadingRACI(true);
    Promise.all([getNodes("identity", false), getNodes("group", false)]).then(([identities, groups]) => {
      if (active) setRACIOptions([...identities, ...groups]);
    }).catch((loadError: unknown) => {
      if (active) setRACILoadError(loadError instanceof Error ? loadError.message : "Could not load identities and groups.");
    }).finally(() => {
      if (active) setLoadingRACI(false);
    });
    return () => { active = false; };
  }, [needsPeople]);
  const raciIdentities = raciOptions.filter((node) => node.kind === "identity");

  useEffect(() => {
    if (!isBirthright) return;
    let active = true;
    setLoadingGrants(true);
    Promise.all([getNodes("role", false), getNodes("entitlement", false)]).then(([roles, entitlements]) => {
      if (!active) return;
      setRoleOptions(roles);
      setEntitlementOptions(entitlements);
    }).catch((loadError: unknown) => {
      if (active) setGrantsLoadError(loadError instanceof Error ? loadError.message : "Could not load roles and entitlements.");
    }).finally(() => {
      if (active) setLoadingGrants(false);
    });
    return () => { active = false; };
  }, [isBirthright]);
  // Split existing grants across the two pickers once the option lists are in.
  useEffect(() => {
    if (!isBirthright || grantsSeeded.current || loadingGrants) return;
    setBirthrightRoles(grantedIDs.filter((id) => roleOptions.some((role) => role.id === id) || id.startsWith("ROLE-")));
    setBirthrightEntitlements(grantedIDs.filter((id) => entitlementOptions.some((item) => item.id === id) || id.startsWith("ENT-")));
    grantsSeeded.current = true;
  }, [isBirthright, loadingGrants, roleOptions, entitlementOptions, grantedIDs]);

  useEffect(() => {
    if (!isBirthright) return;
    let active = true;
    setLoadingBirthrightJobs(true);
    getNodes("job-code", false).then((items) => {
      if (active) setBirthrightJobOptions(items);
    }).catch((loadError: unknown) => {
      if (active) setBirthrightJobLoadError(loadError instanceof Error ? loadError.message : "Could not load job codes.");
    }).finally(() => {
      if (active) setLoadingBirthrightJobs(false);
    });
    return () => { active = false; };
  }, [isBirthright]);

  useEffect(() => {
    if (!isRole) return;
    let active = true;
    setLoadingIncludes(true);
    getNodes("entitlement", false).then((entitlements) => {
      if (active) setEntitlementOptions(entitlements);
    }).catch((loadError: unknown) => {
      if (active) setIncludesLoadError(loadError instanceof Error ? loadError.message : "Could not load entitlements.");
    }).finally(() => {
      if (active) setLoadingIncludes(false);
    });
    return () => { active = false; };
  }, [isRole]);
  useEffect(() => {
    if (!isRole || includesSeeded.current || loadingIncludes) return;
    setRoleIncludes(includedIDs.filter((id) => entitlementOptions.some((item) => item.id === id) || id.startsWith("ENT-")));
    includesSeeded.current = true;
  }, [isRole, loadingIncludes, entitlementOptions, includedIDs]);

  useEffect(() => {
    if (!isJobCode) return;
    let active = true;
    setLoadingJobBirthrights(true);
    getNodes("birthright", false).then((items) => {
      if (active) setJobBirthrightOptions(items);
    }).catch((loadError: unknown) => {
      if (active) setJobBirthrightLoadError(loadError instanceof Error ? loadError.message : "Could not load birthrights.");
    }).finally(() => {
      if (active) setLoadingJobBirthrights(false);
    });
    return () => { active = false; };
  }, [isJobCode]);

  // An identity's job code and birthrights are set on create and updated when
  // they change jobs. The birthright itself is not linked: the form works out
  // which job code qualifies for it and reconciles has-job-code links, which
  // is how the access chain reads it.
  const picksBirthrights = isIdentity;
  const existingJobCodeIDs = isIdentity ? linksFromRecord(nodeRecord, [hasJobCodeKind])[hasJobCodeKind] : [];
  const [jobCodeOptions, setJobCodeOptions] = useState<GraphNode[]>([]);
  const [birthrightOptions, setBirthrightOptions] = useState<GraphNode[]>([]);
  // Start loading so edit does not seed birthrights from an empty options list.
  const [loadingBirthrights, setLoadingBirthrights] = useState(isIdentity);
  const [birthrightLoadError, setBirthrightLoadError] = useState("");
  const [jobCode, setJobCode] = useState<string[]>(() => existingJobCodeIDs.slice(0, 1));
  const [birthrights, setBirthrights] = useState<string[]>([]);
  // When more than one job code qualifies for a chosen birthright, the user picks which one applies.
  const [birthrightVia, setBirthrightVia] = useState<Record<string, string>>({});
  const birthrightsSeeded = useRef(false);
  useEffect(() => {
    if (!picksBirthrights) return;
    let active = true;
    setLoadingBirthrights(true);
    Promise.all([getNodes("job-code", false), getNodes("birthright", false)]).then(([codes, rights]) => {
      if (!active) return;
      setJobCodeOptions(codes);
      setBirthrightOptions(rights);
    }).catch((loadError: unknown) => {
      if (active) setBirthrightLoadError(loadError instanceof Error ? loadError.message : "Could not load job codes and birthrights.");
    }).finally(() => {
      if (active) setLoadingBirthrights(false);
    });
    return () => { active = false; };
  }, [picksBirthrights]);
  // On edit, fill birthrights from every job code the identity already has so a
  // save without changes keeps the same has-job-code set (including extras that
  // exist only to carry a birthright).
  useEffect(() => {
    if (!picksBirthrights || birthrightsSeeded.current || loadingBirthrights) return;
    if (!editing) {
      birthrightsSeeded.current = true;
      return;
    }
    const primary = jobCode[0] ?? existingJobCodeIDs[0] ?? "";
    const held: string[] = [];
    const via: Record<string, string> = {};
    for (const jobID of existingJobCodeIDs) {
      const job = jobCodeOptions.find((option) => option.id === jobID);
      for (const right of linkedEnds(job, qualifiesForKind, "to")) {
        if (!held.includes(right.id)) held.push(right.id);
        if (jobID !== primary) via[right.id] = jobID;
      }
    }
    setBirthrights(held);
    setBirthrightVia(via);
    if (jobCode.length === 0 && existingJobCodeIDs.length > 0) setJobCode([existingJobCodeIDs[0]]);
    birthrightsSeeded.current = true;
  }, [picksBirthrights, editing, loadingBirthrights, jobCodeOptions, existingJobCodeIDs, jobCode]);
  const chosenJobCode = jobCodeOptions.find((node) => node.id === jobCode[0]);
  const jobCodeBirthrights = linkedEnds(chosenJobCode, qualifiesForKind, "to");
  // Each chosen birthright and the job codes that qualify for it; one already
  // covered by the chosen job code needs nothing more.
  const birthrightPlan = birthrights.flatMap((id) => {
    const node = birthrightOptions.find((option) => option.id === id);
    if (!node) return [];
    const qualifiers = linkedEnds(node, qualifiesForKind, "from");
    const covered = qualifiers.some((qualifier) => qualifier.id === jobCode[0]);
    const via = covered ? jobCode[0] : qualifiers.length === 1 ? qualifiers[0].id : birthrightVia[id] && qualifiers.some((qualifier) => qualifier.id === birthrightVia[id]) ? birthrightVia[id] : "";
    return [{ node, name: nodeDisplayName(node) ?? node.id, qualifiers, covered, via, roles: linkedEnds(node, grantsKind, "to") }];
  });
  const birthrightCount = new Set([...birthrights, ...jobCodeBirthrights.map((right) => right.id)]).size;

  const locationCIs = availableCIs
    .filter((ci) => ci.properties?.ciType === "location" && typeof ci.properties?.name === "string" && ci.properties.name)
    .map((ci) => ({ id: ci.id, name: String(ci.properties?.name), siteId: typeof ci.properties?.siteId === "string" ? ci.properties.siteId : "" }))
    .sort((a, b) => a.name.localeCompare(b.name));

  useEffect(() => {
    if (target.type !== "relationship" || editing || !relationshipDefinition) return;
    let active = true;
    setLoadingEndpoints(true);
    setEndpointLoadError("");
    setFromId("");
    setToId("");
    // Some kinds accept more than one node kind at an end: the RACI kinds start
    // from any owned record, and the access kinds end on a role or an entitlement.
    Promise.all([
      Promise.all(relationshipFromKinds(relationshipDefinition).map((kind) => getNodes(kind, false))).then((groups) => groups.flat()),
      Promise.all(relationshipToKinds(relationshipDefinition).map((kind) => getNodes(kind, false))).then((groups) => groups.flat()),
    ]).then(([fromNodes, toNodes]) => {
      if (!active) return;
      // Some kinds (governs, provides, work-location) only accept specific CI types at an end.
      setAvailableFromNodes(filterByCITypes(fromNodes, relationshipDefinition.fromCiTypes));
      setAvailableToNodes(filterByCITypes(toNodes, relationshipDefinition.toCiTypes));
    }).catch((loadError: unknown) => {
      if (active) setEndpointLoadError(loadError instanceof Error ? loadError.message : "Could not load relationship endpoints.");
    }).finally(() => {
      if (active) setLoadingEndpoints(false);
    });
    return () => { active = false; };
  }, [target.type, editing, relationshipDefinition]);

  function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const values: Properties = { ...properties };
    if (target.type === "node" && target.kind === "ci") {
      if (typeof values.ciType !== "string") values.ciType = metadata?.ciTypes[0] ?? "server";
      const categorized = categorizedCITypes.has(values.ciType);
      const location = values.ciType === "location";
      const contract = values.ciType === "contract";
      const application = values.ciType === "application";
      const vendor = values.ciType === "vendor";
      const described = describedCITypes.has(values.ciType);
      if (categorized && typeof values.category !== "string") values.category = metadata?.ciCategories?.[0] ?? categoryOptions[0];
      if (contract && typeof values.contractType !== "string") values.contractType = metadata?.contractTypes?.[0] ?? contractTypeOptions[0];
      if (application && typeof values.hosted !== "string") values.hosted = metadata?.hostingModels?.[0] ?? hostingOptions[0];
      if (vendor && typeof values.criticality !== "string") values.criticality = metadata?.criticalities?.[0] ?? criticalityOptions[0];
      values.critical = values.critical === true;
      if (!contract) delete values.contractType;
      if (!application) delete values.hosted;
      if (!vendor) delete values.criticality;
      if (!categorized) {
        delete values.category;
        if (!contract && !vendor && !described) delete values.description;
        if (!location && !contract && !vendor && !described) delete values.name;
      }
      if (location) {
        if (typeof values.state !== "string" || !usStateCodes.includes(values.state)) { setFormError("Select a two-letter state abbreviation."); return; }
        values.country = fixedCountry;
        for (const key of locationSystemKeys) delete values[key];
        for (const day of weekdays) {
          const entry = hours[day];
          if (entry.closed) { values[hoursKey(day)] = hoursClosed; continue; }
          if (!entry.open || !entry.close) { setFormError(`Enter opening and closing times for ${day}, or mark it closed.`); return; }
          values[hoursKey(day)] = `${entry.open}-${entry.close}`;
        }
      } else {
        for (const field of locationFields) if (field.key !== "name") delete values[field.key];
        for (const day of weekdays) delete values[hoursKey(day)];
        for (const key of locationSystemKeys) delete values[key];
      }
    }
    if (target.type === "node" && target.kind === "incident") {
      values.nodeDown = values.nodeDown === true;
    }
    if (requiresCIs && selectedCIIDs.length === 0) {
      setFormError("Select at least one active CI before creating this record.");
      return;
    }
    if (isRequest) {
      // The server owns the lifecycle fields; drafts simply omit anything left blank.
      for (const key of requestSystemKeys) delete values[key];
      for (const [key, value] of Object.entries(values)) if (typeof value === "string" && value.trim() === "") delete values[key];
      if (!editing) values.requestType = metadata?.requestTypes?.[0] ?? "vendor";
      if (requester.length === 0) { setFormError("Choose the requester before saving this request."); return; }
      if (requestAction.current === "submit") {
        const missing = vendorRequestFields.filter((field) => vendorRequestRequiredKeys.includes(field.key) && (typeof values[field.key] !== "string" || !String(values[field.key]).trim())).map((field) => field.label);
        if (missing.length > 0) { setFormError(`Complete ${missing.join(" and ")} before submitting, or save the request as a draft.`); return; }
        values.state = "submitted";
      }
    }
    if (typeof values.contactEmail === "string" && values.contactEmail.trim() && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(values.contactEmail.trim())) {
      setFormError("Enter the contact email as name@example.com, or leave it blank.");
      return;
    }
    if (typeof values.contactPhone === "string" && values.contactPhone.trim()) {
      const phone = normalizePhoneNumber(values.contactPhone);
      if (!phone) { setFormError("Enter a complete phone number such as (555) 010-0100 or +44 20 7123 4567, or leave it blank."); return; }
      values.contactPhone = phone;
    }
    for (const attribute of attributes) {
      const name = attribute.name.trim();
      const value = attribute.value.trim();
      if (!name && !value) continue;
      if (!name || !value) { setFormError("Enter both an attribute name and value, or remove the empty row."); return; }
      if (Object.hasOwn(values, name)) { setFormError(`Attribute “${name}” is already defined.`); return; }
      if (attribute.type === "number") {
        const number = Number(value);
        if (!Number.isFinite(number)) { setFormError(`Attribute “${name}” must be a valid number.`); return; }
        values[name] = number;
      } else if (attribute.type === "boolean") {
        if (value !== "true" && value !== "false") { setFormError(`Attribute “${name}” must be true or false.`); return; }
        values[name] = value === "true";
      } else values[name] = value;
    }
    // The identity's job code, plus the job code behind each chosen birthright,
    // become has-job-code links (created or retired on save); roles then follow from the graph.
    const jobCodeIDs = [...jobCode];
    if (picksBirthrights) {
      for (const plan of birthrightPlan) {
        if (plan.covered) continue;
        if (plan.qualifiers.length === 0) { setFormError(`No job code qualifies for ${plan.name} yet. Attach a job code on that birthright, or pick a different birthright.`); return; }
        if (!plan.via) { setFormError(`Choose which job code ${plan.name} comes through.`); return; }
        if (!jobCodeIDs.includes(plan.via)) jobCodeIDs.push(plan.via);
      }
    }
    setFormError("");
    // New identities link to their chosen location CI with a work-location relationship.
    const linkedCIIDs = isIdentity && !editing
      ? locationCIs.filter((ci) => ci.name === values.location).map((ci) => ci.id)
      : selectedCIIDs;
    // A submitted request's requester is fixed, so only drafts reconcile the drafted-request link.
    // Identity job codes always reconcile (including an empty list when the job is cleared).
    const links: LinkAssignments | undefined = hasRACI ? { ...raci, ...(isAccessItem ? { [providedByKind]: providedBy, [permissionsKind]: directAssignees } : {}), ...(isBirthright ? { [grantsKind]: [...birthrightRoles, ...birthrightEntitlements], [qualifiesForKind]: qualifyingJobCodes } : {}), ...(isRole ? { [includesKind]: roleIncludes } : {}), ...(isJobCode ? { [qualifiesForKind]: qualifiedBirthrights } : {}) } : isIncident ? { [incidentAssignmentKind]: assignedTo } : isRequest && !requestSubmitted ? { [draftedRequestKind]: requester } : isGroup ? { [memberKind]: members } : picksBirthrights ? { [hasJobCodeKind]: jobCodeIDs } : undefined;
    void onSave(target.type === "relationship" && !record ? { ...target, kind: relationshipKind } : target, fromId.trim(), toId.trim(), values, linkedCIIDs, links);
  }

  function updateAttribute(index: number, field: "name" | "value" | "type", value: string) {
    setAttributes((current) => current.map((attribute, item) => item === index ? { ...attribute, [field]: value } : attribute));
  }

  const form = <form onSubmit={submit}>
        <div className="dialog-heading">
          <div><p className="eyebrow">{isRequest ? (editing ? `${requestState.toUpperCase()} REQUEST${nodeRecord ? ` · ${nodeRecord.id}` : ""}` : "NEW REQUEST") : editing ? "UPDATE RECORD" : "NEW RECORD"}</p><h2 id="dialog-title">{isRequest ? "Vendor Request Form" : `${editing ? "Edit" : "Add"} ${target.type === "relationship" ? "relationship" : kindLabel}`}</h2></div>
          {(!inline || editing) && <button className="close-button" type="button" onClick={onClose} aria-label={inline ? "Start a new request" : "Close"} title={inline ? "Start a new request" : undefined}><X size={17} /></button>}
        </div>
        {hasRACI && <div className="dialog-tabs" role="tablist" aria-label="Record sections">
          <button type="button" role="tab" id="tab-details" aria-selected={tab === "details"} aria-controls="panel-details" className={tab === "details" ? "active" : ""} onClick={() => setTab("details")}>Details</button>
          <button type="button" role="tab" id="tab-raci" aria-selected={tab === "raci"} aria-controls="panel-raci" className={tab === "raci" ? "active" : ""} onClick={() => setTab("raci")}>RACI<span className="tab-count">{raciRoles.reduce((total, { role }) => total + raci[role].length, 0)}</span></button>
        </div>}
        {target.type === "node" ? <>
          <div id="panel-details" role={hasRACI ? "tabpanel" : undefined} aria-labelledby={hasRACI ? "tab-details" : undefined} hidden={hasRACI && tab !== "details"}>
          {generatedID
            ? <p className="system-generated-id">The {titleCase(target.kind)} ID will be assigned automatically when created.</p>
            : <div className="form-field"><label htmlFor="record-id">{target.kind === "job-code" ? "Code" : "Identifier"}</label><input id="record-id" value={nodeRecord?.id ?? ""} readOnly /><span className="field-hint">Stable identifiers cannot be changed after creation.</span></div>}
          {isRequest && <section className="ci-picker raci-section request-section" aria-labelledby="requester-title">
            <div className="ci-picker-heading"><div><h3 id="requester-title">Requester</h3>{!requestSubmitted && <span className="required-mark">Required</span>}</div><span className={`status-pill ${requestStatePillClass(requestState)}`}>{requestState}</span></div>
            {returnComment && <div className="return-notice" role="status"><strong>Returned to you{typeof nodeRecord?.properties?.returnedAt === "string" ? ` ${new Date(nodeRecord.properties.returnedAt).toLocaleString([], { dateStyle: "medium", timeStyle: "short" })}` : ""}</strong><p>{returnComment}</p><span className="field-hint">Update the form and submit it again to restart the workflow.</span></div>}
            {loadingRACI ? <p className="field-hint">Loading identities…</p> : raciLoadError ? <p className="form-error" role="alert">{raciLoadError}</p> : raciIdentities.length === 0 && !editing ? <p className="ci-empty">No active identities are available to raise a request.</p>
              : <RecordPicker id="request-requester" label={requestSubmitted ? "Submitted by" : "Who is this request for?"} hint={requestSubmitted ? `Submitted ${typeof nodeRecord?.properties?.submittedAt === "string" ? new Date(nodeRecord.properties.submittedAt).toLocaleString([], { dateStyle: "medium", timeStyle: "short" }) : ""}. The requester is fixed once a request is submitted.` : "Saved as a drafted-request link; it becomes form-submitted when the request is submitted."} placeholder="Find an identity by name or ID" single readOnly={requestSubmitted} options={raciIdentities} selected={requester} onChange={setRequester} />}
            <SLANotice enabled={formSLA.enabled} days={formSLA.days} />
          </section>}
          <div className="field-grid">{knownFields.map((field, index) => {
            // A field that opens a new group gets a full-width heading in front of it.
            const groupHeading = field.group && knownFields[index - 1]?.group !== field.group
              ? <h3 className="field-group-heading" key={`group-${field.group}`}>{field.group}</h3>
              : null;
            const withHeading = (control: React.ReactNode) => groupHeading ? <Fragment key={field.key}>{groupHeading}{control}</Fragment> : control;
            const options = field.key === "ciType" ? metadata?.ciTypes ?? field.options : field.key === "category" ? metadata?.ciCategories ?? field.options : field.key === "contractType" ? metadata?.contractTypes ?? field.options : field.key === "hosted" ? metadata?.hostingModels ?? field.options : field.key === "criticality" ? metadata?.criticalities ?? field.options : field.key === "timezone" ? timeZoneChoices(properties.timezone) : field.options;
            const optionLabel = (option: string) => field.key === "timezone" ? timeZoneLabel(option) : field.key === "state" ? option : field.key === "contractType" ? contractTypeLabels[option] ?? titleCase(option) : titleCase(option);
            if (isLocation && field.key === "country") {
              return <div className="form-field" key={field.key}><label htmlFor={`field-${field.key}`}>{field.label}</label><input id={`field-${field.key}`} value={fixedCountry} readOnly /></div>;
            }
            if (isIdentity && field.key === "location") {
              const current = typeof properties.location === "string" ? properties.location : "";
              const names = locationCIs.map((ci) => ci.name);
              const choices = current && !names.includes(current) ? [current, ...names] : names;
              return <div className="form-field" key={field.key}><label htmlFor={`field-${field.key}`}>{field.label}</label>
                <select id={`field-${field.key}`} value={current} disabled={loadingCIs} onChange={(event) => setProperties((prev) => {
                  const next = { ...prev };
                  if (event.target.value) next.location = event.target.value; else delete next.location;
                  return next;
                })}>
                  <option value="">{loadingCIs ? "Loading locations…" : choices.length === 0 ? "No locations defined" : "Select a location"}</option>
                  {choices.map((name) => {
                    const ci = locationCIs.find((item) => item.name === name);
                    return <option key={name} value={name}>{name}{ci?.siteId ? ` (${ci.siteId})` : ""}</option>;
                  })}</select>
                {ciLoadError ? <span className="form-error" role="alert">{ciLoadError}</span> : !loadingCIs && locationCIs.length === 0 ? <span className="field-hint">Add a Location configuration item under CMDB to populate this list.</span> : <span className="field-hint">{editing ? "The work-location link is managed under Relationships." : "Creates a work-location link to the selected site."}</span>}</div>;
            }
            if (field.key === "state" && options) {
              const stateValue = typeof properties.state === "string" && options.includes(properties.state) ? properties.state : "";
              return <div className="form-field" key={field.key}><label htmlFor={`field-${field.key}`}>{field.label}</label>
                <select id={`field-${field.key}`} value={stateValue} onChange={(event) => setProperties((current) => ({ ...current, state: event.target.value }))}>
                  <option value="">Select a state</option>{options.map((option) => <option key={option} value={option}>{option}</option>)}</select></div>;
            }
            if (isRequest && options) {
              // Required choices on a request start blank so a draft never carries a value nobody picked.
              const chosen = typeof properties[field.key] === "string" && options.includes(String(properties[field.key])) ? String(properties[field.key]) : "";
              return <div className="form-field" key={field.key}><label htmlFor={`field-${field.key}`}>{field.label} <span className="ci-selection-count">{field.key === "description" ? "" : "Required to submit"}</span></label>
                <select id={`field-${field.key}`} value={chosen} onChange={(event) => setProperties((current) => { const next = { ...current }; if (event.target.value) next[field.key] = event.target.value; else delete next[field.key]; return next; })}>
                  <option value="">Select {field.label.toLowerCase()}</option>{options.map((option) => <option key={option} value={option}>{optionLabel(option)}</option>)}</select></div>;
            }
            if (field.key === "contactPhone") {
              // The phone field keeps its format while typing; deleting a formatting character removes the digit before it.
              return withHeading(<div className="form-field" key={field.key}><label htmlFor={`field-${field.key}`}>{field.label}</label>
                <input id={`field-${field.key}`} type="tel" inputMode="tel" autoComplete="off" placeholder="(555) 010-0100" value={String(properties.contactPhone ?? "")} onChange={(event) => setProperties((current) => {
                  const previous = String(current.contactPhone ?? "");
                  let raw = event.target.value;
                  if (raw.length < previous.length && raw.replace(/\D/g, "") === previous.replace(/\D/g, "")) raw = raw.replace(/\d(?=\D*$)/, "");
                  const next = { ...current };
                  const formatted = formatPhoneInput(raw);
                  if (formatted) next.contactPhone = formatted; else delete next.contactPhone;
                  return next;
                })} />
                <span className="field-hint">Enter 10 digits for a North American number, or start with + for an international number.</span></div>);
            }
            if (field.checkbox) {
              return withHeading(<div className="form-field" key={field.key}>
                <label className="retired-toggle" htmlFor={`field-${field.key}`}>
                  <input id={`field-${field.key}`} type="checkbox" checked={properties[field.key] === true} onChange={(event) => setProperties((current) => ({ ...current, [field.key]: event.target.checked }))} />
                  <span>{field.label}</span>
                </label>
                {field.hint && <span className="field-hint">{field.hint}</span>}
              </div>);
            }
            const inputType = field.key === "contactEmail" ? "email" : "text";
            return withHeading(<div className="form-field" key={field.key}><label htmlFor={`field-${field.key}`}>{field.label}{isRequest && vendorRequestRequiredKeys.includes(field.key) && <> <span className="ci-selection-count">Required to submit</span></>}</label>{options
              ? <select id={`field-${field.key}`} value={String(properties[field.key] ?? options[0])} onChange={(event) => setProperties((current) => ({ ...current, [field.key]: event.target.value }))}>{options.map((option) => <option key={option} value={option}>{optionLabel(option)}</option>)}</select>
              : <input id={`field-${field.key}`} type={inputType} autoComplete="off" value={String(properties[field.key] ?? "")} onChange={(event) => setProperties((current) => ({ ...current, [field.key]: event.target.value }))} />}</div>);
          })}</div>
          {showsWorkflow && <section className="ci-picker request-section" aria-labelledby="workflow-progress-title">
            <div className="ci-picker-heading"><div><h3 id="workflow-progress-title">Workflow progress</h3></div><span className="ci-selection-count">{requestTasks[0]?.workflowName ?? ""}</span></div>
            {requestTasksError ? <p className="form-error" role="alert">{requestTasksError}</p> : requestTasks.length === 0 ? <p className="ci-empty">{requestState === "submitted" ? "No workflow is enabled for this form, so the request is waiting to be fulfilled by hand." : "No workflow tasks recorded for this request."}</p>
              : <>
                {requestTasks[0]?.sla && <div className="agreement-row"><AgreementMeter clock={requestTasks[0].sla} /></div>}
                <ol className="task-trail">{requestTasks.map((task) => <li className={`task-trail-item task-${task.status}`} key={task.id}>
                <div className="task-trail-head"><strong>{task.step.order ? `${task.step.order}. ` : ""}{task.step.name}</strong><span className="agreement-inline"><AgreementMeter clock={task.ola} compact /><span className={`status-pill ${taskStatusPillClass(task.status)}`}>{task.status}</span></span></div>
                <div className="secondary-value">{titleCase(task.step.stepType)} · {task.assignees.map((assignee) => assignee.name ?? assignee.id).join(", ") || "Unassigned"}</div>
                {task.actions.map((action, index) => <div className="task-trail-action" key={index}><span>{titleCase(action.action)} by {action.actorName ?? action.actorId} · {new Date(action.actedAt).toLocaleString([], { dateStyle: "medium", timeStyle: "short" })}</span>{action.comment && <p>{action.comment}</p>}</div>)}
              </li>)}</ol>
              </>}
            {requestState === "fulfilled" && <p className="field-hint">The vendor CI was created from this request{nodeRecord?.relationships?.some((relationship) => relationship.kind === "fulfilled-by") ? ` (${nodeRecord.relationships.filter((relationship) => relationship.kind === "fulfilled-by").map((relationship) => relationship.toName ? `${relationship.toName} ${relationship.toId}` : relationship.toId).join(", ")})` : ""}. The request, its workflow run, and its tasks were retired when the workflow completed; the CI is the live record.</p>}
          </section>}
          {isLocation && <section className="ci-picker" aria-labelledby="hours-title">
            <div className="ci-picker-heading"><div><h3 id="hours-title">Hours of operation</h3></div><span className="ci-selection-count">{String(properties.timezone ?? "Local time")}</span></div>
            <div className="hours-list">{weekdays.map((day) => {
              const entry = hours[day];
              return <div className={`hours-row ${entry.closed ? "closed" : ""}`} key={day}>
                <span className="hours-day">{day}</span>
                <input type="time" value={entry.open} disabled={entry.closed} aria-label={`${day} opening time`} onChange={(event) => setHours((current) => ({ ...current, [day]: { ...current[day], open: event.target.value } }))} />
                <span className="hours-separator">to</span>
                <input type="time" value={entry.close} disabled={entry.closed} aria-label={`${day} closing time`} onChange={(event) => setHours((current) => ({ ...current, [day]: { ...current[day], close: event.target.value } }))} />
                <label className="retired-toggle"><input type="checkbox" checked={entry.closed} onChange={(event) => setHours((current) => ({ ...current, [day]: { ...current[day], closed: event.target.checked, open: current[day].open || "08:00", close: current[day].close || "17:00" } }))} /><span>Closed</span></label>
              </div>;
            })}</div>
            <p className="field-hint">Times are local to the site's time zone. A closing time at or before the opening time runs past midnight.</p>
            {(() => { const point = locationCoordinates(initialProperties); return <p className="field-hint">{point
              ? `Plotted on the location map at ${point.latitude.toFixed(4)}, ${point.longitude.toFixed(4)}${point.precision ? ` (${point.precision} precision)` : ""}. Coordinates refresh from the address when you save.`
              : "Coordinates for the location map are derived from the address when you save."}</p>; })()}
          </section>}
          {picksBirthrights && <section className="ci-picker raci-section" aria-labelledby="birthright-title">
            <div className="ci-picker-heading"><div><h3 id="birthright-title">Job code and birthrights</h3></div><span className="ci-selection-count">{birthrightCount} birthright{birthrightCount === 1 ? "" : "s"}</span></div>
            {loadingBirthrights ? <p className="field-hint">Loading job codes and birthrights…</p> : birthrightLoadError ? <p className="form-error" role="alert">{birthrightLoadError}</p> : <div className="raci-grid">
              <RecordPicker id="identity-job-code" label="Job code" hint={chosenJobCode
                ? jobCodeBirthrights.length > 0 ? `Saved as a has-job-code link. This job code qualifies for ${jobCodeBirthrights.map((right) => right.name).join(", ")}.` : "Saved as a has-job-code link. This job code does not qualify for any birthright yet."
                : editing ? "Clear or change the job code when the person moves roles; has-job-code links are reconciled on save." : "Saved as a has-job-code link; the job code's birthrights come with it."} placeholder="Find a job code by name or code" single options={jobCodeOptions} selected={jobCode} onChange={setJobCode} />
              <RecordPicker id="identity-birthrights" label="Birthright" hint={editing
                ? "Remove birthrights that no longer apply when the job changes so their job-code links are retired; adding one links the qualifying job code as well."
                : "A birthright reaches an identity through a job code that qualifies for it, so choosing one here links the identity to that job code as well."} placeholder="Find a birthright by name or ID" options={birthrightOptions} selected={birthrights} onChange={setBirthrights} />
            </div>}
            {birthrightPlan.length > 0 && <ul className="birthright-plan">{birthrightPlan.map((plan) => <li key={plan.node.id} className={plan.qualifiers.length === 0 ? "birthright-plan-blocked" : ""}>
              <strong>{plan.name}</strong>
              <span className="secondary-value">{plan.roles.length > 0 ? `Grants ${plan.roles.map((role) => role.name).join(", ")}` : "Grants no roles yet"}</span>
              {plan.qualifiers.length === 0
                ? <span className="form-error">No job code qualifies for this birthright. Open the birthright and attach a job code first.</span>
                : plan.covered || plan.qualifiers.length === 1
                  ? <span className="field-hint">Through job code {plan.qualifiers.find((qualifier) => qualifier.id === plan.via)?.name ?? plan.via}{plan.covered ? "" : " (added as has-job-code)"}.</span>
                  : <label className="birthright-via"><span className="field-hint">Through job code</span><select value={plan.via} onChange={(event) => setBirthrightVia((current) => ({ ...current, [plan.node.id]: event.target.value }))}><option value="">Choose a job code</option>{plan.qualifiers.map((qualifier) => <option key={qualifier.id} value={qualifier.id}>{qualifier.name}</option>)}</select></label>}
            </li>)}</ul>}
          </section>}
          {isAccessItem && <section className="ci-picker raci-section" aria-labelledby="provided-by-title">
            <div className="ci-picker-heading"><div><h3 id="provided-by-title">{providedByKind === entitledByKind ? "Entitlement for application" : "Role for application"}</h3></div></div>
            {loadingCIs ? <p className="field-hint">Loading applications…</p> : ciLoadError ? <p className="form-error" role="alert">{ciLoadError}</p> : applicationCIs.length === 0 ? <p className="ci-empty">No active application CIs are available. Add an Application configuration item under CMDB first.</p>
              : <RecordPicker id="provided-by" label="Application" hint={`${providedByKind === entitledByKind ? "Saved as an entitled-by link from the application (entitlement-for, read from this side)." : "Saved as a has-role link from the application (role-for, read from this side)."} Together with an Accountable owner on the RACI tab, this puts the ${kindLabel} on the Access Request Form.`} placeholder="Find an application by name or ID" single options={applicationCIs} selected={providedBy} onChange={setProvidedBy} />}
          </section>}
          {isBirthright && <section className="ci-picker raci-section" aria-labelledby="qualifying-job-title">
            <div className="ci-picker-heading"><div><h3 id="qualifying-job-title">Job codes</h3></div><span className="ci-selection-count">{qualifyingJobCodes.length} job code{qualifyingJobCodes.length === 1 ? "" : "s"}</span></div>
            {loadingBirthrightJobs ? <p className="field-hint">Loading job codes…</p> : birthrightJobLoadError ? <p className="form-error" role="alert">{birthrightJobLoadError}</p> : birthrightJobOptions.length === 0 ? <p className="ci-empty">No active job codes are available. Create a job code first.</p>
              : <RecordPicker id="birthright-job-codes" label="Job codes" hint="Saved as qualifies-for links from the job code (qualified-by from this birthright). Identities with those job codes receive the access this birthright grants. A birthright is typically tied to one job code; more than one is allowed for now." placeholder="Find a job code by name or code" options={birthrightJobOptions} selected={qualifyingJobCodes} onChange={setQualifyingJobCodes} />}
          </section>}
          {isBirthright && <section className="ci-picker raci-section" aria-labelledby="grants-title">
            <div className="ci-picker-heading"><div><h3 id="grants-title">Granted access</h3></div><span className="ci-selection-count">{birthrightRoles.length + birthrightEntitlements.length} grant{(birthrightRoles.length + birthrightEntitlements.length) === 1 ? "" : "s"}</span></div>
            {loadingGrants ? <p className="field-hint">Loading roles and entitlements…</p> : grantsLoadError ? <p className="form-error" role="alert">{grantsLoadError}</p> : <div className="raci-grid">
              <RecordPicker id="birthright-roles" label="Roles" hint="Saved as grants links from this birthright. Identities whose job code qualifies for this birthright receive these roles." placeholder="Find a role by name or ID" options={roleOptions} selected={birthrightRoles} onChange={setBirthrightRoles} />
              <RecordPicker id="birthright-entitlements" label="Entitlements" hint="Saved as grants links from this birthright (same relationship as roles). Use this for entitlements that belong on the birthright directly; role-included entitlements stay on the role form." placeholder="Find an entitlement by name or ID" options={entitlementOptions} selected={birthrightEntitlements} onChange={setBirthrightEntitlements} />
            </div>}
          </section>}
          {isRole && <section className="ci-picker raci-section" aria-labelledby="includes-title">
            <div className="ci-picker-heading"><div><h3 id="includes-title">Included entitlements</h3></div><span className="ci-selection-count">{roleIncludes.length} entitlement{roleIncludes.length === 1 ? "" : "s"}</span></div>
            {loadingIncludes ? <p className="field-hint">Loading entitlements…</p> : includesLoadError ? <p className="form-error" role="alert">{includesLoadError}</p> : entitlementOptions.length === 0 ? <p className="ci-empty">No active entitlements are available. Create an entitlement first.</p>
              : <RecordPicker id="role-includes" label="Entitlements" hint="Saved as includes links from this role (included-by from the entitlement). Identities who hold the role through a birthright or a direct permission also receive these entitlements." placeholder="Find an entitlement by name or ID" options={entitlementOptions} selected={roleIncludes} onChange={setRoleIncludes} />}
          </section>}
          {isAccessItem && <section className="ci-picker raci-section" aria-labelledby="direct-access-title">
            <div className="ci-picker-heading"><div><h3 id="direct-access-title">Direct assignments</h3></div><span className="ci-selection-count">{directAssignees.length} identit{directAssignees.length === 1 ? "y" : "ies"}</span></div>
            {loadingRACI ? <p className="field-hint">Loading identities…</p> : raciLoadError ? <p className="form-error" role="alert">{raciLoadError}</p> : raciIdentities.length === 0 ? <p className="ci-empty">No active identities are available to assign. Create an identity first.</p>
              : <RecordPicker id="direct-assignees" label="Identities" hint={`Saved as permissions links from this ${kindLabel} (permissioned-by from the identity). Use this for exception access outside a birthright; saving creates new links and retires removed ones.`} placeholder="Find an identity by name or ID" options={raciIdentities} selected={directAssignees} onChange={setDirectAssignees} />}
          </section>}
          {isJobCode && <section className="ci-picker raci-section" aria-labelledby="qualifies-title">
            <div className="ci-picker-heading"><div><h3 id="qualifies-title">Birthrights</h3></div><span className="ci-selection-count">{qualifiedBirthrights.length} birthright{qualifiedBirthrights.length === 1 ? "" : "s"}</span></div>
            {loadingJobBirthrights ? <p className="field-hint">Loading birthrights…</p> : jobBirthrightLoadError ? <p className="form-error" role="alert">{jobBirthrightLoadError}</p> : jobBirthrightOptions.length === 0 ? <p className="ci-empty">No active birthrights are available. Create a birthright first.</p>
              : <RecordPicker id="job-code-birthrights" label="Birthrights" hint="Saved as qualifies-for links from this job code. Identities with this job code receive the roles and entitlements those birthrights grant." placeholder="Find a birthright by name or ID" options={jobBirthrightOptions} selected={qualifiedBirthrights} onChange={setQualifiedBirthrights} />}
          </section>}
          </div>
          {hasRACI && <div id="panel-raci" role="tabpanel" aria-labelledby="tab-raci" hidden={tab !== "raci"}><section className="ci-picker raci-section" aria-labelledby="raci-title">
            <div className="ci-picker-heading"><div><h3 id="raci-title">RACI ownership</h3></div><span className="ci-selection-count">{raciRoles.reduce((total, { role }) => total + raci[role].length, 0)} assigned</span></div>
            {isAccessItem && <p className="field-hint">The Accountable identity approves access requests for this {kindLabel}.</p>}
            {loadingRACI ? <p className="field-hint">Loading identities and groups…</p> : raciLoadError ? <p className="form-error" role="alert">{raciLoadError}</p> : raciOptions.length === 0 ? <p className="ci-empty">No active identities or groups are available to assign.</p> : <div className="raci-grid">
              {raciRoles.map(({ role, label, hint }) => <RecordPicker key={role} id={`raci-${role}`} label={label} hint={hint} placeholder={role === "accountable" ? "Find an identity by name or ID" : "Find an identity or group by name or ID"} single={role === "accountable"} options={role === "accountable" ? raciIdentities : raciOptions} selected={raci[role]} onChange={(ids) => setRACI((current) => ({ ...current, [role]: ids }))} />)}
            </div>}
          </section></div>}
          {isIncident && <section className="ci-picker raci-section" aria-labelledby="assignment-title">
            <div className="ci-picker-heading"><div><h3 id="assignment-title">Assignment</h3></div></div>
            {loadingRACI ? <p className="field-hint">Loading identities and groups…</p> : raciLoadError ? <p className="form-error" role="alert">{raciLoadError}</p> : raciOptions.length === 0 ? <p className="ci-empty">No active identities or groups are available to assign.</p>
              : <RecordPicker id="incident-assigned-to" label="Assigned to" hint={target.type === "node" && (target.assignedTo?.length ?? 0) > 0 ? "Filled from this CI's Responsible RACI (assignment groups). Change it if this incident should go elsewhere." : "The identity or group working this incident."} placeholder="Find an identity or group by name or ID" options={raciOptions} selected={assignedTo} onChange={setAssignedTo} />}
          </section>}
          {isGroup && <section className="ci-picker raci-section" aria-labelledby="members-title">
            <div className="ci-picker-heading"><div><h3 id="members-title">Members</h3></div><span className="ci-selection-count">{members.length} {members.length === 1 ? "member" : "members"}</span></div>
            {loadingRACI ? <p className="field-hint">Loading identities…</p> : raciLoadError ? <p className="form-error" role="alert">{raciLoadError}</p> : raciIdentities.length === 0 ? <p className="ci-empty">No active identities are available to add. Create an identity first.</p>
              : <RecordPicker id="group-members" label="Identities" hint="Saved as member links from this group (member-of from the identity). Saving creates new memberships and retires removed ones." placeholder="Find an identity by name or ID" options={raciIdentities} selected={members} onChange={setMembers} />}
          </section>}
          {requiresCIs && <section className="ci-picker" aria-labelledby="ci-picker-title">
            <div className="ci-picker-heading"><div><h3 id="ci-picker-title">Affected configuration items</h3><span className="required-mark">Required</span></div></div>
            {loadingCIs ? <p className="field-hint">Loading active CIs…</p> : ciLoadError ? <p className="form-error" role="alert">{ciLoadError}</p> : availableCIs.length === 0 && !editing ? <p className="ci-empty">No active CIs are available. Create a CI first.</p>
              : <RecordPicker id="affected-cis" label="Configuration items" placeholder="Find a CI by name, ID, or type" options={availableCIs} selected={selectedCIIDs} readOnly={editing} onChange={setSelectedCIIDs} />}
            {editing && <p className="field-hint">CI links are managed separately and remain attached while this record is edited.</p>}
          </section>}
        </> : <>
          <div className="form-field"><label htmlFor="relationship-kind">Relationship type</label><select id="relationship-kind" value={relationshipKind} disabled={editing} onChange={(event) => setRelationshipKind(event.target.value as RelationshipKind)}>{(metadata?.relationships ?? []).map((item) => <option key={item.kind} value={item.kind}>{item.kind} / {item.inverse}</option>)}</select>{relationshipDefinition && <span className="field-hint">Reads "{relationshipDefinition.kind}" from the {titleCase(relationshipDefinition.from)} side and "{relationshipDefinition.inverse}" from the {relationshipToKinds(relationshipDefinition).map(titleCase).join(" / ")} side.</span>}</div>
          {editing ? <div className="field-grid"><div className="form-field"><label>From · {titleCase(relationshipDefinition?.from ?? "")}</label><input value={relationshipRecord?.fromId ?? ""} readOnly /></div><div className="form-field"><label>To · {titleCase(relationshipDefinition?.to ?? "")}</label><input value={relationshipRecord?.toId ?? ""} readOnly /></div></div> : <>
            {loadingEndpoints ? <p className="field-hint">Loading valid endpoints…</p> : endpointLoadError ? <p className="form-error" role="alert">{endpointLoadError}</p> : <div className="field-grid">
              <div className="form-field"><label htmlFor="from-id">From · {ciTypesLabel(relationshipDefinition?.fromCiTypes) ?? titleCase(relationshipDefinition?.from ?? "source")}</label><select id="from-id" value={fromId} required disabled={availableFromNodes.length === 0} onChange={(event) => chooseFromId(event.target.value)}><option value="" disabled>Select source</option>{availableFromNodes.map((node) => <option key={node.id} value={node.id}>{nodeOptionLabel(node)}</option>)}</select>{availableFromNodes.length === 0 && <span className="field-hint">No active source records available.</span>}</div>
              <div className="form-field"><label htmlFor="to-id">To · {ciTypesLabel(toCITypes) ?? (relationshipDefinition ? relationshipToKinds(relationshipDefinition).map(titleCase).join(" / ") : "Destination")}</label><select id="to-id" value={toId} required disabled={toOptions.length === 0} onChange={(event) => setToId(event.target.value)}><option value="" disabled>Select destination</option>{toOptions.map((node) => <option key={node.id} value={node.id}>{nodeOptionLabel(node)}</option>)}</select>{toOptions.length === 0 && <span className="field-hint">No active destination records available.</span>}</div>
            </div>}
          </>}
          <p className="field-hint relationship-hint">Only active records of the relationship’s allowed endpoint types are selectable.</p>
        </>}
        {/* Catalog request forms are fixed: the requester fills in the governed fields only. */}
        {!isRequest && <fieldset className="attribute-editor" hidden={hasRACI && tab !== "details"}>
          <legend>Additional attributes</legend>
          <div className="attribute-list">{attributes.map((attribute, index) => <div className="attribute-row" key={index}>
            <input value={attribute.name} onChange={(event) => updateAttribute(index, "name", event.target.value)} placeholder="Attribute name" aria-label="Attribute name" />
            <select value={attribute.type} onChange={(event) => updateAttribute(index, "type", event.target.value)} aria-label="Attribute value type"><option value="text">Text</option><option value="number">Number</option><option value="boolean">Boolean</option></select>
            <input value={attribute.value} onChange={(event) => updateAttribute(index, "value", event.target.value)} placeholder="Value" aria-label="Attribute value" />
            <button className="attribute-remove" type="button" onClick={() => setAttributes((current) => current.filter((_, item) => item !== index))} aria-label="Remove attribute">Remove</button>
          </div>)}</div>
          <button className="attribute-add" type="button" onClick={() => setAttributes((current) => [...current, { name: "", value: "", type: "text" }])}><Plus size={14} />Add attribute</button>
        </fieldset>}
        {formError && <p className="form-error" role="alert">{formError}</p>}
        <div className="dialog-actions">{(!inline || editing) && <button className="button button-quiet" type="button" onClick={onClose}>{inline ? "Discard changes" : "Cancel"}</button>}
          {isRequest && !requestSubmitted
            ? <><button className="button button-quiet" type="submit" onClick={() => { requestAction.current = "draft"; }}>Save draft</button><button className="button button-primary" type="submit" onClick={() => { requestAction.current = "submit"; }}>{returnComment ? "Resubmit request" : "Submit request"}</button></>
            : isRequest && requestState === "fulfilled" ? <span className="field-hint">This request is complete and retired; its vendor CI is maintained under Configuration items.</span>
            : <button className="button button-primary" type="submit">{editing ? "Save changes" : "Create record"}</button>}</div>
      </form>;

  if (inline) {
    return <section className="editor-dialog editor-inline" aria-labelledby="dialog-title">{form}</section>;
  }
  return <div className="modal-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
    <section className="editor-dialog" role="dialog" aria-modal="true" aria-labelledby="dialog-title">{form}</section>
  </div>;
}

function RetireDialog({ target, onClose, onConfirm }: { target: RetireTarget; onClose: () => void; onConfirm: () => void }) {
  const verb = retireVerb(target.type === "node" ? target.kind : undefined);
  const description = target.type === "relationship"
    ? `Relationship ${target.kind} from ${target.record.fromId} to ${target.record.toId} will be marked retired.`
    : target.kind === "incident"
      ? `Incident ${target.record.id} will be resolved and marked retired, along with its directly attached relationships.`
      : target.kind === "workflow-run"
        ? `Workflow run ${target.record.id} and every task it raised will be marked retired, along with their relationships. The request keeps its own status.`
        : `${titleCase(target.kind)} record ${target.record.id} and its directly attached relationships will be marked retired.`;
  return <div className="modal-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
    <section className="editor-dialog compact-dialog" role="dialog" aria-modal="true" aria-labelledby="retire-title">
      <div className="dialog-heading"><div><p className="eyebrow">LIFECYCLE</p><h2 id="retire-title">{verb} record?</h2></div><button className="close-button" type="button" onClick={onClose} aria-label="Close"><X size={17} /></button></div>
      <p className="confirm-copy">{description}</p>
      <div className="dialog-actions"><button className="button button-quiet" type="button" onClick={onClose}>Cancel</button><button className="button button-danger" type="button" onClick={onConfirm}>{verb}</button></div>
    </section>
  </div>;
}

export default App;