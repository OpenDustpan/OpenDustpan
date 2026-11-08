//go:build e2e

package e2e_test

import (
	"context"
	"crypto/md5" //nolint:gosec // The TFE protocol names state by MD5 for integrity, not security.
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// crashPadBytes is the padding each crash write carries: large enough that
// the request is still in flight when the kill lands after the sent signal,
// small enough that the steps stay fast.
const crashPadBytes = 2 << 20

// TestBinaryCrash stops the server with SIGKILL during a Pulumi checkpoint
// save, an OpenTofu state version upload and a held lock (ADR 40 decision 2).
// After each next start it checks the invariants: no SQL row names a missing
// blob (the export and the state download resolve), no lease, lock or upload
// capability stays usable when it must not, the server starts, and the CLI
// continues. It runs the first CLI version of each tool on every store, with
// the real server binary only: the in-process server cannot be killed. The
// restart reuses the same database and blob storage on the same port.
func TestBinaryCrash(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		// OpenTofu must trust the test server's certificate, and Go honors
		// SSL_CERT_FILE only on Linux; elsewhere it uses the system store.
		t.Skipf("the crash test needs Linux, not %s", runtime.GOOS)
	}

	if !e2e.UseBinaryServer() {
		t.Skip("the SIGKILL crash tests stop the real server binary, " +
			"so they need E2E_SERVER=binary; the in-process server cannot be killed")
	}

	fetcher := &e2e.Fetcher{Client: http.DefaultClient, CacheDir: cacheDir(t)}

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	pulumiVersion := e2e.Pulumi().Single()
	tofuVersion := e2e.OpenTofu().Single()

	if pulumiVersion == "" || tofuVersion == "" {
		t.Fatal("each tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	pulumiCLI, err := fetcher.Install(installCtx, e2e.Pulumi(), pulumiVersion)
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, pulumiVersion, err)
	}

	tofuCLI, err := fetcher.Install(installCtx, e2e.OpenTofu(), tofuVersion)
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.OpenTofu().Name, tofuVersion, err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runCrashOnStore(t, pulumiCLI, tofuCLI, db)
		})
	}
}

// runCrashOnStore runs the three SIGKILL crashes on one store: a checkpoint
// save, a state version upload and a held lock, each followed by a restart
// with the invariant checks.
func runCrashOnStore(t *testing.T, pulumiCLI, tofuCLI e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	if e2e.UseS3Blobs(t) {
		if _, ok := e2e.LookupS3Config(); !ok {
			t.Skip(e2e.S3SkipReason())
		}
	}

	ctx := t.Context()
	env := e2e.NewEnv(ctx, t, pulumiCLI, store)

	checkReady(ctx, t, env)
	crashPulumiCheckpoint(t, env)
	crashTofuUpload(t, env, tofuCLI)
	crashHeldLock(t, env)
}

// crashPad returns n deterministic bytes, so each crash write carries the
// same body on every run without a test hook: the size alone holds the
// request in flight while the kill lands.
func crashPad(n int) string {
	const prefix = "crash-pad-v1:"

	out := make([]byte, 0, n)

	var counter uint64

	for len(out) < n {
		sum := sha256.Sum256([]byte(prefix + strconv.FormatUint(counter, 10)))
		counter++

		for _, b := range sum {
			if len(out) >= n {
				break
			}

			out = append(out, 'a'+b%26)
		}
	}

	return string(out)
}

