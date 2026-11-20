package audit_test

import (
	"errors"
	"strings"
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

// TestComputeHashCoversTheChanges proves that the hash covers the canonical
// JSON of the changes, and that an empty changes list hashes as no changes.
func TestComputeHashCoversTheChanges(t *testing.T) {
	t.Parallel()

	with := testEntry()
	with.Changes = changesFixture()

	got := audit.ComputeHash(with, "")
	if want := encodedHash("", hashTail{"-", "", canonicalFixture, "0", "", "", ""}); got != want {
		t.Fatalf("hash with changes = %s, want %s", got, want)
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
		{"key with CR LF", changeOf("tags.evil\r\nInjected: yes", "set", nil, "", "")},
		{"key with hidden rune", changeOf("tags.a"+string(rune(0x200b))+"b", "set", nil, "", "")},
		{"key too long", changeOf(strings.Repeat("k", audit.MaxChangeKeyLen+1), "set", nil, "", "")},
		{"new with hidden", changeOf("region", "a\rb", nil, "", "")},
		{"new with zero width", changeOf("region", "a"+string(rune(0x200b)), nil, "", "")},
		{"new too long", changeOf("region", strings.Repeat("v", audit.MaxChangeValueLen+1), nil, "", "")},
		{"old with hidden", changeOf("region", "x", new("a"+string(rune(0x202e))+"b"), "", "")},
		{"layer with line break", changeOf("region", "x", nil, "f\nile", "")},
		{"takes effect with hidden", changeOf("region", "x", nil, "", "n"+string(rune(0x2028))+"ext")},
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

	list := testEntry()
	list.Changes = changeOf("tokenManagers", "a: x\nb:\ty", new("c: y"), "", "")

	if err := list.Validate(); err != nil {
		t.Fatalf("Validate of a value with one item on each line: %v", err)
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

// changeOf answers a list of one change with all its fields.
func changeOf(key, newValue string, old *string, layer, takesEffect string) []audit.Change {
	return []audit.Change{{Key: key, Old: old, New: newValue, Layer: layer, TakesEffect: takesEffect}}
}
