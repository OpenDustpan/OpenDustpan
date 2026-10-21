package triggers_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// answer is the part of a trigger document that the tests read.
type answer struct {
	ID        string `json:"id"`
	Namespace string `json:"namespace"`
	Unit      struct {
		ID, Tool, Name string
	} `json:"unit"`
	Kind      string `json:"kind"`
	Frequency *struct {
		Preset string `json:"preset"`
		Cron   string `json:"cron"`
	} `json:"frequency"`
	Source *struct {
		ID, Tool, Name string
	} `json:"source"`
	Enabled      bool   `json:"enabled"`
	LastSaver    string `json:"lastSaver"`
	NeedsNewSave bool   `json:"needsNewSave"`
}

type errAnswer struct {
	Message   string `json:"message"`
	Code      int    `json:"code"`
	ErrorCode string `json:"errorCode"`
	Units     []struct {
		ID, Tool, Name string
	} `json:"units"`
}

func decode[T any](t *testing.T, body string) T {
	t.Helper()

	var v T
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("answer %q is not the expected JSON: %v", body, err)
	}

	return v
}

func fullRig(t *testing.T) *rig {
	t.Helper()

	return newRig(t, newAuthorizer(holdsAll()...))
}

func do(t *testing.T, r *rig, method, path, body string) (int, string, http.Header) {
	t.Helper()

	rec := serve(t, r.h, request{method: method, path: path, token: testToken, body: body, header: nil})

	return rec.Code, rec.Body.String(), rec.Header()
}

func TestCreateEachKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want func(t *testing.T, a answer)
	}{
		{
			"a schedule trigger with a preset", `{"kind":"schedule","frequency":{"preset":"every-6-hours"}}`,
			func(t *testing.T, a answer) {
				t.Helper()

				if a.Kind != "schedule" || a.Frequency == nil || a.Frequency.Preset != "every-6-hours" || a.Source != nil {
					t.Errorf("answer = %+v", a)
				}
			},
		},
		{
			"a schedule trigger with a cron expression", `{"kind":"schedule","frequency":{"cron":"30 4 * * 1-5"},"enabled":false}`,
			func(t *testing.T, a answer) {
				t.Helper()

				if a.Frequency == nil || a.Frequency.Cron != "30 4 * * 1-5" || a.Enabled {
					t.Errorf("answer = %+v", a)
				}
			},
		},
		{
			"a follows trigger", `{"kind":"follows","source":{"id":"` + appID + `"}}`,
			func(t *testing.T, a answer) {
				t.Helper()

				if a.Source == nil || a.Source.ID != appID || a.Source.Tool != "Pulumi" || a.Source.Name != "app" ||
					a.Frequency != nil || !a.Enabled {
					t.Errorf("answer = %+v", a)
				}
			},
		},
		{
			"a review-request-push trigger", `{"kind":"review-request-push"}`,
			func(t *testing.T, a answer) {
				t.Helper()

				if a.Kind != "review-request-push" || a.Frequency != nil || a.Source != nil {
					t.Errorf("answer = %+v", a)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := fullRig(t)

			code, body, header := do(t, r, http.MethodPost, basePath, tt.body)
			if code != http.StatusCreated {
				t.Fatalf("status = %d %s, want 201", code, body)
			}

			a := decode[answer](t, body)

			if a.ID != "trg-1" || a.Namespace != "acme" || a.Unit.ID != netID || a.Unit.Tool != "OpenTofu" ||
				a.Unit.Name != "net" || a.LastSaver != ada || a.NeedsNewSave {
				t.Errorf("answer = %+v, want trg-1 on unit net, saved by %s", a, ada)
			}

			tt.want(t, a)

			if got := header.Get("Location"); got != basePath+"/trg-1" {
				t.Errorf("Location = %q, want %q", got, basePath+"/trg-1")
			}

			if stored := r.store.triggers["trg-1"]; stored.LastSaver != ada || stored.Unit != unitNet() {
				t.Errorf("stored = %+v", stored)
			}
		})
	}
}

func TestCreateRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		body      string
		setup     func(r *rig)
		status    int
		errorCode string
	}{
		{"an empty body", ``, nil, http.StatusBadRequest, "request.body-invalid"},
		{"a body that is not JSON", `kind=schedule`, nil, http.StatusBadRequest, "request.body-invalid"},
		{"an array", `[]`, nil, http.StatusBadRequest, "request.body-invalid"},
		{"data after the document", scheduleDoc + ` {}`, nil, http.StatusBadRequest, "request.body-invalid"},
		{"a body above the limit", `{"kind":"schedule","x":"` + strings.Repeat("a", 17<<10) + `"}`, nil, http.StatusBadRequest, "request.body-invalid"},
		{"the last saver in the body", `{"kind":"schedule","frequency":{"preset":"daily"},"lastSaver":"user:root"}`, nil, http.StatusBadRequest, "request.body-invalid"},
		{"the unit in the body", `{"kind":"schedule","frequency":{"preset":"daily"},"unit":{"id":"` + appID + `"}}`, nil, http.StatusBadRequest, "request.body-invalid"},
		{"an unknown field of the frequency", `{"kind":"schedule","frequency":{"preset":"daily","zone":"x"}}`, nil, http.StatusBadRequest, "request.body-invalid"},
		{"an enabled flag that is not a flag", `{"kind":"schedule","frequency":{"preset":"daily"},"enabled":"yes"}`, nil, http.StatusBadRequest, "request.body-invalid"},
		{"no kind", `{"frequency":{"preset":"daily"}}`, nil, http.StatusBadRequest, "trigger.invalid"},
		{"an unknown kind", `{"kind":"manual"}`, nil, http.StatusBadRequest, "trigger.invalid"},
		{"a kind in other case", `{"kind":"Schedule","frequency":{"preset":"daily"}}`, nil, http.StatusBadRequest, "trigger.invalid"},
		{"a schedule with no frequency", `{"kind":"schedule"}`, nil, http.StatusBadRequest, "trigger.invalid"},
		{"a schedule with a preset and a cron", `{"kind":"schedule","frequency":{"preset":"daily","cron":"0 0 * * *"}}`, nil, http.StatusBadRequest, "trigger.invalid"},
		{"a schedule with an unknown preset", `{"kind":"schedule","frequency":{"preset":"yearly"}}`, nil, http.StatusBadRequest, "trigger.invalid"},
		{"a schedule that runs every minute", `{"kind":"schedule","frequency":{"cron":"* * * * *"}}`, nil, http.StatusBadRequest, "trigger.invalid"},
		{"a cron with a line break", "{\"kind\":\"schedule\",\"frequency\":{\"cron\":\"0 0 * * *\\n\"}}", nil, http.StatusBadRequest, "trigger.invalid"},
		{"a follows trigger with no source", `{"kind":"follows"}`, nil, http.StatusBadRequest, "trigger.invalid"},
		{"a follows trigger with an empty source", `{"kind":"follows","source":{"id":""}}`, nil, http.StatusBadRequest, "trigger.invalid"},
		{"a schedule trigger with a source", `{"kind":"schedule","frequency":{"preset":"daily"},"source":{"id":"` + appID + `"}}`, nil, http.StatusBadRequest, "trigger.invalid"},
		{"a review trigger with a frequency", `{"kind":"review-request-push","frequency":{"preset":"daily"}}`, nil, http.StatusBadRequest, "trigger.invalid"},
		{"a unit that follows itself", `{"kind":"follows","source":{"id":"` + netID + `"}}`, nil, http.StatusConflict, "trigger.cycle"},
		{"a source in another namespace", `{"kind":"follows","source":{"id":"` + otherID + `"}}`, nil, http.StatusForbidden, ""},
		{"a source that does not exist", `{"kind":"follows","source":{"id":"ws-none"}}`, nil, http.StatusForbidden, ""},
		{
			"a last saver that a start would refuse", scheduleDoc,
			func(r *rig) { r.savers.err = trigger.ErrSaverDenied }, http.StatusForbidden, "trigger.saver-denied",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := fullRig(t)
			if tt.setup != nil {
				tt.setup(r)
			}

			code, body, _ := do(t, r, http.MethodPost, basePath, tt.body)
			if code != tt.status {
				t.Fatalf("status = %d %s, want %d", code, body, tt.status)
			}

			if got := decode[errAnswer](t, body); got.ErrorCode != tt.errorCode || got.Code != tt.status || got.Message == "" {
				t.Errorf("answer = %+v, want code %q", got, tt.errorCode)
			}

			if r.store.written() != 0 {
				t.Errorf("a refused save wrote %d times", r.store.written())
			}
		})
	}
}

