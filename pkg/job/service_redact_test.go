package job_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// svcArtifacts is a job.ArtifactStore over one log text. It records the calls
// in order.
type svcArtifacts struct {
	text      string
	openErr   error
	replace   error
	redactErr error

	calls    []string
	replaced []byte
	redacted []history.Redaction
	entries  []audit.Entry
	by       string
}

func newSvcArtifacts(text string) *svcArtifacts {
	return &svcArtifacts{
		text: text, openErr: nil, replace: nil, redactErr: nil, calls: nil, replaced: nil, redacted: nil,
		entries: nil, by: "",
	}
}

func (*svcArtifacts) OpenPlanPart(context.Context, namespace.Name, string, job.OutputKind) (io.ReadCloser, error) {
	return nil, job.ErrNotFound
}

func (a *svcArtifacts) OpenLog(_ context.Context, _ namespace.Name, version string) (io.ReadCloser, error) {
	a.calls = append(a.calls, "open "+version)

	if a.openErr != nil {
		return nil, a.openErr
	}

	return io.NopCloser(strings.NewReader(a.text)), nil
}

func (a *svcArtifacts) ReplaceLog(_ context.Context, r job.LogReplacement) (job.LogRef, error) {
	a.calls = append(a.calls, "replace "+r.From)

	if a.replace != nil {
		return job.LogRef{}, a.replace
	}

	text, err := io.ReadAll(r.Body)
	if err != nil {
		return job.LogRef{}, fmt.Errorf("read body: %w", err)
	}

	a.replaced, a.by = text, r.By

	return job.LogRef{
		ID: "jlv-2", ItemKey: "", Version: 2, Chunks: 1, SHA256: "", Size: int64(len(text)),
		CreatedAt: time.Time{}, ClosedAt: time.Time{}, ExpiresAt: time.Time{}, RedactedAt: time.Time{},
		ReplacedBy: r.By, Matches: 0,
	}, nil
}

func (a *svcArtifacts) Redact(
	_ context.Context, r history.Redaction, _ []string, e audit.Entry, _ namespace.Fence,
) (history.Redaction, error) {
	a.calls = append(a.calls, "redact "+r.Item.Item)

	if a.redactErr != nil {
		return history.Redaction{}, a.redactErr
	}

	a.redacted = append(a.redacted, r)
	a.entries = append(a.entries, e)

	return r, nil
}

const redactLogID = "jlv-1"

// redactRig returns a rig whose authorizer lets every subject read the job and
// redact on the unit, over the store of logs arts.
func redactRig(t *testing.T, arts *svcArtifacts, opts ...rigOption) *rig {
	t.Helper()

	opts = append([]rigOption{func(cfg *job.ServiceConfig, r *rig) {
		cfg.Artifacts = arts

		for _, action := range []identity.Action{job.ActionJobRead, job.ActionUnitRedact} {
			r.authz.allow[string(action)+" "+r.unit.String()] = true
		}
	}}, opts...)

	return newRig(t, opts...)
}

// seedLog stores a job whose plan step holds the closed log version jlv-1.
func (r *rig) seedLog(mod func(*job.LogRef)) {
	closed := r.now.Add(-time.Hour)
	ref := job.LogRef{
		ID: redactLogID, ItemKey: "itk-1", Version: 1, Chunks: 1, SHA256: "", Size: 20,
		CreatedAt: closed.Add(-time.Minute), ClosedAt: closed, ExpiresAt: time.Time{}, RedactedAt: time.Time{},
		ReplacedBy: "", Matches: 0,
	}

	if mod != nil {
		mod(&ref)
	}

	r.seed(func(j *job.Job) {
		j.Phases[0].Steps = []job.StepRecord{{
			Name: "plan", Token: 1, ExitCode: 0, StartedAt: closed.Add(-time.Minute), FinishedAt: closed, Log: &ref,
		}}
	})
}

func subsOf(matches ...string) []job.Substitution {
	subs := make([]job.Substitution, 0, len(matches))
	for _, m := range matches {
		subs = append(subs, job.Substitution{Match: []byte(m)})
	}

	return subs
}

func redactReq(reason string, subs ...string) job.RedactRequest {
	return job.RedactRequest{Version: redactLogID, Substitutions: subsOf(subs...), Reason: reason}
}

func TestServiceActionsRedactReplacesEachMatch(t *testing.T) {
	t.Parallel()

	arts := newSvcArtifacts("token=hunter2 and s3cr3t; hunter2")
	r := redactRig(t, arts)
	r.seedLog(nil)

	got, err := r.svc.RedactLog(t.Context(), actorOf("carol"), approveID,
		redactReq("a token in the log", "hunter2", "s3cr3t"))
	if err != nil {
		t.Fatalf("RedactLog = %v", err)
	}

	if want := "token=[redacted] and [redacted]; [redacted]"; string(arts.replaced) != want {
		t.Errorf("new text = %q, want %q", arts.replaced, want)
	}

	if got.ID != "jlv-2" || got.Matches != 3 || arts.by != "carol" {
		t.Errorf("answer = %+v by %q, want jlv-2 with 3 matches by carol", got, arts.by)
	}

	// The new version is written first, then the old one is redacted.
	if want := "open jlv-1,replace jlv-1,redact jlv-1"; strings.Join(arts.calls, ",") != want {
		t.Errorf("calls = %v, want %s", arts.calls, want)
	}

	checkRedaction(t, r, arts)
	checkRedactionEntry(t, arts)
}

