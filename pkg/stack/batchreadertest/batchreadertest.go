// Package batchreadertest holds the conformance suite of stack.BatchReader.
// Its input is journal batch bodies of versions 1 and 2, as the Pulumi CLI
// sends them, also hostile bodies that the reader must refuse. A body with no
// entries is valid.
package batchreadertest

import (
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
)

// version is the journal version of the bodies of the suite, except for the
// cases of testVersions. versionTwo is the journal version that adds the state
// migration entry.
const (
	version    = 1
	versionTwo = 2
)

// Run runs the conformance suite on the batch reader r. r must be safe for
// use by more than one goroutine, because the cases run in parallel.
func Run(t *testing.T, r stack.BatchReader) {
	t.Helper()

	t.Run("accepted bodies", func(t *testing.T) {
		t.Parallel()

		testAccepted(t, r)
	})

	t.Run("refused bodies", func(t *testing.T) {
		t.Parallel()

		testRefused(t, r)
	})

	t.Run("entry check", func(t *testing.T) {
		t.Parallel()

		testVerify(t, r)
	})

	t.Run("journal versions", func(t *testing.T) {
		t.Parallel()

		testVersions(t, r)
	})
}

// entry returns one journal entry of kind with the sequence ID seq and the
// operation ID op, each a JSON number. ref is the operation that the entry
// removes, or "" for no reference.
func entry(kind, seq, op, ref string) string {
	removeNew := "null"
	if ref != "" {
		removeNew = ref
	}

	return `{"version":1,"kind":` + kind + `,"sequenceID":` + seq + `,"operationID":` + op +
		`,"removeOld":null,"removeNew":` + removeNew + `}`
}

// completed returns a success entry with a state: the entry of a completed
// operation op, which later references can name.
func completed(seq, op string) string {
	return `{"version":1,"kind":1,"sequenceID":` + seq + `,"operationID":` + op +
		`,"removeOld":null,"removeNew":null,` +
		`"state":{"urn":"urn:pulumi:dev::suite::test:resource::r","custom":true,"type":"test:resource","id":"r"}}`
}

// write returns a write entry (kind 5) with an empty snapshot.
func write(seq, op string) string {
	return `{"version":1,"kind":5,"sequenceID":` + seq + `,"operationID":` + op +
		`,"removeOld":null,"removeNew":null,` +
		`"newSnapshot":{"manifest":{"time":"2026-01-01T00:00:00Z","magic":"","version":""},"resources":[]}}`
}

// versioned returns an entry of kind with the entry version v and the
// sequence ID and the operation ID 1, each a JSON number.
func versioned(v, kind string) string {
	return `{"version":` + v + `,"kind":` + kind + `,"sequenceID":1,"operationID":1,"removeOld":null,"removeNew":null}`
}

// Entry kinds of the bodies of the suite.
const (
	kindBegin     = "0"
	kindFailure   = "2"
	kindRebuild   = "7"
	kindMigration = "10"
)

// batch wraps entries in a batch body.
func batch(entries ...string) []byte {
	return []byte(`{"entries":[` + strings.Join(entries, ",") + `]}`)
}

// testAccepted checks the summary of bodies that the reader must accept: the
// entry count, the sequence IDs in the order of the body, the completed
// operations after the last rebuilt base state entry, the rebuild flag, and
// an entry check that is not nil. Each entry has one sequence ID, so the
// entry count is the number of sequence IDs.
func testAccepted(t *testing.T, r stack.BatchReader) {
	t.Helper()

	tests := []struct {
		name    string
		body    []byte
		seqs    []int64
		ops     []int64
		rebuild bool
	}{
		{name: "empty object", body: []byte(`{}`), seqs: nil, ops: nil, rebuild: false},
		{name: "empty list", body: batch(), seqs: nil, ops: nil, rebuild: false},
		{
			name:    "sequence IDs in body order",
			body:    batch(completed("5", "7"), entry(kindBegin, "4", "9", ""), completed("3", "8")),
			seqs:    []int64{5, 4, 3},
			ops:     []int64{7, 8},
			rebuild: false,
		},
		{
			name:    "operations after the last rebuild",
			body:    batch(completed("1", "5"), entry(kindRebuild, "2", "6", ""), completed("3", "8")),
			seqs:    []int64{1, 2, 3},
			ops:     []int64{8},
			rebuild: true,
		},
	}

	for _, tt := range tests {
		summary, err := r.ReadBatch(tt.body, version)
		if err != nil {
			t.Errorf("%s: read batch: %v", tt.name, err)

			continue
		}

		if summary.Entries != len(tt.seqs) || !slices.Equal(summary.SequenceIDs, tt.seqs) {
			t.Errorf("%s: entries %d, sequence IDs %v; want %d and %v",
				tt.name, summary.Entries, summary.SequenceIDs, len(tt.seqs), tt.seqs)
		}

		if !slices.Equal(summary.Operations, tt.ops) || summary.HasRebuild != tt.rebuild {
			t.Errorf("%s: operations %v, rebuild %t; want %v and %t",
				tt.name, summary.Operations, summary.HasRebuild, tt.ops, tt.rebuild)
		}

		if summary.Verify == nil {
			t.Errorf("%s: Verify = nil, want the entry check", tt.name)
		}
	}
}

