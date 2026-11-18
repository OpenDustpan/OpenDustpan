package stack_test

// Hostile tests for the masked read of a stack (the tool-neutral read of a
// pinned version): authorize at point of use, the boundary check for the
// version, the value path and the cursor, the limits of the read, and the
// refusals that reach the denial limiter. Each guard of the read has one
// table here, and each case names the layer it attacks. The adapter of the
// tool-neutral route maps the refusals of this package to the statuses of
// the REST API; the mapping is tested at the handler layer.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret/secrettest"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/stack/stacktest"
)

// maskedCheckpointLimit is the checkpoint size limit of the fixture of the
// masked read: large enough for the deployments of these tests.
const maskedCheckpointLimit = 1 << 20

// testCursorKey returns the key the tests sign cursors with.
func testCursorKey() []byte { return bytes.Repeat([]byte{0x2a}, 32) }

// person returns the principal "pat" of the tests.
func person() identity.Principal {
	return identity.Principal{
		Subject: "pat", DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Kind: identity.KindPerson, Credential: identity.CredentialAccessToken, JobID: "",
	}
}

// grantAuthorizer allows each action in grants on the object encoded in the
// key of the map. It checks the object, not only the action, as the test for
// the point of use requires.
type grantAuthorizer struct {
	grants map[string][]identity.Action
}

// Authorize implements identity.Authorizer.
func (g grantAuthorizer) Authorize(
	_ context.Context, _ identity.Principal, action identity.Action, obj identity.Object,
) error {
	if slices.Contains(g.grants[obj.String()], action) {
		return nil
	}

	return fmt.Errorf("%w: %s on %s", identity.ErrDenied, action, obj)
}

// ListObjects implements identity.Authorizer; the masked read never lists.
func (g grantAuthorizer) ListObjects(
	_ context.Context, _ identity.Principal, _ identity.Action,
) (identity.Scope, error) {
	return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}, nil
}

// readGrant returns the authorizer that grants unit:read on obj only.
func readGrant(obj identity.Object) grantAuthorizer {
	return grantAuthorizer{grants: map[string][]identity.Action{
		obj.String(): {identity.ActionUnitRead},
	}}
}

// unitObj returns the unit object of the stack the fixture serves, with its
// stable ID.
func unitObj(t *testing.T) identity.Object {
	t.Helper()

	obj, err := identity.UnitObject("pulumi", "acme", "web/dev")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	return obj.WithUnitID("stk-test")
}

// testCursor holds the payload values of a cursor that a test makes.
type testCursor struct {
	unit    string
	version string
	subject string
	offset  int
	expiry  time.Time
}

// mintCursor makes the cursor of a read with the payload values of c, for
// the hostile cases that reuse a cursor. It reimplements the wire format of
// the cursor on purpose, so a change of the format breaks these tests.
func mintCursor(t *testing.T, key []byte, c testCursor) string {
	t.Helper()

	return mintPaddedCursor(t, key, c, 0)
}

