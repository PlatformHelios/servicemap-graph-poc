import { useEffect, useState } from "react";
import { Archive, ArrowDown, ArrowUp, Pencil, Plus, Trash2, X } from "lucide-react";
import { getNodes, getWorkflows, retireWorkflow, saveWorkflow } from "./api";
import RecordPicker, { titleCase } from "./RecordPicker";
import { formTitles, requestFieldLabel, requestFieldLabels } from "./workflows";
import { businessDayChoices } from "./AgreementMeter";
import type { ApprovalRule, GraphNode, Metadata, RequestType, StepType, WorkflowDefinition, WorkflowStepDefinition } from "./types";

// A step as edited on screen. The key keeps React rows stable while steps are
// reordered; it is stripped before the definition is sent to the API.
type StepDraft = WorkflowStepDefinition & { uiKey: number };
type WorkflowDraft = Omit<WorkflowDefinition, "steps"> & { steps: StepDraft[] };

const stepTypeHints: Record<StepType, string> = {
  review: "Assignees look the form over, fill in the fields exposed here, and mark the step complete.",
  approval: "Assignees approve or return the request; the rule decides how many approvals the step needs.",
};

// The access request workflow has a fixed shape: owner approval per item, then
// fulfilment. Only who fulfils (and the wording) is open for editing.
const accessWorkflowFallback: WorkflowDefinition = { name: "Access Request Workflow", requestType: "access", enabled: false, steps: [
  { order: 1, name: "Owner approval", stepType: "approval", approvalRule: "any", assigneeRule: "item-accountable", instructions: "Approve or deny the requested role or entitlement for this identity.", editableFields: [], requiredFields: [], assigneeIds: [] },
  { order: 2, name: "Fulfilment", stepType: "review", instructions: "Provision the approved role or entitlement, then mark the task complete to record the permission.", editableFields: [], requiredFields: [], assigneeIds: [] },
] };

let nextStepKey = 1;
function newStep(): StepDraft {
  return { uiKey: nextStepKey++, name: "", stepType: "review", approvalRule: "any", instructions: "", editableFields: [], requiredFields: [], assigneeIds: [], olaEnabled: false, olaBusinessDays: 5 };
}
function newWorkflow(requestType: RequestType, metadata: Metadata | null): WorkflowDraft {
  if (requestType === "access") return draftFrom({ ...(metadata?.accessWorkflowTemplate ?? accessWorkflowFallback), id: undefined, status: undefined, enabled: false });
  return { name: "", description: "", requestType, enabled: false, steps: [newStep()] };
}
function draftFrom(workflow: WorkflowDefinition): WorkflowDraft {
  return { ...workflow, steps: workflow.steps.map((step) => ({ ...step, uiKey: nextStepKey++, editableFields: step.editableFields ?? [], requiredFields: step.requiredFields ?? [], assigneeIds: step.assigneeIds ?? step.assignees?.map((assignee) => assignee.id) ?? [] })) };
}
function definitionFrom(draft: WorkflowDraft): WorkflowDefinition {
  return {
    ...(draft.id ? { id: draft.id } : {}),
    name: draft.name.trim(),
    ...(draft.description?.trim() ? { description: draft.description.trim() } : {}),
    requestType: draft.requestType,
    enabled: draft.enabled,
    steps: draft.steps.map((step, index) => ({
      ...(step.id ? { id: step.id } : {}),
      order: index + 1,
      name: step.name.trim(),
      stepType: step.stepType,
      ...(step.instructions?.trim() ? { instructions: step.instructions.trim() } : {}),
      ...(step.stepType === "approval" ? { approvalRule: step.approvalRule ?? "any" } : {}),
      editableFields: step.editableFields ?? [],
      requiredFields: step.requiredFields ?? [],
      // A step with an assignee rule has no named assignees; the rule picks them per task.
      ...(step.assigneeRule ? { assigneeRule: step.assigneeRule, assigneeIds: [] } : { assigneeIds: step.assigneeIds }),
      olaEnabled: Boolean(step.olaEnabled),
      ...(step.olaEnabled && step.olaBusinessDays ? { olaBusinessDays: step.olaBusinessDays } : {}),
    })),
    slaEnabled: Boolean(draft.slaEnabled),
    ...(draft.slaEnabled && draft.slaBusinessDays ? { slaBusinessDays: draft.slaBusinessDays } : {}),
  };
}