// testRefused checks that the reader refuses hostile bodies, and that the
// summary of each refusal holds the sequence IDs that a plain read of the
// body finds, so the refusal record names them.
func testRefused(t *testing.T, r stack.BatchReader) {
	t.Helper()

	tests := []struct {
		name string
		body []byte
		seqs []int64
	}{
		{name: "not JSON", body: []byte(`{`), seqs: nil},
		{name: "sequence ID zero", body: batch(completed("0", "1")), seqs: []int64{0}},
		{name: "negative sequence ID", body: batch(completed("-2", "1")), seqs: []int64{-2}},
		{name: "repeated sequence ID", body: batch(completed("2", "1"), completed("2", "3")), seqs: []int64{2, 2}},
		{
			name: "repeated sequence ID after a valid entry",
			body: batch(completed("1", "1"), completed("2", "2"), completed("1", "3")),
			seqs: []int64{1, 2, 1},
		},
	}

	for _, tt := range tests {
		summary, err := r.ReadBatch(tt.body, version)
		if err == nil {
			t.Errorf("%s: err = nil, want a refusal", tt.name)

			continue
		}

		if !slices.Equal(summary.SequenceIDs, tt.seqs) {
			t.Errorf("%s: sequence IDs %v, want %v", tt.name, summary.SequenceIDs, tt.seqs)
		}
	}
}

// testVerify checks the entry check of accepted bodies against the stored
// state of the update: a reference must name a known operation or a completed
// entry earlier in the batch, and a write entry must come before every entry
// that changes the replay records.
func testVerify(t *testing.T, r stack.BatchReader) {
	t.Helper()

	tests := []struct {
		name           string
		body           []byte
		known          []int64
		recordsChanged bool
		wantErr        bool
		wantChanged    bool
	}{
		{
			name: "reference to a known operation", body: batch(entry(kindFailure, "1", "3", "7")),
			known: []int64{7}, recordsChanged: false, wantErr: false, wantChanged: true,
		},
		{
			name:  "reference to an operation earlier in the batch",
			body:  batch(completed("1", "7"), entry(kindFailure, "2", "3", "7")),
			known: nil, recordsChanged: false, wantErr: false, wantChanged: true,
		},
		{
			name: "reference to an unknown operation", body: batch(entry(kindFailure, "1", "3", "7")),
			known: nil, recordsChanged: false, wantErr: true, wantChanged: false,
		},
		{
			name: "write before a change in the batch", body: batch(write("1", "1"), entry(kindBegin, "2", "2", "")),
			known: nil, recordsChanged: false, wantErr: false, wantChanged: true,
		},
		{
			name: "write after a change in the batch", body: batch(entry(kindBegin, "1", "1", ""), write("2", "2")),
			known: nil, recordsChanged: false, wantErr: true, wantChanged: false,
		},
		{
			name: "write after a stored change", body: batch(write("1", "1")),
			known: nil, recordsChanged: true, wantErr: true, wantChanged: false,
		},
		{
			name: "write with no change", body: batch(write("1", "1")),
			known: nil, recordsChanged: false, wantErr: false, wantChanged: false,
		},
	}

	for _, tt := range tests {
		summary, err := r.ReadBatch(tt.body, version)
		if err != nil || summary.Verify == nil {
			t.Errorf("%s: read batch: err %v, Verify set %t; want no error and the entry check",
				tt.name, err, summary.Verify != nil)

			continue
		}

		changed, err := summary.Verify(tt.known, tt.recordsChanged)
		if (err != nil) != tt.wantErr {
			t.Errorf("%s: Verify error = %v, want an error %t", tt.name, err, tt.wantErr)

			continue
		}

		if err == nil && changed != tt.wantChanged {
			t.Errorf("%s: changed = %t, want %t", tt.name, changed, tt.wantChanged)
		}
	}
}

// testVersions checks the entry versions against the negotiated journal
// version: an entry version must be from 1 to the negotiated version, and a
// state migration entry needs version 2. A body with no entries is valid at
// each version.
func testVersions(t *testing.T, r stack.BatchReader) {
	t.Helper()

	tests := []struct {
		name       string
		body       []byte
		negotiated int
		wantErr    bool
	}{
		{name: "empty body at version 2", body: []byte(`{}`), negotiated: versionTwo, wantErr: false},
		{
			name: "version 1 entry at version 2", body: batch(completed("1", "5")),
			negotiated: versionTwo, wantErr: false,
		},
		{
			name: "state migration entry at version 2", body: batch(versioned("2", kindMigration)),
			negotiated: versionTwo, wantErr: false,
		},
		{
			name: "version 2 entry at version 1", body: batch(versioned("2", kindBegin)),
			negotiated: version, wantErr: true,
		},
		{name: "entry version zero", body: batch(versioned("0", kindBegin)), negotiated: version, wantErr: true},
		{
			name: "state migration entry of version 1", body: batch(versioned("1", kindMigration)),
			negotiated: versionTwo, wantErr: true,
		},
	}

	for _, tt := range tests {
		summary, err := r.ReadBatch(tt.body, tt.negotiated)
		if (err != nil) != tt.wantErr {
			t.Errorf("%s: read batch: err = %v, want an error %t", tt.name, err, tt.wantErr)

			continue
		}

		if err == nil && summary.Verify == nil {
			t.Errorf("%s: Verify = nil, want the entry check", tt.name)
		}
	}
}
