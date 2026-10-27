package postgres

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// TestAccessByIDReadsUseThePrimaryKey proves that each statement that reads
// or changes a live principal, role, grant or profile by its ID uses the
// primary key of its table. A partial index of the live rows must not serve
// them: on a database with no statistics and with a generic plan, that index
// can win, and then each call reads all the live rows of the index. pgx
// caches a plan for each connection, so the test explains the generic plan of
// each statement on a fresh database.
func TestAccessByIDReadsUseThePrimaryKey(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	recorder := &sqlRecorder{sql: ""}
	conn := genericPlanConn(ctx, t, openKeepLockStore(t, testPostgresURL(t)), recorder)

	for i, tc := range accessPlanCases(ctx, db.New(conn)) {
		// The database holds no row, so a read finds no row.
		if sendErr := tc.send(); sendErr != nil && !errors.Is(sendErr, pgx.ErrNoRows) {
			t.Fatalf("%s: %v", tc.name, sendErr)
		}

		plan, planErr := genericPlan(ctx, conn, "access_plan_"+strconv.Itoa(i), recorder.sql, tc.args)
		if planErr != nil {
			t.Fatalf("%s: %v", tc.name, planErr)
		}

		lookup := lookupPlan(plan, tc.name)
		if !strings.Contains(lookup, tc.pkey) || usesLiveIndex(lookup) {
			t.Errorf("%s: the generic plan does not use %s:\n%s", tc.name, tc.pkey, plan)
		}
	}
}

// TestReplayByIDStatementsUseThePrimaryKey proves that the statements of the
// replay of a revocation that change one live principal or grant by its ID
// use the primary key of its table, as TestAccessByIDReadsUseThePrimaryKey
// proves for the generated statements.
func TestReplayByIDStatementsUseThePrimaryKey(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := openKeepLockStore(t, testPostgresURL(t))
	conn := genericPlanConn(ctx, t, s, nil)
	live := liveSchema(ctx, t, s)

	for i, tc := range []struct {
		kind string
		args string
		pkey string
		step int
	}{
		{kind: maintenance.RevokedPrincipal, step: 0, args: "('p-x', '1')", pkey: "principals_pkey"},
		{kind: maintenance.RevokedTempAdmin, step: 1, args: "('p-x', '1')", pkey: "principals_pkey"},
		{kind: maintenance.RevokedGrant, step: 0, args: "('g-x', '1', 'x')", pkey: "grants_pkey"},
	} {
		steps, ok := replayPlan(live, tc.kind)
		if !ok || len(steps) <= tc.step {
			t.Fatalf("%s: replay plan has no step %d", tc.kind, tc.step)
		}

		plan, err := genericPlan(ctx, conn, "replay_plan_"+strconv.Itoa(i), steps[tc.step].sql, tc.args)
		if err != nil {
			t.Fatalf("%s: %v", tc.kind, err)
		}

		lookup := lookupPlan(plan, "replay "+tc.kind)
		if !strings.Contains(lookup, tc.pkey) || usesLiveIndex(lookup) {
			t.Errorf("%s step %d: the generic plan does not use %s:\n%s", tc.kind, tc.step, tc.pkey, plan)
		}
	}
}

// genericPlanConn opens a connection of s with tracer, which can be nil, and
// with plan_cache_mode = force_generic_plan. The test closes it at the end.
func genericPlanConn(ctx context.Context, t *testing.T, s *Store, tracer pgx.QueryTracer) *pgx.Conn {
	t.Helper()

	cfg := s.pool.Config().ConnConfig.Copy()
	cfg.Tracer = tracer

	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(context.WithoutCancel(ctx)); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	})

	if _, err = conn.Exec(ctx, "SET plan_cache_mode = force_generic_plan"); err != nil {
		t.Fatalf("set plan cache mode: %v", err)
	}

	return conn
}

// accessPlanCase is one statement of TestAccessByIDReadsUseThePrimaryKey: the
// call that sends it, the arguments of its EXECUTE, and the primary key that
// its plan must use.
type accessPlanCase struct {
	send func() error
	name string
	args string
	pkey string
}

// lookupPlan returns the part of plan that is the lookup of the row by its ID:
// the whole plan, or the plan before its first SubPlan for a statement that
// carries the check of the last break-glass account. That check is a SubPlan
// over the few live rows of the installation, not a lookup by ID.
func lookupPlan(plan, statement string) string {
	if statement != "DeletePrincipal" && statement != "replay "+maintenance.RevokedPrincipal {
		return plan
	}

	lookup, _, _ := strings.Cut(plan, "SubPlan")

	return lookup
}

