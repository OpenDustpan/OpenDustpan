package workspace_test

import (
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
	if _, _, err := workspace.RevealValueFromReader(newStream(), marks, path); err != nil {
		t.Fatalf("warm reveal: %v", err)
	}

	runtime.GC() //nolint:revive // The heap measurement needs a quiet runtime, so the test collects first.
	runtime.GC() //nolint:revive // The heap measurement needs a quiet runtime, so the test collects first.

	var before runtime.MemStats

	runtime.ReadMemStats(&before)

	stream := newStream()

	value, masked, err := workspace.RevealValueFromReader(stream, marks, path)

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

	if stream.total < 20<<20 {
		t.Fatalf("stream served %d bytes, want at least 20 MiB", stream.total)
	}

	if growth := after.TotalAlloc - before.TotalAlloc; growth > 4<<20 {
		t.Fatalf(
			"heap allocated = %d bytes, want below 4 MiB for a %d-byte state",
			growth,
			stream.total,
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
// string. The bound is 4 MiB plus four times the string, and it must not grow
// with the size of the document.
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

	bound := uint64(4<<20 + 4*largeBytes)
	if growth := after.TotalAlloc - before.TotalAlloc; growth > bound {
		t.Fatalf("allocated = %d bytes, want at most %d for a %d-byte state with one %d-byte string",
			growth, bound, len(state), largeBytes)
	}
}
