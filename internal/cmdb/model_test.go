package cmdb

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseNodeKind(t *testing.T) {
	kind, err := ParseNodeKind("IDENTITY")
	if err != nil {
		t.Fatal(err)
	}
	if kind != Identity {
		t.Fatalf("ParseNodeKind() = %q, want %q", kind, Identity)
	}
	if _, err := ParseNodeKind("arbitrary-label"); err == nil {
		t.Fatal("ParseNodeKind() accepted an unsupported label")
	}
}

func TestParseRelationshipKind(t *testing.T) {
	kind, err := ParseRelationshipKind("HAS_JOB_CODE")
	if err != nil {
		t.Fatal(err)
	}
	if kind != HasJobCode {
		t.Fatalf("ParseRelationshipKind() = %q, want %q", kind, HasJobCode)
	}
}

func TestParseCMDBRelationshipKinds(t *testing.T) {
	for input, expected := range map[string]RelationshipKind{
		"Depends_On": DependsOn,
		"HOSTS":      Hosts,
		"Uses":       Uses,
		"governs":    Governs,
	} {
		got, err := ParseRelationshipKind(input)
		if err != nil {
			t.Errorf("ParseRelationshipKind(%q): %v", input, err)
			continue
		}
		if got != expected {
			t.Errorf("ParseRelationshipKind(%q) = %q, want %q", input, got, expected)
		}
		definition, err := RelationshipDefinitionFor(got)
		if err != nil {
			t.Fatal(err)
		}
		if definition.From != CI || definition.To != CI {
			t.Errorf("%s endpoints = %s -> %s, want CI -> CI", got, definition.From, definition.To)
		}
	}
	for _, legacy := range []string{"hosted-on", "used-by", "HOSTED_ON"} {
		if _, err := ParseRelationshipKind(legacy); err == nil {
			t.Errorf("ParseRelationshipKind(%q) accepted a retired relationship kind", legacy)
		}
	}
}

