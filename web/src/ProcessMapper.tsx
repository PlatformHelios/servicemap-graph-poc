import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Background,
  BackgroundVariant,
  Controls,
  Handle,
  MarkerType,
  Position,
  ReactFlow,
  applyNodeChanges,
  type Edge,
  type Node,
  type NodeChange,
  type NodeProps,
  type OnNodeDrag,
} from "@xyflow/react";
import { ArrowDown, ArrowUp, Plus, Save, Search, Ticket, Trash2, X } from "lucide-react";
import { createNode, getNode, getNodePage, getNodes, getProcessMap, nodeDisplayName, saveProcessMap } from "./api";
import RecordPicker, { pickerSubtitle, titleCase } from "./RecordPicker";
import type {
  ExecutionModel,
  GraphNode,
  Metadata,
  OutputAction,
  ProcessMap,
  ProcessMapPerformer,
  ProcessMapStep,
  TriggerAction,
} from "./types";
import "@xyflow/react/dist/style.css";

const processSearchLimit = 12;
const stepGapX = 280;
const defaultNodeY = 140;
const startNodeX = 40;
const firstStepX = 360;

const fallbackTriggerActions: TriggerAction[] = [
  "email-received", "file-delivered", "work-item-queued", "form-submitted", "manual-start", "schedule",
  "alert-raised", "approval-granted", "upstream-completed", "status-changed", "customer-contact", "payment-received",
];
const fallbackOutputActions: OutputAction[] = [
  "record-created", "record-updated", "file-produced", "notification-sent", "work-item-created", "decision-recorded",
  "access-granted", "change-applied", "process-started", "queue-cleared", "artifact-published", "payment-issued",
];

function ActionChecklist({
  id,
  label,
  options,
  selected,
  readOnly,
  onChange,
}: {
  id: string;
  label: string;
  options: string[];
  selected: string[];
  readOnly?: boolean;
  onChange: (next: string[]) => void;
}) {
  function toggle(value: string) {
    if (readOnly) return;
    onChange(selected.includes(value) ? selected.filter((item) => item !== value) : [...selected, value]);
  }
  return (
    <fieldset className="process-mapper-actions" disabled={readOnly}>
      <legend>{label}</legend>
      <ul className="process-mapper-action-list" id={id}>
        {options.map((option) => {
          const inputId = `${id}-${option}`;
          return (
            <li key={option}>
              <label htmlFor={inputId}>
                <input
                  id={inputId}
                  type="checkbox"
                  checked={selected.includes(option)}
                  disabled={readOnly}
                  onChange={() => toggle(option)}
                />
                <span>{titleCase(option)}</span>
              </label>
            </li>
          );
        })}
      </ul>
    </fieldset>
  );
}

