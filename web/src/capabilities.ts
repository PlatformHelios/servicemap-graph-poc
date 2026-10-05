import type { ActorAccess, Resource } from "./types";

// Mirrors the Anthos entitlement → capability mapping on the API. Super admin
// bypasses every check.

export function hasCapability(access: ActorAccess | null | undefined, capability: string): boolean {
  if (!access) return false;
  if (access.superAdmin) return true;
  return access.capabilities.includes(capability);
}

export function hasAnyCapability(access: ActorAccess | null | undefined, capabilities: string[]): boolean {
  return capabilities.some((capability) => hasCapability(access, capability));
}

// Whether the nav item for a resource should show for this actor.
export function canViewResource(access: ActorAccess | null | undefined, resource: Resource): boolean {
  if (!access) return resource === "home";
  if (access.superAdmin) return true;
  switch (resource) {
    case "home":
      return true;
    case "identity":
      return hasCapability(access, "identity:read");
    case "job-code":
      return hasCapability(access, "job-code:read");
    case "birthright":
      return hasCapability(access, "birthright:read");
    case "role":
      return hasCapability(access, "role:read");
    case "entitlement":
      return hasCapability(access, "entitlement:read");
    case "group":
      return hasCapability(access, "group:read");
    case "incident":
      return hasCapability(access, "incident:read");
    case "change":
      return hasCapability(access, "change:read");
    case "event":
      return hasCapability(access, "event:read");
    case "relationships":
      return hasCapability(access, "relationship:read");
    case "map":
      return hasCapability(access, "map:read");
    case "ci":
      return hasCapability(access, "config-item:read");
    case "location-map":
      return hasCapability(access, "location-map:read");
    case "request":
      return hasCapability(access, "vendor-request:use");
    case "access-request":
      return hasCapability(access, "access-request:use");
    case "workflow-creator":
      return hasCapability(access, "workflow-creator:use");
    case "workflow-tasks":
      return hasCapability(access, "workflow-tasks:read");
    case "workflow-run":
      return hasCapability(access, "workflow-runs:read");
    default:
      return false;
  }
}

// Whether Add / create is offered on this resource.
export function canCreateResource(access: ActorAccess | null | undefined, resource: Resource): boolean {
  if (!access) return false;
  if (access.superAdmin) return true;
  switch (resource) {
    case "identity":
      return hasCapability(access, "identity:create");
    case "job-code":
      return hasCapability(access, "job-code:create");
    case "birthright":
      return hasCapability(access, "birthright:create");
    case "role":
      return hasCapability(access, "role:create");
    case "entitlement":
      return hasCapability(access, "entitlement:create");
    case "group":
      return hasCapability(access, "group:create");
    case "incident":
      return hasCapability(access, "incident:create");
    case "change":
      return hasCapability(access, "change:create");
    case "event":
      return hasCapability(access, "event:create");
    case "relationships":
      return hasCapability(access, "relationship:create");
    case "ci":
      return hasCapability(access, "config-item:create");
    case "location-map":
      return hasCapability(access, "location-map:create");
    case "request":
      return hasCapability(access, "vendor-request:use");
    default:
      return false;
  }
}

export function canUpdateResource(access: ActorAccess | null | undefined, resource: Resource): boolean {
  if (!access) return false;
  if (access.superAdmin) return true;
  switch (resource) {
    case "identity":
      return hasCapability(access, "identity:update");
    case "job-code":
      return hasCapability(access, "job-code:update");
    case "birthright":
      return hasCapability(access, "birthright:update");
    case "role":
      return hasCapability(access, "role:update");
    case "entitlement":
      return hasCapability(access, "entitlement:update");
    case "group":
      return hasCapability(access, "group:update");
    case "incident":
      return hasCapability(access, "incident:update");
    case "change":
      return hasCapability(access, "change:update");
    case "event":
      return hasCapability(access, "event:update");
    case "relationships":
      return hasCapability(access, "relationship:update");
    case "ci":
      return hasCapability(access, "config-item:update");
    case "location-map":
      return hasCapability(access, "location-map:update");
    case "request":
      return hasCapability(access, "vendor-request:use");
    default:
      return false;
  }
}

export function canRetireResource(access: ActorAccess | null | undefined, resource: Resource): boolean {
  if (!access) return false;
  if (access.superAdmin) return true;
  switch (resource) {
    case "identity":
      return hasCapability(access, "identity:retire");
    case "job-code":
      return hasCapability(access, "job-code:retire");
    case "birthright":
      return hasCapability(access, "birthright:retire");
    case "role":
      return hasCapability(access, "role:retire");
    case "entitlement":
      return hasCapability(access, "entitlement:retire");
    case "group":
      return hasCapability(access, "group:retire");
    case "incident":
      return hasCapability(access, "incident:retire");
    case "change":
      return hasCapability(access, "change:retire");
    case "event":
      return hasCapability(access, "event:retire");
    case "relationships":
      return hasCapability(access, "relationship:retire");
    case "ci":
      return hasCapability(access, "config-item:retire");
    case "location-map":
      return hasCapability(access, "location-map:retire");
    default:
      return false;
  }
}
