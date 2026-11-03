// Package releaserecord reads the release record: the committed list of the
// migration files that each release tag shipped, in shipped.txt.
//
// The record is the one fact in the tree that tells whether a tag shipped.
// The checks that hold the shipped migrations and the version of the API
// description read it, and so does scripts/regenerate-baseline.sh. While the
// record lists no file, those checks stay off and name the record in their
// skip message. From the first line on, they run. The release adds its lines
// in the commit that it tags, so the checks turn on in the same change that
// records the tag. A test reads the record from the tree with no network and
// no Git history.
package releaserecord

import (
	"crypto/sha256"
	_ "embed" // The record is embedded so that a test of any package reads the same bytes.
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Path is the path of the record from the root of the repository.
const Path = "internal/releaserecord/shipped.txt"

// Dialects returns the store dialects that each tag must list.
func Dialects() []string {
	return []string{"postgres", "sqlite"}
}

//go:embed shipped.txt
var shipped []byte

// ErrBadRecord reports a record that is not in the form that shipped.txt
// documents. A caller fails closed on it: it never counts a bad record as
// "not released".
var ErrBadRecord = errors.New("the release record is not valid")

// ErrMigrationFiles reports a migrations directory that the release record
// does not allow.
var ErrMigrationFiles = errors.New("the migration files do not follow the release record")

// fieldCount is the number of fields of a line of the record.
const fieldCount = 4

// The allowlists of the four fields of a line.
var (
	tagPattern  = regexp.MustCompile(`^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$`)
	filePattern = regexp.MustCompile(`^\d{4}_[a-z0-9_]+\.sql$`)
	sumPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Entry is one migration file that a tag shipped.
type Entry struct {
	Tag     string
	Dialect string
	File    string
	SHA256  string
}

// Record is the parsed release record.
type Record struct {
	entries []Entry
}

// Current parses the record that the tree holds.
func Current() (Record, error) {
	return Parse(shipped)
}

// Parse reads a record. Blank lines and lines that start with "#" are
// comments. Each other line is "<tag> <dialect> <file> <sha256>". Each tag
// lists every migration file of each dialect at the tag, so a file that an
// earlier tag shipped appears again under each later tag. Parse refuses any
// other form, an unknown dialect, a line that a tag lists twice, one file of
// a dialect with two SHA-256 values, and a tag that does not list a file of
// each dialect.
func Parse(data []byte) (Record, error) {
	var rec Record

	seen := map[Entry]bool{}
	sums := map[string]string{}

	for i, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		entry, err := parseLine(line)
		if err != nil {
			return Record{}, fmt.Errorf("line %d: %w", i+1, err)
		}

		key := Entry{Tag: entry.Tag, Dialect: entry.Dialect, File: entry.File}
		if seen[key] {
			return Record{}, fmt.Errorf("%w: line %d: %s lists %s %s twice",
				ErrBadRecord, i+1, entry.Tag, entry.Dialect, entry.File)
		}

		seen[key] = true

		file := entry.Dialect + " " + entry.File
		if sum, ok := sums[file]; ok && sum != entry.SHA256 {
			return Record{}, fmt.Errorf("%w: line %d: %s has the SHA-256 %s here and %s on an earlier line",
				ErrBadRecord, i+1, file, entry.SHA256, sum)
		}

		sums[file] = entry.SHA256
		rec.entries = append(rec.entries, entry)
	}

	if err := checkEachDialect(rec.entries); err != nil {
		return Record{}, err
	}

	return rec, nil
}

// checkEachDialect refuses a tag that does not list a file of each dialect.
func checkEachDialect(entries []Entry) error {
	dialectsOfTag := map[string]map[string]bool{}

	for _, entry := range entries {
		if dialectsOfTag[entry.Tag] == nil {
			dialectsOfTag[entry.Tag] = map[string]bool{}
		}

		dialectsOfTag[entry.Tag][entry.Dialect] = true
	}

	for tag, dialects := range dialectsOfTag {
		for _, dialect := range Dialects() {
			if !dialects[dialect] {
				return fmt.Errorf("%w: tag %s lists no %s migration", ErrBadRecord, tag, dialect)
			}
		}
	}

	return nil
}

// parseLine reads one line of four fields, each checked by its allowlist.
func parseLine(line string) (Entry, error) {
	fields := strings.Split(line, " ")
	if len(fields) != fieldCount {
		return Entry{}, fmt.Errorf("%w: want %d fields separated by one space", ErrBadRecord, fieldCount)
	}

	entry := Entry{Tag: fields[0], Dialect: fields[1], File: fields[2], SHA256: fields[3]}

	switch {
	case !tagPattern.MatchString(entry.Tag):
		return Entry{}, fmt.Errorf("%w: tag %q is not vMAJOR.MINOR.PATCH", ErrBadRecord, entry.Tag)
	case !slices.Contains(Dialects(), entry.Dialect):
		return Entry{}, fmt.Errorf("%w: dialect %q is not one of %v", ErrBadRecord, entry.Dialect, Dialects())
	case !filePattern.MatchString(entry.File):
		return Entry{}, fmt.Errorf("%w: file %q is not NNNN_name.sql", ErrBadRecord, entry.File)
	case !sumPattern.MatchString(entry.SHA256):
		return Entry{}, fmt.Errorf("%w: sha256 %q is not 64 lowercase hex digits", ErrBadRecord, entry.SHA256)
	}

	return entry, nil
}

// Released reports whether the record lists a shipped migration file.
func (r Record) Released() bool {
	return len(r.entries) > 0
}

// Shipped returns the entries of dialect, in the order of the record.
func (r Record) Shipped(dialect string) []Entry {
	var out []Entry

	for _, entry := range r.entries {
		if entry.Dialect == dialect {
			out = append(out, entry)
		}
	}

	return out
}

// TagCopies checks the copies of the migrations of one tag, a map from file
// name to SQL, against the lines of that tag and dialect in the record. It
// returns one error for each problem: a copy that the record does not list
// for the tag, a copy with another SHA-256, and a listed file with no copy.
//
// Defense in depth: the release build refuses a tag without its lines, and
// the record parser refuses one file with two SHA-256 values. This check
// stays so that lines removed from the record after the tag fail too, while
// the copies of the tag are in the tree.
func (r Record) TagCopies(dialect, tag string, files map[string]string) []error {
	listed := map[string]string{}

	for _, entry := range r.Shipped(dialect) {
		if entry.Tag == tag {
			listed[entry.File] = entry.SHA256
		}
	}

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}

	slices.Sort(names)

	var problems []error

	for _, name := range names {
		digest := sha256.Sum256([]byte(files[name]))
		got := hex.EncodeToString(digest[:])

		want, ok := listed[name]

		switch {
		case !ok:
			problems = append(problems, fmt.Errorf(
				"%w: the %s copy %s of %s has no line in %s: never remove a line of a tag",
				ErrMigrationFiles, dialect, name, tag, Path,
			))
		case got != want:
			problems = append(problems, fmt.Errorf(
				"%w: the %s copy %s of %s has the SHA-256 %s and %s lists %s: never edit a tag copy or a line of a tag",
				ErrMigrationFiles, dialect, name, tag, got, Path, want,
			))
		}

		delete(listed, name)
	}

	for _, name := range slices.Sorted(maps.Keys(listed)) {
		problems = append(problems, fmt.Errorf(
			"%w: %s lists the %s file %s for %s, and the tag copies have no such file",
			ErrMigrationFiles, Path, dialect, name, tag,
		))
	}

	return problems
}

