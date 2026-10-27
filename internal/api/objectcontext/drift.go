package objectcontext

import (
	"context"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/objectcontext"
)

// driftSource implements objectcontext.DriftReader over the drift store. It
// checks no access: the read API checks unit:read on the unit before it asks.
type driftSource struct {
	results drift.Store
}

var _ objectcontext.DriftReader = driftSource{results: nil}

// Drift implements objectcontext.DriftReader. A unit with no result gives the
// status unknown, never in-sync: nothing has said that the unit matches its
// state.
func (s driftSource) Drift(ctx context.Context, unit graph.Unit) (objectcontext.DriftData, error) {
	unknown := objectcontext.DriftData{
		CheckedAt: nil, Status: string(drift.StatusUnknown), JobID: "", Addresses: []string{}, Truncated: false,
	}

	latest, err := s.results.GetLatest(ctx, unit)

	switch {
	case errors.Is(err, drift.ErrNotFound):
		return unknown, nil
	case err != nil:
		return unknown, fmt.Errorf("latest drift result: %w", err)
	}

	// Defense in depth: the store reads the result of the unit of the call.
	// This check stays so that a result of another unit never answers under
	// this one, whatever a store gives back.
	if latest.Unit != unit || !latest.Status.Valid() {
		return unknown, nil
	}

	checkedAt := latest.CheckedAt
	out := objectcontext.DriftData{
		CheckedAt: &checkedAt, Status: string(latest.Status), JobID: string(latest.JobID),
		Addresses: make([]string, 0, len(latest.Resources)), Truncated: latest.Truncated,
	}

	for _, res := range latest.Resources {
		out.Addresses = append(out.Addresses, res.Address)
	}

	return out, nil
}
