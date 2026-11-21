package profile_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
)

// openSession opens the root profile with body and returns the session ID.
func openSession(t *testing.T, f fixture, body string) string {
	t.Helper()

	const name = "root"

	got := f.do(t, send("admin", http.MethodPost, base+"/"+name+"/open", body))
	if got.code != http.StatusCreated {
		t.Fatalf("open %s: %d %s, want 201", name, got.code, got.body)
	}

	var wire openWire
	if err := json.Unmarshal([]byte(got.body), &wire); err != nil {
		t.Fatalf("decode open: %v", err)
	}

	if wire.SessionID == "" || wire.ExpiresAt == "" {
		t.Fatalf("open answers %+v, want a session ID and its end", wire)
	}

	return wire.SessionID
}

// readSession reads id with the query as admin and returns the answer.
func readSession(t *testing.T, f fixture, id, query string) response {
	t.Helper()

	return f.do(t, get("admin", sessionBase+"/"+id+query))
}

// sessionValues decodes a session read into plain values and secret marks.
func sessionValues(t *testing.T, body string) (map[string]any, map[string]bool) {
	t.Helper()

	var wire struct {
		Values map[string]struct {
			Value  any  `json:"value"`
			Secret bool `json:"secret"`
		} `json:"values"`
	}

	if err := json.Unmarshal([]byte(body), &wire); err != nil {
		t.Fatalf("decode session: %v", err)
	}

	values := make(map[string]any, len(wire.Values))
	secrets := make(map[string]bool, len(wire.Values))

	for path, v := range wire.Values {
		values[path] = v.Value
		secrets[path] = v.Secret
	}

	return values, secrets
}

func TestOpenRoutes(t *testing.T) {
	t.Parallel()

	t.Run("open answers an ID and its end with no value", testOpenAnswersID)
	t.Run("read answers the values with the secrets", testOpenReadValues)
	t.Run("read one path", testOpenReadOnePath)
	t.Run("refusals name no value", testOpenRefusalsNameNoValue)
	t.Run("unknown profile is not found", testOpenUnknownProfile)
	t.Run("denied open stores nothing and names no value", testOpenDeniedStoresNothing)
	t.Run("key replay answers 409 with the session and no value", testOpenKeyReplay)
	t.Run("forged sessions are not found", testOpenForgedSessions)
	t.Run("hostile read paths are refused", testOpenHostileReadPaths)
	t.Run("key on a read is refused", testOpenKeyOnRead)
	t.Run("read after revoke is denied and deletes the session", testOpenReadAfterRevoke)
}

// testOpenAnswersID checks that an open answers a pos- ID and its end.
func testOpenAnswersID(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.create(t, "root", secretDoc)

	got := f.do(t, send("admin", http.MethodPost, base+"/root/open", openBody(t)))
	wantCode(t, "open", got, http.StatusCreated)

	var wire openWire
	if err := json.Unmarshal([]byte(got.body), &wire); err != nil {
		t.Fatalf("decode open: %v", err)
	}

	if !strings.HasPrefix(wire.SessionID, "pos-") || wire.ExpiresAt == "" {
		t.Errorf("open answers %+v, want a pos- ID and its end", wire)
	}

	if strings.Contains(got.body, "hunter2") {
		t.Errorf("open answers a value: %s", got.body)
	}
}

// testOpenReadValues checks that a read answers the values with secrets.
func testOpenReadValues(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.create(t, "root", secretDoc)

	id := openSession(t, f, openBody(t))
	got := readSession(t, f, id, "")

	wantCode(t, "read", got, http.StatusOK)

	if cache := got.header.Get("Cache-Control"); cache != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cache)
	}

	values, secrets := sessionValues(t, got.body)

	if values["region"] != "eu-west-1" || values["db.password"] != "hunter2" {
		t.Errorf("values = %v", values)
	}

	if !secrets["db.password"] || secrets["region"] {
		t.Errorf("secrets = %v", secrets)
	}
}

// testOpenReadOnePath checks that a read of one path answers only it.
func testOpenReadOnePath(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.create(t, "root", secretDoc)

	id := openSession(t, f, openBody(t))
	got := readSession(t, f, id, "?path=region")

	wantCode(t, "read one path", got, http.StatusOK)

	values, _ := sessionValues(t, got.body)
	if len(values) != 1 || values["region"] != "eu-west-1" {
		t.Errorf("values = %v, want only region", values)
	}

	got = readSession(t, f, id, "?path=absent")
	wantCode(t, "unknown path", got, http.StatusNotFound)
	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusNotFound, []byte(got.body),
		errcodetest.Want{Code: errcode.ProfileNotFound, Args: nil, RequestBody: nil})
}

// openRefusalRow is one refusal of an open: the document, the body, and the
// status and code of the refusal.
type openRefusalRow struct {
	name   string
	doc    string
	body   string
	status int
	code   string
}

