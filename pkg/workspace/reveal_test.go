package workspace_test

import (
	"encoding/json"
	"errors"
	"io"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/workspace"
)

// revealState returns a state file with a sensitive input holding the
// secret, in the shape the CLI writes.
func revealState() string {
	return `{"version":4,"terraform_version":"1.11.0","serial":1,"lineage":"seed-lineage",` +
		`"outputs":{},"resources":[` +
		`{"mode":"managed","type":"terraform_data","name":"smoke",` +
		`"provider":"provider[\"terraform.io/builtin/terraform\"]",` +
		`"instances":[{"schema_version":0,` +
		`"attributes":{"id":"smoke-id","input":"s3cr3t"},` +
		`"sensitive_attributes":[[{"type":"get_attr","value":"input"}]] }]}]}`
}

// revealMarks keeps the kept json-state marks of the reveal state non-empty,
// so the read masks the sensitive input only instead of every value.
func revealMarks() workspace.SensitiveMarks {
	return workspace.SensitiveMarks{"other.none": {"/id"}}
}

// TestRevealValueAnswersPlain proves reveal of a masked path answers the
// plain value.
func TestRevealValueAnswersPlain(t *testing.T) {
	t.Parallel()

	masked, err := workspace.MaskedPaths([]byte(revealState()), revealMarks())
	if err != nil {
		t.Fatalf("masked paths: %v", err)
	}

	if len(masked) == 0 {
		t.Fatal("masked holds no path")
	}

	value, _, err := workspace.RevealValue([]byte(revealState()), revealMarks(), masked[0])
	if err != nil {
		t.Fatalf("reveal: %v", err)
	}

	if value != "s3cr3t" {
		t.Fatalf("value = %#v, want s3cr3t", value)
	}
}

// errCloseState is the close error of the test opener.
var errCloseState = errors.New("close state")

// closeFailReadCloser answers the bytes of the wrapped reader and fails on
// close, so the test proves a close error does not fail the lookup.
type closeFailReadCloser struct {
	io.Reader
}

// Close fails with a close error.
func (c closeFailReadCloser) Close() error {
	return errCloseState
}

// TestRevealValueFromOpenerIgnoresCloseError proves a close error does not
// fail the lookup.
func TestRevealValueFromOpenerIgnoresCloseError(t *testing.T) {
	t.Parallel()

	masked, err := workspace.MaskedPaths([]byte(revealState()), revealMarks())
	if err != nil {
		t.Fatalf("masked paths: %v", err)
	}

	if len(masked) == 0 {
		t.Fatal("masked holds no path")
	}

	open := func() (io.ReadCloser, error) {
		return closeFailReadCloser{Reader: strings.NewReader(revealState())}, nil
	}

	value, _, err := workspace.RevealValueFromOpener(open, revealMarks(), masked[0])
	if err != nil {
		t.Fatalf("reveal: %v", err)
	}

	if value != "s3cr3t" {
		t.Fatalf("value = %#v, want s3cr3t", value)
	}
}

// TestRevealValueRejectsPlainPath proves a path outside the masked set
// answers a not-found error, so a caller cannot probe paths.
func TestRevealValueRejectsPlainPath(t *testing.T) {
	t.Parallel()

	masked, err := workspace.MaskedPaths([]byte(revealState()), revealMarks())
	if err != nil {
		t.Fatalf("masked paths: %v", err)
	}

	plain := "/resources/0/instances/0/attributes/id"
	for _, path := range masked {
		if path == plain {
			t.Fatalf("masked holds the plain path %q", plain)
		}
	}

	if _, _, err := workspace.RevealValue([]byte(revealState()), revealMarks(), plain); err == nil {
		t.Fatal("plain path succeeded, want a not-found error")
	} else if !errors.Is(err, workspace.ErrResourceNotFound) {
		t.Fatalf("plain path error = %v, want a not-found error", err)
	}
}

// TestRevealValueRejectsClientEncrypted proves a state the client encrypted
// fails with the client-encrypted sentinel.
func TestRevealValueRejectsClientEncrypted(t *testing.T) {
	t.Parallel()

	encrypted := `{"version":4,"encrypted_data":{"iv":"QUJD","data":"R0hJ"},"outputs":{},"resources":[]}`

	if _, _, err := workspace.RevealValue([]byte(encrypted), nil, "/resources/0"); err == nil {
		t.Fatal("encrypted state succeeded, want a client-encrypted error")
	} else if !errors.Is(err, workspace.ErrClientEncrypted) {
		t.Fatalf("encrypted error = %v, want a client-encrypted error", err)
	}
}

