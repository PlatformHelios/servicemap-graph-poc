package cmdb

import (
	"errors"
	"fmt"
	"strings"
)

var ErrInvalid = errors.New("invalid input")

type NodeKind string

const (
	Identity    NodeKind = "identity"
	JobCode     NodeKind = "job-code"
	Birthright  NodeKind = "birthright"
	Role        NodeKind = "role"
	Entitlement NodeKind = "entitlement"
)

type nodeSpec struct {
	label string
	key   string
}

var nodeSpecs = map[NodeKind]nodeSpec{
	Identity:    {label: "Identity", key: "id"},
	JobCode:     {label: "JobCode", key: "code"},
	Birthright:  {label: "Birthright", key: "id"},
	Role:        {label: "Role", key: "id"},
	Entitlement: {label: "Entitlement", key: "id"},
}

type RelationshipKind string

const (
	HasJobCode   RelationshipKind = "has-job-code"
	QualifiesFor RelationshipKind = "qualifies-for"
	Grants       RelationshipKind = "grants"
	Includes     RelationshipKind = "includes"
)

type relationshipSpec struct {
	typeName string
	from     NodeKind
	to       NodeKind
}

type RelationshipDefinition struct {
	TypeName string
	From     NodeKind
	To       NodeKind
}

var relationshipSpecs = map[RelationshipKind]relationshipSpec{
	HasJobCode:   {typeName: "HAS_JOB_CODE", from: Identity, to: JobCode},
	QualifiesFor: {typeName: "QUALIFIES_FOR", from: JobCode, to: Birthright},
	Grants:       {typeName: "GRANTS", from: Birthright, to: Role},
	Includes:     {typeName: "INCLUDES", from: Role, to: Entitlement},
}

type Node struct {
	Kind          NodeKind       `json:"kind"`
	ID            string         `json:"id"`
	Properties    map[string]any `json:"properties,omitempty"`
	Status        string         `json:"status"`
	RetiredAt     string         `json:"retiredAt,omitempty"`
	Relationships []Relationship `json:"relationships,omitempty"`
}

type Relationship struct {
	Kind       RelationshipKind `json:"kind"`
	FromID     string           `json:"fromId"`
	ToID       string           `json:"toId"`
	Properties map[string]any   `json:"properties,omitempty"`
	Status     string           `json:"status"`
	RetiredAt  string           `json:"retiredAt,omitempty"`
}

func ParseNodeKind(value string) (NodeKind, error) {
	kind := NodeKind(strings.ToLower(strings.TrimSpace(value)))
	if _, ok := nodeSpecs[kind]; !ok {
		return "", fmt.Errorf("%w: unsupported node kind %q", ErrInvalid, value)
	}
	return kind, nil
}

func ParseRelationshipKind(value string) (RelationshipKind, error) {
	kind := RelationshipKind(strings.ToLower(strings.TrimSpace(value)))
	if _, ok := relationshipSpecs[kind]; !ok {
		kind = RelationshipKind(strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "_", "-")))
	}
	if _, ok := relationshipSpecs[kind]; !ok {
		return "", fmt.Errorf("%w: unsupported relationship kind %q", ErrInvalid, value)
	}
	return kind, nil
}

func NodeKinds() []NodeKind {
	return []NodeKind{Identity, JobCode, Birthright, Role, Entitlement}
}

func RelationshipKinds() []RelationshipKind {
	return []RelationshipKind{HasJobCode, QualifiesFor, Grants, Includes}
}

func NodeDefinition(kind NodeKind) (label, key string, err error) {
	spec, ok := nodeSpecs[kind]
	if !ok {
		return "", "", fmt.Errorf("unsupported node kind %q", kind)
	}
	return spec.label, spec.key, nil
}

func RelationshipDefinitionFor(kind RelationshipKind) (RelationshipDefinition, error) {
	spec, ok := relationshipSpecs[kind]
	if !ok {
		return RelationshipDefinition{}, fmt.Errorf("unsupported relationship kind %q", kind)
	}
	return RelationshipDefinition{TypeName: spec.typeName, From: spec.from, To: spec.to}, nil
}
