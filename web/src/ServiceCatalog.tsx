import { useEffect, useState } from "react";
import { ClipboardList, Send, X } from "lucide-react";
import { createCatalogRequest, getCatalogFieldOptions, getCatalogItems, getNodePage, getNodes, getTasks } from "./api";
import { clampPage, defaultPageSize, Pager } from "./Pager";
import { AgreementMeter, SLANotice } from "./AgreementMeter";
import RecordPicker, { titleCase } from "./RecordPicker";
import { formatWhen, requestStateOf, requestStatePillClass, taskStatusPillClass } from "./workflows";
import type { SessionUser } from "./session";
import type { CatalogField, CatalogItem, CatalogOption, CatalogSchema, GraphNode, Properties, TaskView } from "./types";

// The Service Catalog: items published by the teams that automate them. Each
// form is generated from the item's input schema; approvals run in Workflow
// Tasks, then the owning team's Temporal workflow fulfils the request and its
// outputs appear on the request.

type FormValues = Record<string, string | boolean>;

function orderedFields(schema: CatalogSchema | undefined): [string, CatalogField][] {
  if (!schema?.properties) return [];
  const keys = schema["x-order"]?.filter((key) => key in schema.properties) ?? [];
  const rest = Object.keys(schema.properties).filter((key) => !keys.includes(key)).sort();
  return [...keys, ...rest].map((key) => [key, schema.properties[key]]);
}

function fieldLabel(schema: CatalogSchema | undefined, key: string) {
  return schema?.properties?.[key]?.title ?? titleCase(key);
}

function initialValues(item: CatalogItem): FormValues {
  return Object.fromEntries(orderedFields(item.inputs).map(([key, field]) => [key, field.type === "boolean" ? field.default === true : field.default === undefined ? "" : String(field.default)]));
}

// Turns form text back into the types the schema declares; blank optional
// fields are left out so the schema's required list decides what is missing.
function inputsFrom(item: CatalogItem, values: FormValues): Properties {
  const inputs: Properties = {};
  for (const [key, field] of orderedFields(item.inputs)) {
    const value = values[key];
    if (field.type === "boolean") { inputs[key] = value === true; continue; }
    const text = typeof value === "string" ? value.trim() : "";
    if (text === "") continue;
    inputs[key] = field.type === "integer" ? Number.parseInt(text, 10) : field.type === "number" ? Number(text) : text;
  }
  return inputs;
}

function parseJSON(value: unknown): Record<string, unknown> {
  if (typeof value !== "string" || !value) return {};
  try {
    const parsed: unknown = JSON.parse(value);
    return parsed && typeof parsed === "object" ? parsed as Record<string, unknown> : {};
  } catch {
    return {};
  }
}

function resultSummary(record: GraphNode) {
  const properties = record.properties ?? {};
  const state = requestStateOf(record);
  if (state === "failed") return String(properties.failureReason ?? "The fulfilment workflow failed.");
  if (state === "denied") return String(properties.denyComment ?? "Denied by an approver.");
  if (state === "in-review") return "Waiting on approval";
  if (state === "in-progress") return "The owning team's workflow is running";
  const outputs = parseJSON(properties.outputs);
  return Object.values(outputs).map(String).join(" · ") || "—";
}