// TestRevealValueEscapedKeys proves the streaming lookup unescapes value
// paths the same way as the masked read: a key with a slash resolves through
// its escaped token.
func TestRevealValueEscapedKeys(t *testing.T) {
	t.Parallel()

	state := []byte(`{"version":4,"terraform_version":"1.11.0","serial":1,"lineage":"seed-lineage",` +
		`"outputs":{},"resources":[` +
		`{"mode":"managed","type":"terraform_data","name":"esc",` +
		`"provider":"provider[\"terraform.io/builtin/terraform\"]",` +
		`"instances":[{"schema_version":0,` +
		`"attributes":{"id":"esc-id","a/b":"slash-secret"},` +
		`"sensitive_attributes":[[{"type":"get_attr","value":"a/b"}]] }]}]}`)

	marks := workspace.SensitiveMarks{"terraform_data.esc": {"/id"}}

	masked, err := workspace.MaskedPaths(state, marks)
	if err != nil {
		t.Fatalf("masked paths: %v", err)
	}

	path := "/resources/0/instances/0/attributes/a~1b"
	if !slices.Contains(masked, path) {
		t.Fatalf("masked = %q, want the escaped slash path", masked)
	}

	value, _, err := workspace.RevealValue(state, marks, path)
	if err != nil {
		t.Fatalf("reveal: %v", err)
	}

	if value != "slash-secret" {
		t.Fatalf("value = %#v, want slash-secret", value)
	}
}

// largeStateStream generates a state much larger than the decoder buffer
// without ever holding it: one filler template repeated, then the target
// resource with the secret, then the closing brackets. It counts the bytes
// it serves.
type largeStateStream struct {
	prefix  []byte
	filler  []byte
	target  []byte
	suffix  []byte
	fillers int
	stage   int
	comma   bool
	pending []byte
	total   int
}

// Read serves the next piece of the generated state.
func (s *largeStateStream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	for len(s.pending) == 0 {
		if !s.advance() {
			return 0, io.EOF
		}
	}

	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	s.total += n

	return n, nil
}

// advance queues the next piece of the generated state. It reports whether a
// piece is queued.
func (s *largeStateStream) advance() bool {
	switch s.stage {
	case 0:
		s.pending = s.prefix
		s.stage = 1

		return true
	case 1:
		if s.fillers == 0 {
			s.stage = 2

			return s.advance()
		}

		if s.comma {
			s.pending = []byte(",")
			s.comma = false

			return true
		}

		s.pending = s.filler
		s.fillers--
		s.comma = true

		return true
	case 2:
		if s.comma {
			s.pending = []byte(",")
			s.comma = false

			return true
		}

		s.pending = s.target
		s.stage = 3

		return true
	case 3:
		s.pending = s.suffix
		s.stage = 4

		return true
	default:
		return false
	}
}

// TestRevealValueStreamsLargeState proves a reveal near the end of a state
// much larger than the decoder buffer finds the value while the heap
// allocated by the lookup stays far below the state size. The state is read
// from a stream that generates it, never from a byte slice.
//
//nolint:paralleltest // The heap measurement needs a quiet runtime, so this test never runs in parallel.
func TestRevealValueStreamsLargeState(t *testing.T) {
	// No t.Parallel: the heap measurement needs a quiet runtime.
	const fillers = 2600

	filler := []byte(`{"mode":"managed","type":"terraform_data","name":"filler",` +
		`"provider":"provider[\"terraform.io/builtin/terraform\"]",` +
		`"instances":[{"schema_version":0,` +
		`"attributes":{"id":"filler-id","blob":"` + strings.Repeat("x", 8000) + `"},` +
		`"sensitive_attributes":[] }]}`)

	target := []byte(`{"mode":"managed","type":"terraform_data","name":"target",` +
		`"provider":"provider[\"terraform.io/builtin/terraform\"]",` +
		`"instances":[{"schema_version":0,` +
		`"attributes":{"id":"target-id","input":"s3cr3t-large"},` +
		`"sensitive_attributes":[[{"type":"get_attr","value":"input"}]] }]}`)

	newStream := func() *largeStateStream {
		return &largeStateStream{
			prefix: []byte(`{"version":4,"terraform_version":"1.11.0","serial":1,` +
				`"lineage":"seed-lineage","outputs":{},"resources":[`),
			filler:  filler,
			target:  target,
			suffix:  []byte(`]}`),
			fillers: fillers,
			stage:   0,
			comma:   false,
			pending: nil,
			total:   0,
		}
	}

	marks := workspace.SensitiveMarks{"terraform_data.target": {"/input"}}
	path := "/resources/" + strconv.Itoa(fillers) + "/instances/0/attributes/input"

	// Warm the path once, so one-time allocations stay out of the
	// measurement.
	warm := func() (io.ReadCloser, error) {
		return io.NopCloser(newStream()), nil
	}

	if _, _, err := workspace.RevealValueFromOpener(warm, marks, path); err != nil {
		t.Fatalf("warm reveal: %v", err)
	}

	runtime.GC() //nolint:revive // The heap measurement needs a quiet runtime, so the test collects first.
	runtime.GC() //nolint:revive // The heap measurement needs a quiet runtime, so the test collects first.

	var before runtime.MemStats

	runtime.ReadMemStats(&before)

	// The lookup opens the state once per pass, and each open must serve the
	// same bytes from the start, so every open gets a fresh stream.
	var served []*largeStateStream

	serve := func() (io.ReadCloser, error) {
		s := newStream()
		served = append(served, s)

		return io.NopCloser(s), nil
	}

	value, masked, err := workspace.RevealValueFromOpener(serve, marks, path)

	var after runtime.MemStats

	runtime.ReadMemStats(&after)

	if err != nil {
		t.Fatalf("reveal: %v", err)
	}

	if value != "s3cr3t-large" {
		t.Fatalf("value = %#v, want s3cr3t-large", value)
	}

	if len(masked) != 1 || masked[0] != path {
		t.Fatalf("masked = %q, want [%q]", masked, path)
	}

	total := 0
	for _, s := range served {
		total += s.total
	}

	if total < 20<<20 {
		t.Fatalf("streams served %d bytes, want at least 20 MiB", total)
	}

	if growth := after.TotalAlloc - before.TotalAlloc; growth > 4<<20 {
		t.Fatalf(
			"heap allocated = %d bytes, want below 4 MiB for a %d-byte state",
			growth,
			total,
		)
	}
}

