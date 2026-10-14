package audit

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"unicode/utf8"
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

// ErrChangesNotCanonical reports a stored changes text that EncodeChanges
// does not write.
var ErrChangesNotCanonical = errors.New("audit changes are not in canonical form")

// MaxChanges caps the changes of one entry, so an entry never grows without
// a bound.
const MaxChanges = 256

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

// DecodeChanges reads the text of EncodeChanges. An empty text reads as nil.
// It answers ErrChangesNotCanonical for a text that EncodeChanges does not
// write, for example the same changes in another order: the text changed
// after the append, so a chain check counts the entry as changed.
func DecodeChanges(text string) ([]Change, error) {
	if text == "" {
		return nil, nil
	}

	var changes []Change

	if err := json.Unmarshal([]byte(text), &changes); err != nil {
		return nil, fmt.Errorf("decode audit changes: %w", err)
	}

	if EncodeChanges(changes) != text {
		return nil, ErrChangesNotCanonical
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

		valid := utf8.ValidString(c.Key) && utf8.ValidString(c.New) && utf8.ValidString(c.Layer) &&
			utf8.ValidString(c.TakesEffect)
		if c.Old != nil {
			valid = valid && utf8.ValidString(*c.Old)
		}

		if !valid {
			return fmt.Errorf("%w: change %q is not valid UTF-8", ErrInvalidEntry, c.Key)
		}
	}

	return nil
}
