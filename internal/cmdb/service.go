package cmdb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrNotFound = errors.New("record not found")

type Store interface {
	EnsureConstraints(context.Context) error
	CreateGeneratedNode(context.Context, NodeKind, map[string]any, []string) (*Node, error)
	// GetNode returns the record with its direct relationships (retired ones
	// too when includeRetired is set).
	GetNode(context.Context, NodeKind, string, bool) (*Node, error)
	// ListNodes returns one page (or all) of a kind with the totals matched.
	// Each listed record carries a bounded sample of its direct relationships.
	ListNodes(context.Context, NodeKind, ListOptions) ([]Node, ListTotals, error)
	UpdateNode(context.Context, NodeKind, string, map[string]any) (*Node, error)
	// SubmitRequest moves a draft request to submitted, applying properties and
	// swapping its requester link from drafted-request to form-submitted.
	SubmitRequest(context.Context, string, map[string]any) (*Node, error)
	RetireNode(context.Context, NodeKind, string) (*Node, error)
	// Workflow definitions and execution; see workflow.go.
	SaveWorkflow(context.Context, string, WorkflowDefinition) (*WorkflowDefinition, error)
	GetWorkflow(context.Context, string) (*WorkflowDefinition, error)
	ListWorkflows(context.Context, bool) ([]WorkflowDefinition, error)
	RetireWorkflow(context.Context, string) (*WorkflowDefinition, error)
	RetireWorkflowRun(context.Context, string) error
	StartWorkflowRun(context.Context, string, string) error
	GetTask(context.Context, string) (*TaskView, error)
	ListTasks(context.Context, TaskFilter) ([]TaskView, int, error)
	ActOnTask(context.Context, string, string, TaskOutcome) (*TaskView, error)
	// Access requests; see access.go.
	AccessOptions(context.Context, string) (*AccessOptions, error)
	// AccessAnomalies reads the stored anomaly snapshot; RefreshAccessAnomalies
	// recomputes it from the live graph and stores the result.
	AccessAnomalies(context.Context) (*AccessAnomalyReport, error)
	RefreshAccessAnomalies(context.Context) (*AccessAnomalyReport, error)
	CreateAccessRequest(context.Context, AccessRequestInput, string) (*Node, error)
	// Service Catalog; see catalog.go. ResolveAssignees finds the active
	// groups and identities whose id or name is the reference.
	ResolveAssignees(context.Context, string) ([]Assignee, error)
	IdentityGroups(context.Context, string) ([]string, error)
	SaveCatalogItem(ctx context.Context, manifest CatalogManifest, manifestJSON, workflowID, publishedBy string) (*CatalogItemView, error)
	GetCatalogItem(context.Context, string) (*CatalogItemView, error)
	GetCatalogItemByName(context.Context, string) (*CatalogItemView, error)
	CatalogManifest(context.Context, string) (string, error)
	// ListCatalogItems lists active items; with an identity id, only those
	// visible to (or owned by) a group the identity belongs to.
	ListCatalogItems(context.Context, string) ([]CatalogItemView, error)
	CreateCatalogRequest(context.Context, CatalogRequestRecord) (*Node, error)
	SettleCatalogRequest(context.Context, string, CatalogOutcome) (*Node, error)
	// Workflow Analyzer; see analysis.go. GroupMembers maps each group id to
	// its active member identities.
	GroupMembers(context.Context, []string) (map[string][]Assignee, error)
	CatalogRequestSummaries(context.Context, string) ([]CatalogRequestSummary, error)
	ListHolidays(context.Context) ([]Holiday, error)
	SaveHolidays(context.Context, []Holiday) error
	CreateRelationship(context.Context, RelationshipKind, string, string, map[string]any) (*Relationship, error)
	GetRelationship(context.Context, RelationshipKind, string, string, bool) (*Relationship, error)
	ListRelationships(context.Context, RelationshipKind, ListOptions) ([]Relationship, ListTotals, error)
	UpdateRelationship(context.Context, RelationshipKind, string, string, map[string]any) (*Relationship, error)
	RetireRelationship(context.Context, RelationshipKind, string, string) (*Relationship, error)
}