// TestRevealValueRejectsResourceWithoutIdentity proves a value in a resource
// without a string type and name answers not-found: the masked read holds no
// path for such a resource, so the reveal lookup must not answer it either.
func TestRevealValueRejectsResourceWithoutIdentity(t *testing.T) {
	t.Parallel()

	state := []byte(`{"version":4,"terraform_version":"1.11.0","serial":1,"lineage":"seed-lineage",` +
		`"outputs":{},"resources":[` +
		`{"mode":"managed",` +
		`"instances":[{"schema_version":0,` +
		`"attributes":{"id":"orphan-id","input":"s3cr3t"},` +
		`"sensitive_attributes":[[{"type":"get_attr","value":"input"}]] }]}]}`)

	marks := workspace.SensitiveMarks{"other.none": {"/id"}}
	path := "/resources/0/instances/0/attributes/input"

	if _, _, err := workspace.RevealValue(state, marks, path); err == nil {
		t.Fatal("identity-less resource succeeded, want a not-found error")
	} else if !errors.Is(err, workspace.ErrResourceNotFound) {
		t.Fatalf("identity-less resource error = %v, want a not-found error", err)
	}

	plain := []byte(`{"version":4,"outputs":{},"resources":[null]}`)

	if _, _, err := workspace.RevealValue(plain, marks, path); err == nil {
		t.Fatal("non-object resource succeeded, want a not-found error")
	} else if !errors.Is(err, workspace.ErrResourceNotFound) {
		t.Fatalf("non-object resource error = %v, want a not-found error", err)
	}
}

