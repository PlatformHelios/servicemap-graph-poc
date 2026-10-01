const state = {
  resource: "identity",
  relationshipKind: "has-job-code",
  includeRetired: false,
  rows: [],
  metadata: { nodeKinds: [], relationships: [] },
  editing: null,
  retiring: null,
};

const labels = {
  identity: { title: "Identities", singular: "identity", subtitle: "People and their assigned job codes.", kicker: "IDENTITY DIRECTORY" },
  "job-code": { title: "Job codes", singular: "job code", subtitle: "Positions that qualify for access packages.", kicker: "WORKFORCE CLASSIFICATION" },
  birthright: { title: "Birthrights", singular: "birthright", subtitle: "Automatic access bundles derived from workforce attributes.", kicker: "AUTOMATIC ACCESS" },
  role: { title: "Roles", singular: "role", subtitle: "Permission groupings used to grant system access.", kicker: "ACCESS GROUPS" },
  entitlement: { title: "Entitlements", singular: "entitlement", subtitle: "Concrete permissions within connected systems.", kicker: "SYSTEM PERMISSIONS" },
  relationships: { title: "Relationships", singular: "relationship", subtitle: "Directed links that describe how access is derived.", kicker: "GRAPH CONNECTIONS" },
};

const nodePropertyFields = {
  identity: [
    { name: "name", label: "Display name" },
    { name: "department", label: "Department" },
    { name: "location", label: "Location" },
  ],
  "job-code": [{ name: "name", label: "Job title" }],
  birthright: [{ name: "name", label: "Birthright name" }],
  role: [{ name: "name", label: "Role name" }],
  entitlement: [{ name: "name", label: "Entitlement name" }],
};

const elements = {
  title: document.querySelector("#page-title"),
  subtitle: document.querySelector("#page-subtitle"),
  kicker: document.querySelector("#resource-kicker"),
  create: document.querySelector("#create-button"),
  refresh: document.querySelector("#refresh-button"),
  search: document.querySelector("#search-input"),
  retired: document.querySelector("#include-retired"),
  relFilter: document.querySelector("#relationship-filter-wrap"),
  relKind: document.querySelector("#relationship-kind"),
  head: document.querySelector("#table-head"),
  body: document.querySelector("#table-body"),
  empty: document.querySelector("#empty-state"),
  emptyTitle: document.querySelector("#empty-title"),
  emptyCopy: document.querySelector("#empty-copy"),
  count: document.querySelector("#result-count"),
  metrics: {
    total: document.querySelector("#metric-total"),
    active: document.querySelector("#metric-active"),
    retired: document.querySelector("#metric-retired"),
    links: document.querySelector("#metric-links"),
    linksLabel: document.querySelector("#metric-links-label"),
  },
  connectionDot: document.querySelector("#connection-dot"),
  connectionLabel: document.querySelector("#connection-label"),
  editor: document.querySelector("#editor-dialog"),
  editorForm: document.querySelector("#editor-form"),
  editorFields: document.querySelector("#editor-fields"),
  dialogTitle: document.querySelector("#dialog-title"),
  dialogKicker: document.querySelector("#dialog-kicker"),
  save: document.querySelector("#save-button"),
  retire: document.querySelector("#retire-dialog"),
  retireForm: document.querySelector("#retire-form"),
  retireCopy: document.querySelector("#retire-copy"),
  toast: document.querySelector("#toast"),
};

async function request(path, options = {}) {
  const response = await fetch(path, {
    ...options,
    headers: { "Content-Type": "application/json", ...(options.headers || {}) },
  });
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(payload.error || `Request failed (${response.status})`);
  return payload;
}

