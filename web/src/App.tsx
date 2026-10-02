import { lazy, Suspense, useEffect, useState } from "react";
import {
  Archive,
  BriefcaseBusiness,
  KeyRound,
  Link2,
  Network,
  PackageOpen,
  Pencil,
  Plus,
  RefreshCw,
  Search,
  ShieldCheck,
  Users,
  X,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import {
  createNode,
  createRelationship,
  getMetadata,
  getNodes,
  getRelationships,
  retireNode,
  retireRelationship,
  updateNode,
  updateRelationship,
} from "./api";
import type {
  GraphNode,
  GraphRelationship,
  Metadata,
  NodeKind,
  Properties,
  RecordEditorTarget,
  RelationshipKind,
  Resource,
  RetireTarget,
} from "./types";

const GraphMap = lazy(() => import("./GraphMap"));

const resourceInfo: Record<Resource, { title: string; singular: string; subtitle: string; kicker: string }> = {
  identity: { title: "Identities", singular: "identity", subtitle: "People and their assigned job codes.", kicker: "IDENTITY DIRECTORY" },
  "job-code": { title: "Job codes", singular: "job code", subtitle: "Positions that qualify for access packages.", kicker: "WORKFORCE CLASSIFICATION" },
  birthright: { title: "Birthrights", singular: "birthright", subtitle: "Automatic access bundles derived from workforce attributes.", kicker: "AUTOMATIC ACCESS" },
  role: { title: "Roles", singular: "role", subtitle: "Permission groupings used to grant system access.", kicker: "ACCESS GROUPS" },
  entitlement: { title: "Entitlements", singular: "entitlement", subtitle: "Concrete permissions within connected systems.", kicker: "SYSTEM PERMISSIONS" },
  relationships: { title: "Relationships", singular: "relationship", subtitle: "Directed links that describe how access is derived.", kicker: "GRAPH CONNECTIONS" },
  map: { title: "Graph map", singular: "record", subtitle: "Explore configuration items and their connected records.", kicker: "RELATIONSHIP MAP" },
  ci: { title: "Configuration items", singular: "configuration item", subtitle: "Managed assets with governed CI types.", kicker: "CONFIGURATION ITEMS" },
  incident: { title: "Incidents", singular: "incident", subtitle: "Operational incidents associated with configuration items.", kicker: "INCIDENT MANAGEMENT" },
  change: { title: "Changes", singular: "change", subtitle: "Controlled changes that affect configuration items.", kicker: "CHANGE MANAGEMENT" },
  event: { title: "Events", singular: "event", subtitle: "Observed events associated with configuration items.", kicker: "EVENT STREAM" },
};

const nodeFields: Record<NodeKind, { key: string; label: string; options?: string[] }[]> = {
  identity: [
    { key: "name", label: "Display name" },
    { key: "department", label: "Department" },
    { key: "location", label: "Location" },
  ],
  "job-code": [{ key: "name", label: "Job title" }],
  birthright: [{ key: "name", label: "Birthright name" }],
  role: [{ key: "name", label: "Role name" }],
  entitlement: [{ key: "name", label: "Entitlement name" }],
  ci: [{ key: "ciType", label: "CI type", options: ["server", "printer", "data-connector", "application"] }],
  incident: [{ key: "name", label: "Summary" }],
  change: [{ key: "name", label: "Summary" }],
  event: [{ key: "name", label: "Summary" }],
};

const navigation: { kind: Resource; title: string; Icon: LucideIcon }[] = [
  { kind: "identity", title: "Identities", Icon: Users },
  { kind: "job-code", title: "Job codes", Icon: BriefcaseBusiness },
  { kind: "birthright", title: "Birthrights", Icon: PackageOpen },
  { kind: "role", title: "Roles", Icon: ShieldCheck },
  { kind: "entitlement", title: "Entitlements", Icon: KeyRound },
  { kind: "ci", title: "Configuration items", Icon: PackageOpen },
  { kind: "incident", title: "Incidents", Icon: BriefcaseBusiness },
  { kind: "change", title: "Changes", Icon: RefreshCw },
  { kind: "event", title: "Events", Icon: Link2 },
  { kind: "relationships", title: "Relationships", Icon: Link2 },
  { kind: "map", title: "Map", Icon: Network },
];

function titleCase(value: string) {
  return value.replaceAll("-", " ").replace(/\b\w/g, (character) => character.toUpperCase());
}

function scalarProperties(properties: Properties | undefined = {}) {
  return Object.entries(properties).map(([key, value]) => `${key}: ${value}`).join(" · ") || "No additional properties";
}

function nodeOptionLabel(node: GraphNode) {
  const properties = node.properties ?? {};
  const name = properties.name || properties.title || properties.department || node.id;
  const kind = node.kind === "ci" && typeof properties.ciType === "string" ? `CI · ${titleCase(properties.ciType)}` : titleCase(node.kind);
  return `${name} · ${node.id} · ${kind}`;
}

function App() {
  const [resource, setResource] = useState<Resource>("identity");
  const [relationshipKind, setRelationshipKind] = useState<RelationshipKind>("has-job-code");
  const [metadata, setMetadata] = useState<Metadata | null>(null);
  const [records, setRecords] = useState<(GraphNode | GraphRelationship)[]>([]);
  const [includeRetired, setIncludeRetired] = useState(false);
  const [search, setSearch] = useState("");
  const [loading, setLoading] = useState(true);
  const [online, setOnline] = useState(false);
  const [error, setError] = useState("");
  const [editor, setEditor] = useState<RecordEditorTarget | null>(null);
  const [retiring, setRetiring] = useState<RetireTarget | null>(null);
  const [toast, setToast] = useState("");
  const [reload, setReload] = useState(0);

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
    let active = true;
    if (resource === "map") {
      setLoading(false);
      return () => { active = false; };
    }
    setLoading(true);
    const query = resource === "relationships"
      ? getRelationships(relationshipKind, includeRetired)
      : getNodes(resource, includeRetired);
    query.then((result) => {
      if (!active) return;
      setRecords(result);
      setOnline(true);
      setError("");
    }).catch((requestError: unknown) => {
      if (!active) return;
      setRecords([]);
      setOnline(false);
      setError(requestError instanceof Error ? requestError.message : "Request failed.");
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [resource, relationshipKind, includeRetired, reload]);

  useEffect(() => {
    if (!toast) return;
    const timeout = window.setTimeout(() => setToast(""), 3000);
    return () => window.clearTimeout(timeout);
  }, [toast]);

  const currentInfo = resourceInfo[resource];
  const filteredRecords = records.filter((record) => JSON.stringify(record).toLowerCase().includes(search.toLowerCase().trim()));
  const retiredCount = records.filter((record) => record.status === "retired").length;
  const activeCount = records.length - retiredCount;
  const relationshipCount = resource === "relationships"
    ? records.length
    : (records as GraphNode[]).reduce((total, node) => total + (node.relationships?.length ?? 0), 0);
  const editorRecordKey = editor?.record
    ? ("id" in editor.record ? editor.record.id : `${editor.record.fromId}-${editor.record.toId}`)
    : "new";

  async function saveRecord(target: RecordEditorTarget, fromId: string, toId: string, properties: Properties, ciIDs: string[]) {
    try {
      if (target.type === "node") {
        if (target.record) await updateNode(target.kind, target.record.id, properties);
        else {
          await createNode(target.kind, { properties, ...(ciIDs.length > 0 ? { ciIds: ciIDs } : {}) });
        }
      } else {
        const kind = target.record?.kind ?? target.kind;
        if (!kind) throw new Error("Choose a relationship type.");
        if (target.record) await updateRelationship(kind, target.record.fromId, target.record.toId, properties);
        else await createRelationship(kind, { fromId, toId, properties });
      }
      setEditor(null);
      setToast(target.record ? "Changes saved." : "Record created.");
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
        <a className="brand" href="/" aria-label="Service Map home">
          <span className="brand-mark">S</span>
          <span><strong>service map</strong><small>CONFIGURATION DB</small></span>
        </a>
        <div className="rail-section-label">INVENTORY</div>
        <nav className="resource-nav" aria-label="Graph resources">
          {navigation.map(({ kind, title, Icon }) => (
            <button className={`nav-item ${resource === kind ? "active" : ""}`} key={kind} onClick={() => { setResource(kind); setSearch(""); }}>
              <Icon aria-hidden="true" size={17} strokeWidth={1.7} />{title}
            </button>
          ))}
        </nav>
        <div className="rail-footer">
          <span className={`connection-dot ${online ? "online" : error ? "offline" : ""}`} />
          <div><strong>{online ? "Database connected" : error ? "Database unavailable" : "Checking database"}</strong><small>Neo4j connection</small></div>
        </div>
      </aside>

      <div className="content-shell">
        <header className="topbar">
          <span>ACCESS GOVERNANCE <span className="crumb-separator">/</span> GRAPH RECORDS</span>
          <span className="environment-tag">LOCAL WORKSPACE</span>
        </header>
        <main>
          <section className="page-heading">
            <div>
              <p className="eyebrow">{currentInfo.kicker}</p>
              <h1>{currentInfo.title}</h1>
              <p className="page-subtitle">{currentInfo.subtitle}</p>
            </div>
            <div className="heading-actions">
              <button className="button button-quiet icon-button" onClick={() => setReload((value) => value + 1)} title="Refresh records" aria-label="Refresh records">
                <RefreshCw size={16} />Refresh
              </button>
              {resource !== "map" && <button className="button button-primary" onClick={() => setEditor(resource === "relationships"
                ? { type: "relationship", kind: relationshipKind }
                : { type: "node", kind: resource })}>
                <Plus size={16} />Add {currentInfo.singular}
              </button>}
            </div>
          </section>

          {resource !== "map" && <section className="metric-strip" aria-label="Current view totals">
            <Metric label="RECORDS" value={loading ? "—" : records.length} />
            <Metric label="ACTIVE" value={loading ? "—" : activeCount} />
            <Metric label="RETIRED" value={loading ? "—" : retiredCount} />
            <Metric label={resource === "relationships" ? "LINK RECORDS" : "DIRECT LINKS"} value={loading ? "—" : relationshipCount} />
          </section>}

          {resource === "map" ? <Suspense fallback={<div className="map-canvas map-overlay">Loading graph tools…</div>}><GraphMap metadata={metadata} includeRetired={includeRetired} reload={reload} onToggleIncludeRetired={() => setIncludeRetired((value) => !value)} /></Suspense> : <section className="records-section" aria-label="Records">
            <div className="records-toolbar">
              <label className="search-box">
                <Search aria-hidden="true" size={17} />
                <input value={search} onChange={(event) => setSearch(event.target.value)} type="search" placeholder="Filter this list" autoComplete="off" />
              </label>
              {resource === "relationships" && (
                <label className="relationship-filter">
                  <span>Relationship</span>
                  <select value={relationshipKind} onChange={(event) => setRelationshipKind(event.target.value as RelationshipKind)}>
                    {(metadata?.relationships ?? []).map((item) => <option key={item.kind} value={item.kind}>{item.kind}</option>)}
                  </select>
                </label>
              )}
              <label className="retired-toggle"><input type="checkbox" checked={includeRetired} onChange={(event) => setIncludeRetired(event.target.checked)} /><span>Include retired</span></label>
            </div>
            <div className="table-frame">
              <table>
                <thead><tr>{resource === "relationships"
                  ? <><th>Relationship</th><th>From</th><th>To</th><th>Properties</th><th>Status</th><th>Actions</th></>
                  : <><th>Identifier</th><th>Name</th><th>Properties</th><th>Direct relationships</th><th>Status</th><th>Actions</th></>}
                </tr></thead>
                <tbody>
                  {resource === "relationships"
                    ? (filteredRecords as GraphRelationship[]).map((record, index) => <RelationshipRow key={`${record.kind}-${record.fromId ?? index}-${record.toId ?? index}`} record={record} onEdit={() => setEditor({ type: "relationship", record })} onRetire={() => setRetiring({ type: "relationship", kind: record.kind, record })} />)
                    : (filteredRecords as GraphNode[]).map((record, index) => <NodeRow key={`${record.kind}-${record.id ?? index}`} record={record} onEdit={() => setEditor({ type: "node", kind: record.kind, record })} onRetire={() => setRetiring({ type: "node", kind: record.kind, record })} />)}
                </tbody>
              </table>
              {!loading && filteredRecords.length === 0 && <div className="empty-state"><span className="empty-rule" /><strong>{error || (search ? "No matching records" : `No ${currentInfo.title.toLowerCase()} found`)}</strong><p>{search ? "Try another filter." : "Create a record to add it to this inventory."}</p></div>}
            </div>
            <div className="table-footer"><span>{loading ? "Loading records…" : `${filteredRecords.length} ${filteredRecords.length === 1 ? "record" : "records"}`}</span><span>Sorted by identifier</span></div>
          </section>}
          {toast && <div className="toast" role="status" aria-live="polite">{toast}</div>}
        </main>
        <footer className="page-footer"><span>SERVICE MAP CMDB</span><span>GRAPH RECORDS · API V1</span></footer>
      </div>

      {editor && <RecordDialog key={`${editor.type}-${editor.kind ?? editor.record?.kind}-${editorRecordKey}`} target={editor} metadata={metadata} onClose={() => setEditor(null)} onSave={saveRecord} />}
      {retiring && <RetireDialog target={retiring} onClose={() => setRetiring(null)} onConfirm={confirmRetirement} />}
    </div>
  );
}

function Metric({ label, value }: { label: string; value: string | number }) {
  return <div className="metric"><span className="metric-label">{label}</span><strong>{value}</strong></div>;
}

function StatusPill({ status }: { status: "active" | "retired" }) {
  return <span className={`status-pill status-${status}`}>{status}</span>;
}

function RowActions({ status, onEdit, onRetire }: { status: "active" | "retired"; onEdit: () => void; onRetire: () => void }) {
  return <div className="row-actions">
    <button className="action-button" onClick={onEdit} title="Edit record"><Pencil size={14} /><span>Edit</span></button>
    {status !== "retired" && <button className="action-button retire" onClick={onRetire} title="Retire record"><Archive size={14} /><span>Retire</span></button>}
  </div>;
}

function NodeRow({ record, onEdit, onRetire }: { record: GraphNode; onEdit: () => void; onRetire: () => void }) {
  const properties = record.properties ?? {};
  const displayName = properties.name || properties.title || properties.department || (record.kind === "ci" && typeof properties.ciType === "string" ? titleCase(properties.ciType) : "—");
  const recordType = record.kind === "ci" && typeof properties.ciType === "string" ? `CI · ${titleCase(properties.ciType)}` : titleCase(record.kind);
  const relationships = record.relationships ?? [];
  return <tr>
    <td><span className="identifier">{record.id}</span></td>
    <td><div className="primary-value">{displayName}</div><div className="secondary-value">{recordType}</div></td>
    <td><div className="property-list">{scalarProperties(properties)}</div></td>
    <td><div className="relationship-list">{relationships.length === 0
      ? <span className="secondary-value">No direct links</span>
      : relationships.map((relationship, index) => {
        const outgoing = relationship.fromId === record.id;
        return <div className="relationship-line" key={`${relationship.kind}-${index}`}><strong>{relationship.kind}</strong> {outgoing ? "→" : "←"} {outgoing ? relationship.toId : relationship.fromId}{relationship.status === "retired" ? " · retired" : ""}</div>;
      })}</div></td>
    <td><StatusPill status={record.status} /></td>
    <td><RowActions status={record.status} onEdit={onEdit} onRetire={onRetire} /></td>
  </tr>;
}

function RelationshipRow({ record, onEdit, onRetire }: { record: GraphRelationship; onEdit: () => void; onRetire: () => void }) {
  return <tr>
    <td><span className="identifier">{record.kind}</span></td>
    <td><span className="identifier">{record.fromId}</span></td>
    <td><span className="identifier">{record.toId}</span></td>
    <td><div className="property-list">{scalarProperties(record.properties)}</div></td>
    <td><StatusPill status={record.status} /></td>
    <td><RowActions status={record.status} onEdit={onEdit} onRetire={onRetire} /></td>
  </tr>;
}

function RecordDialog({ target, metadata, onClose, onSave }: {
  target: RecordEditorTarget;
  metadata: Metadata | null;
  onClose: () => void;
  onSave: (target: RecordEditorTarget, fromId: string, toId: string, properties: Properties, ciIDs: string[]) => Promise<void>;
}) {
  const nodeRecord = target.type === "node" ? target.record : undefined;
  const relationshipRecord = target.type === "relationship" ? target.record : undefined;
  const record = target.record;
  const initialProperties = target.record?.properties ?? {};
  const knownFields = target.type === "node" ? nodeFields[target.kind] : [];
  const knownNames = new Set(knownFields.map((field) => field.key));
  const requiresCIs = target.type === "node" && (target.kind === "incident" || target.kind === "change");
  const generatedID = target.type === "node" && !record;
  const ciRelationshipKind: RelationshipKind = target.type === "node" && target.kind === "change" ? "changes" : "affects";
  const [fromId, setFromId] = useState(relationshipRecord?.fromId ?? "");
  const [toId, setToId] = useState(relationshipRecord?.toId ?? "");
  const [relationshipKind, setRelationshipKind] = useState<RelationshipKind>(relationshipRecord?.kind ?? (target.type === "relationship" ? target.kind : undefined) ?? "has-job-code");
  const [properties, setProperties] = useState<Properties>(Object.fromEntries(Object.entries(initialProperties).filter(([key]) => knownNames.has(key))));
  const [attributes, setAttributes] = useState(() => Object.entries(initialProperties).filter(([key]) => !knownNames.has(key)).map(([name, value]) => ({ name, value: String(value), type: typeof value === "boolean" ? "boolean" : typeof value === "number" ? "number" : "text" })));
  const [formError, setFormError] = useState("");
  const [availableCIs, setAvailableCIs] = useState<GraphNode[]>([]);
  const [loadingCIs, setLoadingCIs] = useState(false);
  const [ciLoadError, setCILoadError] = useState("");
  const [availableFromNodes, setAvailableFromNodes] = useState<GraphNode[]>([]);
  const [availableToNodes, setAvailableToNodes] = useState<GraphNode[]>([]);
  const [loadingEndpoints, setLoadingEndpoints] = useState(false);
  const [endpointLoadError, setEndpointLoadError] = useState("");
  const [selectedCIIDs, setSelectedCIIDs] = useState<string[]>(() => nodeRecord?.relationships?.filter((relationship) => relationship.kind === ciRelationshipKind).map((relationship) => relationship.toId) ?? []);
  const editing = Boolean(record);
  const kindLabel = target.type === "node" ? resourceInfo[target.kind].singular : "relationship";
  const relationshipDefinition = target.type === "relationship"
    ? metadata?.relationships.find((definition) => definition.kind === relationshipKind)
    : undefined;

  useEffect(() => {
    if (!requiresCIs) return;
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
  }, [requiresCIs]);

  useEffect(() => {
    if (target.type !== "relationship" || editing || !relationshipDefinition) return;
    let active = true;
    setLoadingEndpoints(true);
    setEndpointLoadError("");
    setFromId("");
    setToId("");
    Promise.all([
      getNodes(relationshipDefinition.from, false),
      getNodes(relationshipDefinition.to, false),
    ]).then(([fromNodes, toNodes]) => {
      if (!active) return;
      setAvailableFromNodes(fromNodes);
      setAvailableToNodes(toNodes);
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
    if (target.type === "node" && target.kind === "ci" && typeof values.ciType !== "string") {
      values.ciType = metadata?.ciTypes[0] ?? "server";
    }
    if (requiresCIs && selectedCIIDs.length === 0) {
      setFormError("Select at least one active CI before creating this record.");
      return;
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
    setFormError("");
    void onSave(target.type === "relationship" && !record ? { ...target, kind: relationshipKind } : target, fromId.trim(), toId.trim(), values, selectedCIIDs);
  }

  function updateAttribute(index: number, field: "name" | "value" | "type", value: string) {
    setAttributes((current) => current.map((attribute, item) => item === index ? { ...attribute, [field]: value } : attribute));
  }

  return <div className="modal-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
    <section className="editor-dialog" role="dialog" aria-modal="true" aria-labelledby="dialog-title">
      <form onSubmit={submit}>
        <div className="dialog-heading">
          <div><p className="eyebrow">{editing ? "UPDATE RECORD" : "NEW RECORD"}</p><h2 id="dialog-title">{editing ? "Edit" : "Add"} {target.type === "relationship" ? "relationship" : kindLabel}</h2></div>
          <button className="close-button" type="button" onClick={onClose} aria-label="Close"><X size={17} /></button>
        </div>
        {target.type === "node" ? <>
          {generatedID
            ? <p className="system-generated-id">The {titleCase(target.kind)} ID will be assigned automatically when created.</p>
            : <div className="form-field"><label htmlFor="record-id">{target.kind === "job-code" ? "Code" : "Identifier"}</label><input id="record-id" value={nodeRecord?.id ?? ""} readOnly /><span className="field-hint">Stable identifiers cannot be changed after creation.</span></div>}
          <div className="field-grid">{knownFields.map((field) => <div className="form-field" key={field.key}><label htmlFor={`field-${field.key}`}>{field.label}</label>{field.options
            ? <select id={`field-${field.key}`} value={String(properties[field.key] ?? (target.kind === "ci" ? metadata?.ciTypes[0] : undefined) ?? field.options[0])} onChange={(event) => setProperties((current) => ({ ...current, [field.key]: event.target.value }))}>{(target.kind === "ci" ? metadata?.ciTypes ?? field.options : field.options).map((option) => <option key={option} value={option}>{titleCase(option)}</option>)}</select>
            : <input id={`field-${field.key}`} value={String(properties[field.key] ?? "")} onChange={(event) => setProperties((current) => ({ ...current, [field.key]: event.target.value }))} />}</div>)}</div>
          {requiresCIs && <section className="ci-picker" aria-labelledby="ci-picker-title">
            <div className="ci-picker-heading"><div><h3 id="ci-picker-title">Affected configuration items</h3><span className="required-mark">Required</span></div><span className="ci-selection-count">{selectedCIIDs.length} selected</span></div>
            {loadingCIs ? <p className="field-hint">Loading active CIs…</p> : ciLoadError ? <p className="form-error" role="alert">{ciLoadError}</p> : availableCIs.length === 0 ? <p className="ci-empty">No active CIs are available. Create a CI first.</p> : <div className="ci-options">{availableCIs.map((ci) => {
              const checked = selectedCIIDs.includes(ci.id);
              const ciName = ci.properties?.name || ci.properties?.title || ci.id;
              return <label className={`ci-option ${checked ? "selected" : ""}`} key={ci.id}>
                <input type="checkbox" checked={checked} disabled={editing} onChange={(event) => setSelectedCIIDs((current) => event.target.checked ? [...current, ci.id] : current.filter((value) => value !== ci.id))} />
                <span><strong>{ciName}</strong><small>{ci.id} · {titleCase(String(ci.properties?.ciType ?? "ci"))}</small></span>
              </label>;
            })}</div>}
            {editing && <p className="field-hint">CI links are managed separately and remain attached while this record is edited.</p>}
          </section>}
        </> : <>
          <div className="form-field"><label htmlFor="relationship-kind">Relationship type</label><select id="relationship-kind" value={relationshipKind} disabled={editing} onChange={(event) => setRelationshipKind(event.target.value as RelationshipKind)}>{(metadata?.relationships ?? []).map((item) => <option key={item.kind} value={item.kind}>{item.kind}</option>)}</select></div>
          {editing ? <div className="field-grid"><div className="form-field"><label>From · {titleCase(relationshipDefinition?.from ?? "")}</label><input value={relationshipRecord?.fromId ?? ""} readOnly /></div><div className="form-field"><label>To · {titleCase(relationshipDefinition?.to ?? "")}</label><input value={relationshipRecord?.toId ?? ""} readOnly /></div></div> : <>
            {loadingEndpoints ? <p className="field-hint">Loading valid endpoints…</p> : endpointLoadError ? <p className="form-error" role="alert">{endpointLoadError}</p> : <div className="field-grid">
              <div className="form-field"><label htmlFor="from-id">From · {titleCase(relationshipDefinition?.from ?? "source")}</label><select id="from-id" value={fromId} required disabled={availableFromNodes.length === 0} onChange={(event) => setFromId(event.target.value)}><option value="" disabled>Select source</option>{availableFromNodes.map((node) => <option key={node.id} value={node.id}>{nodeOptionLabel(node)}</option>)}</select>{availableFromNodes.length === 0 && <span className="field-hint">No active source records available.</span>}</div>
              <div className="form-field"><label htmlFor="to-id">To · {titleCase(relationshipDefinition?.to ?? "destination")}</label><select id="to-id" value={toId} required disabled={availableToNodes.length === 0} onChange={(event) => setToId(event.target.value)}><option value="" disabled>Select destination</option>{availableToNodes.map((node) => <option key={node.id} value={node.id}>{nodeOptionLabel(node)}</option>)}</select>{availableToNodes.length === 0 && <span className="field-hint">No active destination records available.</span>}</div>
            </div>}
          </>}
          <p className="field-hint relationship-hint">Only active records of the relationship’s allowed endpoint types are selectable.</p>
        </>}
        <fieldset className="attribute-editor">
          <legend>Additional attributes</legend>
          <div className="attribute-list">{attributes.map((attribute, index) => <div className="attribute-row" key={index}>
            <input value={attribute.name} onChange={(event) => updateAttribute(index, "name", event.target.value)} placeholder="Attribute name" aria-label="Attribute name" />
            <select value={attribute.type} onChange={(event) => updateAttribute(index, "type", event.target.value)} aria-label="Attribute value type"><option value="text">Text</option><option value="number">Number</option><option value="boolean">Boolean</option></select>
            <input value={attribute.value} onChange={(event) => updateAttribute(index, "value", event.target.value)} placeholder="Value" aria-label="Attribute value" />
            <button className="attribute-remove" type="button" onClick={() => setAttributes((current) => current.filter((_, item) => item !== index))} aria-label="Remove attribute">Remove</button>
          </div>)}</div>
          <button className="attribute-add" type="button" onClick={() => setAttributes((current) => [...current, { name: "", value: "", type: "text" }])}><Plus size={14} />Add attribute</button>
        </fieldset>
        {formError && <p className="form-error" role="alert">{formError}</p>}
        <div className="dialog-actions"><button className="button button-quiet" type="button" onClick={onClose}>Cancel</button><button className="button button-primary" type="submit">{editing ? "Save changes" : "Create record"}</button></div>
      </form>
    </section>
  </div>;
}

function RetireDialog({ target, onClose, onConfirm }: { target: RetireTarget; onClose: () => void; onConfirm: () => void }) {
  const description = target.type === "relationship"
    ? `Relationship ${target.kind} from ${target.record.fromId} to ${target.record.toId} will be marked retired.`
    : `${titleCase(target.kind)} record ${target.record.id} and its directly attached relationships will be marked retired.`;
  return <div className="modal-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
    <section className="editor-dialog compact-dialog" role="dialog" aria-modal="true" aria-labelledby="retire-title">
      <div className="dialog-heading"><div><p className="eyebrow">LIFECYCLE</p><h2 id="retire-title">Retire record?</h2></div><button className="close-button" type="button" onClick={onClose} aria-label="Close"><X size={17} /></button></div>
      <p className="confirm-copy">{description}</p>
      <div className="dialog-actions"><button className="button button-quiet" type="button" onClick={onClose}>Cancel</button><button className="button button-danger" type="button" onClick={onConfirm}>Retire</button></div>
    </section>
  </div>;
}

export default App;