func TestRelationshipInverses(t *testing.T) {
	want := map[RelationshipKind]string{
		HasJobCode:   "job-code-for",
		WorkLocation: "work-location-for",
		QualifiesFor: "qualified-by",
		Grants:       "granted-by",
		Includes:     "included-by",
		Affects:      "affected-by",
		Changes:      "changed-by",
		AssignedTo:   "assignee-of",
		ObservedOn:   "observed",
		DependsOn:    "depended-on-by",
		Hosts:        "hosted-by",
		Uses:         "used-by",
		Governs:      "governed-by",
		Provides:     "provided-by",
		Member:       "member-of",
		DraftedRequest: "drafted-request-for",
		FormSubmitted:  "submitted-by",
		HasStep:        "step-of",
		NextStep:       "previous-step",
		StepAssignedTo: "assigned-step",
		RunFor:         "has-run",
		InstanceOf:     "has-instance",
		TaskFor:        "has-task",
		TaskStep:       "step-task",
		TaskAssignedTo: "assigned-task",
		ActedBy:        "acted-on",
		FulfilledBy:    "fulfills",
		HasRole:        "role-for",
		EntitledBy:     "entitlement-for",
		Permissions:    "permissioned-by",
		RequestedFor:   "subject-of",
		RequestsAccess: "requested-on",
		TaskItem:       "item-task",
		ForCatalogItem:  "has-catalog-request",
		CatalogOwnedBy:  "owns-catalog-item",
		VisibleTo:       "sees-catalog-item",
		ApprovedThrough: "approves-catalog-item",
		References:      "referenced-by",
		Accountable:  "accountable-for",
		Responsible:  "responsible-for",
		Consulted:    "consulted-on",
		Informed:     "informed-of",
	}
	for _, kind := range RelationshipKinds() {
		definition, err := RelationshipDefinitionFor(kind)
		if err != nil {
			t.Fatal(err)
		}
		if definition.Inverse == "" || definition.Inverse != want[kind] {
			t.Errorf("%s inverse = %q, want %q", kind, definition.Inverse, want[kind])
		}
		if RelationshipLabel(kind, true) != string(kind) || RelationshipLabel(kind, false) != want[kind] {
			t.Errorf("RelationshipLabel(%s) = %q / %q", kind, RelationshipLabel(kind, true), RelationshipLabel(kind, false))
		}
	}
	governs, _ := RelationshipDefinitionFor(Governs)
	if len(governs.FromCITypes) != 1 || governs.FromCITypes[0] != Contract {
		t.Errorf("governs fromCITypes = %q, want [contract]", governs.FromCITypes)
	}
	workLocation, _ := RelationshipDefinitionFor(WorkLocation)
	if workLocation.From != Identity || workLocation.To != CI || len(workLocation.ToCITypes) != 1 || workLocation.ToCITypes[0] != Location {
		t.Errorf("work-location definition = %+v, want identity -> location CI", workLocation)
	}
	member, _ := RelationshipDefinitionFor(Member)
	if member.From != Group || member.To != Identity || member.TypeName != "MEMBER" || len(member.ToKinds) != 1 || member.ToKinds[0] != Identity {
		t.Errorf("member definition = %+v, want group -> identity", member)
	}
	accountable, _ := RelationshipDefinitionFor(Accountable)
	if accountable.From != CI || len(accountable.ToKinds) != 1 || accountable.ToKinds[0] != Identity {
		t.Errorf("accountable definition = %+v, want ci -> identity only", accountable)
	}
	for _, kind := range []RelationshipKind{Responsible, Consulted, Informed} {
		definition, _ := RelationshipDefinitionFor(kind)
		if definition.From != CI || definition.To != Identity || len(definition.ToKinds) != 2 || definition.ToKinds[0] != Identity || definition.ToKinds[1] != Group {
			t.Errorf("%s definition = %+v, want ci -> identity or group", kind, definition)
		}
	}
	if raci := RACIRelationshipKinds(); len(raci) != 4 || raci[0] != Accountable || raci[3] != Informed {
		t.Errorf("RACIRelationshipKinds() = %v", raci)
	}
	grants, _ := RelationshipDefinitionFor(Grants)
	if grants.From != Birthright || grants.To != Role || len(grants.ToKinds) != 2 || grants.ToKinds[0] != Role || grants.ToKinds[1] != Entitlement {
		t.Errorf("grants definition = %+v, want birthright -> role or entitlement", grants)
	}
	assigned, _ := RelationshipDefinitionFor(AssignedTo)
	if assigned.From != Incident || assigned.TypeName != "ASSIGNED_TO" || len(assigned.ToKinds) != 2 || assigned.ToKinds[0] != Identity || assigned.ToKinds[1] != Group {
		t.Errorf("assigned-to definition = %+v, want incident -> identity or group", assigned)
	}
	drafted, _ := RelationshipDefinitionFor(DraftedRequest)
	if drafted.From != Identity || drafted.To != Request || drafted.TypeName != "DRAFTED_REQUEST" || len(drafted.ToKinds) != 1 {
		t.Errorf("drafted-request definition = %+v, want identity -> request", drafted)
	}
	formSubmitted, _ := RelationshipDefinitionFor(FormSubmitted)
	if formSubmitted.From != Identity || formSubmitted.To != Request || formSubmitted.TypeName != "FORM_SUBMITTED" {
		t.Errorf("form-submitted definition = %+v, want identity -> request", formSubmitted)
	}
	provides, _ := RelationshipDefinitionFor(Provides)
	if provides.From != CI || provides.To != CI || strings.Join(CITypeNamesOf(provides.FromCITypes), ",") != "service,function,vendor" || strings.Join(CITypeNamesOf(provides.ToCITypes), ",") != "service,function,application,server,printer" || len(provides.CITypeRules) != 2 {
		t.Errorf("provides definition = %+v, want service/function and vendor pairing rules", provides)
	}
	for _, pairing := range []struct {
		from, to CIType
		allowed  bool
	}{
		{ServiceCI, Function, true}, {Function, ServiceCI, true}, {ServiceCI, ServiceCI, true},
		{Vendor, Application, true}, {Vendor, Server, true}, {Vendor, Printer, true},
		{Vendor, ServiceCI, false}, {ServiceCI, Server, false}, {Application, Vendor, false}, {Server, Server, false},
	} {
		if got := provides.AllowsCITypes(pairing.from, pairing.to); got != pairing.allowed {
			t.Errorf("provides.AllowsCITypes(%s, %s) = %v, want %v", pairing.from, pairing.to, got, pairing.allowed)
		}
	}
	if governs.AllowsCITypes(Contract, Server) != true || governs.AllowsCITypes(Server, Contract) != false || len(governs.CITypeRules) != 1 {
		t.Errorf("governs rules = %+v, want contract -> any", governs.CITypeRules)
	}
	if hosts, _ := RelationshipDefinitionFor(Hosts); !hosts.AllowsCITypes(Vendor, Vendor) || len(hosts.CITypeRules) != 0 {
		t.Errorf("hosts should be unrestricted, got %+v", hosts.CITypeRules)
	}
	if kind, ciType, ok := LinkedCIRelationship(Identity); !ok || kind != WorkLocation || ciType != Location {
		t.Errorf("LinkedCIRelationship(identity) = %q, %q, %v", kind, ciType, ok)
	}
	if _, _, ok := LinkedCIRelationship(Role); ok {
		t.Error("LinkedCIRelationship(role) should not be linkable")
	}
	if names := RelationshipTypeNames(); len(names) != len(RelationshipKinds()) || names[len(names)-1] != "INFORMED" {
		t.Errorf("RelationshipTypeNames() = %v", names)
	}
}

