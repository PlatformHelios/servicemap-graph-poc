import type { GraphNode } from "./types";

// Who is signed in to the portal. In the final rollout this comes from an
// Entra ID single sign-on, and what the user can see follows from it. Until
// then the portal mocks a sign-in: the development user is the platform super
// admin, who has access to everything and may act as any configured identity
// to see that identity's profile and work queue.

export const superAdmin = { id: "platform-super-admin", name: "Platform Super Admin" } as const;

// The identity the super admin is acting as, kept for the browser session.
// Empty means the super admin is acting as themselves.
const actingAsKey = "servicemap.actingAs";

export function storedActingAs(): string {
  return window.sessionStorage.getItem(actingAsKey) ?? "";
}

export function storeActingAs(identityId: string) {
  if (identityId) window.sessionStorage.setItem(actingAsKey, identityId);
  else window.sessionStorage.removeItem(actingAsKey);
}

export interface SessionUser {
  id: string;
  name: string;
  superAdmin: boolean; // true while the super admin acts as themselves
  identity?: GraphNode; // the configured identity being acted as
}

export function sessionUser(actingAs: string, identities: GraphNode[]): SessionUser {
  const identity = actingAs ? identities.find((node) => node.id === actingAs) : undefined;
  if (!actingAs) return { id: superAdmin.id, name: superAdmin.name, superAdmin: true };
  const name = typeof identity?.properties?.name === "string" ? identity.properties.name : actingAs;
  return { id: actingAs, name, superAdmin: false, identity };
}
