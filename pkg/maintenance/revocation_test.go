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

// TestRevocationValidateActorAndObject proves that the actor and the object
// of a row must be printable text of bounded length: a hostile row is refused
// and a reference with ":" and "/" passes.
func TestRevocationValidateActorAndObject(t *testing.T) {
	t.Parallel()

	for _, text := range []string{"alice", "alice@example.com", "token:tok-1", "acme/pulumi/proj/stack", "ünï"} {
		row := revocationRow(maintenance.RevokedToken, "tok-1")
		row.Actor, row.Object = text, text

		if err := row.Validate(); err != nil {
			t.Errorf("text %q: %v, want valid", text, err)
		}
	}

	hostile := []string{"", "a\nb", "a\rb", "\x00", "a\x1b[31m", "\xff\xfe", "a\u202eb", strings.Repeat("a", 256)}

	for _, text := range hostile {
		actor := revocationRow(maintenance.RevokedToken, "tok-1")
		actor.Actor = text

		if err := actor.Validate(); !errors.Is(err, maintenance.ErrInvalidRevocation) {
			t.Errorf("actor %q: err = %v, want ErrInvalidRevocation", text, err)
		}

		object := revocationRow(maintenance.RevokedToken, "tok-1")
		object.Object = text

		if err := object.Validate(); !errors.Is(err, maintenance.ErrInvalidRevocation) {
			t.Errorf("object %q: err = %v, want ErrInvalidRevocation", text, err)
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

// TestRevocationReplayEntryHostileActor proves that a row with a valid kind
// and target but a hostile actor or object gets a denied entry that holds no
// text of the row, so that one row can not block a restore.
func TestRevocationReplayEntryHostileActor(t *testing.T) {
	t.Parallel()

	for _, hostile := range []string{"mallory\nforged", "mallory\x00"} {
		actor := revocationRow(maintenance.RevokedToken, "tok-1")
		actor.Actor = hostile

		object := revocationRow(maintenance.RevokedToken, "tok-1")
		object.Object = hostile

		checkDeniedEntry(t, actor)
		checkDeniedEntry(t, object)
	}
}

// checkDeniedEntry fails the test unless row is refused and its replay entry
// is valid, denied and free of the text of the row.
func checkDeniedEntry(t *testing.T, row maintenance.Revocation) {
	t.Helper()

	err := row.Validate()
	if !errors.Is(err, maintenance.ErrInvalidRevocation) {
		t.Fatalf("row %+v: err = %v, want ErrInvalidRevocation", row, err)
	}

	denied := row.ReplayEntry(err)
	if verr := denied.Validate(); verr != nil {
		t.Fatalf("row %+v: entry of a refused row: %v", row, verr)
	}

	text := denied.Subject + denied.Object + denied.ValuePath
	if denied.Outcome != audit.OutcomeDenied || strings.Contains(text, "mallory") {
		t.Fatalf("row %+v: entry = %+v, want the outcome denied and no text of the row", row, denied)
	}
}

// errRefused is what the injected validators answer.
var errRefused = errors.New("refused")

// refuseSuccess refuses the entry of an applied row and accepts a denied entry.
func refuseSuccess(e audit.Entry) error {
	if e.Outcome == audit.OutcomeSuccess {
		return errRefused
	}

	return nil
}

// refuseAll refuses every entry.
func refuseAll(audit.Entry) error { return errRefused }

// TestCheckedReplayEntry proves the audit layer: an entry that validate
// refuses is replaced by the fixed denied entry, the row without a time
// gets one, and a validate that refuses both entries gives an error.
func TestCheckedReplayEntry(t *testing.T) {
	t.Parallel()

	row := revocationRow(maintenance.RevokedGrant, "grt-1")
	noTime := row
	noTime.At = time.Time{}

	tests := []struct {
		name     string
		row      maintenance.Revocation
		validate func(audit.Entry) error
		want     audit.Outcome
		wantErr  bool
	}{
		{"accepted", row, audit.Entry.Validate, audit.OutcomeSuccess, false},
		{"row without a time", noTime, audit.Entry.Validate, audit.OutcomeDenied, false},
		{"injected refusal of the first entry", row, refuseSuccess, audit.OutcomeDenied, false},
		{"refusal of both entries", row, refuseAll, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e, err := tt.row.CheckedReplayEntry(nil, tt.validate)
			if tt.wantErr {
				if !errors.Is(err, errRefused) {
					t.Fatalf("err = %v, want the refusal", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("err = %v, want none", err)
			}

			checkReplayEntry(t, e, tt.want)
		})
	}
}

// checkReplayEntry fails unless e is valid, has the outcome want, and names
// the component and not the row when it is denied.
func checkReplayEntry(t *testing.T, e audit.Entry, want audit.Outcome) {
	t.Helper()

	if e.Outcome != want || e.Validate() != nil {
		t.Fatalf("entry = %+v, want a valid entry with the outcome %q", e, want)
	}

	if want == audit.OutcomeDenied && e.Subject != "maintenance" {
		t.Fatalf("denied entry names the row: %+v", e)
	}
}

// TestRevocationValidateRefusesARowWithoutATime proves that a row without a
// time after the epoch is refused: its target would end at the time 0, which
// reads as not ended.
func TestRevocationValidateRefusesARowWithoutATime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		at   time.Time
	}{
		{"zero time", time.Time{}},
		{"epoch", time.UnixMilli(0)},
		{"before the epoch", time.UnixMilli(-1)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			row := revocationRow(maintenance.RevokedToken, "tok-1")
			row.At = tc.at

			if err := row.Validate(); !errors.Is(err, maintenance.ErrInvalidRevocation) {
				t.Fatalf("err = %v, want ErrInvalidRevocation", err)
			}
		})
	}
}
