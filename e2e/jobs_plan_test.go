//go:build e2e

package e2e_test

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// Contents of the plan parts of the scenario. The apply part stands for a plan
// file that can hold a secret value, so the scenario also proves that the blob
// directory holds none of its text.
const (
	planDisplayText = `{"changes": 3}`
	planApplySecret = "plan-secret-value-"
)

// planPartsScenario drives the upload and the commit of a plan through the job
// session of a plan phase, on the real recorder and store: the hostile calls
// change nothing, the commit stores the plan with the hashes of the parts, a
// second commit with the same parts is the same call, and a commit with other
// parts is refused. The job API shows the stored plan, and, on the binary
// server, the blob directory holds none of the plaintext.
func planPartsScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	run := openLaunchedSession(t, env)
	applyText := planApplySecret + run.job

	upload := func(part string, content []byte) (int, []byte) {
		return run.launcher.PutLog(ctx, t, planPartPath(protocol.PathSessionPlanUpload, part), content)
	}
	commit := func(parts ...protocol.PlanPart) (int, []byte) {
		return run.launcher.Post(ctx, t, protocol.PathSessionPlanCommit, protocol.PlanCommit{Parts: parts})
	}

	display := planPartOf("plan-display", []byte(planDisplayText))
	apply := planPartOf("plan-apply", []byte(applyText))

	// A name that is no plan part is refused before any byte is stored.
	wantInvalid(t, "upload of a log as a plan part", func() (int, []byte) { return upload("log", []byte("x")) })
	wantInvalid(t, "upload with a capital in the name", func() (int, []byte) {
		return upload("Plan-Apply", []byte("x"))
	})

	// A body that is not the signed body is refused as bad-proof and stores
	// nothing: a commit that names the signed content finds no part.
	signed := []byte("the content that the proof signs")

	status, body := run.launcher.PutTampered(ctx, t, planPartPath(protocol.PathSessionPlanUpload, "plan-apply"),
		signed, []byte("another content on the wire"))
	if status != http.StatusUnauthorized || !strings.Contains(string(body), protocol.ErrorBadProof) {
		t.Fatalf("upload with a body that is not the signed body = %d %s, want 401 %s", status, body,
			protocol.ErrorBadProof)
	}

	wantInvalid(t, "commit of the part that was refused", func() (int, []byte) {
		return commit(planPartOf("plan-apply", signed))
	})

	// A plan phase reads no plan part, and a name that is no plan part is refused.
	wantInvalid(t, "read of a plan part in a plan phase", func() (int, []byte) {
		return run.launcher.Get(ctx, t, planPartPath(protocol.PathSessionPlanPart, "plan-apply"))
	})
	wantInvalid(t, "read of a log as a plan part", func() (int, []byte) {
		return run.launcher.Get(ctx, t, planPartPath(protocol.PathSessionPlanPart, "log"))
	})

	wantNoContent(t, "upload display", func() (int, []byte) { return upload("plan-display", []byte(planDisplayText)) })
	wantNoContent(t, "upload apply", func() (int, []byte) { return upload("plan-apply", []byte(applyText)) })
	wantNoContent(t, "upload display again", func() (int, []byte) {
		return upload("plan-display", []byte(planDisplayText))
	})

	// The parts are staged, not stored: the job has no plan yet.
	wantNoPlanInTheJob(ctx, t, env, run.job, display.SHA256, apply.SHA256)

	// A commit that names another hash than the one the server holds changes
	// nothing, and the staged parts stay.
	wrong := apply
	wrong.SHA256 = hex.EncodeToString(sha256Of([]byte("another plan")))

	wantConflict(t, "commit with another hash", func() (int, []byte) { return commit(display, wrong) })

	wantNoPlanInTheJob(ctx, t, env, run.job, display.SHA256, apply.SHA256)

	wantNoContent(t, "commit", func() (int, []byte) { return commit(display, apply) })
	wantNoContent(t, "second commit", func() (int, []byte) { return commit(display, apply) })

	wantConflict(t, "commit with other parts", func() (int, []byte) { return commit(display) })

	// A part after the commit does not change the plan.
	wantConflict(t, "upload after the commit", func() (int, []byte) { return upload("plan-display", []byte("late")) })

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "read the stored plan", Method: http.MethodGet,
		Path: jobPath(e2e.Namespace, run.job), Body: "", Want: http.StatusOK, Scheme: "token",
		Check: func(body string) error {
			for _, want := range []string{display.SHA256, apply.SHA256} {
				if !strings.Contains(body, want) {
					return fmt.Errorf("%w: the job holds no part with the hash %s: %s", errLauncherFlow, want, body)
				}
			}

			return nil
		},
	})

	if env.InProcess() {
		return
	}

	// The stored parts are sealed: no file of the blob directory holds the text.
	if env.BlobDirHolds(t, applyText) || env.BlobDirHolds(t, planDisplayText) {
		t.Fatal("a blob of the blob directory holds the plaintext of a plan part")
	}
}

// wantConflict calls send and fails the test when its status is not 409 with
// the code conflict.
func wantConflict(t *testing.T, name string, send func() (int, []byte)) {
	t.Helper()

	status, body := send()
	if status != http.StatusConflict || !strings.Contains(string(body), protocol.ErrorConflict) {
		t.Fatalf("%s = %d %s, want 409 %s", name, status, body, protocol.ErrorConflict)
	}
}

// planPartPath returns the path of a plan route with the part name filled in.
func planPartPath(pattern, part string) string {
	return strings.NewReplacer("{part}", part).Replace(pattern)
}

// planPartOf returns the part of a commit for the kind and the content.
func planPartOf(kind string, content []byte) protocol.PlanPart {
	return protocol.PlanPart{Kind: kind, Size: int64(len(content)), SHA256: hex.EncodeToString(sha256Of(content))}
}

// wantNoPlanInTheJob fails the test when the job holds a part with one of the
// hashes.
func wantNoPlanInTheJob(ctx context.Context, t *testing.T, env *e2e.Env, id string, hashes ...string) {
	t.Helper()

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		UserAgent: jobsAgent, Name: "read the job before the commit", Method: http.MethodGet,
		Path: jobPath(e2e.Namespace, id), Body: "", Want: http.StatusOK, Scheme: "token",
		Check: func(body string) error {
			for _, h := range hashes {
				if strings.Contains(body, h) {
					return fmt.Errorf("%w: the job holds a plan before the commit: %s", errLauncherFlow, body)
				}
			}

			return nil
		},
	})
}
