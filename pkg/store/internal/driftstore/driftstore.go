// Package driftstore holds the rules of the drift result table that the SQLite
// and PostgreSQL adapters share: the JSON form of the resources, the check of a
// stored row, and the rules of Record that need one transaction. An adapter
// gives it a Tx, which reads and writes the rows of one transaction, so the
// adapters differ in their queries only.
//
// A stored row is untrusted when it is read, because a backup, a restore or
// another version of the server can have written it.
package driftstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
)

// ErrCorrupt reports a stored row that does not pass the checks of a drift
// result. The adapters do not serve such a row.
var ErrCorrupt = errors.New("stored drift result is not valid")

// Row is one stored drift result.
type Row struct {
	Namespace     string
	Tool          string
	UnitID        string
	JobID         string
	Status        string
	CheckedAtMs   int64
	Truncated     bool
	ResourcesJSON string
}

// Tx reads and writes the rows of one transaction. The adapter has checked the
// fence of the namespace in that transaction before it calls Record.
type Tx interface {
	// Job returns the job id, and false when none exists.
	Job(ctx context.Context, id job.ID) (jobstore.Row, bool, error)
	// Latest returns the stored result of unit, and false when none exists.
	Latest(ctx context.Context, unit graph.Unit) (Row, bool, error)
	// Put stores r as the result of its unit and replaces an earlier row that
	// is older. It returns an error that wraps drift.ErrStale when a row of
	// the unit is not older than r (a concurrent writer stored a newer one
	// after Latest), and an error that wraps ErrCorrupt when the unit ID
	// belongs to another namespace or another tool.
	Put(ctx context.Context, r Row) error
}

type resourceDoc struct {
	Address string `json:"address"`
	Kind    string `json:"kind"`
}

// EncodeResources returns the JSON of resources. The list keeps its order.
func EncodeResources(resources []drift.Resource) (string, error) {
	docs := make([]resourceDoc, 0, len(resources))
	for _, r := range resources {
		docs = append(docs, resourceDoc{Address: r.Address, Kind: string(r.Kind)})
	}

	b, err := json.Marshal(docs)
	if err != nil {
		return "", fmt.Errorf("encode drift resources: %w", err)
	}

	return string(b), nil
}

// decodeResources reads the JSON that EncodeResources wrote. It refuses a field
// that it does not know and text after the document.
func decodeResources(s string) ([]drift.Resource, error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(s)))
	dec.DisallowUnknownFields()

	var docs []resourceDoc
	if err := dec.Decode(&docs); err != nil {
		return nil, fmt.Errorf("%w: resources: %w", ErrCorrupt, err)
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: resources: text after the document", ErrCorrupt)
	}

	out := make([]drift.Resource, 0, len(docs))
	for _, d := range docs {
		out = append(out, drift.Resource{Address: d.Address, Kind: drift.Kind(d.Kind)})
	}

	return out, nil
}

// Encode returns the row of r. The caller has validated r.
func Encode(r *drift.Result) (Row, error) {
	resources, err := EncodeResources(r.Resources)
	if err != nil {
		return Row{}, err
	}

	return Row{
		Namespace: r.Unit.Namespace.String(), Tool: r.Unit.Tool, UnitID: r.Unit.ID, JobID: string(r.JobID),
		Status: string(r.Status()), CheckedAtMs: r.CheckedAt.UnixMilli(), Truncated: r.Truncated,
		ResourcesJSON: resources,
	}, nil
}