// Server-backed process finder: type a name or ID; only matching Process CIs are listed.
function ProcessSearch({
  selected,
  onSelect,
}: {
  selected: GraphNode | null;
  onSelect: (process: GraphNode | null) => void;
}) {
  const [query, setQuery] = useState("");
  const [debounced, setDebounced] = useState("");
  const [open, setOpen] = useState(false);
  const [matches, setMatches] = useState<GraphNode[]>([]);
  const [searching, setSearching] = useState(false);

  useEffect(() => {
    const timer = window.setTimeout(() => setDebounced(query.trim()), 250);
    return () => window.clearTimeout(timer);
  }, [query]);

  useEffect(() => {
    if (!debounced) {
      setMatches([]);
      setSearching(false);
      return;
    }
    let active = true;
    setSearching(true);
    getNodePage("ci", { q: debounced, limit: 40, includeRetired: false }).then((page) => {
      if (!active) return;
      setMatches(page.items.filter((node) => node.properties?.ciType === "process" && node.status === "active").slice(0, processSearchLimit));
    }).catch(() => {
      if (active) setMatches([]);
    }).finally(() => {
      if (active) setSearching(false);
    });
    return () => { active = false; };
  }, [debounced]);

  function pick(node: GraphNode) {
    onSelect(node);
    setQuery("");
    setOpen(false);
  }

  return (
    <div className="process-mapper-process-search">
      {selected ? (
        <ul className="raci-chips" aria-label="Selected process">
          <li className="raci-chip">
            <span><strong>{nodeDisplayName(selected) ?? selected.id}</strong><small>{pickerSubtitle(selected)}</small></span>
            <button type="button" aria-label={`Clear ${nodeDisplayName(selected) ?? selected.id}`} onClick={() => onSelect(null)}><X size={13} /></button>
          </li>
        </ul>
      ) : (
        <div className="raci-finder" onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false); }}>
          <label className="search-box raci-search process-mapper-search-box">
            <Search aria-hidden="true" size={15} />
            <input
              id="process-map-search"
              value={query}
              type="search"
              autoComplete="off"
              role="combobox"
              aria-label="Search processes"
              aria-expanded={open && debounced.length > 0}
              aria-controls="process-map-search-matches"
              placeholder="Search processes by name or ID"
              onChange={(event) => { setQuery(event.target.value); setOpen(true); }}
              onFocus={() => setOpen(true)}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  event.preventDefault();
                  if (matches[0]) pick(matches[0]);
                } else if (event.key === "Escape" && query) {
                  event.stopPropagation();
                  setQuery("");
                  setOpen(false);
                }
              }}
            />
          </label>
          {open && debounced && (
            <ul className="raci-matches" id="process-map-search-matches" role="listbox">
              {searching
                ? <li className="raci-no-match">Searching…</li>
                : matches.length === 0
                  ? <li className="raci-no-match">No active processes match “{debounced}”.</li>
                  : matches.map((node) => (
                    <li key={node.id} role="option" aria-selected={false}>
                      <button type="button" className="raci-match" onMouseDown={(event) => event.preventDefault()} onClick={() => pick(node)}>
                        <strong>{nodeDisplayName(node) ?? node.id}</strong><small>{pickerSubtitle(node)}</small>
                      </button>
                    </li>
                  ))}
            </ul>
          )}
        </div>
      )}
    </div>
  );
}

type Stage = "process" | "start" | "steps" | "outputs";

type StepDraft = ProcessMapStep & { uiKey: number };

const guidance: Record<Stage, { title: string; body: string }> = {
  process: {
    title: "Choose the business process",
    body: "Pick an existing Process CI, or type a new process name. One Save writes the Process CI (if new) and the full map—triggers, steps, performers, and outputs.",
  },
  start: {
    title: "What kicks this off?",
    body: "Pick one or more trigger actions from the catalog (email received, form submitted, schedule, and so on), then add a short note in workshop language. Triggers are events, not CI links.",
  },
  steps: {
    title: "Map the steps",
    body: "Add ordered steps left to right. Each step is stored as a Process Step CI (manual or automated). Name it in business language, say who or what performs it, then reorder as needed.",
  },
  outputs: {
    title: "What does this produce?",
    body: "Pick outcome actions from the catalog (record created, notification sent, and so on), then note them in plain language. Outputs are process-level events, not CI links.",
  },
};

let nextStepKey = 1;
function newStep(): StepDraft {
  return {
    uiKey: nextStepKey++,
    name: "",
    description: "",
    execution: "manual",
    createsCase: false,
    olaEnabled: false,
    order: 1,
    performers: [],
  };
}

function draftFromMap(map: ProcessMap): {
  name: string;
  description: string;
  triggerDescription: string;
  triggerActions: TriggerAction[];
  outputDescription: string;
  outputActions: OutputAction[];
  steps: StepDraft[];
} {
  return {
    name: map.name ?? "",
    description: map.description ?? "",
    triggerDescription: map.triggerDescription ?? "",
    triggerActions: (map.triggerActions ?? []) as TriggerAction[],
    outputDescription: map.outputDescription ?? "",
    outputActions: (map.outputActions ?? []) as OutputAction[],
    steps: (map.steps ?? []).map((step, index) => ({
      ...step,
      uiKey: nextStepKey++,
      order: step.order || index + 1,
      performers: step.performers ?? [],
      execution: step.execution === "automated" ? "automated" : "manual",
      createsCase: Boolean(step.createsCase),
      olaEnabled: Boolean(step.olaEnabled),
      olaBusinessDays: step.olaBusinessDays,
    })),
  };
}

