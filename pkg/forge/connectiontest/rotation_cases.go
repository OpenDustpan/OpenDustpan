package connectiontest

import (
	"bytes"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
)

const actionConfirmRotation = "git-host-app:confirm-rotation"

// rotationCases returns the cases of the old event secret of a registration:
// the three old fields, the rule that they are all set or all absent, the
// atomic write of a rotation and the confirmation that ends the window.
func rotationCases() []suiteCase {
	return []suiteCase{
		{"put keeps the old event secret fields and get returns them", testRotationRoundTrip},
		{"put of a partial old event secret set is invalid and writes nothing", testRotationPartialSet},
		{"put without the old fields clears an open window", testRotationClearedByPut},
		{"a put with a stale version keeps both secrets", testRotationStaleVersionKeepsBoth},
		{"a put that fails on its audit entry keeps both secrets", testRotationBadEntryKeepsBoth},
		{"a put whose audit append fails keeps both secrets", testRotationFailedAppendKeepsBoth},
		{"two puts with one read version: one wins and one is a conflict", testRotationTwoPutsOneVersion},
		{"confirm clears the old fields, bumps the version and writes one entry", testConfirmClears},
		{"confirm with no open window is no rotation and writes nothing", testConfirmNoWindow},
		{"confirm at or after the end of the window is no rotation and writes nothing", testConfirmAfterEnd},
		{"confirm with another version is a conflict and keeps the window", testConfirmVersion},
		{"confirm of a missing registration is not found", testConfirmMissing},
		{"confirm with an entry of a namespace changes nothing", testConfirmEntryScope},
		{"confirm twice: the second finds no window", testConfirmTwice},
		{"a confirm whose audit append fails keeps the window", testConfirmFailedAppendKeepsWindow},
	}
}

// rotating returns r with an open window of 7 days from the test time.
func rotating(r forge.Registration) forge.Registration {
	r.OldEventSecretSetID = forge.NewSetID()
	r.OldSealedEventSecret = []byte("sealed-old-event-secret")
	r.OldEventSecretEndsAt = time.UnixMilli(testTimeMs).UTC().Add(7 * 24 * time.Hour)

	return r
}

// mustPutRotating stores r with an open window and fails when the answer does
// not hold the old fields, so a later "unchanged" check is never empty.
func mustPutRotating(t *testing.T, h Harness, r forge.Registration) forge.Registration {
	t.Helper()

	got := mustPut(t, h, r)
	if !sameOld(&got, &r) {
		t.Fatalf("put answer = %+v, want the old fields of %+v", got, r)
	}

	return got
}

func sameOld(a, b *forge.Registration) bool {
	return a.OldEventSecretSetID == b.OldEventSecretSetID &&
		bytes.Equal(a.OldSealedEventSecret, b.OldSealedEventSecret) &&
		a.OldEventSecretEndsAt.Equal(b.OldEventSecretEndsAt)
}

func noOld(r *forge.Registration) bool {
	return r.OldEventSecretSetID == "" && len(r.OldSealedEventSecret) == 0 && r.OldEventSecretEndsAt.IsZero()
}

func testRotationRoundTrip(t *testing.T, h Harness) {
	t.Helper()

	want := rotating(newRegistration(secretKeyA))
	mustPut(t, h, want)

	read, err := h.Store.GetRegistration(t.Context(), forge.KindGitHub)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if !sameOld(&read, &want) || !sameRegistration(&read, &want) {
		t.Fatalf("get = %+v, want %+v", read, want)
	}
}

