package workspace_test

// Hostile tests for the workspace layer of the masked read: the cursor
// (signature, binding, expiry, length), the version ID allowlist, the size
// limit before and after the load, and the deadline. Each case sends its
// input directly to this layer, as if the handler had not checked it. Each
// guard has a case that fails when the guard is removed.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// refusalLog collects the reasons that a masked read reports to its sink.
type refusalLog struct {
	mu      sync.Mutex
	reasons []string
}

// sink returns the RefusalSink that writes to the log.
func (l *refusalLog) sink() workspace.RefusalSink {
	return func(_ identity.Principal, _ identity.Object, reason string) {
		l.mu.Lock()
		defer l.mu.Unlock()

		l.reasons = append(l.reasons, reason)
	}
}

// got returns a copy of the reasons so far.
func (l *refusalLog) got() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return append([]string(nil), l.reasons...)
}

// maskedHostile is a fixture for the hostile tests: the workspace of the
// test state, a caller with unit:read, and a log of refusals.
type maskedHostile struct {
	f     fixture
	obj   identity.Object
	id    workspace.StateVersionID
	authz grantAuthorizer
	log   *refusalLog
}

// newMaskedHostile writes the test state through a counting blob store.
func newMaskedHostile(t *testing.T, state string, maxState int64) (*maskedHostile, *countingBlobs) {
	t.Helper()

	counted := &countingBlobs{Store: openFixtureBlobs(t), mu: sync.Mutex{}, gets: 0, failGet: false}
	f := newFixtureSized(t, counted, blobguardtest.None{}, nil, maxState)

	if _, err := f.svc.LockWithID(t.Context(), f.ws.ID, "alice", "apply", "lock-1"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	sv, err := f.svc.WriteState(
		t.Context(), f.ws.ID,
		workspace.StreamInput{Holder: "alice", LockID: "lock-1", MD5: ""},
		streamAuthority(noOverrides(), nil), strings.NewReader(state),
	)
	if err != nil {
		t.Fatalf("write state: %v", err)
	}

	obj := unitObjectOf(t, f)

	return &maskedHostile{
		f: f, obj: obj, id: sv.ID, log: &refusalLog{mu: sync.Mutex{}, reasons: nil},
		authz: grantAuthorizer{grants: map[string][]identity.Action{obj.String(): {identity.ActionUnitRead}}},
	}, counted
}

// read runs one masked read as the subject "pat".
func (h *maskedHostile) read(
	ctx context.Context, sel workspace.VersionSelector, cursor string,
) (workspace.MaskedPage, error) {
	return h.f.svc.ReadMasked(ctx, workspace.ReadMaskedArgs{
		Ref: h.f.ws.Ref, Object: h.obj, Selector: sel,
		Principal: person("pat"), Authorizer: h.authz, Key: testCursorKey(), Sink: h.log.sink(), Cursor: cursor,
	})
}

// latest is the selector of the current version.
func latest() workspace.VersionSelector { return workspace.VersionSelector{Latest: true, ID: ""} }

// setSize changes the size that the record of a version holds, as a record
// that understates or overstates its content.
func (h *maskedHostile) setSize(id workspace.StateVersionID, size int64) {
	h.f.store.mu.Lock()
	defer h.f.store.mu.Unlock()

	sv := h.f.store.versions[id]
	sv.Size = size
	h.f.store.versions[id] = sv
}

// bigState returns a valid state document whose length is at least n bytes.
func bigState(n int) string {
	return `{"version":4,"terraform_version":"1.11.0","serial":1,"lineage":"lin-big",` +
		`"outputs":{"big":{"value":"` + strings.Repeat("A", n) + `","type":"string"}},"resources":[]}`
}

// manySecretsState returns a state with n sensitive outputs.
func manySecretsState(n int) string {
	var b strings.Builder

	b.WriteString(`{"version":4,"terraform_version":"1.11.0","serial":1,"lineage":"lin-many","outputs":{`)

	for i := range n {
		if i > 0 {
			b.WriteString(",")
		}

		fmt.Fprintf(&b, `"o%04d":{"value":"s3cr3t-%d","type":"string","sensitive":true}`, i, i)
	}

	b.WriteString(`},"resources":[]}`)

	return b.String()
}

// cursorCase is one hostile cursor with the name of its attack.
type cursorCase struct {
	name   string
	cursor string
}

// cursorSplit is the two parts of a cursor.
type cursorSplit struct {
	payload string
	sig     string
}

// cursorParts splits a cursor into its payload and its signature.
func cursorParts(t *testing.T, cursor string) cursorSplit {
	t.Helper()

	payload, sig, ok := strings.Cut(cursor, ".")
	if !ok {
		t.Fatalf("cursor %q has no dot", cursor)
	}

	return cursorSplit{payload: payload, sig: sig}
}

// hostileCursors returns the bad cursors of the tests, each signed with the
// key of the test unless its attack is the signature.
func hostileCursors(t *testing.T, h *maskedHostile, good string) []cursorCase {
	t.Helper()

	spec := workspace.MaskedCursorSpec{
		Unit: h.obj.String(), Version: string(h.id), Subject: "pat", Offset: 0, At: *h.f.now,
	}

	mint := func(key []byte, edit func(*workspace.MaskedCursorSpec)) string {
		s := spec
		edit(&s)

		c, err := workspace.NewTestCursor(key, s)
		if err != nil {
			t.Fatalf("mint: %v", err)
		}

		return c
	}

	key := testCursorKey()
	same := func(*workspace.MaskedCursorSpec) {}
	goodParts := cursorParts(t, good)
	goodPayload, goodSig := goodParts.payload, goodParts.sig
	otherPayload := cursorParts(t, mint(key, func(s *workspace.MaskedCursorSpec) { s.Offset = 5 })).payload

	long, err := json.Marshal(map[string]any{
		"u": spec.Unit, "v": spec.Version, "s": spec.Subject, "o": 0, "e": spec.At.Add(time.Minute).Unix(),
		"x": strings.Repeat("A", workspace.MaskedMaxCursorBytes),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	overlong := workspace.NewTestSignedCursor(key, long)
	if len(overlong) <= workspace.MaskedMaxCursorBytes {
		t.Fatalf("test cursor has %d bytes, want more than %d", len(overlong), workspace.MaskedMaxCursorBytes)
	}

	return []cursorCase{
		{"signature of another key", mint([]byte(strings.Repeat("!", 32)), same)},
		{"altered signature", goodPayload + "." + goodSig[:len(goodSig)-2] + "AA"},
		{"payload of another offset with the old signature", otherPayload + "." + goodSig},
		{"bound to another unit", mint(key, func(s *workspace.MaskedCursorSpec) { s.Unit += "x" })},
		{"bound to another version", mint(key, func(s *workspace.MaskedCursorSpec) { s.Version += "x" })},
		{"bound to another caller", mint(key, func(s *workspace.MaskedCursorSpec) { s.Subject = "mallory" })},
		{"expired", mint(key, func(s *workspace.MaskedCursorSpec) { s.At = s.At.Add(-11 * time.Minute) })},
		{"negative offset", mint(key, func(s *workspace.MaskedCursorSpec) { s.Offset = -1 })},
		{"no dot", goodPayload + goodSig},
		{"two dots", good + ".AAAA"},
		{"payload not base64", "!!!." + goodSig},
		{"empty payload", "." + goodSig},
		{"signature of a short size", goodPayload + ".AAAA"},
		{"signed, bound and over the length cap", overlong},
	}
}

// TestMaskedReadCursorIsHostileProof sends each bad cursor to the service: a
// bad signature, another key, another binding, an expired cursor, a bad shape
// and a cursor over the length cap. Each one is refused with ErrBadPath and
// the reason "bad-path", with no marker in the answer.
func TestMaskedReadCursorIsHostileProof(t *testing.T) {
	t.Parallel()

	h, _ := newMaskedHostile(t, manySecretsState(10), 4<<20)

	good, err := workspace.NewTestCursor(testCursorKey(), workspace.MaskedCursorSpec{
		Unit: h.obj.String(), Version: string(h.id), Subject: "pat", Offset: 0, At: *h.f.now,
	})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	if _, err := h.read(t.Context(), latest(), good); err != nil {
		t.Fatalf("a good cursor: %v", err)
	}

	cases := hostileCursors(t, h, good)

	// The checks over all cases run after the parallel subtests end.
	t.Cleanup(func() { wantBadPathRefusals(t, h.log, len(cases)) })

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkRefusedCursor(t, h, tc)
		})
	}
}

