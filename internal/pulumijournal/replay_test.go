package pulumijournal_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/pkg/stack"
)

// The compile-time check fails the build when the replayer port drifts from stack.JournalReplayer.
var _ stack.JournalReplayer = (*pulumijournal.Replayer)(nil)

// cliVersion and replayTime fix the manifest that the golden tests compare.
const (
	cliVersion = "3.268.0"
	replayTime = "2026-10-12T12:00:00Z"
)

// resourceJSON returns a resource state with the URN and no links.
func resourceJSON(urn string) string {
	return `{"urn":"` + urn + `","custom":true,"type":"t:mod:T","id":"x"}`
}

// baseJSON returns a base deployment with the given resource states.
func baseJSON(resources ...string) string {
	return `{"manifest":{"time":"2026-01-01T00:00:00Z","magic":"","version":""},"resources":[` +
		strings.Join(resources, ",") + `]}`
}

// batch wraps entries in a batch body.
func batch(entries ...string) string {
	return `{"entries":[` + strings.Join(entries, ",") + `]}`
}

// replay runs the replayer on base and entries with the fixed clock and version.
func replay(base, entries string) (string, error) {
	now := func() time.Time {
		return time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC)
	}
	open := func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(base)), nil
	}
	var out bytes.Buffer
	err := pulumijournal.NewReplayer(now, cliVersion).
		Replay(context.Background(), open, [][]byte{[]byte(entries)}, &out)
	return out.String(), err
}

// decodeAny decodes JSON to a generic value, so that two documents compare by content.
func decodeAny(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decode %s: %v", s, err)
	}
	return v
}

// magic is the manifest magic of cliVersion.
func magic() string {
	sum := sha256.Sum256([]byte(cliVersion))
	return hex.EncodeToString(sum[:])
}

// expectDeployment checks the output against the expected deployment resources and version.
func expectDeployment(t *testing.T, got, resources string) {
	t.Helper()
	want := `{"version":3,"deployment":{"manifest":{"time":"` + replayTime +
		`","magic":"` + magic() + `","version":"` + cliVersion + `"},"resources":[` + resources + `]}}`
	if !reflect.DeepEqual(decodeAny(t, got), decodeAny(t, want)) {
		t.Fatalf("output is\n%s\nwant\n%s", got, want)
	}
}

// TestReplaySuccessAddsAndDeletes checks a new resource and a delete of a base resource.
func TestReplaySuccessAddsAndDeletes(t *testing.T) {
	t.Parallel()
	base := baseJSON(resourceJSON("urn:a"), resourceJSON("urn:b"))
	entries := batch(
		`{"version":1,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}`,
		`{"version":1,"kind":1,"sequenceID":2,"operationID":1,"removeOld":null,"removeNew":null,`+
			`"state":`+resourceJSON("urn:c")+`,"deleteOld":1}`,
	)
	got, err := replay(base, entries)
	if err != nil {
		t.Fatal(err)
	}
	expectDeployment(t, got, resourceJSON("urn:c")+","+resourceJSON("urn:a")+
		`,{"urn":"urn:b","custom":true,"type":"t:mod:T","id":"x","delete":true}`)
}

// TestReplayKeepsUnchangedStatesCompact checks that a state no entry changes is
// written with the same members, in compact form. The JSON encoder removes
// whitespace from every value it writes, so the bytes differ from the input only
// by whitespace.
func TestReplayKeepsUnchangedStatesCompact(t *testing.T) {
	t.Parallel()
	base := baseJSON(`{"urn":"urn:a",   "custom":true,"type":"t:mod:T"}`)
	got, err := replay(base, batch())
	if err != nil {
		t.Fatal(err)
	}
	const compact = `{"urn":"urn:a","custom":true,"type":"t:mod:T"}`
	if !strings.Contains(got, compact) {
		t.Fatalf("output does not hold the state in compact form:\n%s", got)
	}
	if !jsontext.Value(got).IsValid() {
		t.Fatalf("output is not valid JSON:\n%s", got)
	}
}

