// Package replayertest holds the conformance suite of stack.JournalReplayer.
// Its input is a corpus of recorded updates: for each update, the base
// deployment, the stored journal batches and the stack export of the same
// program with journaling off.
package replayertest

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
)

// Case is one recorded update of the corpus: the base deployment that the
// server served, the stored batch bodies in arrival order, and the export
// taken with journaling off.
type Case struct {
	Name    string
	Base    []byte
	Batches [][]byte
	Export  []byte
}

// errBaseOpen is the error of the base opener that the suite makes fail.
var errBaseOpen = errors.New("replayertest: the base cannot be opened")

// providerIDPattern matches the random IDs that the engine assigns, for
// example to the default provider on every run. Each run mints its own, so
// the comparison ignores them.
var providerIDPattern = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// Run runs the conformance suite on the replayers that newReplayer returns,
// one for each test. corpus holds one folder for each case: base.json, the
// batch files batch-0001.json, batch-0002.json and so on in arrival order,
// and export.json.
func Run(t *testing.T, newReplayer func() stack.JournalReplayer, corpus fs.FS) {
	t.Helper()

	cases := LoadCorpus(t, corpus)

	t.Run("corpus", func(t *testing.T) {
		t.Parallel()

		for _, c := range cases {
			t.Run(c.Name, func(t *testing.T) {
				t.Parallel()

				testCorpusCase(t, newReplayer(), c)
			})
		}
	})

	first := cases[0]

	t.Run("closes each base reader", func(t *testing.T) {
		t.Parallel()

		testClosesBase(t, newReplayer(), first)
	})

	t.Run("no batches gives the base", func(t *testing.T) {
		t.Parallel()

		testNoBatches(t, newReplayer(), first)
	})

	t.Run("open error of the base", func(t *testing.T) {
		t.Parallel()

		testOpenError(t, newReplayer(), first)
	})

	t.Run("ended context", func(t *testing.T) {
		t.Parallel()

		testEndedContext(t, newReplayer(), first)
	})

	t.Run("batch that does not decode", func(t *testing.T) {
		t.Parallel()

		testBadBatch(t, newReplayer(), first)
	})
}

// LoadCorpus reads every case folder of corpus, sorted by name. It fails the
// test when the corpus holds no case, or a case holds no batch.
func LoadCorpus(t *testing.T, corpus fs.FS) []Case {
	t.Helper()

	folders, err := fs.ReadDir(corpus, ".")
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}

	var cases []Case

	for _, folder := range folders {
		if folder.IsDir() {
			cases = append(cases, loadCase(t, corpus, folder.Name()))
		}
	}

	if len(cases) == 0 {
		t.Fatal("the corpus holds no case")
	}

	return cases
}

// loadCase reads the files of one case folder. The file order of the batch
// files is the arrival order.
func loadCase(t *testing.T, corpus fs.FS, dir string) Case {
	t.Helper()

	entries, err := fs.ReadDir(corpus, dir)
	if err != nil {
		t.Fatalf("list case %s: %v", dir, err)
	}

	var names []string

	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && strings.HasPrefix(name, "batch-") && strings.HasSuffix(name, ".json") {
			names = append(names, name)
		}
	}

	if len(names) == 0 {
		t.Fatalf("case %s holds no batch", dir)
	}

	slices.Sort(names)

	batches := make([][]byte, 0, len(names))
	for _, name := range names {
		batches = append(batches, readFile(t, corpus, path.Join(dir, name)))
	}

	return Case{
		Name:    dir,
		Base:    readFile(t, corpus, path.Join(dir, "base.json")),
		Batches: batches,
		Export:  readFile(t, corpus, path.Join(dir, "export.json")),
	}
}

// readFile returns the body of one file of the corpus.
func readFile(t *testing.T, corpus fs.FS, name string) []byte {
	t.Helper()

	raw, err := fs.ReadFile(corpus, name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}

	return raw
}

// Normalize returns the canonical form of a checkpoint or a stack export:
// without the manifest, the metadata, the secrets state and the resource
// times, with one fixed marker for each provider-assigned ID, and with the
// resources sorted by URN. The checkpoint lists the resources in the order
// the engine completed them, and a replay lists them in journal order.
func Normalize(raw []byte) (string, error) {
	var doc struct {
		Version    int            `json:"version"`
		Deployment map[string]any `json:"deployment"`
	}

	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("decode export: %w", err)
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
			return strings.Compare(resourceURN(a), resourceURN(b))
		})
	}

	canonical, err := json.Marshal(doc, json.Deterministic(true))
	if err != nil {
		return "", fmt.Errorf("encode export: %w", err)
	}

	return providerIDPattern.ReplaceAllString(string(canonical), "provider-id"), nil
}

// Equal fails the test when the normalized forms of got and want differ.
// what names the result in the failure report.
func Equal(t *testing.T, what string, got, want []byte) {
	t.Helper()

	gotForm, err := Normalize(got)
	if err != nil {
		t.Fatalf("normalize %s: %v", what, err)
	}

	wantForm, err := Normalize(want)
	if err != nil {
		t.Fatalf("normalize the recorded export: %v", err)
	}

	if gotForm != wantForm {
		t.Fatalf("%s differs from the recorded export:\ngot\n%s\nwant\n%s", what, clip(gotForm), clip(wantForm))
	}
}