// wantBadPathRefusals checks that the sink saw n refusals, all "bad-path".
func wantBadPathRefusals(t *testing.T, log *refusalLog, n int) {
	t.Helper()

	reasons := log.got()
	if len(reasons) != n {
		t.Errorf("sink saw %d refusals, want %d", len(reasons), n)
	}

	for _, reason := range reasons {
		if reason != workspace.RefusalBadPath {
			t.Errorf("reason = %q, want %q", reason, workspace.RefusalBadPath)
		}
	}
}

// checkRefusedCursor sends one hostile cursor and checks the refusal: the
// error, the empty page and no echo of the cursor.
func checkRefusedCursor(t *testing.T, h *maskedHostile, tc cursorCase) {
	t.Helper()

	page, err := h.read(t.Context(), latest(), tc.cursor)
	if !errors.Is(err, workspace.ErrBadPath) {
		t.Fatalf("err = %v, want ErrBadPath", err)
	}

	if len(page.Markers) != 0 || page.NextCursor != "" {
		t.Fatalf("page = %+v, want an empty page", page)
	}

	if strings.Contains(fmt.Sprint(err), tc.cursor) {
		t.Fatalf("the error echoes the cursor: %v", err)
	}
}

// readAllPages follows the cursors of the read to the last page and returns
// the paths of all markers and the number of pages.
func readAllPages(t *testing.T, h *maskedHostile) ([]string, int) {
	t.Helper()

	var seen []string

	cursor, pages := "", 0

	for {
		page, err := h.read(t.Context(), latest(), cursor)
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}

		pages++

		if len(page.Markers) > workspace.MaskedPageMarkers {
			t.Fatalf("page %d has %d markers, want at most %d", pages, len(page.Markers), workspace.MaskedPageMarkers)
		}

		for _, m := range page.Markers {
			seen = append(seen, m.Path)
		}

		if page.NextCursor == "" {
			return seen, pages
		}

		cursor = page.NextCursor
	}
}

