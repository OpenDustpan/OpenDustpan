package settings_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ifMatch returns the header map of a write over version v.
func ifMatch(v string) map[string]string { return map[string]string{"If-Match": v} }

// errorCodeOf returns the errorCode of an error answer.
func errorCodeOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	var body struct {
		ErrorCode string `json:"errorCode"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("answer is not JSON: %v: %s", err, rec.Body.String())
	}

	return body.ErrorCode
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()

	if rec.Code != status {
		t.Fatalf("status = %d, want %d: %s", rec.Code, status, rec.Body.String())
	}

	if code != "" {
		if got := errorCodeOf(t, rec); got != code {
			t.Fatalf("errorCode = %q, want %q", got, code)
		}
	}
}

// answerOf decodes the settings answer.
type answerOf struct {
	UnitID    string `json:"unitID"`
	Version   int64  `json:"version"`
	UpdatedAt string `json:"updatedAt"`
	Settings  struct {
		Executor     string `json:"executor"`
		Pool         string `json:"pool"`
		CodeLocation struct {
			Connection string `json:"connection"`
			Repository string `json:"repository"`
			Directory  string `json:"directory"`
		} `json:"codeLocation"`
	} `json:"settings"`
	Effective map[string]struct {
		Value  any    `json:"value"`
		Origin string `json:"origin"`
	} `json:"effective"`
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) answerOf {
	t.Helper()

	var a answerOf
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		t.Fatalf("answer is not JSON: %v: %s", err, rec.Body.String())
	}

	return a
}

const validBody = `{"pool":"` + poolJobs + `","codeLocation":{"repository":"acme/infra","ref":"main","directory":"stacks/net"}}`

func TestGetOfAUnitWithNoRecordAnswersVersionZeroAndOrigins(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)

	rec := r.call(t, http.MethodGet, settingsPath, "", nil)
	wantStatus(t, rec, http.StatusOK, "")

	if rec.Header().Get("ETag") != `"0"` {
		t.Fatalf("ETag = %q, want \"0\"", rec.Header().Get("ETag"))
	}

	a := decode(t, rec)
	if a.UnitID != netID || a.Version != 0 || a.UpdatedAt != "" {
		t.Fatalf("answer = %+v", a)
	}

	for field, want := range map[string]string{
		"connection": "namespace-default", "executor": "built-in-default", "directory": "built-in-default",
		"pool": "built-in-default",
	} {
		if got := a.Effective[field].Origin; got != want {
			t.Errorf("origin of %s = %q, want %q", field, got, want)
		}
	}

	if a.Effective["connection"].Value != connRW {
		t.Errorf("effective connection = %v, want the default", a.Effective["connection"].Value)
	}
}

func TestPutThenGetShowsTheRecordAndItsOrigin(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)

	rec := r.call(t, http.MethodPut, settingsPath, validBody, ifMatch(`"0"`))
	wantStatus(t, rec, http.StatusOK, "")

	if rec.Header().Get("ETag") != `"1"` {
		t.Fatalf("ETag = %q, want \"1\"", rec.Header().Get("ETag"))
	}

	a := decode(t, r.call(t, http.MethodGet, settingsPath, "", nil))
	if a.Version != 1 || a.Settings.CodeLocation.Repository != "acme/infra" || a.Effective["pool"].Origin != "unit" ||
		a.Effective["repository"].Origin != "unit" || a.Effective["pool"].Value != poolJobs {
		t.Fatalf("answer = %+v", a)
	}

	if len(r.store.entries) != 1 || r.store.entries[0].Subject != ada || r.store.entries[0].Object != netObject {
		t.Fatalf("store entries = %+v, want one entry of ada on the unit", r.store.entries)
	}
}

func TestWriteNeedsTheVersionThatTheCallerRead(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)

	cases := []struct {
		name   string
		header map[string]string
		status int
		code   string
	}{
		{"no If-Match", nil, http.StatusPreconditionRequired, errcode.RequestPreconditionRequired},
		{"unquoted version", ifMatch("0"), http.StatusBadRequest, errcode.RequestIfMatchInvalid},
		{"wildcard", ifMatch("*"), http.StatusBadRequest, errcode.RequestIfMatchInvalid},
		{"negative version", ifMatch(`"-1"`), http.StatusBadRequest, errcode.RequestIfMatchInvalid},
		{"stale version", ifMatch(`"3"`), http.StatusPreconditionFailed, errcode.RequestPreconditionFailed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := r.call(t, http.MethodPut, settingsPath, validBody, tc.header)
			wantStatus(t, rec, tc.status, tc.code)

			if r.store.written() != 0 {
				t.Fatalf("a refused write wrote %d records", r.store.written())
			}
		})
	}
}

func TestPutNamingAConnectionNeedsTheUseAction(t *testing.T) {
	t.Parallel()

	without := []string{}

	for _, h := range fullAccess() {
		if h != "namespace:use-git-host-connection acme" {
			without = append(without, h)
		}
	}

	r := newRig(t, without...)
	body := `{"codeLocation":{"connection":"` + connRW + `","repository":"acme/infra","ref":"main"}}`

	wantStatus(t, r.call(t, http.MethodPut, settingsPath, body, ifMatch(`"0"`)), http.StatusForbidden, "")

	if r.store.written() != 0 {
		t.Fatal("a refused write changed the store")
	}

	if got := r.audit.denied(); len(got) == 0 || !strings.Contains(got[len(got)-1].Action, "use-git-host-connection") {
		t.Fatalf("audit entries = %+v, want the denial of the use action", got)
	}

	// Without the connection in the body the same caller may write.
	wantStatus(t, r.call(t, http.MethodPut, settingsPath, validBody, ifMatch(`"0"`)), http.StatusOK, "")
}

func TestPutChangingTheStepsNeedsDefineSteps(t *testing.T) {
	t.Parallel()

	without := []string{}

	for _, h := range fullAccess() {
		if !strings.HasPrefix(h, "unit:define-steps") {
			without = append(without, h)
		}
	}

	r := newRig(t, without...)
	body := `{"steps":[{"name":"plan","role":"tool","phase":"plan","args":["tofu","plan"]}]}`

	wantStatus(t, r.call(t, http.MethodPut, settingsPath, body, ifMatch(`"0"`)), http.StatusForbidden, "")

	if r.store.written() != 0 {
		t.Fatal("a refused write changed the store")
	}
}

func TestPutRefusesHostileValues(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"directory with dot dot":      `{"codeLocation":{"directory":"../x"}}`,
		"absolute directory":          `{"codeLocation":{"directory":"/etc"}}`,
		"directory in CI config":      `{"codeLocation":{"directory":"a/.github/b"}}`,
		"ref with a leading dash":     `{"codeLocation":{"ref":"--upload-pack=x"}}`,
		"ref with a control char":     `{"codeLocation":{"ref":"main\u0000"}}`,
		"repository with credentials": `{"codeLocation":{"repository":"https://u:p@host/a/b"}}`,
		"file outside the directory":  `{"writeBack":{"files":["../secrets.yaml"]}}`,
		"file in CI config":           `{"writeBack":{"files":[".github/workflows/x.yml"]}}`,
		"unknown executor":            `{"executor":"shell"}`,
		"step with a credential env":  `{"steps":[{"name":"a","role":"tool","phase":"plan","args":["x"],"env":{"AWS_SECRET_ACCESS_KEY":"x"}}]}`,
		"timeout that would wrap":     `{"steps":[{"name":"a","role":"tool","phase":"plan","args":["x"],"timeoutSeconds":9223372036}]}`,
		"pool that is not an ID":      `{"pool":"x; drop"}`,
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t, fullAccess()...)
			rec := r.call(t, http.MethodPut, settingsPath, body, ifMatch(`"0"`))
			wantStatus(t, rec, http.StatusBadRequest, errcode.UnitSettingsInvalid)

			if r.store.written() != 0 {
				t.Fatal("a refused write changed the store")
			}
		})
	}
}

func TestPutRefusesBadBodies(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{"empty", "", http.StatusBadRequest, errcode.RequestBodyInvalid},
		{"unknown field", `{"unit":"x"}`, http.StatusBadRequest, errcode.RequestBodyInvalid},
		{"two documents", `{} {}`, http.StatusBadRequest, errcode.RequestBodyInvalid},
		{"invalid UTF-8", "{\"pool\":\"\xff\"}", http.StatusBadRequest, errcode.RequestBodyInvalid},
		{"too large", `{"pool":"` + strings.Repeat("a", 300<<10) + `"}`, http.StatusRequestEntityTooLarge,
			errcode.RequestBodyTooLarge},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t, fullAccess()...)
			wantStatus(t, r.call(t, http.MethodPut, settingsPath, tc.body, ifMatch(`"0"`)), tc.status, tc.code)
		})
	}
}

func TestPutRefusesReferencesThatDoNotExist(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)

	rec := r.call(t, http.MethodPut, settingsPath, `{"pool":"rp-nope"}`, ifMatch(`"0"`))
	wantStatus(t, rec, http.StatusUnprocessableEntity, errcode.UnitSettingsPoolUnknown)

	body := `{"codeLocation":{"connection":"gitconn-0000000000000000000000000000ffff"}}`
	wantStatus(t, r.call(t, http.MethodPut, settingsPath, body, ifMatch(`"0"`)), http.StatusUnprocessableEntity,
		errcode.UnitSettingsConnectionUnknown)
}

func TestPutFailsClosedOnAFencedWrite(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)
	r.store.failWith = namespace.ErrFenced

	wantStatus(t, r.call(t, http.MethodPut, settingsPath, validBody, ifMatch(`"0"`)), http.StatusConflict,
		errcode.NamespaceFenced)
}

func TestIdempotencyKeyIsRefusedOnEveryWrite(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)
	header := map[string]string{"If-Match": `"0"`, "Idempotency-Key": "k-1"}

	for _, tc := range []struct{ method, path string }{
		{http.MethodPut, settingsPath}, {http.MethodDelete, settingsPath},
		{http.MethodPut, defaultsPath}, {http.MethodPost, bulkPath},
	} {
		wantStatus(t, r.call(t, tc.method, tc.path, "{}", header), http.StatusBadRequest,
			errcode.RequestIdempotencyKeyUnsupported)
	}
}

func TestDeleteResetsTheRecord(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)

	wantStatus(t, r.call(t, http.MethodDelete, settingsPath, "", ifMatch(`"1"`)), http.StatusNotFound,
		errcode.UnitSettingsNotFound)

	wantStatus(t, r.call(t, http.MethodPut, settingsPath, validBody, ifMatch(`"0"`)), http.StatusOK, "")
	wantStatus(t, r.call(t, http.MethodDelete, settingsPath, "", nil), http.StatusPreconditionRequired, "")
	wantStatus(t, r.call(t, http.MethodDelete, settingsPath, "", ifMatch(`"9"`)), http.StatusPreconditionFailed, "")
	wantStatus(t, r.call(t, http.MethodDelete, settingsPath, "", ifMatch(`"1"`)), http.StatusNoContent, "")

	if a := decode(t, r.call(t, http.MethodGet, settingsPath, "", nil)); a.Version != 0 {
		t.Fatalf("version after reset = %d, want 0", a.Version)
	}
}

func TestReadsAnswerNotFoundForWhatTheCallerCannotRead(t *testing.T) {
	t.Parallel()

	r := newRig(t, "namespace:read acme")

	wantStatus(t, r.call(t, http.MethodGet, settingsPath, "", nil), http.StatusNotFound, errcode.UnitSettingsNotFound)

	// A unit of another namespace answers like an unknown unit.
	other := "/api/dustpan/v1/namespaces/acme/units/" + otherID + "/settings"
	wantStatus(t, newRig(t, fullAccess()...).call(t, http.MethodGet, other, "", nil), http.StatusNotFound,
		errcode.UnitSettingsNotFound)

	absent := "/api/dustpan/v1/namespaces/absent/units/" + netID + "/settings"
	wantStatus(t, newRig(t, fullAccess()...).call(t, http.MethodGet, absent, "", nil), http.StatusNotFound,
		errcode.UnitSettingsNotFound)
}

func TestWriteWithoutManageIsForbiddenAndAudited(t *testing.T) {
	t.Parallel()

	r := newRig(t, "namespace:read acme", "unit:read "+netObject)

	wantStatus(t, r.call(t, http.MethodPut, settingsPath, validBody, ifMatch(`"0"`)), http.StatusForbidden, "")

	if got := r.audit.denied(); len(got) != 1 || got[0].Action != "unit:manage" {
		t.Fatalf("audit entries = %+v, want the denial of unit:manage", got)
	}
}

func TestSuggestionsNeverSetAField(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)

	rec := r.call(t, http.MethodGet, suggestionsPath, "", nil)
	wantStatus(t, rec, http.StatusOK, "")

	var out struct {
		Suggestions []struct {
			Repository string `json:"repository"`
			Commit     string `json:"commit"`
			Source     string `json:"source"`
		} `json:"suggestions"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Suggestions) != 1 ||
		out.Suggestions[0].Repository != "acme/infra" {
		t.Fatalf("answer = %s, %v", rec.Body.String(), err)
	}

	if r.store.written() != 0 {
		t.Fatal("the suggestion route wrote")
	}

	// No source gives an empty list, never null.
	empty := newRigWith(t, nil, fullAccess()...)
	if body := empty.call(t, http.MethodGet, suggestionsPath, "", nil).Body.String(); !strings.Contains(body, `"suggestions":[]`) {
		t.Fatalf("answer without a source = %s", body)
	}

	failing := newRigWith(t, suggester{err: errNoList}, fullAccess()...)
	wantStatus(t, failing.call(t, http.MethodGet, suggestionsPath, "", nil), http.StatusInternalServerError, "")
}