// TestRevealValueBoundsLargeStringToken pins the memory bound of the lookup.
// The state is about 20 MiB and holds one 2 MiB string in a resource before
// the target, so the lookup skips that string. jsontext buffers a string token
// whole while it skips it, so the heap growth may reach about four times the
// string per pass. The lookup reads the state three times (locate, pointers,
// answer), so the bound is 4 MiB plus four times the string per pass, and it
// must not grow with the size of the document.
//
//nolint:paralleltest // The heap measurement needs a quiet runtime, so this test never runs in parallel.
func TestRevealValueBoundsLargeStringToken(t *testing.T) {
	const (
		fillers    = 2600
		largeBytes = 2 << 20
	)

	filler := `{"mode":"managed","type":"terraform_data","name":"filler",` +
		`"instances":[{"attributes":{"id":"filler-id","blob":"` + strings.Repeat("x", 8000) + `"}}]}`

	large := `{"mode":"managed","type":"terraform_data","name":"large",` +
		`"instances":[{"attributes":{"id":"large-id","blob":"` + strings.Repeat("y", largeBytes) + `"}}]}`

	target := `{"mode":"managed","type":"terraform_data","name":"target",` +
		`"instances":[{"attributes":{"id":"target-id","input":"s3cr3t-large"},` +
		`"sensitive_attributes":[[{"type":"get_attr","value":"input"}]]}]}`

	var sb strings.Builder

	sb.WriteString(`{"version":4,"terraform_version":"1.11.0","serial":1,"lineage":"seed-lineage",` +
		`"outputs":{},"resources":[`)

	for range fillers {
		sb.WriteString(filler + ",")
	}

	sb.WriteString(large + "," + target + "]}")

	state := []byte(sb.String())
	sb.Reset()

	if len(state) < 20<<20 {
		t.Fatalf("state = %d bytes, want at least 20 MiB", len(state))
	}

	marks := workspace.SensitiveMarks{"terraform_data.target": {"/input"}}
	path := "/resources/" + strconv.Itoa(fillers+1) + "/instances/0/attributes/input"

	// Warm the path once, so one-time allocations stay out of the measurement.
	if _, _, err := workspace.RevealValue(state, marks, path); err != nil {
		t.Fatalf("warm reveal: %v", err)
	}

	runtime.GC() //nolint:revive // The heap measurement needs a quiet runtime, so the test collects first.
	runtime.GC() //nolint:revive // The heap measurement needs a quiet runtime, so the test collects first.

	var before runtime.MemStats

	runtime.ReadMemStats(&before)

	value, masked, err := workspace.RevealValue(state, marks, path)

	var after runtime.MemStats

	runtime.ReadMemStats(&after)

	if err != nil {
		t.Fatalf("reveal: %v", err)
	}

	if value != "s3cr3t-large" {
		t.Fatalf("value = %#v, want s3cr3t-large", value)
	}

	if len(masked) != 1 || masked[0] != path {
		t.Fatalf("masked = %q, want [%q]", masked, path)
	}

	bound := uint64(4<<20 + 12*largeBytes)
	if growth := after.TotalAlloc - before.TotalAlloc; growth > bound {
		t.Fatalf("allocated = %d bytes, want at most %d for a %d-byte state with one %d-byte string",
			growth, bound, len(state), largeBytes)
	}
}

// countCloseReader wraps a reader and counts closes.
type countCloseReader struct {
	*strings.Reader

	closes *int
}

// Close counts one close and reports success.
func (c *countCloseReader) Close() error {
	*c.closes++

	return nil
}

// errOpenerBoom is the injected open failure of the open error test.
var errOpenerBoom = errors.New("opener boom")

// TestRevealValueFromOpenerClosesStream proves the opener lookup closes
// every stream it opens and answers the same value as the bytes lookup. The
// lookup reads once per pass, so it opens at least twice.
func TestRevealValueFromOpenerClosesStream(t *testing.T) {
	t.Parallel()

	t.Run("marks", func(t *testing.T) {
		t.Parallel()
		checkOpenerClosesStream(t, revealMarks())
	})

	t.Run("no marks", func(t *testing.T) {
		t.Parallel()
		checkOpenerClosesStream(t, nil)
	})
}

// checkOpenerClosesStream proves one opener lookup closes every stream it
// opens and answers the same value as the bytes lookup.
func checkOpenerClosesStream(t *testing.T, marks workspace.SensitiveMarks) {
	t.Helper()

	masked, err := workspace.MaskedPaths([]byte(revealState()), marks)
	if err != nil {
		t.Fatalf("masked paths: %v", err)
	}

	if len(masked) == 0 {
		t.Fatal("masked holds no path")
	}

	path := masked[0]

	want, _, err := workspace.RevealValue([]byte(revealState()), marks, path)
	if err != nil {
		t.Fatalf("reveal: %v", err)
	}

	var opens, closes int

	open := func() (io.ReadCloser, error) {
		opens++

		return &countCloseReader{Reader: strings.NewReader(revealState()), closes: &closes}, nil
	}

	value, _, err := workspace.RevealValueFromOpener(open, marks, path)
	if err != nil {
		t.Fatalf("opener reveal: %v", err)
	}

	if value != want {
		t.Fatalf("value = %#v, want %#v", value, want)
	}

	if opens < 2 {
		t.Fatalf("opens = %d, want at least 2", opens)
	}

	if opens != closes {
		t.Fatalf("opens = %d, closes = %d, want equal", opens, closes)
	}
}

// TestRevealValueNoMarksLeafOnly proves a reveal with no marks answers only
// leaves below the attributes: objects, arrays, missing keys, past-the-end
// indexes and non-canonical index spellings answer not-found.
func TestRevealValueNoMarksLeafOnly(t *testing.T) {
	t.Parallel()

	state := []byte(conformanceState(
		`{"a":"x","b":{"c":[1,null,"z"]},"e":{}}`,
		`[]`,
	))
	base := "/resources/0/instances/0/attributes"

	for _, tc := range []struct {
		path string
		want any
	}{
		{path: base + "/a", want: "x"},
		{path: base + "/b/c/0", want: json.Number("1")},
		{path: base + "/b/c/1", want: nil},
		{path: base + "/b/c/2", want: "z"},
	} {
		checkNoMarksLeaf(t, state, tc.path, tc.want)
	}

	for _, path := range []string{
		base + "/b",
		base + "/b/c",
		base + "/e",
		base + "/b/c/3",
		base + "/nope",
		base + "/b/c/01",
	} {
		checkNoMarksNotFound(t, state, path)
	}
}