export default function WorkflowCreator({ metadata, reload, onNotify }: { metadata: Metadata | null; reload: number; onNotify: (message: string) => void }) {
  const requestTypes = metadata?.requestTypes ?? ["vendor"];
  const stepTypes = metadata?.stepTypes ?? ["review", "approval"];
  const approvalRules = metadata?.approvalRules ?? ["any", "all"];
  const [workflows, setWorkflows] = useState<WorkflowDefinition[]>([]);
  const [includeRetired, setIncludeRetired] = useState(false);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [people, setPeople] = useState<GraphNode[]>([]);
  const [peopleError, setPeopleError] = useState("");
  const [draft, setDraft] = useState<WorkflowDraft>(() => newWorkflow(requestTypes[0], metadata));
  const [formError, setFormError] = useState("");
  const [saving, setSaving] = useState(false);
  const [retiring, setRetiring] = useState<WorkflowDefinition | null>(null);
  const [listReload, setListReload] = useState(0);
  const editing = Boolean(draft.id);
  // The access workflow is a system workflow: its steps are fixed, and only the
  // fulfilment step's assignees (plus names and instructions) can be changed.
  const locked = draft.requestType === "access";
  // Catalog approval workflows are generated when a team publishes a catalog
  // item; they are shown here for reference and change only by republishing.
  const generated = draft.requestType === "catalog";
  const formChoices = requestTypes.includes(draft.requestType) ? requestTypes : [...requestTypes, draft.requestType];
  const fieldKeys = metadata?.requestFields?.[draft.requestType] ?? Object.keys(requestFieldLabels[draft.requestType] ?? {});
  const fulfilmentKeys = metadata?.fulfilmentFields?.[draft.requestType] ?? [];
  const uncollected = fulfilmentKeys.filter((key) => !draft.steps.some((step) => step.requiredFields?.includes(key)));
  const otherEnabled = workflows.find((workflow) => workflow.enabled && workflow.status !== "retired" && workflow.requestType === draft.requestType && workflow.id !== draft.id);

  // Retired workflows are fetched too so the list can say they exist when
  // nothing active is left; the toggle only decides whether they are shown.
  useEffect(() => {
    let active = true;
    setLoading(true);
    getWorkflows(true).then((items) => {
      if (!active) return;
      setWorkflows(items);
      setLoadError("");
    }).catch((error: unknown) => {
      if (active) setLoadError(error instanceof Error ? error.message : "Could not load workflows.");
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [reload, listReload]);
  const retiredCount = workflows.filter((workflow) => workflow.status === "retired").length;
  const shownWorkflows = includeRetired ? workflows : workflows.filter((workflow) => workflow.status !== "retired");

  // A retired workflow cannot change; copying it starts a new one with the same steps.
  function copyWorkflow() {
    setDraft((current) => ({ ...current, id: undefined, status: undefined, enabled: false, steps: current.steps.map((step) => ({ ...step, id: undefined, order: undefined })) }));
    setFormError("");
  }

  useEffect(() => {
    let active = true;
    Promise.all([getNodes("identity", false), getNodes("group", false)]).then(([identities, groups]) => {
      if (active) setPeople([...identities, ...groups]);
    }).catch((error: unknown) => {
      if (active) setPeopleError(error instanceof Error ? error.message : "Could not load identities and groups.");
    });
    return () => { active = false; };
  }, [reload]);

  function updateStep(uiKey: number, change: Partial<StepDraft>) {
    setDraft((current) => ({ ...current, steps: current.steps.map((step) => step.uiKey === uiKey ? { ...step, ...change } : step) }));
  }
  function moveStep(index: number, offset: number) {
    setDraft((current) => {
      const steps = [...current.steps];
      const target = index + offset;
      if (target < 0 || target >= steps.length) return current;
      [steps[index], steps[target]] = [steps[target], steps[index]];
      return { ...current, steps };
    });
  }
  // Required implies editable: ticking Required also ticks Editable, and clearing Editable clears Required.
  function toggleField(step: StepDraft, key: string, list: "editableFields" | "requiredFields", checked: boolean) {
    const editable = new Set(step.editableFields ?? []);
    const required = new Set(step.requiredFields ?? []);
    if (list === "requiredFields") {
      if (checked) { required.add(key); editable.add(key); } else required.delete(key);
    } else if (checked) editable.add(key); else { editable.delete(key); required.delete(key); }
    const order = (keys: Set<string>) => fieldKeys.filter((candidate) => keys.has(candidate));
    updateStep(step.uiKey, { editableFields: order(editable), requiredFields: order(required) });
  }

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!draft.name.trim()) { setFormError("Give the workflow a name."); return; }
    if (draft.steps.length === 0) { setFormError("Add at least one step."); return; }
    for (const [index, step] of draft.steps.entries()) {
      if (!step.name.trim()) { setFormError(`Name step ${index + 1}.`); return; }
      if (!step.assigneeRule && step.assigneeIds.length === 0) { setFormError(locked ? "Choose who fulfils approved access: assign the Fulfilment step to a group or identity." : `Assign step ${index + 1} (${step.name.trim()}) to at least one identity or group.`); return; }
    }
    if (draft.enabled && uncollected.length > 0) { setFormError(`An enabled workflow must require ${uncollected.map((key) => requestFieldLabel(draft.requestType, key)).join(" and ")} at one of its steps, so the record can be created when the last step completes.`); return; }
    if (draft.slaEnabled && !(draft.slaBusinessDays && draft.slaBusinessDays > 0)) { setFormError("Choose how many business days the SLA allows."); return; }
    for (const [index, step] of draft.steps.entries()) {
      if (step.olaEnabled && !(step.olaBusinessDays && step.olaBusinessDays > 0)) { setFormError(`Choose how many business days the OLA allows for step ${index + 1}.`); return; }
    }
    if (draft.enabled && otherEnabled) { setFormError(`${otherEnabled.name} (${otherEnabled.id}) is already enabled for the ${formTitles[draft.requestType] ?? draft.requestType}. Disable it first, or save this workflow disabled.`); return; }
    setFormError("");
    setSaving(true);
    try {
      const saved = await saveWorkflow(definitionFrom(draft));
      setDraft(draftFrom(saved));
      onNotify(editing ? "Workflow saved." : "Workflow created.");
      setListReload((value) => value + 1);
    } catch (error) {
      setFormError(error instanceof Error ? error.message : "Could not save the workflow.");
    } finally {
      setSaving(false);
    }
  }

  async function confirmRetire() {
    if (!retiring?.id) return;
    try {
      await retireWorkflow(retiring.id);
      if (draft.id === retiring.id) setDraft(newWorkflow(requestTypes[0], metadata));
      setRetiring(null);
      onNotify("Workflow retired.");
      setListReload((value) => value + 1);
    } catch (error) {
      onNotify(error instanceof Error ? error.message : "Could not retire the workflow.");
    }
  }

  const retired = draft.status === "retired";

  return <>
    <section className="records-section request-form-panel" aria-label="Workflow Creator">
      <section className="editor-dialog editor-inline workflow-editor" aria-labelledby="dialog-title">
        <form onSubmit={submit}>
          <div className="dialog-heading">
            <div><p className="eyebrow">{editing ? `${retired ? "RETIRED " : ""}WORKFLOW · ${draft.id}` : "NEW WORKFLOW"}</p><h2 id="dialog-title">{editing ? draft.name || "Workflow" : "Create a workflow"}</h2></div>
            {editing && <button className="close-button" type="button" onClick={() => { setDraft(newWorkflow(requestTypes[0], metadata)); setFormError(""); }} aria-label="Start a new workflow" title="Start a new workflow"><X size={17} /></button>}
          </div>
          {!editing && !loading && workflows.length > 0 && <p className="system-generated-id">{workflows.length - retiredCount} saved {workflows.length - retiredCount === 1 ? "workflow" : "workflows"}{retiredCount > 0 ? ` and ${retiredCount} retired` : ""} are listed below this form. <button type="button" className="link-button" onClick={() => document.querySelector(".saved-workflows")?.scrollIntoView({ behavior: "smooth", block: "start" })}>Jump to saved workflows</button> to reopen one.</p>}
          {retired && <p className="system-generated-id">This workflow is retired, so it cannot be changed or enabled. <button type="button" className="link-button" onClick={copyWorkflow}>Create a copy</button> to continue from its steps as a new workflow.</p>}
          {generated && <p className="system-generated-id">This approval workflow was generated from a Service Catalog item when its owning team published it. It is read-only here: the team changes it by publishing a new version of the item, which replaces this workflow and disables it for new requests.</p>}
          <fieldset className="plain-fieldset" disabled={generated}>
          {locked && !retired && <p className="system-generated-id">The Access Request Form runs a system workflow: every requested role or entitlement goes to its Accountable owner, and approved items go to the fulfilment team. The steps are fixed; choose who fulfils access at step 2 and enable the workflow so requests can be submitted.</p>}
          <div className="field-grid">
            <div className="form-field"><label htmlFor="workflow-form">Catalog form</label>
              <select id="workflow-form" value={draft.requestType} disabled={editing} onChange={(event) => { const requestType = event.target.value as RequestType; setDraft((current) => requestType === "access" || current.requestType === "access" ? newWorkflow(requestType, metadata) : { ...current, requestType }); setFormError(""); }}>{formChoices.map((type) => <option key={type} value={type}>{formTitles[type] ?? titleCase(type)}</option>)}</select>
              <span className="field-hint">{editing ? "The form a workflow serves is fixed once it is created." : "Submitting this form starts the workflow when it is enabled."}</span></div>
            <div className="form-field"><label htmlFor="workflow-name">Workflow name</label><input id="workflow-name" autoComplete="off" value={draft.name} onChange={(event) => setDraft((current) => ({ ...current, name: event.target.value }))} /></div>
            <div className="form-field full-width"><label htmlFor="workflow-description">Description</label><textarea id="workflow-description" rows={2} value={draft.description ?? ""} onChange={(event) => setDraft((current) => ({ ...current, description: event.target.value }))} /></div>
            <div className="form-field full-width">
              <label className="retired-toggle workflow-enabled"><input type="checkbox" checked={draft.enabled} onChange={(event) => setDraft((current) => ({ ...current, enabled: event.target.checked }))} /><span>Enabled</span></label>
              <span className="field-hint">One workflow per form can be enabled at a time. {otherEnabled ? `${otherEnabled.name} (${otherEnabled.id}) is enabled for this form now.` : "No other workflow is enabled for this form."}{fulfilmentKeys.length > 0 && ` An enabled workflow must require ${fulfilmentKeys.map((key) => requestFieldLabel(draft.requestType, key)).join(" and ")} at some step${uncollected.length > 0 ? " — not yet required by any step" : " — covered"}.`}</span>
            </div>
            <div className="form-field full-width">
              <label className="retired-toggle workflow-enabled"><input type="checkbox" checked={Boolean(draft.slaEnabled)} disabled={retired} onChange={(event) => setDraft((current) => ({ ...current, slaEnabled: event.target.checked, slaBusinessDays: current.slaBusinessDays || 5 }))} /><span>Measure this form against an SLA</span></label>
              {draft.slaEnabled && <>
                <label htmlFor="workflow-sla-days">Business days to complete</label>
                <select id="workflow-sla-days" value={draft.slaBusinessDays || 5} disabled={retired} onChange={(event) => setDraft((current) => ({ ...current, slaBusinessDays: Number(event.target.value) }))}>
                  {(draft.slaBusinessDays && !businessDayChoices.includes(draft.slaBusinessDays) ? [draft.slaBusinessDays, ...businessDayChoices] : businessDayChoices).map((choice) => <option key={choice} value={choice}>{choice} business day{choice === 1 ? "" : "s"}</option>)}
                </select>
                <span className="field-hint">Monday–Friday, skipping days marked as holidays on the platform calendar. Catalog forms show this SLA; requesters cannot change it.</span>
              </>}
            </div>
          </div>

          <section className="ci-picker raci-section" aria-labelledby="steps-title">
            <div className="ci-picker-heading"><div><h3 id="steps-title">Steps</h3><span className="required-mark">Required</span></div><span className="ci-selection-count">{draft.steps.length} {draft.steps.length === 1 ? "step" : "steps"} · {locked ? "fixed for this form" : "run in order"}</span></div>
            {peopleError && <p className="form-error" role="alert">{peopleError}</p>}
            <ol className="workflow-steps">{draft.steps.map((step, index) => <li className="workflow-step" key={step.uiKey}>
              <div className="workflow-step-head">
                <span className="workflow-step-number">{index + 1}</span>
                <div className="form-field"><label htmlFor={`step-name-${step.uiKey}`}>Step name</label><input id={`step-name-${step.uiKey}`} autoComplete="off" placeholder="e.g. Procurement review" value={step.name} onChange={(event) => updateStep(step.uiKey, { name: event.target.value })} /></div>
                <div className="form-field"><label htmlFor={`step-type-${step.uiKey}`}>Step type</label><select id={`step-type-${step.uiKey}`} value={step.stepType} disabled={locked} onChange={(event) => updateStep(step.uiKey, { stepType: event.target.value as StepType })}>{stepTypes.map((type) => <option key={type} value={type}>{titleCase(type)}</option>)}</select></div>
                {!locked && <div className="workflow-step-tools">
                  <button type="button" className="action-button" onClick={() => moveStep(index, -1)} disabled={index === 0} aria-label={`Move step ${index + 1} up`} title="Move up"><ArrowUp size={14} /></button>
                  <button type="button" className="action-button" onClick={() => moveStep(index, 1)} disabled={index === draft.steps.length - 1} aria-label={`Move step ${index + 1} down`} title="Move down"><ArrowDown size={14} /></button>
                  <button type="button" className="action-button retire" onClick={() => setDraft((current) => ({ ...current, steps: current.steps.filter((item) => item.uiKey !== step.uiKey) }))} aria-label={`Remove step ${index + 1}`} title="Remove step"><Trash2 size={14} /></button>
                </div>}
              </div>
              <p className="field-hint">{step.assigneeRule === "item-accountable" ? "One task per requested role or entitlement, each approved or denied by that item's Accountable owner." : locked ? "One task per approved item for the fulfilment team; completing it records the permission on the identity." : stepTypeHints[step.stepType]}{step.id ? ` Saved as ${step.id}.` : ""}</p>
              <div className="field-grid">
                {step.assigneeRule === "item-accountable"
                  ? <div className="form-field"><label>Assigned to <span className="ci-selection-count">by rule</span></label><input value="Accountable of each requested role or entitlement" readOnly /><span className="field-hint">Set on each role's or entitlement's RACI tab. Items without an Accountable owner are not offered on the form.</span></div>
                  : <RecordPicker id={`step-assignees-${step.uiKey}`} label="Assigned to" hint={locked ? "The group or identities that provision approved access, e.g. the identity team's fulfilment group." : "Groups or specific identities. Any member of an assigned group may act."} placeholder="Find an identity or group by name or ID" options={people} selected={step.assigneeIds} onChange={(ids) => updateStep(step.uiKey, { assigneeIds: ids })} />}
                {step.stepType === "approval" && <div className="form-field"><label htmlFor={`step-rule-${step.uiKey}`}>Approval rule</label>
                  <select id={`step-rule-${step.uiKey}`} value={step.approvalRule ?? "any"} disabled={locked} onChange={(event) => updateStep(step.uiKey, { approvalRule: event.target.value as ApprovalRule })}>{approvalRules.map((rule) => <option key={rule} value={rule}>{rule === "any" ? "Any one approver" : "All eligible approvers"}</option>)}</select>
                  <span className="field-hint">{step.approvalRule === "all" ? "Every assigned identity and group member must approve." : "The first approval completes the step."}</span></div>}
                <div className="form-field full-width"><label htmlFor={`step-instructions-${step.uiKey}`}>Instructions</label><textarea id={`step-instructions-${step.uiKey}`} rows={2} placeholder="What the assignees should check or add" value={step.instructions ?? ""} onChange={(event) => updateStep(step.uiKey, { instructions: event.target.value })} /></div>
                <div className="form-field full-width">
                  <label className="retired-toggle"><input type="checkbox" checked={Boolean(step.olaEnabled)} disabled={retired} onChange={(event) => updateStep(step.uiKey, { olaEnabled: event.target.checked, olaBusinessDays: step.olaBusinessDays || 2 })} /><span>Measure this step against an OLA</span></label>
                  {step.olaEnabled && <div className="form-field"><label htmlFor={`step-ola-${step.uiKey}`}>Business days to complete this task</label>
                    <select id={`step-ola-${step.uiKey}`} value={step.olaBusinessDays || 2} disabled={retired} onChange={(event) => updateStep(step.uiKey, { olaBusinessDays: Number(event.target.value) })}>
                      {(step.olaBusinessDays && !businessDayChoices.includes(step.olaBusinessDays) ? [step.olaBusinessDays, ...businessDayChoices] : businessDayChoices).map((choice) => <option key={choice} value={choice}>{choice} business day{choice === 1 ? "" : "s"}</option>)}
                    </select>
                    <span className="field-hint">Counted from when the task is raised, Monday–Friday, skipping platform holidays.</span></div>}
                </div>
              </div>
              {fieldKeys.length > 0 && <div className="workflow-fields">
                <div className="workflow-fields-head"><span>Form fields at this step</span><span>Editable</span><span>Required</span></div>
                {fieldKeys.map((key) => <div className="workflow-field-row" key={key}>
                  <span>{requestFieldLabel(draft.requestType, key)}{fulfilmentKeys.includes(key) && <small> · collected by the workflow</small>}</span>
                  <input type="checkbox" aria-label={`${requestFieldLabel(draft.requestType, key)} editable at step ${index + 1}`} checked={step.editableFields?.includes(key) ?? false} onChange={(event) => toggleField(step, key, "editableFields", event.target.checked)} />
                  <input type="checkbox" aria-label={`${requestFieldLabel(draft.requestType, key)} required at step ${index + 1}`} checked={step.requiredFields?.includes(key) ?? false} onChange={(event) => toggleField(step, key, "requiredFields", event.target.checked)} />
                </div>)}
              </div>}
            </li>)}</ol>
            {!locked && <button className="attribute-add" type="button" onClick={() => setDraft((current) => ({ ...current, steps: [...current.steps, newStep()] }))}><Plus size={14} />Add step</button>}
          </section>
          </fieldset>

          {formError && <p className="form-error" role="alert">{formError}</p>}
          <div className="dialog-actions">
            {editing && <button className="button button-quiet" type="button" onClick={() => { setDraft(newWorkflow(requestTypes[0], metadata)); setFormError(""); }}>{generated ? "Close" : "Discard changes"}</button>}
            {editing && !retired && !generated && <button className="button button-quiet" type="button" onClick={() => setRetiring(definitionFrom(draft))}><Archive size={14} />Retire workflow</button>}
            {!retired && !generated && <button className="button button-primary" type="submit" disabled={saving}>{saving ? "Saving…" : editing ? "Save workflow" : "Create workflow"}</button>}
          </div>
        </form>
      </section>
    </section>

    <section className="records-section saved-workflows" aria-label="Saved workflows">
      <div className="ci-picker-heading saved-requests-heading"><div><h3>Saved workflows</h3></div><span className="ci-selection-count">Reopen a workflow to change its steps. Retired workflows can be copied into a new one.</span></div>
      <div className="records-toolbar"><span className="secondary-value">{loading ? "Loading workflows…" : `${shownWorkflows.length} ${shownWorkflows.length === 1 ? "workflow" : "workflows"}${!includeRetired && retiredCount > 0 ? ` · ${retiredCount} retired hidden` : ""}`}</span><label className="retired-toggle"><input type="checkbox" checked={includeRetired} onChange={(event) => setIncludeRetired(event.target.checked)} /><span>Include retired</span></label></div>
      <div className="table-frame">
        <table>
          <thead><tr><th>Identifier</th><th>Name</th><th>Form</th><th>Steps</th><th>Enabled</th><th>Status</th><th>Actions</th></tr></thead>
          <tbody>{shownWorkflows.map((workflow) => <tr key={workflow.id}>
            <td><span className="identifier">{workflow.id}</span></td>
            <td><div className="primary-value">{workflow.name}</div>{workflow.description && <div className="secondary-value">{workflow.description}</div>}</td>
            <td>{formTitles[workflow.requestType] ?? titleCase(workflow.requestType)}</td>
            <td><div className="relationship-list">{workflow.steps.map((step) => <div className="relationship-line" key={step.id ?? step.order}><strong>{step.order}. {titleCase(step.stepType)}</strong> → {step.name} · {step.assigneeRule === "item-accountable" ? "accountable of each item" : (step.assignees ?? []).map((assignee) => assignee.name ?? assignee.id).join(", ") || "unassigned"}</div>)}</div></td>
            <td>{workflow.enabled ? <span className="status-pill status-active">enabled</span> : <span className="status-pill status-draft">disabled</span>}</td>
            <td><span className={`status-pill status-${workflow.status ?? "active"}`}>{workflow.status ?? "active"}</span></td>
            <td><div className="row-actions">
              <button className="action-button" onClick={() => { setDraft(draftFrom(workflow)); setFormError(""); document.querySelector(".request-form-panel")?.scrollIntoView({ behavior: "smooth", block: "start" }); }} title={workflow.status === "retired" || workflow.requestType === "catalog" ? "Open workflow" : "Edit workflow"}><Pencil size={14} /><span>{workflow.status === "retired" || workflow.requestType === "catalog" ? "Open" : "Edit"}</span></button>
              {workflow.status !== "retired" && workflow.requestType !== "catalog" && <button className="action-button retire" onClick={() => setRetiring(workflow)} title="Retire workflow"><Archive size={14} /><span>Retire</span></button>}
            </div></td>
          </tr>)}</tbody>
        </table>
        {!loading && shownWorkflows.length === 0 && <div className="empty-state"><span className="empty-rule" /><strong>{loadError || (retiredCount > 0 ? "No active workflows" : "No workflows yet")}</strong><p>{loadError ? "" : retiredCount > 0 ? `${retiredCount} retired ${retiredCount === 1 ? "workflow is" : "workflows are"} hidden — tick Include retired to open and copy ${retiredCount === 1 ? "it" : "one"}.` : "Create one above; it starts when a form it serves is submitted."}</p></div>}
      </div>
    </section>

    {retiring && <div className="modal-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) setRetiring(null); }}>
      <section className="editor-dialog compact-dialog" role="dialog" aria-modal="true" aria-labelledby="retire-workflow-title">
        <div className="dialog-heading"><div><p className="eyebrow">LIFECYCLE</p><h2 id="retire-workflow-title">Retire workflow?</h2></div><button className="close-button" type="button" onClick={() => setRetiring(null)} aria-label="Close"><X size={17} /></button></div>
        <p className="confirm-copy">{retiring.name} ({retiring.id}) and its steps will be marked retired. Requests already moving through it finish their current run; new submissions will no longer start it.</p>
        <div className="dialog-actions"><button className="button button-quiet" type="button" onClick={() => setRetiring(null)}>Cancel</button><button className="button button-danger" type="button" onClick={confirmRetire}>Retire</button></div>
      </section>
    </div>}
  </>;
}