// Off returns the reason that check is off: the record lists no shipped
// migration file. It returns "" when the record lists one, and the check
// must run.
func (r Record) Off(check string) string {
	if r.Released() {
		return ""
	}

	return check + " is off until the first tag: " + Path +
		" lists no shipped migration file; the release adds its lines in the commit that it tags"
}

// MigrationFiles checks the migration files of dialect, a map from file name
// to SQL, against the record. It returns one error for each problem.
//
// Before the first tag, the files must be the baseline alone: a change to the
// schema edits the baseline in place and adds no numbered file. After the
// first tag, each file that the record lists for dialect must be present with
// the same SHA-256, and each other file must have a version above every
// listed file: a change to the schema adds a new numbered file.
func (r Record) MigrationFiles(dialect, baseline string, files map[string]string) []error {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}

	slices.Sort(names)

	if !r.Released() {
		if len(names) != 1 || names[0] != baseline {
			return []error{fmt.Errorf(
				"%w: %s migration files = %v, want [%s]: until the first tag, edit %s and add no numbered migration",
				ErrMigrationFiles, dialect, names, baseline, baseline,
			)}
		}

		return nil
	}

	var problems []error

	listed := map[string]bool{}
	highest := -1

	for _, entry := range r.Shipped(dialect) {
		listed[entry.File] = true
		highest = max(highest, fileVersion(entry.File))

		sql, ok := files[entry.File]
		if !ok {
			problems = append(problems, fmt.Errorf(
				"%w: %s migration %s shipped in %s and is missing: never remove a shipped migration",
				ErrMigrationFiles, dialect, entry.File, entry.Tag,
			))

			continue
		}

		digest := sha256.Sum256([]byte(sql))
		if got := hex.EncodeToString(digest[:]); got != entry.SHA256 {
			problems = append(problems, fmt.Errorf(
				"%w: %s migration %s shipped in %s with SHA-256 %s and now has %s: "+
					"never edit a shipped migration; add a new numbered file",
				ErrMigrationFiles, dialect, entry.File, entry.Tag, entry.SHA256, got,
			))
		}
	}

	for _, name := range names {
		if listed[name] {
			continue
		}

		if fileVersion(name) <= highest {
			problems = append(problems, fmt.Errorf(
				"%w: %s migration %s is not above the shipped migrations: number a new file above %04d",
				ErrMigrationFiles, dialect, name, highest,
			))
		}
	}

	return problems
}

// fileVersion returns the number at the start of a migration file name, or
// -1 when the name has no such number.
func fileVersion(name string) int {
	prefix, _, ok := strings.Cut(name, "_")
	if !ok {
		return -1
	}

	n, err := strconv.Atoi(prefix)
	if err != nil || n < 0 {
		return -1
	}

	return n
}