func testRotationPartialSet(t *testing.T, h Harness) {
	t.Helper()

	full := rotating(newRegistration(secretKeyA))

	for name, edit := range map[string]func(r *forge.Registration){
		"only the set ID":       func(r *forge.Registration) { r.OldEventSecretSetID = full.OldEventSecretSetID },
		"only the sealed value": func(r *forge.Registration) { r.OldSealedEventSecret = full.OldSealedEventSecret },
		"only the end":          func(r *forge.Registration) { r.OldEventSecretEndsAt = full.OldEventSecretEndsAt },
		"no end": func(r *forge.Registration) {
			r.OldEventSecretSetID, r.OldSealedEventSecret = full.OldEventSecretSetID, full.OldSealedEventSecret
		},
		"no sealed value": func(r *forge.Registration) {
			r.OldEventSecretSetID, r.OldEventSecretEndsAt = full.OldEventSecretSetID, full.OldEventSecretEndsAt
		},
		"no set ID": func(r *forge.Registration) {
			r.OldSealedEventSecret, r.OldEventSecretEndsAt = full.OldSealedEventSecret, full.OldEventSecretEndsAt
		},
	} {
		r := newRegistration(secretKeyA)
		edit(&r)

		if _, err := put(t, h, r, 0); !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("put with %s = %v, want ErrInvalid", name, err)
		}
	}

	if _, err := h.Store.GetRegistration(t.Context(), forge.KindGitHub); !errors.Is(err, forge.ErrNoRegistration) {
		t.Fatalf("get after refused puts = %v, want ErrNoRegistration", err)
	}

	if n := count(t, h, audit.ServerScope); n != 0 {
		t.Fatalf("%d entries after refused puts, want 0", n)
	}

	// The same refusal holds for a replace: the stored window stays.
	first := mustPutRotating(t, h, full)

	partial := newRegistration(secretKeyB)
	partial.OldEventSecretSetID = forge.NewSetID()

	if _, err := put(t, h, partial, first.Version); !errors.Is(err, forge.ErrInvalid) {
		t.Fatalf("replace with a partial set = %v, want ErrInvalid", err)
	}

	read, err := h.Store.GetRegistration(t.Context(), forge.KindGitHub)
	if err != nil || read.Version != first.Version || !sameOld(&read, &full) {
		t.Fatalf("get = %+v, %v; want the first registration with its window", read, err)
	}
}

func testRotationClearedByPut(t *testing.T, h Harness) {
	t.Helper()

	first := mustPutRotating(t, h, rotating(newRegistration(secretKeyA)))

	next := newRegistration(secretKeyB)
	next.HookID = first.HookID

	got, err := put(t, h, next, first.Version)
	if err != nil {
		t.Fatalf("replace: %v", err)
	}

	if !noOld(&got) {
		t.Fatalf("replace answer = %+v, want no old fields", got)
	}

	read, err := h.Store.GetRegistration(t.Context(), forge.KindGitHub)
	if err != nil || !noOld(&read) || read.Version != 2 {
		t.Fatalf("get = %+v, %v; want version 2 with no old fields", read, err)
	}
}

func testRotationStaleVersionKeepsBoth(t *testing.T, h Harness) {
	t.Helper()

	first := mustPutRotating(t, h, rotating(newRegistration(secretKeyA)))

	moved := rotating(newRegistration(secretKeyB))
	moved.OldSealedEventSecret = first.SealedEventSecret
	moved.OldEventSecretSetID = first.EventSecretSetID

	if _, err := put(t, h, moved, first.Version+1); !errors.Is(err, forge.ErrVersionConflict) {
		t.Fatalf("put with a stale version = %v, want ErrVersionConflict", err)
	}

	assertUnchanged(t, h, &first)
}

func testRotationBadEntryKeepsBoth(t *testing.T, h Harness) {
	t.Helper()

	first := mustPutRotating(t, h, rotating(newRegistration(secretKeyA)))

	h.Namespace(t, nsAcme.String())

	moved := rotating(newRegistration(secretKeyB))
	moved.OldSealedEventSecret = first.SealedEventSecret
	moved.OldEventSecretSetID = first.EventSecretSetID

	_, err := h.Store.PutRegistration(t.Context(), moved, first.Version, entry(nsAcme, actionPutRegistration))
	if !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("put with the entry of a namespace = %v, want ErrInvalidEntry", err)
	}

	assertUnchanged(t, h, &first)
}

// assertUnchanged fails when the stored registration differs from want in its
// current secret, its old fields or its version, or when an entry was added.
func assertUnchanged(t *testing.T, h Harness, want *forge.Registration) {
	t.Helper()

	read, err := h.Store.GetRegistration(t.Context(), forge.KindGitHub)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if read.Version != want.Version || !bytes.Equal(read.SealedEventSecret, want.SealedEventSecret) ||
		read.EventSecretSetID != want.EventSecretSetID || !sameOld(&read, want) {
		t.Fatalf("get = %+v, want the registration as before %+v", read, *want)
	}

	if n := count(t, h, audit.ServerScope); n != 1 {
		t.Fatalf("%d entries, want 1 (the first put)", n)
	}
}