// mintPaddedCursor is mintCursor with an unknown field of pad bytes, so a
// cursor stays signed and bound but grows past the length cap.
func mintPaddedCursor(t *testing.T, key []byte, c testCursor, pad int) string {
	t.Helper()

	payload := fmt.Sprintf(`{"u":%q,"v":%q,"s":%q,"o":%d,"e":%d}`,
		c.unit, c.version, c.subject, c.offset, c.expiry.Unix())
	if pad > 0 {
		payload = payload[:len(payload)-1] + `,"x":"` + strings.Repeat("A", pad) + `"}`
	}

	mac := hmac.New(sha256.New, key)
	if _, err := mac.Write([]byte(payload)); err != nil {
		t.Fatalf("sign cursor: %v", err)
	}

	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// countingSink counts the refusals of a read and keeps the reasons.
type countingSink struct {
	objects []identity.Object
	reasons []string
}

// newSink returns an empty countingSink.
func newSink() *countingSink {
	return &countingSink{objects: nil, reasons: nil}
}

// refusal implements stack.RefusalSink.
func (c *countingSink) refusal(_ identity.Principal, obj identity.Object, reason string) {
	c.objects = append(c.objects, obj)
	c.reasons = append(c.reasons, reason)
}

// wantOne fails the test unless the sink holds exactly one refusal with
// reason.
func (c *countingSink) wantOne(t *testing.T, reason string) {
	t.Helper()

	if len(c.reasons) != 1 || c.reasons[0] != reason {
		t.Fatalf("sink reasons = %v, want one %q", c.reasons, reason)
	}
}

// secretJSON returns a secret value as the CLI writes it, for the markers of
// the fixture deployment. The plaintext is a well-known fake.
func secretJSON(plaintext string) string {
	return fmt.Sprintf(`{%q:%q,"ciphertext":%q}`, stateSigKey, stateSecretValue, plaintext)
}

// deploymentWith builds the inner document of a checkpoint with n password
// resources, each with one secret input and one secret output. The
// plaintext of each value is "s3cr3t-in-<i>" and "s3cr3t-out-<i>".
func deploymentWith(n int) string {
	var sb strings.Builder

	sb.WriteString(`{"resources":[`)

	for i := range n {
		if i > 0 {
			sb.WriteString(",")
		}

		fmt.Fprintf(
			&sb,
			`{"urn":"urn:pulumi:dev::web::random:index/password:Password::pw-%d",`+
				`"type":"random:index/password:Password",`+
				`"inputs":{"password":%s},"outputs":{"result":%s}}`,
			i, secretJSON(fmt.Sprintf("s3cr3t-in-%d", i)), secretJSON(fmt.Sprintf("s3cr3t-out-%d", i)),
		)
	}

	sb.WriteString(`]}`)

	return sb.String()
}

// saveVersion saves version 1 of the stack the fixture serves, with body as
// the inner document of the checkpoint, and completes the update.
func saveVersion(t *testing.T, f updatesFixture, body string) {
	t.Helper()

	s := f.started(t, stack.KindUpdate)

	err := f.svc.SaveCheckpoint(t.Context(), f.ref, s.Update.ID, s.Token, checkpoint(1, envelope(body)))
	if err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	if _, err := f.svc.Complete(t.Context(), f.ref, s.Update.ID, s.Token, stack.StatusSucceeded); err != nil {
		t.Fatalf("complete: %v", err)
	}
}

// maskedReader holds the arguments of the reads of one test. A test copies
// it and changes one field for a hostile case.
type maskedReader struct {
	f     updatesFixture
	obj   identity.Object
	sel   stack.StateSelector
	p     identity.Principal
	authz identity.Authorizer
}

// newMaskedReader saves version 1 with body on a new fixture and returns
// the reader of the version, with the grant of unit:read on the unit.
func newMaskedReader(t *testing.T, body string) maskedReader {
	t.Helper()

	f, _ := countFixture(t, maskedCheckpointLimit)
	saveVersion(t, f, body)

	obj := unitObj(t)

	return maskedReader{
		f: f, obj: obj, sel: stack.StateSelector{Latest: false, Version: 1},
		p: person(), authz: readGrant(obj),
	}
}

// read calls ReadMasked with the arguments of r, sink and cursor.
func (r maskedReader) read(t *testing.T, sink stack.RefusalSink, cursor string) (stack.MaskedPage, error) {
	t.Helper()

	return r.f.svc.ReadMasked(t.Context(), stack.ReadMaskedArgs{
		Ref: r.f.ref, Object: r.obj, Selector: r.sel,
		Principal: r.p, Authorizer: r.authz, Key: testCursorKey(), Sink: sink, Cursor: cursor,
	})
}

// countFixture is newUpdates with a checkpoint size limit and a counting
// blob store, for the tests that prove the read loads no content.
func countFixture(t *testing.T, maxCheckpointBytes int) (updatesFixture, *countingBlob) {
	t.Helper()

	clk := newClock()
	blobs := newCountingBlob()

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: clk.now, Name: "acme",
		PrimaryInstallation: "local", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}

	nsStore := &memNamespaces{ns: ns, reads: atomic.Int64{}}

	namespaces, err := namespace.NewService(
		namespace.Config{Store: nsStore, Now: clk.Now, Installation: "local"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	store := newMemUpdates()
	store.dbNow = clk.Now

	refusals := stacktest.NewRefusals()
	sealer := secrettest.NewMemorySealer()

	svc, err := stack.NewUpdates(stack.UpdatesConfig{
		Sealer: sealer, BlobGuard: blobguardtest.None{}, OnDeleteWait: nil, ManualLocks: nil,
		Updates: store, Tokens: nil, Checkpoints: store, Events: store, Namespaces: namespaces,
		Stacks: store, Blobs: blobs, Now: clk.Now, Logger: nil,
		MaxCheckpointBytes: maxCheckpointBytes, Journal: nil, Replayer: nil, Refusals: refusals,
	})
	if err != nil {
		t.Fatalf("new updates: %v", err)
	}

	ref, err := stack.ParseRef("acme", "web", "dev")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	store.nsID = ns.ID
	store.addStack(ref, "stk-test")

	return updatesFixture{
		svc: svc, store: store, blobs: blobs, clock: clk, namespaces: namespaces, ref: ref,
		refusals: refusals, nsID: ns.ID, nsStore: nsStore,
		prefix: "namespaces/" + ns.ID.String() + "/pulumi/", sealer: sealer,
	}, blobs
}

// checkMarkerPaths proves each marker of page is a secret with a value path
// that the strict parser accepts, in sorted order, and that the value paths
// of the page match the markers.
func checkMarkerPaths(t *testing.T, page stack.MaskedPage) {
	t.Helper()

	paths := make([]string, 0, len(page.Markers))
	for _, marker := range page.Markers {
		if !marker.Secret || marker.Path == "" {
			t.Errorf("marker %+v, want a secret with a value path", marker)
		}

		paths = append(paths, marker.Path)
	}

	if !slices.IsSorted(paths) {
		t.Errorf("paths = %q, want the sorted order for stable pages", paths)
	}

	if !slices.Equal(paths, page.ValuePaths) {
		t.Errorf("valuePaths = %q, want %q", page.ValuePaths, paths)
	}

	// Each JSON Pointer of the page passes the strict parser of the boundary
	// check: the server never emits a path that a client cannot parse.
	for _, path := range paths {
		if _, err := stack.ParseValuePath(path); err != nil {
			t.Fatalf("value path %q does not parse: %v", path, err)
		}
	}
}

// TestMaskedReadReplacesSecrets proves the masked read answers the markers
// of every secret value of the version, keyed by the JSON Pointer value
// path, and never a plain secret value.
func TestMaskedReadReplacesSecrets(t *testing.T) {
	t.Parallel()

	r := newMaskedReader(t, deploymentWith(3))

	page, err := r.read(t, nil, "")
	if err != nil {
		t.Fatalf("masked read: %v", err)
	}

	if page.Version != "1" {
		t.Errorf("version = %q, want %q", page.Version, "1")
	}

	if len(page.Markers) != 6 {
		t.Fatalf("markers = %d, want the markers of the 6 secret values", len(page.Markers))
	}

	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("marshal page: %v", err)
	}

	if bytes.Contains(raw, []byte("s3cr3t")) {
		t.Fatalf("the page holds a plain secret value, want markers only: %s", raw)
	}

	checkMarkerPaths(t, page)

	if page.NextCursor != "" {
		t.Errorf("nextCursor = %q, want empty for a one-page answer", page.NextCursor)
	}
}

// TestMaskedReadMarksSecretValuesInPlace proves a large deployment answers
// bounded pages: 200 markers per page with one opaque cursor, and the pages
// together hold each marker exactly once.
func TestMaskedReadMarksSecretValuesInPlace(t *testing.T) {
	t.Parallel()

	r := newMaskedReader(t, deploymentWith(125))

	// 125 resources, one secret input and one secret output each: 250
	// markers, two pages.
	page, err := r.read(t, nil, "")
	if err != nil {
		t.Fatalf("first page: %v", err)
	}

	if len(page.Markers) != 200 {
		t.Fatalf("first page holds %d markers, want 200", len(page.Markers))
	}

	if page.NextCursor == "" {
		t.Fatal("first page has no cursor, want one for the rest of the markers")
	}

	rest, err := r.read(t, nil, page.NextCursor)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}

	if len(rest.Markers) != 50 {
		t.Fatalf("second page holds %d markers, want 50", len(rest.Markers))
	}

	if rest.NextCursor != "" {
		t.Fatalf("second page has cursor %q, want empty", rest.NextCursor)
	}

	seen := make(map[string]bool, len(page.Markers)+len(rest.Markers))
	for _, marker := range slices.Concat(page.Markers, rest.Markers) {
		seen[marker.Path] = true
	}

	if len(seen) != 250 {
		t.Errorf("the pages hold %d unique paths, want 250", len(seen))
	}
}