// TestReplayRefreshRebuildsLinks checks that a refresh prunes links to deleted resources.
func TestReplayRefreshRebuildsLinks(t *testing.T) {
	t.Parallel()
	b := `{"urn":"urn:b","custom":true,"type":"t:mod:T","parent":"urn:gone","deletedWith":"urn:gone",` +
		`"dependencies":["urn:a","urn:gone"]}`
	base := baseJSON(resourceJSON("urn:a"), b)
	entries := batch(
		`{"version":1,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}`,
		`{"version":1,"kind":3,"sequenceID":2,"operationID":1,"removeOld":0,"removeNew":null}`,
	)
	got, err := replay(base, entries)
	if err != nil {
		t.Fatal(err)
	}
	expectDeployment(t, got, `{"urn":"urn:b","custom":true,"type":"t:mod:T"}`)
}

// TestReplayRefusesMissingReference checks that a reference to an operation no entry created is an error.
func TestReplayRefusesMissingReference(t *testing.T) {
	t.Parallel()
	entries := batch(
		`{"version":1,"kind":1,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null,` +
			`"state":` + resourceJSON("urn:c") + `,"deleteNew":7}`,
	)
	_, err := replay(baseJSON(), entries)
	if !errors.Is(err, pulumijournal.ErrMissingReference) {
		t.Fatalf("error is %v, want ErrMissingReference", err)
	}
}

// migrationBase is a base deployment with a stack resource and one managed
// resource for the state migration tests.
const migrationBase = `{"manifest":{"time":"2026-01-01T00:00:00Z","magic":"","version":""},"resources":[` +
	`{"urn":"urn:pulumi:dev::corpus::pulumi:pulumi:Stack::corpus","custom":false,"type":"pulumi:pulumi:Stack"},` +
	`{"urn":"urn:pulumi:dev::corpus::test:resource::a","custom":true,"type":"test:resource","id":"a"}]}`

// migrationInserted is the resource state that the migration tests insert.
const migrationInserted = `{"urn":"urn:pulumi:dev::corpus::test:resource::b","custom":true,"type":"test:resource","id":"b"}`

// migrationInsertedURN is the URN of the inserted resource, so the refusal
// cases can vary its other members without repeating it.
const migrationInsertedURN = `urn:pulumi:dev::corpus::test:resource::b`

// insertedVariant returns an inserted resource state with the members of the
// inserted resource and the extra member.
func insertedVariant(extra string) string {
	return `{"urn":"` + migrationInsertedURN +
		`","custom":true,"type":"test:resource","id":"b"` + extra + `}`
}

// migrationEntry returns a state migration entry with the layout and states
// members, and no patches.
func migrationEntry(seq int, layout, states string) string {
	return `{"version":2,"kind":10,"sequenceID":` + strconv.Itoa(seq) +
		`,"operationID":0,"removeOld":null,"removeNew":null,"layout":` + layout +
		`,"states":` + states + `}`
}

// TestReplayAppliesStateMigration checks that a kind 10 entry replaces the
// base with its layout: the retained stack stays and the inserted resource
// takes the place of the dropped one.
func TestReplayAppliesStateMigration(t *testing.T) {
	t.Parallel()
	entries := batch(migrationEntry(
		1,
		`[{"baseIndex":0},{"stateIndex":0}]`,
		`[`+migrationInserted+`]`,
	))
	got, err := replay(migrationBase, entries)
	if err != nil {
		t.Fatal(err)
	}
	expectDeployment(t, got,
		`{"urn":"urn:pulumi:dev::corpus::pulumi:pulumi:Stack::corpus","custom":false,"type":"pulumi:pulumi:Stack"}`+
			`,`+migrationInserted)
}

