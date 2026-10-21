package cmdb

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ProcessMap is the assembled tree for one Process CI: ordered steps,
// trigger/output action catalogs, and performers. Used by GET/PUT /api/process-maps/{id}.
type ProcessMap struct {
	ProcessID          string           `json:"processId"`
	Name               string           `json:"name,omitempty"`
	Description        string           `json:"description,omitempty"`
	TriggerActions     []string         `json:"triggerActions,omitempty"`
	TriggerDescription string           `json:"triggerDescription,omitempty"`
	OutputActions      []string         `json:"outputActions,omitempty"`
	OutputDescription  string           `json:"outputDescription,omitempty"`
	Steps              []ProcessMapStep `json:"steps"`
}

// ProcessMapStep is one Process Step CI in the map.
type ProcessMapStep struct {
	ID               string                `json:"id,omitempty"`
	Name             string                `json:"name"`
	Description      string                `json:"description,omitempty"`
	Execution        string                `json:"execution"` // manual | automated
	CreatesCase      bool                  `json:"createsCase,omitempty"`
	OLAEnabled       bool                  `json:"olaEnabled,omitempty"`
	OLABusinessDays  int                   `json:"olaBusinessDays,omitempty"`
	Order            int                   `json:"order"`
	X                float64               `json:"x,omitempty"`
	Y                float64               `json:"y,omitempty"`
	Performers       []ProcessMapPerformer `json:"performers,omitempty"`
}