// checkNoMarksLeaf proves a reveal with no marks of path answers want with
// the path as its masked set.
func checkNoMarksLeaf(t *testing.T, state []byte, path string, want any) {
	t.Helper()

	value, masked, err := workspace.RevealValue(state, nil, path)
	if err != nil {
		t.Fatalf("reveal of %q: %v", path, err)
	}

	if len(masked) != 1 || masked[0] != path {
		t.Fatalf("masked = %q, want [%q]", masked, path)
	}

	checkNoMarksValue(t, path, value, want)
}

// checkNoMarksValue proves value answers want: numbers keep their exact
// encoding, so they compare as json.Number.
func checkNoMarksValue(t *testing.T, path string, value, want any) {
	t.Helper()

	number, numbered := want.(json.Number)
	if !numbered {
		if value != want {
			t.Fatalf("reveal of %q = %#v, want %#v", path, value, want)
		}

		return
	}

	got, ok := value.(json.Number)
	if !ok {
		t.Fatalf("reveal of %q = %#v, want a number", path, value)
	}

	if got.String() != number.String() {
		t.Fatalf("reveal of %q = %q, want %q", path, got.String(), number.String())
	}
}

// checkNoMarksNotFound proves a reveal with no marks of path answers a
// not-found error.
func checkNoMarksNotFound(t *testing.T, state []byte, path string) {
	t.Helper()

	if _, _, err := workspace.RevealValue(state, nil, path); err == nil {
		t.Errorf("reveal of %q succeeded, want a not-found error", path)
	} else if !errors.Is(err, workspace.ErrResourceNotFound) {
		t.Errorf("reveal of %q error = %v, want a not-found error", path, err)
	}
}

// TestRevealValueNoMarksDoesNotDecodeInstance proves a reveal with no marks
// near the end of a large instance keeps heap growth far below the size of
// the instance: it walks the stream without decoding the instance.
//
//nolint:paralleltest // The heap measurement needs a quiet runtime, so this test never runs in parallel.
func TestRevealValueNoMarksDoesNotDecodeInstance(t *testing.T) {
	// No t.Parallel: the heap measurement needs a quiet runtime.
	const members = 12000

	var attrs strings.Builder

	attrs.WriteString("{")

	for i := range members {
		if i > 0 {
			attrs.WriteString(",")
		}

		attrs.WriteString(strconv.Quote("k" + strconv.Itoa(i)))
		attrs.WriteString(":")
		attrs.WriteString(strconv.Quote(strings.Repeat("v", 1000)))
	}

	attrs.WriteString(`,"target":"s3cr3t-near-end"}`)

	state := []byte(conformanceState(attrs.String(), `[]`))
	attrs.Reset()

	if len(state) < 12_000_000 {
		t.Fatalf("state = %d bytes, want at least 12 MB", len(state))
	}

	path := "/resources/0/instances/0/attributes/target"

	// Warm the path once, so one-time allocations stay out of the
	// measurement.
	if _, _, err := workspace.RevealValue(state, nil, path); err != nil {
		t.Fatalf("warm reveal: %v", err)
	}

	runtime.GC() //nolint:revive // The heap measurement needs a quiet runtime, so the test collects first.
	runtime.GC() //nolint:revive // The heap measurement needs a quiet runtime, so the test collects first.

	var before runtime.MemStats

	runtime.ReadMemStats(&before)

	value, masked, err := workspace.RevealValue(state, nil, path)

	var after runtime.MemStats

	runtime.ReadMemStats(&after)

	if err != nil {
		t.Fatalf("reveal: %v", err)
	}

	if value != "s3cr3t-near-end" {
		t.Fatalf("value = %#v, want s3cr3t-near-end", value)
	}

	if len(masked) != 1 || masked[0] != path {
		t.Fatalf("masked = %q, want [%q]", masked, path)
	}

	// Bound found by measuring this test: growth is about 3.2 MB for a
	// 12 MB instance, far below 12 MB x 50 of the old full decode. The
	// bound is twice the measured growth, rounded up to a whole MiB.
	const bound = 7 << 20

	if growth := after.TotalAlloc - before.TotalAlloc; growth > bound {
		t.Fatalf(
			"heap allocated = %d bytes, want below %d for a %d-byte instance",
			growth,
			uint64(bound),
			len(state),
		)
	}
}