// crashRestart proves the server is down after the kill, starts it again on
// the same database and blob storage, and proves the probes answer again.
func crashRestart(ctx context.Context, t *testing.T, env *e2e.Env, tag string) {
	t.Helper()

	status, _, err := env.APIStatus(ctx, http.MethodGet, "/health/live", "")
	if err == nil && status == http.StatusNoContent {
		t.Fatalf("GET /health/live answers 204 after the SIGKILL during %s", tag)
	}

	t.Logf("GET /health/live after the SIGKILL during %s: status %d, err %v", tag, status, err)

	env.StartServer(ctx, t)

	liveStatus, liveBody, liveErr := env.APIStatus(ctx, http.MethodGet, "/health/live", "")
	if liveErr != nil || liveStatus != http.StatusNoContent {
		t.Fatalf("GET /health/live after the restart answers %d, want %d (err %v, body %q)",
			liveStatus, http.StatusNoContent, liveErr, liveBody)
	}

	readyStatus, readyBody, readyErr := env.APIStatus(ctx, http.MethodGet, "/health/ready", "")
	if readyErr != nil || readyStatus != http.StatusOK || !strings.Contains(readyBody, `"status":"ok"`) {
		t.Fatalf("GET /health/ready after the restart answers %d, want %d (err %v, body %q)",
			readyStatus, http.StatusOK, readyErr, readyBody)
	}

	env.NoteStep(t, "restart after "+tag, "the server answers live 204 and ready ok "+
		"on the same database and blob storage")
}

// crashCheckpointBody returns the whole-checkpoint PATCH body for held: the
// exported deployment with a deterministic pad merged in.
func crashCheckpointBody(t *testing.T, exportStdout string) []byte {
	t.Helper()

	var doc struct {
		Deployment map[string]any `json:"deployment"`
		Version    int            `json:"version"`
	}

	if err := json.Unmarshal([]byte(exportStdout), &doc); err != nil {
		t.Fatalf("decode stack export: %v", err)
	}

	if doc.Deployment == nil {
		t.Fatal("decode stack export: no deployment to save")
	}

	doc.Deployment["e2eCrashPad"] = crashPad(crashPadBytes)

	raw, err := json.Marshal(doc.Deployment)
	if err != nil {
		t.Fatalf("encode crash checkpoint: %v", err)
	}

	return []byte(fmt.Sprintf(`{"version":%d,"deployment":%s}`, doc.Version, raw))
}

// crashPulumiCheckpoint kills the server with SIGKILL while a padded
// checkpoint save is in flight, then proves the invariants: the export
// resolves (no row names a missing blob), the running update still blocks a
// second one, cancel frees it, the old lease no longer writes, and the next
// up succeeds.
func crashPulumiCheckpoint(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	org := e2e.Namespace
	qualified := fmt.Sprintf("%s/%s/%s", org, e2e.Project, stackName)

	for _, s := range []e2e.StepSpec{
		step("login", contains(env.URL), "login"),
		step("stack init", nil, "stack", "init", qualified),
		step("stack init other", nil, "stack", "init", "other"),
		step("stack select", nil, "stack", "select", qualified),
		step("config set secret", nil, "config", "set", "--secret", "apiKey", "s3cr3t"),
		step("up", nil, "up", "--yes", "--non-interactive"),
	} {
		env.Step(ctx, t, s)
	}

	exported := env.Step(ctx, t, step("stack export", exportHoldsReference, "stack", "export"))

	held := env.StartHeldUpdate(ctx, t, org, e2e.Project, stackName)
	prefix := "/api/stacks/" + org + "/" + e2e.Project + "/" + stackName
	body := crashCheckpointBody(t, exported.Stdout)

	// The kill follows the sent signal with no sleep: the padded body holds
	// the save in flight, so the server dies inside the write. When the save
	// already committed, the kill still lands inside the update, and every
	// check below holds for both windows.
	env.Recorder.SetStep("crash checkpoint save")

	call := env.StartAsyncCall(ctx, e2e.AsyncRequest{
		Method:      http.MethodPatch,
		Target:      env.URL + prefix + "/update/" + held.ID + "/checkpoint",
		Auth:        "update-token " + held.Lease,
		ContentType: "application/json",
		Body:        body,
	})

	<-call.Sent()
	env.KillServer(t)

	status, _, err := call.Wait()
	env.NoteStep(t, "kill during checkpoint save",
		fmt.Sprintf("the save answers status %d with err %v", status, err))

	crashRestart(ctx, t, env, "a checkpoint save")

	// The export resolves whatever the kill left: the padded checkpoint when
	// the save committed, the base one when it did not. Either row names a
	// blob that exists.
	env.Step(ctx, t, step("stack export after kill", nil, "stack", "export"))
	env.Step(ctx, t, failStep(
		"up blocked after kill",
		outputContains("another update of this stack is in progress"),
		"up", "--yes", "--non-interactive",
	))
	env.Step(ctx, t, step("cancel after kill", contains("has been canceled"), "cancel", "--yes"))

	// The lease of the ended update no longer writes.
	crashLeaseRefused(ctx, t, env, leaseRefusedInput{held: held, prefix: prefix, tag: "checkpoint save"})

	env.Step(
		ctx,
		t,
		step("up after kill", checkCheckpointSaved(env, "up after kill"), "up", "--yes", "--non-interactive"),
	)
	env.Step(ctx, t, step("stack export after recovery", exportHoldsReference, "stack", "export"))
}

