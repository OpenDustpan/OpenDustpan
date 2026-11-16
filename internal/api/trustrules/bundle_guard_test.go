package trustrules_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/trustrule"
)

// rotateRaw sends one rotation of the bundle id as the administrator with a
// fresh console session, with the given raw If-Match header values. A request
// with two values needs the raw header: the send helper sets one value.
func (e *env) rotateRaw(t *testing.T, id string, ifMatch []string, body string) (int, []byte) {
	t.Helper()

	req, err := http.NewRequestWithContext(e.t.Context(), http.MethodPatch,
		e.server.URL+bundlesURL+"/"+id, strings.NewReader(body))
	if err != nil {
		e.t.Fatalf("request: %v", err)
	}

	req.Header.Set("Authorization", "token "+adminToken)
	req.Header.Set(sessionHeader, "1")
	req.Header["If-Match"] = ifMatch

	resp, err := e.server.Client().Do(req)
	if err != nil {
		e.t.Fatalf("send rotate: %v", err)
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			e.t.Errorf("close body: %v", closeErr)
		}
	}()

	out, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatalf("read body: %v", err)
	}

	return resp.StatusCode, out
}

// bundleETag reads the bundle id of acme and returns its ETag.
func (e *env) bundleETag(t *testing.T, id string) string {
	t.Helper()

	req, err := http.NewRequestWithContext(
		e.t.Context(), http.MethodGet, e.server.URL+bundlesURL+"/"+id, http.NoBody,
	)
	if err != nil {
		e.t.Fatalf("request: %v", err)
	}

	req.Header.Set("Authorization", "token "+adminToken)
	req.Header.Set(sessionHeader, "1")

	resp, err := e.server.Client().Do(req)
	if err != nil {
		e.t.Fatalf("read bundle: %v", err)
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			e.t.Errorf("close body: %v", closeErr)
		}
	}()

	out, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatalf("read body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("read bundle = %d %s, want 200", resp.StatusCode, out)
	}

	return resp.Header.Get("ETag")
}

// rotateBody returns the body of a rotation of a valid CA with overlapSeconds.
func rotateBody(t *testing.T, e *env, overlap string) string {
	t.Helper()

	if overlap == "" {
		return `{"pem":` + quote(e.fixtureCAPEM()) + `}`
	}

	return `{"pem":` + quote(e.fixtureCAPEM()) + `,"overlapSeconds":` + overlap + `}`
}

// TestBundleSaveNeedsPEM checks the guard of the body of a save: pem must be
// present. The decoder refuses an unknown field, but a null or a missing pem
// reaches the handler, so the handler refuses it itself. Without the guard,
// the handler reads the value of a nil pointer.
func TestBundleSaveNeedsPEM(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allActions())

	for _, body := range []string{`{}`, `{"pem":null}`} {
		status, out := e.admin(http.MethodPost, bundlesURL, body)
		if status != http.StatusBadRequest || errorCode(t, out) != "request.body-invalid" {
			t.Errorf("save with %s = %d %s, want 400 request.body-invalid", body, status, out)
		}
	}

	status, out := e.admin(http.MethodGet, bundlesURL, "")
	if status != http.StatusOK || !strings.Contains(string(out), `"bundles":[]`) {
		t.Fatalf("bundles after the refused saves = %d %s, want an empty list", status, out)
	}
}

// TestBundleWritesNeedTheBundleOfThePath checks the guard of the bundle ID of
// the path: a well-formed ID that is not the bundle of the namespace answers
// 404 and the bundle of the namespace stays unchanged. Without the guard, the
// rotate would change the bundle of the namespace, because the service names
// the bundle by the namespace and not by the path.
func TestBundleWritesNeedTheBundleOfThePath(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allActions())
	id := e.mustSaveBundle()
	etag := e.bundleETag(t, id)

	foreign := string(trustrule.NewBundleID())
	status, out := e.rotateRaw(t, foreign, []string{etag}, rotateBody(t, e, ""))
	if status != http.StatusNotFound || errorCode(t, out) != "trust-rule.bundle-not-found" {
		t.Fatalf("rotate of a foreign bundle ID = %d %s, want 404 trust-rule.bundle-not-found", status, out)
	}

	if got := e.bundleETag(t, id); got != etag {
		t.Fatalf("bundle ETag after the refused rotate = %s, want %s", got, etag)
	}
}

