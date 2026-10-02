export type NodeKind = "identity" | "job-code" | "birthright" | "role" | "entitlement";

export type RelationshipKind = "has-job-code" | "qualifies-for" | "grants" | "includes";

export type PropertyValue = string | number | boolean;

export type Properties = Record<string, PropertyValue>;

export interface GraphRelationship {
  kind: RelationshipKind;
  fromId: string;
  toId: string;
  properties?: Properties;
  status: "active" | "retired";
  retiredAt?: string;
}

export interface GraphNode {
  kind: NodeKind;
  id: string;
  properties?: Properties;
  status: "active" | "retired";
  retiredAt?: string;
  relationships?: GraphRelationship[];
}

export interface RelationshipDefinition {
  kind: RelationshipKind;
  from: NodeKind;
  to: NodeKind;
}

export interface Metadata {
  nodeKinds: NodeKind[];
  relationships: RelationshipDefinition[];
}

export type Resource = NodeKind | "relationships";

export type RecordEditorTarget =
  | { type: "node"; kind: NodeKind; record?: GraphNode }
  | { type: "relationship"; kind?: RelationshipKind; record?: GraphRelationship };

export type RetireTarget =
  | { type: "node"; kind: NodeKind; record: GraphNode }
  | { type: "relationship"; kind: RelationshipKind; record: GraphRelationship };