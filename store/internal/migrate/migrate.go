// Package migrate loads ordered SQL migrations from an embedded directory.
// Each store adapter applies them with its own driver and locking.
package migrate

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"
)

// ErrBadName reports a migration file not named NNNN_description.sql.
var ErrBadName = errors.New("migration file must be named NNNN_description.sql")

// Migration is one versioned schema change.
type Migration struct {
	Name    string
	SQL     string
	Version int64
}

// Load reads every *.sql file in dir of fsys, ordered by version.
func Load(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}

	migrations := make([]Migration, 0, len(entries))

	for _, entry := range entries {
		if entry.IsDir() || path.Ext(entry.Name()) != ".sql" {
			continue
		}

		m, err := loadFile(fsys, dir, entry.Name())
		if err != nil {
			return nil, err
		}

		migrations = append(migrations, m)
	}

	slices.SortFunc(migrations, func(a, b Migration) int {
		return int(a.Version - b.Version)
	})

	return migrations, nil
}

func loadFile(fsys fs.FS, dir, name string) (Migration, error) {
	prefix, _, ok := strings.Cut(name, "_")
	if !ok {
		return Migration{}, fmt.Errorf("%w: %s", ErrBadName, name)
	}

	version, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil || version <= 0 {
		return Migration{}, fmt.Errorf("%w: %s", ErrBadName, name)
	}

	data, err := fs.ReadFile(fsys, path.Join(dir, name))
	if err != nil {
		return Migration{}, fmt.Errorf("read migration %s: %w", name, err)
	}

	return Migration{Name: name, SQL: string(data), Version: version}, nil
}