// otherUnit returns the unit object of name in ns, with the stable ID id.
func otherUnit(t *testing.T, ns namespace.Name, name, id string) identity.Object {
	t.Helper()

	obj, err := identity.UnitObject("pulumi", ns, name)
	if err != nil {
		t.Fatalf("unit object %s/%s: %v", ns, name, err)
	}

	return obj.WithUnitID(id)
}

// TestMaskedReadDeniedWithoutGrant proves the read refuses a caller without
// unit:read on the unit, and a caller that holds the grant on a sibling
// unit, with no secret bytes and one refusal reported to the sink. The
// authorizer checks the object, not only the action. The HTTP shape of the
// answer (403 with the code unit.forbidden) is the handler layer; it is
// tested at the handler.
func TestMaskedReadDeniedWithoutGrant(t *testing.T) {
	t.Parallel()

	r := newMaskedReader(t, `{"resources":[{"urn":"urn:pulumi:dev::web::pulumi:pulumi:Stack::web-dev",`+
		`"type":"pulumi:pulumi:Stack","outputs":{"password":`+secretJSON("s3cr3t-out")+`}}]}`)

	cases := []struct {
		name  string
		authz grantAuthorizer
	}{
		{name: "no grant at all", authz: grantAuthorizer{grants: map[string][]identity.Action{}}},
		{name: "grant on a sibling unit", authz: readGrant(otherUnit(t, "acme", "web/other", "stk-other"))},
		{
			name:  "grant on the same name in another namespace",
			authz: readGrant(otherUnit(t, "team", "web/dev", "stk-other2")),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			denied := r
			denied.authz = tc.authz
			sink := newSink()

			page, err := denied.read(t, sink.refusal, "")
			if !errors.Is(err, identity.ErrDenied) {
				t.Fatalf("err = %v, want the permission denied of the unit", err)
			}

			if strings.Contains(fmt.Sprint(err), "s3cr3t") {
				t.Fatalf("the error holds secret bytes: %v", err)
			}

			if len(page.Markers) != 0 || len(page.ValuePaths) != 0 {
				t.Fatalf("page = %+v, want no marker and no path", page)
			}

			if len(sink.objects) != 1 || sink.objects[0].String() != r.obj.String() {
				t.Fatalf("sink objects = %v, want the one unit of the read", sink.objects)
			}

			sink.wantOne(t, stack.RefusalDenied)
		})
	}
}