// leaseRefusedInput groups what the old-lease check needs, so the check
// takes one argument next to the context.
type leaseRefusedInput struct {
	held   e2e.HeldUpdate
	prefix string
	tag    string
}

// crashLeaseRefused proves the lease of held no longer writes after its
// update ended: completing and saving with it each answer an error.
func crashLeaseRefused(ctx context.Context, t *testing.T, env *e2e.Env, in leaseRefusedInput) {
	t.Helper()

	held, prefix, tag := in.held, in.prefix, in.tag

	got, err := env.StatusWithHeader(
		ctx,
		"update-token "+held.Lease,
		http.MethodPost,
		prefix+"/update/"+held.ID+"/complete",
		`{"status":"succeeded"}`,
	)
	if err != nil {
		t.Fatalf("complete with the old lease after %s: %v", tag, err)
	}

	if got.Status < http.StatusBadRequest {
		t.Fatalf("complete with the old lease after %s answers %d, want an error (%s)",
			tag, got.Status, got.Body)
	}

	save := env.StartAsyncCall(ctx, e2e.AsyncRequest{
		Method:      http.MethodPatch,
		Target:      env.URL + prefix + "/update/" + held.ID + "/checkpoint",
		Auth:        "update-token " + held.Lease,
		ContentType: "application/json",
		Body:        []byte(`{"version":3,"deployment":{}}`),
	})

	saveStatus, saveBody, saveErr := save.Wait()
	if saveErr == nil && saveStatus < http.StatusBadRequest {
		t.Fatalf("save with the old lease after %s answers %d, want an error (%s)", tag, saveStatus, saveBody)
	}

	env.NoteStep(t, "old lease refused after "+tag,
		fmt.Sprintf("complete answers %d and save answers %d", got.Status, saveStatus))
}

// crashUploadCreate is the parsed 201 answer of a state version create: the
// version ID with its upload URL.
type crashUploadCreate struct {
	ID         string                `json:"id"`
	Attributes crashUploadAttributes `json:"attributes"`
}

// crashUploadAttributes holds the upload URL of a state version document.
type crashUploadAttributes struct {
	//nolint:tagliatelle // The TFE wire name; it must not change.
	UploadURL string `json:"hosted-state-upload-url"`
}

// crashStateContent returns pulled with its serial replaced and a
// deterministic pad output added, so the upload carries crashPadBytes.
func crashStateContent(t *testing.T, pulled string, serial int64, lineage string) []byte {
	t.Helper()

	doc := editedState(t, pulled, serial, lineage)

	var fields map[string]json.RawMessage

	if err := json.Unmarshal(doc, &fields); err != nil {
		t.Fatalf("decode edited state: %v", err)
	}

	var outputs map[string]json.RawMessage

	if raw, ok := fields["outputs"]; ok {
		if err := json.Unmarshal(raw, &outputs); err != nil {
			t.Fatalf("decode state outputs: %v", err)
		}
	}

	if outputs == nil {
		outputs = map[string]json.RawMessage{}
	}

	pad, err := json.Marshal(map[string]string{"value": crashPad(crashPadBytes), "type": "string"})
	if err != nil {
		t.Fatalf("encode crash pad output: %v", err)
	}

	outputs["e2e_crash_pad"] = pad

	rawOutputs, err := json.Marshal(outputs)
	if err != nil {
		t.Fatalf("encode state outputs: %v", err)
	}

	fields["outputs"] = rawOutputs

	out, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encode crash state: %v", err)
	}

	return out
}