func TestApplicationHosting(t *testing.T) {
	if err := validateCIProperties(CI, map[string]any{"ciType": "application", "name": "Payroll"}, true); !errors.Is(err, ErrInvalid) {
		t.Errorf("application without hosted error = %v, want ErrInvalid", err)
	}
	properties := map[string]any{"ciType": "application", "name": "Payroll", "description": "Runs payroll", "hosted": "External"}
	if err := validateCIProperties(CI, properties, true); err != nil {
		t.Fatalf("application with hosted: %v", err)
	}
	if properties["hosted"] != "external" {
		t.Errorf("hosted was not normalized: %#v", properties["hosted"])
	}
	if err := validateCIProperties(CI, map[string]any{"ciType": "application", "hosted": "cloud"}, true); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown hosted error = %v, want ErrInvalid", err)
	}
	if err := validateCIProperties(CI, map[string]any{"hosted": "internal"}, false); err != nil {
		t.Errorf("update hosted alone: %v", err)
	}
	if err := validateCIProperties(Identity, map[string]any{"hosted": "internal"}, false); !errors.Is(err, ErrInvalid) {
		t.Errorf("hosted on non-CI error = %v, want ErrInvalid", err)
	}
	// Other hardware/software CIs carry name and description without a hosted value.
	for _, ciType := range []string{"server", "printer", "data-connector"} {
		if err := validateCIProperties(CI, map[string]any{"ciType": ciType, "name": "x", "description": "y"}, true); err != nil {
			t.Errorf("create %s: %v", ciType, err)
		}
	}
}

func TestContractCIs(t *testing.T) {
	if err := validateCIProperties(CI, map[string]any{"ciType": "contract", "name": "Vendor MSA"}, true); !errors.Is(err, ErrInvalid) {
		t.Errorf("contract without contractType error = %v, want ErrInvalid", err)
	}
	properties := map[string]any{"ciType": "Contract", "name": "Vendor MSA", "contractType": "MSA"}
	if err := validateCIProperties(CI, properties, true); err != nil {
		t.Fatalf("contract with contractType: %v", err)
	}
	if properties["ciType"] != "contract" || properties["contractType"] != "msa" {
		t.Errorf("contract properties were not normalized: %#v", properties)
	}
	if err := validateCIProperties(CI, map[string]any{"ciType": "contract", "contractType": "lease"}, true); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown contractType error = %v, want ErrInvalid", err)
	}
	if err := validateCIProperties(Identity, map[string]any{"contractType": "nda"}, false); !errors.Is(err, ErrInvalid) {
		t.Errorf("contractType on non-CI error = %v, want ErrInvalid", err)
	}
	for _, input := range []string{"Basic Contract", "msa", " NDA "} {
		if _, err := ParseContractType(input); err != nil {
			t.Errorf("ParseContractType(%q): %v", input, err)
		}
	}
}