// Latest returns the drift result that row holds. It returns an error that
// wraps ErrCorrupt for a row that fails the checks of a result, or whose status
// is not the status that its resources give.
//
// Defense in depth: Record validates a result before it writes it. This check
// stays so that a row that a backup, a restore or another version wrote is never
// served.
func Latest(row *Row) (drift.Latest, error) {
	resources, err := decodeResources(row.ResourcesJSON)
	if err != nil {
		return drift.Latest{}, err
	}

	r := drift.Result{
		CheckedAt: time.UnixMilli(row.CheckedAtMs).UTC(),
		Unit:      graph.Unit{Namespace: namespace.Name(row.Namespace), Tool: row.Tool, ID: row.UnitID},
		JobID:     job.ID(row.JobID), Resources: resources, Truncated: row.Truncated,
	}

	if err := r.Validate(); err != nil {
		return drift.Latest{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}

	if row.Status != string(r.Status()) {
		return drift.Latest{}, fmt.Errorf("%w: status %q does not follow from %d resources", ErrCorrupt, row.Status,
			len(resources))
	}

	return drift.Latest{
		CheckedAt: r.CheckedAt, Unit: r.Unit, JobID: r.JobID, Status: r.Status(), Resources: resources,
		Truncated: r.Truncated,
	}, nil
}

// Record stores r in the transaction of tx and returns the change of the drift
// status. The adapter calls it in a transaction that holds the fence of the
// namespace of r.Unit.
//
// It returns drift.ErrInvalid when r fails validation, drift.ErrJobMismatch
// when r.JobID is not a succeeded drift check of r.Unit or r.CheckedAt is
// outside the run of the job, and drift.ErrStale when r is not newer than the
// stored result or comes from the job of the stored result. It changes nothing
// on an error.
func Record(ctx context.Context, tx Tx, r *drift.Result) (drift.Transition, error) {
	// An error returns the zero transition, so that no caller reads a result
	// from it.
	var none drift.Transition

	// Defense in depth: the adapter validates r before it opens the
	// transaction. This check stays so that a new caller cannot store a result
	// that the adapter did not check.
	if err := r.Validate(); err != nil {
		return none, fmt.Errorf("record drift result: %w", err)
	}

	if err := checkJob(ctx, tx, r); err != nil {
		return none, err
	}

	from, err := previous(ctx, tx, r)
	if err != nil {
		return none, err
	}

	row, err := Encode(r)
	if err != nil {
		return none, err
	}

	if err := tx.Put(ctx, row); err != nil {
		return none, fmt.Errorf("store drift result: %w", err)
	}

	return drift.Transition{At: r.CheckedAt, Unit: r.Unit, JobID: r.JobID, From: from, To: r.Status()}, nil
}

// previous returns the status of the stored result of r.Unit, or
// drift.StatusUnknown when it has none. It returns drift.ErrStale when r is not
// newer. A stored row that fails its checks counts as none: the new result has
// passed validation, so it replaces the bad row.
func previous(ctx context.Context, tx Tx, r *drift.Result) (drift.Status, error) {
	row, ok, err := tx.Latest(ctx, r.Unit)
	if err != nil {
		return "", fmt.Errorf("read drift result: %w", err)
	}

	if !ok {
		return drift.StatusUnknown, nil
	}

	prev, err := Latest(&row)
	if err != nil {
		return drift.StatusUnknown, nil //nolint:nilerr // A damaged row is replaced by the new, valid result.
	}

	if prev.JobID == r.JobID || !r.CheckedAt.After(prev.CheckedAt) {
		return "", drift.ErrStale
	}

	return prev.Status, nil
}

// checkJob returns drift.ErrJobMismatch, wrapped with the reason, unless
// r.JobID is a succeeded drift check of r.Unit and r.CheckedAt is inside its
// run, from its creation to its end.
func checkJob(ctx context.Context, tx Tx, r *drift.Result) error {
	row, ok, err := tx.Job(ctx, r.JobID)
	if err != nil {
		return fmt.Errorf("read job: %w", err)
	}

	if !ok {
		return fmt.Errorf("job %s does not exist: %w", r.JobID, drift.ErrJobMismatch)
	}

	j, err := jobstore.Decode(&row)
	if err != nil {
		// A damaged job is never the job of a result. The text of the cause
		// stays out of the answer.
		return fmt.Errorf("job %s cannot be read: %w", r.JobID, drift.ErrJobMismatch)
	}

	// Each check below holds alone. The row and the body both name the unit.
	switch {
	case row.Namespace != r.Unit.Namespace || row.UnitID != r.Unit.ID || j.Spec.Unit.Tool() != r.Unit.Tool:
		return fmt.Errorf("job %s belongs to another unit: %w", r.JobID, drift.ErrJobMismatch)
	case j.Spec.Purpose != job.PurposeDriftCheck:
		return fmt.Errorf("job %s is not a drift check: %w", r.JobID, drift.ErrJobMismatch)
	case j.State != job.StateSucceeded:
		return fmt.Errorf("job %s is %s, not succeeded: %w", r.JobID, j.State, drift.ErrJobMismatch)
	case r.CheckedAt.Before(j.CreatedAt) || r.CheckedAt.After(j.UpdatedAt):
		return fmt.Errorf("job %s: checked outside the run of the job: %w", r.JobID, drift.ErrJobMismatch)
	default:
		return nil
	}
}
