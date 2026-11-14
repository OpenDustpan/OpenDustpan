package setting

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Registry holds the declarations of a server. It is read only after
// NewRegistry returns it.
type Registry struct {
	byLevel map[Level][]Declaration
}

// NewRegistry validates decls and returns the registry. It returns an error
// that wraps ErrInvalidDeclaration for a declaration that fails Validate and
// for a key that two declarations of one level share.
func NewRegistry(decls []Declaration) (*Registry, error) {
	byLevel := map[Level][]Declaration{LevelInstallation: nil, LevelNamespace: nil}
	seen := map[string]bool{}

	for _, d := range decls {
		if err := d.Validate(); err != nil {
			return nil, err
		}

		id := string(d.Level) + "/" + d.Key
		if seen[id] {
			return nil, fmt.Errorf("%w: %s is declared twice", ErrInvalidDeclaration, d.Key)
		}

		seen[id] = true
		byLevel[d.Level] = append(byLevel[d.Level], d)
	}

	for level := range byLevel {
		slices.SortFunc(byLevel[level], func(a, b Declaration) int { return strings.Compare(a.Key, b.Key) })
	}

	return &Registry{byLevel: byLevel}, nil
}

// Declarations returns the declarations of level in key order, as a copy.
func (r *Registry) Declarations(level Level) []Declaration {
	return slices.Clone(r.byLevel[level])
}

// Lookup returns the declaration of key at level.
func (r *Registry) Lookup(level Level, key string) (Declaration, bool) {
	for _, d := range r.byLevel[level] {
		if d.Key == key {
			return d, true
		}
	}

	return Declaration{
		Key: "", Level: "", Kind: "", Default: "", Secret: false, Database: false, StaticCredentialHost: false,
		Apply: "", Check: nil, ReducesSecurity: nil, Doc: "",
	}, false
}

// Effective returns the setting d as an answer shows it. stored is the
// database value, nil when none exists. static is the value of the static
// layer, nil when none sets the key.
//
// The precedence is the static layer, then the database layer, then the
// default. A setting that only the static layer can set is always locked.
func Effective(d Declaration, stored *string, static *StaticValue) Item {
	item := Item{
		Key: d.Key, Level: d.Level, Kind: d.Kind, Value: d.Default, Default: d.Default, Source: SourceDefault,
		Locked: !d.Database, LockedBy: "", DatabaseValue: "", HasDatabaseValue: false, Shadowed: false,
		Apply: d.Apply, Secret: d.Secret, Set: !d.Secret, Database: d.Database,
		ReducesSecurity: d.ReducesSecurity != nil, Doc: d.Doc,
	}

	if !d.Database {
		item.LockedBy = "only the static layer can set this key"
	}

	if stored != nil && d.Database {
		item.Value, item.Source = *stored, SourceDatabase
		item.DatabaseValue, item.HasDatabaseValue = *stored, true
	}

	if static != nil {
		item.Value, item.Source = static.Value, static.Source
		item.Locked = true
		item.LockedBy = static.By
		item.Shadowed = item.HasDatabaseValue
	}

	if d.Secret {
		// Defense in depth: the static layer already hides a secret. This
		// block stays so that no adapter can show a secret value or default.
		item.Set = static != nil
		item.Value = ""
		item.Default = ""
		item.DatabaseValue = ""
	}

	return item
}

// Digest returns a digest of the static layer for the installation
// settings: each key with its source and its value, and for a secret only
// whether it is set. The digest holds no part of a secret. A store keeps it
// to know when the static layer changed an effective value or a source.
func (r *Registry) Digest(static Static) string {
	var text strings.Builder

	for _, d := range r.byLevel[LevelInstallation] {
		value, ok := static.Lookup(d.Key)
		if !ok {
			text.WriteString(d.Key + "\x00-\n")

			continue
		}

		shown := value.Value
		if d.Secret {
			shown = "set"
		}

		text.WriteString(d.Key + "\x00" + string(value.Source) + "\x00" + strconv.Itoa(len(shown)) + ":" + shown + "\n")
	}

	sum := sha256.Sum256([]byte(text.String()))

	return hex.EncodeToString(sum[:])
}
