package cmdb

import (
	"context"
	"errors"
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
		"Hosted-On":  HostedOn,
		"Uses":       Uses,
		"Used-By":    UsedBy,
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
}

func TestParseCIType(t *testing.T) {
	for _, input := range []string{"Server", "printer", "Data Connector", "Application"} {
		if _, err := ParseCIType(input); err != nil {
			t.Errorf("ParseCIType(%q): %v", input, err)
		}
	}
	if _, err := ParseCIType("Laptop"); err == nil {
		t.Fatal("ParseCIType accepted an ungoverned CI type")
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
	if err := validateCIProperties(CI, map[string]any{}, true); err == nil {
		t.Fatal("CI without ciType was accepted")
	}
	if err := validateCIProperties(Identity, map[string]any{"ciType": "server"}, false); err == nil {
		t.Fatal("ciType on a non-CI node was accepted")
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
	for kind, want := range map[NodeKind]string{Identity: "IDN", JobCode: "JOB", Birthright: "BIR", Role: "ROLE", Entitlement: "ENT", CI: "CI", Incident: "INC", Change: "CHG", Event: "EVT"} {
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