// TestRevealValueRejectsLargeSensitiveAttributes proves a lookup whose
// sensitive_attributes run over 1 MiB fails with ErrTooLarge.
func TestRevealValueRejectsLargeSensitiveAttributes(t *testing.T) {
	t.Parallel()

	const entries = 40000

	var sensitive strings.Builder

	sensitive.WriteString("[")

	for i := range entries {
		if i > 0 {
			sensitive.WriteString(",")
		}

		sensitive.WriteString(`[[{"type":"get_attr","value":"k` + strconv.Itoa(i) + `"}]]`)
	}

	sensitive.WriteString("]")

	marks := conformanceOtherMarks()
	state := []byte(conformanceState(`{"id":"a"}`, sensitive.String()))
	path := "/resources/0/instances/0/attributes/id"

	_, _, err := workspace.RevealValue(state, marks, path)
	if err == nil {
		t.Fatal("large sensitive_attributes succeeded, want a too-large error")
	}

	if !errors.Is(err, workspace.ErrTooLarge) {
		t.Fatalf("large sensitive_attributes error = %v, want a too-large error", err)
	}

	if !strings.Contains(err.Error(), "1048576") {
		t.Errorf("error = %v, want it to name the limit 1048576", err)
	}

	if !strings.Contains(err.Error(), strconv.Itoa(len(sensitive.String()))) {
		t.Errorf("error = %v, want it to name the real size %d", err, len(sensitive.String()))
	}
}

// largeMarksState builds a 12 MiB instance: 12000 members of 1000-byte
// strings with a secret and its copy at the end.
func largeMarksState(t *testing.T, members int, secret string) []byte {
	t.Helper()

	var attrs strings.Builder

	attrs.WriteString("{")

	for i := range members {
		if i > 0 {
			attrs.WriteString(",")
		}

		attrs.WriteString(strconv.Quote("m" + strconv.Itoa(i)))
		attrs.WriteString(":")
		attrs.WriteString(strconv.Quote(strings.Repeat("v", 1000)))
	}

	attrs.WriteString(`,"secret":` + strconv.Quote(secret))
	attrs.WriteString(`,"copy":` + strconv.Quote(secret))
	attrs.WriteString("}")

	state := []byte(conformanceState(
		attrs.String(),
		`[[{"type":"get_attr","value":"secret"}]]`,
	))
	attrs.Reset()

	if len(state) < 12_000_000 {
		t.Fatalf("state = %d bytes, want at least 12 MB", len(state))
	}

	return state
}

// checkLargeMarksValues proves the secret and its copy answer with the
// secret value, and a plain member does not answer.
func checkLargeMarksValues(
	t *testing.T, state []byte, marks workspace.SensitiveMarks, secret string,
) {
	t.Helper()

	secretPath := "/resources/0/instances/0/attributes/secret"
	copyPath := "/resources/0/instances/0/attributes/copy"
	plainPath := "/resources/0/instances/0/attributes/m5"

	value, masked, err := workspace.RevealValue(state, marks, secretPath)
	if err != nil {
		t.Fatalf("reveal secret: %v", err)
	}

	text, ok := value.(string)
	if !ok || text != secret {
		t.Fatalf("secret value = %#v, want the %d-byte secret", value, len(secret))
	}

	if len(masked) != 1 || masked[0] != secretPath {
		t.Fatalf("masked = %q, want [%q]", masked, secretPath)
	}

	dup, masked, err := workspace.RevealValue(state, marks, copyPath)
	if err != nil {
		t.Fatalf("reveal copy: %v", err)
	}

	if dup != secret {
		t.Fatal("copy value does not match the secret")
	}

	if len(masked) != 1 || masked[0] != copyPath {
		t.Fatalf("masked = %q, want [%q]", masked, copyPath)
	}

	if _, _, err := workspace.RevealValue(state, marks, plainPath); err == nil {
		t.Errorf("reveal of %q succeeded, want a not-found error", plainPath)
	} else if !errors.Is(err, workspace.ErrResourceNotFound) {
		t.Errorf("reveal of %q error = %v, want a not-found error", plainPath, err)
	}
}