// TestBundleRotateRefusesABoundOverlap checks the overlap bound of the
// handler. The value 86401 is past the bound. The value 18446744074 seconds
// is past it too, and without the guard it overflows the Duration to about
// 0.29 seconds, a value inside the bound of the store: the guard, not the
// store, refuses it.
func TestBundleRotateRefusesABoundOverlap(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allActions())
	id := e.mustSaveBundle()
	etag := e.bundleETag(t, id)

	for _, overlap := range []string{"86401", "18446744074"} {
		status, out := e.rotateRaw(t, id, []string{etag}, rotateBody(t, e, overlap))
		if status != http.StatusBadRequest || errorCode(t, out) != "trust-rule.bundle-invalid" {
			t.Errorf("rotate with overlap %s = %d %s, want 400 trust-rule.bundle-invalid", overlap, status, out)
		}
	}

	if got := e.bundleETag(t, id); got != etag {
		t.Fatalf("bundle ETag after the refused rotates = %s, want %s", got, etag)
	}
}

// TestBundleRotateNeedsOneIfMatch checks the guard that refuses an If-Match
// header with more than one value: the first value alone must not decide the
// write. Without the guard, the rotation runs with the first value.
func TestBundleRotateNeedsOneIfMatch(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allActions())
	id := e.mustSaveBundle()
	etag := e.bundleETag(t, id)

	status, out := e.rotateRaw(t, id, []string{etag, `"999"`}, rotateBody(t, e, ""))
	if status != http.StatusBadRequest || errorCode(t, out) != "request.if-match-invalid" {
		t.Fatalf("rotate with two If-Match values = %d %s, want 400 request.if-match-invalid", status, out)
	}

	if got := e.bundleETag(t, id); got != etag {
		t.Fatalf("bundle ETag after the refused rotate = %s, want %s", got, etag)
	}
}

// TestBundleRotateRefusesARevisionBelowOne checks the guard that refuses a
// revision below 1 in quotes. Without the guard, the revision 0 would run the
// write and answer 412, the answer of a stale revision, and not the 400 of a
// header that carries no revision.
func TestBundleRotateRefusesARevisionBelowOne(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allActions())
	id := e.mustSaveBundle()
	etag := e.bundleETag(t, id)

	status, out := e.rotateRaw(t, id, []string{`"0"`}, rotateBody(t, e, ""))
	if status != http.StatusBadRequest || errorCode(t, out) != "request.if-match-invalid" {
		t.Fatalf("rotate with If-Match 0 = %d %s, want 400 request.if-match-invalid", status, out)
	}

	if got := e.bundleETag(t, id); got != etag {
		t.Fatalf("bundle ETag after the refused rotate = %s, want %s", got, etag)
	}
}

// TestBundleRotateRefusesAWeakTag checks the guard that refuses a weak tag:
// a weak tag never matches in the strong comparison of If-Match, so the
// answer is 412. Without the guard, the number inside the tag would run the
// write as a strong tag.
func TestBundleRotateRefusesAWeakTag(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allActions())
	id := e.mustSaveBundle()
	etag := e.bundleETag(t, id)

	status, out := e.rotateRaw(t, id, []string{"W/" + etag}, rotateBody(t, e, ""))
	if status != http.StatusPreconditionFailed || errorCode(t, out) != "request.precondition-failed" {
		t.Fatalf("rotate with a weak tag = %d %s, want 412 request.precondition-failed", status, out)
	}

	if got := e.bundleETag(t, id); got != etag {
		t.Fatalf("bundle ETag after the refused rotate = %s, want %s", got, etag)
	}
}