type Service struct {
	store      Store
	geocoder   Geocoder
	automation CatalogAutomation
	insight    AutomationInsight
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

// WithGeocoder enables address-to-coordinate resolution for location CIs.
func (s *Service) WithGeocoder(geocoder Geocoder) *Service {
	s.geocoder = geocoder
	return s
}

// geocodeLocation fills latitude/longitude for a location CI from its address
// unless the caller supplied coordinates. existing holds the stored properties
// when updating so a partial address change is geocoded against the full address.
func (s *Service) geocodeLocation(ctx context.Context, properties, existing map[string]any) {
	if hasCoordinates(properties) {
		properties[GeoPrecisionProperty] = GeoPrecisionManual
		return
	}
	if s.geocoder == nil {
		return
	}
	merged := make(map[string]any, len(existing)+len(properties))
	for key, value := range existing {
		merged[key] = value
	}
	for key, value := range properties {
		merged[key] = value
	}
	point, found, err := s.geocoder.GeocodeLocation(ctx, merged)
	if err != nil || !found {
		return
	}
	properties[LatitudeProperty] = point.Latitude
	properties[LongitudeProperty] = point.Longitude
	properties[GeoPrecisionProperty] = point.Precision
}

func (s *Service) EnsureConstraints(ctx context.Context) error {
	return s.store.EnsureConstraints(ctx)
}

func (s *Service) CreateGeneratedNode(ctx context.Context, kind NodeKind, properties map[string]any, ciIDs []string) (*Node, error) {
	if !RequiresGeneratedID(kind) {
		return nil, fmt.Errorf("%w: IDs are not generated for %s records", ErrInvalid, kind)
	}
	if err := rejectWorkflowKind(kind); err != nil {
		return nil, err
	}
	cleanProperties, err := validateProperties(properties)
	if err != nil {
		return nil, err
	}
	if err := validateCIProperties(kind, cleanProperties, true); err != nil {
		return nil, err
	}
	cleanCIIDs, err := normalizeCIIDs(ciIDs)
	if err != nil {
		return nil, err
	}
	if _, _, linkable := LinkedCIRelationship(kind); len(cleanCIIDs) > 0 && !linkable {
		return nil, fmt.Errorf("%w: ciIds are only valid when creating identity, incident, change, or event records", ErrInvalid)
	}
	if RequiresLinkedCIs(kind) && len(cleanCIIDs) == 0 {
		return nil, fmt.Errorf("%w: %s records must be linked to at least one CI", ErrInvalid, kind)
	}
	if kind == CI && cleanProperties["ciType"] == string(Location) {
		s.geocodeLocation(ctx, cleanProperties, nil)
	}
	return s.store.CreateGeneratedNode(ctx, kind, cleanProperties, cleanCIIDs)
}

func RequiresLinkedCIs(kind NodeKind) bool {
	return kind == Incident || kind == Change
}

func normalizeCIIDs(ciIDs []string) ([]string, error) {
	seen := make(map[string]struct{}, len(ciIDs))
	clean := make([]string, 0, len(ciIDs))
	for _, id := range ciIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, fmt.Errorf("%w: CI identifiers cannot be empty", ErrInvalid)
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		clean = append(clean, id)
	}
	return clean, nil
}

func (s *Service) GetNode(ctx context.Context, kind NodeKind, id string, includeRetired bool) (*Node, error) {
	if _, ok := nodeSpecs[kind]; !ok {
		return nil, fmt.Errorf("%w: unsupported node kind %q", ErrInvalid, kind)
	}
	if err := validateID(id); err != nil {
		return nil, err
	}
	return s.store.GetNode(ctx, kind, id, includeRetired)
}

