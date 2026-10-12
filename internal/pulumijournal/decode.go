package pulumijournal

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
)

// Errors that DecodeEntries returns. Each message names the entry, or the
// batch member, and never the value that was refused (ADR 43, decision 5.5).
var (
	// ErrBatchShape reports a batch body that is not an object with one entries array.
	ErrBatchShape = errors.New("pulumijournal: batch body has the wrong shape")
	// ErrUnknownMember reports an entry member that is unknown or has the wrong type.
	ErrUnknownMember = errors.New("pulumijournal: journal entry has an unknown member or a member of the wrong type")
	// ErrMissingMember reports an entry without a member that every entry must have.
	ErrMissingMember = errors.New("pulumijournal: journal entry has no required member")
	// ErrVersion reports an entry version that is zero or above the negotiated version (R14).
	ErrVersion = errors.New("pulumijournal: journal entry version is not supported")
	// ErrUnknownKind reports an entry kind that this package does not know (R14).
	ErrUnknownKind = errors.New("pulumijournal: unknown journal entry kind")
	// ErrKindVersion reports a kind that needs a newer entry version (R14).
	ErrKindVersion = errors.New("pulumijournal: journal entry kind needs a newer version")
	// ErrSequenceID reports an entry with a sequence ID of zero or less (R17).
	ErrSequenceID = errors.New("pulumijournal: journal entry sequence ID is not positive")
)

// stateMigrationVersion is the entry version that kind StateMigration needs (R14).
const stateMigrationVersion = 2

// batchEntriesKey is the name of the array of entries in a batch body.
const batchEntriesKey = "entries"

// entryFields names the members that every entry must have. Its fields hold
// the raw JSON of each member, so an absent member leaves its field nil and
// a member with the value null does not. R15 allows null for removeOld and
// removeNew, so those two only need to be present.
type entryFields struct {
	Version     jsontext.Value `json:"version"`
	Kind        jsontext.Value `json:"kind"`
	SequenceID  jsontext.Value `json:"sequenceID"`
	OperationID jsontext.Value `json:"operationID"`
	RemoveOld   jsontext.Value `json:"removeOld"`
	RemoveNew   jsontext.Value `json:"removeNew"`
}

// DecodeEntries reads a batch body of the form {"entries": [...]} from r and
// calls fn with each entry, in the order of the body. An entry that fails the
// checks stops the decoding, and DecodeEntries returns the error.
//
// fn runs before the later entries of the body are checked. A caller must
// therefore store nothing until DecodeEntries returns nil. A refused batch
// stores nothing (ADR 43, decision 5.5). The size bounds of a batch come in
// item 13, and the 413 answer for a batch that is too large comes in item 9.
//
// The body is never held in full (ADR 43, decision 8.6). The batch refuses an
// unknown or repeated batch member, a missing entries member, data after the
// closing brace, and an entry with an unknown member or a missing required one.
func DecodeEntries(r io.Reader, negotiated int, fn func(JournalEntry) error) error {
	dec := jsontext.NewDecoder(r)
	if err := readDelim(dec, '{'); err != nil {
		return err
	}
	seen := false
	for dec.PeekKind() != '}' {
		key, err := dec.ReadToken()
		if err != nil {
			return fmt.Errorf("pulumijournal: read batch member name: %w: %w", ErrBatchShape, err)
		}
		if key.Kind() != '"' {
			return fmt.Errorf("pulumijournal: batch member name is not a string: %w", ErrBatchShape)
		}
		if key.String() != batchEntriesKey {
			return fmt.Errorf("pulumijournal: unknown batch member: %w", ErrBatchShape)
		}
		if seen {
			return fmt.Errorf("pulumijournal: repeated batch member: %w", ErrBatchShape)
		}
		seen = true
		if err := decodeArray(dec, negotiated, fn); err != nil {
			return err
		}
	}
	if err := readDelim(dec, '}'); err != nil {
		return err
	}
	if !seen {
		return fmt.Errorf("pulumijournal: batch has no %s member: %w", batchEntriesKey, ErrBatchShape)
	}
	if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("pulumijournal: data after the batch: %w", ErrBatchShape)
	}
	return nil
}