// TestMaskedReadBoundaryRefusals proves each input of the read that the
// boundary check guards: the version, the JSON Pointer of a value path, and
// the cursor. Each refusal carries no secret bytes, and a cursor above the
// length cap never reaches the blob store.
func TestMaskedReadBoundaryRefusals(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		check func(t *testing.T)
	}{
		{"a version outside the allowlist", checkVersionAllowlist},
		{"bad json pointers", checkJSONPointers},
		{"a cursor over the length cap", checkOverlongCursor},
		{"a forged cursor", checkForgedCursor},
		{"an expired cursor or a cursor of another caller, unit or version", checkCursorBinding},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.check(t)
		})
	}
}

// checkVersionAllowlist proves the boundary check refuses a version outside
// 1..2^40 with the bad path of the read and one refusal.
func checkVersionAllowlist(t *testing.T) {
	t.Helper()

	r := newMaskedReader(t, deploymentWith(3))

	for _, bad := range []int64{-1, 0, 1<<40 + 1} {
		hostile := r
		hostile.sel = stack.StateSelector{Latest: false, Version: bad}
		sink := newSink()

		_, err := hostile.read(t, sink.refusal, "")
		if !errors.Is(err, stack.ErrBadPath) {
			t.Fatalf("version %d: err = %v, want the bad path of the read", bad, err)
		}

		sink.wantOne(t, stack.RefusalBadPath)
	}
}

// checkJSONPointers proves the strict parser of a value path refuses each
// malformed JSON Pointer with no echo of the input, and accepts the valid
// forms.
func checkJSONPointers(t *testing.T) {
	t.Helper()

	for _, input := range []string{"abc", "/a~", "/a~2", "/a\x7fb", "/a/b~c", "/\x01a"} {
		_, err := stack.ParseValuePath(input)
		if err == nil {
			t.Errorf("path %q: err = nil, want a refusal", input)

			continue
		}

		if strings.Contains(err.Error(), input) {
			t.Errorf("path %q: the error echoes the input: %v", input, err)
		}
	}

	for _, input := range []string{"", "/", "//", "/resources/1/inputs/password", "/a~1b", "/a~0b", "/a~01", "/a//b"} {
		if _, err := stack.ParseValuePath(input); err != nil {
			t.Errorf("path %q: err = %v, want nil", input, err)
		}
	}
}