func TestCreateCycleNamesTheUnits(t *testing.T) {
	t.Parallel()

	r := fullRig(t)

	// app follows net.
	code, body, _ := do(t, r, http.MethodPost, "/api/dustpan/v1/namespaces/acme/units/"+appID+"/triggers",
		`{"kind":"follows","source":{"id":"`+netID+`"}}`)
	if code != http.StatusCreated {
		t.Fatalf("status = %d %s, want 201", code, body)
	}

	// net follows app: a loop.
	code, body, _ = do(t, r, http.MethodPost, basePath, `{"kind":"follows","source":{"id":"`+appID+`"}}`)
	if code != http.StatusConflict {
		t.Fatalf("status = %d %s, want 409", code, body)
	}

	got := decode[errAnswer](t, body)
	if got.ErrorCode != "trigger.cycle" || len(got.Units) != 3 {
		t.Fatalf("answer = %+v, want the cycle code and three units in the path", got)
	}

	// The path starts and ends with the same unit and uses the names and tool
	// labels of the tools.
	first, last := got.Units[0], got.Units[len(got.Units)-1]
	if first != last || first.ID != appID || first.Name != "app" || first.Tool != "Pulumi" ||
		got.Units[1].ID != netID || got.Units[1].Name != "net" || got.Units[1].Tool != "OpenTofu" {
		t.Errorf("units = %+v", got.Units)
	}

	if !strings.Contains(got.Message, "app -> net -> app") {
		t.Errorf("message = %q, want the path by name", got.Message)
	}
}

func TestReplaceGetListDelete(t *testing.T) {
	t.Parallel()

	r := fullRig(t)

	if code, body, _ := do(t, r, http.MethodPost, basePath, scheduleDoc); code != http.StatusCreated {
		t.Fatalf("create = %d %s", code, body)
	}

	path := basePath + "/trg-1"

	// Replace.
	code, body, _ := do(t, r, http.MethodPut, path, `{"kind":"schedule","frequency":{"cron":"0 6 * * *"},"enabled":false}`)
	if a := decode[answer](t, body); code != http.StatusOK || a.Enabled || a.Frequency == nil || a.Frequency.Cron != "0 6 * * *" {
		t.Fatalf("replace = %d %s", code, body)
	}

	// Replace with another kind.
	code, body, _ = do(t, r, http.MethodPut, path, `{"kind":"review-request-push"}`)
	if got := decode[errAnswer](t, body); code != http.StatusBadRequest || got.ErrorCode != "trigger.invalid" {
		t.Fatalf("replace with another kind = %d %s, want 400", code, body)
	}

	// Get and list.
	code, body, _ = do(t, r, http.MethodGet, path, "")
	if a := decode[answer](t, body); code != http.StatusOK || a.ID != "trg-1" || a.Enabled {
		t.Fatalf("get = %d %s", code, body)
	}

	code, body, _ = do(t, r, http.MethodGet, basePath, "")
	if list := decode[struct {
		Triggers []answer `json:"triggers"`
	}](t, body); code != http.StatusOK || len(list.Triggers) != 1 || list.Triggers[0].ID != "trg-1" {
		t.Fatalf("list = %d %s", code, body)
	}

	// The list of a unit with no trigger is empty and not null.
	code, body, _ = do(t, r, http.MethodGet, "/api/dustpan/v1/namespaces/acme/units/"+appID+"/triggers", "")
	if code != http.StatusOK || !strings.Contains(body, `"triggers":[]`) {
		t.Fatalf("list of an empty unit = %d %s, want an empty array", code, body)
	}

	// Delete, then the trigger is gone.
	if code, body, _ := do(t, r, http.MethodDelete, path, ""); code != http.StatusNoContent || body != "" {
		t.Fatalf("delete = %d %q", code, body)
	}

	if code, _, _ := do(t, r, http.MethodGet, path, ""); code != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", code)
	}

	if code, _, _ := do(t, r, http.MethodDelete, path, ""); code != http.StatusNotFound {
		t.Fatalf("second delete = %d, want 404", code)
	}
}