function escapeHTML(value) {
  return String(value ?? "").replace(/[&<>"']/g, character => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  })[character]);
}

function currentKind() {
  return state.resource === "relationships" ? state.relationshipKind : state.resource;
}

function recordsURL() {
  const retired = state.includeRetired ? "?includeRetired=true" : "";
  const group = state.resource === "relationships" ? "relationships" : "nodes";
  return `/api/${group}/${encodeURIComponent(currentKind())}${retired}`;
}

async function loadRecords() {
  elements.body.innerHTML = "";
  elements.count.textContent = "Loading records…";
  try {
    state.rows = await request(recordsURL());
    render();
    setConnection(true);
  } catch (error) {
    state.rows = [];
    render();
    setConnection(false);
    showToast(error.message, true);
  }
}

function setConnection(online) {
  elements.connectionDot.classList.toggle("online", online);
  elements.connectionDot.classList.toggle("offline", !online);
  elements.connectionLabel.textContent = online ? "Database connected" : "Database unavailable";
}

function resourceTitle(kind) {
  return kind.replaceAll("-", " ").replace(/\b\w/g, char => char.toUpperCase());
}

function render() {
  const info = labels[state.resource];
  elements.title.textContent = info.title;
  elements.subtitle.textContent = info.subtitle;
  elements.kicker.textContent = info.kicker;
  elements.create.textContent = `Add ${info.singular}`;
  elements.relFilter.classList.toggle("hidden", state.resource !== "relationships");
  elements.metrics.linksLabel.textContent = state.resource === "relationships" ? "LINK RECORDS" : "DIRECT LINKS";
  renderHeaders();

  const query = elements.search.value.trim().toLowerCase();
  const rows = state.rows.filter(row => JSON.stringify(row).toLowerCase().includes(query));
  elements.body.innerHTML = rows.map((row, index) => state.resource === "relationships"
    ? renderRelationshipRow(row, index)
    : renderNodeRow(row, index)).join("");

  elements.empty.classList.toggle("hidden", rows.length !== 0);
  elements.emptyTitle.textContent = query ? "No matching records" : `No ${info.title.toLowerCase()} found`;
  elements.emptyCopy.textContent = query ? "Try another filter." : "Create a record to add it to this inventory.";
  elements.count.textContent = `${rows.length} ${rows.length === 1 ? "record" : "records"}`;

  const retired = state.rows.filter(row => row.status === "retired").length;
  elements.metrics.total.textContent = state.rows.length;
  elements.metrics.retired.textContent = retired;
  elements.metrics.active.textContent = state.rows.length - retired;
  const linkCount = state.resource === "relationships"
    ? state.rows.length
    : state.rows.reduce((total, node) => total + (node.relationships || []).length, 0);
  elements.metrics.links.textContent = linkCount;
}

function renderHeaders() {
  const headings = state.resource === "relationships"
    ? ["Relationship", "From", "To", "Properties", "Status", ""]
    : ["Identifier", "Name", "Properties", "Direct relationships", "Status", ""];
  elements.head.innerHTML = `<tr>${headings.map(heading => `<th>${heading}</th>`).join("")}</tr>`;
}

function renderNodeRow(node, index) {
  const name = node.properties?.name || node.properties?.title || node.properties?.department || "—";
  const properties = Object.entries(node.properties || {}).map(([key, value]) => `${key}: ${value}`).join(" · ") || "No additional properties";
  const relationships = (node.relationships || []).map(relationship => {
    const direction = relationship.fromId === node.id ? "out" : "in";
    const neighbor = direction === "out" ? relationship.toId : relationship.fromId;
    return `<div class="relationship-line"><strong>${escapeHTML(relationship.kind)}</strong> ${direction === "out" ? "→" : "←"} ${escapeHTML(neighbor)}${relationship.status === "retired" ? " · retired" : ""}</div>`;
  }).join("") || `<span class="secondary-value">No direct links</span>`;
  return `<tr>
    <td><span class="identifier">${escapeHTML(node.id)}</span></td>
    <td><div class="primary-value">${escapeHTML(name)}</div><div class="secondary-value">${escapeHTML(resourceTitle(node.kind))}</div></td>
    <td><div class="property-list">${escapeHTML(properties)}</div></td>
    <td><div class="relationship-list">${relationships}</div></td>
    <td>${statusPill(node.status)}</td>
    <td>${actions(node, false)}</td>
  </tr>`;
}

function renderRelationshipRow(relationship, index) {
  const properties = Object.entries(relationship.properties || {}).map(([key, value]) => `${key}: ${value}`).join(" · ") || "—";
  return `<tr>
    <td><span class="identifier">${escapeHTML(relationship.kind)}</span></td>
    <td><span class="identifier">${escapeHTML(relationship.fromId)}</span></td>
    <td><span class="identifier">${escapeHTML(relationship.toId)}</span></td>
    <td><div class="property-list">${escapeHTML(properties)}</div></td>
    <td>${statusPill(relationship.status)}</td>
    <td>${actions(relationship, true)}</td>
  </tr>`;
}

function statusPill(status) {
  const value = status === "retired" ? "retired" : "active";
  return `<span class="status-pill status-${value}">${value}</span>`;
}

function actions(row, relationship) {
  const idAttributes = relationship
    ? `data-kind="${escapeHTML(row.kind)}" data-from="${escapeHTML(row.fromId)}" data-to="${escapeHTML(row.toId)}"`
    : `data-kind="${escapeHTML(row.kind)}" data-id="${escapeHTML(row.id)}"`;
  return `<div class="row-actions"><button class="action-button" data-action="edit" ${idAttributes}>Edit</button>${row.status !== "retired" ? `<button class="action-button retire" data-action="retire" ${idAttributes}>Retire</button>` : ""}</div>`;
}

function showToast(message, error = false) {
  elements.toast.textContent = message;
  elements.toast.classList.toggle("error", error);
  elements.toast.classList.add("visible");
  clearTimeout(showToast.timer);
  showToast.timer = setTimeout(() => elements.toast.classList.remove("visible"), 3000);
}

function renderPropertyFields(kind, properties = {}) {
  const fields = nodePropertyFields[kind] || [];
  const fixedNames = new Set(fields.map(field => field.name));
  const fixedFields = fields.map(field => `
    <div class="form-field">
      <label for="property-${escapeHTML(field.name)}">${escapeHTML(field.label)}</label>
      <input id="property-${escapeHTML(field.name)}" type="text" data-property-name="${escapeHTML(field.name)}" value="${escapeHTML(properties[field.name] ?? "")}" autocomplete="off">
    </div>`).join("");
  const attributes = Object.entries(properties).filter(([name]) => !fixedNames.has(name));
  return `<div class="field-grid">${fixedFields}</div>${renderAttributes(attributes)}`;
}

function renderAttributes(attributes = []) {
  return `<fieldset class="attribute-editor">
    <legend>Additional attributes</legend>
    <div class="attribute-list">${attributes.map(([name, value]) => renderAttributeRow(name, value)).join("")}</div>
    <button class="attribute-add" type="button" data-add-attribute>Add attribute</button>
  </fieldset>`;
}

function renderAttributeRow(name = "", value = "") {
  const type = typeof value === "number" ? "number" : typeof value === "boolean" ? "boolean" : "text";
  const displayValue = type === "boolean" ? String(value) : value;
  return `<div class="attribute-row">
    <input type="text" data-attribute-name value="${escapeHTML(name)}" placeholder="Attribute name" aria-label="Attribute name">
    <select data-attribute-type aria-label="Attribute value type">
      <option value="text" ${type === "text" ? "selected" : ""}>Text</option>
      <option value="number" ${type === "number" ? "selected" : ""}>Number</option>
      <option value="boolean" ${type === "boolean" ? "selected" : ""}>Boolean</option>
    </select>
    <input type="text" data-attribute-value value="${escapeHTML(displayValue)}" placeholder="Value" aria-label="Attribute value">
    <button class="attribute-remove" type="button" data-remove-attribute aria-label="Remove attribute">Remove</button>
  </div>`;
}

function collectProperties() {
  const properties = {};
  elements.editorFields.querySelectorAll("[data-property-name]").forEach(input => {
    const value = input.value.trim();
    if (value !== "") properties[input.dataset.propertyName] = value;
  });
  for (const row of elements.editorFields.querySelectorAll("[data-attribute-name]")) {
    const name = row.value.trim();
    const value = row.closest(".attribute-row").querySelector("[data-attribute-value]").value.trim();
    const type = row.closest(".attribute-row").querySelector("[data-attribute-type]").value;
    if (!name && !value) continue;
    if (!name || !value) throw new Error("Enter both an attribute name and value, or remove the empty row.");
    if (Object.hasOwn(properties, name)) throw new Error(`Attribute "${name}" is entered more than once.`);
    if (type === "number") {
      const number = Number(value);
      if (!Number.isFinite(number)) throw new Error(`Attribute "${name}" must have a valid number.`);
      properties[name] = number;
    } else if (type === "boolean") {
      if (value !== "true" && value !== "false") throw new Error(`Attribute "${name}" must be true or false.`);
      properties[name] = value === "true";
    } else {
      properties[name] = value;
    }
  }
  return properties;
}

function openNodeEditor(node = null) {
  state.editing = node ? { type: "node", value: node } : { type: "node", value: null };
  const kind = state.resource;
  const idLabel = kind === "job-code" ? "Code" : "Identifier";
  const idValue = node?.id || "";
  elements.dialogKicker.textContent = node ? "UPDATE RECORD" : "NEW RECORD";
  elements.dialogTitle.textContent = `${node ? "Edit" : "Add"} ${labels[kind].singular}`;
  elements.save.textContent = node ? "Save changes" : "Create record";
  elements.editorFields.innerHTML = `
    <div class="form-field"><label for="record-id">${idLabel}</label><input id="record-id" name="id" value="${escapeHTML(idValue)}" required ${node ? "readonly" : "autofocus"}><span class="field-hint">Stable identifiers cannot be changed after creation.</span></div>
    ${renderPropertyFields(kind, node?.properties || {})}`;
  elements.editor.showModal();
  if (!node) document.querySelector("#record-id").focus();
}

function openRelationshipEditor(relationship = null) {
  state.editing = { type: "relationship", value: relationship };
  const kinds = state.metadata.relationships;
  const kindOptions = kinds.map(item => `<option value="${escapeHTML(item.kind)}" ${item.kind === (relationship?.kind || state.relationshipKind) ? "selected" : ""}>${escapeHTML(item.kind)}</option>`).join("");
  const fromId = relationship?.fromId || "";
  const toId = relationship?.toId || "";
  elements.dialogKicker.textContent = relationship ? "UPDATE RELATIONSHIP" : "NEW RELATIONSHIP";
  elements.dialogTitle.textContent = relationship ? "Edit relationship" : "Add relationship";
  elements.save.textContent = relationship ? "Save changes" : "Create relationship";
  elements.editorFields.innerHTML = `
    <div class="form-field"><label for="relationship-kind-input">Relationship type</label><select id="relationship-kind-input" name="kind" ${relationship ? "disabled" : ""}>${kindOptions}</select></div>
    <div class="form-field"><label for="relationship-from">From identifier</label><input id="relationship-from" name="fromId" value="${escapeHTML(fromId)}" required ${relationship ? "readonly" : ""}></div>
    <div class="form-field"><label for="relationship-to">To identifier</label><input id="relationship-to" name="toId" value="${escapeHTML(toId)}" required ${relationship ? "readonly" : ""}></div>
    <p class="field-hint relationship-hint">Endpoints are fixed by the selected relationship type.</p>
    ${renderAttributes(Object.entries(relationship?.properties || {}))}`;
  elements.editor.showModal();
}

async function saveRecord(event) {
  event.preventDefault();
  const form = new FormData(elements.editorForm);
  let properties;
  try { properties = collectProperties(); }
  catch (error) { showToast(error.message, true); return; }
  const existing = state.editing.value;
  let path;
  let method;
  let body;
  if (state.editing.type === "node") {
    const kind = state.resource;
    path = existing ? `/api/nodes/${encodeURIComponent(kind)}/${encodeURIComponent(existing.id)}` : `/api/nodes/${encodeURIComponent(kind)}`;
    method = existing ? "PATCH" : "POST";
    body = existing ? { properties } : { id: form.get("id"), properties };
  } else {
    const kind = existing ? existing.kind : form.get("kind");
    const fromId = existing ? existing.fromId : form.get("fromId");
    const toId = existing ? existing.toId : form.get("toId");
    path = existing
      ? `/api/relationships/${encodeURIComponent(kind)}/${encodeURIComponent(fromId)}/${encodeURIComponent(toId)}`
      : `/api/relationships/${encodeURIComponent(kind)}`;
    method = existing ? "PATCH" : "POST";
    body = existing ? { properties } : { fromId, toId, properties };
  }
  try {
    await request(path, { method, body: JSON.stringify(body) });
    elements.editor.close();
    showToast(existing ? "Changes saved." : "Record created.");
    await loadRecords();
  } catch (error) { showToast(error.message, true); }
}

function openRetireDialog(row, relationship) {
  state.retiring = { row, relationship };
  elements.retireCopy.textContent = relationship
    ? `Relationship ${row.kind} from ${row.fromId} to ${row.toId} will be marked retired.`
    : `${resourceTitle(row.kind)} record ${row.id} and its directly attached relationships will be marked retired.`;
  elements.retire.showModal();
}

async function retireRecord(event) {
  event.preventDefault();
  const { row, relationship } = state.retiring;
  const path = relationship
    ? `/api/relationships/${encodeURIComponent(row.kind)}/${encodeURIComponent(row.fromId)}/${encodeURIComponent(row.toId)}`
    : `/api/nodes/${encodeURIComponent(row.kind)}/${encodeURIComponent(row.id)}`;
  try {
    await request(path, { method: "DELETE" });
    elements.retire.close();
    showToast("Record retired.");
    await loadRecords();
  } catch (error) { showToast(error.message, true); }
}

function findRow(button, relationship) {
  if (relationship) return state.rows.find(row => row.kind === button.dataset.kind && row.fromId === button.dataset.from && row.toId === button.dataset.to);
  return state.rows.find(row => row.kind === button.dataset.kind && row.id === button.dataset.id);
}

async function initialize() {
  try {
    state.metadata = await request("/api/meta");
    elements.relKind.innerHTML = state.metadata.relationships.map(item => `<option value="${escapeHTML(item.kind)}">${escapeHTML(item.kind)}</option>`).join("");
    state.relationshipKind = elements.relKind.value || "has-job-code";
  } catch (error) {
    setConnection(false);
    showToast(error.message, true);
  }
  await loadRecords();
}

document.querySelectorAll("[data-resource]").forEach(button => button.addEventListener("click", () => {
  state.resource = button.dataset.resource;
  document.querySelectorAll("[data-resource]").forEach(item => item.classList.toggle("active", item === button));
  elements.search.value = "";
  loadRecords();
}));
elements.create.addEventListener("click", () => state.resource === "relationships" ? openRelationshipEditor() : openNodeEditor());
elements.refresh.addEventListener("click", loadRecords);
elements.search.addEventListener("input", render);
elements.retired.addEventListener("change", () => { state.includeRetired = elements.retired.checked; loadRecords(); });
elements.relKind.addEventListener("change", () => { state.relationshipKind = elements.relKind.value; loadRecords(); });
elements.editorForm.addEventListener("submit", saveRecord);
elements.retireForm.addEventListener("submit", retireRecord);
document.querySelectorAll("[data-close-dialog]").forEach(button => button.addEventListener("click", () => elements.editor.close()));
document.querySelectorAll("[data-close-retire]").forEach(button => button.addEventListener("click", () => elements.retire.close()));
elements.editorFields.addEventListener("click", event => {
  const addButton = event.target.closest("[data-add-attribute]");
  if (addButton) {
    const list = elements.editorFields.querySelector(".attribute-list");
    list.insertAdjacentHTML("beforeend", renderAttributeRow());
    list.lastElementChild.querySelector("[data-attribute-name]").focus();
    return;
  }
  const removeButton = event.target.closest("[data-remove-attribute]");
  if (removeButton) removeButton.closest(".attribute-row").remove();
});
elements.body.addEventListener("click", event => {
  const button = event.target.closest("button[data-action]");
  if (!button) return;
  const relationship = state.resource === "relationships";
  const row = findRow(button, relationship);
  if (!row) return;
  if (button.dataset.action === "edit") relationship ? openRelationshipEditor(row) : openNodeEditor(row);
  if (button.dataset.action === "retire") openRetireDialog(row, relationship);
});

initialize();