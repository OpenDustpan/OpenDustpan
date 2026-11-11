package postgres

import (
	"context"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// UnitStateVersionForTest returns the current state version of the unit, as
// the job store reads it. This file compiles into the test binary only.
func (s *Store) UnitStateVersionForTest(
	ctx context.Context, ns namespace.Name, unitID string,
) (string, error) {
	row, ok, err := unitState(ctx, s.queries, ns.String(), unitID)
	if err != nil {
		return "", err
	}

	if !ok {
		return "", nil
	}

	return row.VersionID, nil
}
