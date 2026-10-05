import { useState } from "react";
import { Search, X } from "lucide-react";
import { nodeDisplayName } from "./api";
import type { GraphNode } from "./types";

export function titleCase(value: string) {
  return value.replaceAll("-", " ").replace(/\b\w/g, (character) => character.toUpperCase());
}

// Object picker for records linked from a form (RACI roles, incident assignment,
// affected CIs, workflow assignees): chosen records show as removable chips and
// new ones are found by typing a name or ID. Only the closest matches are listed,
// so the form never enumerates every identity, group, or CI.
const pickerMatchLimit = 8;
function pickerSearchText(node: GraphNode) {
  return [nodeDisplayName(node), node.id, node.kind, JSON.stringify(node.properties ?? {})].join(" ").toLowerCase();
}
export function pickerSubtitle(node: GraphNode) {
  const kind = node.kind === "ci" ? String(node.properties?.ciType ?? "ci") : node.kind;
  return `${node.id} · ${titleCase(kind)}`;
}
export default function RecordPicker({ id, label, hint, placeholder, options, selected, single, readOnly, onChange }: { id: string; label: string; hint?: string; placeholder: string; options: GraphNode[]; selected: string[]; single?: boolean; readOnly?: boolean; onChange: (ids: string[]) => void }) {
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState(false);
  const term = query.trim().toLowerCase();
  const matches = term
    ? options.filter((node) => !selected.includes(node.id) && pickerSearchText(node).includes(term)).slice(0, pickerMatchLimit)
    : [];
  const chosen = selected.map((value) => options.find((node) => node.id === value) ?? { id: value, kind: "identity", status: "active" } as GraphNode);
  const canAdd = !readOnly && (!single || selected.length === 0);

  function pick(node: GraphNode) {
    onChange(single ? [node.id] : [...selected, node.id]);
    setQuery("");
    setOpen(false);
  }
  function onKeyDown(event: React.KeyboardEvent<HTMLInputElement>) {
    if (event.key === "Enter") {
      event.preventDefault();
      if (matches[0]) pick(matches[0]);
    } else if (event.key === "Escape" && query) {
      event.stopPropagation();
      setQuery("");
      setOpen(false);
    }
  }

  return <div className="form-field raci-role">
    <label htmlFor={id}>{label} <span className="ci-selection-count">{single ? (selected.length ? "assigned" : "not assigned") : `${selected.length} selected`}</span></label>
    {chosen.length > 0 && <ul className="raci-chips" aria-label={`${label} assignments`}>{chosen.map((node) => <li className="raci-chip" key={node.id}>
      <span><strong>{nodeDisplayName(node) ?? node.id}</strong><small>{pickerSubtitle(node)}</small></span>
      {!readOnly && <button type="button" aria-label={`Remove ${nodeDisplayName(node) ?? node.id} from ${label}`} onClick={() => onChange(selected.filter((value) => value !== node.id))}><X size={13} /></button>}
    </li>)}</ul>}
    {canAdd && <div className="raci-finder" onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false); }}>
      <label className="search-box raci-search"><Search aria-hidden="true" size={15} /><input id={id} value={query} type="search" autoComplete="off" role="combobox" aria-expanded={open && term.length > 0} aria-controls={`${id}-matches`} placeholder={placeholder} onChange={(event) => { setQuery(event.target.value); setOpen(true); }} onFocus={() => setOpen(true)} onKeyDown={onKeyDown} /></label>
      {open && term && <ul className="raci-matches" id={`${id}-matches`} role="listbox">
        {matches.length === 0
          ? <li className="raci-no-match">No active records match “{query.trim()}”.</li>
          : matches.map((node) => <li key={node.id} role="option" aria-selected={false}>
            <button type="button" className="raci-match" onMouseDown={(event) => event.preventDefault()} onClick={() => pick(node)}>
              <strong>{nodeDisplayName(node) ?? node.id}</strong><small>{pickerSubtitle(node)}</small>
            </button>
          </li>)}
      </ul>}
    </div>}
    {hint && <span className="field-hint">{hint}</span>}
  </div>;
}
