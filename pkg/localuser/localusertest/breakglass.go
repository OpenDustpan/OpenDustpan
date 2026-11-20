package localusertest

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/localuser"
)

// BreakGlassMarker is an optional capability of a store for the cases of a
// break-glass person who holds a set-up code or a reset code. No port sets
// the break-glass flag on a person that exists, but a restore writes the rows
// of a backup, and a backup of another version or a changed backup can hold
// such a person. A store that implements it sets the flag of the principal
// with a write outside the ports, as such a restored row would. A store that
// does not implement it skips those cases.
type BreakGlassMarker interface {
	// MarkBreakGlassForTest sets the break-glass flag of the principal
	// principalID, and fails the test when it cannot.
	MarkBreakGlassForTest(t *testing.T, principalID string)
}

// testUpdateKeepsBreakGlassFlag proves the premise of the break-glass
// password rule in the ports: an update of a person with a password does not
// set the break-glass flag, and keeps the password. So only a restore can
// put the flag on a person with a password, and the restore refuses it with
// principal.ErrBreakGlassPassword.
func testUpdateKeepsBreakGlassFlag(t *testing.T, s Store) {
	t.Helper()

	u := mustUser(t, s, 1)
	mustPassword(t, s, u, 1)

	p, err := s.GetPrincipal(t.Context(), u.PrincipalID)
	if err != nil {
		t.Fatalf("get the person: %v", err)
	}

	p.BreakGlass = true
	p.DisplayName = "renamed"
	p.UpdatedAt = at(5)

	if updateErr := s.UpdatePrincipal(t.Context(), p, entry("principal:"+p.ID, at(5))); updateErr != nil {
		t.Fatalf("update the person: %v", updateErr)
	}

	got, err := s.GetPrincipal(t.Context(), u.PrincipalID)
	if err != nil {
		t.Fatalf("get the person after the update: %v", err)
	}

	if got.BreakGlass || got.DisplayName != "renamed" {
		t.Fatalf("person after the update = %+v, want the new name and no break-glass flag", got)
	}

	if h := storedHash(t, s, "user1"); h != hash(1) {
		t.Fatalf("hash after the update = %q, want hash 1", h)
	}
}

// testRedeemBreakGlass returns the case of a code of purpose that a person
// got before a restore made the person a break-glass account: the redeem
// fails with ErrBreakGlass, stores no hash, keeps the session generation and
// appends no entry.
func testRedeemBreakGlass(purpose localuser.Purpose) func(t *testing.T, s Store) {
	return func(t *testing.T, s Store) {
		t.Helper()

		marker, ok := s.(BreakGlassMarker)
		if !ok {
			t.Skip("the store cannot set the break-glass flag outside the ports")
		}

		u := mustUser(t, s, 1)
		d := mustReset(t, s, newReset("lpr-1", u.ID, purpose, at(3)))

		marker.MarkBreakGlassForTest(t, u.PrincipalID)

		p, err := s.GetPrincipal(t.Context(), u.PrincipalID)
		if err != nil || !p.BreakGlass {
			t.Fatalf("person after the mark = %+v, %v; want a break-glass account", p, err)
		}

		before := generation(t, s, u.PrincipalID)

		_, err = redeem(t, s, d, at(4), hash(5))
		wantErr(t, "redeem a "+string(purpose)+" code of a break-glass person", err, localuser.ErrBreakGlass)

		if h := storedHash(t, s, "user1"); h != "" {
			t.Fatalf("hash after a refused redeem = %q, want none", h)
		}

		if g := generation(t, s, u.PrincipalID); g != before {
			t.Fatalf("session generation after a refused redeem = %d, want %d", g, before)
		}

		wantEntries(t, s, redeemObject, 0)
	}
}
