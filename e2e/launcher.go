//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	iofs "io/fs"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/scopeaudit"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// launcherRelease is the release that the fake runner and the fake launcher
// send. The server binary of the suite is a development build, which accepts
// the releases 0.0.x.
const launcherRelease = "0.0.1"

// nonceBytes is the length of the nonce of a proof, in bytes.
const nonceBytes = 16

// FakeRunner is a runner that joined a pool and claims phases over the runner
// protocol, with its own key pair. It stands in for the runner of a pool.
type FakeRunner struct {
	env  *Env
	pool string
	id   string
	priv ed25519.PrivateKey
}

// JoinFakeRunner makes a join token of the pool in the namespace ns and joins a
// runner named name with it. It fails the test when the join does not answer
// 200.
func (e *Env) JoinFakeRunner(ctx context.Context, tb testing.TB, ns, poolID, name string) *FakeRunner {
	tb.Helper()

	var created struct {
		Token string `json:"token"`
	}

	e.apiCreated(ctx, tb, http.MethodPost, "/api/runners/"+ns+"/pools/"+poolID+"/join-tokens", &created)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		tb.Fatalf("generate the key of the runner: %v", err)
	}

	req := protocol.JoinRequest{
		Token: created.Token, Pool: poolID, Name: name, Kind: string(runner.KindMachine),
		Isolation: string(runner.IsolationGVisor), Labels: nil, PublicKey: pub,
		Proof: ed25519.Sign(priv, runner.JoinChallenge(e.URL, poolID, name, created.Token)),
	}

	raw, err := json.Marshal(req)
	if err != nil {
		tb.Fatalf("encode the join request: %v", err)
	}

	status, body := e.launcherSend(ctx, tb, launcherRequest{
		Method: http.MethodPost, Path: protocol.PathJoin, Body: raw, Proof: "", Extra: nil,
	})
	if status != http.StatusOK {
		tb.Fatalf("join = %d %s, want 200", status, body)
	}

	var resp protocol.JoinResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		tb.Fatalf("decode the join answer: %v", err)
	}

	return &FakeRunner{env: e, pool: poolID, id: resp.Runner, priv: priv}
}

// apiCreated sends an authenticated request as the administrator, wants the
// status 201 and decodes the answer into out.
func (e *Env) apiCreated(ctx context.Context, tb testing.TB, method, path string, out any) {
	tb.Helper()

	req, err := http.NewRequestWithContext(ctx, method, e.URL+path, http.NoBody)
	if err != nil {
		tb.Fatalf("build %s %s: %v", method, path, err)
	}

	req.Header.Set("Authorization", "token "+e.token)

	resp, err := e.client.Do(req)
	if err != nil {
		tb.Fatalf("%s %s: %v", method, path, err)
	}

	body, readErr := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil || readErr != nil {
		tb.Fatalf("read %s %s: %v %v", method, path, readErr, closeErr)
	}

	if resp.StatusCode != http.StatusCreated {
		tb.Fatalf("%s %s answers %d %s, want 201", method, path, resp.StatusCode, body)
	}

	if err := json.Unmarshal(body, out); err != nil {
		tb.Fatalf("decode %s %s: %v", method, path, err)
	}
}

// launcherRequest is one request of the runner protocol. A non-empty Proof goes
// in the proof header, and a non-nil Extra map adds headers.
type launcherRequest struct {
	Method string
	Path   string
	Body   []byte
	Proof  string
	Extra  map[string]string
}

// launcherSend sends one request of the runner protocol and returns the status
// and the body.
func (e *Env) launcherSend(ctx context.Context, tb testing.TB, lr launcherRequest) (int, []byte) {
	tb.Helper()

	method, path, proof, extra := lr.Method, lr.Path, lr.Proof, lr.Extra

	req, err := http.NewRequestWithContext(ctx, method, e.URL+path, bytes.NewReader(lr.Body))
	if err != nil {
		tb.Fatalf("build %s %s: %v", method, path, err)
	}

	req.Header.Set(protocol.HeaderRelease, launcherRelease)

	if proof != "" {
		req.Header.Set(protocol.HeaderProof, proof)
	}

	for k, v := range extra {
		req.Header.Set(k, v)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		tb.Fatalf("%s %s: %v", method, path, err)
	}

	body, readErr := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil || readErr != nil {
		tb.Fatalf("read %s %s: %v %v", method, path, readErr, closeErr)
	}

	return resp.StatusCode, body
}