func TestUnitDefaults(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)

	rec := r.call(t, http.MethodGet, defaultsPath, "", nil)
	wantStatus(t, rec, http.StatusOK, "")

	if rec.Header().Get("ETag") != `"0"` {
		t.Fatalf("ETag = %q", rec.Header().Get("ETag"))
	}

	wantStatus(t, r.call(t, http.MethodPut, defaultsPath, `{"pool":"`+poolJobs+`"}`, nil), http.StatusPreconditionRequired, "")
	wantStatus(t, r.call(t, http.MethodPut, defaultsPath, `{"pool":"rp-nope"}`, ifMatch(`"0"`)),
		http.StatusUnprocessableEntity, errcode.UnitSettingsPoolUnknown)
	wantStatus(t, r.call(t, http.MethodPut, defaultsPath, `{"pool":"`+poolJobs+`"}`, ifMatch(`"0"`)), http.StatusOK, "")
	wantStatus(t, r.call(t, http.MethodPut, defaultsPath, `{"pool":""}`, ifMatch(`"0"`)), http.StatusPreconditionFailed, "")

	// The unit now takes the pool from the namespace and says so.
	if a := decode(t, r.call(t, http.MethodGet, settingsPath, "", nil)); a.Effective["pool"].Origin != "namespace-default" {
		t.Fatalf("origin of the pool = %q, want namespace-default", a.Effective["pool"].Origin)
	}
}