// decodeArray reads the entries array one entry at a time.
func decodeArray(dec *jsontext.Decoder, negotiated int, fn func(JournalEntry) error) error {
	if err := readDelim(dec, '['); err != nil {
		return err
	}
	for index := 0; dec.PeekKind() != ']'; index++ {
		entry, err := decodeEntry(dec, index, negotiated)
		if err != nil {
			return err
		}
		if err := fn(entry); err != nil {
			return err
		}
	}
	return readDelim(dec, ']')
}

// decodeEntry reads one entry, checks its members, and validates it. index is
// the position of the entry in the batch. It names the entry in an error when
// the sequence ID is not usable.
func decodeEntry(dec *jsontext.Decoder, index, negotiated int) (JournalEntry, error) {
	var raw jsontext.Value
	if err := json.UnmarshalDecode(dec, &raw); err != nil {
		return JournalEntry{}, fmt.Errorf("pulumijournal: read entry at index %d: %w", index, err)
	}
	var fields entryFields
	if err := json.Unmarshal(raw, &fields); err != nil {
		return JournalEntry{}, fmt.Errorf("pulumijournal: entry at index %d is not an object: %w", index, ErrBatchShape)
	}
	name := entryName(fields, index)
	if member := missingMember(fields); member != "" {
		return JournalEntry{}, fmt.Errorf(
			"pulumijournal: entry %s has no %s member: %w",
			name,
			member,
			ErrMissingMember,
		)
	}
	var entry JournalEntry
	if err := json.Unmarshal(raw, &entry, json.RejectUnknownMembers(true)); err != nil {
		return JournalEntry{}, fmt.Errorf("pulumijournal: entry %s: %w", name, ErrUnknownMember)
	}
	if err := validate(entry, negotiated, name); err != nil {
		return JournalEntry{}, err
	}
	return entry, nil
}

// entryName names an entry in an error. It is the sequence ID when the entry
// has one that is a number, and otherwise the index of the entry in the batch.
func entryName(fields entryFields, index int) string {
	var id int64
	if fields.SequenceID != nil && json.Unmarshal(fields.SequenceID, &id) == nil {
		return fmt.Sprintf("with sequence ID %d", id)
	}
	return fmt.Sprintf("at index %d", index)
}

// missingMember returns the name of the first required member that the entry
// does not have, or an empty string when it has all of them.
func missingMember(fields entryFields) string {
	switch {
	case fields.Version == nil:
		return "version"
	case fields.Kind == nil:
		return "kind"
	case fields.SequenceID == nil:
		return "sequenceID"
	case fields.OperationID == nil:
		return "operationID"
	case fields.RemoveOld == nil:
		return "removeOld"
	case fields.RemoveNew == nil:
		return "removeNew"
	default:
		return ""
	}
}

// readDelim reads one token and returns an error unless it is want.
func readDelim(dec *jsontext.Decoder, want jsontext.Kind) error {
	tok, err := dec.ReadToken()
	if err != nil {
		return fmt.Errorf("pulumijournal: read batch: %w", err)
	}
	if tok.Kind() != want {
		return fmt.Errorf("pulumijournal: batch has an unexpected token: %w", ErrBatchShape)
	}
	return nil
}

// validate applies R14 and R17 to one decoded entry. negotiated is the entry
// version that the CLI and the server agreed on at the start of the update.
// name names the entry in the error.
func validate(entry JournalEntry, negotiated int, name string) error {
	if entry.Version <= 0 || entry.Version > negotiated {
		return fmt.Errorf("pulumijournal: entry %s: %w", name, ErrVersion)
	}
	if entry.Kind < JournalEntryKindBegin || entry.Kind > JournalEntryKindStateMigration {
		return fmt.Errorf("pulumijournal: entry %s: %w", name, ErrUnknownKind)
	}
	if entry.Kind == JournalEntryKindStateMigration && entry.Version != stateMigrationVersion {
		return fmt.Errorf("pulumijournal: entry %s: %w", name, ErrKindVersion)
	}
	if entry.SequenceID <= 0 {
		return fmt.Errorf("pulumijournal: entry %s: %w", name, ErrSequenceID)
	}
	return nil
}
