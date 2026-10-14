package audit_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
)

// changesFixture answers three settings out of key order: a sensitive
// value, a value with an old value, and a plain value.
func changesFixture() []audit.Change {
	old := "info"

	return []audit.Change{
		{Key: "region", Old: nil, New: "eu-west", Layer: "file", TakesEffect: ""},
		{Key: "log-level", Old: &old, New: "debug", Layer: "file", TakesEffect: ""},
		{Key: "database", Old: nil, New: "set", Layer: "env", TakesEffect: ""},
	}
}

// canonicalFixture is the canonical JSON of changesFixture.
const canonicalFixture = `[{"key":"database","new":"set","layer":"env"},` +
	`{"key":"log-level","old":"info","new":"debug","layer":"file"},` +
	`{"key":"region","new":"eu-west","layer":"file"}]`

func TestEncodeChangesIsCanonical(t *testing.T) {
	t.Parallel()

	settings := changesFixture()

	if got := audit.EncodeChanges(settings); got != canonicalFixture {
		t.Fatalf("EncodeChanges = %s, want %s", got, canonicalFixture)
	}

	reversed := []audit.Change{settings[2], settings[0], settings[1]}
	if got := audit.EncodeChanges(reversed); got != canonicalFixture {
		t.Fatalf("EncodeChanges of another order = %s, want %s", got, canonicalFixture)
	}

	if settings[0].Key != "region" {
		t.Fatal("EncodeChanges sorted the slice of the caller")
	}

	if got := audit.EncodeChanges(nil); got != "" {
		t.Fatalf("EncodeChanges(nil) = %q, want empty", got)
	}
}

func TestDecodeChangesIsLenient(t *testing.T) {
	t.Parallel()

	decoded, err := audit.DecodeChanges(canonicalFixture)
	if err != nil {
		t.Fatalf("DecodeChanges: %v", err)
	}

	if got := audit.EncodeChanges(decoded); got != canonicalFixture {
		t.Fatalf("round trip = %s, want %s", got, canonicalFixture)
	}

	if got, err := audit.DecodeChanges(""); err != nil || got != nil {
		t.Fatalf("DecodeChanges(\"\") = %v, %v; want nil, nil", got, err)
	}

	reordered := `[{"key":"region","new":"eu-west","layer":"file"},{"key":"database","new":"set","layer":"env"}]`
	if got, err := audit.DecodeChanges(reordered); err != nil || len(got) != 2 {
		t.Fatalf("DecodeChanges of another order = %v, %v; want 2 changes", got, err)
	}

	if _, err := audit.DecodeChanges(`{`); err == nil {
		t.Fatal("DecodeChanges accepted a text that is not JSON")
	}
}

// TestComputeStoredHashHashesTheStoredText proves the stored hash covers the
// changes text as the store holds it: the canonical text gives the hash of
// ComputeHash, and the same changes in another order give another hash.
func TestComputeStoredHashHashesTheStoredText(t *testing.T) {
	t.Parallel()

	e := testEntry()
	e.Changes = changesFixture()

	want := audit.ComputeHash(e, "")

	if got := audit.ComputeStoredHash(e, "", canonicalFixture); got != want {
		t.Fatalf("stored hash of the canonical text = %s, want %s", got, want)
	}

	reordered := `[{"key":"region","new":"eu-west","layer":"file"},` +
		`{"key":"database","new":"set","layer":"env"},` +
		`{"key":"log-level","old":"info","new":"debug","layer":"file"}]`
	if got := audit.ComputeStoredHash(e, "", reordered); got == want {
		t.Fatal("the same changes in another order gave the same stored hash")
	}
}

// TestComputeHashCoversTheChanges pins the hash of a fixed entry with
// changes, alone and after the value count, in the fixed order of the
// optional fields. A script outside Go computed both values from the
// documented encoding.
func TestComputeHashCoversTheChanges(t *testing.T) {
	t.Parallel()

	const (
		wantChanges      = "c58e34fc5789a51597c6101c5f02421cbc2a8619f0a72278f7971fb1f66bc5aa"
		wantCountChanges = "c298dfac4f3c44132113655517f0651cc579c3a243b28590b07e75c3fa60647a"
	)

	with := testEntry()
	with.Changes = changesFixture()

	if got := audit.ComputeHash(with, ""); got != wantChanges {
		t.Fatalf("hash with changes = %s, want %s", got, wantChanges)
	}

	count := int64(2)
	with.ValueCount = &count

	if got := audit.ComputeHash(with, ""); got != wantCountChanges {
		t.Fatalf("hash with a count and changes = %s, want %s", got, wantCountChanges)
	}

	empty := testEntry()
	empty.Changes = []audit.Change{}

	if got := audit.ComputeHash(empty, ""); got != audit.ComputeHash(testEntry(), "") {
		t.Fatal("an empty changes list changed the hash")
	}
}

func TestValidateChecksTheChanges(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		settings []audit.Change
	}{
		{"no key", []audit.Change{{Key: "", Old: nil, New: "x", Layer: "file", TakesEffect: ""}}},
		{"key two times", []audit.Change{
			{Key: "region", Old: nil, New: "a", Layer: "file", TakesEffect: ""},
			{Key: "region", Old: nil, New: "b", Layer: "env", TakesEffect: ""},
		}},
		{"invalid UTF-8", []audit.Change{{Key: "region", Old: nil, New: "\xff", Layer: "file", TakesEffect: ""}}},
		{"too many", func() []audit.Change {
			out := make([]audit.Change, 0, audit.MaxChanges+1)
			for i := range audit.MaxChanges + 1 {
				out = append(
					out,
					audit.Change{
						Key:         string(rune('a'+i%26)) + string(rune('0'+i)),
						Layer:       "",
						New:         "",
						Old:         nil,
						TakesEffect: "",
					},
				)
			}

			return out
		}()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := testEntry()
			e.Changes = tc.settings

			if err := e.Validate(); !errors.Is(err, audit.ErrInvalidEntry) {
				t.Fatalf("Validate = %v, want %v", err, audit.ErrInvalidEntry)
			}
		})
	}

	ok := testEntry()
	ok.Changes = changesFixture()
	ok.Channel = audit.ChannelStartup

	if err := ok.Validate(); err != nil {
		t.Fatalf("Validate of a start entry: %v", err)
	}

	ok.Channel = audit.ChannelConfigFile

	if err := ok.Validate(); err != nil {
		t.Fatalf("Validate of a config-file entry: %v", err)
	}
}
