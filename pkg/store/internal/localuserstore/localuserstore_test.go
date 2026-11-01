package localuserstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/internal/localuserstore"
)

// errBroken is the error of a fake read that fails.
var errBroken = errors.New("broken read")

// fakeTx holds one person, one local user and one reset code, and records
// each write. A case sets the fields that it needs.
type fakeTx struct {
	principal localuserstore.Principal
	user      localuserstore.User
	reset     localuserstore.Reset
	kind      string
	kindErr   error
	writes    []string
}

func newFake() *fakeTx {
	return &fakeTx{
		principal: localuserstore.Principal{
			Kind:       string(principal.KindPerson),
			Subject:    "local:lus-1",
			BreakGlass: false,
		},
		user: localuserstore.User{
			ID: "lus-1", PrincipalID: "prn-1", UserName: "alice", PasswordHash: "", CreatedBy: "admin",
			PasswordSetAtMs: 0, CreatedAtMs: 1, SessionGeneration: 0, ChangeRequired: false, Demo: false,
		},
		reset: localuserstore.Reset{
			ID: "lpr-1", UserID: "lus-1", PrincipalID: "prn-1", Digest: localuser.DigestOf("code").String(),
			Purpose: string(localuser.PurposeSetUp), CreatedBy: "admin", Reason: "", CreatedAtMs: 1,
			ExpiresAtMs: at().Add(time.Hour).UnixMilli(), RevokeAccessTokens: false,
		},
		kind:    "dev",
		kindErr: nil,
		writes:  nil,
	}
}

func at() time.Time {
	return time.UnixMilli(1_700_000_000_000).UTC()
}

func (f *fakeTx) LockPrincipal(context.Context, string) (localuserstore.Principal, error) {
	return f.principal, nil
}

func (f *fakeTx) InstallationKind(context.Context) (string, error) { return f.kind, f.kindErr }

func (f *fakeTx) PrincipalBySubject(context.Context, string) (string, error) { return "prn-1", nil }

func (f *fakeTx) CreateUser(context.Context, localuserstore.User) (int64, error) {
	return f.write("create user")
}

func (f *fakeTx) CreateDemoUser(context.Context, localuserstore.User) (int64, error) {
	return f.write("create demo user")
}

func (f *fakeTx) CountUserConflicts(context.Context, string, string, string) (int64, error) {
	return 0, nil
}

func (f *fakeTx) UserByID(context.Context, string) (localuserstore.User, error) { return f.user, nil }

func (f *fakeTx) UserByName(context.Context, string) (localuserstore.User, error) {
	f.writes = append(f.writes, "read by name")

	return f.user, nil
}

func (f *fakeTx) UserByPrincipal(context.Context, string) (localuserstore.User, error) {
	return f.user, nil
}

func (f *fakeTx) CountOtherUserName(context.Context, string, string) (int64, error) { return 0, nil }

func (f *fakeTx) RenameUser(context.Context, string, string) (int64, error) { return f.write("rename") }

func (f *fakeTx) DeleteUser(context.Context, string) (int64, error) { return f.write("delete user") }

func (f *fakeTx) DeleteResetsOfUser(context.Context, string) error {
	_, err := f.write("delete resets")

	return err
}

func (f *fakeTx) SetPassword(context.Context, string, localuserstore.Write) (int64, error) {
	return f.write("set password")
}

func (f *fakeTx) SetChangeRequired(context.Context, string, bool) (int64, error) {
	return f.write("change required")
}

func (f *fakeTx) RehashPassword(context.Context, string, string, string) (int64, error) {
	return f.write("rehash")
}

func (f *fakeTx) BumpSessionGeneration(context.Context, string) (int64, error) {
	return f.write("bump")
}

func (f *fakeTx) RevokeAccessTokens(context.Context, string, int64) (int64, error) {
	return f.write("revoke")
}

func (f *fakeTx) CreateReset(context.Context, localuserstore.Reset) (int64, error) {
	return f.write("create reset")
}

func (f *fakeTx) ResetByDigest(context.Context, string) (localuserstore.Reset, error) {
	return f.reset, nil
}

func (f *fakeTx) DeleteReset(context.Context, string) (int64, error) { return f.write("delete reset") }

func (f *fakeTx) write(name string) (int64, error) {
	f.writes = append(f.writes, name)

	return 1, nil
}

// validHash is a hash that CheckHash accepts.
const validHash localuser.PasswordHash = "$argon2id$v=19$m=19456,t=2,p=1$AAAAAAAAAAAAAAAAAAAAAA$" +
	"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