func TestVendorCriticality(t *testing.T) {
	if err := validateCIProperties(CI, map[string]any{"ciType": "vendor", "name": "Acme"}, true); !errors.Is(err, ErrInvalid) {
		t.Errorf("vendor without criticality error = %v, want ErrInvalid", err)
	}
	properties := map[string]any{"ciType": "Vendor", "name": "Acme", "description": "Payments processor", "criticality": "Business Support"}
	if err := validateCIProperties(CI, properties, true); err != nil {
		t.Fatalf("vendor with criticality: %v", err)
	}
	if properties["ciType"] != "vendor" || properties["criticality"] != "business-support" {
		t.Errorf("vendor properties were not normalized: %#v", properties)
	}
	if err := validateCIProperties(CI, map[string]any{"ciType": "vendor", "criticality": "low"}, true); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown criticality error = %v, want ErrInvalid", err)
	}
	// Vendor contact details are optional, but an email must be well formed and
	// a phone number is stored in its canonical format.
	contact := map[string]any{"ciType": "vendor", "name": "Acme", "criticality": "critical", "contactName": "Pat Lee", "contactPhone": "555.010.0100", "contactEmail": "Pat@Acme.example"}
	if err := validateCIProperties(CI, contact, true); err != nil || contact["contactEmail"] != "pat@acme.example" || contact["contactPhone"] != "(555) 010-0100" {
		t.Errorf("vendor contact: err=%v properties=%#v", err, contact)
	}
	if err := validateCIProperties(CI, map[string]any{"contactEmail": "pat@"}, false); !errors.Is(err, ErrInvalid) {
		t.Errorf("malformed contactEmail error = %v, want ErrInvalid", err)
	}
	if err := validateCIProperties(CI, map[string]any{"contactPhone": "call Pat"}, false); !errors.Is(err, ErrInvalid) {
		t.Errorf("malformed contactPhone error = %v, want ErrInvalid", err)
	}
	phones := map[string]string{
		"5550100100":          "(555) 010-0100",
		"(555) 010-0100":      "(555) 010-0100",
		"1-555-010-0100":      "(555) 010-0100",
		"+1 555 010 0100":     "(555) 010-0100",
		"+44 20 7123 4567":    "+442071234567",
		"555-0100":            "",
		"55501001000":         "",
		"+1234":               "",
		"555-010-0100 ext. 7": "",
		"":                    "",
	}
	for input, want := range phones {
		got, ok := NormalizePhoneNumber(input)
		if ok != (want != "") || got != want {
			t.Errorf("NormalizePhoneNumber(%q) = %q, %v; want %q", input, got, ok, want)
		}
	}
	if names := ContactProperties(); strings.Join(names, ",") != "contactName,contactPhone,contactEmail" {
		t.Errorf("ContactProperties() = %v", names)
	}
	if err := validateCIProperties(Identity, map[string]any{"criticality": "critical"}, false); !errors.Is(err, ErrInvalid) {
		t.Errorf("criticality on non-CI error = %v, want ErrInvalid", err)
	}
	if names := CriticalityNames(); strings.Join(names, ",") != "critical,important,business-support" {
		t.Errorf("CriticalityNames() = %v", names)
	}
}

// requestStore serves one stored request and records whether a save went
// through UpdateNode or SubmitRequest.
type requestStore struct {
	Store
	existing  map[string]any
	saved     map[string]any
	submitted bool
}

func (r *requestStore) CreateGeneratedNode(_ context.Context, kind NodeKind, properties map[string]any, _ []string) (*Node, error) {
	r.saved = properties
	return &Node{Kind: kind, ID: "REQ-000001", Properties: properties, Status: "active"}, nil
}

func (r *requestStore) GetNode(_ context.Context, kind NodeKind, id string, _ bool) (*Node, error) {
	return &Node{Kind: kind, ID: id, Properties: r.existing, Status: "active"}, nil
}

func (r *requestStore) UpdateNode(_ context.Context, kind NodeKind, id string, properties map[string]any) (*Node, error) {
	r.saved = properties
	return &Node{Kind: kind, ID: id, Properties: properties, Status: "active"}, nil
}

func (r *requestStore) SubmitRequest(_ context.Context, id string, properties map[string]any) (*Node, error) {
	r.saved = properties
	r.submitted = true
	return &Node{Kind: Request, ID: id, Properties: properties, Status: "active"}, nil
}

// No workflows are defined, so submitted requests stay submitted.
func (r *requestStore) ListWorkflows(context.Context, bool) ([]WorkflowDefinition, error) {
	return nil, nil
}

func (r *requestStore) ListHolidays(context.Context) ([]Holiday, error) {
	return nil, nil
}

