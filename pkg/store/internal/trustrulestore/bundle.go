package trustrulestore

import (
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// BundleRow is a trust bundle row as the database holds it.
type BundleRow struct {
	ID          string
	Namespace   string
	Active      int64
	Revision    int64
	CreatedAtMs int64
}

// VersionRow is a trust bundle version row as the database holds it.
type VersionRow struct {
	Version         int64
	PEM             string
	CreatedAtMs     int64
	OverlapEndsAtMs int64
}

// CheckBundleNamespace returns ErrInvalid for a namespace that fails
// validation.
func CheckBundleNamespace(ns namespace.Name) error {
	if _, err := namespace.ParseName(ns.String()); err != nil {
		return fmt.Errorf("%w: namespace %q", trustrule.ErrInvalid, ns.String())
	}

	return nil
}

// checkBundleFence returns namespace.ErrFenced when ns is not the namespace of
// the fence.
func checkBundleFence(ns, fence namespace.Name) error {
	// Defense in depth: the fence check of the transaction compares the
	// namespace of the fence with the namespace row. This check stays so that a
	// bundle of one namespace cannot be written under the fence of another
	// namespace, whatever the audit entry says. It does not rely on the
	// service.
	if ns != fence {
		return fmt.Errorf("%w: the write names %q, the fence names %q", namespace.ErrFenced, ns, fence)
	}

	return nil
}

// CheckBundleSave checks a bundle save or rotate before the transaction: the
// namespace, the fence, the overlap and the PEM text. A save has no overlap.
// The time of w is the time of the audit entry, which expiry is checked
// against.
func CheckBundleSave(ns namespace.Name, f trustrule.BundleFields, w BundleWrite, fence namespace.Fence) error {
	if err := CheckBundleNamespace(ns); err != nil {
		return err
	}

	if err := checkBundleFence(ns, fence.Namespace); err != nil {
		return err
	}

	switch {
	case !w.Rotate && f.Overlap != 0:
		return fmt.Errorf("%w: a save has no overlap, use a rotate", trustrule.ErrBundle)
	case f.Overlap < 0 || f.Overlap > trustrule.MaxOverlap:
		return fmt.Errorf("%w: the overlap is 0 to %s, got %s", trustrule.ErrBundle, trustrule.MaxOverlap, f.Overlap)
	}

	if _, err := trustrule.ParseBundle(f.PEM, w.Now); err != nil {
		return fmt.Errorf("save trust bundle: %w", err)
	}

	return nil
}

// CheckBundleDelete checks a version delete before the transaction.
func CheckBundleDelete(ns namespace.Name, fence namespace.Fence) error {
	if err := CheckBundleNamespace(ns); err != nil {
		return err
	}

	return checkBundleFence(ns, fence.Namespace)
}

// BundlePlan is the outcome of the checks of a bundle write: the number of the
// new version, the active version after the write and the numbers of the
// versions to retire.
type BundlePlan struct {
	Next    int64
	Active  int64
	Retired []int64
	// OverlapEndsAtMs is zero for a save and now plus the overlap for a
	// rotate.
	OverlapEndsAtMs int64
	// NowMs is the time of the audit entry in Unix milliseconds: the creation
	// time of the new version.
	NowMs int64
}

// BundleWrite is a bundle save or a bundle rotate. Revision is the revision
// that a rotate expects. Now is the time of the audit entry.
type BundleWrite struct {
	Rotate   bool
	Revision int64
	Now      time.Time
	Overlap  time.Duration
}

// NewBundleWrite returns the write of a bundle save, or of a bundle rotate when
// rotate is true, for the fields f at the time now.
func NewBundleWrite(f trustrule.BundleFields, now time.Time, rotate bool) BundleWrite {
	return BundleWrite{Rotate: rotate, Revision: f.Revision, Now: now, Overlap: f.Overlap}
}

// PlanBundle checks a bundle write against the stored bundle and returns what
// the write changes. A stored row with no ID means that the namespace has no
// bundle yet. A rotate of no bundle gives ErrNotFound, and a rotate of
// another revision than the stored one gives ErrStale. The new version is one
// past the highest stored number. At most trustrule.MaxBundleVersions
// versions stay: the oldest go first, never the active one.
func PlanBundle(stored BundleRow, vs []VersionRow, w BundleWrite) (BundlePlan, error) {
	rotate := w.Rotate
	exists := stored.ID != ""

	if rotate && !exists {
		return BundlePlan{}, trustrule.ErrNotFound
	}

	if rotate && w.Revision != stored.Revision {
		return BundlePlan{}, trustrule.ErrStale
	}

	plan := BundlePlan{Next: 1, Active: 1, Retired: nil, OverlapEndsAtMs: 0, NowMs: w.Now.UnixMilli()}
	numbers := make([]int64, 0, len(vs)+1)

	for i := range vs {
		plan.Next = max(plan.Next, vs[i].Version+1)
		numbers = append(numbers, vs[i].Version)
	}

	if exists {
		plan.Active = stored.Active
	}

	if rotate {
		plan.Active = plan.Next
		plan.OverlapEndsAtMs = w.Now.Add(w.Overlap).UnixMilli()
	}

	numbers = append(numbers, plan.Next)

	var old []int64

	for _, n := range numbers {
		if n != plan.Active {
			old = append(old, n)
		}
	}

	slices.Sort(old)

	if excess := len(numbers) - trustrule.MaxBundleVersions; excess > 0 && excess <= len(old) {
		plan.Retired = old[:excess]
	}

	return plan, nil
}

// Bundle returns the bundle of the stored rows, or ErrCorrupt when they do not
// pass the checks of a bundle. A stored version stays readable after one of its
// CAs expires.
func Bundle(row BundleRow, vs []VersionRow) (trustrule.Bundle, error) {
	id := trustrule.BundleID(row.ID)
	if err := id.Validate(); err != nil {
		return trustrule.Bundle{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}

	if _, err := namespace.ParseName(row.Namespace); err != nil {
		return trustrule.Bundle{}, fmt.Errorf("%w: bundle namespace %q", ErrCorrupt, row.Namespace)
	}

	if row.Revision < 1 {
		return trustrule.Bundle{}, fmt.Errorf("%w: bundle revision %d", ErrCorrupt, row.Revision)
	}

	out := trustrule.Bundle{
		ID: id, Namespace: namespace.Name(row.Namespace), Active: row.Active, Revision: row.Revision,
		CreatedAt: time.UnixMilli(row.CreatedAtMs).UTC(), Versions: make([]trustrule.BundleVersion, 0, len(vs)),
	}

	found := false

	for i := range vs {
		certs, err := trustrule.ParseStoredBundle(vs[i].PEM, time.UnixMilli(vs[i].CreatedAtMs).UTC())
		if err != nil {
			return trustrule.Bundle{}, fmt.Errorf("%w: version %d: %w", ErrCorrupt, vs[i].Version, err)
		}

		v := trustrule.BundleVersion{
			Number: vs[i].Version, Certificates: certs, CreatedAt: time.UnixMilli(vs[i].CreatedAtMs).UTC(),
			OverlapEndsAt: time.Time{},
		}
		if vs[i].OverlapEndsAtMs != 0 {
			v.OverlapEndsAt = time.UnixMilli(vs[i].OverlapEndsAtMs).UTC()
		}

		found = found || v.Number == row.Active

		out.Versions = append(out.Versions, v)
	}

	if !found {
		return trustrule.Bundle{}, fmt.Errorf("%w: the active version %d is not stored", ErrCorrupt, row.Active)
	}

	return out, nil
}

// CheckBundleOfNamespace returns trustrule.ErrNotFound when b is not the
// bundle of the namespace ns. Defense in depth: the query matched the
// namespace. This check stays so that a change of the query cannot serve a
// bundle of another namespace. It does not rely on the query.
func CheckBundleOfNamespace(b trustrule.Bundle, ns namespace.Name) error {
	if b.Namespace != ns {
		return trustrule.ErrNotFound
	}

	return nil
}
