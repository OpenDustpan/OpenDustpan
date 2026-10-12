package pulumijournal

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
)

// Errors that DecodeEntries returns (ADR 43, decision 8; RFC 64).
var (
	// ErrBatchShape reports a batch body that is not an object with an entries array.
	ErrBatchShape = errors.New("pulumijournal: batch body has the wrong shape")
	// ErrVersion reports an entry version that is zero or above the negotiated version (R14).
	ErrVersion = errors.New("pulumijournal: journal entry version is not supported")
	// ErrUnknownKind reports an entry kind that this package does not know (R14).
	ErrUnknownKind = errors.New("pulumijournal: unknown journal entry kind")
	// ErrKindVersion reports a kind that needs a newer entry version (R14).
	ErrKindVersion = errors.New("pulumijournal: journal entry kind needs a newer version")
	// ErrSequenceID reports an entry with sequence ID zero (R17).
	ErrSequenceID = errors.New("pulumijournal: journal entry sequence ID is zero")
)

// stateMigrationVersion is the entry version that kind StateMigration needs (R14).
const stateMigrationVersion = 2

// batchEntriesKey is the name of the array of entries in a batch body.
const batchEntriesKey = "entries"

// DecodeEntries reads a batch body of the form {"entries": [...]} from r. It
// calls fn with each entry, in the order of the body, after the entry passes
// validation against the negotiated version. It stops at the first error and
// returns it. It never holds the whole body in memory (ADR 43, decision 8.6).
func DecodeEntries(r io.Reader, negotiated int, fn func(JournalEntry) error) error {
	dec := jsontext.NewDecoder(r)
	if err := readDelim(dec, '{'); err != nil {
		return err
	}
	for dec.PeekKind() != '}' {
		key, err := dec.ReadToken()
		if err != nil {
			return fmt.Errorf("pulumijournal: read batch key: %w", err)
		}
		if key.Kind() != '"' {
			return fmt.Errorf("pulumijournal: batch key is not a string: %w", ErrBatchShape)
		}
		if key.String() != batchEntriesKey {
			if err := dec.SkipValue(); err != nil {
				return fmt.Errorf("pulumijournal: skip batch field: %w", err)
			}
			continue
		}
		if err := decodeArray(dec, negotiated, fn); err != nil {
			return err
		}
	}
	return readDelim(dec, '}')
}

// decodeArray reads the entries array one entry at a time.
func decodeArray(dec *jsontext.Decoder, negotiated int, fn func(JournalEntry) error) error {
	if err := readDelim(dec, '['); err != nil {
		return err
	}
	for dec.PeekKind() != ']' {
		var entry JournalEntry
		if err := json.UnmarshalDecode(dec, &entry); err != nil {
			return fmt.Errorf("pulumijournal: decode entry: %w", err)
		}
		if err := validate(entry, negotiated); err != nil {
			return err
		}
		if err := fn(entry); err != nil {
			return err
		}
	}
	return readDelim(dec, ']')
}

// readDelim reads one token and returns an error unless it is want.
func readDelim(dec *jsontext.Decoder, want jsontext.Kind) error {
	tok, err := dec.ReadToken()
	if err != nil {
		return fmt.Errorf("pulumijournal: read batch: %w", err)
	}
	if tok.Kind() != want {
		return fmt.Errorf("pulumijournal: batch has token %v, want %v: %w", tok.Kind(), want, ErrBatchShape)
	}
	return nil
}

// validate applies R14 and R17 to one decoded entry. negotiated is the entry
// version that the CLI and the server agreed on at the start of the update.
func validate(entry JournalEntry, negotiated int) error {
	if entry.Version <= 0 || entry.Version > negotiated {
		return fmt.Errorf("entry version %d with negotiated version %d: %w", entry.Version, negotiated, ErrVersion)
	}
	if entry.Kind < JournalEntryKindBegin || entry.Kind > JournalEntryKindStateMigration {
		return fmt.Errorf("entry kind %d: %w", int(entry.Kind), ErrUnknownKind)
	}
	if entry.Kind == JournalEntryKindStateMigration && entry.Version != stateMigrationVersion {
		return fmt.Errorf("entry kind %s with version %d: %w", entry.Kind, entry.Version, ErrKindVersion)
	}
	if entry.SequenceID == 0 {
		return fmt.Errorf("entry kind %s: %w", entry.Kind, ErrSequenceID)
	}
	return nil
}
