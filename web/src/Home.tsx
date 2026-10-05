import { useEffect, useState } from "react";
import { ClipboardList, ChevronLeft, ChevronRight, UserRound } from "lucide-react";
import { getAccessOptions, getHolidays, getNodePage, getTasks, nodeDisplayName, saveHolidays } from "./api";
import { AgreementMeter } from "./AgreementMeter";
import { defaultPageSize } from "./Pager";
import { titleCase } from "./RecordPicker";
import { TaskDialog } from "./WorkflowTasks";
import { formatWhen, requestStateOf, requestStatePillClass, requestTypeOf, taskStatusPillClass, formTitles } from "./workflows";
import type { SessionUser } from "./session";
import type { AccessHolding, GraphNode, Holiday, Metadata, TaskView } from "./types";

// The signed-in user's landing page: who they are and the work waiting on
// them. The super admin, acting as themselves, sees every open task and
// request on the platform instead.

const heldVia: Record<AccessHolding["via"], string> = {
  birthright: "via birthright",
  role: "via role",
  direct: "granted directly",
  pending: "requested",
};

export default function Home({ user, metadata, reload, onNotify, onOpenRequest, onOpenTasks }: {
  user: SessionUser;
  metadata: Metadata | null;
  reload: number;
  onNotify: (message: string) => void;
  onOpenRequest: (record: GraphNode) => void;
  onOpenTasks: () => void;
}) {
  const actorId = user.superAdmin ? "" : user.id;
  // The identity record (with its job code, group, and location links) comes with the session.
  const identity = user.identity ?? null;
  const [held, setHeld] = useState<AccessHolding[]>([]);
  const [profileError, setProfileError] = useState("");
  const [tasks, setTasks] = useState<TaskView[]>([]);
  const [tasksError, setTasksError] = useState("");
  const [requests, setRequests] = useState<GraphNode[]>([]);
  const [requestTotal, setRequestTotal] = useState(0);
  const [requestsError, setRequestsError] = useState("");
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState<TaskView | null>(null);
  const [listReload, setListReload] = useState(0);
  const [profileTab, setProfileTab] = useState<"details" | "access">("details");

  // Keep SLA/OLA rings current while Home stays open.
  useEffect(() => {
    const timer = window.setInterval(() => setListReload((value) => value + 1), 60_000);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    let active = true;
    setLoading(true);
    const profile = actorId
      ? getAccessOptions(actorId).then((options) => {
        if (!active) return;
        setHeld(options.held.filter((holding) => holding.via !== "pending"));
        setProfileError("");
      }).catch((error: unknown) => {
        if (active) setProfileError(error instanceof Error ? error.message : "Could not load the access held.");
      })
      : Promise.resolve().then(() => { setHeld([]); setProfileError(""); });
    const work = getTasks({ actorId, includeDone: false }).then((items) => {
      if (active) { setTasks(items); setTasksError(""); }
    }).catch((error: unknown) => {
      if (active) setTasksError(error instanceof Error ? error.message : "Could not load tasks.");
    });
    // Open requests the user raised or that are for them (the API anchors the
    // read at the identity); everything open for the super admin, latest page.
    const mine = getNodePage("request", { involvedId: actorId || undefined, limit: defaultPageSize }).then((result) => {
      if (!active) return;
      setRequests(result.items.filter((record) => {
        const state = requestStateOf(record);
        return state !== "fulfilled" && state !== "denied";
      }));
      setRequestTotal(result.total);
      setRequestsError("");
    }).catch((error: unknown) => {
      if (active) setRequestsError(error instanceof Error ? error.message : "Could not load requests.");
    });
    Promise.all([profile, work, mine]).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [actorId, reload, listReload]);

  const properties = identity?.properties ?? {};
  const links = (identity?.relationships ?? []).filter((relationship) => relationship.status !== "retired");
  const jobCodes = links.filter((relationship) => relationship.kind === "has-job-code" && relationship.fromId === identity?.id).map((relationship) => relationship.toName ?? relationship.toId);
  const groups = links.filter((relationship) => relationship.kind === "member" && relationship.toId === identity?.id).map((relationship) => relationship.fromName ?? relationship.fromId);
  const location = links.find((relationship) => relationship.kind === "work-location" && relationship.fromId === identity?.id);
  const approvals = tasks.filter((task) => task.step.stepType === "approval").length;

  return <>
    <section className="records-section request-form-panel" aria-label="Profile">
      <section className="editor-dialog editor-inline home-profile" aria-labelledby="profile-title">
        <form onSubmit={(event) => event.preventDefault()}>
          <div className="dialog-heading">
            <div className="home-identity"><span className="home-avatar" aria-hidden="true"><UserRound size={26} strokeWidth={1.6} /></span><div><p className="eyebrow">{user.superAdmin ? "PLATFORM ACCOUNT" : "IDENTITY PROFILE"}</p><h2 id="profile-title">{user.name}</h2><p className="page-subtitle">{user.superAdmin ? "Development sign-in with access to every view." : [user.id, properties.department, jobCodes[0]].filter((part) => typeof part === "string" && part).join(" · ")}</p></div></div>
          </div>
          {user.superAdmin ? <>
            <p className="system-generated-id">You are signed in as the platform super admin. Until single sign-on arrives, use the user menu in the top-left to act as any configured identity and see their profile and queue; the lists below show everything open on the platform. Mark holidays here so SLA and OLA clocks skip days the business is closed.</p>
            <HolidayCalendar onNotify={onNotify} reload={reload} />
          </>
            : <>
              <div className="dialog-tabs" role="tablist" aria-label="Profile sections">
                <button type="button" role="tab" id="tab-profile-details" aria-selected={profileTab === "details"} aria-controls="panel-profile-details" className={profileTab === "details" ? "active" : ""} onClick={() => setProfileTab("details")}>Details</button>
                <button type="button" role="tab" id="tab-profile-access" aria-selected={profileTab === "access"} aria-controls="panel-profile-access" className={profileTab === "access" ? "active" : ""} onClick={() => setProfileTab("access")}>Access held<span className="tab-count">{held.length}</span></button>
              </div>
              <div id="panel-profile-details" role="tabpanel" aria-labelledby="tab-profile-details" hidden={profileTab !== "details"}>
                <div className="field-grid">
                  <div className="form-field"><label>Department</label><input value={typeof properties.department === "string" ? properties.department : ""} readOnly placeholder="—" /></div>
                  <div className="form-field"><label>Location</label><input value={location?.toName ?? (typeof properties.location === "string" ? properties.location : "")} readOnly placeholder="—" /></div>
                  <div className="form-field"><label>Job code{jobCodes.length === 1 ? "" : "s"}</label><input value={jobCodes.join(", ")} readOnly placeholder="—" /></div>
                  <div className="form-field"><label>Groups</label><input value={groups.join(", ")} readOnly placeholder="—" /></div>
                </div>
              </div>
              <div id="panel-profile-access" role="tabpanel" aria-labelledby="tab-profile-access" hidden={profileTab !== "access"}>
                {profileError ? <p className="form-error" role="alert">{profileError}</p>
                  : <section className="ci-picker" aria-labelledby="held-title">
                    <div className="ci-picker-heading"><div><h3 id="held-title">Access held</h3></div><span className="ci-selection-count">{held.length} {held.length === 1 ? "item" : "items"}</span></div>
                    {held.length === 0 ? <p className="ci-empty">No roles or entitlements are held yet.</p>
                      : <ul className="raci-chips" aria-label="Access held">{held.map((holding) => <li className="raci-chip" key={`${holding.kind}:${holding.id}`}><span><strong>{holding.name ?? holding.id}</strong><small>{holding.id} · {titleCase(holding.kind)} · {heldVia[holding.via]}{holding.source ? ` ${holding.source}` : ""}</small></span></li>)}</ul>}
                  </section>}
              </div>
            </>}
        </form>
      </section>
    </section>

    <section className="records-section" aria-label="Open requests">
      <div className="ci-picker-heading saved-requests-heading"><div><h3>{user.superAdmin ? "Open requests on the platform" : "Your open requests"}</h3></div><span className="ci-selection-count">{loading ? "" : requestTotal > defaultPageSize ? `showing ${requests.length} of ${requestTotal.toLocaleString()} open` : `${requests.length} open`}</span></div>
      <div className="table-frame">
        <table>
          <thead><tr><th>Identifier</th><th>Name</th><th>Form</th><th>Submitted</th><th>Status</th><th>Actions</th></tr></thead>
          <tbody>{requests.map((record) => {
            const state = requestStateOf(record);
            const requestedFor = record.relationships?.find((relationship) => relationship.kind === "requested-for" && relationship.status !== "retired");
            return <tr key={record.id}>
              <td><span className="identifier">{record.id}</span></td>
              <td><div className="primary-value">{nodeDisplayName(record) ?? record.id}</div>{requestedFor && <div className="secondary-value">for {requestedFor.toName ?? requestedFor.toId}</div>}</td>
              <td>{formTitles[requestTypeOf(record)]}</td>
              <td><div className="secondary-value">{formatWhen(typeof record.properties?.submittedAt === "string" ? record.properties.submittedAt : undefined) || "—"}</div></td>
              <td><span className={`status-pill ${requestStatePillClass(state)}`}>{state}</span>{record.properties?.slaEnabled ? <div className="secondary-value">SLA {String(record.properties.slaBusinessDays)} business days{typeof record.properties.slaDueAt === "string" ? ` · ${record.properties.slaDueAt}` : ""}</div> : null}</td>
              <td><div className="row-actions"><button className="action-button" onClick={() => onOpenRequest(record)} title="Open request"><ClipboardList size={14} /><span>Open</span></button></div></td>
            </tr>;
          })}</tbody>
        </table>
        {!loading && requests.length === 0 && <div className="empty-state"><span className="empty-rule" /><strong>{requestsError || "No open requests"}</strong><p>{requestsError ? "" : user.superAdmin ? "No catalog request is in progress." : "Requests you raise, or that are raised for you, appear here until they finish."}</p></div>}
      </div>
    </section>

    <section className="records-section" aria-label="Work waiting">
      <div className="ci-picker-heading saved-requests-heading"><div><h3>{user.superAdmin ? "Open tasks on the platform" : "Work waiting on you"}</h3><button type="button" className="action-button" onClick={onOpenTasks} title="Open Workflow Tasks"><ClipboardList size={13} /><span>All tasks</span></button></div><span className="ci-selection-count">{loading ? "" : `${tasks.length} pending · ${approvals} ${approvals === 1 ? "approval" : "approvals"}`}</span></div>
      <div className="table-frame">
        <table>
          <thead><tr><th>Identifier</th><th>Request</th><th>Step</th><th>Assigned to</th><th>Raised</th><th>Agreement</th><th>Status</th><th>Actions</th></tr></thead>
          <tbody>{tasks.map((task) => <tr key={task.id}>
            <td><span className="identifier">{task.id}</span></td>
            <td><div className="primary-value">{nodeDisplayName(task.request) ?? task.request.id}</div><div className="secondary-value">{task.request.id} · {task.requestedFor ? `for ${task.requestedFor.name ?? task.requestedFor.id}` : task.requester ? `for ${task.requester.name ?? task.requester.id}` : "requester unknown"}</div></td>
            <td><div className="primary-value">{task.step.order}. {task.step.name}{task.item ? ` · ${task.item.name ?? task.item.id}` : ""}</div><div className="secondary-value">{titleCase(task.step.stepType)} · {task.workflowName}</div></td>
            <td><div className="secondary-value">{task.assignees.map((assignee) => assignee.name ?? assignee.id).join(", ") || "—"}</div></td>
            <td><div className="secondary-value">{formatWhen(task.createdAt)}</div></td>
            <td><div className="agreement-stack"><AgreementMeter clock={task.sla} compact /><AgreementMeter clock={task.ola} compact /></div></td>
            <td><span className={`status-pill ${taskStatusPillClass(task.status)}`}>{task.status}</span></td>
            <td><div className="row-actions"><button className="action-button" onClick={() => setSelected(task)} title="Open task"><ClipboardList size={14} /><span>Open</span></button></div></td>
          </tr>)}</tbody>
        </table>
        {!loading && tasks.length === 0 && <div className="empty-state"><span className="empty-rule" /><strong>{tasksError || "Nothing waiting"}</strong><p>{tasksError ? "" : user.superAdmin ? "No workflow task is pending anywhere on the platform." : "Tasks appear here when a submitted form reaches a step assigned to you or your groups."}</p></div>}
      </div>
    </section>

    {selected && <TaskDialog key={selected.id} task={selected} actorId={actorId} metadata={metadata} onClose={() => setSelected(null)} onOpenRequest={onOpenRequest} onDone={(message) => { setSelected(null); onNotify(message); setListReload((value) => value + 1); }} />}
  </>;
}

const weekdayLabels = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];

