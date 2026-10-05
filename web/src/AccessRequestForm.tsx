import { useEffect, useState } from "react";
import { ClipboardList, X } from "lucide-react";
import { createAccessRequest, getAccessOptions, getFormSLA, getNodePage, getNodes, getTasks, nodeDisplayName } from "./api";
import { clampPage, defaultPageSize, Pager } from "./Pager";
import { AgreementMeter, SLANotice } from "./AgreementMeter";
import RecordPicker, { titleCase } from "./RecordPicker";
import { formatWhen, itemDecisionLabel, itemDecisionPillClass, requestStateOf, requestStatePillClass, taskStatusPillClass } from "./workflows";
import type { SessionUser } from "./session";
import type { AccessItemKind, AccessOption, AccessOptions, GraphNode, ItemDecision, Metadata, TaskView } from "./types";

// One role or entitlement ticked on the form, with its optional note.
type Pick = { id: string; kind: AccessItemKind; note: string };
const pickKey = (item: { id: string; kind: AccessItemKind }) => `${item.kind}:${item.id}`;

const heldVia: Record<AccessOptions["held"][number]["via"], string> = {
  birthright: "through birthright",
  role: "through role",
  direct: "granted directly",
  pending: "already requested",
};

export default function AccessRequestForm({ user, metadata, reload, onNotify, openRequest, onOpenRequest }: {
  user: SessionUser;
  metadata: Metadata | null;
  reload: number;
  onNotify: (message: string) => void;
  openRequest: GraphNode | null; // a request chosen from the list, the map, or a task
  onOpenRequest: (record: GraphNode | null) => void;
}) {
  const [identities, setIdentities] = useState<GraphNode[]>([]);
  const [identitiesError, setIdentitiesError] = useState("");
  // The signed-in identity raises the request by default; the super admin has no identity, so they pick one.
  const [requestedBy, setRequestedBy] = useState<string[]>(() => user.superAdmin ? [] : [user.id]);
  const [requestFor, setRequestFor] = useState<string[]>([]);
  const [options, setOptions] = useState<AccessOptions | null>(null);
  const [loadingOptions, setLoadingOptions] = useState(false);
  const [optionsError, setOptionsError] = useState("");
  const [picks, setPicks] = useState<Pick[]>([]);
  const [formError, setFormError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [slaEnabled, setSlaEnabled] = useState(false);
  const [slaDays, setSlaDays] = useState(5);
  const [optionsVersion, setOptionsVersion] = useState(0);
  // The list beneath the form: every access request, retired ones on demand.
  const [requests, setRequests] = useState<GraphNode[]>([]);
  const [requestTotal, setRequestTotal] = useState(0);
  const [requestPage, setRequestPage] = useState(0);
  const [includeRetired, setIncludeRetired] = useState(false);
  const [loadingRequests, setLoadingRequests] = useState(false);
  const [requestsError, setRequestsError] = useState("");
  const [listReload, setListReload] = useState(0);
  const subjectId = requestFor[0] ?? "";
  const subject = identities.find((node) => node.id === subjectId);
  const itemKinds = metadata?.accessItemKinds ?? ["role", "entitlement"];

  useEffect(() => {
    let active = true;
    getNodes("identity", false).then((items) => {
      if (active) setIdentities(items);
    }).catch((error: unknown) => {
      if (active) setIdentitiesError(error instanceof Error ? error.message : "Could not load identities.");
    });
    return () => { active = false; };
  }, [reload]);

  useEffect(() => {
    let active = true;
    getFormSLA("access").then((sla) => {
      if (!active) return;
      setSlaEnabled(Boolean(sla.enabled));
      setSlaDays(sla.businessDays || 0);
    }).catch(() => { /* leave SLA off when the form has none */ });
    return () => { active = false; };
  }, [reload]);

  useEffect(() => {
    setPicks([]);
    setFormError("");
    if (!subjectId) { setOptions(null); return; }
    let active = true;
    setLoadingOptions(true);
    getAccessOptions(subjectId).then((result) => {
      if (!active) return;
      setOptions(result);
      setOptionsError("");
    }).catch((error: unknown) => {
      if (!active) return;
      setOptions(null);
      setOptionsError(error instanceof Error ? error.message : "Could not load what this identity may request.");
    }).finally(() => {
      if (active) setLoadingOptions(false);
    });
    return () => { active = false; };
  }, [subjectId, reload, optionsVersion]);

  useEffect(() => { setRequestPage(0); }, [includeRetired]);

  useEffect(() => {
    let active = true;
    setLoadingRequests(true);
    getNodePage("request", { includeRetired, requestType: "access", limit: defaultPageSize, offset: requestPage * defaultPageSize }).then((result) => {
      if (!active) return;
      setRequests(result.items);
      setRequestTotal(result.total);
      const clamped = clampPage(requestPage, result.total);
      if (clamped !== requestPage) setRequestPage(clamped);
      setRequestsError("");
    }).catch((error: unknown) => {
      if (active) setRequestsError(error instanceof Error ? error.message : "Could not load access requests.");
    }).finally(() => {
      if (active) setLoadingRequests(false);
    });
    return () => { active = false; };
  }, [includeRetired, requestPage, reload, listReload]);

  function toggle(option: AccessOption) {
    setPicks((current) => current.some((pick) => pickKey(pick) === pickKey(option)) ? current.filter((pick) => pickKey(pick) !== pickKey(option)) : [...current, { id: option.id, kind: option.kind, note: "" }]);
  }
  function setNote(option: AccessOption, note: string) {
    setPicks((current) => current.map((pick) => pickKey(pick) === pickKey(option) ? { ...pick, note } : pick));
  }

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!requestedBy[0]) { setFormError("Choose who is raising this request."); return; }
    if (!subjectId) { setFormError("Choose who the access is for."); return; }
    if (picks.length === 0) { setFormError("Tick at least one role or entitlement to request."); return; }
    setFormError("");
    setSubmitting(true);
    try {
      const created = await createAccessRequest({ requestedById: requestedBy[0], requestForId: subjectId, items: picks.map((pick) => ({ id: pick.id, kind: pick.kind, ...(pick.note.trim() ? { note: pick.note.trim() } : {}) })) });
      onNotify(`Access request ${created.id} submitted: ${picks.length} ${picks.length === 1 ? "approval task" : "approval tasks"} raised.`);
      setPicks([]);
      setOptionsVersion((value) => value + 1);
      setListReload((value) => value + 1);
    } catch (error) {
      setFormError(error instanceof Error ? error.message : "Could not submit the access request.");
    } finally {
      setSubmitting(false);
    }
  }

  const offered = options?.applications.reduce((total, application) => total + application.roles.length + application.entitlements.length, 0) ?? 0;

  return <>
    <section className="records-section request-form-panel" aria-label="Access Request Form">
      <section className="editor-dialog editor-inline" aria-labelledby="access-form-title">
        <form onSubmit={(event) => void submit(event)}>
          <div className="dialog-heading"><div><p className="eyebrow">NEW REQUEST</p><h2 id="access-form-title">Access Request Form</h2></div></div>
          <section className="ci-picker raci-section request-section" aria-labelledby="access-people-title">
            <div className="ci-picker-heading"><div><h3 id="access-people-title">Who</h3><span className="required-mark">Required</span></div></div>
            {identitiesError ? <p className="form-error" role="alert">{identitiesError}</p> : <div className="raci-grid">
              <RecordPicker id="access-requested-by" label="Requested by" hint="The identity raising the request; it starts as the signed-in user." placeholder="Find an identity by name or ID" single options={identities} selected={requestedBy} onChange={setRequestedBy} />
              <RecordPicker id="access-request-for" label="Request for" hint="The identity that will receive the access. Anything it already holds, or is already waiting on, is left off the list." placeholder="Find an identity by name or ID" single options={identities} selected={requestFor} onChange={setRequestFor} />
            </div>}
          </section>

          <section className="ci-picker" aria-labelledby="access-items-title">
            <div className="ci-picker-heading"><div><h3 id="access-items-title">Access to request</h3>{subjectId && <span className="required-mark">Required</span>}</div><span className="ci-selection-count">{subjectId ? `${picks.length} selected · ${offered} available` : ""}</span></div>
            {!subjectId ? <p className="ci-empty">Choose who the request is for to see the applications, roles, and entitlements they can ask for.</p>
              : loadingOptions ? <p className="field-hint">Loading what {subject ? nodeDisplayName(subject) ?? subject.id : "this identity"} may request…</p>
              : optionsError ? <p className="form-error" role="alert">{optionsError}</p>
              : !options || options.applications.length === 0 ? <p className="ci-empty">Nothing is available to request{subject ? ` for ${nodeDisplayName(subject) ?? subject.id}` : ""}. Roles and entitlements appear here once an application CI provides access to them and an Accountable owner is set on their RACI tab, and only when the identity does not already hold them.</p>
              : <div className="access-applications">{options.applications.map((application) => <section className="access-application" key={application.id} aria-label={application.name ?? application.id}>
                <div className="access-application-head"><strong>{application.name ?? application.id}</strong><small>{application.id} · Application</small></div>
                {(["role", "entitlement"] as const).filter((kind) => itemKinds.includes(kind)).map((kind) => {
                  const items = kind === "role" ? application.roles : application.entitlements;
                  if (items.length === 0) return null;
                  return <div className="access-item-group" key={kind}>
                    <span className="access-item-kind">{titleCase(kind)}s</span>
                    <div className="ci-options access-options">{items.map((option) => {
                      const pick = picks.find((candidate) => pickKey(candidate) === pickKey(option));
                      const inputId = `access-${option.kind}-${option.id}`;
                      return <div className={`access-option ${pick ? "selected" : ""}`} key={pickKey(option)}>
                        <label className={`ci-option ${pick ? "selected" : ""}`} htmlFor={inputId}>
                          <input id={inputId} type="checkbox" checked={Boolean(pick)} onChange={() => toggle(option)} />
                          <span><strong>{option.name ?? option.id}</strong><small>{option.id} · {titleCase(option.kind)} · approved by {option.owner.name ?? option.owner.id}</small>{option.description && <small className="access-description">{option.description}</small>}</span>
                        </label>
                        {pick && <div className="form-field access-note"><label htmlFor={`${inputId}-note`}>Note for the approver <span className="ci-selection-count">Optional</span></label><input id={`${inputId}-note`} value={pick.note} autoComplete="off" placeholder="Why this access is needed" onChange={(event) => setNote(option, event.target.value)} /></div>}
                      </div>;
                    })}</div>
                  </div>;
                })}
              </section>)}</div>}
          </section>

          {options && options.held.length > 0 && <section className="ci-picker" aria-labelledby="access-held-title">
            <div className="ci-picker-heading"><div><h3 id="access-held-title">Already held or requested</h3></div><span className="ci-selection-count">{options.held.length} not offered</span></div>
            <ul className="raci-chips" aria-label="Access already held">{options.held.map((holding) => <li className="raci-chip access-held" key={pickKey(holding)}><span><strong>{holding.name ?? holding.id}</strong><small>{holding.id} · {titleCase(holding.kind)} · {heldVia[holding.via]}{holding.source ? ` ${holding.source}` : ""}</small></span></li>)}</ul>
          </section>}

          <SLANotice enabled={slaEnabled} days={slaDays} />

          {formError && <p className="form-error" role="alert">{formError}</p>}
          <div className="dialog-actions">
            <span className="field-hint">Each role or entitlement goes to its Accountable owner for approval; approved items are then provisioned by the fulfilment team. There is no draft.</span>
            <button className="button button-primary" type="submit" disabled={submitting || picks.length === 0}>{submitting ? "Submitting…" : "Submit request"}</button>
          </div>
        </form>
      </section>
    </section>

    <section className="records-section" aria-label="Access requests">
      <div className="ci-picker-heading saved-requests-heading"><div><h3>Access requests</h3></div><span className="ci-selection-count">Open a request to follow each item's approval and provisioning. Finished requests are retired; tick Include retired to see them.</span></div>
      <div className="records-toolbar"><span className="secondary-value">{loadingRequests ? "Loading requests…" : `${requestTotal.toLocaleString()} ${requestTotal === 1 ? "request" : "requests"}`}</span><label className="retired-toggle"><input type="checkbox" checked={includeRetired} onChange={(event) => setIncludeRetired(event.target.checked)} /><span>Include retired</span></label></div>
      <div className="table-frame">
        <table>
          <thead><tr><th>Identifier</th><th>For</th><th>Items</th><th>Submitted</th><th>Status</th><th>Actions</th></tr></thead>
          <tbody>{requests.map((record) => {
            const items = accessItemsOf(record);
            const state = requestStateOf(record);
            const requestedFor = record.relationships?.find((relationship) => relationship.kind === "requested-for" && relationship.status === "active");
            const requester = record.relationships?.find((relationship) => relationship.kind === "form-submitted");
            return <tr key={record.id}>
              <td><span className="identifier">{record.id}</span></td>
              <td><div className="primary-value">{requestedFor?.toName ?? requestedFor?.toId ?? "—"}</div><div className="secondary-value">raised by {requester?.fromName ?? requester?.fromId ?? "unknown"}</div></td>
              <td><div className="primary-value">{items.length} {items.length === 1 ? "item" : "items"}</div><div className="secondary-value">{summarizeDecisions(items)}</div></td>
              <td><div className="secondary-value">{formatWhen(typeof record.properties?.submittedAt === "string" ? record.properties.submittedAt : undefined) || "—"}</div></td>
              <td><span className={`status-pill ${requestStatePillClass(state)}`}>{state}</span>{record.status === "retired" && <div className="secondary-value">retired</div>}</td>
              <td><div className="row-actions"><button className="action-button" onClick={() => onOpenRequest(record)} title="Open request"><ClipboardList size={14} /><span>Open</span></button></div></td>
            </tr>;
          })}</tbody>
        </table>
        {!loadingRequests && requests.length === 0 && <div className="empty-state"><span className="empty-rule" /><strong>{requestsError || "No access requests yet"}</strong><p>{requestsError ? "" : "Submitted requests are listed here with the decision on each item."}</p></div>}
      </div>
      <div className="table-footer"><Pager page={requestPage} total={requestTotal} loading={loadingRequests} noun="record" onPage={setRequestPage} /><span>Sorted by identifier</span></div>
    </section>

    {openRequest && <AccessRequestDialog key={openRequest.id} record={openRequest} onClose={() => onOpenRequest(null)} />}
  </>;
}