// TestMaskedReadPagesAreStable proves a state with more markers than one page
// answers the pages in order through the cursor, with no repeat and no gap.
func TestMaskedReadPagesAreStable(t *testing.T) {
	t.Parallel()

	total := workspace.MaskedPageMarkers*2 + 50
	h, _ := newMaskedHostile(t, manySecretsState(total), 4<<20)

	seen, pages := readAllPages(t, h)
	if pages != 3 || len(seen) != total {
		t.Fatalf("pages = %d, markers = %d, want 3 pages and %d markers", pages, len(seen), total)
	}

	uniq := map[string]bool{}

	for _, p := range seen {
		if uniq[p] {
			t.Fatalf("path %q repeats across pages", p)
		}

		uniq[p] = true
	}
}

// TestMaskedReadCursorOfAnotherCaller proves the cursor of one caller does not
// open a page for another caller: the binding holds on a cursor the server
// made, not only on a forged one.
func TestMaskedReadCursorOfAnotherCaller(t *testing.T) {
	t.Parallel()

	h, _ := newMaskedHostile(t, manySecretsState(workspace.MaskedPageMarkers+1), 4<<20)

	page, err := h.read(t.Context(), latest(), "")
	if err != nil || page.NextCursor == "" {
		t.Fatalf("first page: cursor %q, err %v, want a cursor", page.NextCursor, err)
	}

	_, err = h.f.svc.ReadMasked(t.Context(), workspace.ReadMaskedArgs{
		Ref: h.f.ws.Ref, Object: h.obj, Selector: latest(),
		Principal: person("mallory"), Authorizer: h.authz, Key: testCursorKey(), Sink: nil, Cursor: page.NextCursor,
	})
	if !errors.Is(err, workspace.ErrBadPath) {
		t.Fatalf("err = %v, want ErrBadPath", err)
	}
}

