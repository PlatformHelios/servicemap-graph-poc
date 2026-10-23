import { describe, expect, it } from "vitest";
import { canCreateResource, canRetireResource, canUpdateResource, canViewResource, hasCapability } from "./capabilities";
import type { ActorAccess } from "./types";

function access(capabilities: string[], superAdmin = false): ActorAccess {
  return { actorId: "IDENTITY-1", superAdmin, entitlements: [], capabilities };
}

describe("hasCapability", () => {
  it("denies a missing actor", () => {
    expect(hasCapability(null, "identity:read")).toBe(false);
    expect(hasCapability(undefined, "identity:read")).toBe(false);
  });

  it("lets super admin bypass every check", () => {
    expect(hasCapability(access([], true), "anything:at-all")).toBe(true);
  });

  it("matches only granted capabilities", () => {
    const actor = access(["identity:read"]);
    expect(hasCapability(actor, "identity:read")).toBe(true);
    expect(hasCapability(actor, "identity:update")).toBe(false);
  });
});

describe("canViewResource", () => {
  it("shows only home without an actor", () => {
    expect(canViewResource(null, "home")).toBe(true);
    expect(canViewResource(null, "identity")).toBe(false);
  });

  it("maps resources to their read capability", () => {
    expect(canViewResource(access(["config-item:read"]), "ci")).toBe(true);
    expect(canViewResource(access(["config-item:read"]), "identity")).toBe(false);
  });

  it("accepts either capability for the process mapper", () => {
    expect(canViewResource(access(["process-map:read"]), "process-mapper")).toBe(true);
    expect(canViewResource(access(["config-item:read"]), "process-mapper")).toBe(true);
    expect(canViewResource(access([]), "process-mapper")).toBe(false);
  });
});

describe("create, update, and retire", () => {
  it("require the matching capability", () => {
    const actor = access(["incident:create", "incident:update"]);
    expect(canCreateResource(actor, "incident")).toBe(true);
    expect(canUpdateResource(actor, "incident")).toBe(true);
    expect(canRetireResource(actor, "incident")).toBe(false);
  });

  it("are never offered on read-only resources", () => {
    const actor = access(["map:read"]);
    expect(canCreateResource(actor, "map")).toBe(false);
    expect(canUpdateResource(actor, "map")).toBe(false);
    expect(canRetireResource(actor, "map")).toBe(false);
  });
});