// TestReplayAppliesStateMigrationPatches checks that a kind 10 entry applies
// its base and new-state patches, and moves the markings of the retained base
// resources to their new positions.
func TestReplayAppliesStateMigrationPatches(t *testing.T) {
	t.Parallel()
	base := baseJSON(
		`{"urn":"urn:pulumi:dev::corpus::pulumi:pulumi:Stack::corpus","custom":false,"type":"pulumi:pulumi:Stack"}`,
		resourceJSON("urn:old-a"),
		resourceJSON("urn:keep"),
	)
	patched := `{"urn":"urn:keep","custom":true,"type":"t:mod:T","id":"x","inputs":{"v":"new"}}`
	replacement := `{"urn":"urn:pulumi:dev::corpus::test:resource::new","custom":true,"type":"test:resource","id":"n"}`
	entries := batch(
		`{"version":1,"kind":1,"sequenceID":1,"operationID":7,"removeOld":null,"removeNew":null,`+
			`"state":`+resourceJSON("urn:early")+`}`,
		`{"version":1,"kind":1,"sequenceID":2,"operationID":8,"removeOld":null,"removeNew":null,`+
			`"state":`+resourceJSON("urn:other")+`,"deleteOld":1}`,
		`{"version":2,"kind":10,"sequenceID":3,"operationID":0,"removeOld":null,"removeNew":null,`+
			`"layout":[{"baseIndex":0},{"stateIndex":0},{"baseIndex":2}],`+
			`"states":[`+replacement+`],`+
			`"baseStatePatches":[{"index":2,"state":`+patched+`}],`+
			`"newStatePatches":[{"operationID":7,"state":`+resourceJSON("urn:early-patched")+`}]}`,
	)
	got, err := replay(base, entries)
	if err != nil {
		t.Fatal(err)
	}
	// The delete marking of the dropped base index 1 is gone with it. The new
	// resources come first, then the migrated base in layout order.
	expectDeployment(t, got,
		resourceJSON("urn:early-patched")+","+resourceJSON("urn:other")+","+
			`{"urn":"urn:pulumi:dev::corpus::pulumi:pulumi:Stack::corpus","custom":false,"type":"pulumi:pulumi:Stack"}`+
			","+replacement+","+patched)
}

// TestReplayRefusesStateMigrationAtVersionOne checks that a kind 10 entry with
// version 1 is refused with ErrKindVersion, before the replay runs.
func TestReplayRefusesStateMigrationAtVersionOne(t *testing.T) {
	t.Parallel()
	entry := `{"version":1,"kind":10,"sequenceID":1,"operationID":0,"removeOld":null,"removeNew":null}`
	_, err := replay(baseJSON(), batch(entry))
	if !errors.Is(err, pulumijournal.ErrKindVersion) {
		t.Fatalf("error is %v, want ErrKindVersion", err)
	}
}