func checkRedaction(t *testing.T, r *rig, arts *svcArtifacts) {
	t.Helper()

	if len(arts.redacted) != 1 {
		t.Fatalf("redactions = %d, want 1", len(arts.redacted))
	}

	red := arts.redacted[0]
	if red.Cause != history.CauseRequest || red.Reason != "a token in the log" || red.ItemKey != "itk-1" ||
		red.By.Subject != "carol" || red.ID == "" {
		t.Errorf("redaction = %+v, want a request redaction by carol with the item key", red)
	}

	item := red.Item
	if item.Item != redactLogID || item.Kind != secret.ItemKindJobLog || item.Unit.ID != "unt-web" ||
		item.Unit.Namespace != r.unit.Namespace() {
		t.Errorf("item = %+v, want the log version jlv-1 of the unit", item)
	}
}

func checkRedactionEntry(t *testing.T, arts *svcArtifacts) {
	t.Helper()

	e := arts.entries[0]
	if e.Action != string(job.ActionUnitRedact) || e.Subject != "carol" || e.Outcome != audit.OutcomeSuccess ||
		e.Reason != "a token in the log" {
		t.Errorf("entry = %+v, want unit:redact by carol with the reason", e)
	}

	// The audit entry holds the number of matches and never the text.
	if text := fmt.Sprintf("%+v", e); strings.Contains(text, "hunter2") || strings.Contains(text, "s3cr3t") {
		t.Errorf("entry holds the text of a substitution: %s", text)
	}
}

// TestServiceActionsRedactReplacesInOnePass proves that the marker is never
// matched by a later substitution.
func TestServiceActionsRedactReplacesInOnePass(t *testing.T) {
	t.Parallel()

	arts := newSvcArtifacts("xx")
	r := redactRig(t, arts)
	r.seedLog(nil)

	got, err := r.svc.RedactLog(t.Context(), actorOf("carol"), approveID, redactReq("why", "x", "redacted", "[red"))
	if err != nil {
		t.Fatalf("RedactLog = %v", err)
	}

	if want := "[redacted][redacted]"; string(arts.replaced) != want || got.Matches != 2 {
		t.Errorf("new text = %q with %d matches, want %q with 2", arts.replaced, got.Matches, want)
	}
}

// redactCase is one request that RedactLog must refuse before it writes a
// version or a redaction.
type redactCase struct {
	name string
	mod  func(*job.LogRef)
	prep func(*rig, *svcArtifacts)
	id   job.ID
	req  job.RedactRequest
	want error
}

// redactRefuses returns a case of a redaction by carol of the log version
// jlv-1 that fails with want. The options change the request.
func redactRefuses(name string, want error, opts ...func(*redactCase)) redactCase {
	c := redactCase{
		name: name, mod: nil, prep: nil, id: approveID, req: redactReq("a token in the log", "hunter2"), want: want,
	}

	for _, o := range opts {
		o(&c)
	}

	return c
}

func rMod(f func(*job.LogRef)) func(*redactCase) { return func(c *redactCase) { c.mod = f } }

func rPrep(f func(*rig, *svcArtifacts)) func(*redactCase) { return func(c *redactCase) { c.prep = f } }

func rID(id job.ID) func(*redactCase) { return func(c *redactCase) { c.id = id } }

func rReq(f func(*job.RedactRequest)) func(*redactCase) { return func(c *redactCase) { f(&c.req) } }

