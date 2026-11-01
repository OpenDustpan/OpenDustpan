package backup

import (
	"fmt"

	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/localuser/argon2id"
)

// The columns of the table of local users that have a check.
const (
	portColUserName     = "user_name"
	portColPasswordHash = "password_hash"
)

// portableLocalUserTables is the portable description of the table of local
// users, in the order of Tables. The reset codes are not in a backup.
func portableLocalUserTables() []PortableTable {
	return []PortableTable{
		{
			Table:   TableLocalUsers,
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColPrincipalID, Kind: KindText},
				{Name: portColUserName, Kind: KindText},
				{Name: portColPasswordHash, Kind: KindText},
				{Name: "password_set_at_ms", Kind: KindInteger},
				{Name: "change_required", Kind: KindBool},
				{Name: "demo", Kind: KindBool},
				{Name: portColCreatedBy, Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
			},
		},
	}
}

// checkLocalUserValue runs the check of the column of the table of local
// users that has one.
func checkLocalUserValue(column string, value any) error {
	switch column {
	case portColUserName:
		return checkLocalUserName(value)
	case portColPasswordHash:
		return checkLocalUserHash(value)
	default:
		return nil
	}
}

// checkLocalUserName refuses a local user row whose user name a person could
// not have set. It returns ErrCorrupt, wrapped with localuser.ErrInvalid.
//
// Defense in depth: the service and the store check each user name when it
// is set, and the database refuses a name out of the pattern. This check
// stays so that a restored backup cannot bring in a name that skips them.
func checkLocalUserName(value any) error {
	name, isText := value.(string)
	if !isText {
		return fmt.Errorf("local user row has no user name: %w", ErrCorrupt)
	}

	if err := localuser.ValidateUserName(name); err != nil {
		return fmt.Errorf("local user row has an invalid user name: %w: %w", err, ErrCorrupt)
	}

	return nil
}

// checkLocalUserHash refuses a local user row whose password hash is not
// empty and is not a hash that the hasher reads: a hash of another
// algorithm, or one with parameters outside the bounds, which could make the
// server spend unbounded memory or time at the next sign-in. It returns
// ErrCorrupt, wrapped with localuser.ErrInvalid.
//
// Defense in depth: the hasher parses each stored hash again before any
// work. This check stays so that a restored backup cannot bring in such a
// hash at all.
func checkLocalUserHash(value any) error {
	hash, isText := value.(string)
	if !isText {
		return fmt.Errorf("local user row has no password hash: %w", ErrCorrupt)
	}

	if hash == "" {
		return nil
	}

	if _, err := argon2id.Decode(localuser.PasswordHash(hash)); err != nil {
		return fmt.Errorf("local user row has an invalid password hash: %w: %w", err, ErrCorrupt)
	}

	return nil
}