// TestReplayRefusesBadStateMigrations checks that a kind 10 entry that does
// not apply to the checkpoint built so far is refused.
func TestReplayRefusesBadStateMigrations(t *testing.T) {
	t.Parallel()
	goodLayout := `[{"baseIndex":0},{"stateIndex":0}]`
	goodStates := `[` + migrationInserted + `]`
	success := func(seq, op int64) string {
		return `{"version":1,"kind":1,"sequenceID":` + strconv.FormatInt(seq, 10) +
			`,"operationID":` + strconv.FormatInt(op, 10) +
			`,"removeOld":null,"removeNew":null,"state":` + resourceJSON("urn:early") + `}`
	}
	tests := []struct {
		name    string
		base    string
		entries string
		want    error
	}{
		{
			name:    "pending base operations",
			base:    `{"manifest":{},"resources":[],"pending_operations":[{"type":"creating"}]}`,
			entries: batch(migrationEntry(1, goodLayout, goodStates)),
			want:    pulumijournal.ErrStateMigration,
		},
		{
			name: "incomplete operation",
			base: baseJSON(),
			entries: batch(
				`{"version":1,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null,`+
					`"operation":{"resource":`+resourceJSON("urn:op")+`}}`,
				migrationEntry(2, goodLayout, goodStates),
			),
			want: pulumijournal.ErrStateMigration,
		},
		{
			name:    "no inserted states",
			base:    migrationBase,
			entries: batch(migrationEntry(1, `[{"baseIndex":0}]`, `[]`)),
			want:    pulumijournal.ErrStateMigration,
		},
		{
			name:    "layout names both",
			base:    migrationBase,
			entries: batch(migrationEntry(1, `[{"baseIndex":0,"stateIndex":0}]`, goodStates)),
			want:    pulumijournal.ErrStateMigration,
		},
		{
			name:    "layout names neither",
			base:    migrationBase,
			entries: batch(migrationEntry(1, `[{}]`, goodStates)),
			want:    pulumijournal.ErrStateMigration,
		},
		{
			name:    "layout base index outside the base",
			base:    migrationBase,
			entries: batch(migrationEntry(1, `[{"baseIndex":7},{"stateIndex":0}]`, goodStates)),
			want:    pulumijournal.ErrStateMigration,
		},
		{
			name:    "layout places base twice",
			base:    migrationBase,
			entries: batch(migrationEntry(1, `[{"baseIndex":0},{"baseIndex":0},{"stateIndex":0}]`, goodStates)),
			want:    pulumijournal.ErrStateMigration,
		},
		{
			name:    "layout misses an inserted state",
			base:    migrationBase,
			entries: batch(migrationEntry(1, `[{"baseIndex":0}]`, goodStates)),
			want:    pulumijournal.ErrStateMigration,
		},
		{
			name: "base patch outside the base",
			base: migrationBase,
			entries: batch(`{"version":2,"kind":10,"sequenceID":1,"operationID":0,"removeOld":null,"removeNew":null,` +
				`"layout":` + goodLayout + `,"states":` + goodStates +
				`,"baseStatePatches":[{"index":7,"state":` + migrationInserted + `}]}`),
			want: pulumijournal.ErrStateMigration,
		},
		{
			name: "base patch of a dropped resource",
			base: migrationBase,
			entries: batch(`{"version":2,"kind":10,"sequenceID":1,"operationID":0,"removeOld":null,"removeNew":null,` +
				`"layout":` + goodLayout + `,"states":` + goodStates +
				`,"baseStatePatches":[{"index":1,"state":` + migrationInserted + `}]}`),
			want: pulumijournal.ErrStateMigration,
		},
		{
			name: "new-state patch of an unknown operation",
			base: migrationBase,
			entries: batch(`{"version":2,"kind":10,"sequenceID":1,"operationID":0,"removeOld":null,"removeNew":null,` +
				`"layout":` + goodLayout + `,"states":` + goodStates +
				`,"newStatePatches":[{"operationID":9,"state":` + migrationInserted + `}]}`),
			want: pulumijournal.ErrMissingReference,
		},
		{
			name: "new-state patch of a removed operation",
			base: migrationBase,
			entries: batch(
				success(1, 5),
				`{"version":1,"kind":1,"sequenceID":2,"operationID":6,"removeOld":null,"removeNew":5,`+
					`"state":`+resourceJSON("urn:next")+`}`,
				`{"version":2,"kind":10,"sequenceID":3,"operationID":0,"removeOld":null,"removeNew":null,`+
					`"layout":`+goodLayout+`,"states":`+goodStates+
					`,"newStatePatches":[{"operationID":5,"state":`+migrationInserted+`}]}`,
			),
			want: pulumijournal.ErrMissingReference,
		},
		{
			name:    "inserted state with an invalid URN",
			base:    migrationBase,
			entries: batch(migrationEntry(1, goodLayout, `[{"urn":"not-a-urn","custom":false,"type":"t"}]`)),
			want:    pulumijournal.ErrInvalidURN,
		},
		{
			name: "inserted state with a mismatched type",
			base: migrationBase,
			entries: batch(migrationEntry(1, goodLayout,
				`[{"urn":"`+migrationInsertedURN+`","custom":true,"type":"other:resource","id":"b"}]`)),
			want: pulumijournal.ErrStateMigration,
		},
		{
			name: "inserted state marked for deletion",
			base: migrationBase,
			entries: batch(migrationEntry(1, goodLayout,
				`[`+insertedVariant(`,"delete":true`)+`]`)),
			want: pulumijournal.ErrStateMigration,
		},
		{
			name: "inserted state that is a view",
			base: migrationBase,
			entries: batch(migrationEntry(1, goodLayout,
				`[`+insertedVariant(`,"viewOf":"urn:x"`)+`]`)),
			want: pulumijournal.ErrStateMigration,
		},
		{
			name: "inserted custom state without an ID",
			base: migrationBase,
			entries: batch(migrationEntry(1, goodLayout,
				`[{"urn":"`+migrationInsertedURN+`","custom":true,"type":"test:resource"}]`)),
			want: pulumijournal.ErrStateMigration,
		},
		{
			name: "inserted state with an unknown extension",
			base: migrationBase,
			entries: batch(migrationEntry(1, goodLayout,
				`[`+insertedVariant(`,"extensionRef":"ext-x"`)+`]`)),
			want: pulumijournal.ErrStateMigration,
		},
		{
			name: "inserted states with a repeated URN",
			base: migrationBase,
			entries: batch(migrationEntry(1,
				`[{"baseIndex":0},{"stateIndex":0},{"stateIndex":1}]`,
				`[`+migrationInserted+`,`+migrationInserted+`]`,
			)),
			want: pulumijournal.ErrStateMigration,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := replay(tt.base, tt.entries)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error is %v, want %v", err, tt.want)
			}
		})
	}
}