type RequestItem = { id: string; kind: AccessItemKind; name?: string; note?: string; decision: ItemDecision; decidedAt?: string; decidedBy?: string; fulfilledAt?: string };

// The items on a saved access request live on its requests-access links. The
// link names the item but not its kind; IDs are server-assigned with a kind
// prefix, so an ENT- ID is an entitlement and anything else here is a role.
function accessItemsOf(record: GraphNode): RequestItem[] {
  return (record.relationships ?? []).filter((relationship) => relationship.kind === "requests-access" && relationship.fromId === record.id).map((relationship): RequestItem => {
    const properties = relationship.properties ?? {};
    const text = (key: string) => typeof properties[key] === "string" ? String(properties[key]) : undefined;
    const decision = text("decision");
    return {
      id: relationship.toId,
      kind: relationship.toId.startsWith("ENT-") ? "entitlement" : "role",
      name: relationship.toName,
      note: text("note"),
      decision: decision === "approved" || decision === "denied" || decision === "fulfilled" ? decision : "pending",
      decidedAt: text("decidedAt"),
      decidedBy: text("decidedBy"),
      fulfilledAt: text("fulfilledAt"),
    };
  }).sort((a, b) => (a.name ?? a.id).localeCompare(b.name ?? b.id));
}

function summarizeDecisions(items: RequestItem[]) {
  const counts = new Map<ItemDecision, number>();
  for (const item of items) counts.set(item.decision, (counts.get(item.decision) ?? 0) + 1);
  return [...counts.entries()].map(([decision, count]) => `${count} ${decision === "fulfilled" ? "provisioned" : decision}`).join(" · ") || "—";
}