// TestMaskedReadVersionAllowlist sends each hostile version ID to the
// service. Each one is refused before the store is asked for it, with no blob
// read.
func TestMaskedReadVersionAllowlist(t *testing.T) {
	t.Parallel()

	h, counted := newMaskedHostile(t, manySecretsState(2), 4<<20)
	before := counted.reads()

	ids := []string{
		"", "sv-", "SV-abc", "abc", "sv-ABC", "sv-a b", "sv-a/b", "sv-../x", "sv-a\nb", "sv-é", "sv-a_b",
		"sv-" + strings.Repeat("a", 62), "sv-%00",
	}

	for _, id := range ids {
		t.Run(fmt.Sprintf("%q", id), func(t *testing.T) {
			t.Parallel()

			_, err := h.read(
				t.Context(),
				workspace.VersionSelector{Latest: false, ID: workspace.StateVersionID(id)},
				"",
			)
			if id == "sv-" {
				// A bare prefix has no letters but passes the shape; the store
				// then answers not found.
				if !errors.Is(err, workspace.ErrStateVersionNotFound) && !errors.Is(err, workspace.ErrBadPath) {
					t.Fatalf("err = %v, want a refusal", err)
				}

				return
			}

			if !errors.Is(err, workspace.ErrBadPath) {
				t.Fatalf("err = %v, want ErrBadPath", err)
			}
		})
	}

	if counted.reads() != before {
		t.Errorf("blob reads = %d, want %d: a refused ID reached the blob store", counted.reads(), before)
	}

	// A well-formed ID of no version is not found, not a bad path.
	_, err := h.read(t.Context(), workspace.VersionSelector{Latest: false, ID: "sv-nosuchversion"}, "")
	if !errors.Is(err, workspace.ErrStateVersionNotFound) {
		t.Fatalf("unknown version: err = %v, want ErrStateVersionNotFound", err)
	}
}

// TestMaskedReadRefusesAnOversizedVersion proves the 5 MB limit at both
// places: the record that names a size over the limit is refused before the
// blob is read, and a record that understates its content is refused after
// the load.
func TestMaskedReadRefusesAnOversizedVersion(t *testing.T) {
	t.Parallel()

	t.Run("the record names a size over the limit", func(t *testing.T) {
		t.Parallel()

		h, counted := newMaskedHostile(t, manySecretsState(2), 4<<20)
		h.setSize(h.id, workspace.MaskedMaxVersionBytes+1)

		before := counted.reads()

		_, err := h.read(t.Context(), latest(), "")
		if !errors.Is(err, workspace.ErrTooLarge) {
			t.Fatalf("err = %v, want ErrTooLarge", err)
		}

		if counted.reads() != before {
			t.Errorf("blob reads = %d, want %d: the oversized version was loaded", counted.reads(), before)
		}

		if got := h.log.got(); len(got) != 1 || got[0] != workspace.RefusalTooLarge {
			t.Errorf("reasons = %q, want [%q]", got, workspace.RefusalTooLarge)
		}
	})

	t.Run("the record understates the content", func(t *testing.T) {
		t.Parallel()

		h, _ := newMaskedHostile(t, bigState(workspace.MaskedMaxVersionBytes+1024), 8<<20)
		h.setSize(h.id, 10)

		page, err := h.read(t.Context(), latest(), "")
		if !errors.Is(err, workspace.ErrTooLarge) {
			t.Fatalf("err = %v, want ErrTooLarge", err)
		}

		if len(page.Markers) != 0 {
			t.Fatalf("page = %+v, want an empty page", page)
		}

		if got := h.log.got(); len(got) != 1 || got[0] != workspace.RefusalTooLarge {
			t.Errorf("reasons = %q, want [%q]", got, workspace.RefusalTooLarge)
		}
	})
}

// TestMaskedReadDeadlineBeforeTheLoad proves a context that is past its
// deadline is refused at the start, with no blob read and one refusal.
func TestMaskedReadDeadlineBeforeTheLoad(t *testing.T) {
	t.Parallel()

	h, counted := newMaskedHostile(t, manySecretsState(2), 4<<20)
	before := counted.reads()

	ctx, cancel := context.WithTimeout(t.Context(), -time.Second)
	defer cancel()

	_, err := h.read(ctx, latest(), "")
	if !errors.Is(err, workspace.ErrReadDeadline) {
		t.Fatalf("err = %v, want ErrReadDeadline", err)
	}

	if counted.reads() != before {
		t.Errorf("blob reads = %d, want %d", counted.reads(), before)
	}

	if got := h.log.got(); len(got) != 1 || got[0] != workspace.RefusalDeadline {
		t.Errorf("reasons = %q, want [%q]", got, workspace.RefusalDeadline)
	}
}