function HolidayCalendar({ onNotify, reload }: { onNotify: (message: string) => void; reload: number }) {
  const today = new Date();
  const [cursor, setCursor] = useState({ year: today.getFullYear(), month: today.getMonth() });
  const [holidays, setHolidays] = useState<Holiday[]>([]);
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    let active = true;
    getHolidays().then((items) => { if (active) { setHolidays(items); setError(""); } }).catch((err: unknown) => {
      if (active) setError(err instanceof Error ? err.message : "Could not load holidays.");
    });
    return () => { active = false; };
  }, [reload]);

  const first = new Date(cursor.year, cursor.month, 1);
  const startWeekday = (first.getDay() + 6) % 7; // Monday-first
  const daysInMonth = new Date(cursor.year, cursor.month + 1, 0).getDate();
  const cells = Array.from({ length: startWeekday + daysInMonth }, (_, index) => index < startWeekday ? null : index - startWeekday + 1);
  const marked = new Map(holidays.map((holiday) => [holiday.date, holiday]));
  const title = first.toLocaleString([], { month: "long", year: "numeric" });

  async function toggle(day: number) {
    const date = `${cursor.year}-${String(cursor.month + 1).padStart(2, "0")}-${String(day).padStart(2, "0")}`;
    const weekday = new Date(cursor.year, cursor.month, day).getDay();
    if (weekday === 0 || weekday === 6) return;
    const next = marked.has(date) ? holidays.filter((holiday) => holiday.date !== date) : [...holidays, { date, ...(name.trim() ? { name: name.trim() } : {}) }].sort((a, b) => a.date.localeCompare(b.date));
    setSaving(true);
    try {
      setHolidays(await saveHolidays(next));
      setError("");
      onNotify(marked.has(date) ? `${date} is a business day again.` : `${date} marked as a holiday.`);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : "Could not save holidays.");
    } finally {
      setSaving(false);
    }
  }

  return <section className="holiday-calendar" aria-labelledby="holiday-title">
    <div className="ci-picker-heading"><div><h3 id="holiday-title">Holiday calendar</h3></div><span className="ci-selection-count">{holidays.length} marked</span></div>
    <p className="field-hint">Click a weekday to mark the business closed. Weekends are already skipped by SLA and OLA clocks. Optional name is applied to the next day you mark.</p>
    <div className="holiday-toolbar">
      <button type="button" className="action-button" onClick={() => setCursor((current) => current.month === 0 ? { year: current.year - 1, month: 11 } : { year: current.year, month: current.month - 1 })} aria-label="Previous month"><ChevronLeft size={14} /></button>
      <strong>{title}</strong>
      <button type="button" className="action-button" onClick={() => setCursor((current) => current.month === 11 ? { year: current.year + 1, month: 0 } : { year: current.year, month: current.month + 1 })} aria-label="Next month"><ChevronRight size={14} /></button>
      <div className="form-field holiday-name"><label htmlFor="holiday-name">Name for next mark</label><input id="holiday-name" value={name} autoComplete="off" placeholder="e.g. Thanksgiving" onChange={(event) => setName(event.target.value)} /></div>
    </div>
    <div className="holiday-grid" aria-label={title}>
      {weekdayLabels.map((label) => <span className="holiday-weekday" key={label}>{label}</span>)}
      {cells.map((day, index) => {
        if (day === null) return <span key={`pad-${index}`} />;
        const date = `${cursor.year}-${String(cursor.month + 1).padStart(2, "0")}-${String(day).padStart(2, "0")}`;
        const holiday = marked.get(date);
        const weekend = [0, 6].includes(new Date(cursor.year, cursor.month, day).getDay());
        return <button type="button" key={date} className={`holiday-day${holiday ? " marked" : ""}${weekend ? " weekend" : ""}`} disabled={saving || weekend} onClick={() => void toggle(day)} title={holiday?.name || (weekend ? "Weekend" : date)}>
          <span>{day}</span>
          {holiday?.name && <small>{holiday.name}</small>}
        </button>;
      })}
    </div>
    {error && <p className="form-error" role="alert">{error}</p>}
  </section>;
}
