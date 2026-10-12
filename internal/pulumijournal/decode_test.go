package pulumijournal_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/pulumijournal"
)

// errStop is the error that the callback returns in TestDecodeEntriesStopsAtCallbackError.
var errStop = errors.New("stop")

// decodeAll decodes body with the negotiated version and returns the entries that fn received.
func decodeAll(t *testing.T, body string, negotiated int) ([]pulumijournal.JournalEntry, error) {
	t.Helper()
	var got []pulumijournal.JournalEntry
	err := pulumijournal.DecodeEntries(strings.NewReader(body), negotiated, func(e pulumijournal.JournalEntry) error {
		got = append(got, e)
		return nil
	})
	return got, err
}

// TestDecodeEntriesCallsEachValidEntryInOrder checks the entries of a valid batch.
func TestDecodeEntriesCallsEachValidEntryInOrder(t *testing.T) {
	t.Parallel()
	body := `{"entries":[` +
		`{"version":1,"kind":0,"sequenceID":1,"operationID":1},` +
		`{"version":1,"kind":2,"sequenceID":2,"operationID":1}]}`
	got, err := decodeAll(t, body, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if got[0].SequenceID != 1 || got[1].SequenceID != 2 {
		t.Fatalf("sequence IDs are %d and %d, want 1 and 2", got[0].SequenceID, got[1].SequenceID)
	}
	if got[1].Kind != pulumijournal.JournalEntryKindFailure {
		t.Fatalf("second kind is %v, want failure", got[1].Kind)
	}
}

// TestDecodeEntriesSkipsOtherFields checks that fields other than entries are skipped.
func TestDecodeEntriesSkipsOtherFields(t *testing.T) {
	t.Parallel()
	body := `{"other":{"nested":[1,2,{"x":"y"}]},"entries":[{"version":1,"kind":1,"sequenceID":3,"operationID":1}]}`
	got, err := decodeAll(t, body, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SequenceID != 3 {
		t.Fatalf("got %+v, want one entry with sequence ID 3", got)
	}
}

// TestDecodeEntriesAcceptsStateMigrationAtVersionTwo checks that kind 10 needs version 2 and is then accepted.
func TestDecodeEntriesAcceptsStateMigrationAtVersionTwo(t *testing.T) {
	t.Parallel()
	body := `{"entries":[{"version":2,"kind":10,"sequenceID":1,"operationID":1}]}`
	got, err := decodeAll(t, body, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != pulumijournal.JournalEntryKindStateMigration {
		t.Fatalf("got %+v, want one state migration entry", got)
	}
}

// TestDecodeEntriesRefusesInvalidEntries checks each refusal of R14 and R17.
func TestDecodeEntriesRefusesInvalidEntries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		body       string
		negotiated int
		want       error
	}{
		{
			name:       "version zero",
			body:       `{"entries":[{"version":0,"kind":0,"sequenceID":1,"operationID":1}]}`,
			negotiated: 1,
			want:       pulumijournal.ErrVersion,
		},
		{
			name:       "version above the negotiated one",
			body:       `{"entries":[{"version":2,"kind":0,"sequenceID":1,"operationID":1}]}`,
			negotiated: 1,
			want:       pulumijournal.ErrVersion,
		},
		{
			name:       "unknown kind",
			body:       `{"entries":[{"version":1,"kind":11,"sequenceID":1,"operationID":1}]}`,
			negotiated: 1,
			want:       pulumijournal.ErrUnknownKind,
		},
		{
			name:       "negative kind",
			body:       `{"entries":[{"version":1,"kind":-1,"sequenceID":1,"operationID":1}]}`,
			negotiated: 1,
			want:       pulumijournal.ErrUnknownKind,
		},
		{
			name:       "state migration at version one",
			body:       `{"entries":[{"version":1,"kind":10,"sequenceID":1,"operationID":1}]}`,
			negotiated: 2,
			want:       pulumijournal.ErrKindVersion,
		},
		{
			name:       "sequence ID zero",
			body:       `{"entries":[{"version":1,"kind":0,"sequenceID":0,"operationID":1}]}`,
			negotiated: 1,
			want:       pulumijournal.ErrSequenceID,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeAll(t, tt.body, tt.negotiated)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error is %v, want %v", err, tt.want)
			}
		})
	}
}

// TestDecodeEntriesRejectsBatchShape checks a body that is not an object with an entries array.
func TestDecodeEntriesRejectsBatchShape(t *testing.T) {
	t.Parallel()
	for _, body := range []string{`[]`, `{"entries":{}}`, `{"entries":[1]}`} {
		_, err := decodeAll(t, body, 1)
		if err == nil {
			t.Fatalf("body %s: no error", body)
		}
	}
}

// TestDecodeEntriesStopsAtCallbackError checks that an error from fn stops the decoding and is returned.
func TestDecodeEntriesStopsAtCallbackError(t *testing.T) {
	t.Parallel()
	body := `{"entries":[{"version":1,"kind":0,"sequenceID":1,"operationID":1},` +
		`{"version":1,"kind":0,"sequenceID":2,"operationID":1}]}`
	calls := 0
	err := pulumijournal.DecodeEntries(strings.NewReader(body), 1, func(pulumijournal.JournalEntry) error {
		calls++
		return errStop
	})
	if !errors.Is(err, errStop) {
		t.Fatalf("error is %v, want the callback error", err)
	}
	if calls != 1 {
		t.Fatalf("callback ran %d times, want 1", calls)
	}
}