func (s *Service) ListNodes(ctx context.Context, kind NodeKind, options ListOptions) ([]Node, ListTotals, error) {
	if _, ok := nodeSpecs[kind]; !ok {
		return nil, ListTotals{}, fmt.Errorf("%w: unsupported node kind %q", ErrInvalid, kind)
	}
	options, err := options.Validate()
	if err != nil {
		return nil, ListTotals{}, err
	}
	if (options.InvolvedID != "" || options.RequestType != "") && kind != Request {
		return nil, ListTotals{}, fmt.Errorf("%w: involvedId and requestType only filter request lists", ErrInvalid)
	}
	if options.RequestType != "" {
		requestType, err := ParseRequestType(string(options.RequestType))
		if err != nil {
			return nil, ListTotals{}, err
		}
		options.RequestType = requestType
	}
	return s.store.ListNodes(ctx, kind, options)
}

func (s *Service) UpdateNode(ctx context.Context, kind NodeKind, id string, properties map[string]any) (*Node, error) {
	if _, ok := nodeSpecs[kind]; !ok {
		return nil, fmt.Errorf("%w: unsupported node kind %q", ErrInvalid, kind)
	}
	if err := validateID(id); err != nil {
		return nil, err
	}
	if err := rejectWorkflowKind(kind); err != nil {
		return nil, err
	}
	cleanProperties, err := validateProperties(properties)
	if err != nil {
		return nil, err
	}
	if err := validateCIProperties(kind, cleanProperties, false); err != nil {
		return nil, err
	}
	if len(cleanProperties) == 0 {
		return nil, fmt.Errorf("%w: at least one property is required for update", ErrInvalid)
	}
	if kind == CI && (hasAddressChange(cleanProperties) || hasCoordinates(cleanProperties)) {
		existing, err := s.store.GetNode(ctx, kind, id, false)
		if err != nil {
			return nil, err
		}
		if existing.Properties["ciType"] == string(Location) {
			s.geocodeLocation(ctx, cleanProperties, existing.Properties)
		}
	}
	if kind == Request {
		if state, changingState := cleanProperties["state"].(string); changingState {
			return s.updateRequestState(ctx, id, RequestState(state), cleanProperties)
		}
	}
	return s.store.UpdateNode(ctx, kind, id, cleanProperties)
}

// updateRequestState applies a request update that names a state. Moving a
// draft to submitted checks the form is complete and hands off to the store so
// the requester link is swapped in the same write, then starts the form's
// workflow if one is enabled. Callers cannot move a request backwards: only a
// workflow reviewer returns it to draft, and only the workflow marks it
// in-review or fulfilled. Submitted requests stay editable.
func (s *Service) updateRequestState(ctx context.Context, id string, state RequestState, properties map[string]any) (*Node, error) {
	existing, err := s.store.GetNode(ctx, Request, id, false)
	if err != nil {
		return nil, err
	}
	current := requestStateOf(existing.Properties)
	switch {
	case state == current:
		delete(properties, "state")
		if len(properties) == 0 {
			return existing, nil
		}
		return s.store.UpdateNode(ctx, Request, id, properties)
	case state == RequestDraft:
		return nil, fmt.Errorf("%w: %s requests cannot return to draft; a reviewer can return them from the workflow", ErrInvalid, current)
	case state == RequestSubmitted && current == RequestDraft:
		if err := validateSubmittedRequest(existing.Properties, properties); err != nil {
			return nil, err
		}
		delete(properties, "state") // the store sets state and submittedAt together
		requestType := RequestType("")
		if name, ok := existing.Properties["requestType"].(string); ok {
			requestType = RequestType(name)
		}
		if err := s.applyFormSLA(ctx, requestType, properties); err != nil {
			return nil, err
		}
		holidays, err := s.store.ListHolidays(ctx)
		if err != nil {
			return nil, err
		}
		stampSLADueAt(properties, time.Now().UTC(), holidays)
		submitted, err := s.store.SubmitRequest(ctx, id, properties)
		if err != nil {
			return nil, err
		}
		return s.startWorkflow(ctx, submitted)
	case state == RequestSubmitted:
		return nil, fmt.Errorf("%w: request %s is already %s", ErrInvalid, id, current)
	default:
		return nil, fmt.Errorf("%w: state %s is set by the workflow, not by callers", ErrInvalid, state)
	}
}

