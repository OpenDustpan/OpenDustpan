// Package devstrip holds the logic of the SQL adapters of backup.DevStripper
// that does not depend on the SQL dialect: which staged rows belong to a dev
// principal, how many of them there are, how they are removed, and how the
// removal is proved. An adapter gives it a Querier over one transaction on the
// staging area and a Dialect with the two string functions that differ. So the
// adapters differ in these two fragments only, and the match of a dev subject
// is written once.
//
// The match is the one of principal.IsDevSubject: the first 4 bytes, ASCII
// case-insensitive, equal to "dev:". The SQL does not fold a non-ASCII letter.
// The code never trusts the foreign keys of the staged data: it finds rows
// that name a missing principal too.
package devstrip

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"opendustpan.org/opendustpan/pkg/backup"
)

// Querier runs statements inside one transaction on the staging area. The
// placeholder of every statement is "?". An adapter whose driver uses another
// placeholder calls Rebind.
type Querier interface {
	// Exec runs a statement and returns the count of rows that it changed.
	Exec(ctx context.Context, query string, args ...any) (int64, error)
	// Query runs a query and returns its rows. The caller closes them.
	Query(ctx context.Context, query string, args ...any) (Rows, error)
}

// Rows is the cursor of a query.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close() error
}

// Dialect holds the SQL fragments that differ between the engines.
type Dialect struct {
	// Lower returns SQL that lowers the ASCII letters of expr and no other
	// character.
	Lower func(expr string) string
	// Left4 returns SQL for the first 4 characters of expr.
	Left4 func(expr string) string
}

// ErrDevRowsRemain reports dev rows that remain in the staging area after the strip.
var ErrDevRowsRemain = errors.New("dev rows remain in the staging area")

// ReservedProvider is the provider ID that the dev sign-in uses. The match
// folds ASCII letters, as the match of a subject does.
const ReservedProvider = "dev"

// colPrincipalID is the column that links a row to a principal.
const colPrincipalID = "principal_id"

// devPrefix is the prefix of a dev subject, in lower case.
const devPrefix = "dev:"

// matchSubject returns the SQL condition that the column subject holds a dev subject.
func (d Dialect) matchSubject() string {
	return d.Lower(d.Left4("subject")) + " = '" + devPrefix + "'"
}

// provider returns the SQL condition that col holds the reserved provider ID.
func (d Dialect) provider(col string) string {
	return d.Lower(col) + " = '" + ReservedProvider + "'"
}

// devPrincipals is the sub-select of the IDs of the dev principals.
func (d Dialect) devPrincipals() string {
	return "(SELECT id FROM principals WHERE " + d.matchSubject() + ")"
}

// devPrincipalSubjects is the sub-select of the text form ("principal:<id>")
// of the dev principals. A grant holds its holder twice, as subject and as
// subject_id, and the two can disagree in a hostile backup. The strip removes a
// grant that names a dev principal in either, and does not rely on the other.
func (d Dialect) devPrincipalSubjects() string {
	return "(SELECT 'principal:' || id FROM principals WHERE " + d.matchSubject() + ")"
}

// Rebind turns the "?" placeholders of query into the numbered placeholders
// "$1", "$2" and so on.
func Rebind(query string) string {
	var b strings.Builder

	n := 0

	for i := range len(query) {
		if query[i] != '?' {
			b.WriteByte(query[i])

			continue
		}

		n++

		fmt.Fprintf(&b, "$%d", n)
	}

	return b.String()
}

// class describes a table whose rows belong to a principal.
type class struct {
	name backup.DevRemovalClass
	// table is the table, and link its column that holds the principal ID.
	table, link string
	// only narrows the rows that name a principal: the grants table holds
	// group subjects too.
	only string
	// extra is the SQL condition of rows that go with the class without a
	// removed principal. It is a function of the dialect.
	extra func(d Dialect) string
}

