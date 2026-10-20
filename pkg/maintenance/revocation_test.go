package maintenance_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/maintenance"
)

// revocationRow returns a valid journal row of kind with the target.
func revocationRow(kind, target string) maintenance.Revocation {
	return maintenance.Revocation{
		ID: 7, Kind: kind, Target: target, EndReason: "", Actor: "alice", Object: "token:x",
		At: time.Date(2026, time.October, 15, 9, 0, 0, 0, time.UTC),
	}
}

// TestRevocationValidateKinds proves that every kind the stores write passes
// and that an unknown kind is refused.
func TestRevocationValidateKinds(t *testing.T) {
	t.Parallel()

	known := []string{
		maintenance.RevokedSession, maintenance.RevokedToken, maintenance.RevokedPrincipal,
		maintenance.RevokedTempAdmin, maintenance.RevokedGrant, maintenance.RevokedJoinToken,
		maintenance.RevokedProvider,
	}

	for _, kind := range known {
		if err := revocationRow(kind, "tok-1").Validate(); err != nil {
			t.Errorf("kind %q: %v, want valid", kind, err)
		}
	}

	for _, kind := range []string{"", "wipe", "Session", "token ", "grant;"} {
		if err := revocationRow(kind, "tok-1").Validate(); !errors.Is(err, maintenance.ErrInvalidRevocation) {
			t.Errorf("kind %q: err = %v, want ErrInvalidRevocation", kind, err)
		}
	}
}

// TestRevocationValidateTargets proves that a target must be an ID: hostile
// text that a replay must never match is refused.
func TestRevocationValidateTargets(t *testing.T) {
	t.Parallel()

	for _, target := range []string{"tok-1", "ses_2", "prn-person", "corp.idp", "A1"} {
		if err := revocationRow(maintenance.RevokedToken, target).Validate(); err != nil {
			t.Errorf("target %q: %v, want valid", target, err)
		}
	}

	hostile := []string{
		"", " ", "tok-1%", "' OR '1'='1", "tok-1\n", "../prn-1", "tok-1;DROP", "tok\x00", "ünï", "a b",
		strings.Repeat("a", 256),
	}

	for _, target := range hostile {
		err := revocationRow(maintenance.RevokedToken, target).Validate()
		if !errors.Is(err, maintenance.ErrInvalidRevocation) {
			t.Errorf("target %q: err = %v, want ErrInvalidRevocation", target, err)
		}
	}
}

// TestRevocationReplayEntry proves the entry of an applied row and the entry
// of a refused row.
func TestRevocationReplayEntry(t *testing.T) {
	t.Parallel()

	row := revocationRow(maintenance.RevokedGrant, "grt-1")

	applied := row.ReplayEntry(nil)
	if err := applied.Validate(); err != nil {
		t.Fatalf("entry of an applied row: %v", err)
	}

	if applied.Namespace != audit.ServerScope || applied.Subject != "alice" || applied.Object != "token:x" ||
		applied.Outcome != audit.OutcomeSuccess || applied.Action != maintenance.ActionReplayRevocation ||
		!applied.At.Equal(row.At) {
		t.Fatalf("entry of an applied row = %+v", applied)
	}

	hostile := maintenance.Revocation{
		ID: 9, Kind: "wipe", Target: "x\ny", EndReason: "", Actor: "mallory\n", Object: "\x00", At: row.At,
	}

	denied := hostile.ReplayEntry(hostile.Validate())
	if err := denied.Validate(); err != nil {
		t.Fatalf("entry of a refused row: %v", err)
	}

	if denied.Outcome != audit.OutcomeDenied || denied.Object != "maintenance-revocation:9" ||
		strings.Contains(denied.Subject+denied.Object+denied.ValuePath, "mallory") {
		t.Fatalf("entry of a refused row = %+v, want the outcome denied and no text of the row", denied)
	}
}