func TestVendorRequestLifecycle(t *testing.T) {
	if _, err := ParseNodeKind("request"); err != nil {
		t.Fatal(err)
	}
	if names := RequestTypeNames(); strings.Join(names, ",") != "vendor,access,catalog" {
		t.Errorf("RequestTypeNames() = %v", names)
	}
	if names := RequestStateNames(); strings.Join(names, ",") != "draft,submitted,in-review,fulfilled,denied,in-progress,failed" {
		t.Errorf("RequestStateNames() = %v", names)
	}
	if required := RequestRequiredProperties(VendorRequest); strings.Join(required, ",") != "name" {
		t.Errorf("RequestRequiredProperties(vendor) = %v", required)
	}

	// Drafts may be incomplete, but need a requestType and start in draft.
	store := &requestStore{}
	service := NewService(store)
	if _, err := service.CreateGeneratedNode(context.Background(), Request, map[string]any{"requestType": "Vendor", "name": "Acme"}, nil); err != nil {
		t.Fatalf("create draft: %v", err)
	}
	if store.saved["requestType"] != "vendor" || store.saved["state"] != "draft" {
		t.Errorf("draft was not normalized: %#v", store.saved)
	}
	for name, properties := range map[string]map[string]any{
		"missing type":         {"name": "Acme"},
		"unknown type":         {"requestType": "server"},
		"created as submitted": {"requestType": "vendor", "state": "submitted", "name": "Acme"},
		"ci-only property":     {"requestType": "vendor", "ciType": "vendor"},
		"bad criticality":      {"requestType": "vendor", "criticality": "low"},
		"caller submittedAt":   {"requestType": "vendor", "submittedAt": "now"},
		"bad contact email":    {"requestType": "vendor", "contactEmail": "not-an-address"},
		"contact not text":     {"requestType": "vendor", "contactPhone": 5551234},
	} {
		if _, err := service.CreateGeneratedNode(context.Background(), Request, properties, nil); !errors.Is(err, ErrInvalid) {
			t.Errorf("create request (%s) error = %v, want ErrInvalid", name, err)
		}
	}
	if _, err := service.CreateGeneratedNode(context.Background(), Request, map[string]any{"requestType": "vendor"}, []string{"CI-1"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("request with ciIds error = %v, want ErrInvalid", err)
	}

	// Submitting checks the stored draft plus the update for every required property.
	store = &requestStore{existing: map[string]any{"requestType": "vendor", "state": "draft", "description": "Card processing"}}
	service = NewService(store)
	if _, err := service.UpdateNode(context.Background(), Request, "REQ-000001", map[string]any{"state": "submitted"}); !errors.Is(err, ErrInvalid) || store.submitted {
		t.Errorf("submit without a vendor name error = %v (submitted=%v), want ErrInvalid", err, store.submitted)
	}
	if _, err := service.UpdateNode(context.Background(), Request, "REQ-000001", map[string]any{"state": "submitted", "name": "Acme", "contactName": "Pat Lee", "contactEmail": " Pat.Lee@Acme.example "}); err != nil {
		t.Fatalf("submit complete request: %v", err)
	}
	if !store.submitted || store.saved["contactEmail"] != "pat.lee@acme.example" || store.saved["contactName"] != "Pat Lee" {
		t.Errorf("submit did not go through SubmitRequest with normalized properties: submitted=%v saved=%#v", store.submitted, store.saved)
	}
	if _, ok := store.saved["state"]; ok {
		t.Errorf("state should be set by the store on submit, got %#v", store.saved)
	}
	// Plain draft edits stay drafts and never touch the requester links.
	store.submitted = false
	if _, err := service.UpdateNode(context.Background(), Request, "REQ-000001", map[string]any{"description": "Card processing"}); err != nil || store.submitted {
		t.Errorf("draft edit: err=%v submitted=%v", err, store.submitted)
	}

	// Submitted requests remain editable but cannot go back to draft.
	store = &requestStore{existing: map[string]any{"requestType": "vendor", "state": "submitted", "name": "Acme", "criticality": "critical"}}
	service = NewService(store)
	if _, err := service.UpdateNode(context.Background(), Request, "REQ-000001", map[string]any{"state": "draft"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("revert to draft error = %v, want ErrInvalid", err)
	}
	if _, err := service.UpdateNode(context.Background(), Request, "REQ-000001", map[string]any{"state": "submitted", "description": "edited by admin"}); err != nil || store.submitted {
		t.Errorf("edit submitted request: err=%v submitted=%v", err, store.submitted)
	}
}

func TestParseCIType(t *testing.T) {
	for _, input := range []string{"Server", "printer", "Data Connector", "Application", "Service", "Process", "function", "Location", "Contract", "Vendor"} {
		if _, err := ParseCIType(input); err != nil {
			t.Errorf("ParseCIType(%q): %v", input, err)
		}
	}
	if _, err := ParseCIType("Laptop"); err == nil {
		t.Fatal("ParseCIType accepted an ungoverned CI type")
	}
}

func TestParseCICategory(t *testing.T) {
	for _, input := range []string{"Business", "technology", " Security "} {
		if _, err := ParseCICategory(input); err != nil {
			t.Errorf("ParseCICategory(%q): %v", input, err)
		}
	}
	if _, err := ParseCICategory("finance"); err == nil {
		t.Fatal("ParseCICategory accepted an ungoverned category")
	}
}

func TestCategoryRequiredForServiceCIs(t *testing.T) {
	if _, err := ParseCIType("business-process"); err == nil {
		t.Fatal("ParseCIType accepted the retired business-process type")
	}
	if legacy := LegacyCITypes(); len(legacy) != 1 || legacy[0].OldType != "business-process" || legacy[0].NewType != Process {
		t.Fatalf("LegacyCITypes() = %#v, want business-process -> process", legacy)
	}
	for _, ciType := range []string{"service", "process", "function"} {
		if err := validateCIProperties(CI, map[string]any{"ciType": ciType, "name": "x"}, true); !errors.Is(err, ErrInvalid) {
			t.Errorf("create %s without category error = %v, want ErrInvalid", ciType, err)
		}
		properties := map[string]any{"ciType": ciType, "name": "x", "category": "Technology"}
		if err := validateCIProperties(CI, properties, true); err != nil {
			t.Errorf("create %s with category: %v", ciType, err)
		}
		if properties["category"] != "technology" {
			t.Errorf("category was not normalized: %#v", properties["category"])
		}
		if err := validateCIProperties(CI, map[string]any{"description": "updated"}, false); err != nil {
			t.Errorf("update %s without category: %v", ciType, err)
		}
	}
	if err := validateCIProperties(CI, map[string]any{"ciType": "server"}, true); err != nil {
		t.Errorf("server without category: %v", err)
	}
	if err := validateCIProperties(CI, map[string]any{"ciType": "service", "category": "finance"}, true); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown category error = %v, want ErrInvalid", err)
	}
	if err := validateCIProperties(Identity, map[string]any{"category": "business"}, false); !errors.Is(err, ErrInvalid) {
		t.Errorf("category on non-CI error = %v, want ErrInvalid", err)
	}
}

func TestValidateCIProperties(t *testing.T) {
	properties := map[string]any{"ciType": "Data Connector"}
	if err := validateCIProperties(CI, properties, true); err != nil {
		t.Fatal(err)
	}
	if properties["ciType"] != "data-connector" {
		t.Fatalf("ciType was not normalized: %#v", properties["ciType"])
	}
	if properties["critical"] != false {
		t.Fatalf("critical default = %#v, want false", properties["critical"])
	}
	if err := validateCIProperties(CI, map[string]any{}, true); err == nil {
		t.Fatal("CI without ciType was accepted")
	}
	if err := validateCIProperties(Identity, map[string]any{"ciType": "server"}, false); err == nil {
		t.Fatal("ciType on a non-CI node was accepted")
	}
}

func TestCriticalAndNodeDownFlags(t *testing.T) {
	ci := map[string]any{"ciType": "server", "name": "app-01", "critical": true}
	if err := validateCIProperties(CI, ci, true); err != nil {
		t.Fatalf("CI with critical: %v", err)
	}
	if ci["critical"] != true {
		t.Fatalf("critical = %#v, want true", ci["critical"])
	}
	if err := validateCIProperties(CI, map[string]any{"critical": "yes"}, false); !errors.Is(err, ErrInvalid) {
		t.Errorf("non-bool critical error = %v, want ErrInvalid", err)
	}
	if err := validateCIProperties(Identity, map[string]any{"critical": true}, false); !errors.Is(err, ErrInvalid) {
		t.Errorf("critical on non-CI error = %v, want ErrInvalid", err)
	}
	incident := map[string]any{"name": "Outage", "nodeDown": true}
	if err := validateCIProperties(Incident, incident, true); err != nil {
		t.Fatalf("incident with nodeDown: %v", err)
	}
	if incident["nodeDown"] != true {
		t.Fatalf("nodeDown = %#v, want true", incident["nodeDown"])
	}
	plain := map[string]any{"name": "Noise"}
	if err := validateCIProperties(Incident, plain, true); err != nil {
		t.Fatalf("incident create defaults: %v", err)
	}
	if plain["nodeDown"] != false {
		t.Fatalf("nodeDown default = %#v, want false", plain["nodeDown"])
	}
	if err := validateCIProperties(CI, map[string]any{"nodeDown": true}, false); !errors.Is(err, ErrInvalid) {
		t.Errorf("nodeDown on CI error = %v, want ErrInvalid", err)
	}
	if err := validateCIProperties(Incident, map[string]any{"critical": true}, false); !errors.Is(err, ErrInvalid) {
		t.Errorf("critical on incident error = %v, want ErrInvalid", err)
	}
}

func TestCreateCIRequiresFixedType(t *testing.T) {
	service := NewService(nil)
	for name, properties := range map[string]map[string]any{
		"missing type": {},
		"unknown type": {"ciType": "laptop"},
	} {
		if _, err := service.CreateGeneratedNode(context.Background(), CI, properties, nil); !errors.Is(err, ErrInvalid) {
			t.Errorf("CreateGeneratedNode(CI, %s) error = %v, want ErrInvalid", name, err)
		}
	}
	if _, err := service.CreateGeneratedNode(context.Background(), CI, map[string]any{"ciType": "server"}, []string{"ci-1"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("CreateGeneratedNode(CI, linked CI) error = %v, want ErrInvalid", err)
	}
	if _, err := service.CreateGeneratedNode(context.Background(), Incident, map[string]any{"ciType": "server"}, []string{"ci-1"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("CreateGeneratedNode(incident with ciType) error = %v, want ErrInvalid", err)
	}
}

func TestIncidentAndChangeRequireCIsAtCreation(t *testing.T) {
	service := NewService(nil)
	for _, kind := range []NodeKind{Incident, Change} {
		if _, err := service.CreateGeneratedNode(context.Background(), kind, nil, nil); !errors.Is(err, ErrInvalid) {
			t.Errorf("CreateGeneratedNode(%s, no CIs) error = %v, want ErrInvalid", kind, err)
		}
	}
	if _, err := service.CreateGeneratedNode(context.Background(), Incident, nil, []string{" ", "ci-1"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("CreateGeneratedNode(blank CI) error = %v, want ErrInvalid", err)
	}
}

func TestGeneratedIDPrefixes(t *testing.T) {
	for kind, want := range map[NodeKind]string{Identity: "IDN", JobCode: "JOB", Birthright: "BIR", Role: "ROLE", Entitlement: "ENT", Group: "GRP", CI: "CI", Incident: "INC", Change: "CHG", Event: "EVT", Request: "REQ"} {
		if !RequiresGeneratedID(kind) {
			t.Errorf("RequiresGeneratedID(%s) = false, want true", kind)
		}
		got, err := GeneratedIDPrefix(kind)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("GeneratedIDPrefix(%s) = %q, want %q", kind, got, want)
		}
	}
	if _, err := GeneratedIDPrefix(NodeKind("unsupported")); !errors.Is(err, ErrInvalid) {
		t.Errorf("GeneratedIDPrefix(unsupported) error = %v, want ErrInvalid", err)
	}
}

func TestNormalizeCIIDs(t *testing.T) {
	got, err := normalizeCIIDs([]string{" ci-1 ", "ci-1", "ci-2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "ci-1" || got[1] != "ci-2" {
		t.Fatalf("normalizeCIIDs() = %#v", got)
	}
}

func TestValidateProperties(t *testing.T) {
	properties, err := validateProperties(map[string]any{"department": "Platform Engineering", "active": true, "level": float64(3)})
	if err != nil {
		t.Fatal(err)
	}
	if len(properties) != 3 {
		t.Fatalf("validateProperties() returned %d properties, want 3", len(properties))
	}

	for _, properties := range []map[string]any{
		{"id": "override"},
		{"status": "active"},
		{"metadata": map[string]any{"nested": true}},
		{"optional": nil},
	} {
		if _, err := validateProperties(properties); err == nil {
			t.Errorf("validateProperties(%v) accepted invalid properties", properties)
		}
	}
}
