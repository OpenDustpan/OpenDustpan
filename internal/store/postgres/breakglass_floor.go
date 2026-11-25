package postgres

import (
	"context"
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/internal/store/codec"
	"opendustpan.org/opendustpan/internal/store/postgres/internal/db"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

var _ principal.WorkingBreakGlassCounter = (*Store)(nil)

// CountWorkingBreakGlass implements principal.WorkingBreakGlassCounter. The
// count reads the rows of the account at the time at.
func (s *Store) CountWorkingBreakGlass(ctx context.Context, at time.Time) (int64, error) {
	return countWorkingBreakGlass(ctx, s.queries, at)
}

// CountWorkingBreakGlassInNamespace implements
// principal.WorkingBreakGlassCounter. It returns ErrInvalid for an empty ns:
// CountWorkingBreakGlass holds the installation scope.
func (s *Store) CountWorkingBreakGlassInNamespace(ctx context.Context, ns namespace.Name, at time.Time) (int64, error) {
	// Defense in depth: the service does not ask for an empty scope. This
	// check stays so that a new caller cannot read the installation count
	// through the namespace port. It does not rely on the caller.
	if ns == "" {
		return 0, fmt.Errorf("count working break-glass accounts of a namespace: %w", principal.ErrInvalid)
	}

	return countWorkingBreakGlassInNamespace(ctx, s.queries, ns, at)
}

// checkWorkingBreakGlassFloor refuses a change that lowers the working
// break-glass count and leaves it below the floor.
func checkWorkingBreakGlassFloor(before, after int64) error {
	if after < before && after < principal.FloorWorkingPaths {
		return fmt.Errorf("restore leaves too few working break-glass accounts: %w",
			principal.ErrNoWorkingBreakGlass)
	}

	return nil
}

// checkBreakGlassPasswords refuses data that holds a break-glass account with
// a local password. A restore writes the break-glass flag and the passwords
// from the backup, so it is a write that can put the flag on a person with a
// password. It reads with the queries of one transaction, and fails closed
// when the count cannot run.
func checkBreakGlassPasswords(ctx context.Context, q *db.Queries) error {
	n, err := q.CountBreakGlassWithPassword(ctx)
	if err != nil {
		return fmt.Errorf("count break-glass accounts with a password: %w: %w",
			principal.ErrBreakGlassUnavailable, err)
	}

	if n > 0 {
		return fmt.Errorf("restore holds %d break-glass accounts with a local password: %w",
			n, principal.ErrBreakGlassPassword)
	}

	return nil
}

// countWorkingBreakGlass counts the working break-glass accounts of the
// installation at the time at with the queries of one transaction.
func countWorkingBreakGlass(ctx context.Context, q *db.Queries, at time.Time) (int64, error) {
	return countWorkingBreakGlassInNamespace(ctx, q, "", at)
}

// countWorkingBreakGlassInNamespace counts the working break-glass accounts of
// the scope ns at the time at. An empty ns is the installation scope, which
// reads the grants on the installation object; a namespace reads the grants on
// its namespace object.
func countWorkingBreakGlassInNamespace(
	ctx context.Context, q *db.Queries, ns namespace.Name, at time.Time,
) (int64, error) {
	objectKind := grantObjectKindInstallation
	if ns != "" {
		objectKind = grantObjectKindNamespace
	}

	n, err := q.CountWorkingBreakGlassPrincipals(ctx, db.CountWorkingBreakGlassPrincipalsParams{
		Namespace:  string(ns),
		AtMs:       codec.Millis(at),
		ObjectKind: objectKind,
	})
	if err != nil {
		return 0, fmt.Errorf("count working break-glass accounts: %w: %w", principal.ErrBreakGlassUnavailable, err)
	}

	return n, nil
}

// breakGlassScopes holds the working count of every namespace that holds a
// break-glass person in one side of a swap.
type breakGlassScopes struct {
	names  []namespace.Name
	counts map[namespace.Name]int64
}

// breakGlassScopeCounts counts the working break-glass accounts of every
// namespace that holds a break-glass person in the data that q reads, at the
// time at.
func breakGlassScopeCounts(ctx context.Context, q *db.Queries, at time.Time) (breakGlassScopes, error) {
	names, err := q.ListBreakGlassNamespaceScopes(ctx)
	if err != nil {
		return breakGlassScopes{}, fmt.Errorf("list the namespaces of the break-glass accounts: %w: %w",
			principal.ErrBreakGlassUnavailable, err)
	}

	out := breakGlassScopes{
		names:  make([]namespace.Name, 0, len(names)),
		counts: make(map[namespace.Name]int64, len(names)),
	}

	for _, name := range names {
		ns := namespace.Name(name)

		n, err := countWorkingBreakGlassInNamespace(ctx, q, ns, at)
		if err != nil {
			return breakGlassScopes{}, err
		}

		out.names = append(out.names, ns)
		out.counts[ns] = n
	}

	return out, nil
}

// checkNamespaceFloors refuses a swap that lowers the working break-glass
// count of a namespace below the floor. The scopes are the namespaces that
// hold a break-glass person in the live or the staged data; a namespace that
// the staged data drops counts as zero. The count of one namespace never
// counts the accounts of another namespace or of the installation, so a
// restore that drops the accounts of one namespace is refused even when the
// other scopes hold enough of them.
func checkNamespaceFloors(before, after breakGlassScopes) error {
	scopes := make([]namespace.Name, 0, len(before.names)+len(after.names))
	seen := make(map[namespace.Name]bool, len(before.names)+len(after.names))

	for _, ns := range slices.Concat(before.names, after.names) {
		if !seen[ns] {
			seen[ns] = true
			scopes = append(scopes, ns)
		}
	}

	for _, ns := range scopes {
		if err := checkWorkingBreakGlassFloor(before.counts[ns], after.counts[ns]); err != nil {
			return fmt.Errorf("namespace %q: %w", ns, err)
		}
	}

	return nil
}

// The object kinds that the count reads, as the grants store them.
const (
	grantObjectKindInstallation = "installation"
	grantObjectKindNamespace    = "namespace"
)