func requestStateOf(properties map[string]any) RequestState {
	if value, ok := properties["state"].(string); ok {
		if state, err := ParseRequestState(value); err == nil {
			return state
		}
	}
	return RequestDraft
}

// rejectWorkflowKind keeps workflow records out of the generic node endpoints;
// they are shaped and validated through the workflow API instead.
func rejectWorkflowKind(kind NodeKind) error {
	for _, managed := range WorkflowNodeKinds() {
		if kind == managed {
			return fmt.Errorf("%w: %s records are managed through the workflow API", ErrInvalid, kind)
		}
	}
	if kind == CatalogItem {
		return fmt.Errorf("%w: catalog items are published by their owning team (POST /api/catalog-items)", ErrInvalid)
	}
	return nil
}

// validateSubmittedRequest checks the stored properties, overlaid with the
// update, carry everything the request's CI type requires.
func validateSubmittedRequest(existing, updates map[string]any) error {
	merged := make(map[string]any, len(existing)+len(updates))
	for key, value := range existing {
		merged[key] = value
	}
	for key, value := range updates {
		merged[key] = value
	}
	requestType, _ := merged["requestType"].(string)
	missing := make([]string, 0)
	for _, key := range RequestRequiredProperties(RequestType(requestType)) {
		if value, ok := merged[key].(string); !ok || strings.TrimSpace(value) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s requests require %s before they can be submitted", ErrInvalid, requestType, strings.Join(missing, ", "))
	}
	return nil
}

// validateRequestProperties normalizes a catalog request. Drafts may be
// incomplete, so only the values that are present are checked; completeness is
// enforced when the request is submitted.
func validateRequestProperties(properties map[string]any, creating bool) error {
	for _, key := range []string{"ciType", "category", "contractType", "hosted"} {
		if _, present := properties[key]; present {
			return fmt.Errorf("%w: %s is only valid for CI records", ErrInvalid, key)
		}
	}
	if value, present := properties["requestType"]; present {
		name, ok := value.(string)
		if !ok {
			return fmt.Errorf("%w: requestType must be a string", ErrInvalid)
		}
		requestType, err := ParseRequestType(name)
		if err != nil {
			return err
		}
		if creating && requestType == AccessRequest {
			return fmt.Errorf("%w: access requests are raised complete from the Access Request Form (POST /api/access-requests); they are not drafted", ErrInvalid)
		}
		if creating && requestType == CatalogRequest {
			return fmt.Errorf("%w: catalog requests are raised from a catalog item (POST /api/catalog-requests); they are not drafted", ErrInvalid)
		}
		properties["requestType"] = string(requestType)
	} else if creating {
		return fmt.Errorf("%w: request records require a requestType (%s)", ErrInvalid, strings.Join(RequestTypeNames(), ", "))
	}
	if value, present := properties["state"]; present {
		name, ok := value.(string)
		if !ok {
			return fmt.Errorf("%w: state must be a string", ErrInvalid)
		}
		state, err := ParseRequestState(name)
		if err != nil {
			return err
		}
		if creating && state != RequestDraft {
			return fmt.Errorf("%w: requests are created as drafts; submit them by updating state to %s once a requester is linked", ErrInvalid, RequestSubmitted)
		}
		if state == RequestInReview || state == RequestFulfilled || state == RequestDenied || state == RequestInProgress || state == RequestFailed {
			return fmt.Errorf("%w: state %s is set by the workflow, not by callers", ErrInvalid, state)
		}
		properties["state"] = string(state)
	} else if creating {
		properties["state"] = string(RequestDraft)
	}
	if value, present := properties["criticality"]; present {
		name, ok := value.(string)
		if !ok {
			return fmt.Errorf("%w: criticality must be a string", ErrInvalid)
		}
		criticality, err := ParseCriticality(name)
		if err != nil {
			return err
		}
		properties["criticality"] = string(criticality)
	}
	if value, present := properties["name"]; present {
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%w: name must be a string", ErrInvalid)
		}
	}
	if err := validateContactProperties(properties); err != nil {
		return err
	}
	// SLA is defined on the form's workflow; callers cannot set it on the request.
	delete(properties, "slaEnabled")
	delete(properties, "slaBusinessDays")
	delete(properties, "slaDueAt")
	return nil
}

