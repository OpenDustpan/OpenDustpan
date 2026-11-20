package codec

import (
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
