package devstrip

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/backup"
)

// reporter builds the report while the strip removes rows.
type reporter struct {
	report backup.DevStripReport
}

func newReporter() *reporter {
	return &reporter{report: backup.DevStripReport{
		Principals:      nil,
		OtherPrincipals: 0,
		OtherCounts:     map[backup.DevRemovalClass]int64{},
		Classes:         map[backup.DevRemovalClass]int64{},
		Approvers:       nil,
		Warnings:        nil,
	}}
}

// add adds n to the total of class when n is above zero.
func (r *reporter) add(class backup.DevRemovalClass, n int64) {
	if n > 0 {
		r.report.Classes[class] += n
	}
}

// principals reads the dev principals: the count, and the first
// MaxAuditedPrincipals of them with their cleaned subjects. It returns their
// IDs. It removes nothing.
func (r *reporter) principals(ctx context.Context, q Querier, d Dialect) ([]string, error) {
	total, err := countRows(ctx, q, "SELECT COUNT(*) FROM principals WHERE "+d.matchSubject())
	if err != nil {
		return nil, err
	}

	r.add(backup.RemovedPrincipals, total)

	rows, err := q.Query(ctx, fmt.Sprintf(
		"SELECT id, subject FROM principals WHERE %s ORDER BY subject, id LIMIT %d",
		d.matchSubject(), backup.MaxAuditedPrincipals,
	))
	if err != nil {
		return nil, fmt.Errorf("read the dev principals of the staging area: %w", backup.ErrExportRead)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // Best-effort close of a finished read.
	}()

	var ids []string

	for rows.Next() {
		var id, subject string
		if err := rows.Scan(&id, &subject); err != nil {
			return nil, fmt.Errorf("read the dev principals of the staging area: %w", backup.ErrExportRead)
		}

		// Defense in depth: the service cleans the subject again before it
		// writes it. This layer does not rely on that, so a report that
		// another caller reads holds no control character.
		clean, cut := backup.CleanSubject(subject)
		ids = append(ids, id)
		r.report.Principals = append(r.report.Principals, backup.DevRemoval{
			Subject:    clean,
			SubjectCut: cut,
			Counts:     map[backup.DevRemovalClass]int64{},
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the dev principals of the staging area: %w", backup.ErrExportRead)
	}

	r.report.OtherPrincipals = max(total-int64(len(ids)), 0)

	return ids, nil
}

// class counts the rows of c that go with the dev principals, assigns the
// counts to the first principals and to the others, and removes the rows.
func (r *reporter) class(ctx context.Context, q Querier, d Dialect, c class, ids []string) error {
	total, err := countRows(ctx, q, "SELECT COUNT(*) FROM "+c.table+" WHERE "+c.where(d))
	if err != nil {
		return err
	}

	r.add(c.name, total)

	if total > 0 && len(ids) > 0 {
		if err := r.perPrincipal(ctx, q, d, c, ids); err != nil {
			return err
		}
	}

	if total == 0 {
		return nil
	}

	if _, err := q.Exec(ctx, "DELETE FROM "+c.table+" WHERE "+c.where(d)); err != nil {
		return fmt.Errorf("remove dev rows of table %q: %w", c.table, backup.ErrExportRead)
	}

	return nil
}

// perPrincipal fills the counts of c for the listed principals, and the sum
// for the other dev principals.
func (r *reporter) perPrincipal(ctx context.Context, q Querier, d Dialect, c class, ids []string) error {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}

	counts, err := groupCounts(ctx, q, c, args)
	if err != nil {
		return err
	}

	for i, id := range ids {
		if n := counts[id]; n > 0 {
			r.report.Principals[i].Counts[c.name] = n
		}
	}

	only := ""
	if c.only != "" {
		only = c.only + " AND "
	}

	other, err := countRows(ctx, q,
		"SELECT COUNT(*) FROM "+c.table+" WHERE "+only+c.link+" IN "+d.devPrincipals()+
			" AND "+c.link+" NOT IN ("+placeholders(len(ids))+")",
		args...,
	)
	if err != nil {
		return err
	}

	if other > 0 {
		r.report.OtherCounts[c.name] = other
	}

	return nil
}

// groupCounts counts the rows of c for each principal ID in args.
func groupCounts(ctx context.Context, q Querier, c class, args []any) (map[string]int64, error) {
	fail := fmt.Errorf("count dev rows of table %q: %w", c.table, backup.ErrExportRead)

	rows, err := q.Query(ctx,
		"SELECT "+c.link+", COUNT(*) FROM "+c.table+" WHERE "+c.ofPrincipals(len(args))+" GROUP BY "+c.link,
		args...,
	)
	if err != nil {
		return nil, fail
	}

	counts := map[string]int64{}

	for rows.Next() {
		var (
			id string
			n  int64
		)

		if scanErr := rows.Scan(&id, &n); scanErr != nil {
			_ = rows.Close() //nolint:errcheck // The scan error is the cause.

			return nil, fail
		}

		counts[id] = n
	}

	err = rows.Err()
	if closeErr := rows.Close(); closeErr != nil && err == nil {
		err = closeErr
	}

	if err != nil {
		return nil, fail
	}

	return counts, nil
}

// remove removes the dev principals, the providers with the reserved ID and
// the sign-in requests that name it.
func (r *reporter) remove(ctx context.Context, q Querier, d Dialect) error {
	steps := []struct {
		class backup.DevRemovalClass
		table string
		where string
		skip  bool
	}{
		{class: backup.RemovedPrincipals, table: "principals", where: d.matchSubject(), skip: true},
		{class: backup.RemovedProviders, table: "sign_in_providers", where: d.provider("provider_id"), skip: false},
		{class: backup.RemovedProviders, table: "oidc_providers", where: d.provider("provider_id"), skip: false},
		{class: backup.RemovedSignInRequests, table: "sign_in_requests", where: d.provider("provider"), skip: false},
	}

	for _, step := range steps {
		n, err := q.Exec(ctx, "DELETE FROM "+step.table+" WHERE "+step.where)
		if err != nil {
			return fmt.Errorf("remove dev rows of table %q: %w", step.table, backup.ErrExportRead)
		}

		if !step.skip {
			r.add(step.class, n)
		}
	}

	return nil
}

// dangling removes the rows that name a principal that the staging area does
// not hold: grants, group memberships, sessions and access tokens. The
// foreign keys of the staged data may be wrong already, so the strip does not
// rely on them.
func (r *reporter) dangling(ctx context.Context, q Querier) error {
	const missing = "NOT IN (SELECT id FROM principals)"

	for _, step := range []struct{ table, where string }{
		{
			table: "grants",
			where: "subject_kind = 'principal' AND (subject_id " + missing +
				" OR subject NOT IN (SELECT 'principal:' || id FROM principals))",
		},
		{table: "group_members", where: "principal_id " + missing},
		{table: "sessions", where: "principal_id " + missing},
		{table: "access_tokens", where: "principal_id " + missing},
	} {
		n, err := q.Exec(ctx, "DELETE FROM "+step.table+" WHERE "+step.where)
		if err != nil {
			return fmt.Errorf("remove dangling rows of table %q: %w", step.table, backup.ErrExportRead)
		}

		r.add(backup.RemovedDangling, n)
	}

	return nil
}