// measureRevealGrowth warms the path once, then reports the heap growth of
// one reveal.
func measureRevealGrowth(
	t *testing.T, state []byte, marks workspace.SensitiveMarks, path string,
) uint64 {
	t.Helper()

	// Warm the path once, so one-time allocations stay out of the
	// measurement.
	if _, _, err := workspace.RevealValue(state, marks, path); err != nil {
		t.Fatalf("warm reveal: %v", err)
	}

	runtime.GC() //nolint:revive // The heap measurement needs a quiet runtime, so the test collects first.
	runtime.GC() //nolint:revive // The heap measurement needs a quiet runtime, so the test collects first.

	var before runtime.MemStats

	runtime.ReadMemStats(&before)

	if _, _, err := workspace.RevealValue(state, marks, path); err != nil {
		t.Fatalf("reveal: %v", err)
	}

	var after runtime.MemStats

	runtime.ReadMemStats(&after)

	growth := after.TotalAlloc - before.TotalAlloc
	t.Logf("heap growth = %d bytes for a %d-byte instance", growth, len(state))

	return growth
}

// checkRevealGrowth fails the test when growth runs over the bound.
func checkRevealGrowth(t *testing.T, growth, bound uint64, stateLen int) {
	t.Helper()

	if growth > bound {
		t.Fatalf(
			"heap allocated = %d bytes, want below %d for a %d-byte instance",
			growth,
			bound,
			stateLen,
		)
	}
}

// TestRevealValueMarksStreamsLargeInstance proves a reveal with marks near
// the end of a large instance keeps heap growth far below the size of the
// instance: with a disjoint pointer the lookup streams the answer.
//
//nolint:paralleltest // The heap measurement needs a quiet runtime, so this test never runs in parallel.
func TestRevealValueMarksStreamsLargeInstance(t *testing.T) {
	// No t.Parallel: the heap measurement needs a quiet runtime.
	secret := strings.Repeat("s", 64)
	marks := conformanceOtherMarks()
	state := largeMarksState(t, 12000, secret)

	checkLargeMarksValues(t, state, marks, secret)

	growth := measureRevealGrowth(
		t, state, marks, "/resources/0/instances/0/attributes/secret",
	)

	// Bound found by measuring this test: growth is about 5 MB for a
	// 12 MB instance, far below 12 MB x 50 of the old full decode. The
	// bound is twice the measured growth, rounded up to a whole MiB.
	const bound = 10 << 20

	checkRevealGrowth(t, growth, bound, len(state))
}

// containerState is one built secret-container state with its paths.
type containerState struct {
	state []byte
	paths containerPaths
}

// containerMarksState builds an instance with a 100000-member secret object
// and its duplicate.
func containerMarksState(t *testing.T, members int) containerState {
	t.Helper()

	var raw strings.Builder

	raw.WriteString("{")

	for i := range members {
		if i > 0 {
			raw.WriteString(",")
		}

		raw.WriteString(strconv.Quote("k" + strconv.Itoa(i)))
		raw.WriteString(":")
		raw.WriteString(strconv.Quote("vvvvv"))
	}

	raw.WriteString("}")

	attrs := `{"obj":` + raw.String() + `,"dup":` + raw.String() + `,"other":"x"}`
	raw.Reset()

	built := containerState{state: nil, paths: containerPaths{obj: "", dup: ""}}
	built.state = []byte(conformanceState(
		attrs,
		`[[{"type":"get_attr","value":"obj"}]]`,
	))

	if len(built.state) < 3_000_000 {
		t.Fatalf("state = %d bytes, want at least 3 MB", len(built.state))
	}

	built.paths = containerPaths{
		obj: "/resources/0/instances/0/attributes/obj",
		dup: "/resources/0/instances/0/attributes/dup",
	}

	return built
}

// containerPaths names the secret object and its duplicate.
type containerPaths struct {
	obj string
	dup string
}

// checkContainerMarksValues proves the secret object and its duplicate
// answer with all members.
func checkContainerMarksValues(
	t *testing.T, state []byte, marks workspace.SensitiveMarks, paths containerPaths, members int,
) {
	t.Helper()

	value, masked, err := workspace.RevealValue(state, marks, paths.obj)
	if err != nil {
		t.Fatalf("reveal: %v", err)
	}

	objValue, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("value type = %T, want an object", value)
	}

	if len(objValue) != members {
		t.Fatalf("value holds %d members, want %d", len(objValue), members)
	}

	if len(masked) != 1 || masked[0] != paths.obj {
		t.Fatalf("masked = %q, want [%q]", masked, paths.obj)
	}

	dupValue, _, err := workspace.RevealValue(state, marks, paths.dup)
	if err != nil {
		t.Fatalf("reveal dup: %v", err)
	}

	dupObj, ok := dupValue.(map[string]any)
	if !ok || len(dupObj) != members {
		t.Fatal("dup value does not match the secret object")
	}
}