func TestNeedsNewSave(t *testing.T) {
	t.Parallel()

	r := fullRig(t)
	seed(r)

	_, body, _ := do(t, r, http.MethodGet, basePath+"/trg-seed", "")
	if decode[answer](t, body).NeedsNewSave {
		t.Fatalf("a saver that holds the actions needs a new save: %s", body)
	}

	r.savers.err = trigger.ErrSaverDenied

	_, body, _ = do(t, r, http.MethodGet, basePath+"/trg-seed", "")
	if !decode[answer](t, body).NeedsNewSave {
		t.Fatalf("a saver that lost access does not need a new save: %s", body)
	}

	_, body, _ = do(t, r, http.MethodGet, basePath, "")
	if list := decode[struct {
		Triggers []answer `json:"triggers"`
	}](t, body); len(list.Triggers) != 1 || !list.Triggers[0].NeedsNewSave {
		t.Fatalf("list = %s, want a trigger that needs a new save", body)
	}
}

func TestFire(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		body      string
		setup     func(r *rig)
		status    int
		errorCode string
		reason    string
	}{
		{"no body", ``, nil, http.StatusCreated, "", ""},
		{"a reason", `{"reason":"test run"}`, nil, http.StatusCreated, "", "test run"},
		{"an empty object", `{}`, nil, http.StatusCreated, "", ""},
		{"an unknown field", `{"reason":"x","executor":"write-back"}`, nil, http.StatusBadRequest, "request.body-invalid", ""},
		{"data after the document", `{} {}`, nil, http.StatusBadRequest, "request.body-invalid", ""},
		{"a reason that is too long", `{"reason":"` + strings.Repeat("x", 201) + `"}`, nil, http.StatusBadRequest, "trigger.invalid", ""},
		{
			"a trigger that is off", ``, func(r *rig) { r.disp.err = trigger.ErrDisabled }, http.StatusConflict,
			"trigger.disabled", "",
		},
		{
			"a saver that lost access", ``, func(r *rig) { r.disp.err = trigger.ErrSaverDenied }, http.StatusForbidden,
			"trigger.saver-denied", "",
		},
		{"a freeze", ``, func(r *rig) { r.disp.err = job.ErrFrozen }, http.StatusConflict, "job.frozen", ""},
		{"a limit", ``, func(r *rig) { r.disp.err = job.ErrLimit }, http.StatusTooManyRequests, "job.limit", ""},
		{"a policy", ``, func(r *rig) { r.disp.err = job.ErrPolicyDenied }, http.StatusForbidden, "job.policy-denied", ""},
		{"an enforced gate", ``, func(r *rig) { r.disp.err = job.ErrGateEnforced }, http.StatusForbidden, "job.gate-enforced", ""},
		{"an unsupported operation", ``, func(r *rig) { r.disp.err = job.ErrUnsupported }, http.StatusUnprocessableEntity, "job.unsupported", ""},
		{"a denied action", ``, func(r *rig) { r.disp.err = identity.ErrDenied }, http.StatusForbidden, "", ""},
		{"a failure of the server", ``, func(r *rig) { r.disp.err = errors.New("database is down: password=x") }, http.StatusInternalServerError, "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := fullRig(t)
			seed(r)

			if tt.setup != nil {
				tt.setup(r)
			}

			code, body, header := do(t, r, http.MethodPost, basePath+"/trg-seed/fire", tt.body)
			if code != tt.status {
				t.Fatalf("status = %d %s, want %d", code, body, tt.status)
			}

			if code != http.StatusCreated {
				got := decode[errAnswer](t, body)
				if got.ErrorCode != tt.errorCode {
					t.Errorf("error code = %q, want %q", got.ErrorCode, tt.errorCode)
				}

				if strings.Contains(body, "password") {
					t.Errorf("the answer repeats the text of an internal error: %s", body)
				}

				return
			}

			var res struct {
				Job struct {
					ID string `json:"id"`
				} `json:"job"`
			}

			if err := json.Unmarshal([]byte(body), &res); err != nil || res.Job.ID != "job-abc" {
				t.Fatalf("answer = %s, want the job job-abc", body)
			}

			if got := header.Get("Location"); got != "/api/dustpan/v1/namespaces/acme/jobs/job-abc" {
				t.Errorf("Location = %q", got)
			}

			if len(r.disp.reqs) != 1 || r.disp.subjs[0] != ada || r.disp.reqs[0].Trigger != "trg-seed" ||
				r.disp.reqs[0].Namespace != "acme" || r.disp.reqs[0].Reason != tt.reason {
				t.Errorf("dispatcher calls = %+v by %v", r.disp.reqs, r.disp.subjs)
			}
		})
	}
}
