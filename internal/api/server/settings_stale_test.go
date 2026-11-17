package server_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestPatchStaleRevisionAnswersRevisionStale(t *testing.T) {
	t.Parallel()

	e := newSettingsEnv(t, nil)

	wantSettingsStatus(t, e.patch(t, settingsURL, `"0"`, `{"test.label":"blue"}`), http.StatusOK)

	for _, path := range []string{settingsURL} {
		r := e.patch(t, path, `"0"`, `{"test.label":"red"}`)
		wantSettingsStatus(t, r, http.StatusPreconditionFailed)

		if !strings.Contains(r.body, `"errorCode":"settings.revision-stale"`) {
			t.Fatalf("body = %s, want the code settings.revision-stale", r.body)
		}
	}

	// The refused change moved nothing.
	got := e.do(t, getCall(settingsURL))
	if got.header.Get("ETag") != `"1"` {
		t.Fatalf("ETag = %q, want \"1\"", got.header.Get("ETag"))
	}

	// The namespace route answers the same code.
	ns := e.patch(t, settingsNSURL, `"9"`, `{"test.nsFlag":true}`)
	wantSettingsStatus(t, ns, http.StatusPreconditionFailed)

	if !strings.Contains(ns.body, `"errorCode":"settings.revision-stale"`) {
		t.Fatalf("namespace body = %s, want the code settings.revision-stale", ns.body)
	}
}