// openRefusalRows lists the refusals of an open that name no value.
func openRefusalRows() []openRefusalRow {
	return []openRefusalRow{
		{
			"provider block",
			"schemaVersion: 1\nproviders:\n  vault:\n    use: hashivault\nvalues:\n  x: 1\n",
			`{"consumer":"api"}`, http.StatusUnprocessableEntity, errcode.ProfileProvider,
		},
		{
			"never-store path",
			"schemaVersion: 1\nvalues:\n  token: { $secret: hunter2, $neverStore: true }\n",
			`{"consumer":"api","paths":["token"]}`,
			http.StatusUnprocessableEntity, errcode.ProfileNeverStore,
		},
		{
			"final value",
			"schemaVersion: 1\nvalues:\n  region: { $value: eu-west-1, $final: true }\n" +
				"  other: { $value: x }\n",
			`{"consumer":"api"}`,
			http.StatusUnprocessableEntity, errcode.ProfileFinal,
		},
		{
			"unknown expression name",
			"schemaVersion: 1\nvalues:\n  x: ${providers.db.password}\n",
			`{"consumer":"api"}`, http.StatusUnprocessableEntity, errcode.ProfileReference,
		},
		{
			"import cycle",
			"schemaVersion: 1\nimports: [loop]\nvalues:\n  x: 1\n",
			`{"consumer":"api"}`, http.StatusUnprocessableEntity, errcode.ProfileImportCycle,
		},
		{
			"unknown consumer",
			"schemaVersion: 1\nvalues:\n  x: 1\n",
			`{"consumer":"run"}`, http.StatusBadRequest, errcode.ProfileInvalid,
		},
		{
			"bad duration",
			"schemaVersion: 1\nvalues:\n  x: 1\n",
			`{"consumer":"api","duration":"soon"}`,
			http.StatusBadRequest, errcode.ProfileInvalid,
		},
		{
			"duration below a minute",
			"schemaVersion: 1\nvalues:\n  x: 1\n",
			`{"consumer":"api","duration":"30s"}`,
			http.StatusBadRequest, errcode.ProfileInvalid,
		},
		{
			"unknown field", "schemaVersion: 1\nvalues:\n  x: 1\n",
			`{"consumer":"api","extra":1}`, http.StatusBadRequest, errcode.RequestBodyInvalid,
		},
		{
			"trailing JSON", "schemaVersion: 1\nvalues:\n  x: 1\n",
			`{"consumer":"api"}{"consumer":"api"}`, http.StatusBadRequest, errcode.RequestBodyInvalid,
		},
		{
			"path with a C1 character", "schemaVersion: 1\nvalues:\n  x: 1\n",
			`{"consumer":"api","paths":["a\u0085b"]}`, http.StatusBadRequest, errcode.ProfileInvalid,
		},
		{
			"path with NUL", "schemaVersion: 1\nvalues:\n  x: 1\n",
			`{"consumer":"api","paths":["a\u0000b"]}`, http.StatusBadRequest, errcode.ProfileInvalid,
		},
		{
			"path with DEL", "schemaVersion: 1\nvalues:\n  x: 1\n",
			`{"consumer":"api","paths":["a\u007fb"]}`, http.StatusBadRequest, errcode.ProfileInvalid,
		},
		{
			"empty path", "schemaVersion: 1\nvalues:\n  x: 1\n",
			`{"consumer":"api","paths":[""]}`, http.StatusBadRequest, errcode.ProfileInvalid,
		},
		{
			"body that is no JSON",
			"schemaVersion: 1\nvalues:\n  x: 1\n",
			`{`, http.StatusBadRequest, errcode.RequestBodyInvalid,
		},
	}
}

// testOpenRefusalsNameNoValue checks that each refusal names no value.
func testOpenRefusalsNameNoValue(t *testing.T) {
	t.Parallel()

	for _, tc := range openRefusalRows() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			testOneOpenRefusal(t, tc)
		})
	}
}

// testOneOpenRefusal checks one refusal row.
func testOneOpenRefusal(t *testing.T, tc openRefusalRow) {
	t.Helper()

	f := newFixture(t)

	const name = "root"

	if tc.name == "import cycle" {
		f.create(t, "loop", "schemaVersion: 1\nimports: [root]\nvalues:\n  y: 2\n")
	}

	if tc.name == "final value" {
		// The root holds a final region; the import replaces it.
		f.create(t, "base", tc.doc)
		f.create(t, name, "schemaVersion: 1\nimports: [base]\nvalues:\n  region: eu-west-2\n")
	} else {
		f.create(t, name, tc.doc)
	}

	got := f.do(t, send("admin", http.MethodPost, base+"/"+name+"/open", tc.body))
	wantCode(t, tc.name, got, tc.status)

	if strings.Contains(got.body, "hunter2") {
		t.Errorf("%s names a value: %s", tc.name, got.body)
	}

	if tc.code != errcode.RequestBodyInvalid {
		errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.status, []byte(got.body),
			errcodetest.Want{Code: tc.code, Args: nil, RequestBody: nil})
	}
}