// ProcessMapPerformer is who/what performs a step: identity, group, application, or automation.
type ProcessMapPerformer struct {
	Kind string `json:"kind"` // identity | group | application | automation
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// GetProcessMap loads the Process CI and its process-map relationships.
func (s *Service) GetProcessMap(ctx context.Context, processID string) (*ProcessMap, error) {
	if err := validateID(processID); err != nil {
		return nil, err
	}
	process, err := s.store.GetNode(ctx, CI, processID, false)
	if err != nil {
		return nil, err
	}
	if err := requireProcessCI(process); err != nil {
		return nil, err
	}
	return s.assembleProcessMap(ctx, process)
}

// SaveProcessMap reconciles steps, sequence, and performers for a Process CI,
// and stores trigger/output action catalogs on the Process. The Process links
// process-step only to the first step; later steps are chained with
// next-in-process. Missing step IDs create Process Step CIs.
func (s *Service) SaveProcessMap(ctx context.Context, processID string, input ProcessMap) (*ProcessMap, error) {
	if err := validateID(processID); err != nil {
		return nil, err
	}
	process, err := s.store.GetNode(ctx, CI, processID, false)
	if err != nil {
		return nil, err
	}
	if err := requireProcessCI(process); err != nil {
		return nil, err
	}
	clean, err := validateProcessMapInput(input)
	if err != nil {
		return nil, err
	}

	processProps := map[string]any{
		"ciType":             string(Process),
		"triggerActions":     clean.TriggerActions,
		"triggerDescription": clean.TriggerDescription,
		"outputActions":      clean.OutputActions,
		"outputDescription":  clean.OutputDescription,
	}
	if clean.Name != "" {
		processProps["name"] = clean.Name
	}
	if clean.Description != "" {
		processProps["description"] = clean.Description
	}
	if _, err := s.UpdateNode(ctx, CI, processID, processProps); err != nil {
		return nil, err
	}

	current, err := s.GetProcessMap(ctx, processID)
	if err != nil {
		return nil, err
	}

	savedStepIDs := make([]string, 0, len(clean.Steps))
	for i, step := range clean.Steps {
		stepID := strings.TrimSpace(step.ID)
		props := map[string]any{
			"name":            step.Name,
			"execution":       step.Execution,
			"x":               step.X,
			"y":               step.Y,
			"createsCase":     step.CreatesCase,
			"olaEnabled":      step.OLAEnabled,
			"olaBusinessDays": step.OLABusinessDays,
		}
		if step.Description != "" {
			props["description"] = step.Description
		}
		if stepID == "" {
			props["ciType"] = string(ProcessStepCI)
			created, err := s.CreateGeneratedNode(ctx, CI, props, nil)
			if err != nil {
				return nil, fmt.Errorf("step %d: %w", i+1, err)
			}
			stepID = created.ID
		} else {
			stepNode, err := s.store.GetNode(ctx, CI, stepID, false)
			if err != nil {
				return nil, fmt.Errorf("step %d (%s): %w", i+1, stepID, err)
			}
			if propertyString(stepNode.Properties, "ciType") != string(ProcessStepCI) {
				return nil, fmt.Errorf("%w: step %s must be a process-step CI", ErrInvalid, stepID)
			}
			if _, err := s.UpdateNode(ctx, CI, stepID, props); err != nil {
				return nil, fmt.Errorf("step %d (%s): %w", i+1, stepID, err)
			}
		}
		if err := s.reconcileStepPerformers(ctx, stepID, step.Performers); err != nil {
			return nil, err
		}
		savedStepIDs = append(savedStepIDs, stepID)
	}

	headID := ""
	if len(savedStepIDs) > 0 {
		headID = savedStepIDs[0]
		edgeProps := map[string]any{"order": 1}
		if clean.Steps[0].X != 0 {
			edgeProps["x"] = clean.Steps[0].X
		}
		if clean.Steps[0].Y != 0 {
			edgeProps["y"] = clean.Steps[0].Y
		}
		if _, err := s.CreateRelationship(ctx, ProcessStep, processID, headID, edgeProps); err != nil {
			return nil, fmt.Errorf("process-step %s: %w", headID, err)
		}
	}

	// Refresh process edges after create so we see the latest process-step links.
	process, err = s.store.GetNode(ctx, CI, processID, false)
	if err != nil {
		return nil, err
	}
	for _, rel := range process.Relationships {
		if rel.Status != "active" || rel.Kind != ProcessStep || rel.FromID != processID {
			continue
		}
		if rel.ToID == headID {
			continue
		}
		if _, err := s.RetireRelationship(ctx, ProcessStep, processID, rel.ToID); err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}

	if err := s.reconcileNextInProcess(ctx, current.Steps, savedStepIDs); err != nil {
		return nil, err
	}

	return s.GetProcessMap(ctx, processID)
}

type processStepHead struct {
	toID  string
	order int
	x, y  float64
}

func (s *Service) assembleProcessMap(ctx context.Context, process *Node) (*ProcessMap, error) {
	result := &ProcessMap{
		ProcessID:          process.ID,
		Name:               propertyString(process.Properties, "name"),
		Description:        propertyString(process.Properties, "description"),
		TriggerActions:     propertyStringList(process.Properties, "triggerActions"),
		TriggerDescription: propertyString(process.Properties, "triggerDescription"),
		OutputActions:      propertyStringList(process.Properties, "outputActions"),
		OutputDescription:  propertyString(process.Properties, "outputDescription"),
		Steps:              []ProcessMapStep{},
	}
	var heads []processStepHead
	for _, rel := range process.Relationships {
		if rel.Status != "active" || rel.Kind != ProcessStep || rel.FromID != process.ID {
			continue
		}
		heads = append(heads, processStepHead{
			toID:  rel.ToID,
			order: propertyInt(rel.Properties, "order"),
			x:     propertyFloat(rel.Properties, "x"),
			y:     propertyFloat(rel.Properties, "y"),
		})
	}
	if len(heads) == 0 {
		return result, nil
	}
	sort.Slice(heads, func(i, j int) bool {
		if heads[i].order == heads[j].order {
			return heads[i].toID < heads[j].toID
		}
		return heads[i].order < heads[j].order
	})

	edgeXY := map[string][2]float64{}
	for _, edge := range heads {
		edgeXY[edge.toID] = [2]float64{edge.x, edge.y}
	}

	orderedIDs, err := s.collectProcessStepIDs(ctx, heads)
	if err != nil {
		return nil, err
	}

	for i, stepID := range orderedIDs {
		stepNode, err := s.store.GetNode(ctx, CI, stepID, false)
		if err != nil {
			return nil, err
		}
		fallback := edgeXY[stepID]
		step, err := s.mapStepFromNode(ctx, stepNode, i+1, fallback[0], fallback[1])
		if err != nil {
			return nil, err
		}
		result.Steps = append(result.Steps, step)
	}
	return result, nil
}

// collectProcessStepIDs returns ordered step IDs: walk next-in-process from the
// lowest-order process-step head. If multiple process-step links remain (legacy
// fan-out) and the chain does not cover them, fall back to the sorted head list.
func (s *Service) collectProcessStepIDs(ctx context.Context, heads []processStepHead) ([]string, error) {
	if len(heads) == 0 {
		return nil, nil
	}
	headID := heads[0].toID
	chain, err := s.walkNextInProcess(ctx, headID)
	if err != nil {
		return nil, err
	}
	if len(heads) == 1 {
		return chain, nil
	}
	linked := map[string]bool{}
	for _, edge := range heads {
		linked[edge.toID] = true
	}
	for _, id := range chain {
		delete(linked, id)
	}
	if len(linked) == 0 {
		return chain, nil
	}
	// Legacy fan-out without a complete next-in-process chain.
	ids := make([]string, 0, len(heads))
	for _, edge := range heads {
		ids = append(ids, edge.toID)
	}
	return ids, nil
}

func (s *Service) walkNextInProcess(ctx context.Context, headID string) ([]string, error) {
	ordered := []string{}
	seen := map[string]bool{}
	currentID := headID
	for currentID != "" {
		if seen[currentID] {
			return nil, fmt.Errorf("%w: next-in-process cycle at %s", ErrInvalid, currentID)
		}
		seen[currentID] = true
		ordered = append(ordered, currentID)
		node, err := s.store.GetNode(ctx, CI, currentID, false)
		if err != nil {
			return nil, err
		}
		nextID := ""
		for _, rel := range node.Relationships {
			if rel.Status != "active" || rel.Kind != NextInProcess || rel.FromID != currentID {
				continue
			}
			nextID = rel.ToID
			break
		}
		currentID = nextID
	}
	return ordered, nil
}

func (s *Service) mapStepFromNode(ctx context.Context, stepNode *Node, order int, fallbackX, fallbackY float64) (ProcessMapStep, error) {
	x := propertyFloat(stepNode.Properties, "x")
	y := propertyFloat(stepNode.Properties, "y")
	if x == 0 && y == 0 {
		x, y = fallbackX, fallbackY
	}
	step := ProcessMapStep{
		ID:              stepNode.ID,
		Name:            propertyString(stepNode.Properties, "name"),
		Description:     propertyString(stepNode.Properties, "description"),
		Execution:       propertyString(stepNode.Properties, "execution"),
		CreatesCase:     propertyBool(stepNode.Properties, "createsCase"),
		OLAEnabled:      propertyBool(stepNode.Properties, "olaEnabled"),
		OLABusinessDays: propertyInt(stepNode.Properties, "olaBusinessDays"),
		Order:           order,
		X:               x,
		Y:               y,
		Performers:      []ProcessMapPerformer{},
	}
	for _, rel := range stepNode.Relationships {
		if rel.Status != "active" || rel.FromID != stepNode.ID {
			continue
		}
		switch rel.Kind {
		case Responsible:
			kind := string(Identity)
			if strings.HasPrefix(rel.ToID, "GRP-") {
				kind = string(Group)
			}
			step.Performers = append(step.Performers, ProcessMapPerformer{Kind: kind, ID: rel.ToID, Name: rel.ToName})
		case Uses:
			kind := "application"
			if target, err := s.store.GetNode(ctx, CI, rel.ToID, false); err == nil && propertyString(target.Properties, "ciType") == string(Automation) {
				kind = "automation"
			}
			step.Performers = append(step.Performers, ProcessMapPerformer{Kind: kind, ID: rel.ToID, Name: rel.ToName})
		}
	}
	return step, nil
}

func (s *Service) reconcileNextInProcess(ctx context.Context, previous []ProcessMapStep, stepIDs []string) error {
	desired := map[string]string{}
	for i := 0; i+1 < len(stepIDs); i++ {
		desired[stepIDs[i]] = stepIDs[i+1]
	}
	seen := map[string]bool{}
	for _, id := range stepIDs {
		seen[id] = true
	}
	for _, step := range previous {
		seen[step.ID] = true
	}
	for fromID := range seen {
		node, err := s.store.GetNode(ctx, CI, fromID, false)
		if err != nil {
			continue
		}
		for _, rel := range node.Relationships {
			if rel.Status != "active" || rel.Kind != NextInProcess || rel.FromID != fromID {
				continue
			}
			wantTo, ok := desired[fromID]
			if ok && wantTo == rel.ToID {
				delete(desired, fromID)
				continue
			}
			if _, err := s.RetireRelationship(ctx, NextInProcess, fromID, rel.ToID); err != nil {
				return err
			}
		}
	}
	for fromID, toID := range desired {
		if _, err := s.CreateRelationship(ctx, NextInProcess, fromID, toID, nil); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) reconcileStepPerformers(ctx context.Context, stepID string, performers []ProcessMapPerformer) error {
	wantResponsible := map[string]bool{}
	wantUses := map[string]bool{}
	for _, performer := range performers {
		id := strings.TrimSpace(performer.ID)
		if id == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(performer.Kind)) {
		case string(Identity), string(Group):
			wantResponsible[id] = true
		case "application", "automation", string(CI):
			wantUses[id] = true
		default:
			return fmt.Errorf("%w: performer kind %q must be identity, group, application, or automation", ErrInvalid, performer.Kind)
		}
	}
	node, err := s.store.GetNode(ctx, CI, stepID, false)
	if err != nil {
		return err
	}
	haveResponsible := map[string]bool{}
	haveUses := map[string]bool{}
	for _, rel := range node.Relationships {
		if rel.Status != "active" || rel.FromID != stepID {
			continue
		}
		switch rel.Kind {
		case Responsible:
			haveResponsible[rel.ToID] = true
			if !wantResponsible[rel.ToID] {
				if _, err := s.RetireRelationship(ctx, Responsible, stepID, rel.ToID); err != nil {
					return err
				}
			}
		case Uses:
			haveUses[rel.ToID] = true
			if !wantUses[rel.ToID] {
				if _, err := s.RetireRelationship(ctx, Uses, stepID, rel.ToID); err != nil {
					return err
				}
			}
		}
	}
	for id := range wantResponsible {
		if haveResponsible[id] {
			continue
		}
		if _, err := s.CreateRelationship(ctx, Responsible, stepID, id, nil); err != nil {
			return err
		}
	}
	for id := range wantUses {
		if haveUses[id] {
			continue
		}
		if _, err := s.CreateRelationship(ctx, Uses, stepID, id, nil); err != nil {
			return err
		}
	}
	return nil
}

func validateProcessMapInput(input ProcessMap) (*ProcessMap, error) {
	clean := &ProcessMap{
		ProcessID:          strings.TrimSpace(input.ProcessID),
		Name:               strings.TrimSpace(input.Name),
		Description:        strings.TrimSpace(input.Description),
		TriggerDescription: strings.TrimSpace(input.TriggerDescription),
		OutputDescription:  strings.TrimSpace(input.OutputDescription),
		TriggerActions:     nil,
		OutputActions:      nil,
		Steps:              make([]ProcessMapStep, 0, len(input.Steps)),
	}
	triggers, err := normalizeActionList(stringSliceAny(input.TriggerActions), ParseTriggerAction)
	if err != nil {
		return nil, fmt.Errorf("triggerActions: %w", err)
	}
	clean.TriggerActions = triggers
	outputs, err := normalizeActionList(stringSliceAny(input.OutputActions), ParseOutputAction)
	if err != nil {
		return nil, fmt.Errorf("outputActions: %w", err)
	}
	clean.OutputActions = outputs
	seenSteps := map[string]bool{}
	for i, step := range input.Steps {
		position := i + 1
		name := strings.TrimSpace(step.Name)
		if name == "" {
			return nil, fmt.Errorf("%w: step %d needs a name", ErrInvalid, position)
		}
		executionName := strings.TrimSpace(step.Execution)
		if executionName == "" {
			executionName = string(ManualExecution)
		}
		execution, err := ParseExecutionModel(executionName)
		if err != nil {
			return nil, fmt.Errorf("step %d: %w", position, err)
		}
		stepID := strings.TrimSpace(step.ID)
		if stepID != "" {
			if seenSteps[stepID] {
				return nil, fmt.Errorf("%w: step %s appears more than once", ErrInvalid, stepID)
			}
			seenSteps[stepID] = true
		}
		cleanStep := ProcessMapStep{
			ID:              stepID,
			Name:            name,
			Description:     strings.TrimSpace(step.Description),
			Execution:       string(execution),
			CreatesCase:     step.CreatesCase,
			OLAEnabled:      step.OLAEnabled,
			OLABusinessDays: step.OLABusinessDays,
			Order:           position,
			X:               step.X,
			Y:               step.Y,
			Performers:      nil,
		}
		if cleanStep.OLAEnabled {
			if cleanStep.OLABusinessDays < 1 {
				cleanStep.OLABusinessDays = 5
			}
			if cleanStep.OLABusinessDays > 365 {
				return nil, fmt.Errorf("%w: step %d olaBusinessDays must be between 1 and 365", ErrInvalid, position)
			}
		} else {
			cleanStep.OLABusinessDays = 0
		}
		seenPerf := map[string]bool{}
		automated := execution == AutomatedExecution
		for _, performer := range step.Performers {
			id := strings.TrimSpace(performer.ID)
			kind := strings.ToLower(strings.TrimSpace(performer.Kind))
			if id == "" {
				continue
			}
			if kind != string(Identity) && kind != string(Group) && kind != "application" && kind != "automation" {
				return nil, fmt.Errorf("%w: step %d performer kind must be identity, group, application, or automation", ErrInvalid, position)
			}
			if automated && kind != "automation" {
				return nil, fmt.Errorf("%w: step %d automated execution only allows automation performers", ErrInvalid, position)
			}
			if !automated && kind == "automation" {
				return nil, fmt.Errorf("%w: step %d manual execution cannot use automation performers", ErrInvalid, position)
			}
			key := kind + ":" + id
			if seenPerf[key] {
				continue
			}
			seenPerf[key] = true
			cleanStep.Performers = append(cleanStep.Performers, ProcessMapPerformer{Kind: kind, ID: id})
		}
		if automated {
			cleanStep.CreatesCase = false
		}
		clean.Steps = append(clean.Steps, cleanStep)
	}
	return clean, nil
}

func stringSliceAny(values []string) any {
	if values == nil {
		return []string{}
	}
	return values
}

func requireProcessCI(node *Node) error {
	if node == nil || node.Kind != CI {
		return fmt.Errorf("%w: process map requires a process CI", ErrInvalid)
	}
	if propertyString(node.Properties, "ciType") != string(Process) {
		return fmt.Errorf("%w: %s is not a process CI", ErrInvalid, node.ID)
	}
	return nil
}

func (s *Service) prepareCaseFromStep(ctx context.Context, properties map[string]any, stepIDs []string) error {
	if len(stepIDs) != 1 {
		return fmt.Errorf("%w: a case must be linked to exactly one process-step CI", ErrInvalid)
	}
	step, err := s.store.GetNode(ctx, CI, stepIDs[0], false)
	if err != nil {
		return err
	}
	if propertyString(step.Properties, "ciType") != string(ProcessStepCI) {
		return fmt.Errorf("%w: cases must be linked to a process-step CI", ErrInvalid)
	}
	if !propertyBool(step.Properties, "createsCase") {
		return fmt.Errorf("%w: %s does not allow case creation", ErrInvalid, step.ID)
	}
	if strings.TrimSpace(propertyString(properties, "name")) == "" {
		properties["name"] = propertyString(step.Properties, "name")
	}
	if _, hasOLA := properties["olaEnabled"]; !hasOLA {
		properties["olaEnabled"] = propertyBool(step.Properties, "olaEnabled")
	}
	if _, hasDays := properties["olaBusinessDays"]; !hasDays {
		properties["olaBusinessDays"] = propertyInt(step.Properties, "olaBusinessDays")
	}
	return normalizeOLAProperties(properties, true)
}

func (s *Service) assignCaseFromStep(ctx context.Context, caseID, stepID string) error {
	step, err := s.store.GetNode(ctx, CI, stepID, false)
	if err != nil {
		return err
	}
	for _, rel := range step.Relationships {
		if rel.Status != "active" || rel.Kind != Responsible || rel.FromID != stepID {
			continue
		}
		if _, err := s.CreateRelationship(ctx, AssignedTo, caseID, rel.ToID, nil); err != nil {
			return err
		}
	}
	return nil
}

func propertyBool(properties map[string]any, key string) bool {
	if properties == nil {
		return false
	}
	flag, _ := properties[key].(bool)
	return flag
}

func propertyString(properties map[string]any, key string) string {
	if properties == nil {
		return ""
	}
	value, _ := properties[key].(string)
	return value
}

func propertyStringList(properties map[string]any, key string) []string {
	if properties == nil {
		return nil
	}
	switch value := properties[key].(type) {
	case []string:
		return append([]string(nil), value...)
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if text, ok := item.(string); ok && text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func propertyInt(properties map[string]any, key string) int {
	if properties == nil {
		return 0
	}
	switch value := properties[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func propertyFloat(properties map[string]any, key string) float64 {
	if properties == nil {
		return 0
	}
	switch value := properties[key].(type) {
	case float64:
		return value
	case int:
		return float64(value)
	case int64:
		return float64(value)
	default:
		return 0
	}
}