// signedCall names what a proof signs besides the body: the subject that signs
// and the method and the path of the request.
type signedCall struct {
	Subject string
	Method  string
	Path    string
}

// signProof returns the proof header of a request: the subject signs the
// method, the path and the hash of the signed body with key.
func signProof(tb testing.TB, key ed25519.PrivateKey, call signedCall, signedBody []byte) string {
	tb.Helper()

	subject, method, path := call.Subject, call.Method, call.Path

	nonce := make([]byte, nonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		tb.Fatalf("nonce: %v", err)
	}

	sum := sha256.Sum256(signedBody)
	proof := runner.Proof{Subject: subject, At: time.Now(), Nonce: hex.EncodeToString(nonce), Signature: nil}
	proof.Signature = ed25519.Sign(key, runner.ProofInput(proof, runner.Signed{
		Method: method, PathQuery: path, BodySHA256: sum[:],
	}))

	return fmt.Sprintf("v1 sub=%s t=%d n=%s sig=%s", subject, proof.At.UnixMilli(), proof.Nonce,
		base64.RawURLEncoding.EncodeToString(proof.Signature))
}

// Poll long-polls for one phase and returns it, or nil when the poll answers 204.
func (r *FakeRunner) Poll(ctx context.Context, tb testing.TB, wait time.Duration) *protocol.Work {
	tb.Helper()

	status, body := r.call(ctx, tb, protocol.PathPoll, protocol.PollRequest{
		Wait: protocol.FromStd(wait), AssignmentVersion: runner.AssignmentVersion,
	})

	switch status {
	case http.StatusNoContent:
		return nil
	case http.StatusOK:
		var work protocol.Work
		if err := json.Unmarshal(body, &work); err != nil {
			tb.Fatalf("decode the work: %v", err)
		}

		return &work
	default:
		tb.Fatalf("poll = %d %s, want 200 or 204", status, body)

		return nil
	}
}

// Handoff asks for the handoff token of the claim of operation.
func (r *FakeRunner) Handoff(ctx context.Context, tb testing.TB, operation string) string {
	tb.Helper()

	path := strings.NewReplacer("{operation}", operation).Replace(protocol.PathClaimHandoff)

	status, body := r.call(ctx, tb, path, nil)
	if status != http.StatusOK {
		tb.Fatalf("handoff = %d %s, want 200", status, body)
	}

	var resp protocol.HandoffResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		tb.Fatalf("decode the handoff: %v", err)
	}

	return resp.HandoffToken
}

// call sends a signed POST of the runner with a JSON body (nil sends none).
func (r *FakeRunner) call(ctx context.Context, tb testing.TB, path string, body any) (int, []byte) {
	tb.Helper()

	return r.env.signedPost(ctx, tb, r.priv, signedCall{Subject: r.id, Method: "", Path: path}, body)
}

// signedPost sends a POST with a JSON body (nil sends none) to the path of
// who, signed by the subject of who with key.
func (e *Env) signedPost(
	ctx context.Context, tb testing.TB, key ed25519.PrivateKey, who signedCall, body any,
) (int, []byte) {
	tb.Helper()

	var raw []byte

	if body != nil {
		var err error

		if raw, err = json.Marshal(body); err != nil {
			tb.Fatalf("encode the body of %s: %v", who.Path, err)
		}
	}

	who.Method = http.MethodPost

	return e.launcherSend(ctx, tb, launcherRequest{
		Method: who.Method, Path: who.Path, Body: raw, Extra: nil,
		Proof: signProof(tb, key, who, raw),
	})
}

// FakeLauncher is the launcher of a claim: it holds the key of its job
// session and signs each call with it.
type FakeLauncher struct {
	env  *Env
	ID   string
	priv ed25519.PrivateKey
	// Assignment is what the server answered to the exchange.
	Assignment protocol.Assignment
}