// checkOverlongCursor proves a cursor over the length cap is refused without
// reading state.
func checkOverlongCursor(t *testing.T) {
	t.Helper()

	f, blobs := countFixture(t, maskedCheckpointLimit)
	saveVersion(t, f, deploymentWith(3))

	obj := unitObj(t)
	r := maskedReader{
		f: f, obj: obj, sel: stack.StateSelector{Latest: false, Version: 1},
		p: person(), authz: readGrant(obj),
	}

	sink := newSink()

	// The cursor is signed with the right key and bound to the right unit,
	// version and caller, and it has not expired: only its length is hostile.
	// The control proves the same cursor with no padding is accepted.
	valid := testCursor{
		unit: obj.String(), version: "1", subject: r.p.Subject, offset: 0,
		expiry: time.Now().Add(10 * time.Minute),
	}

	if _, err := r.read(t, nil, mintCursor(t, testCursorKey(), valid)); err != nil {
		t.Fatalf("control cursor: err = %v, want nil", err)
	}

	before := blobs.calls()

	overlong := mintPaddedCursor(t, testCursorKey(), valid, stack.MaxCursorBytes)
	if len(overlong) <= stack.MaxCursorBytes {
		t.Fatalf("test cursor has %d bytes, want more than %d", len(overlong), stack.MaxCursorBytes)
	}

	_, err := r.read(t, sink.refusal, overlong)
	if !errors.Is(err, stack.ErrBadPath) {
		t.Fatalf("err = %v, want the bad path of the read", err)
	}

	if got := blobs.calls(); got != before {
		t.Fatalf("blob calls = %d, want %d: the read must not open state for an overlong cursor", got, before)
	}

	sink.wantOne(t, stack.RefusalBadPath)
}

// checkForgedCursor proves a cursor with a changed payload, a changed
// signature or a changed format is refused with no echo of a secret.
func checkForgedCursor(t *testing.T) {
	t.Helper()

	r := newMaskedReader(t, deploymentWith(3))

	raw := mintCursor(t, testCursorKey(), testCursor{
		unit: r.obj.String(), version: "1", subject: r.p.Subject, offset: 1,
		expiry: time.Now().Add(10 * time.Minute),
	})

	// A freshly minted cursor proves the hostile cases reuse the wire
	// format and fail only on the tamper, not on the format.
	page, err := r.read(t, nil, raw)
	if err != nil {
		t.Fatalf("fresh cursor: err = %v, want nil", err)
	}

	if len(page.Markers) == 0 {
		t.Fatal("fresh cursor answered no marker, want the markers from the offset")
	}

	payload, sig, ok := strings.Cut(raw, ".")
	if !ok {
		t.Fatalf("cursor %q: want payload and signature", raw)
	}

	tampered := []string{
		payload[:len(payload)-1] + "A." + sig,
		payload + "." + sig[:len(sig)-1] + "A",
		raw + "x",
		"abc",
	}

	for _, cursor := range tampered {
		sink := newSink()

		_, err := r.read(t, sink.refusal, cursor)
		if !errors.Is(err, stack.ErrBadPath) {
			t.Errorf("cursor %q: err = %v, want the bad path of the read", cursor, err)
		}

		if strings.Contains(fmt.Sprint(err), "s3cr3t") {
			t.Errorf("cursor %q: the error echoes a secret", cursor)
		}
	}
}

// checkCursorBinding proves a cursor that has expired, or that names another
// caller, another unit or another version, is refused with the bad path of
// the read and one refusal.
func checkCursorBinding(t *testing.T) {
	t.Helper()

	r := newMaskedReader(t, deploymentWith(3))
	later := time.Now().Add(10 * time.Minute)

	cases := []struct {
		name   string
		cursor testCursor
	}{
		{
			name: "expired",
			cursor: testCursor{
				unit: r.obj.String(), version: "1", subject: r.p.Subject, offset: 0,
				expiry: time.Date(2023, time.November, 14, 0, 0, 0, 0, time.UTC),
			},
		},
		{
			name:   "another caller",
			cursor: testCursor{unit: r.obj.String(), version: "1", subject: "mallory", offset: 0, expiry: later},
		},
		{
			name: "another unit",
			cursor: testCursor{
				unit: "pulumi:acme/web/other", version: "1", subject: r.p.Subject, offset: 0, expiry: later,
			},
		},
		{
			name:   "another version",
			cursor: testCursor{unit: r.obj.String(), version: "2", subject: r.p.Subject, offset: 0, expiry: later},
		},
	}

	for _, tc := range cases {
		sink := newSink()

		_, err := r.read(t, sink.refusal, mintCursor(t, testCursorKey(), tc.cursor))
		if !errors.Is(err, stack.ErrBadPath) {
			t.Fatalf("%s: err = %v, want the bad path of the read", tc.name, err)
		}

		sink.wantOne(t, stack.RefusalBadPath)
	}
}