func testRotationTwoPutsOneVersion(t *testing.T, h Harness) {
	t.Helper()

	first := mustPut(t, h, newRegistration(secretKeyA))

	const writers = 8

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		wins    int
		refused int
		other   []error
	)

	for i := range writers {
		wg.Go(func() {
			next := newRegistration(secretKeyB)
			next.HookID = first.HookID
			next.OldEventSecretSetID = first.EventSecretSetID
			next.OldSealedEventSecret = []byte(strconv.Itoa(i))
			next.OldEventSecretEndsAt = time.UnixMilli(testTimeMs).UTC().Add(7 * 24 * time.Hour)

			_, err := put(t, h, next, first.Version)

			mu.Lock()
			defer mu.Unlock()

			switch {
			case err == nil:
				wins++
			case errors.Is(err, forge.ErrVersionConflict):
				refused++
			default:
				other = append(other, err)
			}
		})
	}

	wg.Wait()

	if wins != 1 || refused != writers-1 || len(other) != 0 {
		t.Fatalf("%d wins, %d conflicts, other errors %v; want 1 win and %d conflicts", wins, refused, other, writers-1)
	}

	read, err := h.Store.GetRegistration(t.Context(), forge.KindGitHub)
	if err != nil || read.Version != first.Version+1 || len(read.OldSealedEventSecret) != 1 {
		t.Fatalf("get = %+v, %v; want one winner at version %d", read, err, first.Version+1)
	}

	const putAndWinner = 2

	if n := count(t, h, audit.ServerScope); n != putAndWinner {
		t.Fatalf("%d entries, want %d", n, putAndWinner)
	}
}

func confirm(t *testing.T, h Harness, ifVersion int64) (forge.Registration, error) {
	t.Helper()

	return h.Store.ConfirmRotation(
		t.Context(), forge.KindGitHub, ifVersion, entry(audit.ServerScope, actionConfirmRotation),
	)
}

func testConfirmClears(t *testing.T, h Harness) {
	t.Helper()

	first := mustPutRotating(t, h, rotating(newRegistration(secretKeyA)))

	got, err := confirm(t, h, first.Version)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}

	if !noOld(&got) || got.Version != first.Version+1 || !sameRegistration(&got, &first) {
		t.Fatalf("confirm answer = %+v, want the registration with no old fields at version %d", got, first.Version+1)
	}

	read, err := h.Store.GetRegistration(t.Context(), forge.KindGitHub)
	if err != nil || !noOld(&read) || read.Version != first.Version+1 || !sameRegistration(&read, &first) {
		t.Fatalf("get = %+v, %v; want the same current secret and no old fields", read, err)
	}

	entries := h.Audit(t, audit.ServerScope)
	if len(entries) != 2 || entries[1].Action != actionConfirmRotation {
		t.Fatalf("entries = %+v, want the put and one confirm entry", entries)
	}
}

func testConfirmNoWindow(t *testing.T, h Harness) {
	t.Helper()

	first := mustPut(t, h, newRegistration(secretKeyA))

	if _, err := confirm(t, h, 0); !errors.Is(err, forge.ErrNoRotation) {
		t.Fatalf("confirm with no window = %v, want ErrNoRotation", err)
	}

	if _, err := confirm(t, h, first.Version); !errors.Is(err, forge.ErrNoRotation) {
		t.Fatalf("confirm with no window and the version = %v, want ErrNoRotation", err)
	}

	read, err := h.Store.GetRegistration(t.Context(), forge.KindGitHub)
	if err != nil || read.Version != first.Version {
		t.Fatalf("get = %+v, %v; want version %d (no write)", read, err, first.Version)
	}

	if n := count(t, h, audit.ServerScope); n != 1 {
		t.Fatalf("%d entries, want 1 (the put)", n)
	}
}