// OpenSession exchanges the handoff token for a job session as the launcher
// does. It fails the test when the exchange does not answer 200.
func (e *Env) OpenSession(ctx context.Context, tb testing.TB, handoff string) *FakeLauncher {
	tb.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		tb.Fatalf("generate the session key: %v", err)
	}

	raw, err := json.Marshal(protocol.SessionRequest{
		HandoffToken: handoff, PublicKey: pub, Release: launcherRelease,
		Proof: ed25519.Sign(
			priv, runner.SessionChallenge(e.URL, handoff, runner.Key{Public: pub, CreatedAt: time.Time{}}),
		),
	})
	if err != nil {
		tb.Fatalf("encode the exchange: %v", err)
	}

	token := sha256.Sum256([]byte(handoff))
	subject := hex.EncodeToString(token[:])

	status, body := e.launcherSend(ctx, tb, launcherRequest{
		Method: http.MethodPost, Path: protocol.PathSession, Body: raw, Extra: nil,
		Proof: signProof(tb, priv, signedCall{
			Subject: subject, Method: http.MethodPost, Path: protocol.PathSession,
		}, raw),
	})
	if status != http.StatusOK {
		tb.Fatalf("session exchange = %d %s, want 200", status, body)
	}

	var resp protocol.SessionResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		tb.Fatalf("decode the session: %v", err)
	}

	return &FakeLauncher{env: e, ID: resp.Session, priv: priv, Assignment: resp.Assignment}
}

// Post sends a signed POST of the session with a JSON body (nil sends none)
// and returns the status and the body.
func (l *FakeLauncher) Post(ctx context.Context, tb testing.TB, path string, body any) (int, []byte) {
	tb.Helper()

	return l.env.signedPost(ctx, tb, l.priv, signedCall{Subject: l.ID, Method: "", Path: path}, body)
}

// PutLog uploads one chunk of a log. The header and the proof carry the hash
// of the chunk.
func (l *FakeLauncher) PutLog(ctx context.Context, tb testing.TB, path string, chunk []byte) (int, []byte) {
	tb.Helper()

	sum := sha256.Sum256(chunk)

	return l.env.launcherSend(ctx, tb, launcherRequest{
		Method: http.MethodPut, Path: path, Body: chunk,
		Proof: signProof(tb, l.priv, signedCall{Subject: l.ID, Method: http.MethodPut, Path: path}, chunk),
		Extra: map[string]string{protocol.HeaderContentSHA256: hex.EncodeToString(sum[:])},
	})
}

// ServerLogHolds reports which of the secrets the output of the stopped or
// running server binary holds. It needs E2E_SERVER=binary.
func (e *Env) ServerLogHolds(tb testing.TB, secrets ...string) []string {
	tb.Helper()

	if e.backendLogs == nil {
		tb.Fatalf("ServerLogHolds needs E2E_SERVER=binary")
	}

	logs := e.backendLogs.String()

	var found []string

	for _, s := range secrets {
		if s != "" && strings.Contains(logs, s) {
			found = append(found, s)
		}
	}

	return found
}

// noRefusals drops every refusal of the blob views: the read finds the refusal
// as the error of the call.
type noRefusals struct{}

func (noRefusals) RecordOutOfScope(context.Context, namespace.ID, string, scopeaudit.Where) error {
	return nil
}

func (noRefusals) RecordMismatch(context.Context, namespace.ID, string) error {
	return nil
}

// jobParts are the stores of the jobs of one database.
type jobParts struct {
	jobs interface {
		job.Store
		job.RecordStore
	}
	index secret.ItemKeyIndex
	names job.NamespaceReader
}

// JobStepLog stops the binary server, opens its database and its blob directory
// as the server does, and returns the plaintext log of the step in the job as
// the job log reader gives it. The caller starts the server again if it needs
// it. It needs E2E_SERVER=binary and the blob directory.
func (e *Env) JobStepLog(ctx context.Context, tb testing.TB, ns, jobID, step string) string {
	tb.Helper()

	if e.backendCfg.blobStore != "" {
		tb.Skipf("the job log read opens the blob directory: the S3 blob store is not read here")
	}

	e.StopServer(ctx, tb)

	parts, shut := e.openJobParts(ctx, tb)

	defer func() {
		if err := shut(); err != nil {
			tb.Errorf("close the stopped database: %v", err)
		}
	}()

	got, err := parts.jobs.Get(ctx, job.ID(jobID))
	if err != nil {
		tb.Fatalf("read the job %s: %v", jobID, err)
	}

	version := stepLogVersion(&got, step)
	if version == "" {
		tb.Fatalf("the job %s holds no log of the step %s", jobID, step)
	}

	reader := e.newJobLogReader(tb, parts)

	rc, err := reader.OpenLog(ctx, namespace.Name(ns), version)
	if err != nil {
		tb.Fatalf("open the log %s: %v", version, err)
	}

	text, readErr := io.ReadAll(rc)
	if closeErr := rc.Close(); readErr != nil || closeErr != nil {
		tb.Fatalf("read the log %s: %v %v", version, readErr, closeErr)
	}

	return string(text)
}