// crashTofuUpload kills the server with SIGKILL while a padded state version
// upload is in flight, then proves the invariants: the workspace stays locked
// by its holder, another principal still conflicts, the upload capability no
// longer takes other content once finalized (or the same content retries when
// the kill won), the download resolves, and the CLI applies again.
func crashTofuUpload(t *testing.T, env *e2e.Env, tofuCLI e2e.Install) {
	t.Helper()

	ctx := t.Context()
	env.SetCLI(tofuCLI)

	for _, s := range []e2e.StepSpec{
		step("init", nil, "init", "-input=false", "-no-color"),
		step("apply", nil, "apply", "-input=false", "-auto-approve", "-no-color"),
	} {
		env.Step(ctx, t, s)
	}

	pulled := env.Step(ctx, t, step("state pull", contains(`"greeting"`), "state", "pull"))

	var current pulledStateDoc

	if err := json.Unmarshal([]byte(pulled.Stdout), &current); err != nil {
		t.Fatalf("decode pulled state: %v", err)
	}

	if current.Lineage == "" {
		t.Fatal("pulled state has no lineage")
	}

	wsID := env.WorkspaceID(ctx, t)
	lockPath := "/api/v2/workspaces/" + wsID + "/actions/lock"

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:     nil,
		Name:      "lock for crash upload",
		Method:    http.MethodPost,
		Path:      lockPath,
		Body:      `{"data":{}}`,
		Want:      http.StatusOK,
		UserAgent: "",
		Scheme:    "",
	})

	newSerial := current.Serial + 1
	content := crashStateContent(t, pulled.Stdout, newSerial, current.Lineage)
	sum := md5.Sum(content) //nolint:gosec // The TFE protocol names state by MD5 for integrity.

	env.Recorder.SetStep("crash state upload")

	status, body, err := env.TFEStatusA(ctx, http.MethodPost, "/api/v2/workspaces/"+wsID+"/state-versions", fmt.Sprintf(
		`{"data":{"type":"state-versions","attributes":{"serial":%d,"lineage":%q,"md5":%q}}}`,
		newSerial,
		current.Lineage,
		hex.EncodeToString(sum[:]),
	))
	if err != nil {
		t.Fatalf("POST state-versions for the crash upload: %v", err)
	}

	if status != http.StatusCreated {
		t.Fatalf("POST state-versions for the crash upload answers %d, want %d (%s)",
			status, http.StatusCreated, body)
	}

	var created struct {
		Data crashUploadCreate `json:"data"`
	}

	if err := json.Unmarshal([]byte(body), &created); err != nil ||
		created.Data.ID == "" || created.Data.Attributes.UploadURL == "" {
		t.Fatalf("decode crash upload %q: %v", body, err)
	}

	// The kill follows the sent signal with no sleep: the padded content
	// holds the upload in flight, so the server dies inside the write.
	call := env.StartAsyncCall(ctx, e2e.AsyncRequest{
		Method:      http.MethodPut,
		Target:      created.Data.Attributes.UploadURL,
		Auth:        "",
		ContentType: "application/octet-stream",
		Body:        content,
	})

	<-call.Sent()
	env.KillServer(t)

	putStatus, _, putErr := call.Wait()
	env.NoteStep(t, "kill during state upload",
		fmt.Sprintf("the upload answers status %d with err %v", putStatus, putErr))

	crashRestart(ctx, t, env, "a state version upload")

	// The lock survived the crash with its holder.
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:     checkWorkspaceLocked,
		Name:      "workspace still locked after kill",
		Method:    http.MethodGet,
		Path:      "/api/v2/workspaces/" + wsID,
		Body:      "",
		Want:      http.StatusOK,
		UserAgent: "",
		Scheme:    "",
	})

	env.APIStepB(ctx, t, e2e.APIStepSpec{
		Check:     nil,
		Name:      "lock by B conflicts after kill",
		Method:    http.MethodPost,
		Path:      lockPath,
		Body:      `{"data":{}}`,
		Want:      http.StatusConflict,
		UserAgent: "",
		Scheme:    "",
	})

	crashUploadSettled(ctx, t, env, uploadSettledInput{
		wsID:      wsID,
		oldSerial: current.Serial,
		newSerial: newSerial,
		created:   created.Data,
		content:   content,
	})

	// The stored version resolves to its blob.
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:     containsCrashPad,
		Name:      "download after kill resolves",
		Method:    http.MethodGet,
		Path:      "/api/v2/state-versions/" + created.Data.ID + "/download",
		Body:      "",
		Want:      http.StatusOK,
		UserAgent: "",
		Scheme:    "",
	})

	env.Step(ctx, t, e2e.StepSpec{
		Check:  nil,
		Name:   "force-unlock after kill",
		Args:   []string{"force-unlock", "-force", e2e.Namespace + "/" + e2e.Workspace},
		Env:    env.TofuEnvB(),
		Stdin:  "",
		Fail:   false,
		Redact: false,
	})
	env.Step(ctx, t, step("apply after kill", nil, "apply", "-input=false", "-auto-approve", "-no-color"))
	env.Step(ctx, t, step("state pull after kill", contains(`"greeting"`), "state", "pull"))
}