// use returns a use of the code of the fake with the hash h.
func use(h localuser.PasswordHash) localuser.Redeem {
	return localuser.Redeem{At: at(), Hash: h, Change: noChange(), Digest: localuser.DigestOf("code")}
}

func noChange() localuser.PasswordChange {
	return localuser.PasswordChange{ChangeRequired: false}
}

func wantNoWrites(t *testing.T, f *fakeTx) {
	t.Helper()

	if len(f.writes) != 0 {
		t.Fatalf("writes = %v, want none", f.writes)
	}
}

// TestRedeemRefusesBreakGlass is the store layer of the break-glass rule for
// a code that no port could make: a redeem for a break-glass person writes
// nothing, not even the delete of the code.
func TestRedeemRefusesBreakGlass(t *testing.T) {
	t.Parallel()

	for _, purpose := range []localuser.Purpose{localuser.PurposeSetUp, localuser.PurposeReset} {
		f := newFake()
		f.principal.BreakGlass = true
		f.reset.Purpose = string(purpose)

		_, err := localuserstore.RedeemReset(t.Context(), f, use(validHash))
		if !errors.Is(err, localuser.ErrBreakGlass) {
			t.Fatalf("redeem a %s code of a break-glass person: err = %v, want ErrBreakGlass", purpose, err)
		}

		wantNoWrites(t, f)
	}
}

// TestPasswordWritesRefuseBreakGlass checks each write that stores a hash.
func TestPasswordWritesRefuseBreakGlass(t *testing.T) {
	t.Parallel()

	writes := map[string]func(f *fakeTx) error{
		"set password": func(f *fakeTx) error {
			return localuserstore.SetPassword(context.Background(), f, "lus-1",
				localuserstore.PasswordWrite(validHash, noChange(), at()))
		},
		"rehash": func(f *fakeTx) error {
			return localuserstore.Rehash(context.Background(), f, "lus-1", validHash, validHash)
		},
		"seed": func(f *fakeTx) error {
			return localuserstore.SeedDemoPassword(context.Background(), f, validHash, at())
		},
	}

	for name, write := range writes {
		f := newFake()
		f.principal.BreakGlass = true

		if err := write(f); !errors.Is(err, localuser.ErrBreakGlass) {
			t.Errorf("%s for a break-glass person: err = %v, want ErrBreakGlass", name, err)
		}

		wantNoWrites(t, f)
	}
}

// TestSeedRefusesAnotherKind is the store layer of the demo rule: the seed
// reads the kind in its own transaction and refuses each kind but dev, and a
// kind that cannot be read, before any write.
func TestSeedRefusesAnotherKind(t *testing.T) {
	t.Parallel()

	cases := []struct {
		err  error
		name string
		kind string
	}{
		{name: "production", kind: "production", err: nil},
		{name: "no kind", kind: "", err: nil},
		{name: "an unknown kind", kind: "DEV", err: nil},
		{name: "a read error", kind: "dev", err: errBroken},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFake()
			f.kind, f.kindErr = tc.kind, tc.err

			err := localuserstore.SeedDemoPassword(t.Context(), f, validHash, at())
			if !errors.Is(err, localuser.ErrDemoOnly) {
				t.Fatalf("seed: err = %v, want ErrDemoOnly", err)
			}

			wantNoWrites(t, f)
		})
	}
}

// TestSeedRefusesAnotherUser checks that the seed never sets the password of
// a local user that is not the demo record, even when it belongs to the
// demo person.
func TestSeedRefusesAnotherUser(t *testing.T) {
	t.Parallel()

	f := newFake()
	f.user.Demo = false

	err := localuserstore.SeedDemoPassword(t.Context(), f, validHash, at())
	if !errors.Is(err, localuser.ErrInvalid) {
		t.Fatalf("seed over a user that is not the demo user: err = %v, want ErrInvalid", err)
	}

	wantNoWrites(t, f)
}

// TestHashCheckedBeforeAnyWrite is the boundary layer of the store: a hash
// that the hasher does not read never reaches a write.
func TestHashCheckedBeforeAnyWrite(t *testing.T) {
	t.Parallel()

	for _, h := range []localuser.PasswordHash{"", "plain", validHash + "\n", "$argon2id$v=19$m=7168,t=1,p=0$AA$AA"} {
		f := newFake()

		err := localuserstore.SetPassword(t.Context(), f, "lus-1", localuserstore.PasswordWrite(h, noChange(), at()))
		if !errors.Is(err, localuser.ErrInvalid) {
			t.Errorf("set the hash %q: err = %v, want ErrInvalid", h, err)
		}

		_, err = localuserstore.RedeemReset(t.Context(), f, use(h))
		if !errors.Is(err, localuser.ErrInvalid) {
			t.Errorf("redeem with the hash %q: err = %v, want ErrInvalid", h, err)
		}

		wantNoWrites(t, f)
	}
}

