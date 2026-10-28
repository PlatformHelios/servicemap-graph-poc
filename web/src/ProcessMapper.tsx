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
import { ArrowDown, ArrowUp, Download, Plus, Save, Search, Ticket, Trash2, X } from "lucide-react";
import { createNode, getNode, getNodePage, getNodes, getProcessMap, getProcessMapMermaid, nodeDisplayName, saveProcessMap } from "./api";
import RecordPicker, { pickerSubtitle, titleCase } from "./RecordPicker";
import type {
  ExecutionModel,
  GraphNode,
  Metadata,
  OutputAction,
  ProcessMap,
  ProcessMapDependency,
  ProcessMapPerformer,
  ProcessMapRef,
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
const triggerCITypes = new Set(["application", "process", "function", "service"]);

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
    getNodePage("ci", { q: debounced, limit: 40, includeRetired: false, includeDraft: true }).then((page) => {
      if (!active) return;
      setMatches(page.items.filter((node) => node.properties?.ciType === "process" && (node.status === "active" || node.status === "draft")).slice(0, processSearchLimit));
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
                        <strong>{nodeDisplayName(node) ?? node.id}</strong><small>{pickerSubtitle(node)}{node.status === "draft" ? " · draft" : ""}</small>
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

type StepDependency = { kind: "process-start" } | { kind: "step"; uiKey: number };

type StepDraft = Omit<ProcessMapStep, "dependsOn"> & { uiKey: number; dependsOn: StepDependency[] };

function linearDependencies(steps: StepDraft[]): StepDraft[] {
  if (steps.length === 0 || steps.some((step) => step.dependsOn.length > 0)) return steps;
  return steps.map((step, index) => ({
    ...step,
    dependsOn: index === 0
      ? [{ kind: "process-start" }]
      : [{ kind: "step", uiKey: steps[index - 1].uiKey }],
  }));
}

function keepEarlierDependencies(steps: StepDraft[]): StepDraft[] {
  const indexOf = new Map(steps.map((step, index) => [step.uiKey, index]));
  return steps.map((step, index) => ({
    ...step,
    order: index + 1,
    dependsOn: step.dependsOn.filter((dep) => {
      if (dep.kind === "process-start") return true;
      const depIndex = indexOf.get(dep.uiKey);
      return depIndex !== undefined && depIndex < index;
    }),
  }));
}

function dependenciesFromMap(steps: StepDraft[], source: ProcessMapStep[]): StepDraft[] {
  const byId = new Map(steps.filter((step) => step.id).map((step) => [step.id as string, step.uiKey]));
  const mapped = steps.map((step, index) => {
    const dependsOn: StepDependency[] = [];
    for (const dep of source[index]?.dependsOn ?? []) {
      if (dep.kind === "process-start") {
        dependsOn.push({ kind: "process-start" });
        continue;
      }
      const uiKey = dep.id ? byId.get(dep.id) : steps.find((item) => item.order === dep.order)?.uiKey;
      if (uiKey !== undefined && uiKey !== step.uiKey) dependsOn.push({ kind: "step", uiKey });
    }
    return { ...step, dependsOn };
  });
  return linearDependencies(mapped);
}

const guidance: Record<Stage, { title: string; body: string }> = {
  process: {
    title: "Choose the business process",
    body: "Pick an existing Process CI, or type a new process name. One Save writes the Process CI (if new) and the full map—triggers, steps, performers, and outputs.",
  },
  start: {
    title: "What kicks this off?",
    body: "Pick the trigger actions, then the configuration items that start this process (an application, process, function, or service). Use + if that CI does not exist yet.",
  },
  steps: {
    title: "Map the steps",
    body: "Add ordered steps left to right. Each step is stored as a Process Step CI (manual or automated). Name it, say who performs it, and choose what it depends on. Process Start runs steps together from the beginning. Depending on an earlier step makes this step wait for that work.",
  },
  outputs: {
    title: "What does this produce?",
    body: "Pick the outcome actions, then the configuration items this process produces. Use + if that CI does not exist yet.",
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
    dependsOn: [],
  };
}

function draftFromMap(map: ProcessMap): {
  name: string;
  description: string;
  triggerDescription: string;
  triggerActions: TriggerAction[];
  triggerIds: string[];
  outputDescription: string;
  outputActions: OutputAction[];
  outputIds: string[];
  steps: StepDraft[];
} {
  return {
    name: map.name ?? "",
    description: map.description ?? "",
    triggerDescription: map.triggerDescription ?? "",
    triggerActions: (map.triggerActions ?? []) as TriggerAction[],
    triggerIds: (map.triggers ?? []).map((item) => item.id),
    outputDescription: map.outputDescription ?? "",
    outputActions: (map.outputActions ?? []) as OutputAction[],
    outputIds: (map.outputs ?? []).map((item) => item.id),
    steps: dependenciesFromMap((map.steps ?? []).map((step, index) => ({
      ...step,
      uiKey: nextStepKey++,
      order: step.order || index + 1,
      performers: step.performers ?? [],
      dependsOn: [],
      execution: step.execution === "automated" ? "automated" : "manual",
      createsCase: Boolean(step.createsCase),
      olaEnabled: Boolean(step.olaEnabled),
      olaBusinessDays: step.olaBusinessDays,
    })), map.steps ?? []),
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
  canCreateCI,
  canCreateGroup,
  onCreateRecord,
  createdRecord,
  onCreatedRecordHandled,
}: {
  metadata: Metadata | null;
  reload: number;
  onNotify: (message: string) => void;
  initialProcessId?: string | null;
  canUpdate: boolean;
  canCreateCI?: boolean;
  canCreateGroup?: boolean;
  onCreateRecord?: (kind: "ci" | "group", defaults?: { ciType?: string }) => void;
  createdRecord?: GraphNode | null;
  onCreatedRecordHandled?: () => void;
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
  const [triggerIds, setTriggerIds] = useState<string[]>([]);
  const [outputDescription, setOutputDescription] = useState("");
  const [outputActions, setOutputActions] = useState<OutputAction[]>([]);
  const [outputIds, setOutputIds] = useState<string[]>([]);
  const [pendingCreate, setPendingCreate] = useState<"trigger" | "output" | "group" | null>(null);
  const [steps, setSteps] = useState<StepDraft[]>([]);
  const [selectedStepKey, setSelectedStepKey] = useState<number | null>(null);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [raising, setRaising] = useState(false);
  const [exporting, setExporting] = useState(false);
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
      getNodes("ci", false, true),
      getNodes("identity", false, true),
      getNodes("group", false, true),
    ]).then(([allCIs, identities, groupNodes]) => {
      if (!active) return;
      setCIs(allCIs);
      setPeople(identities.filter((node) => node.status === "active" || node.status === "draft"));
      setGroups(groupNodes.filter((node) => node.status === "active" || node.status === "draft"));
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
      setTriggerIds(draft.triggerIds);
      setOutputDescription(draft.outputDescription);
      setOutputActions(draft.outputActions);
      setOutputIds(draft.outputIds);
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
      setTriggerIds([]);
      setOutputDescription("");
      setOutputActions([]);
      setOutputIds([]);
      setSteps([]);
      setSelectedStepKey(null);
      setStage("process");
      return;
    }
    loadMap(process.id, process);
  }

  const rememberRecord = useCallback((node: GraphNode) => {
    if (node.kind === "ci") setCIs((current) => current.some((item) => item.id === node.id) ? current : [...current, node]);
    if (node.kind === "group") setGroups((current) => current.some((item) => item.id === node.id) ? current : [...current, node]);
    if (node.kind === "identity") setPeople((current) => current.some((item) => item.id === node.id) ? current : [...current, node]);
  }, []);

  useEffect(() => {
    if (!createdRecord || !pendingCreate) return;
    rememberRecord(createdRecord);
    if (pendingCreate === "trigger") {
      const ciType = String(createdRecord.properties?.ciType ?? "");
      if (createdRecord.kind !== "ci" || !triggerCITypes.has(ciType)) {
        setError("A trigger must be an application, process, function, or service.");
      } else {
        setTriggerIds((current) => current.includes(createdRecord.id) ? current : [...current, createdRecord.id]);
        setError("");
      }
    } else if (pendingCreate === "output") {
      if (createdRecord.kind !== "ci") {
        setError("An output must be a configuration item.");
      } else if (createdRecord.id === processId) {
        setError("A process cannot produce itself.");
      } else {
        setOutputIds((current) => current.includes(createdRecord.id) ? current : [...current, createdRecord.id]);
        setError("");
      }
    } else if (pendingCreate === "group") {
      if (createdRecord.kind !== "group") {
        setError("Create a group to add it to Performed by.");
      } else if (selectedStepKey != null) {
        setSteps((current) => current.map((step) => {
          if (step.uiKey !== selectedStepKey || step.execution === "automated") return step;
          const performers = step.performers ?? [];
          if (performers.some((performer) => performer.id === createdRecord.id)) return step;
          return { ...step, performers: [...performers, { kind: "group" as const, id: createdRecord.id, name: nodeDisplayName(createdRecord) }] };
        }));
        setError("");
      }
    }
    setPendingCreate(null);
    onCreatedRecordHandled?.();
  }, [createdRecord, pendingCreate, processId, rememberRecord, selectedStepKey, onCreatedRecordHandled]);

  const applicationOptions = useMemo(
    () => cis.filter((node) => node.properties?.ciType === "application"),
    [cis],
  );
  const automationOptions = useMemo(
    () => cis.filter((node) => node.properties?.ciType === "automation"),
    [cis],
  );
  const triggerCIOptions = useMemo(
    () => cis.filter((node) => node.status !== "retired" && node.id !== processId && triggerCITypes.has(String(node.properties?.ciType ?? ""))),
    [cis, processId],
  );
  const outputCIOptions = useMemo(
    () => cis.filter((node) => node.status !== "retired" && node.id !== processId && node.properties?.ciType !== "process-step"),
    [cis, processId],
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
    const triggerNames = triggerIds.map((id) => nodeDisplayName(cis.find((node) => node.id === id) ?? { id, kind: "ci", status: "active" })).filter(Boolean);
    const outputNames = outputIds.map((id) => nodeDisplayName(cis.find((node) => node.id === id) ?? { id, kind: "ci", status: "active" })).filter(Boolean);
    const startDetail = triggerDescription.trim()
      || [...triggerActions.map((action) => titleCase(action)), ...triggerNames].join(", ")
      || "No trigger yet";
    const endDetail = outputDescription.trim()
      || [...outputActions.map((action) => titleCase(action)), ...outputNames].join(", ")
      || "No outputs yet";
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
    const waitedOn = new Set<number>();
    if (steps.length === 0) {
      edges.push({ id: "start-end", source: "start", target: "end", markerEnd: marker, style: edgeStyle });
    } else {
      for (const step of steps) {
        for (const dep of step.dependsOn) {
          if (dep.kind === "process-start") {
            edges.push({
              id: `start-${step.uiKey}`,
              source: "start",
              target: `step-${step.uiKey}`,
              markerEnd: marker,
              style: edgeStyle,
            });
            continue;
          }
          waitedOn.add(dep.uiKey);
          edges.push({
            id: `dep-${dep.uiKey}-${step.uiKey}`,
            source: `step-${dep.uiKey}`,
            target: `step-${step.uiKey}`,
            markerEnd: marker,
            style: edgeStyle,
          });
        }
      }
      for (const step of steps) {
        if (waitedOn.has(step.uiKey)) continue;
        edges.push({
          id: `end-${step.uiKey}`,
          source: `step-${step.uiKey}`,
          target: "end",
          markerEnd: marker,
          style: edgeStyle,
        });
      }
    }
    setFlowNodes(nodes);
    setFlowEdges(edges);
  }, [steps, triggerDescription, triggerActions, triggerIds, outputDescription, outputActions, outputIds, cis, anchorPositions, defaultEndX, canUpdate]);

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
      return keepEarlierDependencies(copy);
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
          status: "draft",
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
        triggers: triggerIds.map((id): ProcessMapRef => ({ kind: "ci", id })),
        outputActions,
        outputDescription: outputDescription.trim(),
        outputs: outputIds.map((id): ProcessMapRef => ({ kind: "ci", id })),
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
          dependsOn: step.dependsOn.map((dep): ProcessMapDependency => {
            if (dep.kind === "process-start") return { kind: "process-start" };
            const targetIndex = steps.findIndex((item) => item.uiKey === dep.uiKey);
            const target = steps[targetIndex];
            return { kind: "step", ...(target?.id ? { id: target.id } : {}), order: targetIndex + 1 };
          }),
        })),
      };
      const addedStepCount = steps.filter((step) => !step.id).length;
      const saved = await saveProcessMap(activeProcessId, payload);
      const draft = draftFromMap(saved);
      setName(draft.name);
      setDescription(draft.description);
      setTriggerDescription(draft.triggerDescription);
      setTriggerActions(draft.triggerActions);
      setTriggerIds(draft.triggerIds);
      setOutputDescription(draft.outputDescription);
      setOutputActions(draft.outputActions);
      setOutputIds(draft.outputIds);
      const previousId = steps.find((step) => step.uiKey === selectedStepKey)?.id;
      setSteps(draft.steps);
      setSelectedStepKey((draft.steps.find((step) => step.id && step.id === previousId) ?? draft.steps[0])?.uiKey ?? null);
      const draftPackage = createdNew || selectedProcess?.status === "draft";
      const addedDraft = !draftPackage && addedStepCount > 0;
      onNotify(draftPackage
        ? `Saved ${saved.name || saved.processId} as a draft. It stays off the live inventory until a super admin publishes it from Home.`
        : addedDraft
          ? `Saved ${saved.name || saved.processId}. ${addedStepCount === 1 ? "The new step was" : `${addedStepCount} new steps were`} saved as draft for approval.`
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

  // Downloads the saved map (not unsaved edits) as a Mermaid .mmd file.
  async function exportMermaid() {
    if (!processId) return;
    setExporting(true);
    setError("");
    try {
      const source = await getProcessMapMermaid(processId);
      const url = URL.createObjectURL(new Blob([source], { type: "text/vnd.mermaid" }));
      const link = document.createElement("a");
      link.href = url;
      link.download = `${processId.replace(/[^A-Za-z0-9_-]/g, "") || "process-map"}.mmd`;
      link.click();
      URL.revokeObjectURL(url);
    } catch (exportError: unknown) {
      setError(exportError instanceof Error ? exportError.message : "Could not export this map.");
    } finally {
      setExporting(false);
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
          {processId && (
            <button
              className="button button-quiet icon-button"
              type="button"
              title="Download the saved map as a Mermaid flowchart"
              disabled={exporting || loading}
              onClick={() => void exportMermaid()}
            >
              <Download size={14} />{exporting ? "Exporting…" : "Export Mermaid"}
            </button>
          )}
        </div>

        {(selectedProcess?.status === "draft" || (!processId && canUpdate)) && (
          <p className="field-hint">New processes, new steps, and records created with + are saved as draft, including steps added after a process is already active. They show as a package on the super admin home until they are published.</p>
        )}
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
            <RecordPicker
              id="process-trigger-cis"
              label="Trigger configuration items"
              hint="Stored as triggered-by. An application, process, function, or service that starts this process."
              placeholder="Find a configuration item by name or ID"
              options={triggerCIOptions}
              selected={triggerIds}
              readOnly={!canUpdate}
              onChange={setTriggerIds}
              onCreate={canUpdate && canCreateCI && onCreateRecord ? () => { setPendingCreate("trigger"); onCreateRecord("ci", { ciType: "application" }); } : undefined}
              createLabel="Create configuration item"
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
                      setSteps((current) => {
                        const dependsOn: StepDependency[] = current.length === 0
                          ? [{ kind: "process-start" }]
                          : [{ kind: "step", uiKey: current[current.length - 1].uiKey }];
                        return [...current, { ...step, order: current.length + 1, dependsOn }];
                      });
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
                              setSteps((current) => keepEarlierDependencies(current.filter((item) => item.uiKey !== step.uiKey)));
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
                  onCreate={canUpdate && canCreateGroup && onCreateRecord && !stepIsAutomated ? () => { setPendingCreate("group"); onCreateRecord("group"); } : undefined}
                  createLabel="Create group"
                />
                <fieldset className="process-mapper-actions" disabled={!canUpdate}>
                  <legend>Depends on</legend>
                  <ul className="process-mapper-action-list process-mapper-dependencies" id={`step-depends-${selectedStep.uiKey}`}>
                    {[{ key: "process-start", label: "Process Start" }, ...steps.slice(0, steps.findIndex((step) => step.uiKey === selectedStep.uiKey)).map((step, index) => ({
                      key: `step:${step.uiKey}`,
                      label: `Step ${index + 1}${step.name.trim() ? ` · ${step.name.trim()}` : ""}`,
                    }))].map((option) => {
                      const checked = option.key === "process-start"
                        ? selectedStep.dependsOn.some((dep) => dep.kind === "process-start")
                        : selectedStep.dependsOn.some((dep) => dep.kind === "step" && `step:${dep.uiKey}` === option.key);
                      return <li key={option.key}>
                        <label htmlFor={`step-depends-${selectedStep.uiKey}-${option.key}`}>
                          <input
                            id={`step-depends-${selectedStep.uiKey}-${option.key}`}
                            type="checkbox"
                            checked={checked}
                            disabled={!canUpdate}
                            onChange={() => {
                              const next = checked
                                ? selectedStep.dependsOn.filter((dep) => (dep.kind === "process-start" ? "process-start" : `step:${dep.uiKey}`) !== option.key)
                                : [...selectedStep.dependsOn, option.key === "process-start" ? { kind: "process-start" as const } : { kind: "step" as const, uiKey: Number(option.key.slice("step:".length)) }];
                              updateStep(selectedStep.uiKey, { dependsOn: next });
                            }}
                          />
                          <span>{option.label}</span>
                        </label>
                      </li>;
                    })}
                  </ul>
                  <p className="field-hint">Steps that depend on Process Start begin together. Select earlier steps when this step should wait for them. More than one selection means it waits for all of them. Stored as depends-on.</p>
                </fieldset>
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
            <RecordPicker
              id="process-output-cis"
              label="Output configuration items"
              hint="Stored as produces. The configuration items this process creates or updates."
              placeholder="Find a configuration item by name or ID"
              options={outputCIOptions}
              selected={outputIds}
              readOnly={!canUpdate}
              onChange={setOutputIds}
              onCreate={canUpdate && canCreateCI && onCreateRecord ? () => { setPendingCreate("output"); onCreateRecord("ci"); } : undefined}
              createLabel="Create configuration item"
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