export default function ServiceCatalog({ user, reload, onNotify, openRequest, onOpenRequest }: {
  user: SessionUser;
  reload: number;
  onNotify: (message: string) => void;
  openRequest: GraphNode | null;
  onOpenRequest: (record: GraphNode | null) => void;
}) {
  const [items, setItems] = useState<CatalogItem[]>([]);
  const [itemsError, setItemsError] = useState("");
  const [loadingItems, setLoadingItems] = useState(false);
  const [selected, setSelected] = useState<CatalogItem | null>(null);
  const [requests, setRequests] = useState<GraphNode[]>([]);
  const [requestTotal, setRequestTotal] = useState(0);
  const [requestPage, setRequestPage] = useState(0);
  const [includeRetired, setIncludeRetired] = useState(false);
  const [loadingRequests, setLoadingRequests] = useState(false);
  const [requestsError, setRequestsError] = useState("");
  const [listReload, setListReload] = useState(0);

  useEffect(() => {
    let active = true;
    setLoadingItems(true);
    getCatalogItems().then((result) => {
      if (!active) return;
      setItems(result);
      setItemsError("");
    }).catch((error: unknown) => {
      if (active) setItemsError(error instanceof Error ? error.message : "Could not load the catalog.");
    }).finally(() => {
      if (active) setLoadingItems(false);
    });
    return () => { active = false; };
  }, [reload, user.id]);

  useEffect(() => { setRequestPage(0); }, [includeRetired]);

  // Requests that are still moving refresh on their own, so a request handed
  // to a team's workflow shows its outcome without a manual refresh.
  const moving = requests.some((record) => ["in-review", "in-progress"].includes(requestStateOf(record)));
  useEffect(() => {
    if (!moving) return;
    const timer = window.setInterval(() => setListReload((value) => value + 1), 4000);
    return () => window.clearInterval(timer);
  }, [moving]);

  useEffect(() => {
    let active = true;
    setLoadingRequests(true);
    getNodePage("request", { includeRetired, requestType: "catalog", involvedId: user.superAdmin ? undefined : user.id, limit: defaultPageSize, offset: requestPage * defaultPageSize }).then((result) => {
      if (!active) return;
      setRequests(result.items);
      setRequestTotal(result.total);
      const clamped = clampPage(requestPage, result.total);
      if (clamped !== requestPage) setRequestPage(clamped);
      setRequestsError("");
    }).catch((error: unknown) => {
      if (active) setRequestsError(error instanceof Error ? error.message : "Could not load catalog requests.");
    }).finally(() => {
      if (active) setLoadingRequests(false);
    });
    return () => { active = false; };
  }, [includeRetired, requestPage, reload, listReload, user.id, user.superAdmin]);

  const itemFor = (record: GraphNode) => items.find((item) => item.name === record.properties?.catalogItem);

  return <>
    <section className="records-section request-form-panel" aria-label="Service Catalog">
      {selected ? <CatalogForm key={selected.id} item={selected} user={user} onCancel={() => setSelected(null)} onSubmitted={(created) => {
        onNotify(selected.approvals.length > 0 ? `Request ${created.id} submitted: it is waiting on ${selected.approvals[0].name}.` : `Request ${created.id} submitted and handed to ${selected.owner.name ?? selected.owner.id}'s workflow.`);
        setSelected(null);
        setListReload((value) => value + 1);
      }} />
        : <section className="editor-dialog editor-inline" aria-labelledby="catalog-items-title">
          <div className="dialog-heading"><div><p className="eyebrow">OFFERED TO YOU</p><h2 id="catalog-items-title">Catalog items</h2></div><span className="ci-selection-count">{loadingItems ? "Loading…" : `${items.length} ${items.length === 1 ? "item" : "items"}`}</span></div>
          {itemsError ? <p className="form-error" role="alert">{itemsError}</p>
            : !loadingItems && items.length === 0 ? <p className="ci-empty">Nothing is offered to you yet. Teams publish catalog items from their own repositories; an item appears here once it is visible to a group you belong to.</p>
            : <div className="catalog-items">{items.map((item) => <article className="access-application catalog-item" key={item.id}>
              <div className="access-application-head"><strong>{item.title}</strong><small>{item.id} · v{item.version}</small></div>
              {item.description && <p className="catalog-description">{item.description}</p>}
              <dl className="catalog-facts">
                <div><dt>Owned by</dt><dd>{item.owner.name ?? item.owner.id}</dd></div>
                <div><dt>Approvals</dt><dd>{item.approvals.length === 0 ? "None: starts straight away" : item.approvals.map((approval) => approval.name).join(", then ")}</dd></div>
                {item.slaBusinessDays ? <div><dt>SLA</dt><dd>{item.slaBusinessDays} business day{item.slaBusinessDays === 1 ? "" : "s"}</dd></div> : null}
              </dl>
              <div className="dialog-actions"><button className="button button-primary" type="button" onClick={() => setSelected(item)}><Send size={15} />Request</button></div>
            </article>)}</div>}
        </section>}
    </section>

    <section className="records-section" aria-label="Catalog requests">
      <div className="ci-picker-heading saved-requests-heading"><div><h3>{user.superAdmin ? "Catalog requests" : "My catalog requests"}</h3></div><span className="ci-selection-count">Open a request to see its approvals and what the team's workflow returned. Denied requests are retired; tick Include retired to see them.</span></div>
      <div className="records-toolbar"><span className="secondary-value">{loadingRequests ? "Loading requests…" : `${requestTotal.toLocaleString()} ${requestTotal === 1 ? "request" : "requests"}`}</span><label className="retired-toggle"><input type="checkbox" checked={includeRetired} onChange={(event) => setIncludeRetired(event.target.checked)} /><span>Include retired</span></label></div>
      <div className="table-frame">
        <table>
          <thead><tr><th>Identifier</th><th>Item</th><th>Submitted</th><th>Status</th><th>Result</th><th>Actions</th></tr></thead>
          <tbody>{requests.map((record) => {
            const state = requestStateOf(record);
            const properties = record.properties ?? {};
            const requester = record.relationships?.find((relationship) => relationship.kind === "form-submitted");
            return <tr key={record.id}>
              <td><span className="identifier">{record.id}</span></td>
              <td><div className="primary-value">{String(properties.name ?? properties.catalogItem ?? "—")}</div><div className="secondary-value">{String(properties.catalogItem ?? "")} v{String(properties.catalogItemVersion ?? "?")}{user.superAdmin && requester ? ` · by ${requester.fromName ?? requester.fromId}` : ""}</div></td>
              <td><div className="secondary-value">{formatWhen(typeof properties.submittedAt === "string" ? properties.submittedAt : undefined) || "—"}</div></td>
              <td><span className={`status-pill ${requestStatePillClass(state)}`}>{state}</span>{record.status === "retired" && <div className="secondary-value">retired</div>}</td>
              <td><div className="secondary-value catalog-result">{resultSummary(record)}</div></td>
              <td><div className="row-actions"><button className="action-button" onClick={() => onOpenRequest(record)} title="Open request"><ClipboardList size={14} /><span>Open</span></button></div></td>
            </tr>;
          })}</tbody>
        </table>
        {!loadingRequests && requests.length === 0 && <div className="empty-state"><span className="empty-rule" /><strong>{requestsError || "No catalog requests yet"}</strong><p>{requestsError ? "" : "Requests you raise from the catalog are listed here with their outcome."}</p></div>}
      </div>
      <div className="table-footer"><Pager page={requestPage} total={requestTotal} loading={loadingRequests} noun="record" onPage={setRequestPage} /><span>Sorted by identifier</span></div>
    </section>

    {openRequest && <CatalogRequestDialog key={openRequest.id} record={openRequest} item={itemFor(openRequest)} onClose={() => onOpenRequest(null)} />}
  </>;
}