// TestCreateRefusesDemo checks that only the seed makes the demo record.
func TestCreateRefusesDemo(t *testing.T) {
	t.Parallel()

	f := newFake()
	u := localuserstore.UserOf(f.user)
	u.Demo = true

	if err := localuserstore.CreateUser(t.Context(), f, u); !errors.Is(err, localuser.ErrInvalid) {
		t.Fatalf("create a demo user: err = %v, want ErrInvalid", err)
	}

	wantNoWrites(t, f)
}

// TestByNameRefusesBadName checks that a name that no row can hold sends no
// query.
func TestByNameRefusesBadName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", "Alice", "alice'; DROP TABLE local_users; --", "a\x00b"} {
		f := newFake()

		if _, err := localuserstore.ByName(t.Context(), f, name); !errors.Is(err, localuser.ErrNotFound) {
			t.Errorf("by name %q: err = %v, want ErrNotFound", name, err)
		}

		wantNoWrites(t, f)
	}
}

// TestRedeemExpired checks the error of an expired code: ErrNotFound, marked
// so that the adapter deletes the code, and no write.
func TestRedeemExpired(t *testing.T) {
	t.Parallel()

	f := newFake()
	f.reset.ExpiresAtMs = at().UnixMilli()

	_, err := localuserstore.RedeemReset(t.Context(), f, use(validHash))
	if !errors.Is(err, localuser.ErrNotFound) || !localuserstore.Expired(err) {
		t.Fatalf("redeem an expired code: err = %v, want ErrNotFound marked as expired", err)
	}

	wantNoWrites(t, f)
}

// TestResetChanges checks the changes that a reset adds to its entry.
func TestResetChanges(t *testing.T) {
	t.Parallel()

	f := newFake()
	var e audit.Entry

	r := localuserstore.ResetOf(f.reset)
	r.Purpose = localuser.PurposeReset
	r.RevokeAccessTokens = true

	if err := localuserstore.CreateReset(t.Context(), f, r, localuser.DigestOf("code"), &e); err != nil {
		t.Fatalf("create reset: %v", err)
	}

	got := map[string]string{}
	for _, c := range e.Changes {
		got[c.Key] = c.New
	}

	want := map[string]string{"purpose": "reset", "revokeAccessTokens": "true", "revokedAccessTokens": "1"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("change %s = %q, want %q", k, got[k], v)
		}
	}
}

// TestDeleteRefusesLastCredential checks that a delete of a user with a
// password fails closed and writes nothing, and that a delete of a user with
// no password bumps the session generation.
func TestDeleteRefusesLastCredential(t *testing.T) {
	t.Parallel()

	f := newFake()
	f.user.PasswordHash = string(validHash)

	if err := localuserstore.Delete(t.Context(), f, "lus-1"); !errors.Is(err, localuser.ErrLastCredential) {
		t.Fatalf("delete a user with a password: err = %v, want ErrLastCredential", err)
	}

	wantNoWrites(t, f)

	f = newFake()

	if err := localuserstore.Delete(t.Context(), f, "lus-1"); err != nil {
		t.Fatalf("delete a user with no password: %v", err)
	}

	want := []string{"delete resets", "delete user", "bump"}
	if len(f.writes) != len(want) {
		t.Fatalf("writes = %v, want %v", f.writes, want)
	}

	for i := range want {
		if f.writes[i] != want[i] {
			t.Fatalf("writes = %v, want %v", f.writes, want)
		}
	}
}

// TestByNameRefusesGenerationBelowOne is the fail-closed layer of the
// generation that a sign-in seals: a value that no write can make fails.
func TestByNameRefusesGenerationBelowOne(t *testing.T) {
	t.Parallel()

	for _, g := range []int64{0, -1} {
		f := newFake()
		f.user.SessionGeneration = g

		if _, err := localuserstore.ByName(t.Context(), f, "alice"); !errors.Is(err, localuser.ErrUnavailable) {
			t.Errorf("by name with the generation %d: err = %v, want ErrUnavailable", g, err)
		}
	}

	f := newFake()
	f.user.SessionGeneration = 7

	c, err := localuserstore.ByName(t.Context(), f, "alice")
	if err != nil || c.SessionGeneration != 7 {
		t.Fatalf("by name = %+v, %v; want the generation 7 of the read", c, err)
	}
}