function AccessRequestDialog({ record, onClose }: { record: GraphNode; onClose: () => void }) {
  const items = accessItemsOf(record);
  const state = requestStateOf(record);
  const properties = record.properties ?? {};
  const requestedFor = record.relationships?.find((relationship) => relationship.kind === "requested-for");
  const requester = record.relationships?.find((relationship) => relationship.kind === "form-submitted");
  const [tasks, setTasks] = useState<TaskView[]>([]);
  const [tasksError, setTasksError] = useState("");

  useEffect(() => {
    let active = true;
    getTasks({ requestId: record.id, includeDone: true }).then((result) => {
      if (active) setTasks(result);
    }).catch((error: unknown) => {
      if (active) setTasksError(error instanceof Error ? error.message : "Could not load the request's tasks.");
    });
    return () => { active = false; };
  }, [record.id]);

  return <div className="modal-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
    <section className="editor-dialog task-dialog" role="dialog" aria-modal="true" aria-labelledby="access-request-title">
      <form onSubmit={(event) => event.preventDefault()}>
        <div className="dialog-heading">
          <div><p className="eyebrow">{state.toUpperCase()} REQUEST · {record.id}</p><h2 id="access-request-title">{nodeDisplayName(record) ?? "Access request"}</h2><p className="page-subtitle">For {requestedFor?.toName ?? requestedFor?.toId ?? "an unknown identity"}, raised by {requester?.fromName ?? requester?.fromId ?? "an unknown requester"}{typeof properties.submittedAt === "string" ? ` · submitted ${formatWhen(properties.submittedAt)}` : ""}</p></div>
          <button className="close-button" type="button" onClick={onClose} aria-label="Close"><X size={17} /></button>
        </div>

        <SLANotice enabled={Boolean(properties.slaEnabled)} days={typeof properties.slaBusinessDays === "number" ? properties.slaBusinessDays : Number(properties.slaBusinessDays) || 0} />

        <section className="ci-picker raci-section request-section" aria-labelledby="access-request-items-title">
          <div className="ci-picker-heading"><div><h3 id="access-request-items-title">Requested access</h3></div><span className={`status-pill ${requestStatePillClass(state)}`}>{state}</span></div>
          {items.length === 0 ? <p className="ci-empty">No items are recorded on this request.</p> : <ol className="task-trail">{items.map((item) => <li className={`task-trail-item item-${item.decision}`} key={`${item.kind}:${item.id}`}>
            <div className="task-trail-head"><strong>{item.name ?? item.id}</strong><span className={`status-pill ${itemDecisionPillClass(item.decision)}`}>{itemDecisionLabel(item.decision)}</span></div>
            <div className="secondary-value">{item.id} · {titleCase(item.kind)}{item.decidedBy ? ` · ${item.decision === "denied" ? "denied" : "approved"} by ${item.decidedBy}${item.decidedAt ? ` ${formatWhen(item.decidedAt)}` : ""}` : ""}{item.fulfilledAt ? ` · provisioned ${formatWhen(item.fulfilledAt)}` : ""}</div>
            {item.note && <div className="task-trail-action"><p>{item.note}</p></div>}
          </li>)}</ol>}
          {state === "fulfilled" && <p className="field-hint">Provisioned items are now permissions links from the role or entitlement to the identity, stamped with this request's ID. The request, its run, and its tasks were retired when the last item was settled.</p>}
          {state === "denied" && <p className="field-hint">Every item was denied, so the request was retired without granting anything.</p>}
        </section>

        <section className="ci-picker" aria-labelledby="access-request-tasks-title">
          <div className="ci-picker-heading"><div><h3 id="access-request-tasks-title">Workflow progress</h3></div><span className="ci-selection-count">{tasks[0]?.workflowName ?? ""}</span></div>
          {tasksError ? <p className="form-error" role="alert">{tasksError}</p> : tasks.length === 0 ? <p className="ci-empty">No workflow tasks recorded for this request.</p>
            : <>
              {tasks[0]?.sla && <div className="agreement-row"><AgreementMeter clock={tasks[0].sla} /></div>}
              <ol className="task-trail">{tasks.map((task) => <li className={`task-trail-item task-${task.status}`} key={task.id}>
              <div className="task-trail-head"><strong>{task.step.order ? `${task.step.order}. ` : ""}{task.step.name}{task.item ? ` · ${task.item.name ?? task.item.id}` : ""}</strong><span className="agreement-inline"><AgreementMeter clock={task.ola} compact /><span className={`status-pill ${taskStatusPillClass(task.status)}`}>{task.status}</span></span></div>
              <div className="secondary-value">{titleCase(task.step.stepType)} · {task.assignees.map((assignee) => assignee.name ?? assignee.id).join(", ") || "Unassigned"} · {task.id}</div>
              {task.actions.map((action, index) => <div className="task-trail-action" key={index}><span>{titleCase(action.action)} by {action.actorName ?? action.actorId} · {formatWhen(action.actedAt)}</span>{action.comment && <p>{action.comment}</p>}</div>)}
            </li>)}</ol>
            </>}
        </section>

        <div className="dialog-actions"><button className="button button-quiet" type="button" onClick={onClose}>Close</button></div>
      </form>
    </section>
  </div>;
}
