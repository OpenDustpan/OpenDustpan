package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ access.Units = (*Store)(nil)

// Tools whose units this store keeps, as the unit references name them.
const (
	pulumiTool   = "pulumi"
	opentofuTool = "opentofu"
)

// Prefixes of the stable unit IDs of each tool (ADR 19).
const (
	stackIDPrefix     = "stk-"
	workspaceIDPrefix = "ws-"
)

// UnitID implements access.Units. A Pulumi unit name is
// "<project>/<stack>", and an OpenTofu unit name is the workspace name.
func (s *Store) UnitID(ctx context.Context, obj identity.Object) (string, bool, error) {
	if !obj.IsUnit() {
		return "", false, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var (
		id  string
		err error
	)

	switch obj.Tool() {
	case pulumiTool:
		project, stack, ok := strings.Cut(obj.UnitName(), "/")
		if !ok {
			return "", false, nil
		}

		id, err = s.queries.UnitStackID(ctx, db.UnitStackIDParams{
			Namespace: obj.Namespace().String(), Project: project, Name: stack,
		})
	case opentofuTool:
		id, err = s.queries.UnitWorkspaceID(ctx, db.UnitWorkspaceIDParams{
			Namespace: obj.Namespace().String(), Name: obj.UnitName(),
		})
	default:
		return "", false, nil
	}

	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("read unit ID: %w", err)
	}

	return id, true, nil
}

// UnitObjects implements access.Units.
func (s *Store) UnitObjects(ctx context.Context, ids []string) (map[string]identity.Object, error) {
	split := splitUnitIDs(ids)
	stacks, workspaces := split.stacks, split.workspaces

	out := make(map[string]identity.Object, len(ids))

	if len(stacks) == 0 && len(workspaces) == 0 {
		return out, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if err := s.unitStacks(ctx, stacks, out); err != nil {
		return nil, err
	}

	if err := s.unitWorkspaces(ctx, workspaces, out); err != nil {
		return nil, err
	}

	return out, nil
}

// unitStacks puts the unit reference of each stack with an ID in ids in out.
func (s *Store) unitStacks(ctx context.Context, ids []string, out map[string]identity.Object) error {
	if len(ids) == 0 {
		return nil
	}

	rows, err := s.queries.UnitStacksByIDs(ctx, ids)
	if err != nil {
		return fmt.Errorf("read stacks of grants: %w", err)
	}

	for i := range rows {
		obj, objErr := unitObject(pulumiTool, rows[i].Namespace, rows[i].Project+"/"+rows[i].Name)
		if objErr != nil {
			return objErr
		}

		out[rows[i].ID] = obj.WithUnitID(rows[i].ID)
	}

	return nil
}

// unitWorkspaces puts the unit reference of each workspace with an ID in ids
// in out.
func (s *Store) unitWorkspaces(ctx context.Context, ids []string, out map[string]identity.Object) error {
	if len(ids) == 0 {
		return nil
	}

	rows, err := s.queries.UnitWorkspacesByIDs(ctx, ids)
	if err != nil {
		return fmt.Errorf("read workspaces of grants: %w", err)
	}

	for i := range rows {
		obj, objErr := unitObject(opentofuTool, rows[i].Namespace, rows[i].Name)
		if objErr != nil {
			return objErr
		}

		out[rows[i].ID] = obj.WithUnitID(rows[i].ID)
	}

	return nil
}

// unitObject returns the unit reference of a stored unit.
func unitObject(tool, ns, name string) (identity.Object, error) {
	obj, err := identity.UnitObject(tool, namespace.Name(ns), name)
	if err != nil {
		return identity.Object{}, fmt.Errorf("decode unit %s:%s/%s: %w", tool, ns, name, err)
	}

	return obj, nil
}

// unitIDs are the stable IDs of units, by tool.
type unitIDs struct {
	stacks     []string
	workspaces []string
}

// splitUnitIDs returns the stack IDs and the workspace IDs in ids. Any
// other ID names no unit.
func splitUnitIDs(ids []string) unitIDs {
	var out unitIDs

	for _, id := range ids {
		switch {
		case strings.HasPrefix(id, stackIDPrefix):
			out.stacks = append(out.stacks, id)
		case strings.HasPrefix(id, workspaceIDPrefix):
			out.workspaces = append(out.workspaces, id)
		default:
		}
	}

	return out
}
