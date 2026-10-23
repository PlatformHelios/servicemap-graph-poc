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

func (p *processMapStore) ListHolidays(context.Context) ([]Holiday, error) {
	return nil, nil
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

func (p *processMapStore) CreateGeneratedNode(ctx context.Context, kind NodeKind, properties map[string]any, ciIDs []string) (*Node, error) {
	p.nextCI++
	prefix, err := GeneratedIDPrefix(kind)
	if err != nil {
		prefix = "CI"
	}
	id := fmt.Sprintf("%s-%06d", prefix, p.nextCI)
	node := &Node{Kind: kind, ID: id, Status: NodeStatusFromContext(ctx), Properties: copyMap(properties)}
	p.nodes[id] = node
	if relKind, _, ok := LinkedCIRelationship(kind); ok {
		for _, ciID := range ciIDs {
			p.relationships = append(p.relationships, Relationship{Kind: relKind, FromID: id, ToID: ciID, Status: "active"})
		}
	}
	return node, nil
}

func (p *processMapStore) SetNodeStatus(_ context.Context, kind NodeKind, id, status string) error {
	node, ok := p.nodes[id]
	if !ok || node.Kind != kind {
		return ErrNotFound
	}
	node.Status = status
	return nil
}

func (p *processMapStore) DeleteDraftNode(_ context.Context, kind NodeKind, id string) error {
	node, ok := p.nodes[id]
	if !ok || node.Kind != kind || node.Status != "draft" {
		return ErrNotFound
	}
	delete(p.nodes, id)
	return nil
}

func (p *processMapStore) ListDraftPackages(context.Context) ([]DraftPackage, error) {
	return nil, nil
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
	store.nodes["CI-APP"] = &Node{Kind: CI, ID: "CI-APP", Status: "active", Properties: map[string]any{"ciType": "application", "name": "Intake"}}
	store.nodes["CI-AUTO"] = &Node{Kind: CI, ID: "CI-AUTO", Status: "active", Properties: map[string]any{"ciType": "automation", "name": "Loader", "environment": "production", "triggerType": "event"}}
	store.nodes["CI-OUT"] = &Node{Kind: CI, ID: "CI-OUT", Status: "active", Properties: map[string]any{"ciType": "service", "name": "Vendor record", "category": "business"}}
	service := NewService(store)
	saved, err := service.SaveProcessMap(context.Background(), "CI-PROC", ProcessMap{
		Name:               "Onboard vendor",
		TriggerDescription: "Vendor request submitted",
		TriggerActions:     []string{"form-submitted", "email-received"},
		Triggers:           []ProcessMapRef{{Kind: "ci", ID: "CI-APP"}},
		OutputDescription:  "Vendor record ready",
		OutputActions:      []string{"record-created", "notification-sent"},
		Outputs:            []ProcessMapRef{{Kind: "ci", ID: "CI-OUT"}},
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
		case TriggeredBy:
			if rel.FromID != "CI-PROC" || rel.ToID != "CI-APP" {
				t.Fatalf("triggered-by = %s -> %s", rel.FromID, rel.ToID)
			}
		case Produces:
			if rel.FromID != "CI-PROC" || rel.ToID != "CI-OUT" {
				t.Fatalf("produces = %s -> %s", rel.FromID, rel.ToID)
			}
		}
	}
	if nextCount != 2 {
		t.Fatalf("next-in-process count = %d, want 2", nextCount)
	}
	if processStepCount != 1 {
		t.Fatalf("process-step count = %d, want 1 (head only)", processStepCount)
	}
	if len(saved.Triggers) != 1 || saved.Triggers[0].ID != "CI-APP" || saved.Triggers[0].Name != "Intake" {
		t.Fatalf("triggers = %+v", saved.Triggers)
	}
	if len(saved.Outputs) != 1 || saved.Outputs[0].ID != "CI-OUT" || saved.Outputs[0].Name != "Vendor record" {
		t.Fatalf("outputs = %+v", saved.Outputs)
	}
	cleared, err := service.SaveProcessMap(context.Background(), "CI-PROC", ProcessMap{
		Name:     "Onboard vendor",
		Steps:    saved.Steps,
		Triggers: nil,
		Outputs:  nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cleared.Triggers) != 0 || len(cleared.Outputs) != 0 {
		t.Fatalf("cleared triggers=%+v outputs=%+v", cleared.Triggers, cleared.Outputs)
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

func TestDraftProcessMapPublishesWithItsProcess(t *testing.T) {
	store := newProcessMapStore()
	store.nodes["CI-PROC"].Status = "draft"
	service := NewService(store)
	saved, err := service.SaveProcessMap(context.Background(), "CI-PROC", ProcessMap{
		Steps: []ProcessMapStep{{Name: "Collect paperwork", Execution: "manual"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	stepID := saved.Steps[0].ID
	if store.nodes[stepID].Status != "draft" {
		t.Fatalf("step status = %s, want draft", store.nodes[stepID].Status)
	}
	err = service.PublishDrafts(context.Background(), []DraftRef{{Kind: "ci", ID: stepID}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("publish step alone error = %v, want ErrInvalid", err)
	}
	if err := service.PublishDrafts(context.Background(), []DraftRef{{Kind: "ci", ID: "CI-PROC"}, {Kind: "ci", ID: stepID}}); err != nil {
		t.Fatal(err)
	}
	if store.nodes["CI-PROC"].Status != "active" || store.nodes[stepID].Status != "active" {
		t.Fatalf("published statuses process=%s step=%s", store.nodes["CI-PROC"].Status, store.nodes[stepID].Status)
	}
	if err := service.DeleteDraftNode(context.Background(), CI, stepID); err == nil {
		t.Fatal("delete published step should fail")
	}
}

func TestNewStepOnActiveProcessIsDraft(t *testing.T) {
	store := newProcessMapStore()
	service := NewService(store)
	first, err := service.SaveProcessMap(context.Background(), "CI-PROC", ProcessMap{
		Steps: []ProcessMapStep{{Name: "Collect paperwork", Execution: "manual"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	stepID := first.Steps[0].ID
	if store.nodes[stepID].Status != "draft" {
		t.Fatalf("first step status = %s, want draft", store.nodes[stepID].Status)
	}
	if err := store.SetNodeStatus(context.Background(), CI, stepID, "active"); err != nil {
		t.Fatal(err)
	}
	saved, err := service.SaveProcessMap(context.Background(), "CI-PROC", ProcessMap{
		Steps: []ProcessMapStep{
			{ID: stepID, Name: "Collect paperwork", Execution: "manual", DependsOn: []ProcessMapDependency{{Kind: "process-start"}}},
			{Name: "Security review", Execution: "manual", DependsOn: []ProcessMapDependency{{Kind: "step", ID: stepID}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.nodes[stepID].Status != "active" {
		t.Fatalf("approved step status = %s, want active", store.nodes[stepID].Status)
	}
	if len(saved.Steps) != 2 || store.nodes[saved.Steps[1].ID].Status != "draft" {
		t.Fatalf("added step = %+v status %s", saved.Steps, store.nodes[saved.Steps[1].ID].Status)
	}
}

func TestSaveProcessMapDependenciesForkAndJoin(t *testing.T) {
	store := newProcessMapStore()
	service := NewService(store)
	saved, err := service.SaveProcessMap(context.Background(), "CI-PROC", ProcessMap{
		Steps: []ProcessMapStep{
			{Name: "Check credit", Execution: "manual", DependsOn: []ProcessMapDependency{{Kind: "process-start"}}},
			{Name: "Check inventory", Execution: "manual", DependsOn: []ProcessMapDependency{{Kind: "process-start"}}},
			{Name: "Confirm order", Execution: "manual", DependsOn: []ProcessMapDependency{{Kind: "step", Order: 1}, {Kind: "step", Order: 2}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Steps) != 3 {
		t.Fatalf("steps = %d", len(saved.Steps))
	}
	if len(saved.Steps[0].DependsOn) != 1 || saved.Steps[0].DependsOn[0].Kind != processStartDependency {
		t.Fatalf("step 1 dependencies = %+v", saved.Steps[0].DependsOn)
	}
	if len(saved.Steps[1].DependsOn) != 1 || saved.Steps[1].DependsOn[0].Kind != processStartDependency {
		t.Fatalf("step 2 dependencies = %+v", saved.Steps[1].DependsOn)
	}
	if len(saved.Steps[2].DependsOn) != 2 || saved.Steps[2].DependsOn[0].ID != saved.Steps[0].ID || saved.Steps[2].DependsOn[1].Order != 2 {
		t.Fatalf("step 3 dependencies = %+v", saved.Steps[2].DependsOn)
	}
	want := map[string]bool{
		saved.Steps[0].ID + "->CI-PROC":              true,
		saved.Steps[1].ID + "->CI-PROC":              true,
		saved.Steps[2].ID + "->" + saved.Steps[0].ID: true,
		saved.Steps[2].ID + "->" + saved.Steps[1].ID: true,
	}
	for _, rel := range store.relationships {
		if rel.Status != "active" || rel.Kind != DependsOn {
			continue
		}
		key := rel.FromID + "->" + rel.ToID
		if !want[key] {
			t.Fatalf("unexpected depends-on %s", key)
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Fatalf("missing depends-on %v", want)
	}

	store.relationships = append(store.relationships, Relationship{Kind: DependsOn, FromID: saved.Steps[0].ID, ToID: "CI-OTHER", Status: "active"})
	joined, err := service.SaveProcessMap(context.Background(), "CI-PROC", ProcessMap{
		Steps: []ProcessMapStep{
			{ID: saved.Steps[0].ID, Name: "Check credit", Execution: "manual", DependsOn: []ProcessMapDependency{{Kind: "process-start"}}},
			{ID: saved.Steps[1].ID, Name: "Check inventory", Execution: "manual", DependsOn: []ProcessMapDependency{{Kind: "process-start"}}},
			{ID: saved.Steps[2].ID, Name: "Confirm order", Execution: "manual", DependsOn: []ProcessMapDependency{{Kind: "step", ID: saved.Steps[0].ID}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(joined.Steps[2].DependsOn) != 1 || joined.Steps[2].DependsOn[0].ID != saved.Steps[0].ID {
		t.Fatalf("joined dependencies = %+v", joined.Steps[2].DependsOn)
	}
	var other, retiredJoin int
	for _, rel := range store.relationships {
		if rel.Kind != DependsOn || rel.FromID != saved.Steps[0].ID && rel.FromID != saved.Steps[2].ID {
			continue
		}
		if rel.FromID == saved.Steps[0].ID && rel.ToID == "CI-OTHER" && rel.Status == "active" {
			other++
		}
		if rel.FromID == saved.Steps[2].ID && rel.ToID == saved.Steps[1].ID && rel.Status == "retired" {
			retiredJoin++
		}
	}
	if other != 1 || retiredJoin != 1 {
		t.Fatalf("other=%d retiredJoin=%d", other, retiredJoin)
	}

	_, err = service.SaveProcessMap(context.Background(), "CI-PROC", ProcessMap{
		Steps: []ProcessMapStep{
			{ID: saved.Steps[0].ID, Name: "Check credit", Execution: "manual", DependsOn: []ProcessMapDependency{{Kind: "step", ID: saved.Steps[1].ID}}},
			{ID: saved.Steps[1].ID, Name: "Check inventory", Execution: "manual", DependsOn: []ProcessMapDependency{{Kind: "process-start"}}},
		},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("later-step dependency error = %v, want ErrInvalid", err)
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
	if len(clean.Steps[0].DependsOn) != 1 || clean.Steps[0].DependsOn[0].Kind != processStartDependency {
		t.Fatalf("omitted dependencies = %+v, want process start", clean.Steps[0].DependsOn)
	}
	if len(clean.TriggerActions) != 1 || clean.TriggerActions[0] != "manual-start" {
		t.Fatalf("deduped triggers = %v", clean.TriggerActions)
	}
	if _, err := validateProcessMapInput(ProcessMap{TriggerActions: []string{"not-a-trigger"}, Steps: []ProcessMapStep{{Name: "Step"}}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad trigger error = %v", err)
	}
}
