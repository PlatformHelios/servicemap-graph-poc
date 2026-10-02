package cmdb

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var ErrNotFound = errors.New("record not found")

type Store interface {
	EnsureConstraints(context.Context) error
	CreateGeneratedNode(context.Context, NodeKind, map[string]any, []string) (*Node, error)
	GetNode(context.Context, NodeKind, string, bool) (*Node, error)
	ListNodes(context.Context, NodeKind, bool) ([]Node, error)
	UpdateNode(context.Context, NodeKind, string, map[string]any) (*Node, error)
	RetireNode(context.Context, NodeKind, string) (*Node, error)
	CreateRelationship(context.Context, RelationshipKind, string, string, map[string]any) (*Relationship, error)
	GetRelationship(context.Context, RelationshipKind, string, string, bool) (*Relationship, error)
	ListRelationships(context.Context, RelationshipKind, bool) ([]Relationship, error)
	UpdateRelationship(context.Context, RelationshipKind, string, string, map[string]any) (*Relationship, error)
	RetireRelationship(context.Context, RelationshipKind, string, string) (*Relationship, error)
}

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) EnsureConstraints(ctx context.Context) error {
	return s.store.EnsureConstraints(ctx)
}

func (s *Service) CreateGeneratedNode(ctx context.Context, kind NodeKind, properties map[string]any, ciIDs []string) (*Node, error) {
	if !RequiresGeneratedID(kind) {
		return nil, fmt.Errorf("%w: IDs are not generated for %s records", ErrInvalid, kind)
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
	if len(cleanCIIDs) > 0 && kind != Incident && kind != Change && kind != Event {
		return nil, fmt.Errorf("%w: ciIds are only valid when creating incident, change, or event records", ErrInvalid)
	}
	if RequiresLinkedCIs(kind) && len(cleanCIIDs) == 0 {
		return nil, fmt.Errorf("%w: %s records must be linked to at least one CI", ErrInvalid, kind)
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

func (s *Service) ListNodes(ctx context.Context, kind NodeKind, includeRetired bool) ([]Node, error) {
	if _, ok := nodeSpecs[kind]; !ok {
		return nil, fmt.Errorf("%w: unsupported node kind %q", ErrInvalid, kind)
	}
	return s.store.ListNodes(ctx, kind, includeRetired)
}

func (s *Service) UpdateNode(ctx context.Context, kind NodeKind, id string, properties map[string]any) (*Node, error) {
	if _, ok := nodeSpecs[kind]; !ok {
		return nil, fmt.Errorf("%w: unsupported node kind %q", ErrInvalid, kind)
	}
	if err := validateID(id); err != nil {
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
	return s.store.UpdateNode(ctx, kind, id, cleanProperties)
}

func validateCIProperties(kind NodeKind, properties map[string]any, requireCIType bool) error {
	value, hasCIType := properties["ciType"]
	if kind != CI {
		if hasCIType {
			return fmt.Errorf("%w: ciType is only valid for CI records", ErrInvalid)
		}
		return nil
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
	return nil
}

func (s *Service) RetireNode(ctx context.Context, kind NodeKind, id string) (*Node, error) {
	if _, ok := nodeSpecs[kind]; !ok {
		return nil, fmt.Errorf("%w: unsupported node kind %q", ErrInvalid, kind)
	}
	if err := validateID(id); err != nil {
		return nil, err
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

func (s *Service) ListRelationships(ctx context.Context, kind RelationshipKind, includeRetired bool) ([]Relationship, error) {
	if _, ok := relationshipSpecs[kind]; !ok {
		return nil, fmt.Errorf("%w: unsupported relationship kind %q", ErrInvalid, kind)
	}
	return s.store.ListRelationships(ctx, kind, includeRetired)
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
		case "id", "code", "status", "retiredat":
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