func TestDefaultsWritesNeedNamespaceAdmin(t *testing.T) {
	t.Parallel()

	r := newRig(t, "namespace:read acme")

	wantStatus(t, r.call(t, http.MethodGet, defaultsPath, "", nil), http.StatusOK, "")
	wantStatus(t, r.call(t, http.MethodPut, defaultsPath, `{"pool":""}`, ifMatch(`"0"`)), http.StatusForbidden, "")
}

type bulkOut struct {
	Results []struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		Version int64  `json:"version"`
		Error   *struct {
			Code      int    `json:"code"`
			ErrorCode string `json:"errorCode"`
		} `json:"error"`
	} `json:"results"`
}

func TestBulkAppliesEachItemOnItsOwn(t *testing.T) {
	t.Parallel()

	// The caller manages net but only reads app.
	r := newRig(t, "namespace:read acme", "unit:read "+netObject, "unit:manage "+netObject,
		"unit:read "+appObject)

	body := fmt.Sprintf(`{"units":[{"id":%q,"version":0},{"id":%q,"version":0},{"id":%q,"version":0},{"id":"nope","version":0},{"id":%q,"version":4}],"settings":%s}`,
		netID, appID, otherID, netID, validBody)

	rec := r.call(t, http.MethodPost, bulkPath, body, nil)
	wantStatus(t, rec, http.StatusOK, "")

	var out bulkOut
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Results) != 5 {
		t.Fatalf("answer = %s, %v", rec.Body.String(), err)
	}

	want := []struct {
		id, status string
		code       int
	}{
		{netID, "ok", 0}, {appID, "error", http.StatusForbidden}, {otherID, "error", http.StatusNotFound},
		{"nope", "error", http.StatusNotFound}, {netID, "error", http.StatusPreconditionFailed},
	}

	for i, w := range want {
		got := out.Results[i]
		if got.ID != w.id || got.Status != w.status || (w.code != 0 && (got.Error == nil || got.Error.Code != w.code)) {
			t.Errorf("result %d = %+v, want %+v", i, got, w)
		}
	}

	if out.Results[0].Version != 1 || r.store.written() != 1 {
		t.Fatalf("version %d with %d writes, want 1 and 1", out.Results[0].Version, r.store.written())
	}

	if got := r.audit.denied(); len(got) != 1 || got[0].Action != "unit:manage" {
		t.Fatalf("denial entries = %+v, want one for the unit that the caller cannot manage", got)
	}
}

