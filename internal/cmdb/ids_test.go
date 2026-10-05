package cmdb

import (
	"errors"
	"strings"
	"testing"
)

func TestFormatGeneratedID(t *testing.T) {
	id, err := FormatGeneratedID(Group, 5)
	if err != nil || id != "GRP-000005" {
		t.Fatalf("FormatGeneratedID(group, 5) = %q %v, want GRP-000005", id, err)
	}
	id, err = FormatGeneratedID(Identity, 1)
	if err != nil || id != "IDN-000001" {
		t.Fatalf("FormatGeneratedID(identity, 1) = %q %v, want IDN-000001", id, err)
	}
	id, err = FormatGeneratedID(CI, 1000000)
	if err != nil || id != "CI-1000000" {
		t.Fatalf("wide sequence = %q %v, want CI-1000000", id, err)
	}
	if _, err := FormatGeneratedID(NodeKind("nope"), 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown kind error = %v, want ErrInvalid", err)
	}
}

func TestRetiredTypeNames(t *testing.T) {
	if RetiredTypeName("HAS_JOB_CODE") != "HAS_JOB_CODE_RETIRED" {
		t.Fatalf("RetiredTypeName = %q", RetiredTypeName("HAS_JOB_CODE"))
	}
	kind, retired, err := ParseStoredRelationshipType("HAS_JOB_CODE_RETIRED")
	if err != nil || kind != HasJobCode || !retired {
		t.Fatalf("parse retired type = %v %v %v", kind, retired, err)
	}
	kind, retired, err = ParseStoredRelationshipType("MEMBER")
	if err != nil || kind != Member || retired {
		t.Fatalf("parse live type = %v %v %v", kind, retired, err)
	}
	if _, _, err := ParseStoredRelationshipType("NOPE_RETIRED"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown type error = %v, want ErrInvalid", err)
	}
}

// Retiring a record only needs to visit the relationship types that can touch
// its kind; a task has five, an identity many more, and none include types
// that cannot reach the kind.
func TestRelationshipTypeNamesFor(t *testing.T) {
	task := RelationshipTypeNamesFor(Task)
	want := []string{"TASK_FOR", "TASK_STEP", "TASK_ITEM", "TASK_ASSIGNED_TO", "ACTED_BY"}
	if strings.Join(task, ",") != strings.Join(want, ",") {
		t.Fatalf("task types = %v, want %v", task, want)
	}
	identity := strings.Join(RelationshipTypeNamesFor(Identity), ",")
	for _, typeName := range []string{"HAS_JOB_CODE", "MEMBER", "PERMISSIONS", "ACCOUNTABLE", "TASK_ASSIGNED_TO", "STEP_ASSIGNED_TO"} {
		if !strings.Contains(identity, typeName) {
			t.Errorf("identity types %s missing %s", identity, typeName)
		}
	}
	if strings.Contains(identity, "HAS_STEP") || strings.Contains(identity, "HOSTS") {
		t.Errorf("identity types %s include types that cannot touch an identity", identity)
	}
}

func TestListOptionsValidate(t *testing.T) {
	if _, err := (ListOptions{Limit: -1}).Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative limit error = %v", err)
	}
	if _, err := (ListOptions{Offset: -1}).Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative offset error = %v", err)
	}
	options, err := (ListOptions{Query: "  ACME ", InvolvedID: " IDN-1 "}).Validate()
	if err != nil || options.Query != "acme" || options.InvolvedID != "IDN-1" {
		t.Fatalf("validated options = %#v, %v", options, err)
	}
}