// resourceURN returns the URN of a decoded resource, or "" when the value has
// none.
func resourceURN(v any) string {
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

// clip shortens a canonical export for the failure report.
func clip(s string) string {
	const limit = 8000
	if len(s) <= limit {
		return s
	}

	return s[:limit] + "..."
}

// opener returns a base opener that serves base, and counts the readers it
// opens and the readers that are closed.
type opener struct {
	base   []byte
	mu     sync.Mutex
	opened int
	closed int
}

func (o *opener) open(context.Context) (io.ReadCloser, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.opened++

	return &countedReader{Reader: bytes.NewReader(o.base), opener: o, done: false}, nil
}

// readerCounts holds the number of base readers that a replay opened and
// closed.
type readerCounts struct {
	opened int
	closed int
}

// counts returns the number of opened and closed readers.
func (o *opener) counts() readerCounts {
	o.mu.Lock()
	defer o.mu.Unlock()

	return readerCounts{opened: o.opened, closed: o.closed}
}

// countedReader is a base reader that tells its opener when it is closed.
type countedReader struct {
	*bytes.Reader

	opener *opener
	done   bool
}

func (r *countedReader) Close() error {
	r.opener.mu.Lock()
	defer r.opener.mu.Unlock()

	if !r.done {
		r.done = true
		r.opener.closed++
	}

	return nil
}

// replay runs the replayer on c.Base and batches, and returns the output.
func replay(ctx context.Context, r stack.JournalReplayer, o *opener, batches [][]byte) ([]byte, error) {
	var out bytes.Buffer

	err := r.Replay(ctx, o.open, batches, &out)

	return out.Bytes(), err
}

// testCorpusCase replays the stored batches of c on its base and compares
// the result with the recorded export.
func testCorpusCase(t *testing.T, r stack.JournalReplayer, c Case) {
	t.Helper()

	o := &opener{base: c.Base, mu: sync.Mutex{}, opened: 0, closed: 0}

	got, err := replay(t.Context(), r, o, c.Batches)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}

	Equal(t, "the replay of the stored batches", got, c.Export)
}

// testClosesBase checks that the replay opens the base at least one time and
// closes each reader that it opens.
func testClosesBase(t *testing.T, r stack.JournalReplayer, c Case) {
	t.Helper()

	o := &opener{base: c.Base, mu: sync.Mutex{}, opened: 0, closed: 0}

	if _, err := replay(t.Context(), r, o, c.Batches); err != nil {
		t.Fatalf("replay: %v", err)
	}

	if n := o.counts(); n.opened == 0 || n.closed != n.opened {
		t.Fatalf("base readers: %d opened, %d closed; want at least 1, all closed", n.opened, n.closed)
	}
}

// testNoBatches checks that a replay of no batches writes the base in a
// checkpoint.
func testNoBatches(t *testing.T, r stack.JournalReplayer, c Case) {
	t.Helper()

	o := &opener{base: c.Base, mu: sync.Mutex{}, opened: 0, closed: 0}

	got, err := replay(t.Context(), r, o, nil)
	if err != nil {
		t.Fatalf("replay of no batches: %v", err)
	}

	gotForm, err := Normalize(got)
	if err != nil {
		t.Fatalf("normalize the replay of no batches: %v", err)
	}

	// The base is a bare deployment, and the replay writes a checkpoint of
	// version 3 that holds it.
	checkpoint := slices.Concat([]byte(`{"version":3,"deployment":`), c.Base, []byte(`}`))

	baseForm, err := Normalize(checkpoint)
	if err != nil {
		t.Fatalf("normalize the base: %v", err)
	}

	if gotForm != baseForm {
		t.Fatalf("replay of no batches:\ngot\n%s\nwant the base\n%s", clip(gotForm), clip(baseForm))
	}
}

// testOpenError checks that an error of the base opener is returned, so the
// service does not store a checkpoint built from no base.
func testOpenError(t *testing.T, r stack.JournalReplayer, c Case) {
	t.Helper()

	failing := func(context.Context) (io.ReadCloser, error) { return nil, errBaseOpen }

	var out bytes.Buffer

	err := r.Replay(t.Context(), failing, c.Batches, &out)
	if !errors.Is(err, errBaseOpen) {
		t.Fatalf("err = %v, want the error of the base opener", err)
	}
}

// testEndedContext checks that a replay under an ended context stops with an
// error that holds the context error.
func testEndedContext(t *testing.T, r stack.JournalReplayer, c Case) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	o := &opener{base: c.Base, mu: sync.Mutex{}, opened: 0, closed: 0}

	if _, err := replay(ctx, r, o, c.Batches); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}

	if n := o.counts(); n.closed != n.opened {
		t.Fatalf("base readers: %d opened, %d closed; want all closed", n.opened, n.closed)
	}
}

// testBadBatch checks that a batch that is not a journal batch is refused
// with an error, and that the base readers are closed.
func testBadBatch(t *testing.T, r stack.JournalReplayer, c Case) {
	t.Helper()

	o := &opener{base: c.Base, mu: sync.Mutex{}, opened: 0, closed: 0}
	batches := append(slices.Clone(c.Batches), []byte(`{"entries":[{"sequenceID":"x"}]}`))

	if _, err := replay(t.Context(), r, o, batches); err == nil {
		t.Fatal("err = nil, want a refusal of the batch")
	}

	if n := o.counts(); n.closed != n.opened {
		t.Fatalf("base readers: %d opened, %d closed; want all closed", n.opened, n.closed)
	}
}