func TestBulkRefusesASizeOutsideTheLimit(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)

	units := make([]string, 101)
	for i := range units {
		units[i] = fmt.Sprintf(`{"id":"ws-%d","version":0}`, i)
	}

	for name, body := range map[string]string{
		"no unit":       `{"units":[],"settings":` + validBody + `}`,
		"too many":      `{"units":[` + strings.Join(units, ",") + `],"settings":` + validBody + `}`,
		"missing units": `{"settings":` + validBody + `}`,
	} {
		wantStatus(t, r.call(t, http.MethodPost, bulkPath, body, nil), http.StatusBadRequest, errcode.UnitSettingsBulkSize)

		_ = name
	}

	wantStatus(t, r.call(t, http.MethodPost, bulkPath, `{"units":[{"id":"`+netID+`","version":0}]}`, nil),
		http.StatusBadRequest, errcode.RequestBodyInvalid)
}

func TestBulkRefusesAnInvalidBodyForEachUnit(t *testing.T) {
	t.Parallel()

	r := newRig(t, fullAccess()...)
	body := `{"units":[{"id":"` + netID + `","version":0}],"settings":{"codeLocation":{"directory":"../x"}}}`

	rec := r.call(t, http.MethodPost, bulkPath, body, nil)
	wantStatus(t, rec, http.StatusOK, "")

	var out bulkOut
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Results[0].Error == nil ||
		out.Results[0].Error.ErrorCode != errcode.UnitSettingsInvalid {
		t.Fatalf("answer = %s, %v", rec.Body.String(), err)
	}
}