// containsCrashPad proves a downloaded state holds the crash pad output.
func containsCrashPad(body string) error {
	if !strings.Contains(body, "e2e_crash_pad") {
		return fmt.Errorf("%w: downloaded state lacks the crash pad output", errUnexpected)
	}

	return nil
}

// uploadSettledInput groups what the upload outcome check needs, so the
// check takes one argument next to the context.
type uploadSettledInput struct {
	wsID      string
	oldSerial int64
	newSerial int64
	created   crashUploadCreate
	content   []byte
}

// crashUploadSettled proves the upload reached exactly one outcome: when the
// kill lost, the version is current and its capability no longer takes other
// content; when the kill won, the same content retries to current.
func crashUploadSettled(ctx context.Context, t *testing.T, env *e2e.Env, in uploadSettledInput) {
	t.Helper()

	wsID, oldSerial, newSerial := in.wsID, in.oldSerial, in.newSerial
	created, content := in.created, in.content

	serial := crashCurrentSerial(ctx, t, env, wsID)

	switch serial {
	case newSerial:
		retry := env.StartAsyncCall(ctx, e2e.AsyncRequest{
			Method:      http.MethodPut,
			Target:      created.Attributes.UploadURL,
			Auth:        "",
			ContentType: "application/octet-stream",
			Body:        []byte(`{}`),
		})

		retryStatus, retryBody, retryErr := retry.Wait()
		if retryErr == nil && retryStatus < http.StatusBadRequest {
			t.Fatalf("upload capability takes other content after the finalize: status %d (%s)",
				retryStatus, retryBody)
		}

		env.NoteStep(t, "upload capability refused after kill",
			fmt.Sprintf("other content answers status %d with err %v", retryStatus, retryErr))
	case oldSerial:
		retry := env.StartAsyncCall(ctx, e2e.AsyncRequest{
			Method:      http.MethodPut,
			Target:      created.Attributes.UploadURL,
			Auth:        "",
			ContentType: "application/octet-stream",
			Body:        content,
		})

		retryStatus, retryBody, retryErr := retry.Wait()
		if retryErr != nil || retryStatus != http.StatusOK {
			t.Fatalf("retry of the lost upload answers %d, want %d (err %v, body %s)",
				retryStatus, http.StatusOK, retryErr, retryBody)
		}

		if got := crashCurrentSerial(ctx, t, env, wsID); got != newSerial {
			t.Fatalf("current serial is %d after the retry, want %d", got, newSerial)
		}

		env.NoteStep(t, "upload retried after kill", "the same content completes the lost upload")
	default:
		t.Fatalf("current serial is %d, want %d or %d", serial, newSerial, oldSerial)
	}
}