func normalizeRequestSLA(properties map[string]any) error {
	enabled := false
	if value, present := properties["slaEnabled"]; present {
		flag, ok := value.(bool)
		if !ok {
			return fmt.Errorf("%w: slaEnabled must be a boolean", ErrInvalid)
		}
		enabled = flag
	}
	properties["slaEnabled"] = enabled
	days, err := parseBusinessDays(properties["slaBusinessDays"], "slaBusinessDays")
	if err != nil {
		return err
	}
	if enabled && days < 1 {
		return fmt.Errorf("%w: slaBusinessDays is required when SLA is enabled", ErrInvalid)
	}
	if !enabled {
		days = 0
		delete(properties, "slaDueAt")
	}
	properties["slaBusinessDays"] = days
	return nil
}

func stampSLADueAt(properties map[string]any, start time.Time, holidays []Holiday) {
	if !boolProperty(properties, "slaEnabled") {
		delete(properties, "slaDueAt")
		return
	}
	days, _ := intProperty(properties, "slaBusinessDays")
	if days < 1 {
		return
	}
	properties["slaDueAt"] = AddBusinessDays(start, days, holidays).Format(holidayDateLayout)
}

func (s *Service) applyFormSLA(ctx context.Context, requestType RequestType, properties map[string]any) error {
	workflow, err := s.enabledWorkflow(ctx, requestType)
	if err != nil {
		return err
	}
	if workflow == nil || !workflow.SLAEnabled {
		properties["slaEnabled"] = false
		properties["slaBusinessDays"] = 0
		delete(properties, "slaDueAt")
		return nil
	}
	properties["slaEnabled"] = true
	properties["slaBusinessDays"] = workflow.SLABusinessDays
	return normalizeRequestSLA(properties)
}

// validateContactProperties normalizes the vendor contact details shared by
// vendor CIs and vendor requests. All are optional; an email address, when
// given, must at least have a local part and a domain, and a phone number is
// stored in a single canonical format rather than free text.
func validateContactProperties(properties map[string]any) error {
	for _, key := range ContactProperties() {
		value, present := properties[key]
		if !present {
			continue
		}
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("%w: %s must be a string", ErrInvalid, key)
		}
		text = strings.TrimSpace(text)
		if key == ContactEmailProperty && text != "" {
			at := strings.Index(text, "@")
			if at <= 0 || at == len(text)-1 || strings.ContainsAny(text, " \t") || !strings.Contains(text[at+1:], ".") {
				return fmt.Errorf("%w: %s must be an email address such as name@example.com", ErrInvalid, key)
			}
			text = strings.ToLower(text)
		}
		if key == ContactPhoneProperty && text != "" {
			formatted, ok := NormalizePhoneNumber(text)
			if !ok {
				return fmt.Errorf("%w: %s must be a phone number such as (555) 010-0100 or +44 20 7123 4567", ErrInvalid, key)
			}
			text = formatted
		}
		properties[key] = text
	}
	return nil
}

// NormalizePhoneNumber accepts the common ways people write a phone number
// (spaces, dots, dashes, parentheses, an optional +country code) and returns
// it in one canonical form: North American numbers as "(555) 010-0100" and
// other international numbers as "+" followed by their digits. It reports
// false when the input does not contain a plausible phone number.
func NormalizePhoneNumber(text string) (string, bool) {
	text = strings.TrimSpace(text)
	international := strings.HasPrefix(text, "+")
	var digits strings.Builder
	for _, r := range strings.TrimPrefix(text, "+") {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
		case r == ' ' || r == '-' || r == '.' || r == '(' || r == ')':
			// Formatting characters carry no meaning.
		default:
			return "", false
		}
	}
	number := digits.String()
	if len(number) == 11 && number[0] == '1' {
		number, international = number[1:], false
	}
	switch {
	case len(number) == 10 && !international:
		return fmt.Sprintf("(%s) %s-%s", number[:3], number[3:6], number[6:]), true
	case international && len(number) >= 7 && len(number) <= 15:
		return "+" + number, true
	default:
		return "", false
	}
}