// TestReplayWriteDropsSecretsSetBeforeIt checks that a secrets manager entry
// before a write (kind 5) does not survive it: the snapshot replaces the base,
// and the base holds the secrets provider of the snapshot. Upstream loses such an
// entry the same way (backend/journal.go, lines 295 to 298).
func TestReplayWriteDropsSecretsSetBeforeIt(t *testing.T) {
	t.Parallel()
	base := baseJSON()
	snapshot := `{"manifest":{"time":"2026-01-01T00:00:00Z","magic":"","version":""},` +
		`"secrets_providers":{"type":"service","state":{"stack":"snapshot"}},"resources":[` +
		resourceJSON("urn:z") + `]}`
	entries := batch(
		`{"version":1,"kind":6,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null,`+
			`"secretsProvider":{"type":"service","state":{"stack":"entry"}}}`,
		`{"version":1,"kind":5,"sequenceID":2,"operationID":2,"removeOld":null,"removeNew":null,`+
			`"newSnapshot":`+snapshot+`}`,
	)
	got, err := replay(base, entries)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, `"stack":"entry"`) {
		t.Fatalf("the secrets entry before the write survived it:\n%s", got)
	}
	if !strings.Contains(got, `"stack":"snapshot"`) {
		t.Fatalf("the snapshot's secrets provider is missing:\n%s", got)
	}
}

// TestReplayWriteWithoutSnapshotIsRefused checks that a write entry without a
// snapshot is refused with ErrNoSnapshot.
func TestReplayWriteWithoutSnapshotIsRefused(t *testing.T) {
	t.Parallel()
	entry := `{"version":1,"kind":5,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}`
	_, err := replay(baseJSON(), batch(entry))
	if !errors.Is(err, pulumijournal.ErrNoSnapshot) {
		t.Fatalf("error is %v, want ErrNoSnapshot", err)
	}
}

