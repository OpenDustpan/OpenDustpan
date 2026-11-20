package sqlite

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/profile/profiletest"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

const (
	rowNamespace = "ns-id-a"
	rowID        = "pos-row"
)

func rowStart() time.Time { return time.UnixMilli(1_700_000_000_000).UTC() }

func rowOwner() profile.SessionOwner { return profile.PrincipalOwner("alice") }

// validSessionRow is a row that sessionFromRow answers for the key of
// readKey at rowStart.
func validSessionRow(t *testing.T) db.ProfileOpenSession {
	t.Helper()

	paths, err := codec.EncodeSessionPaths([]string{"db.password"})
	if err != nil {
		t.Fatalf("encode paths: %v", err)
	}

	return db.ProfileOpenSession{
		NamespaceID: rowNamespace, ID: rowID, RootProfileID: "prf-abc123", OwnerKind: string(profile.OwnerPrincipal),
		OwnerID: "alice", Consumer: "api", Paths: paths, Sealed: []byte("sealed"),
		CreatedAtMs: codec.Millis(rowStart()), ExpiresAtMs: codec.Millis(rowStart().Add(time.Hour)),
	}
}

// TestSessionFromRow sends a hostile row straight to the check of Get that
// follows the query, as if the query had returned it. Each row breaks one
// rule and keeps the others, so each case fails when its own check goes.
func TestSessionFromRow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		change  func(r *db.ProfileOpenSession)
		now     time.Time
		owner   profile.SessionOwner
		wantErr error
	}{
		{
			name: "the row of the key", wantErr: nil,
			change: func(*db.ProfileOpenSession) {}, now: rowStart(), owner: rowOwner(),
		},
		{
			name: "a row of another namespace", wantErr: profile.ErrNotFound,
			change: func(r *db.ProfileOpenSession) { r.NamespaceID = "ns-id-b" }, now: rowStart(), owner: rowOwner(),
		},
		{
			name: "a row of another owner", wantErr: profile.ErrNotFound,
			change: func(*db.ProfileOpenSession) {}, now: rowStart(), owner: profile.PrincipalOwner("bob"),
		},
		{
			name: "a row of the same owner ID with another ID", wantErr: profile.ErrNotFound,
			change: func(r *db.ProfileOpenSession) { r.ID = "pos-other" }, now: rowStart(), owner: rowOwner(),
		},
		{
			name: "an ended row at the end", wantErr: profile.ErrNotFound,
			change: func(*db.ProfileOpenSession) {}, now: rowStart().Add(time.Hour), owner: rowOwner(),
		},
		{
			name: "an ended row after the end", wantErr: profile.ErrNotFound,
			change: func(*db.ProfileOpenSession) {}, now: rowStart().Add(2 * time.Hour), owner: rowOwner(),
		},
		{
			name: "a row with paths that are not JSON", wantErr: profile.ErrInvalid,
			change: func(r *db.ProfileOpenSession) { r.Paths = "not json" }, now: rowStart(), owner: rowOwner(),
		},
		{
			name: "a row of the owner kind job", wantErr: profile.ErrInvalid,
			change: func(r *db.ProfileOpenSession) { r.OwnerKind = "job" }, now: rowStart(), owner: rowOwner(),
		},
		{
			name: "a row with an unknown consumer", wantErr: profile.ErrInvalid,
			change: func(r *db.ProfileOpenSession) { r.Consumer = "run" }, now: rowStart(), owner: rowOwner(),
		},
		{
			name: "a row with no sealed bytes", wantErr: profile.ErrInvalid,
			change: func(r *db.ProfileOpenSession) { r.Sealed = nil }, now: rowStart(), owner: rowOwner(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			row := validSessionRow(t)
			tc.change(&row)

			got, err := sessionFromRow(
				sessionReadKey{ns: "acme", nsID: rowNamespace, id: rowID, owner: tc.owner, now: tc.now}, row,
			)
			if tc.wantErr == nil {
				if err != nil || got.ID() != rowID {
					t.Fatalf("sessionFromRow = %v, %v, want the session", got.ID(), err)
				}

				return
			}

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("sessionFromRow error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestGetQueryGuards calls the generated query of Get alone, with no check of
// Go after it, with a key that is wrong in one part. No row returns.
func TestGetQueryGuards(t *testing.T) {
	t.Parallel()

	s := openInternalTemplated(t)
	ports := profiletest.Store{
		Profiles: s.Profiles(), Namespaces: s, Audit: s, Records: nil, Sessions: s.ProfileSessions(),
	}
	id := profiletest.SeedOpenSession(t, ports, "acme")
	other := profiletest.SeedOpenSession(t, ports, "globex")
	owner, now := profiletest.SeedOwner(), profiletest.SeedNow()

	nsID, err := profileNamespaceID(t.Context(), s.queries, "acme")
	if err != nil {
		t.Fatalf("namespace ID: %v", err)
	}

	otherNS, err := profileNamespaceID(t.Context(), s.queries, namespace.Name("globex"))
	if err != nil {
		t.Fatalf("namespace ID: %v", err)
	}

	key := db.GetProfileOpenSessionParams{
		NamespaceID: nsID, ID: id, OwnerKind: string(owner.Kind), OwnerID: owner.ID, NowMs: codec.Millis(now),
	}

	if _, err = s.queries.GetProfileOpenSession(t.Context(), key); err != nil {
		t.Fatalf("query with the right key: %v, want the row", err)
	}

	tests := []struct {
		name   string
		change func(p *db.GetProfileOpenSessionParams)
	}{
		{"another owner", func(p *db.GetProfileOpenSessionParams) { p.OwnerID = "bob" }},
		{"another owner kind", func(p *db.GetProfileOpenSessionParams) { p.OwnerKind = "job" }},
		{"an owner ID with a wildcard", func(p *db.GetProfileOpenSessionParams) { p.OwnerID = "%" }},
		{"another namespace", func(p *db.GetProfileOpenSessionParams) { p.NamespaceID = otherNS }},
		{"an ID of another namespace", func(p *db.GetProfileOpenSessionParams) { p.ID = other }},
		{"an ID with a wildcard", func(p *db.GetProfileOpenSessionParams) { p.ID = "pos-%" }},
		{"an ID with SQL text", func(p *db.GetProfileOpenSessionParams) { p.ID = id + "' OR '1'='1" }},
		{"the end time", func(p *db.GetProfileOpenSessionParams) { p.NowMs = codec.Millis(now.Add(time.Hour)) }},
		{
			"after the end time",
			func(p *db.GetProfileOpenSessionParams) { p.NowMs = codec.Millis(now.Add(24 * time.Hour)) },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := key
			tc.change(&p)

			if _, err := s.queries.GetProfileOpenSession(t.Context(), p); !errors.Is(err, sql.ErrNoRows) {
				t.Errorf("query with %s: error = %v, want no rows", tc.name, err)
			}
		})
	}
}
