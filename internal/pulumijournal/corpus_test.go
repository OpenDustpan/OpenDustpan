package pulumijournal_test

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json/v2"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/pulumijournal"
)

// corpusProviderIDPattern matches the random IDs that the engine assigns, for
// example to the default provider on every run. Each run mints its own, so
// the comparison ignores them.
var corpusProviderIDPattern = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// corpusCase is one recorded case: the base deployment, the accepted batch
// bodies in arrival order, and the export taken with journaling off.
type corpusCase struct {
	name    string
	base    string
	batches []string
	want    string
}

// loadCorpusCase reads the files of one case folder. The file order of the
// batch files is the arrival order.
func loadCorpusCase(t *testing.T, dir string) corpusCase {
	t.Helper()

	base, err := os.ReadFile(filepath.Join(dir, "base.json"))
	if err != nil {
		t.Fatalf("read base of %s: %v", dir, err)
	}

	files, err := filepath.Glob(filepath.Join(dir, "batch-*.json"))
	if err != nil {
		t.Fatalf("list batches of %s: %v", dir, err)
	}

	slices.Sort(files)

	if len(files) == 0 {
		t.Fatalf("case %s holds no batch", dir)
	}

	batches := make([]string, 0, len(files))

	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read batch %s: %v", file, err)
		}

		batches = append(batches, string(raw))
	}

	want, err := os.ReadFile(filepath.Join(dir, "export.json"))
	if err != nil {
		t.Fatalf("read export of %s: %v", dir, err)
	}

	return corpusCase{name: filepath.Base(dir), base: string(base), batches: batches, want: string(want)}
}

// corpusCollect decodes each batch body in order and returns the entries in
// arrival order. Decoding also checks the strict batch shape.
func corpusCollect(t *testing.T, batches []string) []pulumijournal.JournalEntry {
	t.Helper()

	var entries []pulumijournal.JournalEntry

	for i, body := range batches {
		err := pulumijournal.DecodeEntries(strings.NewReader(body), 2, func(entry pulumijournal.JournalEntry) error {
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

// corpusReplay replays entries on base with the fixed clock and CLI version,
// so the manifest is stable. The caller normalizes the output before it
// compares.
func corpusReplay(t *testing.T, base string, entries []pulumijournal.JournalEntry) string {
	t.Helper()

	body, err := json.Marshal(pulumijournal.JournalEntries{Entries: entries})
	if err != nil {
		t.Fatalf("encode entries: %v", err)
	}

	now := func() time.Time {
		return time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC)
	}
	open := func() io.ReadCloser {
		return io.NopCloser(strings.NewReader(base))
	}

	var out bytes.Buffer

	err = pulumijournal.NewReplayer(now, cliVersion).Replay(
		t.Context(),
		func(_ context.Context) (io.ReadCloser, error) { return open(), nil },
		bytes.NewReader(body),
		&out,
	)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}

	return out.String()
}

// corpusResourceURN returns the URN of a decoded resource, or "" when the
// value has none.
func corpusResourceURN(v any) string {
	obj, ok := v.(map[string]any)
	if !ok {
		return ""
	}

	urn, _ := obj["urn"].(string)

	return urn
}

// normalizeCorpusExport returns the canonical form of a checkpoint or stack
// export: without the manifest, the metadata, the secrets state and the
// resource times, with one fixed marker for each provider-assigned ID, and
// with the resources sorted by URN.
func normalizeCorpusExport(raw []byte) (string, error) {
	var doc struct {
		Version    int            `json:"version"`
		Deployment map[string]any `json:"deployment"`
	}

	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", err
	}

	delete(doc.Deployment, "manifest")
	delete(doc.Deployment, "metadata")

	if providers, ok := doc.Deployment["secrets_providers"].(map[string]any); ok {
		delete(providers, "state")
	}

	if resources, ok := doc.Deployment["resources"].([]any); ok {
		for _, res := range resources {
			if r, ok := res.(map[string]any); ok {
				delete(r, "created")
				delete(r, "modified")
			}
		}

		slices.SortFunc(resources, func(a, b any) int {
			return strings.Compare(corpusResourceURN(a), corpusResourceURN(b))
		})
	}

	canonical, err := json.Marshal(doc, json.Deterministic(true))
	if err != nil {
		return "", err
	}

	return corpusProviderIDPattern.ReplaceAllString(string(canonical), "provider-id"), nil
}

// clipCorpus shortens a canonical export for the failure report.
func clipCorpus(s string) string {
	const limit = 8000
	if len(s) <= limit {
		return s
	}

	return s[:limit] + "..."
}

// TestCorpusEquivalence replays the batches of each recorded case on its base
// deployment and compares the normalized result with the normalized recorded
// export. Each case runs twice: once in arrival order and once in sequenceID
// order. A difference is a test failure.
func TestCorpusEquivalence(t *testing.T) {
	t.Parallel()

	const corpusDir = "testdata/corpus"

	folders, err := os.ReadDir(corpusDir)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}

	cases := make([]string, 0, len(folders))

	for _, folder := range folders {
		if folder.IsDir() {
			cases = append(cases, folder.Name())
		}
	}

	if len(cases) == 0 {
		t.Fatalf("corpus %s holds no case", corpusDir)
	}

	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := loadCorpusCase(t, filepath.Join(corpusDir, name))
			arrival := corpusCollect(t, c.batches)
			ordered := slices.Clone(arrival)
			slices.SortStableFunc(ordered, func(a, b pulumijournal.JournalEntry) int {
				return cmp.Compare(a.SequenceID, b.SequenceID)
			})

			orders := []struct {
				name    string
				entries []pulumijournal.JournalEntry
			}{
				{name: "arrival", entries: arrival},
				{name: "sequenceID", entries: ordered},
			}

			for _, order := range orders {
				t.Run(order.name, func(t *testing.T) {
					t.Parallel()

					got, err := normalizeCorpusExport([]byte(corpusReplay(t, c.base, order.entries)))
					if err != nil {
						t.Fatalf("normalize replay result: %v", err)
					}

					want, err := normalizeCorpusExport([]byte(c.want))
					if err != nil {
						t.Fatalf("normalize recorded export: %v", err)
					}

					if got != want {
						t.Fatalf("replay in %s order differs from the recorded export:\ngot\n%s\nwant\n%s",
							order.name, clipCorpus(got), clipCorpus(want))
					}
				})
			}
		})
	}
}
