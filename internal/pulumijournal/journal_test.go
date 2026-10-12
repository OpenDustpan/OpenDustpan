package pulumijournal_test

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"testing"

	"opendustpan.org/opendustpan/internal/pulumijournal"
)

// TestJournalEntryStateRoundTrip decodes an entry and checks that State keeps the raw object bytes.
func TestJournalEntryStateRoundTrip(t *testing.T) {
	t.Parallel()
	const state = `{"urn":"urn:pulumi:stack::project::type::name","id":"abc123"}`
	raw := []byte(
		`{"version":1,"kind":2,"sequenceID":7,"operationID":9,"state":` + state + `}`,
	)
	var entry pulumijournal.JournalEntry
	if err := json.UnmarshalDecode(jsontext.NewDecoder(bytes.NewReader(raw)), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Kind != pulumijournal.JournalEntryKindFailure {
		t.Fatalf("Kind is %v, want failure", entry.Kind)
	}
	if entry.SequenceID != 7 {
		t.Fatalf("SequenceID is %d, want 7", entry.SequenceID)
	}
	if entry.OperationID != 9 {
		t.Fatalf("OperationID is %d, want 9", entry.OperationID)
	}
	if !entry.State.IsValid() {
		t.Fatal("State is not valid JSON")
	}
	if string(entry.State) != state {
		t.Fatalf("State is %s, want %s", string(entry.State), state)
	}
}

// TestJournalEntryMissingState checks that a missing state leaves State nil.
func TestJournalEntryMissingState(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"version":1,"kind":1,"sequenceID":1,"operationID":2}`)
	var entry pulumijournal.JournalEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatal(err)
	}
	if entry.State != nil {
		t.Fatalf("State is %s, want nil", string(entry.State))
	}
}