// TestMaskedReadLoadStopsBetweenChunks proves the chunked load stops when the
// context ends between two chunks, and that a live context loads the whole
// content.
func TestMaskedReadLoadStopsBetweenChunks(t *testing.T) {
	t.Parallel()

	t.Run("the context ends between two chunks", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		src := &cancelAfterReader{
			r: bytes.NewReader(make([]byte, 4*workspace.MaskedChunkBytes)), cancel: cancel, after: 1, reads: 0,
		}

		got, err := workspace.LoadMaskedChunks(ctx, src)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}

		if len(got) != 0 {
			t.Errorf("got %d bytes with the error, want none", len(got))
		}

		if src.reads > 2 {
			t.Errorf("reader was read %d times, want the load to stop after the cancel", src.reads)
		}
	})

	t.Run("a live context loads the whole content in chunks", func(t *testing.T) {
		t.Parallel()

		want := bytes.Repeat([]byte("x"), 2*workspace.MaskedChunkBytes+17)

		got, err := workspace.LoadMaskedChunks(t.Context(), bytes.NewReader(want))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("read = %d bytes, %v; want %d bytes", len(got), err, len(want))
		}
	})
}

// cancelAfterReader cancels its context after `after` reads.
type cancelAfterReader struct {
	r      io.Reader
	cancel context.CancelFunc
	after  int
	reads  int
}

// Read implements io.Reader.
func (c *cancelAfterReader) Read(p []byte) (int, error) {
	c.reads++

	if c.reads > c.after {
		c.cancel()
	}

	n, err := c.r.Read(p)
	if err != nil {
		return n, fmt.Errorf("read: %w", err)
	}

	return n, nil
}

// TestMaskedReadUsesTheSensitiveValuesMarks proves the marks that the
// sensitive_values of the json-state rendering name reach the read: a nested
// attribute that only the rendering marks is a marker, and an unmarked
// attribute of the same resource is not.
func TestMaskedReadUsesTheSensitiveValuesMarks(t *testing.T) {
	t.Parallel()

	state := `{"version":4,"terraform_version":"1.11.0","serial":2,"lineage":"lin-sv","outputs":{},` +
		`"resources":[{"mode":"managed","type":"vendor_thing","name":"cfg",` +
		`"provider":"provider[\"registry.opentofu.org/vendor/thing\"]",` +
		`"instances":[{"schema_version":0,` +
		`"attributes":{"id":"cfg-id","name":"visible-name","config":{"token":"s3cr3t-tok","region":"eu"},` +
		`"keys":["s3cr3t-k0","plain"]},"sensitive_attributes":[]}]}]}`

	rendering := `{"format_version":"1.2","values":{"root_module":{"resources":[` +
		`{"address":"vendor_thing.cfg","type":"vendor_thing","name":"cfg",` +
		`"values":{"id":"cfg-id","name":"visible-name","config":{"token":"s3cr3t-tok","region":"eu"},` +
		`"keys":["s3cr3t-k0","plain"]},` +
		`"sensitive_values":{"config":{"token":true},"keys":[true,false]}}]}}}`

	marks, err := workspace.ExtractSensitiveMarks([]byte(rendering))
	if err != nil {
		t.Fatalf("extract marks: %v", err)
	}

	h, _ := newMaskedHostile(t, state, 4<<20)

	fence := namespace.Fence{Namespace: "acme", FailoverVersion: 1}

	err = h.f.store.SaveSensitiveMarks(t.Context(), h.id, marks, fence)
	if err != nil {
		t.Fatalf("save marks: %v", err)
	}

	page, err := h.read(t.Context(), latest(), "")
	if err != nil {
		t.Fatalf("masked read: %v", err)
	}

	want := []string{
		"/resources/0/instances/0/attributes/config/token",
		"/resources/0/instances/0/attributes/keys/0",
	}

	got := make([]string, 0, len(page.Markers))
	for _, m := range page.Markers {
		got = append(got, m.Path)
	}

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("markers = %q, want %q", got, want)
	}

	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if strings.Contains(string(raw), "s3cr3t") {
		t.Fatalf("the page holds a plain secret: %s", raw)
	}
}