// TestReplayRebuildsBaseAtRebuiltEntry checks that a rebuilt base state entry
// (kind 7) makes the deployment of the replay so far the base. The entries after
// it apply to that base, and the entries before it do not.
func TestReplayRebuildsBaseAtRebuiltEntry(t *testing.T) {
	t.Parallel()
	base := baseJSON(resourceJSON("urn:a"), resourceJSON("urn:b"))
	entries := batch(
		`{"version":1,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}`,
		`{"version":1,"kind":1,"sequenceID":2,"operationID":1,"removeOld":null,"removeNew":null,`+
			`"state":`+resourceJSON("urn:c")+`,"deleteOld":1}`,
		`{"version":1,"kind":7,"sequenceID":3,"operationID":3,"removeOld":null,"removeNew":null}`,
		`{"version":1,"kind":0,"sequenceID":4,"operationID":2,"removeOld":null,"removeNew":null}`,
		`{"version":1,"kind":1,"sequenceID":5,"operationID":2,"removeOld":null,"removeNew":null,`+
			`"state":`+resourceJSON("urn:d")+`}`,
	)
	got, err := replay(base, entries)
	if err != nil {
		t.Fatal(err)
	}
	expectDeployment(t, got, resourceJSON("urn:d")+","+resourceJSON("urn:c")+","+resourceJSON("urn:a")+
		`,{"urn":"urn:b","custom":true,"type":"t:mod:T","id":"x","delete":true}`)
}

