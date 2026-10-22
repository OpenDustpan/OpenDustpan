package pulumijournal_test

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"io/fs"
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

// openCorpusRoot opens the case folder as a file-system root. All later reads
// stay inside the folder.
func openCorpusRoot(t *testing.T, dir string) *os.Root {
	t.Helper()

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("open case %s: %v", dir, err)
	}

	t.Cleanup(func() {
		if cerr := root.Close(); cerr != nil {
			t.Errorf("close case %s: %v", dir, cerr)
		}
	})

	return root
}

// isCorpusBatch reports whether name is a batch file name.
func isCorpusBatch(name string) bool {
	if !strings.HasPrefix(name, "batch-") || !strings.HasSuffix(name, ".json") {
		return false
	}

	return !strings.Contains(name, "/") && !strings.Contains(name, `\`)
}

// listCorpusBatchNames returns the sorted batch file names in the case root.
func listCorpusBatchNames(t *testing.T, root *os.Root, dir string) []string {
	t.Helper()

	dirEntries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		t.Fatalf("list batches of %s: %v", dir, err)
	}

	var batchNames []string

	for _, entry := range dirEntries {
		if entry.IsDir() {
			continue
		}

		if name := entry.Name(); isCorpusBatch(name) {
			batchNames = append(batchNames, name)
		}
	}

	slices.Sort(batchNames)

	if len(batchNames) == 0 {
		t.Fatalf("case %s holds no batch", dir)
	}

	return batchNames
}

// readCorpusBatches returns the bodies of the named batch files in order.
func readCorpusBatches(t *testing.T, root *os.Root, dir string, batchNames []string) []string {
	t.Helper()

	batches := make([]string, 0, len(batchNames))

	for _, name := range batchNames {
		raw, readErr := root.ReadFile(name)
		if readErr != nil {
			t.Fatalf("read batch %s: %v", filepath.Join(dir, name), readErr)
		}

		batches = append(batches, string(raw))
	}

	return batches
}

// readCorpusFile returns the body of one fixed file in the case root.
func readCorpusFile(t *testing.T, root *os.Root, dir, name, what string) string {
	t.Helper()

	raw, err := root.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s of %s: %v", what, dir, err)
	}

	return string(raw)
}

// loadCorpusCase reads the files of one case folder. The file order of the
// batch files is the arrival order.
func loadCorpusCase(t *testing.T, dir string) corpusCase {
	t.Helper()

	root := openCorpusRoot(t, dir)
	base := readCorpusFile(t, root, dir, "base.json", "base")
	batchNames := listCorpusBatchNames(t, root, dir)
	batches := readCorpusBatches(t, root, dir, batchNames)
	want := readCorpusFile(t, root, dir, "export.json", "export")

	return corpusCase{name: filepath.Base(dir), base: base, batches: batches, want: want}
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
		[][]byte{body},
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

	urn, ok := obj["urn"].(string)
	if !ok {
		return ""
	}

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
		return "", fmt.Errorf("decode corpus export: %w", err)
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
		return "", fmt.Errorf("encode corpus export: %w", err)
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

// listCorpusCases returns the sorted names of the case folders in corpusDir.
func listCorpusCases(t *testing.T, corpusDir string) []string {
	t.Helper()

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

	return cases
}

// runCorpusOrder replays one entry order and compares the normalized result
// with the normalized recorded export.
func runCorpusOrder(t *testing.T, c corpusCase, orderName string, entries []pulumijournal.JournalEntry) {
	t.Helper()

	got, err := normalizeCorpusExport([]byte(corpusReplay(t, c.base, entries)))
	if err != nil {
		t.Fatalf("normalize replay result: %v", err)
	}

	want, err := normalizeCorpusExport([]byte(c.want))
	if err != nil {
		t.Fatalf("normalize recorded export: %v", err)
	}

	if got != want {
		t.Fatalf("replay in %s order differs from the recorded export:\ngot\n%s\nwant\n%s",
			orderName, clipCorpus(got), clipCorpus(want))
	}
}

// runCorpusCase replays one recorded case in arrival order and in sequenceID
// order.
func runCorpusCase(t *testing.T, corpusDir, name string) {
	t.Helper()

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

			runCorpusOrder(t, c, order.name, order.entries)
		})
	}
}

// TestCorpusEquivalence replays the batches of each recorded case on its base
// deployment and compares the normalized result with the normalized recorded
// export. Each case runs twice: once in arrival order and once in sequenceID
// order. A difference is a test failure.
func TestCorpusEquivalence(t *testing.T) {
	t.Parallel()

	const corpusDir = "testdata/corpus"

	cases := listCorpusCases(t, corpusDir)

	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			runCorpusCase(t, corpusDir, name)
		})
	}
}
