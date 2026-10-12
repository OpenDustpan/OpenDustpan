package pulumijournal_test

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"reflect"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/pulumijournal"
)

// errStop is the error that the callback returns in TestDecodeEntriesStopsAtCallbackError.
var errStop = errors.New("stop")

// validEntry is one entry that passes every check at negotiated version 2.
const validEntry = `{"version":1,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}`

// decodeAll decodes body with the negotiated version and returns the entries that fn received.
func decodeAll(body string, negotiated int) ([]pulumijournal.JournalEntry, error) {
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
		`{"version":1,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null},` +
		`{"version":1,"kind":2,"sequenceID":2,"operationID":1,"removeOld":null,"removeNew":null}]}`
	got, err := decodeAll(body, 1)
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

// TestDecodeEntriesAcceptsStateMigrationAtVersionTwo checks that kind 10 needs version 2 and is then accepted.
func TestDecodeEntriesAcceptsStateMigrationAtVersionTwo(t *testing.T) {
	t.Parallel()
	body := `{"entries":[{"version":2,"kind":10,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}]}`
	got, err := decodeAll(body, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != pulumijournal.JournalEntryKindStateMigration {
		t.Fatalf("got %+v, want one state migration entry", got)
	}
}

// TestDecodeEntriesRefusesInvalidBatches checks each refusal of ADR 43, decision 5.5, R14 and R17.
func TestDecodeEntriesRefusesInvalidBatches(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		body       string
		negotiated int
		want       error
	}{
		{
			name:       "version zero",
			body:       `{"entries":[{"version":0,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}]}`,
			negotiated: 1,
			want:       pulumijournal.ErrVersion,
		},
		{
			name:       "version above the negotiated one",
			body:       `{"entries":[{"version":2,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}]}`,
			negotiated: 1,
			want:       pulumijournal.ErrVersion,
		},
		{
			name:       "unknown kind",
			body:       `{"entries":[{"version":1,"kind":11,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}]}`,
			negotiated: 1,
			want:       pulumijournal.ErrUnknownKind,
		},
		{
			name:       "negative kind",
			body:       `{"entries":[{"version":1,"kind":-1,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}]}`,
			negotiated: 1,
			want:       pulumijournal.ErrUnknownKind,
		},
		{
			name:       "state migration at version one",
			body:       `{"entries":[{"version":1,"kind":10,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}]}`,
			negotiated: 2,
			want:       pulumijournal.ErrKindVersion,
		},
		{
			name:       "sequence ID zero",
			body:       `{"entries":[{"version":1,"kind":0,"sequenceID":0,"operationID":1,"removeOld":null,"removeNew":null}]}`,
			negotiated: 1,
			want:       pulumijournal.ErrSequenceID,
		},
		{
			name:       "sequence ID negative",
			body:       `{"entries":[{"version":1,"kind":0,"sequenceID":-5,"operationID":1,"removeOld":null,"removeNew":null}]}`,
			negotiated: 1,
			want:       pulumijournal.ErrSequenceID,
		},
		{
			name:       "unknown entry member",
			body:       `{"entries":[{"version":1,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null,"bogus":1}]}`,
			negotiated: 1,
			want:       pulumijournal.ErrUnknownMember,
		},
		{
			name:       "missing kind",
			body:       `{"entries":[{"version":1,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}]}`,
			negotiated: 1,
			want:       pulumijournal.ErrMissingMember,
		},
		{
			name:       "missing removeNew",
			body:       `{"entries":[{"version":1,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null}]}`,
			negotiated: 1,
			want:       pulumijournal.ErrMissingMember,
		},
		{
			name:       "missing entries member",
			body:       `{}`,
			negotiated: 1,
			want:       pulumijournal.ErrBatchShape,
		},
		{
			name:       "capitalized entries member",
			body:       `{"Entries":[` + validEntry + `]}`,
			negotiated: 1,
			want:       pulumijournal.ErrBatchShape,
		},
		{
			name:       "unknown batch member",
			body:       `{"other":1,"entries":[` + validEntry + `]}`,
			negotiated: 1,
			want:       pulumijournal.ErrBatchShape,
		},
		{
			name:       "repeated entries member",
			body:       `{"entries":[],"entries":[` + validEntry + `]}`,
			negotiated: 1,
			want:       pulumijournal.ErrBatchShape,
		},
		{
			name:       "data after the closing brace",
			body:       `{"entries":[` + validEntry + `]}x`,
			negotiated: 1,
			want:       pulumijournal.ErrBatchShape,
		},
		{
			name:       "second object after the batch",
			body:       `{"entries":[` + validEntry + `]}{"entries":[]}`,
			negotiated: 1,
			want:       pulumijournal.ErrBatchShape,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeAll(tt.body, tt.negotiated)
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
		_, err := decodeAll(body, 1)
		if !errors.Is(err, pulumijournal.ErrBatchShape) {
			t.Fatalf("body %s: error is %v, want ErrBatchShape", body, err)
		}
	}
}

// TestDecodeEntriesStopsAtCallbackError checks that an error from fn stops the decoding and is returned.
func TestDecodeEntriesStopsAtCallbackError(t *testing.T) {
	t.Parallel()
	body := `{"entries":[{"version":1,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null},` +
		`{"version":1,"kind":0,"sequenceID":2,"operationID":1,"removeOld":null,"removeNew":null}]}`
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

// FuzzDecodeEntries checks that no body makes DecodeEntries panic. The seeds
// cover each refusal and one valid batch.
func FuzzDecodeEntries(f *testing.F) {
	seeds := []string{
		`{"entries":[` + validEntry + `]}`,
		`{"entries":[]}`,
		`{}`,
		`[]`,
		`{"Entries":[` + validEntry + `]}`,
		`{"entries":[{"version":0,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}]}`,
		`{"entries":[{"version":3,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}]}`,
		`{"entries":[{"version":1,"kind":11,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}]}`,
		`{"entries":[{"version":1,"kind":10,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}]}`,
		`{"entries":[{"version":1,"kind":0,"sequenceID":0,"operationID":1,"removeOld":null,"removeNew":null}]}`,
		`{"entries":[{"version":1,"kind":0,"sequenceID":-5,"operationID":1,"removeOld":null,"removeNew":null}]}`,
		`{"entries":[{"version":1,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null,"bogus":1}]}`,
		`{"entries":[{"version":1,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}]}`,
		`{"entries":[` + validEntry + `]}x`,
		`{"entries":[` + validEntry + `]}{"entries":[]}`,
	}
	for _, seed := range seeds {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		err := pulumijournal.DecodeEntries(bytes.NewReader(body), 2, func(pulumijournal.JournalEntry) error {
			return nil
		})
		if err != nil {
			t.Logf("refused: %v", err)
		}
	})
}

// TestGeneratedTypesRoundTrip checks that each generated type encodes and decodes to the same value.
func TestGeneratedTypesRoundTrip(t *testing.T) {
	t.Parallel()
	var layout pulumijournal.JournalLayoutItem
	index := int64(3)
	layout.BaseIndex = &index

	var basePatch pulumijournal.JournalBaseStatePatch
	basePatch.Index = 4
	basePatch.State = jsontext.Value(`{"urn":"urn:pulumi:stack::project::type::name"}`)

	var newPatch pulumijournal.JournalNewStatePatch
	newPatch.OperationID = 5
	newPatch.State = jsontext.Value(`{"urn":"urn:pulumi:stack::project::type::other"}`)

	var entry pulumijournal.JournalEntry
	entry.Version = 2
	entry.Kind = pulumijournal.JournalEntryKindStateMigration
	entry.SequenceID = 6
	entry.OperationID = 7
	entry.State = jsontext.Value(`{"urn":"urn:pulumi:stack::project::type::name"}`)
	entry.Layout = []pulumijournal.JournalLayoutItem{layout}
	entry.BaseStatePatches = []pulumijournal.JournalBaseStatePatch{basePatch}
	entry.NewStatePatches = []pulumijournal.JournalNewStatePatch{newPatch}

	var entries pulumijournal.JournalEntries
	entries.Entries = []pulumijournal.JournalEntry{entry}

	tests := []struct {
		name  string
		value any
		empty func() any
	}{
		{
			name:  "JournalEntryKind",
			value: pulumijournal.JournalEntryKindWrite,
			empty: func() any { return new(pulumijournal.JournalEntryKind) },
		},
		{name: "JournalLayoutItem", value: layout, empty: func() any { return new(pulumijournal.JournalLayoutItem) }},
		{
			name:  "JournalBaseStatePatch",
			value: basePatch,
			empty: func() any { return new(pulumijournal.JournalBaseStatePatch) },
		},
		{
			name:  "JournalNewStatePatch",
			value: newPatch,
			empty: func() any { return new(pulumijournal.JournalNewStatePatch) },
		},
		{name: "JournalEntry", value: entry, empty: func() any { return new(pulumijournal.JournalEntry) }},
		{name: "JournalEntries", value: entries, empty: func() any { return new(pulumijournal.JournalEntries) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			encoded, err := json.Marshal(tt.value)
			if err != nil {
				t.Fatal(err)
			}
			decoded := tt.empty()
			if err := json.Unmarshal(encoded, decoded); err != nil {
				t.Fatal(err)
			}
			got := reflect.ValueOf(decoded).Elem().Interface()
			if !reflect.DeepEqual(got, tt.value) {
				t.Fatalf("round trip changed the value: got %+v, want %+v", got, tt.value)
			}
		})
	}
}
