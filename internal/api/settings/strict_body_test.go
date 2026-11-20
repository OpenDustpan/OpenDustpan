package settings_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
)

// TestEveryWriteRefusesNullAndARepeatedKey sends a body of null and a body
// with a repeated key to each route of the package that reads a body: the
// PUT of the settings with and without an Idempotency-Key, the PUT of the unit
// defaults and the bulk call. Each answers 400 and writes nothing.
func TestEveryWriteRefusesNullAndARepeatedKey(t *testing.T) {
	t.Parallel()

	routes := []struct {
		name, method, path string
		header             map[string]string
		repeated           string
	}{
		{
			name: "put", method: http.MethodPut, path: settingsPath, header: ifMatch(`"0"`),
			repeated: `{"executor":"runner","executor":"write-back"}`,
		},
		{
			name: "keyed put", method: http.MethodPut, path: settingsPath, header: keyedHeader("strict-1", `"0"`),
			repeated: `{"executor":"runner","executor":"write-back"}`,
		},
		{
			name: "defaults", method: http.MethodPut, path: defaultsPath, header: ifMatch(`"0"`),
			repeated: `{"pool":"` + poolJobs + `","pool":""}`,
		},
		{
			name:     "bulk",
			method:   http.MethodPost,
			path:     bulkPath,
			header:   nil,
			repeated: `{"units":[{"id":"` + netID + `","version":0}],"settings":{"executor":"runner","executor":"write-back"}}`,
		},
	}

	for _, rt := range routes {
		for name, body := range map[string]string{"null": `null`, "a repeated key": rt.repeated} {
			t.Run(rt.name+" with "+name, func(t *testing.T) {
				t.Parallel()

				r := newRig(t, fullAccess()...)
				rec := r.call(t, rt.method, rt.path, body, rt.header)
				wantStatus(t, rec, http.StatusBadRequest, errcode.RequestBodyInvalid)

				if r.store.written() != 0 {
					t.Fatal("a refused body changed the store")
				}

				if name == "a repeated key" && !strings.Contains(rec.Body.String(), "names a key two times") {
					t.Fatalf("answer = %s, want the refusal of a repeated key", rec.Body.String())
				}
			})
		}
	}
}

// TestReadAnswerRoundTripsIntoAWrite reads the settings of a unit with no
// record, writes the effective directory that the read shows, and then writes
// the settings of the next read back unchanged. Each write succeeds, and the
// second one gives the same settings as the first.
func TestReadAnswerRoundTripsIntoAWrite(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)

	first := decode(t, r.call(t, http.MethodGet, settingsPath, "", nil))

	root, ok := first.Effective["directory"].Value.(string)
	if !ok || root != "." {
		t.Fatalf("effective directory = %v, want \".\"", first.Effective["directory"].Value)
	}

	body := `{"executor":"write-back","codeLocation":{"repository":"acme/infra","ref":"main","directory":"` +
		root + `"},"writeBack":{"delivery":"review-request","files":["Pulumi.s1.yaml"]}}`

	rec := r.call(t, http.MethodPut, settingsPath, body, ifMatch(`"0"`))
	wantStatus(t, rec, http.StatusOK, "")

	written := decode(t, rec)
	if written.Settings.CodeLocation.Directory != "" || written.Effective["directory"].Value != "." ||
		written.Effective["directory"].Origin != "built-in-default" {
		t.Fatalf("answer = %+v, want the empty directory and the root from the built-in default", written)
	}

	read := r.call(t, http.MethodGet, settingsPath, "", nil)
	wantStatus(t, read, http.StatusOK, "")

	var whole struct {
		Settings json.RawMessage `json:"settings"`
	}

	if err := json.Unmarshal(read.Body.Bytes(), &whole); err != nil {
		t.Fatalf("answer is not JSON: %v", err)
	}

	again := r.call(t, http.MethodPut, settingsPath, string(whole.Settings), ifMatch(read.Header().Get("ETag")))
	wantStatus(t, again, http.StatusOK, "")

	// The store of this test writes each time; the conformance suite of the
	// store proves that a write of equal settings writes nothing.
	got := decode(t, again)
	if got.Settings != written.Settings || got.Effective["directory"] != written.Effective["directory"] {
		t.Fatalf("answer after the round trip = %+v, want the settings of the first write %+v", got, written)
	}
}
