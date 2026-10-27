package storetest

import (
	"errors"
	"testing"
	"time"

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
	if !errors.Is(err, principal.ErrLastBreakGlass) {
		t.Fatalf("disable of the last break-glass account in maintenance mode: err = %v, want ErrLastBreakGlass", err)
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