// normalizeBoolProperty accepts a boolean property, or defaults it to false when
// required and omitted (create). Partial updates leave an absent key alone.
func normalizeBoolProperty(properties map[string]any, key string, required bool) error {
	value, present := properties[key]
	if !present {
		if required {
			properties[key] = false
		}
		return nil
	}
	flag, ok := value.(bool)
	if !ok {
		return fmt.Errorf("%w: %s must be a boolean", ErrInvalid, key)
	}
	properties[key] = flag
	return nil
}

func validateCIProperties(kind NodeKind, properties map[string]any, requireCIType bool) error {
	value, hasCIType := properties["ciType"]
	categoryValue, hasCategory := properties["category"]
	contractTypeValue, hasContractType := properties["contractType"]
	hostedValue, hasHosted := properties["hosted"]
	criticalityValue, hasCriticality := properties["criticality"]
	_, hasCritical := properties["critical"]
	_, hasNodeDown := properties["nodeDown"]
	if kind == Request {
		return validateRequestProperties(properties, requireCIType)
	}
	if kind == Incident {
		if err := normalizeBoolProperty(properties, "nodeDown", requireCIType); err != nil {
			return err
		}
	} else if hasNodeDown {
		return fmt.Errorf("%w: nodeDown is only valid for incident records", ErrInvalid)
	}
	if kind != CI {
		if hasCIType {
			return fmt.Errorf("%w: ciType is only valid for CI records", ErrInvalid)
		}
		if hasCategory {
			return fmt.Errorf("%w: category is only valid for CI records", ErrInvalid)
		}
		if hasContractType {
			return fmt.Errorf("%w: contractType is only valid for CI records", ErrInvalid)
		}
		if hasHosted {
			return fmt.Errorf("%w: hosted is only valid for CI records", ErrInvalid)
		}
		if hasCriticality {
			return fmt.Errorf("%w: criticality is only valid for CI records", ErrInvalid)
		}
		if hasCritical {
			return fmt.Errorf("%w: critical is only valid for CI records", ErrInvalid)
		}
		return nil
	}
	if err := normalizeBoolProperty(properties, "critical", requireCIType); err != nil {
		return err
	}
	if hasCriticality {
		criticalityName, ok := criticalityValue.(string)
		if !ok {
			return fmt.Errorf("%w: criticality must be a string", ErrInvalid)
		}
		criticality, err := ParseCriticality(criticalityName)
		if err != nil {
			return err
		}
		properties["criticality"] = string(criticality)
	}
	if hasHosted {
		hostedName, ok := hostedValue.(string)
		if !ok {
			return fmt.Errorf("%w: hosted must be a string", ErrInvalid)
		}
		hosting, err := ParseHostingModel(hostedName)
		if err != nil {
			return err
		}
		properties["hosted"] = string(hosting)
	}
	if hasContractType {
		contractTypeName, ok := contractTypeValue.(string)
		if !ok {
			return fmt.Errorf("%w: contractType must be a string", ErrInvalid)
		}
		contractType, err := ParseContractType(contractTypeName)
		if err != nil {
			return err
		}
		properties["contractType"] = string(contractType)
	}
	if err := validateLocationProperties(properties); err != nil {
		return err
	}
	if err := validateContactProperties(properties); err != nil {
		return err
	}
	if hasCategory {
		categoryName, ok := categoryValue.(string)
		if !ok {
			return fmt.Errorf("%w: category must be a string", ErrInvalid)
		}
		category, err := ParseCICategory(categoryName)
		if err != nil {
			return err
		}
		properties["category"] = string(category)
	}
	if !hasCIType {
		if requireCIType {
			return fmt.Errorf("%w: CI records require a ciType", ErrInvalid)
		}
		return nil
	}
	typeName, ok := value.(string)
	if !ok {
		return fmt.Errorf("%w: ciType must be a string", ErrInvalid)
	}
	ciType, err := ParseCIType(typeName)
	if err != nil {
		return err
	}
	properties["ciType"] = string(ciType)
	if requireCIType && RequiresCategory(ciType) && !hasCategory {
		return fmt.Errorf("%w: %s CI records require a category (%s)", ErrInvalid, ciType, strings.Join(CICategoryNames(), ", "))
	}
	if requireCIType && RequiresContractType(ciType) && !hasContractType {
		return fmt.Errorf("%w: %s CI records require a contractType (%s)", ErrInvalid, ciType, strings.Join(ContractTypeNames(), ", "))
	}
	if requireCIType && RequiresHosting(ciType) && !hasHosted {
		return fmt.Errorf("%w: %s CI records require a hosted value (%s)", ErrInvalid, ciType, strings.Join(HostingModelNames(), ", "))
	}
	if requireCIType && RequiresCriticality(ciType) && !hasCriticality {
		return fmt.Errorf("%w: %s CI records require a criticality (%s)", ErrInvalid, ciType, strings.Join(CriticalityNames(), ", "))
	}
	return nil
}