// testOpenUnknownProfile checks that an unknown profile is not found.
func testOpenUnknownProfile(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	got := f.do(t, send("admin", http.MethodPost, base+"/absent/open", openBody(t)))
	wantCode(t, "unknown profile", got, http.StatusNotFound)
	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusNotFound, []byte(got.body),
		errcodetest.Want{Code: errcode.ProfileNotFound, Args: nil, RequestBody: nil})
}

// testOpenDeniedStoresNothing checks that a denied open stores nothing.
func testOpenDeniedStoresNothing(t *testing.T) {
	t.Parallel()

	// The fixture denies profile:open for every subject.
	f := newFixture(t, open)
	f.create(t, "root", secretDoc)

	got := f.do(t, send("admin", http.MethodPost, base+"/root/open", openBody(t)))
	wantCode(t, "denied open", got, http.StatusForbidden)

	if strings.Contains(got.body, "hunter2") {
		t.Errorf("denied open names a value: %s", got.body)
	}
}

// testOpenKeyReplay checks that a key replay answers 409 with the session.
func testOpenKeyReplay(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.create(t, "root", secretDoc)

	first := f.do(t, send("admin", http.MethodPost, base+"/root/open", openBody(t)).
		withHeader("Idempotency-Key", "open-key-1"))
	wantCode(t, "first open", first, http.StatusCreated)

	var wire openWire
	if err := json.Unmarshal([]byte(first.body), &wire); err != nil {
		t.Fatalf("decode open: %v", err)
	}

	again := f.do(t, send("admin", http.MethodPost, base+"/root/open", openBody(t)).
		withHeader("Idempotency-Key", "open-key-1"))
	wantCode(t, "replay", again, http.StatusConflict)

	if !strings.Contains(again.body, wire.SessionID) {
		t.Errorf("replay = %s, want the session %s", again.body, wire.SessionID)
	}

	if strings.Contains(again.body, "hunter2") {
		t.Errorf("replay names a value: %s", again.body)
	}

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusConflict, []byte(again.body),
		errcodetest.Want{
			Code:        errcode.RequestIdempotencyKeyReplayedSecret,
			Args:        map[string]any{"objectID": wire.SessionID},
			RequestBody: nil,
		})
}

// testOpenForgedSessions checks that forged sessions are not found.
func testOpenForgedSessions(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.create(t, "root", secretDoc)

	id := openSession(t, f, openBody(t))

	// An ID that the handler refuses never reaches the store: the service
	// and the store refuse it too, so only a count of reads shows the layer.
	before := f.sessionReads.Load()

	for _, forged := range []string{"", "pos-", "ses-0001", "pos-%2F", "pos-A"} {
		got := readSession(t, f, forged, "")
		wantCode(t, "session "+forged, got, http.StatusNotFound)
	}

	if n := f.sessionReads.Load() - before; n != 0 {
		t.Errorf("the store read %d forged IDs, want 0", n)
	}

	got := readSession(t, f, id+"x", "")
	wantCode(t, "session "+id+"x", got, http.StatusNotFound)
}

// testOpenHostileReadPaths checks that a hostile path in the query of a read
// answers 400 and names no value.
func testOpenHostileReadPaths(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.create(t, "root", secretDoc)

	id := openSession(t, f, openBody(t))

	for _, raw := range []string{"%00", "%0d", "%0a", "%7f", "%c2%85", "%ff", "a..b", "a%5Cb"} {
		got := readSession(t, f, id, "?path="+raw)

		wantCode(t, "path "+raw, got, http.StatusBadRequest)

		if strings.Contains(got.body, "hunter2") {
			t.Errorf("path %s names a value: %s", raw, got.body)
		}
	}
}

// testOpenKeyOnRead checks that a key on a read is refused.
func testOpenKeyOnRead(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.create(t, "root", secretDoc)

	id := openSession(t, f, openBody(t))

	got := f.do(t, get("admin", sessionBase+"/"+id).withHeader("Idempotency-Key", "k1"))
	wantCode(t, "key on a read", got, http.StatusBadRequest)
	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusBadRequest, []byte(got.body),
		errcodetest.Want{Code: errcode.RequestIdempotencyKeyUnsupported, Args: nil, RequestBody: nil})
}

// testOpenReadAfterRevoke checks that a read after revoke is denied.
func testOpenReadAfterRevoke(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.create(t, "root", secretDoc)

	id := openSession(t, f, openBody(t))

	f.denied[open] = true

	got := readSession(t, f, id, "")
	wantCode(t, "read after revoke", got, http.StatusForbidden)

	if strings.Contains(got.body, "hunter2") {
		t.Errorf("denied read names a value: %s", got.body)
	}

	// The session is gone, but the answer stays a denial: it never leaks
	// whether the session still reads.
	got = readSession(t, f, id, "")
	wantCode(t, "second read", got, http.StatusForbidden)
}