// classes lists the tables that a principal owns, in the order of the report.
//
//nolint:gochecknoglobals // A frozen table of the classes, like the table lists of pkg/backup.
var classes = []class{
	{
		name:  backup.RemovedGrants,
		table: "grants",
		link:  "subject_id",
		only:  "subject_kind = 'principal'",
		extra: func(d Dialect) string { return "subject_kind = 'principal' AND subject IN " + d.devPrincipalSubjects() },
	},
	{name: backup.RemovedGroupMembers, table: "group_members", link: colPrincipalID, only: "", extra: nil},
	{
		name: backup.RemovedSessions, table: "sessions", link: colPrincipalID, only: "",
		extra: func(d Dialect) string { return d.provider("provider") },
	},
	{name: backup.RemovedAccessTokens, table: "access_tokens", link: colPrincipalID, only: "", extra: nil},
	{name: backup.RemovedPreferences, table: "person_preferences", link: colPrincipalID, only: "", extra: nil},
	{
		name: backup.RemovedTempAdmins, table: "temp_admins", link: colPrincipalID, only: "",
		extra: func(d Dialect) string { return d.matchSubject() },
	},
}

// where returns the SQL condition of the rows of c that the strip removes.
func (c class) where(d Dialect) string {
	cond := c.link + " IN " + d.devPrincipals()
	if c.only != "" {
		cond = c.only + " AND " + cond
	}

	if c.extra != nil {
		cond = "(" + cond + ") OR (" + c.extra(d) + ")"
	}

	return cond
}

// ofPrincipals returns the SQL condition of the rows of c that name a
// principal in the list of ids. It adds one placeholder for each ID.
func (c class) ofPrincipals(n int) string {
	cond := c.link + " IN (" + placeholders(n) + ")"
	if c.only != "" {
		cond = c.only + " AND " + cond
	}

	return cond
}

// placeholders returns n placeholders separated by commas.
func placeholders(n int) string {
	if n <= 0 {
		return "NULL"
	}

	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// Run removes the dev data from the staging area through q and returns what
// it removed. The caller owns the transaction: it commits to remove, and rolls
// back to look. The match is d. After the removal of the dev data, Run removes
// the dangling rows when it found dev data, and no other time: a restore
// without dev data changes no row of another person.
func Run(ctx context.Context, q Querier, d Dialect) (backup.DevStripReport, error) {
	r := newReporter()

	ids, err := r.principals(ctx, q, d)
	if err != nil {
		return backup.DevStripReport{}, err
	}

	for _, c := range classes {
		if err := r.class(ctx, q, d, c, ids); err != nil {
			return backup.DevStripReport{}, err
		}
	}

	if err := r.remove(ctx, q, d); err != nil {
		return backup.DevStripReport{}, err
	}

	if err := removeApprovers(ctx, q, d, r); err != nil {
		return backup.DevStripReport{}, err
	}

	if r.report.HasDev() {
		if err := r.dangling(ctx, q); err != nil {
			return backup.DevStripReport{}, err
		}
	}

	return r.report, nil
}

// fixedCountQueries is the number of count queries that do not depend on a class.
const fixedCountQueries = 4

// Count returns the number of rows in the staging area that hold a dev
// principal or name one, and the number of dev approvers. It reads only. The
// swap check and the proof after the removal use it, so they share one
// definition of dev data with the strip.
func Count(ctx context.Context, q Querier, d Dialect) (int64, error) {
	var total int64

	queries := make([]string, 0, fixedCountQueries+len(classes))
	queries = append(queries,
		"SELECT COUNT(*) FROM principals WHERE "+d.matchSubject(),
		"SELECT COUNT(*) FROM sign_in_providers WHERE "+d.provider("provider_id"),
		"SELECT COUNT(*) FROM oidc_providers WHERE "+d.provider("provider_id"),
		"SELECT COUNT(*) FROM sign_in_requests WHERE "+d.provider("provider"),
	)

	for _, c := range classes {
		queries = append(queries, "SELECT COUNT(*) FROM "+c.table+" WHERE "+c.where(d))
	}

	for _, query := range queries {
		n, err := countRows(ctx, q, query)
		if err != nil {
			return 0, err
		}

		total += n
	}

	approvers, err := countApprovers(ctx, q)
	if err != nil {
		return 0, err
	}

	return total + approvers, nil
}

// countRows runs a query that returns one count.
func countRows(ctx context.Context, q Querier, query string, args ...any) (int64, error) {
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("count staged rows: %w", backup.ErrExportRead)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // Best-effort close of a finished read.
	}()

	var n int64

	if !rows.Next() {
		return 0, fmt.Errorf("count staged rows: %w", backup.ErrExportRead)
	}

	if err := rows.Scan(&n); err != nil {
		return 0, fmt.Errorf("count staged rows: %w", backup.ErrExportRead)
	}

	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("count staged rows: %w", backup.ErrExportRead)
	}

	return n, nil
}
