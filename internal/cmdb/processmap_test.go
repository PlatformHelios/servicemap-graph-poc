package cmdb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type processMapStore struct {
	Store
	nodes         map[string]*Node
	relationships []Relationship
	nextCI        int
}

func newProcessMapStore() *processMapStore {
	return &processMapStore{
		nodes: map[string]*Node{
			"CI-PROC": {
				Kind: CI, ID: "CI-PROC", Status: "active",
				Properties: map[string]any{"ciType": "process", "name": "Onboard vendor", "category": "business", "description": "Vendor intake"},
			},
		},
		nextCI: 1,
	}
}

func (p *processMapStore) GetNode(_ context.Context, kind NodeKind, id string, _ bool) (*Node, error) {
	node, ok := p.nodes[id]
	if !ok || node.Kind != kind {
		return nil, ErrNotFound
	}
	copyNode := *node
	copyNode.Properties = copyMap(node.Properties)
	copyNode.Relationships = nil
	for _, rel := range p.relationships {
		if rel.Status != "active" {
			continue
		}
		if rel.FromID == id || rel.ToID == id {
			copyNode.Relationships = append(copyNode.Relationships, rel)
		}
	}
	return &copyNode, nil
}

func (p *processMapStore) CreateGeneratedNode(_ context.Context, kind NodeKind, properties map[string]any, ciIDs []string) (*Node, error) {
	p.nextCI++
	prefix, err := GeneratedIDPrefix(kind)
	if err != nil {
		prefix = "CI"
	}
	id := fmt.Sprintf("%s-%06d", prefix, p.nextCI)
	node := &Node{Kind: kind, ID: id, Status: "active", Properties: copyMap(properties)}
	p.nodes[id] = node
	if relKind, _, ok := LinkedCIRelationship(kind); ok {
		for _, ciID := range ciIDs {
			p.relationships = append(p.relationships, Relationship{Kind: relKind, FromID: id, ToID: ciID, Status: "active"})
		}
	}
	return node, nil
}

func (p *processMapStore) UpdateNode(_ context.Context, kind NodeKind, id string, properties map[string]any) (*Node, error) {
	node, ok := p.nodes[id]
	if !ok || node.Kind != kind {
		return nil, ErrNotFound
	}
	for key, value := range properties {
		node.Properties[key] = value
	}
	return node, nil
}

func (p *processMapStore) CreateRelationship(_ context.Context, kind RelationshipKind, fromID, toID string, properties map[string]any) (*Relationship, error) {
	for i, rel := range p.relationships {
		if rel.Kind == kind && rel.FromID == fromID && rel.ToID == toID && rel.Status == "active" {
			if properties != nil {
				p.relationships[i].Properties = copyMap(properties)
			}
			return &p.relationships[i], nil
		}
	}
	rel := Relationship{Kind: kind, FromID: fromID, ToID: toID, Status: "active", Properties: copyMap(properties)}
	p.relationships = append(p.relationships, rel)
	return &rel, nil
}

func (p *processMapStore) RetireRelationship(_ context.Context, kind RelationshipKind, fromID, toID string) (*Relationship, error) {
	for i, rel := range p.relationships {
		if rel.Kind == kind && rel.FromID == fromID && rel.ToID == toID && rel.Status == "active" {
			p.relationships[i].Status = "retired"
			return &p.relationships[i], nil
		}
	}
	return nil, ErrNotFound
}

func copyMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func TestSaveProcessMapCreatesStepsAndLinks(t *testing.T) {
	store := newProcessMapStore()
	store.nextCI = 10
	service := NewService(store)
	saved, err := service.SaveProcessMap(context.Background(), "CI-PROC", ProcessMap{
		Name:               "Onboard vendor",
		TriggerDescription: "Vendor request submitted",
		TriggerActions:     []string{"form-submitted", "email-received"},
		OutputDescription:  "Vendor record ready",
		OutputActions:      []string{"record-created", "notification-sent"},
		Steps: []ProcessMapStep{
			{Name: "Collect paperwork", Execution: "manual", Performers: []ProcessMapPerformer{{Kind: "identity", ID: "IDN-1"}}},
			{Name: "Security review", Execution: "manual", Performers: []ProcessMapPerformer{{Kind: "application", ID: "CI-APP"}}},
			{Name: "Load files", Execution: "automated", CreatesCase: true, Performers: []ProcessMapPerformer{{Kind: "automation", ID: "CI-AUTO"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(saved.Steps))
	}
	if saved.Steps[2].CreatesCase {
		t.Fatal("automated execution must clear createsCase")
	}
	var autoUses, appUses int
	for _, rel := range store.relationships {
		if rel.Status != "active" || rel.Kind != Uses {
			continue
		}
		if rel.ToID == "CI-AUTO" {
			autoUses++
		}
		if rel.ToID == "CI-APP" {
			appUses++
		}
	}
	if autoUses != 1 {
		t.Fatalf("automation uses links = %d, want 1", autoUses)
	}
	if appUses != 1 {
		t.Fatalf("application uses links = %d, want 1", appUses)
	}
	if saved.ProcessID != "CI-PROC" || saved.TriggerDescription != "Vendor request submitted" {
		t.Fatalf("process fields = %+v", saved)
	}
	if len(saved.TriggerActions) != 2 || saved.TriggerActions[0] != "form-submitted" || saved.TriggerActions[1] != "email-received" {
		t.Fatalf("triggerActions = %v", saved.TriggerActions)
	}
	if len(saved.OutputActions) != 2 || saved.OutputActions[0] != "record-created" {
		t.Fatalf("outputActions = %v", saved.OutputActions)
	}
	if saved.OutputDescription != "Vendor record ready" {
		t.Fatalf("outputDescription = %q", saved.OutputDescription)
	}
	if saved.Steps[0].Order != 1 || saved.Steps[1].Order != 2 || saved.Steps[2].Order != 3 {
		t.Fatalf("orders = %d, %d", saved.Steps[0].Order, saved.Steps[1].Order)
	}
	var nextCount, processStepCount int
	wantNext := map[string]string{
		saved.Steps[0].ID: saved.Steps[1].ID,
		saved.Steps[1].ID: saved.Steps[2].ID,
	}
	for _, rel := range store.relationships {
		if rel.Status != "active" {
			continue
		}
		switch rel.Kind {
		case NextInProcess:
			nextCount++
			if wantNext[rel.FromID] != rel.ToID {
				t.Fatalf("next-in-process = %s -> %s", rel.FromID, rel.ToID)
			}
		case ProcessStep:
			processStepCount++
			if rel.FromID != "CI-PROC" || rel.ToID != saved.Steps[0].ID {
				t.Fatalf("process-step = %s -> %s, want CI-PROC -> %s", rel.FromID, rel.ToID, saved.Steps[0].ID)
			}
		case TriggeredBy, Produces:
			if rel.FromID == "CI-PROC" {
				t.Fatalf("unexpected CI trigger/output link %s %s->%s", rel.Kind, rel.FromID, rel.ToID)
			}
		}
	}
	if nextCount != 2 {
		t.Fatalf("next-in-process count = %d, want 2", nextCount)
	}
	if processStepCount != 1 {
		t.Fatalf("process-step count = %d, want 1 (head only)", processStepCount)
	}
}

func TestCreateCaseFromProcessStep(t *testing.T) {
	store := newProcessMapStore()
	store.nodes["CI-STEP"] = &Node{
		Kind: CI, ID: "CI-STEP", Status: "active",
		Properties: map[string]any{"ciType": "process-step", "name": "Collect paperwork", "execution": "manual", "createsCase": true, "olaEnabled": true, "olaBusinessDays": 3},
	}
	store.relationships = []Relationship{
		{Kind: Responsible, FromID: "CI-STEP", ToID: "GRP-OPS", Status: "active"},
	}
	service := NewService(store)
	created, err := service.CreateGeneratedNode(context.Background(), Case, map[string]any{}, []string{"CI-STEP"})
	if err != nil {
		t.Fatal(err)
	}
		if created.Kind != Case || !strings.HasPrefix(created.ID, "CAS-") {
			t.Fatalf("created = %+v", created)
		}
		if created.Properties["name"] != "Collect paperwork" {
			t.Fatalf("case name = %v", created.Properties["name"])
		}
		if created.Properties["olaEnabled"] != true || created.Properties["olaBusinessDays"] != 3 {
			t.Fatalf("case OLA = %#v", created.Properties)
		}
		var assigned, caseFor int
		for _, rel := range store.relationships {
			if rel.Kind == AssignedTo && rel.FromID == created.ID && rel.ToID == "GRP-OPS" && rel.Status == "active" {
				assigned++
			}
			if rel.Kind == CaseFor && rel.FromID == created.ID && rel.ToID == "CI-STEP" && rel.Status == "active" {
				caseFor++
			}
		}
		if assigned != 1 || caseFor != 1 {
			t.Fatalf("assigned=%d case-for=%d relationships=%+v", assigned, caseFor, store.relationships)
		}
		if _, err := service.CreateGeneratedNode(context.Background(), Case, nil, []string{"CI-PROC"}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("case on process CI error = %v, want ErrInvalid", err)
		}
		store.nodes["CI-STEP"].Properties["createsCase"] = false
		if _, err := service.CreateGeneratedNode(context.Background(), Case, nil, []string{"CI-STEP"}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("case on step without createsCase error = %v, want ErrInvalid", err)
		}
}

func TestSaveProcessMapRejectsMismatchedPerformers(t *testing.T) {
	store := newProcessMapStore()
	service := NewService(store)
	_, err := service.SaveProcessMap(context.Background(), "CI-PROC", ProcessMap{
		Steps: []ProcessMapStep{
			{Name: "Bot step", Execution: "automated", Performers: []ProcessMapPerformer{{Kind: "identity", ID: "IDN-1"}}},
		},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("automated with identity error = %v, want ErrInvalid", err)
	}
	_, err = service.SaveProcessMap(context.Background(), "CI-PROC", ProcessMap{
		Steps: []ProcessMapStep{
			{Name: "Human step", Execution: "manual", Performers: []ProcessMapPerformer{{Kind: "automation", ID: "CI-AUTO"}}},
		},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("manual with automation error = %v, want ErrInvalid", err)
	}
}

func TestSaveProcessMapCollapsesLegacyFanOut(t *testing.T) {
	store := newProcessMapStore()
	store.nodes["CI-STEP-1"] = &Node{Kind: CI, ID: "CI-STEP-1", Status: "active", Properties: map[string]any{"ciType": "process-step", "name": "One", "execution": "manual"}}
	store.nodes["CI-STEP-2"] = &Node{Kind: CI, ID: "CI-STEP-2", Status: "active", Properties: map[string]any{"ciType": "process-step", "name": "Two", "execution": "manual"}}
	store.relationships = []Relationship{
		{Kind: ProcessStep, FromID: "CI-PROC", ToID: "CI-STEP-1", Status: "active", Properties: map[string]any{"order": 1}},
		{Kind: ProcessStep, FromID: "CI-PROC", ToID: "CI-STEP-2", Status: "active", Properties: map[string]any{"order": 2}},
	}
	service := NewService(store)
	loaded, err := service.GetProcessMap(context.Background(), "CI-PROC")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Steps) != 2 || loaded.Steps[0].ID != "CI-STEP-1" || loaded.Steps[1].ID != "CI-STEP-2" {
		t.Fatalf("legacy load = %+v", loaded.Steps)
	}
	saved, err := service.SaveProcessMap(context.Background(), "CI-PROC", ProcessMap{
		Steps: []ProcessMapStep{
			{ID: "CI-STEP-1", Name: "One", Execution: "manual"},
			{ID: "CI-STEP-2", Name: "Two", Execution: "manual"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Steps) != 2 {
		t.Fatalf("saved steps = %d", len(saved.Steps))
	}
	var activeProcessSteps int
	for _, rel := range store.relationships {
		if rel.Kind == ProcessStep && rel.Status == "active" {
			activeProcessSteps++
			if rel.ToID != "CI-STEP-1" {
				t.Fatalf("active process-step still points at %s", rel.ToID)
			}
		}
	}
	if activeProcessSteps != 1 {
		t.Fatalf("active process-step count = %d, want 1", activeProcessSteps)
	}
}

func TestGetProcessMapRejectsNonProcess(t *testing.T) {
	store := newProcessMapStore()
	store.nodes["CI-FN"] = &Node{Kind: CI, ID: "CI-FN", Status: "active", Properties: map[string]any{"ciType": "function", "name": "x", "category": "business"}}
	service := NewService(store)
	if _, err := service.GetProcessMap(context.Background(), "CI-FN"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want ErrInvalid", err)
	}
}

func TestValidateProcessMapInput(t *testing.T) {
	if _, err := validateProcessMapInput(ProcessMap{Steps: []ProcessMapStep{{Name: ""}}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty name error = %v", err)
	}
	clean, err := validateProcessMapInput(ProcessMap{
		TriggerActions: []string{"manual-start", "manual-start"},
		OutputActions:  []string{"record-updated"},
		Steps:          []ProcessMapStep{{Name: "Step"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if clean.Steps[0].Execution != "manual" || clean.Steps[0].Order != 1 {
		t.Fatalf("defaults = %+v", clean.Steps[0])
	}
	if len(clean.TriggerActions) != 1 || clean.TriggerActions[0] != "manual-start" {
		t.Fatalf("deduped triggers = %v", clean.TriggerActions)
	}
	if _, err := validateProcessMapInput(ProcessMap{TriggerActions: []string{"not-a-trigger"}, Steps: []ProcessMapStep{{Name: "Step"}}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad trigger error = %v", err)
	}
}