func (s *Service) RetireNode(ctx context.Context, kind NodeKind, id string) (*Node, error) {
	if _, ok := nodeSpecs[kind]; !ok {
		return nil, fmt.Errorf("%w: unsupported node kind %q", ErrInvalid, kind)
	}
	if err := validateID(id); err != nil {
		return nil, err
	}
	if kind == Identity && id == PlatformSuperAdminID {
		return nil, fmt.Errorf("%w: the platform super admin identity is a system account and cannot be retired", ErrInvalid)
	}
	switch kind {
	case Workflow:
		// Retiring a workflow retires its steps with it.
		if _, err := s.store.RetireWorkflow(ctx, id); err != nil {
			return nil, err
		}
		return s.store.GetNode(ctx, kind, id, true)
	case WorkflowRun:
		// A finished run (completed or returned) can be closed out along with its
		// tasks. A run still in progress is driven from its pending task: return
		// the request from there rather than retiring the run under it.
		run, err := s.store.GetNode(ctx, kind, id, false)
		if err != nil {
			return nil, err
		}
		if run.Properties["state"] == string(RunActive) {
			return nil, fmt.Errorf("%w: workflow run %s is still active; return the request from its pending task instead", ErrInvalid, id)
		}
		if err := s.store.RetireWorkflowRun(ctx, id); err != nil {
			return nil, err
		}
		return s.store.GetNode(ctx, kind, id, true)
	case Task:
		// Only a task that has been acted on can be retired; a pending task is
		// what moves its run along.
		task, err := s.store.GetNode(ctx, kind, id, false)
		if err != nil {
			return nil, err
		}
		if task.Properties["state"] == string(TaskPending) {
			return nil, fmt.Errorf("%w: task %s is still pending; complete, approve, or return it from Workflow Tasks instead", ErrInvalid, id)
		}
		return s.store.RetireNode(ctx, kind, id)
	case WorkflowStep:
		return nil, fmt.Errorf("%w: workflow steps are retired by saving their workflow without them", ErrInvalid)
	}
	return s.store.RetireNode(ctx, kind, id)
}

func (s *Service) CreateRelationship(ctx context.Context, kind RelationshipKind, fromID, toID string, properties map[string]any) (*Relationship, error) {
	if _, ok := relationshipSpecs[kind]; !ok {
		return nil, fmt.Errorf("%w: unsupported relationship kind %q", ErrInvalid, kind)
	}
	if err := validateID(fromID); err != nil {
		return nil, fmt.Errorf("from id: %w", err)
	}
	if err := validateID(toID); err != nil {
		return nil, fmt.Errorf("to id: %w", err)
	}
	cleanProperties, err := validateProperties(properties)
	if err != nil {
		return nil, err
	}
	return s.store.CreateRelationship(ctx, kind, fromID, toID, cleanProperties)
}