interface ProcessNodeData extends Record<string, unknown> {
  label: string;
  detail: string;
  tone: "process" | "start" | "step" | "end";
}

function MapperNode({ data }: NodeProps<Node<ProcessNodeData>>) {
  return (
    <div className={`process-mapper-node tone-${data.tone}`}>
      <Handle type="target" position={Position.Left} />
      <strong>{data.label}</strong>
      <span>{data.detail}</span>
      <Handle type="source" position={Position.Right} />
    </div>
  );
}

const nodeTypes = { mapper: MapperNode };

export default function ProcessMapper({
  metadata,
  reload,
  onNotify,
  initialProcessId,
  canUpdate,
}: {
  metadata: Metadata | null;
  reload: number;
  onNotify: (message: string) => void;
  initialProcessId?: string | null;
  canUpdate: boolean;
}) {
  const [stage, setStage] = useState<Stage>("process");
  const [cis, setCIs] = useState<GraphNode[]>([]);
  const [people, setPeople] = useState<GraphNode[]>([]);
  const [groups, setGroups] = useState<GraphNode[]>([]);
  const [selectedProcess, setSelectedProcess] = useState<GraphNode | null>(null);
  const [processId, setProcessId] = useState(initialProcessId ?? "");
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [triggerDescription, setTriggerDescription] = useState("");
  const [triggerActions, setTriggerActions] = useState<TriggerAction[]>([]);
  const [outputDescription, setOutputDescription] = useState("");
  const [outputActions, setOutputActions] = useState<OutputAction[]>([]);
  const [steps, setSteps] = useState<StepDraft[]>([]);
  const [selectedStepKey, setSelectedStepKey] = useState<number | null>(null);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [raising, setRaising] = useState(false);
  const [error, setError] = useState("");
  const [newProcessName, setNewProcessName] = useState("");
  const [flowNodes, setFlowNodes] = useState<Node<ProcessNodeData>[]>([]);
  const [flowEdges, setFlowEdges] = useState<Edge[]>([]);
  const [anchorPositions, setAnchorPositions] = useState<{ start?: { x: number; y: number }; end?: { x: number; y: number } }>({});

  const triggerOptions = metadata?.triggerActions?.length ? metadata.triggerActions : fallbackTriggerActions;
  const outputOptions = metadata?.outputActions?.length ? metadata.outputActions : fallbackOutputActions;

  useEffect(() => {
    let active = true;
    Promise.all([
      getNodes("ci", false),
      getNodes("identity", false),
      getNodes("group", false),
    ]).then(([allCIs, identities, groupNodes]) => {
      if (!active) return;
      setCIs(allCIs);
      setPeople(identities.filter((node) => node.status === "active"));
      setGroups(groupNodes.filter((node) => node.status === "active"));
    }).catch((loadError: unknown) => {
      if (active) setError(loadError instanceof Error ? loadError.message : "Could not load records.");
    });
    return () => { active = false; };
  }, [reload]);

  const loadMap = useCallback((id: string, processNode?: GraphNode | null) => {
    if (!id) return;
    setLoading(true);
    setError("");
    Promise.all([
      getProcessMap(id),
      processNode ? Promise.resolve(processNode) : getNode("ci", id, false).catch(() => null),
    ]).then(([map, node]) => {
      const draft = draftFromMap(map);
      setProcessId(map.processId);
      setSelectedProcess(node ?? {
        id: map.processId,
        kind: "ci",
        status: "active",
        properties: {
          ciType: "process",
          ...(map.name ? { name: map.name } : {}),
          ...(map.description ? { description: map.description } : {}),
        },
      });
      setName(draft.name);
      setDescription(draft.description);
      setTriggerDescription(draft.triggerDescription);
      setTriggerActions(draft.triggerActions);
      setOutputDescription(draft.outputDescription);
      setOutputActions(draft.outputActions);
      setSteps(draft.steps);
      setSelectedStepKey(draft.steps[0]?.uiKey ?? null);
      setStage(draft.steps.length ? "steps" : "start");
    }).catch((loadError: unknown) => {
      setError(loadError instanceof Error ? loadError.message : "Could not load process map.");
    }).finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    if (initialProcessId) loadMap(initialProcessId);
  }, [initialProcessId, loadMap]);

  function selectProcess(process: GraphNode | null) {
    if (!process) {
      setSelectedProcess(null);
      setProcessId("");
      setName("");
      setDescription("");
      setTriggerDescription("");
      setTriggerActions([]);
      setOutputDescription("");
      setOutputActions([]);
      setSteps([]);
      setSelectedStepKey(null);
      setStage("process");
      return;
    }
    loadMap(process.id, process);
  }

  const applicationOptions = useMemo(
    () => cis.filter((node) => node.properties?.ciType === "application"),
    [cis],
  );
  const automationOptions = useMemo(
    () => cis.filter((node) => node.properties?.ciType === "automation"),
    [cis],
  );
  const selectedStep = steps.find((step) => step.uiKey === selectedStepKey) ?? null;
  const stepIsAutomated = selectedStep?.execution === "automated";
  const performerOptions = useMemo(
    () => (stepIsAutomated ? automationOptions : [...people, ...groups, ...applicationOptions]),
    [stepIsAutomated, people, groups, applicationOptions, automationOptions],
  );
  const hasAutomationPerformer = stepIsAutomated
    || (selectedStep?.performers ?? []).some((performer) => performer.kind === "automation");

  function setStepExecution(uiKey: number, execution: ExecutionModel) {
    const step = steps.find((item) => item.uiKey === uiKey);
    if (!step) return;
    const performers = (step.performers ?? []).filter((performer) => (
      execution === "automated" ? performer.kind === "automation" : performer.kind !== "automation"
    ));
    updateStep(uiKey, {
      execution,
      performers,
      ...(execution === "automated" ? { createsCase: false } : {}),
    });
  }

  const defaultEndX = firstStepX + Math.max(steps.length, 1) * stepGapX;

  useEffect(() => {
    setAnchorPositions({});
  }, [processId]);

  useEffect(() => {
    const startDetail = triggerDescription.trim()
      || (triggerActions.length ? triggerActions.map((action) => titleCase(action)).join(", ") : "No trigger yet");
    const endDetail = outputDescription.trim()
      || (outputActions.length ? outputActions.map((action) => titleCase(action)).join(", ") : "No outputs yet");
    const nodes: Node<ProcessNodeData>[] = [
      {
        id: "start",
        type: "mapper",
        position: anchorPositions.start ?? { x: startNodeX, y: defaultNodeY },
        data: { label: "Start", detail: startDetail, tone: "start" },
        draggable: canUpdate,
      },
    ];
    for (const [index, step] of steps.entries()) {
      const hasPosition = typeof step.x === "number" && typeof step.y === "number";
      nodes.push({
        id: `step-${step.uiKey}`,
        type: "mapper",
        position: hasPosition
          ? { x: step.x as number, y: step.y as number }
          : { x: firstStepX + index * stepGapX, y: defaultNodeY },
        data: {
          label: step.name.trim() || `Step ${index + 1}`,
          detail: [
            (step.performers ?? []).length
              ? (step.performers ?? []).map((performer) => performer.name || performer.id).join(", ")
              : "No performer",
            step.createsCase ? "Allows case creation" : "",
            step.olaEnabled ? `OLA ${step.olaBusinessDays || 5}d` : "",
          ].filter(Boolean).join(" · "),
          tone: "step",
        },
        draggable: canUpdate,
      });
    }
    nodes.push({
      id: "end",
      type: "mapper",
      position: anchorPositions.end ?? { x: defaultEndX, y: defaultNodeY },
      data: { label: "Outputs", detail: endDetail, tone: "end" },
      draggable: canUpdate,
    });
    const edgeStyle = { stroke: "var(--brand-blue)", strokeWidth: 2 };
    const marker = { type: MarkerType.ArrowClosed, width: 18, height: 18 };
    const edges: Edge[] = [];
    if (steps.length === 0) {
      edges.push({ id: "start-end", source: "start", target: "end", markerEnd: marker, style: edgeStyle });
    } else {
      edges.push({ id: "start-step0", source: "start", target: `step-${steps[0].uiKey}`, markerEnd: marker, style: edgeStyle });
      for (let i = 0; i + 1 < steps.length; i++) {
        edges.push({
          id: `step-${steps[i].uiKey}-${steps[i + 1].uiKey}`,
          source: `step-${steps[i].uiKey}`,
          target: `step-${steps[i + 1].uiKey}`,
          markerEnd: marker,
          style: edgeStyle,
        });
      }
      edges.push({
        id: "step-end",
        source: `step-${steps[steps.length - 1].uiKey}`,
        target: "end",
        markerEnd: marker,
        style: edgeStyle,
      });
    }
    setFlowNodes(nodes);
    setFlowEdges(edges);
  }, [steps, triggerDescription, triggerActions, outputDescription, outputActions, anchorPositions, defaultEndX, canUpdate]);

  const onNodesChange = useCallback((changes: NodeChange<Node<ProcessNodeData>>[]) => {
    setFlowNodes((current) => applyNodeChanges(changes, current));
  }, []);

  const onNodeDragStop = useCallback<OnNodeDrag<Node<ProcessNodeData>>>((_event, node) => {
    if (node.id === "start") {
      setAnchorPositions((current) => ({ ...current, start: node.position }));
      return;
    }
    if (node.id === "end") {
      setAnchorPositions((current) => ({ ...current, end: node.position }));
      return;
    }
    if (!node.id.startsWith("step-")) return;
    const uiKey = Number(node.id.slice("step-".length));
    if (!Number.isFinite(uiKey)) return;
    setSteps((current) => current.map((step) => (
      step.uiKey === uiKey ? { ...step, x: node.position.x, y: node.position.y } : step
    )));
  }, []);

  function updateStep(uiKey: number, patch: Partial<StepDraft>) {
    setSteps((current) => current.map((step) => (step.uiKey === uiKey ? { ...step, ...patch } : step)));
  }

  function moveStep(uiKey: number, direction: -1 | 1) {
    setSteps((current) => {
      const index = current.findIndex((step) => step.uiKey === uiKey);
      const next = index + direction;
      if (index < 0 || next < 0 || next >= current.length) return current;
      const copy = [...current];
      [copy[index], copy[next]] = [copy[next], copy[index]];
      return copy.map((step, order) => ({ ...step, order: order + 1 }));
    });
  }

  async function persist() {
    if (steps.some((step) => !step.name.trim())) {
      setError("Every step needs a name.");
      setStage("steps");
      return;
    }
    const processName = (processId ? name : newProcessName || name).trim();
    if (!processId && !processName) {
      setError("Name the new process, or pick an existing one, before saving.");
      setStage("process");
      return;
    }
    setSaving(true);
    setError("");
    try {
      let activeProcessId = processId;
      let createdNew = false;
      if (!activeProcessId) {
        const created = await createNode("ci", {
          properties: { ciType: "process", name: processName, category: "business", description: description.trim() },
        });
        createdNew = true;
        activeProcessId = created.id;
        setCIs((current) => [...current, created]);
        setSelectedProcess(created);
        setProcessId(created.id);
        setName(processName);
        setNewProcessName("");
      }
      const payload: ProcessMap = {
        processId: activeProcessId,
        name: (name.trim() || processName),
        description: description.trim(),
        triggerActions,
        triggerDescription: triggerDescription.trim(),
        outputActions,
        outputDescription: outputDescription.trim(),
        steps: steps.map((step, index) => ({
          ...(step.id ? { id: step.id } : {}),
          name: step.name.trim(),
          description: step.description?.trim() || "",
          execution: step.execution,
          createsCase: Boolean(step.createsCase),
          olaEnabled: Boolean(step.olaEnabled),
          ...(step.olaEnabled ? { olaBusinessDays: Number(step.olaBusinessDays) || 5 } : {}),
          order: index + 1,
          ...(typeof step.x === "number" ? { x: step.x } : {}),
          ...(typeof step.y === "number" ? { y: step.y } : {}),
          performers: (step.performers ?? []).map(({ kind, id }) => ({ kind, id })),
        })),
      };
      const saved = await saveProcessMap(activeProcessId, payload);
      const draft = draftFromMap(saved);
      setName(draft.name);
      setDescription(draft.description);
      setTriggerDescription(draft.triggerDescription);
      setTriggerActions(draft.triggerActions);
      setOutputDescription(draft.outputDescription);
      setOutputActions(draft.outputActions);
      const previousId = steps.find((step) => step.uiKey === selectedStepKey)?.id;
      setSteps(draft.steps);
      setSelectedStepKey((draft.steps.find((step) => step.id && step.id === previousId) ?? draft.steps[0])?.uiKey ?? null);
      onNotify(createdNew
        ? `Created process and saved map for ${saved.name || saved.processId}.`
        : `Saved process map for ${saved.name || saved.processId}.`);
    } catch (saveError: unknown) {
      setError(saveError instanceof Error ? saveError.message : "Could not save process map.");
    } finally {
      setSaving(false);
    }
  }

  function setStepPerformers(uiKey: number, ids: string[]) {
    const performers: ProcessMapPerformer[] = ids.map((id) => {
      const person = people.find((node) => node.id === id);
      if (person) return { kind: "identity" as const, id, name: nodeDisplayName(person) };
      const group = groups.find((node) => node.id === id);
      if (group) return { kind: "group" as const, id, name: nodeDisplayName(group) };
      const automation = automationOptions.find((node) => node.id === id);
      if (automation) return { kind: "automation" as const, id, name: nodeDisplayName(automation) };
      const app = applicationOptions.find((node) => node.id === id);
      return { kind: "application" as const, id, name: nodeDisplayName(app ?? { id, kind: "ci", status: "active", properties: {} }) };
    });
    const clearsCase = performers.some((performer) => performer.kind === "automation");
    updateStep(uiKey, { performers, ...(clearsCase ? { createsCase: false } : {}) });
  }

  async function raiseCase(step: StepDraft) {
    if (!step.id) {
      setError("Save the map before raising a case from this step.");
      return;
    }
    if (!step.createsCase) {
      setError("Tick Allow case creation on this step and save the map first.");
      return;
    }
    setRaising(true);
    setError("");
    try {
      const created = await createNode("case", { properties: {}, ciIds: [step.id] });
      onNotify(`Raised case ${created.id} for ${step.name.trim() || step.id}.`);
    } catch (raiseError: unknown) {
      setError(raiseError instanceof Error ? raiseError.message : "Could not raise a case from this step.");
    } finally {
      setRaising(false);
    }
  }

  const coach = guidance[stage];

  return (
    <div className="process-mapper">
      <aside className="process-mapper-guide" aria-label="Workshop guidance">
        <p className="eyebrow">GUIDANCE</p>
        <h2>{coach.title}</h2>
        <p>{coach.body}</p>
        <ol className="process-mapper-stages">
          {(["process", "start", "steps", "outputs"] as Stage[]).map((item) => (
            <li key={item}>
              <button type="button" className={stage === item ? "active" : ""} onClick={() => setStage(item)}>
                {titleCase(item === "start" ? "Start" : item)}
              </button>
            </li>
          ))}
        </ol>
        {!canUpdate && <p className="field-hint">You can view this map; saving requires process-map or configuration-item update rights.</p>}
      </aside>

      <div className="process-mapper-main">
        <div className="process-mapper-toolbar">
          <div className="process-mapper-process-pick">
            <ProcessSearch selected={selectedProcess} onSelect={selectProcess} />
          </div>
          {canUpdate && !processId && (
            <div className="process-mapper-create">
              <input
                value={newProcessName}
                onChange={(event) => {
                  setNewProcessName(event.target.value);
                  setName(event.target.value);
                }}
                placeholder="New process name"
                aria-label="New process name"
              />
            </div>
          )}
          {canUpdate && (
            <button
              className="button button-primary icon-button"
              type="button"
              disabled={saving || loading || (!processId && !newProcessName.trim() && !name.trim())}
              onClick={() => void persist()}
            >
              <Save size={14} />{saving ? "Saving…" : processId ? "Save map" : "Create & save map"}
            </button>
          )}
        </div>

        {error && <p className="form-error" role="alert">{error}</p>}
        {loading && <p className="field-hint">Loading process map…</p>}

        {stage === "process" && (
          <section className="process-mapper-panel" aria-label="Process details">
            <div className="form-field">
              <label htmlFor="process-map-name">Process name</label>
              <input
                id="process-map-name"
                value={name}
                disabled={!canUpdate}
                onChange={(event) => {
                  setName(event.target.value);
                  if (!processId) setNewProcessName(event.target.value);
                }}
              />
            </div>
            <div className="form-field">
              <label htmlFor="process-map-description">Description</label>
              <textarea id="process-map-description" rows={3} value={description} disabled={!canUpdate} onChange={(event) => setDescription(event.target.value)} />
            </div>
          </section>
        )}

        {stage === "start" && (
          <section className="process-mapper-panel" aria-label="Process start">
            <ActionChecklist
              id="process-trigger-actions"
              label="Trigger actions"
              options={triggerOptions}
              selected={triggerActions}
              readOnly={!canUpdate}
              onChange={(next) => setTriggerActions(next as TriggerAction[])}
            />
            <div className="form-field">
              <label htmlFor="process-trigger-note">Trigger description</label>
              <textarea
                id="process-trigger-note"
                rows={3}
                value={triggerDescription}
                disabled={!canUpdate}
                placeholder="e.g. Customer submits a support ticket"
                onChange={(event) => setTriggerDescription(event.target.value)}
              />
            </div>
          </section>
        )}

        {stage === "steps" && (
          <section className="process-mapper-panel process-mapper-steps" aria-label="Process steps">
            <div className="process-mapper-step-list">
              <div className="ci-picker-heading">
                <div><h3>Steps</h3></div>
                {canUpdate && (
                  <button
                    className="button button-quiet icon-button"
                    type="button"
                    onClick={() => {
                      const step = newStep();
                      setSteps((current) => [...current, { ...step, order: current.length + 1 }]);
                      setSelectedStepKey(step.uiKey);
                    }}
                  >
                    <Plus size={14} />Add step
                  </button>
                )}
              </div>
              {steps.length === 0 ? <p className="field-hint">No steps yet. Add the first action in this process.</p> : (
                <ul>
                  {steps.map((step, index) => (
                    <li key={step.uiKey}>
                      <button type="button" className={selectedStepKey === step.uiKey ? "active" : ""} onClick={() => setSelectedStepKey(step.uiKey)}>
                        <span className="process-mapper-step-order">{index + 1}</span>
                        <span>{step.name.trim() || "Untitled step"}</span>
                      </button>
                      {canUpdate && (
                        <span className="process-mapper-step-actions">
                          <button type="button" className="icon-button" aria-label="Move up" onClick={() => moveStep(step.uiKey, -1)}><ArrowUp size={14} /></button>
                          <button type="button" className="icon-button" aria-label="Move down" onClick={() => moveStep(step.uiKey, 1)}><ArrowDown size={14} /></button>
                          <button
                            type="button"
                            className="icon-button"
                            aria-label="Remove step"
                            onClick={() => {
                              setSteps((current) => current.filter((item) => item.uiKey !== step.uiKey).map((item, order) => ({ ...item, order: order + 1 })));
                              if (selectedStepKey === step.uiKey) setSelectedStepKey(null);
                            }}
                          >
                            <Trash2 size={14} />
                          </button>
                        </span>
                      )}
                    </li>
                  ))}
                </ul>
              )}
            </div>
            {selectedStep && (
              <div className="process-mapper-step-editor">
                <div className="form-field">
                  <label htmlFor="step-name">Step name</label>
                  <input id="step-name" value={selectedStep.name} disabled={!canUpdate} onChange={(event) => updateStep(selectedStep.uiKey, { name: event.target.value })} />
                </div>
                <div className="form-field">
                  <label htmlFor="step-description">Brief description</label>
                  <textarea id="step-description" rows={2} value={selectedStep.description ?? ""} disabled={!canUpdate} onChange={(event) => updateStep(selectedStep.uiKey, { description: event.target.value })} />
                </div>
                <div className="form-field">
                  <label htmlFor="step-execution">Execution</label>
                  <select id="step-execution" value={selectedStep.execution} disabled={!canUpdate} onChange={(event) => setStepExecution(selectedStep.uiKey, event.target.value as ExecutionModel)}>
                    <option value="manual">Manual</option>
                    <option value="automated">Automated</option>
                  </select>
                </div>
                <RecordPicker
                  id={`step-performers-${selectedStep.uiKey}`}
                  label="Performed by"
                  hint={stepIsAutomated
                    ? "Automated steps are performed by Automation CIs (stored as uses)."
                    : "Identities and groups use responsible; applications use uses."}
                  placeholder={stepIsAutomated
                    ? "Find an automation"
                    : "Find a person, group, or application"}
                  options={performerOptions}
                  selected={(selectedStep.performers ?? []).map((performer) => performer.id)}
                  readOnly={!canUpdate}
                  onChange={(ids) => setStepPerformers(selectedStep.uiKey, ids)}
                />
                <label className="retired-toggle" htmlFor="step-creates-case">
                  <input
                    id="step-creates-case"
                    type="checkbox"
                    checked={Boolean(selectedStep.createsCase) && !hasAutomationPerformer}
                    disabled={!canUpdate || hasAutomationPerformer}
                    onChange={(event) => updateStep(selectedStep.uiKey, { createsCase: event.target.checked })}
                  />
                  <span>Allow case creation</span>
                </label>
                <p className="field-hint">{hasAutomationPerformer
                  ? "Unavailable for automated steps."
                  : "Does not create a case automatically. When checked, a Case can be raised for this step (API, Raise case, or Cases / Tickets) and is assigned to Performed by."}</p>
                <label className="retired-toggle" htmlFor="step-ola-enabled">
                  <input
                    id="step-ola-enabled"
                    type="checkbox"
                    checked={Boolean(selectedStep.olaEnabled)}
                    disabled={!canUpdate}
                    onChange={(event) => updateStep(selectedStep.uiKey, { olaEnabled: event.target.checked, olaBusinessDays: selectedStep.olaBusinessDays || 5 })}
                  />
                  <span>Measure this step against an OLA</span>
                </label>
                {selectedStep.olaEnabled && (
                  <div className="form-field">
                    <label htmlFor="step-ola-days">OLA business days</label>
                    <input
                      id="step-ola-days"
                      type="number"
                      min={1}
                      max={365}
                      value={selectedStep.olaBusinessDays || 5}
                      disabled={!canUpdate}
                      onChange={(event) => updateStep(selectedStep.uiKey, { olaBusinessDays: Number(event.target.value) || 5 })}
                    />
                  </div>
                )}
                {selectedStep.createsCase && !hasAutomationPerformer && (
                  <button className="button button-quiet icon-button" type="button" disabled={raising || !selectedStep.id} onClick={() => void raiseCase(selectedStep)}>
                    <Ticket size={14} />{raising ? "Raising…" : selectedStep.id ? "Raise case" : "Save map to raise a case"}
                  </button>
                )}
              </div>
            )}
          </section>
        )}

        {stage === "outputs" && (
          <section className="process-mapper-panel" aria-label="Process outputs">
            <ActionChecklist
              id="process-output-actions"
              label="Output actions"
              options={outputOptions}
              selected={outputActions}
              readOnly={!canUpdate}
              onChange={(next) => setOutputActions(next as OutputAction[])}
            />
            <div className="form-field">
              <label htmlFor="process-output-note">Output description</label>
              <textarea
                id="process-output-note"
                rows={3}
                value={outputDescription}
                disabled={!canUpdate}
                placeholder="e.g. Vendor record created and requester notified"
                onChange={(event) => setOutputDescription(event.target.value)}
              />
            </div>
          </section>
        )}

        <div className="process-mapper-canvas" aria-label="Process flow canvas">
          <ReactFlow
            nodes={flowNodes}
            edges={flowEdges}
            nodeTypes={nodeTypes}
            fitView
            fitViewOptions={{ padding: 0.2, maxZoom: 1 }}
            onNodesChange={onNodesChange}
            onNodeDragStop={onNodeDragStop}
            nodesDraggable={canUpdate}
            nodesConnectable={false}
            elementsSelectable={canUpdate}
            panOnDrag
            zoomOnScroll
            minZoom={0.35}
            maxZoom={1.5}
            proOptions={{ hideAttribution: true }}
          >
            <Background variant={BackgroundVariant.Dots} gap={18} size={1} color="var(--line)" />
            <Controls showInteractive={false} />
          </ReactFlow>
        </div>
      </div>
    </div>
  );
}
