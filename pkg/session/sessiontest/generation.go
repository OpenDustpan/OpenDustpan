package sessiontest

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/session"
)

// testGenerationRules checks the session generation of a stored session: a
// session with the generation 0 takes the stored generation of its
// principal, a session with another generation than the stored one is
// refused with ErrGenerationChanged and stores nothing, a negative
// generation is ErrInvalid, and the lookup answers the stored generation of
// the owner from the same read.
func testGenerationRules(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	mustPerson(t, s, "prn-1")

	cases := []struct {
		want       error
		name       string
		id         string
		generation int64
		digest     int
	}{
		{name: "zero takes the stored generation", id: "ses-1", generation: 0, digest: 1, want: nil},
		{name: "the stored generation is kept", id: "ses-2", generation: 1, digest: 2, want: nil},
		{
			name: "a newer generation is refused", id: "ses-3", generation: 2, digest: 3,
			want: session.ErrGenerationChanged,
		},
		{name: "a negative generation is refused", id: "ses-4", generation: -1, digest: 4, want: session.ErrInvalid},
	}

	for _, tc := range cases {
		ss := newSession(tc.id, "prn-1", at(5))
		ss.Generation = tc.generation

		err := s.CreateSession(ctx, ss, digest(tc.digest), entry(ss.ID, ss.CreatedAt))
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: create = %v, want %v", tc.name, err, tc.want)
		}

		if tc.want != nil {
			wantNoSession(t, s, tc.name, tc.id)

			continue
		}

		wantGenerationOne(t, s, tc.name, tc.id, digest(tc.digest))
	}
}

// wantNoSession fails unless the session id does not exist.
func wantNoSession(t *testing.T, s Store, name, id string) {
	t.Helper()

	if _, err := s.GetSession(t.Context(), id); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("%s: get after a refused create = %v, want ErrNotFound", name, err)
	}
}

// wantGenerationOne fails unless the session id and the lookup of the digest
// d answer the generation 1 for the session and for its owner.
func wantGenerationOne(t *testing.T, s Store, name, id string, d session.Digest) {
	t.Helper()

	got, err := s.GetSession(t.Context(), id)
	if err != nil {
		t.Fatalf("%s: get: %v", name, err)
	}

	if got.Generation != 1 {
		t.Fatalf("%s: stored generation = %d, want 1", name, got.Generation)
	}

	m, err := s.GetSessionByDigest(t.Context(), d)
	if err != nil {
		t.Fatalf("%s: get by digest: %v", name, err)
	}

	if m.Session.Generation != 1 || m.Generation != 1 {
		t.Fatalf("%s: lookup generations = %d and %d, want 1 and 1", name, m.Session.Generation, m.Generation)
	}
}
