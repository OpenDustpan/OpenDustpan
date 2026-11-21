package codec

import (
	"encoding/json"
	"fmt"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
)

// ProfileRow is the column representation of a profile. Both dialects
// generate the same fields, so a row converts with ProfileRow(row).
type ProfileRow struct {
	NamespaceID string
	ID          string
	Name        string
	Latest      int64
	CreatedBy   string
	CreatedAtMs int64
	UpdatedAtMs int64
	DeletedAtMs int64
}

// ProfileRevisionRow is the column representation of a profile revision.
type ProfileRevisionRow struct {
	NamespaceID    string
	ProfileID      string
	Number         int64
	Document       string
	DocumentSha256 string
	Author         string
	CreatedAtMs    int64
	Reason         string
}

// ProfileFromRow decodes a profile row of the namespace ns.
func ProfileFromRow(ns namespace.Name, r ProfileRow) profile.Profile {
	return profile.Profile{
		ID:        profile.ID(r.ID),
		Namespace: ns,
		Name:      profile.Name(r.Name),
		Latest:    int(r.Latest),
		CreatedAt: FromMillis(r.CreatedAtMs),
		CreatedBy: r.CreatedBy,
		UpdatedAt: FromMillis(r.UpdatedAtMs),
	}
}

// ProfileRevisionFromRow decodes a revision row.
func ProfileRevisionFromRow(r ProfileRevisionRow) profile.Revision {
	return profile.Revision{
		Profile:   profile.ID(r.ProfileID),
		Number:    int(r.Number),
		Document:  []byte(r.Document),
		SHA256:    r.DocumentSha256,
		Author:    r.Author,
		CreatedAt: FromMillis(r.CreatedAtMs),
		Reason:    r.Reason,
	}
}

// ProfileOpenSessionRow is the column representation of an open session. Both
// dialects generate the same fields, so a row converts with
// ProfileOpenSessionRow(row).
type ProfileOpenSessionRow struct {
	NamespaceID   string
	ID            string
	RootProfileID string
	OwnerKind     string
	OwnerID       string
	Consumer      string
	Paths         string
	Sealed        []byte
	CreatedAtMs   int64
	ExpiresAtMs   int64
}

// EncodeSessionPaths encodes the paths of a session as a JSON array of text.
// No path is the empty array.
func EncodeSessionPaths(paths []string) (string, error) {
	if len(paths) == 0 {
		return "[]", nil
	}

	b, err := json.Marshal(paths)
	if err != nil {
		return "", fmt.Errorf("%w: encode session paths: %w", profile.ErrInvalid, err)
	}

	return string(b), nil
}

// ProfileSessionFromRow decodes a session row of the namespace ns. It checks
// the row with profile.NewSession, so a row of another version that fails the
// rules is an error and never a session. The error wraps profile.ErrInvalid.
func ProfileSessionFromRow(ns namespace.Name, r ProfileOpenSessionRow) (profile.Session, error) {
	var paths []string
	if err := json.Unmarshal([]byte(r.Paths), &paths); err != nil {
		return profile.Session{}, fmt.Errorf("%w: stored session paths: %w", profile.ErrInvalid, err)
	}

	// Fail closed: the text null decodes with no error to nil paths, and nil
	// paths means all paths. The writer stores an empty array for all paths,
	// so a row that holds null is not a row that this server wrote.
	if paths == nil {
		return profile.Session{}, fmt.Errorf("%w: stored session paths are not a JSON array", profile.ErrInvalid)
	}

	return profile.NewSession(profile.SessionParams{
		ID: r.ID, Namespace: ns, Root: profile.ID(r.RootProfileID),
		Owner:    profile.SessionOwner{Kind: profile.OwnerKind(r.OwnerKind), ID: r.OwnerID},
		Consumer: profile.OpenConsumer(r.Consumer), Paths: paths, Sealed: r.Sealed,
		CreatedAt: FromMillis(r.CreatedAtMs), ExpiresAt: FromMillis(r.ExpiresAtMs),
	})
}
