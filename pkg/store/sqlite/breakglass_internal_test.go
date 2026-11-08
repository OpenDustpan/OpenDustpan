package sqlite

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"opendustpan.org/opendustpan/internal/calendar"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/breakglass"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// seedBreakGlassReview makes the namespace acme with one pending break-glass
// review in it.
func seedBreakGlassReview(t *testing.T, s *Store) {
	t.Helper()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
		Name:                "acme",
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	at := time.UnixMilli(1_700_000_000_000).UTC()
	r := breakglass.Review{
		ID: "bgr-1",
		Use: breakglass.Use{
			Kind: breakglass.KindFreezeBreak, Ref: "job-1", Scope: ns.Name, Object: "opentofu:acme/network",
			Subject: "alice", People: []string{"alice", "carol"}, Reason: "Roll back the failed apply",
			At: at, Until: time.Time{},
		},
		State:        breakglass.StatePending,
		CreatedAt:    at,
		DueAt:        at.Add(31 * time.Hour),
		Calendar:     calendar.DefaultID,
		CalendarFrom: calendar.Scope{Namespace: ns.Name, Group: ""},
		Step:         0,
		NextNoticeAt: at.Add(time.Hour),
		Verdict: breakglass.Verdict{
			Reviewer: "", Note: "", Auth: "", AuthAt: time.Time{}, ReviewedAt: time.Time{}, ActAs: nil,
		},
		Version: 0,
	}

	if _, err := s.BreakGlass().Open(t.Context(), r, ns.Fence(), breakGlassAuditEntry(ns.Name)); err != nil {
		t.Fatalf("open review: %v", err)
	}
}

// breakGlassAuditEntry returns a valid audit entry of the kind change in scope.
func breakGlassAuditEntry(scope namespace.Name) audit.Entry {
	return audit.Entry{
		At:            time.UnixMilli(1_700_000_000_000).UTC(),
		Namespace:     scope,
		Subject:       "server",
		IDPSubject:    "",
		TokenID:       "",
		Action:        "namespace:review-break-glass",
		Object:        scope.String(),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     "test",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "req-break-glass",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// TestCheckStagedBreakGlassReviews proves the restore check: it accepts the
// row that the store wrote, and it refuses each row that the store would not
// have stored.
func TestCheckStagedBreakGlassReviews(t *testing.T) {
	t.Parallel()

	t.Run("a row that the store wrote passes", func(t *testing.T) {
		t.Parallel()

		s := openInternalTemplated(t)
		seedBreakGlassReview(t, s)

		if err := checkStagedBreakGlassReviews(t.Context(), s.conn); err != nil {
			t.Fatalf("check = %v, want nil", err)
		}
	})

	for _, tc := range []struct{ name, update string }{
		{"ID that is not a review ID", `UPDATE break_glass_reviews SET id = 'review-1'`},
		{"ID with the prefix only", `UPDATE break_glass_reviews SET id = 'bgr-'`},
		{"empty reference", `UPDATE break_glass_reviews SET use_ref = ''`},
		{"empty subject", `UPDATE break_glass_reviews SET use_subject = ''`},
		{"empty reason", `UPDATE break_glass_reviews SET use_reason = ''`},
		{"use with no time", `UPDATE break_glass_reviews SET use_at_ms = 0`},
		{"server scope on a freeze break", `UPDATE break_glass_reviews SET use_scope = '@server'`},
		{"people that are not JSON", `UPDATE break_glass_reviews SET use_people = 'not json'`},
		{"people that are not a list", `UPDATE break_glass_reviews SET use_people = '{}'`},
		{"people that do not hold the subject", `UPDATE break_glass_reviews SET use_people = '["carol"]'`},
		{"people with an empty name", `UPDATE break_glass_reviews SET use_people = '["alice",""]'`},
		{"pending review with a verdict", `UPDATE break_glass_reviews SET verdict_reviewer = 'bob'`},
		{"accepted review with no verdict", `UPDATE break_glass_reviews SET state = 'accepted'`},
		{"act-as list that is not JSON", `UPDATE break_glass_reviews SET verdict_act_as = 'oops'`},
		{"version 0", `UPDATE break_glass_reviews SET version = 0`},
		{
			"calendar scope of a namespace and a group",
			`UPDATE break_glass_reviews SET calendar_from_namespace = 'acme', calendar_from_group = 'g'`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := openInternalTemplated(t)
			seedBreakGlassReview(t, s)

			if _, err := s.conn.ExecContext(t.Context(), tc.update); err != nil {
				t.Fatalf("change the row: %v", err)
			}

			err := checkStagedBreakGlassReviews(t.Context(), s.conn)
			if !errors.Is(err, backup.ErrCorrupt) {
				t.Fatalf("check = %v, want ErrCorrupt", err)
			}
		})
	}
}

// TestCheckStagedBreakGlassReviewsRefusesAMissingNamespace proves that a
// review of a namespace that the backup does not hold is corrupt. An inner
// join would skip such a row, and the restore would bring it in.
func TestCheckStagedBreakGlassReviewsRefusesAMissingNamespace(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)
	seedBreakGlassReview(t, s)

	const update = `UPDATE break_glass_reviews SET use_scope = 'gone'`

	if _, err := s.conn.ExecContext(t.Context(), update); err != nil {
		t.Fatalf("change the row: %v", err)
	}

	err := checkStagedBreakGlassReviews(t.Context(), s.conn)
	if !errors.Is(err, backup.ErrCorrupt) {
		t.Fatalf("check = %v, want ErrCorrupt", err)
	}

	if !strings.Contains(fmt.Sprint(err), "has no namespace") {
		t.Fatalf("check = %v, want the refusal of a review with no namespace", err)
	}
}

// TestBreakGlassReviewUseIsUnique proves the unique index on the use of a
// review: a second row with the same kind and reference fails with a unique
// violation, and another use inserts.
func TestBreakGlassReviewUseIsUnique(t *testing.T) {
	t.Parallel()

	const insert = `INSERT INTO break_glass_reviews (
id, use_kind, use_ref, use_scope, use_object, use_subject, use_people, use_reason,
use_at_ms, use_until_ms, state, created_at_ms, due_at_ms, calendar, calendar_from_namespace,
calendar_from_group, step, next_notice_at_ms, verdict_reviewer, verdict_note, verdict_auth,
verdict_auth_at_ms, verdict_reviewed_at_ms, verdict_act_as, version)
VALUES ('bgr-2', ?, ?, ?, ?, 'alice', '["alice"]', 'Roll back the failed apply',
	1, 0, 'pending', 1, 1, 'cal-default', 'acme', '', 0, 0, '', '', '', 0, 0, '[]', 1)`

	for _, tc := range []struct {
		name          string
		kind          string
		ref           string
		scope         string
		object        string
		wantViolation bool
	}{
		{"the same kind and reference", "freeze-break", "job-1", "acme", "opentofu:acme/network", true},
		{"another reference", "freeze-break", "job-2", "acme", "opentofu:acme/network", false},
		{"another kind", "account-session", "job-1", "@server", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := openInternalTemplated(t)
			seedBreakGlassReview(t, s)

			_, err := s.conn.ExecContext(t.Context(), insert, tc.kind, tc.ref, tc.scope, tc.object)

			if tc.wantViolation {
				sqliteErr, ok := errors.AsType[*sqlite.Error](err)
				if !ok || sqliteErr.Code() != sqlite3.SQLITE_CONSTRAINT_UNIQUE {
					t.Fatalf("insert = %v, want a unique violation", err)
				}
			} else if err != nil {
				t.Fatalf("insert = %v, want nil", err)
			}
		})
	}
}