// TestReplayReplacesAndSetsFeatures checks a replaced base state in its place, and the feature that it sets.
func TestReplayReplacesAndSetsFeatures(t *testing.T) {
	t.Parallel()
	replaced := `{"urn":"urn:b","custom":true,"type":"t:mod:T","taint":true}`
	base := baseJSON(resourceJSON("urn:a"), resourceJSON("urn:b"))
	entries := batch(
		`{"version":1,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}`,
		`{"version":1,"kind":3,"sequenceID":2,"operationID":1,"removeOld":1,"removeNew":null,"state":`+replaced+`}`,
	)
	got, err := replay(base, entries)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"version":4,"features":["taint"],"deployment":{"manifest":{"time":"` + replayTime +
		`","magic":"` + magic() + `","version":"` + cliVersion + `"},"resources":[` +
		resourceJSON("urn:a") + `,` + replaced + `]}}`
	if !reflect.DeepEqual(decodeAny(t, got), decodeAny(t, want)) {
		t.Fatalf("output is\n%s\nwant\n%s", got, want)
	}
}

// TestReplayRebuildsEachLinkField checks that the rebuild prunes each link the
// upstream rebuild prunes: propertyDependencies and replaceWith as well as the
// members that TestReplayRefreshRebuildsLinks covers.
func TestReplayRebuildsEachLinkField(t *testing.T) {
	t.Parallel()
	b := `{"urn":"urn:b","custom":true,"type":"t:mod:T",` +
		`"propertyDependencies":{"p":["urn:a","urn:gone"]},"replaceWith":["urn:gone","urn:a"]}`
	base := baseJSON(resourceJSON("urn:a"), b)
	entries := batch(
		`{"version":1,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}`,
		`{"version":1,"kind":3,"sequenceID":2,"operationID":1,"removeOld":0,"removeNew":null}`,
	)
	got, err := replay(base, entries)
	if err != nil {
		t.Fatal(err)
	}
	// The rebuild leaves no link, so the members are omitted. The replaceWith
	// feature stays: upstream reads the features before the rebuild.
	want := `{"version":4,"features":["replaceWith"],"deployment":{"manifest":{"time":"` + replayTime +
		`","magic":"` + magic() + `","version":"` + cliVersion + `"},"resources":[` +
		`{"urn":"urn:b","custom":true,"type":"t:mod:T"}]}}`
	if !reflect.DeepEqual(decodeAny(t, got), decodeAny(t, want)) {
		t.Fatalf("output is\n%s\nwant\n%s", got, want)
	}
}

// TestReplayWritesNewResourcesBeforeBase checks the order of the output with a
// new resource, a base resource that is replaced, one that is deleted, and one
// that is marked pending replacement.
func TestReplayWritesNewResourcesBeforeBase(t *testing.T) {
	t.Parallel()
	base := baseJSON(resourceJSON("urn:a"), resourceJSON("urn:b"), resourceJSON("urn:c"))
	replaced := `{"urn":"urn:b2","custom":true,"type":"t:mod:T"}`
	entries := batch(
		`{"version":1,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}`,
		`{"version":1,"kind":1,"sequenceID":2,"operationID":1,"removeOld":null,"removeNew":null,`+
			`"state":`+resourceJSON("urn:n")+`,"pendingReplacementOld":2}`,
		`{"version":1,"kind":3,"sequenceID":3,"operationID":2,"removeOld":1,"removeNew":null,"state":`+replaced+`}`,
		`{"version":1,"kind":1,"sequenceID":4,"operationID":3,"removeOld":null,"removeNew":null,`+
			`"state":`+resourceJSON("urn:m")+`,"deleteOld":0}`,
	)
	got, err := replay(base, entries)
	if err != nil {
		t.Fatal(err)
	}
	expectDeployment(t, got, resourceJSON("urn:n")+","+resourceJSON("urn:m")+
		`,{"urn":"urn:a","custom":true,"type":"t:mod:T","id":"x","delete":true}`+","+replaced+
		`,{"urn":"urn:c","custom":true,"type":"t:mod:T","id":"x","pendingReplacement":true}`)
}

// TestReplayIsDeterministic checks that the same input gives the same bytes on
// each run, with several maps in the output (extensions and property dependencies).
func TestReplayIsDeterministic(t *testing.T) {
	t.Parallel()
	b := `{"urn":"urn:b","custom":true,"type":"t:mod:T",` +
		`"propertyDependencies":{"p":["urn:a"],"q":["urn:a"],"r":["urn:a"]}}`
	base := baseJSON(resourceJSON("urn:a"), b,
		`{"name":"x"}`)
	entries := batch(
		`{"version":1,"kind":8,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null,`+
			`"extensionRef":"ext-b","extension":{"blob":"b"}}`,
		`{"version":1,"kind":8,"sequenceID":2,"operationID":1,"removeOld":null,"removeNew":null,`+
			`"extensionRef":"ext-a","extension":{"blob":"a"}}`,
		`{"version":1,"kind":8,"sequenceID":3,"operationID":1,"removeOld":null,"removeNew":null,`+
			`"extensionRef":"ext-c","extension":{"blob":"c"}}`,
	)
	first, err := replay(base, entries)
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		again, err := replay(base, entries)
		if err != nil {
			t.Fatal(err)
		}
		if again != first {
			t.Fatalf("two runs differ:\n%s\n%s", first, again)
		}
	}
}

// TestReplayRefusesBrokenBaseMembers checks that a base member that does not decode is an error, not dropped.
func TestReplayRefusesBrokenBaseMembers(t *testing.T) {
	t.Parallel()
	base := `{"resources":[],"snippets":"not a list"}`
	if _, err := replay(base, batch()); err == nil {
		t.Fatal("broken snippets member did not return an error")
	}
	base = `{"resources":[],"extensions":[1]}`
	if _, err := replay(base, batch()); err == nil {
		t.Fatal("broken extensions member did not return an error")
	}
}

// TestReplayAcceptsNullResources checks that a null resources member is an empty base, as upstream reads it.
func TestReplayAcceptsNullResources(t *testing.T) {
	t.Parallel()
	got, err := replay(`{"resources":null}`, batch(
		`{"version":1,"kind":0,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	expectDeployment(t, got, "")
}

// TestReplayRefusesMissingMembers checks that a secrets entry without a provider, and an
// extension entry without a reference, return ErrMissingMember (upstream panics here).
func TestReplayRefusesMissingMembers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		entry string
	}{
		{
			name:  "secrets provider",
			entry: `{"version":1,"kind":6,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}`,
		},
		{
			name:  "extension reference",
			entry: `{"version":1,"kind":8,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := replay(baseJSON(), batch(tt.entry))
			if !errors.Is(err, pulumijournal.ErrMissingMember) {
				t.Fatalf("error is %v, want ErrMissingMember", err)
			}
		})
	}
}
