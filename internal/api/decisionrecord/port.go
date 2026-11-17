// Package decisionrecord serves the read routes of the decision records:
// the namespace route GET /api/dustpan/v1/namespaces/{namespace}/decision-records/{id}
// and the installation route GET /api/dustpan/v1/decision-records/{id}. A decision
// record is written once, at a decision point, and never changes after it, so the
// routes are reads only.
//
// No caller of the routes learns that a record exists, or that it does not: a
// record that the caller may not read, a missing record, a malformed ID and an
// unauthenticated call all answer the same 404 with the code gate.not-found.
package decisionrecord

import (
	"context"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Records is the persistence port of the decision record reads: the one method
// of the gate record store that the reads use. The gate record store satisfies
// it. The adapter that fills it takes the ID and the namespace of the record as
// data, never as SQL: the query binds both as placeholders.
type Records interface {
	// Get returns the record id of ns, or gate.ErrNotFound for an unknown
	// ID and for a record of another namespace. ns is audit.ServerScope
	// for a record of the installation.
	Get(ctx context.Context, ns namespace.Name, id gate.RecordID) (gate.Record, error)
}

// records adapts a gate store to Records. The gate record store is the only
// implementation, so the reads take the query with the placeholders from
// main.
type records struct {
	store gate.Store
}

// NewRecords returns the read port over store. It returns nil for a nil store,
// so a caller can wire the nil and let the start check refuse it.
func NewRecords(store gate.Store) Records {
	if store == nil {
		return nil
	}

	return records{store: store}
}

// Get implements Records.
func (r records) Get(ctx context.Context, ns namespace.Name, id gate.RecordID) (gate.Record, error) {
	return r.store.Records().Get(ctx, ns, id)
}
