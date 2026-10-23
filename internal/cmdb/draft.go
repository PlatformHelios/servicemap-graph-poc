package cmdb

import (
	"context"
	"fmt"
	"strings"
)

type nodeStatusContextKey struct{}

// WithNodeStatus asks CreateGeneratedNode to write the record as draft.
// Any other value, including empty, stays active.
func WithNodeStatus(ctx context.Context, status string) context.Context {
	return context.WithValue(ctx, nodeStatusContextKey{}, strings.TrimSpace(status))
}

// NodeStatusFromContext returns "draft" when the caller asked for a draft record.
func NodeStatusFromContext(ctx context.Context) string {
	status, _ := ctx.Value(nodeStatusContextKey{}).(string)
	if status == "draft" {
		return "draft"
	}
	return "active"
}

// DraftObject is one record waiting in a process-map package.
type DraftObject struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	CIType string `json:"ciType,omitempty"`
	Status string `json:"status"`
}

// DraftPackage is a process map's unpublished records. ProcessID is empty for
// drafts that are not attached to a process yet.
type DraftPackage struct {
	ProcessID string        `json:"processId,omitempty"`
	Name      string        `json:"name"`
	Objects   []DraftObject `json:"objects"`
}

// DraftRef names one draft record to publish or delete.
type DraftRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// ListDraftPackages returns unpublished process-map records grouped by process.
func (s *Service) ListDraftPackages(ctx context.Context) ([]DraftPackage, error) {
	return s.store.ListDraftPackages(ctx)
}

// DeleteDraftNode removes a record that has never been published.
func (s *Service) DeleteDraftNode(ctx context.Context, kind NodeKind, id string) error {
	if err := validateID(id); err != nil {
		return err
	}
	node, err := s.store.GetNode(ctx, kind, id, false)
	if err != nil {
		return err
	}
	if node.Status != "draft" {
		return fmt.Errorf("%w: %s is %s and cannot be deleted as a draft", ErrInvalid, id, node.Status)
	}
	return s.store.DeleteDraftNode(ctx, kind, id)
}

// PublishDrafts marks the selected draft records active. A process step stays
// draft until its process is already active or is published in the same batch.
func (s *Service) PublishDrafts(ctx context.Context, refs []DraftRef) error {
	if len(refs) == 0 {
		return fmt.Errorf("%w: choose at least one draft record", ErrInvalid)
	}
	seen := map[string]bool{}
	nodes := make([]*Node, 0, len(refs))
	publishing := map[string]bool{}
	for _, ref := range refs {
		kind, err := ParseNodeKind(ref.Kind)
		if err != nil {
			return err
		}
		id := strings.TrimSpace(ref.ID)
		if err := validateID(id); err != nil {
			return err
		}
		key := string(kind) + ":" + id
		if seen[key] {
			continue
		}
		seen[key] = true
		node, err := s.store.GetNode(ctx, kind, id, false)
		if err != nil {
			return err
		}
		if node.Status != "draft" {
			return fmt.Errorf("%w: %s is %s", ErrInvalid, id, node.Status)
		}
		nodes = append(nodes, node)
		if kind == CI && propertyString(node.Properties, "ciType") == string(Process) {
			publishing[id] = true
		}
	}
	for _, node := range nodes {
		if node.Kind != CI || propertyString(node.Properties, "ciType") != string(ProcessStepCI) {
			continue
		}
		process, err := s.processForStep(ctx, node.ID)
		if err != nil {
			return err
		}
		if process.Status == "draft" && !publishing[process.ID] {
			return fmt.Errorf("%w: publish process %s before step %s", ErrInvalid, process.ID, node.ID)
		}
	}
	for _, node := range nodes {
		if err := s.store.SetNodeStatus(ctx, node.Kind, node.ID, "active"); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) processForStep(ctx context.Context, stepID string) (*Node, error) {
	seen := map[string]bool{}
	current := stepID
	for {
		if seen[current] {
			return nil, fmt.Errorf("%w: process step cycle at %s", ErrInvalid, current)
		}
		seen[current] = true
		node, err := s.store.GetNode(ctx, CI, current, false)
		if err != nil {
			return nil, err
		}
		for _, rel := range node.Relationships {
			if rel.Status != "active" || rel.Kind != ProcessStep || rel.ToID != current {
				continue
			}
			return s.store.GetNode(ctx, CI, rel.FromID, false)
		}
		previous := ""
		for _, rel := range node.Relationships {
			if rel.Status == "active" && rel.Kind == NextInProcess && rel.ToID == current {
				previous = rel.FromID
			}
		}
		if previous == "" {
			return nil, fmt.Errorf("%w: step %s is not in a process map", ErrInvalid, stepID)
		}
		current = previous
	}
}
