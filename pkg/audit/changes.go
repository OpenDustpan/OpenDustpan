package audit

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/ident"
)

// Change is one key of an entry with its values: a setting of a start entry
// or of a reload of the configuration file (ADR 63 decisions 9 and 10), or
// a key of another change that records old and new values, for example a
// token policy. Key names the key. Old is the value before the change, and
// stays nil when the entry records a value and no change, as a start entry
// does. New is the value after the change. Layer names where a setting got
// its value (flag, env, file or default) and stays empty for a key that is
// not a setting. TakesEffect says when a recorded change applies, for
// example "next start", so a reader never takes a recorded change for an
// applied one; it stays empty when the change applied at once.
//
// For a sensitive key, Old and New are only "set" or "not set": the caller
// never passes the value (ADR 63 decision 6).
type Change struct {
	Key         string  `json:"key"`
	Old         *string `json:"old,omitempty"`
	New         string  `json:"new"`
	Layer       string  `json:"layer,omitempty"`
	TakesEffect string  `json:"takesEffect,omitempty"`
}

// MaxChanges caps the changes of one entry, so an entry never grows without
// a bound.
const MaxChanges = 256

// MaxChangeKeyLen caps the bytes of one change key, and MaxChangeValueLen
// caps the bytes of the Old and New text of a change, so a stored entry never
// holds an unbounded text. MaxChangeValueLen is large because a value can be
// a list with one item on each line, for example the token managers of a
// grant.
const (
	MaxChangeKeyLen   = 256
	MaxChangeValueLen = 1 << 20
)

// maxChangeLabelLen caps the bytes of Layer and TakesEffect, which are short
// words.
const maxChangeLabelLen = 256

// EncodeChanges answers the canonical JSON of changes: a list sorted by key,
// each object with its fields in a fixed order. The same changes always
// give the same text, so the hash of an entry never depends on the order in
// which the caller listed them. It answers "" for no changes.
func EncodeChanges(changes []Change) string {
	if len(changes) == 0 {
		return ""
	}

	sorted := slices.Clone(changes)
	slices.SortStableFunc(sorted, func(a, b Change) int { return cmp.Compare(a.Key, b.Key) })

	data, err := json.Marshal(sorted)
	if err != nil {
		return ""
	}

	return string(data)
}

// DecodeChanges reads a stored changes text for display. It is lenient: it
// takes the changes in any order, and it answers nil and an error for a text
// that is not a JSON list of changes. A reader always gets the entry: only
// the chain check, which hashes the stored text as it is (ComputeStoredHash),
// reports a changed text. An empty text reads as nil.
func DecodeChanges(text string) ([]Change, error) {
	if text == "" {
		return nil, nil
	}

	var changes []Change

	if err := json.Unmarshal([]byte(text), &changes); err != nil {
		return nil, fmt.Errorf("decode audit changes: %w", err)
	}

	return changes, nil
}

// validateChanges checks that each change has a key, that no key is in the
// list two times, that the list has at most MaxChanges changes, and that
// each text is valid UTF-8, so the stored JSON reads back to the same text
// and the same hash.
func validateChanges(changes []Change) error {
	if len(changes) > MaxChanges {
		return fmt.Errorf("%w: more than %d changes", ErrInvalidEntry, MaxChanges)
	}

	seen := make(map[string]bool, len(changes))

	for _, c := range changes {
		if c.Key == "" {
			return fmt.Errorf("%w: a change without a key", ErrInvalidEntry)
		}

		if seen[c.Key] {
			return fmt.Errorf("%w: change %q is in the list two times", ErrInvalidEntry, c.Key)
		}

		seen[c.Key] = true

		if err := validateChangeTexts(c); err != nil {
			return err
		}
	}

	return nil
}

// validateChangeTexts checks the texts of one change: each is valid UTF-8,
// holds no hidden character (a control, format or separator character, as a
// line break), and stays inside its byte bound.
//
// Defense in depth: the services that build a change check their own input
// and the stores check an entry again before they write it. This check stays
// so that a new writer cannot store a text that a reader shows in a
// misleading way.
func validateChangeTexts(c Change) error {
	// A key, a layer and a takes-effect text are one line. Old and New may hold
	// line breaks and tabs, because a value can be a list with one item on
	// each line; any other hidden character is refused there too.
	oneLine := func(r rune) bool { return ident.IsHiddenRune(r) }
	multiLine := func(r rune) bool { return ident.IsHiddenRuneExcept(r, '\n', '\t') }

	texts := []struct {
		text   string
		limit  int
		hidden func(rune) bool
	}{
		{c.Key, MaxChangeKeyLen, oneLine},
		{c.New, MaxChangeValueLen, multiLine},
		{c.Layer, maxChangeLabelLen, oneLine},
		{c.TakesEffect, maxChangeLabelLen, oneLine},
	}
	if c.Old != nil {
		texts = append(texts, struct {
			text   string
			limit  int
			hidden func(rune) bool
		}{*c.Old, MaxChangeValueLen, multiLine})
	}

	for _, t := range texts {
		if !utf8.ValidString(t.text) || strings.IndexFunc(t.text, t.hidden) >= 0 {
			return fmt.Errorf("%w: change %q holds text that is not valid or has a hidden character",
				ErrInvalidEntry, printable(c.Key))
		}

		if len(t.text) > t.limit {
			return fmt.Errorf("%w: change %q holds a text of more than %d bytes",
				ErrInvalidEntry, printable(c.Key), t.limit)
		}
	}

	return nil
}

// printable shortens key and replaces each byte that is not printable ASCII,
// so an error text never holds the hostile text of a key.
func printable(key string) string {
	const shown = 40

	out := make([]byte, 0, shown)

	for i := 0; i < len(key) && len(out) < shown; i++ {
		if key[i] < 0x20 || key[i] > 0x7e {
			out = append(out, '?')

			continue
		}

		out = append(out, key[i])
	}

	return string(out)
}