func (s *Service) GetRelationship(ctx context.Context, kind RelationshipKind, fromID, toID string, includeRetired bool) (*Relationship, error) {
	if _, ok := relationshipSpecs[kind]; !ok {
		return nil, fmt.Errorf("%w: unsupported relationship kind %q", ErrInvalid, kind)
	}
	if err := validateID(fromID); err != nil {
		return nil, fmt.Errorf("from id: %w", err)
	}
	if err := validateID(toID); err != nil {
		return nil, fmt.Errorf("to id: %w", err)
	}
	return s.store.GetRelationship(ctx, kind, fromID, toID, includeRetired)
}

func (s *Service) ListRelationships(ctx context.Context, kind RelationshipKind, options ListOptions) ([]Relationship, ListTotals, error) {
	if _, ok := relationshipSpecs[kind]; !ok {
		return nil, ListTotals{}, fmt.Errorf("%w: unsupported relationship kind %q", ErrInvalid, kind)
	}
	options, err := options.Validate()
	if err != nil {
		return nil, ListTotals{}, err
	}
	if options.InvolvedID != "" || options.RequestType != "" {
		return nil, ListTotals{}, fmt.Errorf("%w: involvedId and requestType only filter request lists", ErrInvalid)
	}
	return s.store.ListRelationships(ctx, kind, options)
}

func (s *Service) UpdateRelationship(ctx context.Context, kind RelationshipKind, fromID, toID string, properties map[string]any) (*Relationship, error) {
	if _, ok := relationshipSpecs[kind]; !ok {
		return nil, fmt.Errorf("unsupported relationship kind %q", kind)
	}
	if err := validateID(fromID); err != nil {
		return nil, fmt.Errorf("from id: %w", err)
	}
	if err := validateID(toID); err != nil {
		return nil, fmt.Errorf("to id: %w", err)
	}
	cleanProperties, err := validateProperties(properties)
	if err != nil {
		return nil, err
	}
	if len(cleanProperties) == 0 {
		return nil, fmt.Errorf("%w: at least one property is required for update", ErrInvalid)
	}
	return s.store.UpdateRelationship(ctx, kind, fromID, toID, cleanProperties)
}

func (s *Service) RetireRelationship(ctx context.Context, kind RelationshipKind, fromID, toID string) (*Relationship, error) {
	if _, ok := relationshipSpecs[kind]; !ok {
		return nil, fmt.Errorf("%w: unsupported relationship kind %q", ErrInvalid, kind)
	}
	if err := validateID(fromID); err != nil {
		return nil, fmt.Errorf("from id: %w", err)
	}
	if err := validateID(toID); err != nil {
		return nil, fmt.Errorf("to id: %w", err)
	}
	return s.store.RetireRelationship(ctx, kind, fromID, toID)
}

func validateID(id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: id cannot be empty", ErrInvalid)
	}
	return nil
}

func validateProperties(properties map[string]any) (map[string]any, error) {
	clean := make(map[string]any, len(properties))
	for key, value := range properties {
		if strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("%w: property name cannot be empty", ErrInvalid)
		}
		switch strings.ToLower(key) {
		case "id", "code", "status", "retiredat", "submittedat", "returncomment", "returnedat":
			return nil, fmt.Errorf("%w: property %q is managed by the application", ErrInvalid, key)
		}
		if value == nil {
			return nil, fmt.Errorf("%w: property %q cannot be null", ErrInvalid, key)
		}
		switch value.(type) {
		case string, bool, int, int64, float64:
			clean[key] = value
		default:
			return nil, fmt.Errorf("%w: property %q must be a string, boolean, or number", ErrInvalid, key)
		}
	}
	return clean, nil
}