// TestRevealValueMarksLargeSecretContainer proves a reveal of a large secret
// object answers: the answer itself is large, so the bound holds the decoded
// answer.
//
//nolint:paralleltest // The heap measurement needs a quiet runtime, so this test never runs in parallel.
func TestRevealValueMarksLargeSecretContainer(t *testing.T) {
	// No t.Parallel: the heap measurement needs a quiet runtime.
	const members = 100000

	marks := conformanceOtherMarks()
	built := containerMarksState(t, members)

	checkContainerMarksValues(t, built.state, marks, built.paths, members)

	growth := measureRevealGrowth(t, built.state, marks, built.paths.obj)

	// Bound found by measuring this test: growth is about 198 MB for a
	// 3 MB instance, because the answer is a 100000-member object held
	// decoded. The bound is twice the measured growth, rounded up to a
	// whole MiB.
	const bound = 400 << 20

	checkRevealGrowth(t, growth, bound, len(built.state))
}

// TestRevealValueEntangledLargeInstance proves nested pointers use the full
// decode: a 5 MiB instance fails with ErrTooLarge, while the same layout at
// 1 KiB answers.
func TestRevealValueEntangledLargeInstance(t *testing.T) {
	t.Parallel()

	marks := conformanceOtherMarks()
	path := "/resources/0/instances/0/attributes/a"
	sensitive := `[[{"type":"get_attr","value":"a"}],` +
		`[{"type":"get_attr","value":"a"},{"type":"get_attr","value":"b"}]]`

	big := conformanceState(
		`{"a":{"b":"secret","pad":`+strconv.Quote(strings.Repeat("p", 5<<20))+`}}`,
		sensitive,
	)

	_, _, err := workspace.RevealValue([]byte(big), marks, path)
	if err == nil {
		t.Fatal("large entangled instance succeeded, want a too-large error")
	}

	if !errors.Is(err, workspace.ErrTooLarge) {
		t.Fatalf("large entangled error = %v, want a too-large error", err)
	}

	if !strings.Contains(err.Error(), "4194304") {
		t.Errorf("error = %v, want it to name the limit 4194304", err)
	}

	small := conformanceState(
		`{"a":{"b":"secret","pad":`+strconv.Quote(strings.Repeat("p", 1<<10))+`}}`,
		sensitive,
	)

	value, masked, err := workspace.RevealValue([]byte(small), marks, path)
	if err != nil {
		t.Fatalf("small entangled reveal: %v", err)
	}

	_ = value

	if len(masked) != 1 || masked[0] != path {
		t.Fatalf("masked = %q, want [%q]", masked, path)
	}
}

// TestRevealValueWrapperLargeInstance proves a pointer through a dynamic
// wrapper uses the full decode: a 5 MiB instance fails with ErrTooLarge,
// while a small instance answers.
func TestRevealValueWrapperLargeInstance(t *testing.T) {
	t.Parallel()

	marks := conformanceOtherMarks()
	path := "/resources/0/instances/0/attributes/w/k"
	sensitive := `[[{"type":"get_attr","value":"w"},{"type":"get_attr","value":"k"}]]`

	big := conformanceState(
		`{"w":{"value":{"k":"s"},"type":"dynamic"},`+
			`"pad":`+strconv.Quote(strings.Repeat("p", 5<<20))+`}`,
		sensitive,
	)

	_, _, err := workspace.RevealValue([]byte(big), marks, path)
	if err == nil {
		t.Fatal("large wrapper instance succeeded, want a too-large error")
	}

	if !errors.Is(err, workspace.ErrTooLarge) {
		t.Fatalf("large wrapper error = %v, want a too-large error", err)
	}

	small := conformanceState(
		`{"w":{"value":{"k":"s"},"type":"dynamic"},"other":"x"}`,
		sensitive,
	)

	value, masked, err := workspace.RevealValue([]byte(small), marks, path)
	if err != nil {
		t.Fatalf("small wrapper reveal: %v", err)
	}

	if value != "s" {
		t.Fatalf("value = %#v, want s", value)
	}

	if len(masked) != 1 || masked[0] != path {
		t.Fatalf("masked = %q, want [%q]", masked, path)
	}
}

// TestRevealValueFromOpenerOpenError proves a failing open fails the lookup
// with an error that wraps the open error.
func TestRevealValueFromOpenerOpenError(t *testing.T) {
	t.Parallel()

	open := func() (io.ReadCloser, error) {
		return nil, errOpenerBoom
	}

	_, _, err := workspace.RevealValueFromOpener(open, revealMarks(), "/resources/0/instances/0/attributes/input")
	if err == nil {
		t.Fatal("open error succeeded, want an error")
	} else if !errors.Is(err, errOpenerBoom) {
		t.Fatalf("open error = %v, want it to wrap the open failure", err)
	}
}