// redactRefusals are the hostile requests, each against one layer alone.
func redactRefusals() []redactCase {
	key := func(r *rig, action identity.Action) string { return string(action) + " " + r.unit.String() }
	tooMany := strings.Split(strings.Repeat("a,", job.MaxSubstitutions+1), ",")
	tooLong := strings.Repeat("a", job.MaxSubstitutionBytes+1)

	return []redactCase{
		redactRefuses("unknown job", job.ErrNotFound, rID("job-missing")),
		redactRefuses("id with no job prefix", job.ErrNotFound, rID("x-1")),
		redactRefuses("no read action", job.ErrNotFound,
			rPrep(func(r *rig, _ *svcArtifacts) { delete(r.authz.allow, key(r, job.ActionJobRead)) })),
		redactRefuses("read but no redact action", identity.ErrDenied,
			rPrep(func(r *rig, _ *svcArtifacts) { delete(r.authz.allow, key(r, job.ActionUnitRedact)) })),
		redactRefuses("the authorizer fails on redact", identity.ErrDenied,
			rPrep(func(r *rig, _ *svcArtifacts) { r.authz.fail[key(r, job.ActionUnitRedact)] = errNoRights })),
		redactRefuses("version of no step of the job", job.ErrNotFound,
			rReq(func(q *job.RedactRequest) { q.Version = "jlv-9" })),
		redactRefuses("empty version", job.ErrNotFound, rReq(func(q *job.RedactRequest) { q.Version = "" })),
		redactRefuses("log that is open", history.ErrInProgress,
			rMod(func(l *job.LogRef) { l.ClosedAt = time.Time{} })),
		redactRefuses("log that is redacted", history.ErrRedacted,
			rMod(func(l *job.LogRef) { l.RedactedAt = l.CreatedAt })),
		redactRefuses("log that is too large", job.ErrInvalid,
			rMod(func(l *job.LogRef) { l.Size = job.MaxRedactLogBytes + 1 })),
		redactRefuses("text that is too large", job.ErrInvalid,
			rPrep(func(_ *rig, a *svcArtifacts) { a.text = strings.Repeat("a", job.MaxRedactLogBytes+1) })),
		redactRefuses("the log cannot be opened", errDriver,
			rPrep(func(_ *rig, a *svcArtifacts) { a.openErr = errDriver })),
		redactRefuses("the log is shredded", secret.ErrShredded,
			rPrep(func(_ *rig, a *svcArtifacts) { a.openErr = secret.ErrShredded })),
		redactRefuses("no substitution matches", job.ErrInvalid,
			rReq(func(q *job.RedactRequest) { q.Substitutions = subsOf("absent") })),
		redactRefuses("no substitution", job.ErrInvalid,
			rReq(func(q *job.RedactRequest) { q.Substitutions = []job.Substitution{} })),
		redactRefuses("empty match", job.ErrInvalid,
			rReq(func(q *job.RedactRequest) { q.Substitutions = subsOf("a", "") })),
		redactRefuses("too many substitutions", job.ErrInvalid,
			rReq(func(q *job.RedactRequest) { q.Substitutions = subsOf(tooMany...) })),
		redactRefuses("match too long", job.ErrInvalid,
			rReq(func(q *job.RedactRequest) { q.Substitutions = subsOf(tooLong) })),
		redactRefuses("empty reason", job.ErrInvalid, rReq(func(q *job.RedactRequest) { q.Reason = "" })),
		redactRefuses("hidden character in the reason", job.ErrInvalid,
			rReq(func(q *job.RedactRequest) { q.Reason = "r" + bidiOverride() })),
	}
}

// TestServiceActionsRedactRefuses holds the hostile cases: each guard of
// RedactLog refuses before it writes a version or a redaction.
func TestServiceActionsRedactRefuses(t *testing.T) {
	t.Parallel()

	for _, tc := range redactRefusals() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			arts := newSvcArtifacts("token=hunter2")
			r := redactRig(t, arts)
			r.seedLog(tc.mod)

			if tc.prep != nil {
				tc.prep(r, arts)
			}

			_, err := r.svc.RedactLog(t.Context(), actorOf("carol"), tc.id, tc.req)
			if !errors.Is(err, tc.want) {
				t.Fatalf("RedactLog = %v, want %v", err, tc.want)
			}

			for _, c := range arts.calls {
				if strings.HasPrefix(c, "replace") || strings.HasPrefix(c, "redact") {
					t.Errorf("a refused redaction made the call %q", c)
				}
			}
		})
	}
}

func TestServiceActionsRedactStopsOnAStoreFault(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		arts       func(*svcArtifacts)
		fence      bool
		want       error
		wantRedact bool
	}{
		{"replace fails", func(a *svcArtifacts) { a.replace = errDriver }, false, errDriver, false},
		{"redact fails", func(a *svcArtifacts) { a.redactErr = history.ErrLatest }, false, history.ErrLatest, true},
		{"fence fails", func(*svcArtifacts) {}, true, errDriver, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			arts := newSvcArtifacts("token=hunter2")
			tc.arts(arts)

			r := redactRig(t, arts, func(cfg *job.ServiceConfig, _ *rig) {
				if tc.fence {
					cfg.Fences = svcFences{err: errDriver}
				}
			})
			r.seedLog(nil)

			_, err := r.svc.RedactLog(t.Context(), actorOf("carol"), approveID, redactReq("why", "hunter2"))
			if !errors.Is(err, tc.want) {
				t.Fatalf("RedactLog = %v, want %v", err, tc.want)
			}

			if redacted := strings.Contains(strings.Join(arts.calls, ","), "redact"); redacted != tc.wantRedact {
				t.Errorf("calls = %v, want a redact call: %t", arts.calls, tc.wantRedact)
			}
		})
	}
}
