package cmdb

import "testing"

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