// crashCurrentSerial returns the serial of the workspace's current state
// version, failing the test when the workspace has none.
func crashCurrentSerial(ctx context.Context, t *testing.T, env *e2e.Env, wsID string) int64 {
	t.Helper()

	status, body, err := env.TFEStatusA(ctx, http.MethodGet, "/api/v2/workspaces/"+wsID+"/current-state-version", "")
	if err != nil {
		t.Fatalf("GET current state after kill: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("GET current state after kill answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var doc currentStateDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode current state after kill %q: %v", body, err)
	}

	return doc.Data.Attributes.Serial
}

// crashHeldLock kills the server with SIGKILL while principal A holds the
// workspace lock, then proves the lock survived with its holder, another
// principal still cannot take or release it, and the CLI plans again after
// the release.
func crashHeldLock(t *testing.T, env *e2e.Env) {
	t.Helper()

	ctx := t.Context()
	wsID := env.WorkspaceID(ctx, t)
	lockPath := "/api/v2/workspaces/" + wsID + "/actions/lock"
	unlockPath := "/api/v2/workspaces/" + wsID + "/actions/unlock"

	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:     nil,
		Name:      "lock for crash kill",
		Method:    http.MethodPost,
		Path:      lockPath,
		Body:      `{"data":{}}`,
		Want:      http.StatusOK,
		UserAgent: "",
		Scheme:    "",
	})

	// The lock is held, so the kill lands during it with no sleep and no
	// hook: the sequence itself is the repeatable point.
	env.Recorder.SetStep("crash during lock")
	env.KillServer(t)
	env.NoteStep(t, "kill during lock", "the server dies while principal A holds the workspace lock")

	crashRestart(ctx, t, env, "a lock")

	status, body, err := env.TFEStatusA(ctx, http.MethodGet, "/api/v2/workspaces/"+wsID, "")
	if err != nil {
		t.Fatalf("GET workspace after kill: %v", err)
	}

	if status != http.StatusOK {
		t.Fatalf("GET workspace after kill answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	attrs, err := decodeWorkspaceLock(body)
	if err != nil {
		t.Fatalf("decode workspace after kill: %v", err)
	}

	if !attrs.Locked || attrs.LockHolder != e2e.Subject {
		t.Fatalf("workspace after kill holds locked %v by %q, want locked by %q",
			attrs.Locked, attrs.LockHolder, e2e.Subject)
	}

	env.NoteStep(t, "lock survived the kill", "the workspace stays locked by "+attrs.LockHolder)

	env.APIStepB(ctx, t, e2e.APIStepSpec{
		Check:     nil,
		Name:      "lock by B conflicts after kill",
		Method:    http.MethodPost,
		Path:      lockPath,
		Body:      `{"data":{}}`,
		Want:      http.StatusConflict,
		UserAgent: "",
		Scheme:    "",
	})
	env.APIStepB(ctx, t, e2e.APIStepSpec{
		Check:     nil,
		Name:      "unlock by B conflicts after kill",
		Method:    http.MethodPost,
		Path:      unlockPath,
		Body:      "",
		Want:      http.StatusConflict,
		UserAgent: "",
		Scheme:    "",
	})
	env.APIStepA(ctx, t, e2e.APIStepSpec{
		Check:     nil,
		Name:      "unlock by A after kill",
		Method:    http.MethodPost,
		Path:      unlockPath,
		Body:      "",
		Want:      http.StatusOK,
		UserAgent: "",
		Scheme:    "",
	})

	env.Step(ctx, t, step("plan after lock kill", nil, "plan", "-input=false", "-no-color"))
}