// stepLogVersion returns the ID of the log version of the step in j, or "".
func stepLogVersion(j *job.Job, step string) string {
	version := ""

	for p := range j.Phases {
		for i := range j.Phases[p].Steps {
			rec := &j.Phases[p].Steps[i]
			if rec.Name == step && rec.Log != nil {
				version = rec.Log.ID
			}
		}
	}

	return version
}

// newJobLogReader builds the job log reader over parts and the blob directory
// of the binary server, with the key-encryption key of the suite.
func (e *Env) newJobLogReader(tb testing.TB, parts jobParts) *job.ArtifactReads {
	tb.Helper()

	blobStore, err := fs.Open(e.backendCfg.blobDir)
	if err != nil {
		tb.Fatalf("open the blob directory: %v", err)
	}

	artifacts, err := job.NewArtifactBlobs(blobStore, noRefusals{})
	if err != nil {
		tb.Fatalf("artifact blobs: %v", err)
	}

	itemBlobs, err := secret.NewItemKeyBlobs(blobStore, noRefusals{}, noRefusals{})
	if err != nil {
		tb.Fatalf("item key blobs: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte(testKeyEncryptionKey))
	if err != nil {
		tb.Fatalf("key-encryption key: %v", err)
	}

	sealer, err := secret.NewItemKeySealer(secret.ItemSealerConfig{
		Index: parts.index, Blobs: itemBlobs, Wrapper: wrapper, Namespaces: parts.names, Now: time.Now,
	})
	if err != nil {
		tb.Fatalf("item sealer: %v", err)
	}

	reader, err := job.NewArtifactReader(parts.jobs, artifacts, sealer, parts.names)
	if err != nil {
		tb.Fatalf("job log reader: %v", err)
	}

	return reader
}

// openJobParts opens the database of the stopped binary server.
func (e *Env) openJobParts(ctx context.Context, tb testing.TB) (jobParts, func() error) {
	tb.Helper()

	switch e.store {
	case StoreSQLite:
		st, err := sqlite.Open(ctx, strings.TrimPrefix(e.backendCfg.dbURL, "sqlite:"))
		if err != nil {
			tb.Fatalf("open the stopped database: %v", err)
		}

		return jobParts{jobs: st.Jobs(), index: st, names: st}, st.Close
	case StorePostgres:
		st, err := postgres.Open(ctx, e.backendCfg.dbURL)
		if err != nil {
			tb.Fatalf("open the stopped database: %v", err)
		}

		return jobParts{jobs: st.Jobs(), index: st, names: st}, func() error { st.CloseContext(ctx); return nil }
	default:
		tb.Fatalf("unknown store: %s", e.store)

		return jobParts{jobs: nil, index: nil, names: nil}, nil
	}
}

// BlobDirHolds reports whether a file of the blob directory of the binary
// server holds needle. It needs E2E_SERVER=binary and the blob directory, and
// the server may run: it only reads files, through a root so that a link in
// the directory leads nowhere outside it.
func (e *Env) BlobDirHolds(tb testing.TB, needle string) bool {
	tb.Helper()

	if e.backendCfg.blobDir == "" {
		tb.Fatal("BlobDirHolds needs E2E_SERVER=binary and the blob directory")
	}

	root, err := os.OpenRoot(e.backendCfg.blobDir)
	if err != nil {
		tb.Fatalf("open the blob directory: %v", err)
	}

	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			tb.Errorf("close the blob directory: %v", closeErr)
		}
	}()

	found := false
	want := []byte(needle)

	walkErr := iofs.WalkDir(root.FS(), ".", func(path string, d iofs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		raw, readErr := iofs.ReadFile(root.FS(), path)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", path, readErr)
		}

		if bytes.Contains(raw, want) {
			found = true
		}

		return nil
	})
	if walkErr != nil {
		tb.Fatalf("walk the blob directory: %v", walkErr)
	}

	return found
}
