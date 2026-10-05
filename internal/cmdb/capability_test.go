package cmdb

import (
	"errors"
	"testing"
)

func TestAccessFromEntitlementsMapsAnthosTools(t *testing.T) {
	access := AccessFromEntitlements("IDN-000004", []string{
		"IAA-identities-read",
		"CAT-incidents-admin",
		"CAT-incidents-create", // covered by admin
		"CREQ-accessrequestform-use",
		"unknown-entitlement",
	})
	if access.SuperAdmin {
		t.Fatal("identity access must not be super admin")
	}
	if !access.Has(CapIdentityRead) || access.Has(CapIdentityCreate) {
		t.Fatalf("identity read-only = %v", access.Capabilities)
	}
	if !access.Has(CapIncidentCreate) || !access.Has(CapIncidentRetire) {
		t.Fatalf("incident admin missing = %v", access.Capabilities)
	}
	if !access.Has(CapAccessRequestUse) {
		t.Fatal("access request use missing")
	}
	if err := access.Require(CapIdentityCreate); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Require missing capability error = %v", err)
	}
}

func TestSuperAdminAccessHasEverything(t *testing.T) {
	access := SuperAdminAccess()
	if !access.SuperAdmin || access.ActorID != PlatformSuperAdminID {
		t.Fatalf("super admin = %+v", access)
	}
	if err := access.Require(CapIncidentRetire); err != nil {
		t.Fatalf("super admin should hold every capability: %v", err)
	}
	blank := AccessFromEntitlements("", nil)
	if !blank.SuperAdmin {
		t.Fatal("blank actor id should be treated as super admin for local tools")
	}
}

func TestNodeCapabilitiesCoverAnthosKinds(t *testing.T) {
	if cap, ok := NodeReadCapability(Incident); !ok || cap != CapIncidentRead {
		t.Fatalf("incident read = %s %v", cap, ok)
	}
	if cap, ok := NodeWriteCapability(Incident, "create"); !ok || cap != CapIncidentCreate {
		t.Fatalf("incident create = %s %v", cap, ok)
	}
	if _, ok := NodeWriteCapability(WorkflowRun, "create"); ok {
		t.Fatal("workflow-run writes should not be offered through node write")
	}
}

func TestMapReadEntitlementIsDedicated(t *testing.T) {
	mapOnly := AccessFromEntitlements("IDN-000001", []string{"CMDB-map-read"})
	if !mapOnly.Has(CapMapRead) || mapOnly.Has(CapConfigItemRead) || mapOnly.Has(CapRelationshipRead) {
		t.Fatalf("CMDB-map-read should grant only map:read, got %v", mapOnly.Capabilities)
	}
	relationships := AccessFromEntitlements("IDN-000001", []string{"CMDB-relationships-read"})
	if relationships.Has(CapMapRead) {
		t.Fatal("CMDB-relationships-read must not imply map:read")
	}
}
