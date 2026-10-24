package pulumijournal_test

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/stack/replayertest"
)

// corpusDir holds the recorded cases of the corpus.
const corpusDir = "testdata/corpus"

// corpusNow is the fixed clock of the corpus replays, so the manifest is
// stable.
func corpusNow() time.Time {
	return time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC)
}

// openCorpus opens the corpus folder as a file-system root. All later reads
// stay inside the folder.
func openCorpus(t *testing.T) *os.Root {
	t.Helper()

	root, err := os.OpenRoot(corpusDir)
	if err != nil {
		t.Fatalf("open corpus: %v", err)
	}

	t.Cleanup(func() {
		if cerr := root.Close(); cerr != nil {
			t.Errorf("close corpus: %v", cerr)
		}
	})

	return root
}

// TestReplayerConformance runs the conformance suite of the replayer port on
// the replayer of this package. The suite replays the stored batches of each
// recorded case on its base and compares the result with the recorded
// export.
func TestReplayerConformance(t *testing.T) {
	t.Parallel()

	replayertest.Run(t, func() stack.JournalReplayer {
		return pulumijournal.NewReplayer(corpusNow, cliVersion)
	}, openCorpus(t).FS(), newStatesOverBound())
}

// newStatesOverBound returns an update whose new states pass
// MaxNewStateBytesPerUpdate with the bound of the package: five success
// entries, each with a state of 7 MiB, on a base with no resources.
func newStatesOverBound() replayertest.Case {
	const (
		states     = 5
		stateBytes = 7 << 20
	)

	blob := strings.Repeat("a", stateBytes)

	var b strings.Builder

	b.WriteString(`{"entries":[`)

	for i := range states {
		if i > 0 {
			b.WriteString(",")
		}

		fmt.Fprintf(&b, `{"version":1,"kind":1,"sequenceID":%d,"operationID":%d,"removeOld":null,"removeNew":null,`+
			`"state":{"urn":"urn:pulumi:dev::p::t:index:R::r%d","custom":true,"type":"t:index:R","id":"r%d",`+
			`"outputs":{"blob":%q}}}`, i+1, i+1, i, i, blob)
	}

	b.WriteString(`]}`)

	return replayertest.Case{
		Name:    "new states over the bound",
		Base:    []byte(`{"resources":[]}`),
		Batches: [][]byte{[]byte(b.String())},
		Export:  nil,
	}
}

// corpusEntries decodes each batch body in order and returns the entries in
// arrival order. Decoding also checks the strict batch shape.
func corpusEntries(t *testing.T, batches [][]byte) []pulumijournal.JournalEntry {
	t.Helper()

	var entries []pulumijournal.JournalEntry

	for i, body := range batches {
		err := pulumijournal.DecodeEntries(bytes.NewReader(body), 2, func(entry pulumijournal.JournalEntry) error {
			entries = append(entries, entry)
			return nil
		})
		if err != nil {
			t.Fatalf("decode batch %d: %v", i, err)
		}
	}

	if len(entries) == 0 {
		t.Fatal("the batches hold no entry")
	}

	return entries
}

// TestCorpusSequenceOrder replays the entries of each recorded case in
// sequenceID order, as one batch, and compares the normalized result with
// the normalized recorded export. The conformance suite covers the arrival
// order. A difference is a test failure.
func TestCorpusSequenceOrder(t *testing.T) {
	t.Parallel()

	for _, c := range replayertest.LoadCorpus(t, openCorpus(t).FS()) {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()

			entries := corpusEntries(t, c.Batches)
			slices.SortStableFunc(entries, func(a, b pulumijournal.JournalEntry) int {
				return cmp.Compare(a.SequenceID, b.SequenceID)
			})

			body, err := json.Marshal(pulumijournal.JournalEntries{Entries: entries})
			if err != nil {
				t.Fatalf("encode entries: %v", err)
			}

			open := func(context.Context) (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(c.Base)), nil
			}

			var out bytes.Buffer

			err = pulumijournal.NewReplayer(corpusNow, cliVersion).Replay(t.Context(), open, [][]byte{body}, &out)
			if err != nil {
				t.Fatalf("replay: %v", err)
			}

			replayertest.Equal(t, "the replay in sequenceID order", out.Bytes(), c.Export)
		})
	}
}