function CatalogForm({ item, user, onCancel, onSubmitted }: { item: CatalogItem; user: SessionUser; onCancel: () => void; onSubmitted: (created: GraphNode) => void }) {
  const [values, setValues] = useState<FormValues>(() => initialValues(item));
  const [pickerOptions, setPickerOptions] = useState<Record<string, CatalogOption[]>>({});
  const [formError, setFormError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  // The super admin raises requests for itself unless it names someone else.
  const [requestedBy, setRequestedBy] = useState<string[]>([]);
  const [identities, setIdentities] = useState<GraphNode[]>([]);
  const fields = orderedFields(item.inputs);
  const required = new Set(item.inputs.required ?? []);

  useEffect(() => {
    if (!user.superAdmin) return;
    let active = true;
    getNodes("identity", false).then((result) => { if (active) setIdentities(result); }).catch(() => { /* the picker stays empty */ });
    return () => { active = false; };
  }, [user.superAdmin]);

  useEffect(() => {
    let active = true;
    for (const [key, field] of fields) {
      if (!field["x-portal-source"]) continue;
      getCatalogFieldOptions(item.id, key).then((options) => {
        if (active) setPickerOptions((current) => ({ ...current, [key]: options }));
      }).catch(() => {
        if (active) setPickerOptions((current) => ({ ...current, [key]: [] }));
      });
    }
    return () => { active = false; };
    // fields derive from item, which keys this component
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [item.id]);

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const missing = fields.filter(([key, field]) => required.has(key) && field.type !== "boolean" && String(values[key] ?? "").trim() === "").map(([key]) => fieldLabel(item.inputs, key));
    if (missing.length > 0) { setFormError(`Complete ${missing.join(" and ")} before submitting.`); return; }
    setFormError("");
    setSubmitting(true);
    try {
      onSubmitted(await createCatalogRequest({ catalogItemId: item.id, inputs: inputsFrom(item, values), ...(user.superAdmin && requestedBy[0] ? { requestedById: requestedBy[0] } : {}) }));
    } catch (error) {
      setFormError(error instanceof Error ? error.message : "Could not submit the request.");
    } finally {
      setSubmitting(false);
    }
  }

  return <section className="editor-dialog editor-inline" aria-labelledby="catalog-form-title">
    <form onSubmit={(event) => void submit(event)}>
      <div className="dialog-heading">
        <div><p className="eyebrow">NEW REQUEST · {item.name} v{item.version}</p><h2 id="catalog-form-title">{item.title}</h2>{item.description && <p className="page-subtitle">{item.description}</p>}</div>
        <button className="close-button" type="button" onClick={onCancel} aria-label="Back to the catalog" title="Back to the catalog"><X size={17} /></button>
      </div>

      {user.superAdmin && <section className="ci-picker raci-section request-section" aria-labelledby="catalog-requester-title">
        <div className="ci-picker-heading"><div><h3 id="catalog-requester-title">Requested by</h3><span className="ci-selection-count">Optional</span></div></div>
        <RecordPicker id="catalog-requested-by" label="Requester" hint="Leave empty to raise it as the platform super admin, or name the identity it is for. That identity must be offered the item through one of its groups." placeholder="Find an identity by name or ID" single options={identities.filter((identity) => identity.id !== user.id)} selected={requestedBy} onChange={setRequestedBy} />
      </section>}

      <section className="ci-picker raci-section request-section" aria-labelledby="catalog-fields-title">
        <div className="ci-picker-heading"><div><h3 id="catalog-fields-title">Details</h3></div><span className="ci-selection-count">Form published by {item.owner.name ?? item.owner.id}</span></div>
        <div className="field-grid">{fields.map(([key, field]) => {
          const id = `catalog-${key}`;
          const label = <label htmlFor={id}>{field.title ?? titleCase(key)} {required.has(key) ? <span className="required-mark">Required</span> : <span className="ci-selection-count">Optional</span>}</label>;
          const hint = field.description && <span className="field-hint">{field.description}</span>;
          const value = values[key];
          const setValue = (next: string | boolean) => setValues((current) => ({ ...current, [key]: next }));
          if (field.type === "boolean") return <div className="form-field" key={key}><label className="retired-toggle" htmlFor={id}><input id={id} type="checkbox" checked={value === true} onChange={(event) => setValue(event.target.checked)} /><span>{field.title ?? titleCase(key)}</span></label>{hint}</div>;
          if (field["x-portal-source"]) {
            const options = pickerOptions[key];
            const ciType = field["x-portal-source"].replace(/^cmdb:/, "");
            return <div className="form-field" key={key}>{label}
              <select id={id} value={String(value ?? "")} onChange={(event) => setValue(event.target.value)} disabled={!options}><option value="">{options ? (options.length ? `Select ${titleCase(ciType).toLowerCase()}` : `No ${titleCase(ciType).toLowerCase()} CIs available`) : "Loading…"}</option>{options?.map((option) => <option key={option.id} value={option.id}>{option.name ?? option.id} ({option.id})</option>)}</select>{hint}</div>;
          }
          if (field.enum) return <div className="form-field" key={key}>{label}
            <select id={id} value={String(value ?? "")} onChange={(event) => setValue(event.target.value)}><option value="">Select {String(field.title ?? key).toLowerCase()}</option>{field.enum.map((option) => <option key={String(option)} value={String(option)}>{titleCase(String(option))}</option>)}</select>{hint}</div>;
          const long = field.type === "string" && (field.maxLength ?? 0) > 120;
          return <div className={`form-field ${long ? "field-wide" : ""}`} key={key}>{label}
            {long ? <textarea id={id} rows={3} value={String(value ?? "")} maxLength={field.maxLength} onChange={(event) => setValue(event.target.value)} />
              : <input id={id} type={field.type === "string" ? "text" : "number"} autoComplete="off" value={String(value ?? "")} min={field.minimum} max={field.maximum} step={field.type === "integer" ? 1 : undefined} maxLength={field.maxLength} onChange={(event) => setValue(event.target.value)} />}{hint}</div>;
        })}</div>
      </section>

      <section className="ci-picker" aria-labelledby="catalog-route-title">
        <div className="ci-picker-heading"><div><h3 id="catalog-route-title">What happens next</h3></div></div>
        <ol className="task-trail">
          {item.approvals.map((approval, index) => <li className="task-trail-item task-pending" key={index}><div className="task-trail-head"><strong>{index + 1}. {approval.name}</strong><span className="status-pill status-in-review">approval</span></div>{approval.instructions && <div className="secondary-value">{approval.instructions}</div>}</li>)}
          <li className="task-trail-item task-pending"><div className="task-trail-head"><strong>{item.approvals.length + 1}. Automated fulfilment</strong><span className="status-pill status-draft">workflow</span></div><div className="secondary-value">{item.owner.name ?? item.owner.id}'s {item.target.workflowType} workflow runs on Temporal and reports back here.</div></li>
        </ol>
      </section>

      <SLANotice enabled={(item.slaBusinessDays ?? 0) > 0} days={item.slaBusinessDays ?? 0} />
      {formError && <p className="form-error" role="alert">{formError}</p>}
      <div className="dialog-actions">
        <button className="button button-quiet" type="button" onClick={onCancel}>Back to the catalog</button>
        <button className="button button-primary" type="submit" disabled={submitting}>{submitting ? "Submitting…" : "Submit request"}</button>
      </div>
    </form>
  </section>;
}

function CatalogRequestDialog({ record, item, onClose }: { record: GraphNode; item: CatalogItem | undefined; onClose: () => void }) {
  const properties = record.properties ?? {};
  const state = requestStateOf(record);
  const inputs = parseJSON(properties.inputs);
  const outputs = parseJSON(properties.outputs);
  const requester = record.relationships?.find((relationship) => relationship.kind === "form-submitted");
  const references = new Map((record.relationships ?? []).filter((relationship) => relationship.kind === "references").map((relationship) => [relationship.toId, relationship.toName ?? relationship.toId]));
  const [tasks, setTasks] = useState<TaskView[]>([]);
  const [tasksError, setTasksError] = useState("");

  useEffect(() => {
    let active = true;
    getTasks({ requestId: record.id, includeDone: true }).then((result) => {
      if (active) setTasks(result);
    }).catch((error: unknown) => {
      if (active) setTasksError(error instanceof Error ? error.message : "Could not load the request's approvals.");
    });
    return () => { active = false; };
  }, [record.id]);

  const inputKeys = item ? orderedFields(item.inputs).map(([key]) => key).filter((key) => key in inputs) : Object.keys(inputs);
  const outputKeys = item?.outputs ? orderedFields(item.outputs).map(([key]) => key).filter((key) => key in outputs) : Object.keys(outputs);

  return <div className="modal-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
    <section className="editor-dialog task-dialog" role="dialog" aria-modal="true" aria-labelledby="catalog-request-title">
      <form onSubmit={(event) => event.preventDefault()}>
        <div className="dialog-heading">
          <div><p className="eyebrow">{state.toUpperCase()} REQUEST · {record.id}</p><h2 id="catalog-request-title">{String(properties.name ?? "Catalog request")}</h2><p className="page-subtitle">{String(properties.catalogItem ?? "")} v{String(properties.catalogItemVersion ?? "?")}, raised by {requester?.fromName ?? requester?.fromId ?? "an unknown requester"}{typeof properties.submittedAt === "string" ? ` · submitted ${formatWhen(properties.submittedAt)}` : ""}</p></div>
          <button className="close-button" type="button" onClick={onClose} aria-label="Close"><X size={17} /></button>
        </div>

        <section className="ci-picker raci-section request-section" aria-labelledby="catalog-request-inputs-title">
          <div className="ci-picker-heading"><div><h3 id="catalog-request-inputs-title">Request</h3></div><span className={`status-pill ${requestStatePillClass(state)}`}>{state}</span></div>
          <div className="field-grid">{inputKeys.map((key) => {
            const value = inputs[key];
            const text = typeof value === "string" && references.has(value) ? `${references.get(value)} (${value})` : String(value);
            return <div className="form-field" key={key}><label>{fieldLabel(item?.inputs, key)}</label><input value={text} readOnly /></div>;
          })}</div>
        </section>

        {(state === "fulfilled" || state === "failed" || state === "denied" || state === "in-progress") && <section className="ci-picker" aria-labelledby="catalog-request-result-title">
          <div className="ci-picker-heading"><div><h3 id="catalog-request-result-title">Result</h3></div><span className="ci-selection-count">{typeof properties.automationWorkflowId === "string" ? `Temporal workflow ${properties.automationWorkflowId}` : ""}</span></div>
          {state === "in-progress" && <p className="field-hint">Approved{typeof properties.approvedAt === "string" ? ` ${formatWhen(properties.approvedAt)}` : ""}. The owning team's workflow is running; this request updates when it finishes.</p>}
          {state === "failed" && <p className="form-error" role="alert">{String(properties.failureReason ?? "The fulfilment workflow failed.")}</p>}
          {state === "denied" && <p className="field-hint">Denied{typeof properties.deniedAt === "string" ? ` ${formatWhen(properties.deniedAt)}` : ""}: {String(properties.denyComment ?? "no reason given")}</p>}
          {state === "fulfilled" && <div className="field-grid">{outputKeys.map((key) => <div className="form-field" key={key}><label>{fieldLabel(item?.outputs, key)}</label><input value={String(outputs[key])} readOnly /></div>)}</div>}
        </section>}

        <section className="ci-picker" aria-labelledby="catalog-request-tasks-title">
          <div className="ci-picker-heading"><div><h3 id="catalog-request-tasks-title">Approvals</h3></div><span className="ci-selection-count">{tasks[0]?.workflowName ?? ""}</span></div>
          {tasksError ? <p className="form-error" role="alert">{tasksError}</p> : tasks.length === 0 ? <p className="ci-empty">This item needs no approval; it went straight to the team's workflow.</p>
            : <>
              {tasks[0]?.sla && <div className="agreement-row"><AgreementMeter clock={tasks[0].sla} /></div>}
              <ol className="task-trail">{tasks.map((task) => <li className={`task-trail-item task-${task.status}`} key={task.id}>
                <div className="task-trail-head"><strong>{task.step.order ? `${task.step.order}. ` : ""}{task.step.name}</strong><span className={`status-pill ${taskStatusPillClass(task.status)}`}>{task.status}</span></div>
                <div className="secondary-value">{task.assignees.map((assignee) => assignee.name ?? assignee.id).join(", ") || "Unassigned"} · {task.id}</div>
                {task.actions.map((action, index) => <div className="task-trail-action" key={index}><span>{titleCase(action.action)} by {action.actorName ?? action.actorId} · {formatWhen(action.actedAt)}</span>{action.comment && <p>{action.comment}</p>}</div>)}
              </li>)}</ol>
            </>}
        </section>

        <div className="dialog-actions"><button className="button button-quiet" type="button" onClick={onClose}>Close</button></div>
      </form>
    </section>
  </div>;
}
