import { useEffect, useState } from "react";
import { Archive, ClipboardList, SquareArrowOutUpRight, X } from "lucide-react";
import { actOnTask, getTaskPage, nodeDisplayName } from "./api";
import { clampPage, defaultPageSize, Pager } from "./Pager";
import { titleCase } from "./RecordPicker";
import { AgreementMeter } from "./AgreementMeter";
import { formatWhen, itemDecisionLabel, itemDecisionPillClass, requestFieldLabel, requestFieldLabels, requestStateOf, requestStatePillClass, taskStatusPillClass } from "./workflows";
import type { SessionUser } from "./session";
import type { GraphNode, Metadata, Properties, RequestType, TaskAction, TaskView } from "./types";

// Unknown request types fall back to the vendor form so the task still renders.
function requestTypeOf(task: TaskView): RequestType {
  const type = task.request.properties?.requestType;
  return typeof type === "string" && type in requestFieldLabels ? type as RequestType : "vendor";
}

// The queue follows the signed-in user: tasks assigned to that identity or to a
// group it belongs to. The super admin, acting as themselves, sees every task
// but acts on none; they switch to an eligible identity from the user menu.
export default function WorkflowTasks({ user, metadata, reload, onNotify, onOpenRequest, onRetire }: { user: SessionUser; metadata: Metadata | null; reload: number; onNotify: (message: string) => void; onOpenRequest: (record: GraphNode) => void; onRetire: (task: TaskView) => void }) {
  const [includeDone, setIncludeDone] = useState(false);
  const [tasks, setTasks] = useState<TaskView[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(0);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState("");
  const [selected, setSelected] = useState<TaskView | null>(null);
  const [listReload, setListReload] = useState(0);
  const actorId = user.superAdmin ? "" : user.id;

  useEffect(() => { setPage(0); }, [actorId, includeDone]);

  // Refresh open SLA/OLA meters about once a minute so the ring keeps moving.
  useEffect(() => {
    const timer = window.setInterval(() => setListReload((value) => value + 1), 60_000);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    let active = true;
    setLoading(true);
    getTaskPage({ actorId, includeDone, limit: defaultPageSize, offset: page * defaultPageSize }).then((result) => {
      if (!active) return;
      setTasks(result.items);
      setTotal(result.total);
      const clamped = clampPage(page, result.total);
      if (clamped !== page) setPage(clamped);
      setLoadError("");
      setSelected((current) => current ? result.items.find((task) => task.id === current.id) ?? current : null);
    }).catch((error: unknown) => {
      if (active) setLoadError(error instanceof Error ? error.message : "Could not load tasks.");
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [actorId, includeDone, page, reload, listReload]);

  // Without completed tasks the list is the pending queue, so its total is the pending count.
  const pending = includeDone ? tasks.filter((task) => task.status === "pending").length : total;

  return <>
    <section className="records-section" aria-label="Tasks">
      <div className="ci-picker-heading saved-requests-heading"><div><h3>{user.superAdmin ? "All tasks on the platform" : `Tasks for ${user.name}`}</h3></div><span className="ci-selection-count">{`${pending} pending${includeDone && total > defaultPageSize ? " on this page" : ""}`}{user.superAdmin ? " · act as an identity from the user menu to work a task" : ""}</span></div>
      <div className="records-toolbar"><span className="secondary-value"><Pager page={page} total={total} loading={loading} noun="task" onPage={setPage} /></span><label className="retired-toggle"><input type="checkbox" checked={includeDone} onChange={(event) => setIncludeDone(event.target.checked)} /><span>Include completed</span></label></div>
      {includeDone && <p className="field-hint saved-requests-heading">Completed tasks stay here as history; once a workflow finishes they, their run, and the request are retired.</p>}
      <div className="table-frame">
        <table>
          <thead><tr><th>Identifier</th><th>Request</th><th>Step</th><th>Assigned to</th><th>Raised</th><th>Agreement</th><th>Status</th><th>Actions</th></tr></thead>
          <tbody>{tasks.map((task) => <tr key={task.id}>
            <td><span className="identifier">{task.id}</span></td>
            <td><div className="primary-value">{nodeDisplayName(task.request) ?? task.request.id}</div><div className="secondary-value">{task.request.id} · {task.requestedFor ? `for ${task.requestedFor.name ?? task.requestedFor.id}, raised by ${task.requester?.name ?? task.requester?.id ?? "unknown"}` : task.requester ? `for ${task.requester.name ?? task.requester.id}` : "requester unknown"}</div></td>
            <td><div className="primary-value">{task.step.order}. {task.step.name}{task.item ? ` · ${task.item.name ?? task.item.id}` : ""}</div><div className="secondary-value">{titleCase(task.step.stepType)}{task.step.stepType === "approval" && !task.item ? ` · ${task.step.approvalRule === "all" ? "all approvers" : "any approver"}` : ""}{task.item ? ` · ${titleCase(task.item.kind)}${task.item.applicationName ? ` in ${task.item.applicationName}` : ""}` : ""} · {task.workflowName}{task.finalStep && !task.item ? " · final step" : ""}</div></td>
            <td><div className="secondary-value">{task.assignees.map((assignee) => assignee.name ?? assignee.id).join(", ") || "—"}</div></td>
            <td><div className="secondary-value">{formatWhen(task.createdAt)}</div></td>
            <td><div className="agreement-stack"><AgreementMeter clock={task.sla} compact /><AgreementMeter clock={task.ola} compact /></div></td>
            <td><span className={`status-pill ${taskStatusPillClass(task.status)}`}>{task.status}</span>{task.retiredAt && <div className="secondary-value">retired</div>}</td>
            <td><div className="row-actions">
              <button className="action-button" onClick={() => setSelected(task)} title={task.status === "pending" ? "Open task" : "View task"}><ClipboardList size={14} /><span>{task.status === "pending" ? "Open" : "View"}</span></button>
              {task.status !== "pending" && !task.retiredAt && <button className="action-button retire" onClick={() => onRetire(task)} title="Retire task"><Archive size={14} /><span>Retire</span></button>}
            </div></td>
          </tr>)}</tbody>
        </table>
        {!loading && tasks.length === 0 && <div className="empty-state"><span className="empty-rule" /><strong>{loadError || (actorId ? "Nothing waiting on you" : "No tasks")}</strong><p>{loadError ? "" : actorId ? "Tasks appear here when a submitted form reaches a step assigned to you or your groups." : "No workflow task is pending anywhere on the platform."}</p></div>}
      </div>
    </section>

    {selected && <TaskDialog key={selected.id} task={selected} actorId={actorId} metadata={metadata} onClose={() => setSelected(null)} onOpenRequest={onOpenRequest} onDone={(message) => { setSelected(null); onNotify(message); setListReload((value) => value + 1); }} />}
  </>;
}

export function TaskDialog({ task, actorId, metadata, onClose, onOpenRequest, onDone }: { task: TaskView; actorId: string; metadata: Metadata | null; onClose: () => void; onOpenRequest: (record: GraphNode) => void; onDone: (message: string) => void }) {
  const requestType = requestTypeOf(task);
  const fieldKeys = metadata?.requestFields?.[requestType] ?? Object.keys(requestFieldLabels[requestType] ?? {});
  const editable = task.step.editableFields ?? [];
  const required = task.step.requiredFields ?? [];
  const requestProperties = task.request.properties ?? {};
  const [values, setValues] = useState<Properties>(() => Object.fromEntries(editable.map((key) => [key, requestProperties[key] ?? ""])));
  const [comment, setComment] = useState("");
  const [error, setError] = useState("");
  const [acting, setActing] = useState(false);
  const pending = task.status === "pending";
  const eligible = task.eligibleActors.some((candidate) => candidate.id === actorId);
  const alreadyApproved = task.actions.some((action) => action.actorId === actorId && action.action === "approved");
  const canAct = pending && eligible && !alreadyApproved;
  const requestState = requestStateOf(task.request);
  // Access tasks are about one requested role or entitlement: the approval step
  // approves or denies it, and the fulfilment step records it as provisioned.
  const isAccess = requestType === "access";
  const item = task.item;
  const approvalStep = task.step.stepType === "approval";

  async function act(action: TaskAction) {
    if (action === "reject" && !comment.trim()) { setError(isAccess ? "Give the reason for denying this access before continuing." : "Explain what the requester needs to change before returning the request."); return; }
    const properties: Properties = {};
    if (action !== "reject" && !isAccess) {
      for (const key of editable) {
        const value = values[key];
        const text = typeof value === "string" ? value.trim() : value;
        if (text === "" || text === undefined) continue;
        if (text !== requestProperties[key]) properties[key] = text;
      }
      const missing = required.filter((key) => { const value = key in properties ? properties[key] : requestProperties[key]; return value === undefined || value === ""; });
      if (missing.length > 0) { setError(`Complete ${missing.map((key) => requestFieldLabel(requestType, key)).join(" and ")} before ${action === "approve" ? "approving" : "completing"} this step.`); return; }
    }
    setError("");
    setActing(true);
    try {
      const result = await actOnTask(task.id, { actorId, action, ...(comment.trim() ? { comment: comment.trim() } : {}), ...(Object.keys(properties).length > 0 ? { properties } : {}) });
      const resultState = requestStateOf(result.request);
      const fulfilled = resultState === "fulfilled";
      if (isAccess) {
        const itemName = item?.name ?? item?.id ?? "the item";
        onDone(action === "reject" ? (resultState === "denied" ? `${itemName} denied; every item was denied, so the request was retired.` : `${itemName} denied; the rest of the request continues.`)
          : action === "approve" ? `${itemName} approved; a fulfilment task was raised.`
          : fulfilled ? `${itemName} provisioned and the permission recorded; the request is complete and retired.` : `${itemName} provisioned and the permission recorded; other items are still in progress.`);
        return;
      }
      onDone(action === "reject" ? "Request returned to the requester." : fulfilled ? "Request fulfilled: the vendor CI was created and the request, run, and tasks were retired." : result.status === "pending" ? "Approval recorded; the step is waiting on other approvers." : action === "approve" ? "Step approved." : "Step completed.");
    } catch (actError) {
      setError(actError instanceof Error ? actError.message : "Could not act on this task.");
    } finally {
      setActing(false);
    }
  }

  return <div className="modal-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
    <section className="editor-dialog task-dialog" role="dialog" aria-modal="true" aria-labelledby="task-title">
      <form onSubmit={(event) => { event.preventDefault(); void act(task.step.stepType === "approval" ? "approve" : "complete"); }}>
        <div className="dialog-heading">
          <div><p className="eyebrow">{task.status.toUpperCase()} TASK · {task.id}</p><h2 id="task-title">{task.step.order}. {task.step.name}{item ? ` · ${item.name ?? item.id}` : ""}</h2><p className="page-subtitle">{titleCase(task.step.stepType)} step of {task.workflowName}{isAccess ? (approvalStep ? " · approve or deny this one item" : " · completing it records the permission on the identity") : `${task.step.stepType === "approval" ? ` · ${task.step.approvalRule === "all" ? "every eligible approver must approve" : "any one approver"}` : ""}${task.finalStep ? " · completing it creates the vendor CI" : ""}`}</p></div>
          <button className="close-button" type="button" onClick={onClose} aria-label="Close"><X size={17} /></button>
        </div>
        {task.step.instructions && <p className="system-generated-id">{task.step.instructions}</p>}
        <div className="agreement-row">
          <AgreementMeter clock={task.sla} />
          <AgreementMeter clock={task.ola} />
        </div>

        <section className="ci-picker raci-section request-section" aria-labelledby="task-request-title">
          <div className="ci-picker-heading"><div><h3 id="task-request-title">Request {task.request.id}</h3><button type="button" className="action-button" onClick={() => onOpenRequest(task.request)} title="Open the request form"><SquareArrowOutUpRight size={13} /><span>Open form</span></button></div><span className={`status-pill ${requestStatePillClass(requestState)}`}>{requestState}</span></div>
          {isAccess ? <>
            <p className="field-hint">Access for <strong>{task.requestedFor?.name ?? task.requestedFor?.id ?? "an unknown identity"}</strong>, raised by {task.requester?.name ?? task.requester?.id ?? "an unknown requester"}{typeof requestProperties.submittedAt === "string" ? `, submitted ${formatWhen(requestProperties.submittedAt)}` : ""}.</p>
            {item && <div className="access-item-card"><strong>{item.name ?? item.id}</strong><small>{item.id} · {titleCase(item.kind)}{item.applicationName ? ` · provided by ${item.applicationName}` : ""} · {itemDecisionLabel(item.decision)}</small>{item.note && <p>{item.note}</p>}</div>}
            {task.items && task.items.length > 1 && <>
              <span className="access-item-kind">Everything on this request</span>
              <ol className="task-trail">{task.items.map((sibling) => <li className={`task-trail-item item-${sibling.decision ?? "pending"}`} key={`${sibling.kind}:${sibling.id}`}>
                <div className="task-trail-head"><strong>{sibling.name ?? sibling.id}{item && sibling.id === item.id && sibling.kind === item.kind ? " (this task)" : ""}</strong><span className={`status-pill ${itemDecisionPillClass(sibling.decision)}`}>{itemDecisionLabel(sibling.decision)}</span></div>
                <div className="secondary-value">{sibling.id} · {titleCase(sibling.kind)}{sibling.applicationName ? ` in ${sibling.applicationName}` : ""}{sibling.decidedBy ? ` · ${sibling.decision === "denied" ? "denied" : "approved"} by ${sibling.decidedBy}` : ""}</div>
              </li>)}</ol>
            </>}
          </> : <>
          <p className="field-hint">Raised by {task.requester?.name ?? task.requester?.id ?? "an unknown requester"}{typeof requestProperties.submittedAt === "string" ? `, submitted ${formatWhen(requestProperties.submittedAt)}` : ""}. {editable.length > 0 ? "Fields this step may change are open for entry." : "This step reviews the form as it stands."}</p>
          <div className="field-grid">{fieldKeys.map((key) => {
            const label = requestFieldLabel(requestType, key);
            const isEditable = canAct && editable.includes(key);
            const isRequired = required.includes(key);
            const current = requestProperties[key];
            if (!isEditable) return <div className="form-field" key={key}><label>{label}{isRequired && !isEditable && <> <span className="ci-selection-count">Required here</span></>}</label><input value={current === undefined ? "" : String(current)} readOnly placeholder="—" /></div>;
            const value = values[key] === undefined ? "" : String(values[key]);
            const setValue = (next: string) => setValues((previous) => ({ ...previous, [key]: next }));
            if (key === "criticality") {
              const options = metadata?.criticalities ?? ["critical", "important", "business-support"];
              return <div className="form-field" key={key}><label htmlFor={`task-${key}`}>{label} <span className="ci-selection-count">{isRequired ? "Required" : "Editable"}</span></label>
                <select id={`task-${key}`} value={value} onChange={(event) => setValue(event.target.value)}><option value="">Select {label.toLowerCase()}</option>{options.map((option) => <option key={option} value={option}>{titleCase(option)}</option>)}</select></div>;
            }
            return <div className="form-field" key={key}><label htmlFor={`task-${key}`}>{label} <span className="ci-selection-count">{isRequired ? "Required" : "Editable"}</span></label>
              <input id={`task-${key}`} type={key === "contactEmail" ? "email" : key === "contactPhone" ? "tel" : "text"} autoComplete="off" placeholder={key === "contactPhone" ? "(555) 010-0100" : undefined} value={value} onChange={(event) => setValue(event.target.value)} /></div>;
          })}</div>
          </>}
        </section>

        <section className="ci-picker" aria-labelledby="task-history-title">
          <div className="ci-picker-heading"><div><h3 id="task-history-title">Assignment and history</h3></div><span className="ci-selection-count">{task.eligibleActors.length} can act</span></div>
          <p className="field-hint">Assigned to {task.assignees.map((assignee) => `${assignee.name ?? assignee.id} (${titleCase(assignee.kind)})`).join(", ") || "nobody"}. Eligible: {task.eligibleActors.map((candidate) => candidate.name ?? candidate.id).join(", ") || "nobody"}.</p>
          {task.actions.length > 0 && <ol className="task-trail">{task.actions.map((action, index) => <li className={`task-trail-item task-${action.action}`} key={index}><div className="task-trail-action"><span>{titleCase(action.action)} by {action.actorName ?? action.actorId} · {formatWhen(action.actedAt)}</span>{action.comment && <p>{action.comment}</p>}</div></li>)}</ol>}
        </section>

        {canAct && <div className="form-field"><label htmlFor="task-comment">Comment</label><textarea id="task-comment" rows={3} value={comment} onChange={(event) => setComment(event.target.value)} placeholder={isAccess ? (approvalStep ? "Optional when approving; required when denying." : "Optional, e.g. where the access was provisioned.") : task.step.stepType === "approval" ? "Optional when approving; required when returning the request." : "Optional when completing; required when returning the request."} /></div>}
        {!pending && <p className="field-hint">This task is {task.status}; nothing further can be done here.</p>}
        {pending && !eligible && <p className="form-error" role="alert">{actorId ? "The identity you are acting as is not eligible for this task." : "The super admin does not act on tasks directly: use the user menu to act as one of the eligible identities."}</p>}
        {pending && alreadyApproved && <p className="field-hint">You have already approved this step; it is waiting on the other approvers.</p>}
        {error && <p className="form-error" role="alert">{error}</p>}
        <div className="dialog-actions">
          <button className="button button-quiet" type="button" onClick={onClose}>Close</button>
          {canAct && (!isAccess || approvalStep) && <button className="button button-danger" type="button" disabled={acting} onClick={() => void act("reject")}>{isAccess ? "Deny" : "Return to requester"}</button>}
          {canAct && <button className="button button-primary" type="submit" disabled={acting}>{acting ? "Working…" : approvalStep ? "Approve" : isAccess ? "Mark provisioned" : "Complete step"}</button>}
        </div>
      </form>
    </section>
  </div>;
}
