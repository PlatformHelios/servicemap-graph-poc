import type { GraphNode, ItemDecision, RequestState, RequestType, TaskStatus } from "./types";

// Shared between the catalog forms, the Workflow Creator, and Workflow Tasks.

const requestStates: RequestState[] = ["draft", "submitted", "in-review", "fulfilled", "denied"];

export function requestStateOf(record: GraphNode | undefined): RequestState {
  const state = record?.properties?.state;
  return requestStates.find((candidate) => candidate === state) ?? "draft";
}

export function requestTypeOf(record: GraphNode | undefined): RequestType {
  return record?.properties?.requestType === "access" ? "access" : "vendor";
}

export function requestStatePillClass(state: RequestState) {
  return state === "draft" ? "status-draft" : state === "fulfilled" ? "status-active" : state === "denied" ? "status-retired" : "status-in-review";
}

export function taskStatusPillClass(status: TaskStatus) {
  return status === "pending" ? "status-in-review" : status === "rejected" ? "status-retired" : "status-active";
}

// Where one item of an access request has got to.
export function itemDecisionPillClass(decision: ItemDecision | undefined) {
  return decision === "fulfilled" ? "status-active" : decision === "denied" ? "status-retired" : decision === "approved" ? "status-draft" : "status-in-review";
}

export function itemDecisionLabel(decision: ItemDecision | undefined) {
  return decision === "approved" ? "approved · awaiting fulfilment" : decision === "fulfilled" ? "provisioned" : decision ?? "pending";
}

export const formTitles: Record<RequestType, string> = { vendor: "Vendor Request Form", access: "Access Request Form" };

// Labels for the request fields a workflow step may expose, keyed the same way
// as the form itself (the keys come from Metadata.requestFields). The access
// form has no fields a step can expose.
export const requestFieldLabels: Record<RequestType, Record<string, string>> = {
  vendor: {
    name: "Vendor Name",
    description: "Description",
    criticality: "Criticality",
    contactName: "Vendor Contact Name",
    contactPhone: "Contact phone number",
    contactEmail: "Contact Email Address",
  },
  access: {},
};

export function requestFieldLabel(requestType: RequestType, key: string) {
  return requestFieldLabels[requestType]?.[key] ?? key;
}

export function formatWhen(value: string | undefined) {
  return value ? new Date(value).toLocaleString([], { dateStyle: "medium", timeStyle: "short" }) : "";
}