// accessPlanCases returns the statements that look up one live row of the
// access and profile tables by its ID, sent through q.
func accessPlanCases(ctx context.Context, q *db.Queries) []accessPlanCase {
	return []accessPlanCase{
		{name: "GetGrant", args: "('g-x')", pkey: "grants_pkey", send: func() error {
			_, err := q.GetGrant(ctx, "g-x")

			return err
		}},
		{name: "RevokeGrant", args: "('1', 'x', 'g-x')", pkey: "grants_pkey", send: func() error {
			_, err := q.RevokeGrant(ctx, db.RevokeGrantParams{
				RevokedAtMs: pgtype.Int8{Int64: 1, Valid: true}, RevokedBy: "x", ID: "g-x",
			})

			return err
		}},
		{name: "UpdatePrincipal", args: "('x', '1', 'p-x', 'acme')", pkey: "principals_pkey", send: func() error {
			_, err := q.UpdatePrincipal(ctx, db.UpdatePrincipalParams{
				DisplayName: "x", UpdatedAtMs: 1, ID: "p-x", Namespace: "acme",
			})

			return err
		}},
		{
			name: "DeletePrincipal",
			args: "('1', 'p-x', 'acme')",
			pkey: "principals_pkey",
			send: func() error {
				_, err := q.DeletePrincipal(ctx, db.DeletePrincipalParams{
					DeletedAtMs: pgtype.Int8{Int64: 1, Valid: true}, ID: "p-x", Namespace: "acme",
				})

				return err
			},
		},
		{
			name: "CreateFirstHostToken",
			args: "('t-x', 'x', 'x', 'x', '1', '2', 'x', 'p-x', '1')",
			pkey: "principals_pkey",
			send: func() error {
				_, err := q.CreateFirstHostToken(ctx, db.CreateFirstHostTokenParams{
					ID: "t-x", Name: "x", Kind: "x", Digest: "x", CreatedAtMs: 1, ExpiresAtMs: 2, CreatedBy: "x",
					PrincipalID: "p-x", WindowMs: 1,
				})

				return err
			},
		},
		{name: "RolesByIDs", args: "('{r-x}')", pkey: "roles_pkey", send: func() error {
			_, err := q.RolesByIDs(ctx, []string{"r-x"})

			return err
		}},
		{name: "GetLiveProfileByID", args: "('n-x', 'f-x')", pkey: "profiles_pkey", send: func() error {
			_, err := q.GetLiveProfileByID(ctx, db.GetLiveProfileByIDParams{NamespaceID: "n-x", ID: "f-x"})

			return err
		}},
		{name: "MoveProfileLatest", args: "('2', '1', 'n-x', 'f-x', '1')", pkey: "profiles_pkey", send: func() error {
			_, err := q.MoveProfileLatest(ctx, db.MoveProfileLatestParams{
				Next: 2, UpdatedAtMs: 1, NamespaceID: "n-x", ID: "f-x", Base: 1,
			})

			return err
		}},
		{name: "MarkProfileDeleted", args: "('1', 'n-x', 'f-x', '0')", pkey: "profiles_pkey", send: func() error {
			_, err := q.MarkProfileDeleted(ctx, db.MarkProfileDeletedParams{
				DeletedAtMs: 1, NamespaceID: "n-x", ID: "f-x", IfLatest: 0,
			})

			return err
		}},
		{name: "GetAccessTokenByDigest", args: "('d-x')", pkey: "principals_pkey", send: func() error {
			_, err := q.GetAccessTokenByDigest(ctx, "d-x")

			return err
		}},
		{name: "GetSessionByDigest", args: "('d-x')", pkey: "principals_pkey", send: func() error {
			_, err := q.GetSessionByDigest(ctx, "d-x")

			return err
		}},
	}
}

// usesLiveIndex reports whether plan names a partial index of the live rows of
// the access and profile tables. A lookup of one row by its ID must not use
// them.
func usesLiveIndex(plan string) bool {
	for _, name := range []string{
		"principals_subject", "principals_namespace", "roles_name_key", "principal_groups_name_key",
		"grants_current", "grants_holder", "grants_role", "grants_target", "profiles_live_name",
	} {
		if strings.Contains(plan, " "+name+" ") {
			return true
		}
	}

	return false
}