// TestMaskedReadOverSizeRefusedBeforeLoad proves a version above 5 MB is
// refused with the size error before the server holds its content: the
// version record, not the blob, decides. The read makes no call to the blob
// store.
func TestMaskedReadOverSizeRefusedBeforeLoad(t *testing.T) {
	t.Parallel()

	f, blobs := countFixture(t, 16<<20)
	saveVersion(t, f, `{"resources":[],"pad":"`+strings.Repeat("a", 6<<20)+`"}`)

	obj := unitObj(t)
	r := maskedReader{
		f: f, obj: obj, sel: stack.StateSelector{Latest: false, Version: 1},
		p: person(), authz: readGrant(obj),
	}

	before := blobs.calls()
	sink := newSink()

	_, err := r.read(t, sink.refusal, "")
	if !errors.Is(err, stack.ErrTooLarge) {
		t.Fatalf("err = %v, want the size error of the read", err)
	}

	if got := blobs.calls(); got != before {
		t.Fatalf("blob calls = %d, want %d: the read must not open the content of an oversized version",
			got, before)
	}

	sink.wantOne(t, stack.RefusalTooLarge)
}

// TestMaskedReadDeadlineAnswers504 proves a read whose context is past its
// deadline fails closed before any work: the answer carries no bytes, and
// the refusal is reported to the sink. The adapter answers 504 for the
// deadline error; that is tested at the handler.
func TestMaskedReadDeadlineAnswers504(t *testing.T) {
	t.Parallel()

	r := newMaskedReader(t, deploymentWith(1))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	sink := newSink()

	page, err := r.f.svc.ReadMasked(ctx, stack.ReadMaskedArgs{
		Ref: r.f.ref, Object: r.obj, Selector: r.sel,
		Principal: r.p, Authorizer: r.authz, Key: testCursorKey(), Sink: sink.refusal, Cursor: "",
	})
	if !errors.Is(err, stack.ErrReadDeadline) {
		t.Fatalf("err = %v, want the deadline error of the read", err)
	}

	if len(page.Markers) != 0 || len(page.ValuePaths) != 0 {
		t.Fatalf("page = %+v, want no marker and no path", page)
	}

	sink.wantOne(t, stack.RefusalDeadline)
}

// TestMaskedReadRefusalsCounted proves one caller's repeated refusals reach
// the shared denial limiter once per refusal, keyed by the principal, the
// action, the unit kind and the stable unit ID. The HTTP adapter binds the
// server's denial limiter as the sink, so the entries of the refusals of one
// key hold for the window; the limiter drops no refusal from the count.
func TestMaskedReadRefusalsCounted(t *testing.T) {
	t.Parallel()

	r := newMaskedReader(t, deploymentWith(1))
	r.sel = stack.StateSelector{Latest: false, Version: -1}

	fixed := time.Date(2026, time.October, 9, 12, 0, 0, 0, time.UTC)

	limiter := denylimit.New(denylimit.Config{
		Window: 0, MaxKeys: 0, MaxFlush: 0, MaxKeysPerPrincipal: 0,
		Now: func() time.Time { return fixed },
	})

	writes := 0

	sink := stack.RefusalSink(func(p identity.Principal, unit identity.Object, _ string) {
		d := limiter.Allow(denylimit.Key{
			Principal: p.Subject, Action: string(identity.ActionUnitRead),
			Kind: "unit:pulumi", Scope: unit.UnitID(),
		})

		if d.Write {
			writes++
		}
	})

	for range 5 {
		if _, err := r.read(t, sink, ""); !errors.Is(err, stack.ErrBadPath) {
			t.Fatalf("err = %v, want the bad path of the read", err)
		}
	}

	if writes != 1 {
		t.Fatalf("entries that the limiter allows = %d, want 1 in the window of the 5 refusals", writes)
	}

	if got := limiter.Len(); got != 1 {
		t.Fatalf("keys held by the limiter = %d, want 1 for one principal and unit", got)
	}
}
