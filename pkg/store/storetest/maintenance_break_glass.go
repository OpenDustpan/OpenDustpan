package storetest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/principal"
)

// revBreakGlass stores an enabled break-glass person with the ID id.
func revBreakGlass(t *testing.T, s RevocationStore, id string) {
	t.Helper()

	p := principal.Principal{
		CreatedAt: revAt(0), UpdatedAt: revAt(0), DisabledAt: time.Time{},
		ID: id, Subject: "subject-" + id, DisplayName: "Name of " + id, Kind: principal.KindPerson,
		Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: true,
	}

	if err := s.CreatePrincipal(t.Context(), p, revEntry(principal.Ref(id), revAt(0))); err != nil {
		t.Fatalf("create break-glass principal %q: %v", id, err)
	}
}

// testBreakGlassInMaintenance proves that the disable of one of two
// break-glass accounts works in maintenance mode, that the disable of the last
// one is refused there and appends no entry, and that the enable of a disabled
// one is refused there: an enable is not on the control path.
func testBreakGlassInMaintenance(t *testing.T, s RevocationStore) {
	t.Helper()

	ctx := t.Context()

	revBreakGlass(t, s, "prn-bg1")
	revBreakGlass(t, s, "prn-bg2")
	revEnter(t, s)

	if err := s.DisablePrincipal(ctx, "prn-bg1", revEntry(principal.Ref("prn-bg1"), revAt(10))); err != nil {
		t.Fatalf("disable of one of two break-glass accounts in maintenance mode: %v", err)
	}

	before := revAuditTotal(t, s)

	err := s.DisablePrincipal(ctx, "prn-bg2", revEntry(principal.Ref("prn-bg2"), revAt(11)))
	if !errors.Is(err, principal.ErrNoWorkingBreakGlass) {
		t.Fatalf(
			"disable of the last break-glass account in maintenance mode: err = %v, want ErrNoWorkingBreakGlass",
			err,
		)
	}

	if got := revAuditTotal(t, s); got != before {
		t.Fatalf("audit entries after the refused disable = %d, want %d", got, before)
	}

	err = s.EnablePrincipal(ctx, "prn-bg1", revEntry(principal.Ref("prn-bg1"), revAt(12)))
	if !errors.Is(err, maintenance.ErrInMaintenance) {
		t.Fatalf("enable of a break-glass account in maintenance mode: err = %v, want ErrInMaintenance", err)
	}

	got, err := s.GetPrincipal(ctx, "prn-bg2")
	if err != nil || !got.DisabledAt.IsZero() {
		t.Fatalf("last break-glass account after the refused disable = %+v, %v; want present and enabled", got, err)
	}
}

// testRestoreKeepsOneBreakGlass proves that the replay of a disable never
// leaves the installation with no enabled break-glass account. The backup
// holds the accounts A and B. Later the live data gets C, and in maintenance
// mode A and B are disabled, which the store allows while C is enabled. The
// restore replaces the data with the backup, which holds no C, and replays
// both disables. The replay skips the second one: B stays enabled, and the
// audit log holds one entry with the outcome denied and the reason
// principal.no-working-break-glass.
func testRestoreKeepsOneBreakGlass(t *testing.T, s RevocationStore) {
	t.Helper()

	ctx := t.Context()

	revBreakGlass(t, s, "prn-bg-a")
	revBreakGlass(t, s, "prn-bg-b")

	export := revTakeExport(t, s)

	revBreakGlass(t, s, "prn-bg-c")
	revEnter(t, s)

	for i, id := range []string{"prn-bg-a", "prn-bg-b"} {
		err := s.DisablePrincipal(ctx, id, revEntry(principal.Ref(id), revAt(10+i)))
		if err != nil {
			t.Fatalf("disable of %s in maintenance mode: %v", id, err)
		}
	}

	revStageAndApply(t, s, export)

	if countEnabledBreakGlass(t, s, "prn-bg-a", "prn-bg-b") != 1 {
		t.Fatal("enabled break-glass accounts after the restore, want exactly one")
	}

	entries := revReplayEntries(t, s)
	if len(entries) != 2 {
		t.Fatalf(
			"replay audit entries = %d, want 2: the disable that applied and the one that was skipped",
			len(entries),
		)
	}

	applied, kept := entries[0], entries[1]
	if applied.Outcome != audit.OutcomeSuccess || applied.Reason != "" {
		t.Fatalf("entry of the disable that applied = %+v, want success with no reason", applied)
	}

	if kept.Outcome != audit.OutcomeDenied || kept.Reason != maintenance.ReasonNoWorkingBreakGlass ||
		kept.Object != principal.Ref("prn-bg-b") {
		t.Fatalf("entry of the skipped disable = outcome %q, reason %q, object %q; want denied, %q, %q",
			kept.Outcome, kept.Reason, kept.Object, maintenance.ReasonNoWorkingBreakGlass, principal.Ref("prn-bg-b"))
	}
}

// countEnabledBreakGlass returns how many of the principals ids exist and are
// enabled.
func countEnabledBreakGlass(t *testing.T, s RevocationStore, ids ...string) int {
	t.Helper()

	n := 0

	for _, id := range ids {
		got, err := s.GetPrincipal(t.Context(), id)

		switch {
		case errors.Is(err, principal.ErrNotFound):
		case err != nil:
			t.Fatalf("get principal %q: %v", id, err)
		case got.DisabledAt.IsZero():
			n++
		default:
		}
	}

	return n
}