// testConfirmAfterEnd proves that a window is closed at its end: a confirm
// with an entry at the end, or after it, answers ErrNoRotation, whatever the
// version, and changes nothing. A confirm just before the end still works.
func testConfirmAfterEnd(t *testing.T, h Harness) {
	t.Helper()

	first := mustPutRotating(t, h, rotating(newRegistration(secretKeyA)))
	ends := first.OldEventSecretEndsAt

	for _, tc := range []struct {
		name      string
		at        time.Time
		ifVersion int64
	}{
		{"at the end", ends, 0},
		{"one millisecond after the end", ends.Add(time.Millisecond), 0},
		{"a week after the end", ends.Add(7 * 24 * time.Hour), 0},
		{"after the end with the version", ends.Add(time.Millisecond), first.Version},
	} {
		e := entry(audit.ServerScope, actionConfirmRotation)
		e.At = tc.at

		_, err := h.Store.ConfirmRotation(t.Context(), forge.KindGitHub, tc.ifVersion, e)
		if !errors.Is(err, forge.ErrNoRotation) {
			t.Fatalf("confirm %s = %v, want ErrNoRotation", tc.name, err)
		}

		assertUnchanged(t, h, &first)

		if n := count(t, h, audit.ServerScope); n != 1 {
			t.Fatalf("confirm %s left %d entries, want 1 (the put)", tc.name, n)
		}
	}

	e := entry(audit.ServerScope, actionConfirmRotation)
	e.At = ends.Add(-time.Millisecond)

	if _, err := h.Store.ConfirmRotation(t.Context(), forge.KindGitHub, first.Version, e); err != nil {
		t.Fatalf("confirm one millisecond before the end: %v", err)
	}
}

func testConfirmVersion(t *testing.T, h Harness) {
	t.Helper()

	first := mustPutRotating(t, h, rotating(newRegistration(secretKeyA)))

	if _, err := confirm(t, h, first.Version+1); !errors.Is(err, forge.ErrVersionConflict) {
		t.Fatalf("confirm with another version = %v, want ErrVersionConflict", err)
	}

	assertUnchanged(t, h, &first)
}

func testConfirmMissing(t *testing.T, h Harness) {
	t.Helper()

	if _, err := confirm(t, h, 0); !errors.Is(err, forge.ErrNoRegistration) {
		t.Fatalf("confirm of a missing registration = %v, want ErrNoRegistration", err)
	}

	if n := count(t, h, audit.ServerScope); n != 0 {
		t.Fatalf("%d entries, want 0", n)
	}
}

func testConfirmEntryScope(t *testing.T, h Harness) {
	t.Helper()

	first := mustPutRotating(t, h, rotating(newRegistration(secretKeyA)))

	h.Namespace(t, nsAcme.String())

	_, err := h.Store.ConfirmRotation(
		t.Context(),
		forge.KindGitHub,
		first.Version,
		entry(nsAcme, actionConfirmRotation),
	)
	if !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("confirm with the entry of a namespace = %v, want ErrInvalidEntry", err)
	}

	assertUnchanged(t, h, &first)
}

func testConfirmTwice(t *testing.T, h Harness) {
	t.Helper()

	first := mustPutRotating(t, h, rotating(newRegistration(secretKeyA)))

	if _, err := confirm(t, h, first.Version); err != nil {
		t.Fatalf("first confirm: %v", err)
	}

	if _, err := confirm(t, h, 0); !errors.Is(err, forge.ErrNoRotation) {
		t.Fatalf("second confirm = %v, want ErrNoRotation", err)
	}
}

func testRotationFailedAppendKeepsBoth(t *testing.T, h Harness) {
	t.Helper()

	if h.BreakAuditAppends == nil {
		t.Skip("the harness cannot make an audit append fail")
	}

	first := mustPutRotating(t, h, rotating(newRegistration(secretKeyA)))

	moved := rotating(newRegistration(secretKeyB))
	moved.OldSealedEventSecret = first.SealedEventSecret
	moved.OldEventSecretSetID = first.EventSecretSetID

	h.BreakAuditAppends(t)

	if _, err := put(t, h, moved, first.Version); err == nil {
		t.Fatal("put with a broken audit append = nil, want an error")
	}

	assertUnchanged(t, h, &first)
}

func testConfirmFailedAppendKeepsWindow(t *testing.T, h Harness) {
	t.Helper()

	if h.BreakAuditAppends == nil {
		t.Skip("the harness cannot make an audit append fail")
	}

	first := mustPutRotating(t, h, rotating(newRegistration(secretKeyA)))

	h.BreakAuditAppends(t)

	if _, err := confirm(t, h, first.Version); err == nil {
		t.Fatal("confirm with a broken audit append = nil, want an error")
	}

	assertUnchanged(t, h, &first)
}
