//go:build e2e

package e2e

import (
	"bytes"
	"cmp"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/scopeaudit"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/launcher/session"
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

// envTransport sends the requests of the runner protocol through the HTTP
// client of the harness, which trusts the certificate of the server.
type envTransport struct {
	env *Env
}

// Do implements session.Transport.
func (t envTransport) Do(ctx context.Context, req *session.Request) (*session.Response, error) {
	hr, err := http.NewRequestWithContext(ctx, req.Method, t.env.URL+req.Path, bytes.NewReader(req.Body))
	if err != nil {
		return nil, fmt.Errorf("build %s %s: %w", req.Method, req.Path, err)
	}

	for k, v := range req.Header {
		hr.Header.Set(k, v)
	}

	resp, err := t.env.client.Do(hr)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", req.Method, req.Path, err)
	}

	body, readErr := io.ReadAll(resp.Body)
	if err = errors.Join(readErr, resp.Body.Close()); err != nil {
		return nil, fmt.Errorf("read %s %s: %w", req.Method, req.Path, err)
	}

	return &session.Response{Status: resp.StatusCode, Body: body, RetryAfter: 0}, nil
}

// protocolClient returns the client of the runner protocol of the harness:
// the runner and the launcher sign their calls through it.
func (e *Env) protocolClient() *session.Client {
	return &session.Client{Server: e.URL, Transport: envTransport{env: e}, Release: launcherRelease, Now: nil}
}

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

	resp, err := e.protocolClient().Send(ctx, &session.Request{
		Method: http.MethodPost, Path: protocol.PathJoin, Body: raw, Header: nil,
	})
	if err != nil || resp.Status != http.StatusOK {
		tb.Fatalf("join = %+v, %v; want 200", resp, err)
	}

	var joined protocol.JoinResponse
	if err := json.Unmarshal(resp.Body, &joined); err != nil {
		tb.Fatalf("decode the join answer: %v", err)
	}

	return &FakeRunner{env: e, pool: poolID, id: joined.Runner, priv: priv}
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

// call sends a POST of the runner with a JSON body (nil sends none), signed
// with the key of the runner, and returns the status and the body.
func (r *FakeRunner) call(ctx context.Context, tb testing.TB, path string, body any) (int, []byte) {
	tb.Helper()

	var raw []byte

	if body != nil {
		var err error

		if raw, err = json.Marshal(body); err != nil {
			tb.Fatalf("encode the body of %s: %v", path, err)
		}
	}

	resp, err := r.env.protocolClient().Signed(ctx, r.priv, r.id, &session.Request{
		Method: http.MethodPost, Path: path, Body: raw, Header: nil,
	}, nil)
	if err != nil {
		tb.Fatalf("POST %s: %v", path, err)
	}

	return resp.Status, resp.Body
}

// FakeLauncher is the launcher of a claim: it holds the job session that the
// session client of the launcher opened, and signs each call with its key.
type FakeLauncher struct {
	sess   *session.Session
	tamper *tamperTransport
	ID     string
	// Assignment is what the server answered to the exchange.
	Assignment protocol.Assignment
}

// tamperTransport is the transport of a fake launcher. It sends each request
// as the client signed it, except the next one after PutTampered, whose body it
// replaces after the signature, as an attacker on the path could.
type tamperTransport struct {
	inner envTransport

	mu   sync.Mutex
	body []byte
}

// Do implements session.Transport.
func (t *tamperTransport) Do(ctx context.Context, req *session.Request) (*session.Response, error) {
	t.mu.Lock()
	body := t.body
	t.body = nil
	t.mu.Unlock()

	if body != nil {
		changed := *req
		changed.Body = body
		req = &changed
	}

	return t.inner.Do(ctx, req)
}

// OpenSession exchanges the handoff token for a job session with the session
// client of the launcher. It fails the test when the exchange fails.
func (e *Env) OpenSession(ctx context.Context, tb testing.TB, handoff string) *FakeLauncher {
	tb.Helper()

	tamper := &tamperTransport{inner: envTransport{env: e}, mu: sync.Mutex{}, body: nil}
	client := &session.Client{Server: e.URL, Transport: tamper, Release: launcherRelease, Now: nil}

	sess, err := session.Open(ctx, client, handoff)
	if err != nil {
		tb.Fatalf("session exchange: %v", err)
	}

	return &FakeLauncher{sess: sess, tamper: tamper, ID: sess.ID, Assignment: sess.Assignment}
}

// PutTampered uploads like PutLog, but the body on the wire is sent, not
// signed: the header and the proof carry the hash of signed.
func (l *FakeLauncher) PutTampered(ctx context.Context, tb testing.TB, path string, signed, sent []byte) (int, []byte) {
	tb.Helper()

	l.tamper.mu.Lock()
	l.tamper.body = sent
	l.tamper.mu.Unlock()

	return l.PutLog(ctx, tb, path, signed)
}

// Get sends a signed GET of the session and returns the status and the body.
func (l *FakeLauncher) Get(ctx context.Context, tb testing.TB, path string) (int, []byte) {
	tb.Helper()

	resp, err := l.sess.Get(ctx, path)
	if err != nil {
		tb.Fatalf("GET %s: %v", path, err)
	}

	return resp.Status, resp.Body
}

// Post sends a signed POST of the session with a JSON body (nil sends none)
// and returns the status and the body.
func (l *FakeLauncher) Post(ctx context.Context, tb testing.TB, path string, body any) (int, []byte) {
	tb.Helper()

	resp, err := l.sess.Post(ctx, path, body)
	if err != nil {
		tb.Fatalf("POST %s: %v", path, err)
	}

	return resp.Status, resp.Body
}

// PutLog uploads one chunk of a log. The header and the proof carry the hash
// of the chunk.
func (l *FakeLauncher) PutLog(ctx context.Context, tb testing.TB, path string, chunk []byte) (int, []byte) {
	tb.Helper()

	resp, err := l.sess.PutLogChunk(ctx, path, chunk)
	if err != nil {
		tb.Fatalf("PUT %s: %v", path, err)
	}

	return resp.Status, resp.Body
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

	parts, shut, openErr := e.openJobParts(ctx)
	if openErr != nil {
		tb.Fatalf("%v", openErr)
	}

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

	reader, err := e.newJobLogReader(parts)
	if err != nil {
		tb.Fatalf("%v", err)
	}

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

// MaxStepLogReport is the most bytes of one step log that LogJobStepsOnFailure
// prints.
const MaxStepLogReport = 4096

// LogJobStepsOnFailure registers a cleanup of tb: when tb has failed by then,
// it prints the stored log of each step of the job, so that the output of a
// CI run shows why a step ended. Each log is cut at MaxStepLogReport bytes and
// every text of secrets is replaced (see StepLogReport). The read stops the
// binary server, so the cleanup runs after all other work of the test. It
// prints a note and does not fail the test when the logs cannot be read, and
// it prints nothing when the server runs in the process of the test or the
// blob store is not a directory.
func (e *Env) LogJobStepsOnFailure(tb testing.TB, ns, jobID string, secrets ...string) {
	tb.Helper()

	tb.Cleanup(func() {
		if !tb.Failed() || e.InProcess() || e.backendCfg.blobStore != "" {
			return
		}

		// The context of the test is canceled before its cleanups run. The
		// report has a deadline, so that it never hangs the test.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(tb.Context()), stepLogReportLimit)
		defer cancel()

		tb.Log(e.jobStepLogReport(ctx, tb, ns, jobID, secrets))
	})
}

// errUnknownStore is the error of a store that holds no job log reader.
var errUnknownStore = errors.New("unknown store")

// stepLogReportLimit is the longest time that the cleanup of
// LogJobStepsOnFailure may run.
const stepLogReportLimit = 30 * time.Second

// stepLogWaitDelay is the longest time that the cleanup waits for the output
// of the killed server to close.
const stepLogWaitDelay = 5 * time.Second

// jobStepLogReport returns the report of the step logs of the job, or the
// reason why it cannot read them. It never fails tb.
func (e *Env) jobStepLogReport(ctx context.Context, tb testing.TB, ns, jobID string, secrets []string) string {
	tb.Helper()

	if reason := e.killServerForReport(tb); reason != "" {
		return fmt.Sprintf("step logs of job %s: not read, %s", jobID, reason)
	}

	parts, shut, err := e.openJobParts(ctx)
	if err != nil {
		return fmt.Sprintf("step logs of job %s: not read, %s", jobID, ReportText(err.Error(), secrets...))
	}

	defer func() {
		if closeErr := shut(); closeErr != nil {
			tb.Logf("close the stopped database: %s", ReportText(closeErr.Error(), secrets...))
		}
	}()

	got, err := parts.jobs.Get(ctx, job.ID(jobID))
	if err != nil {
		return fmt.Sprintf("step logs of job %s: cannot read the job: %s", jobID, ReportText(err.Error(), secrets...))
	}

	reader, err := e.newJobLogReader(parts)
	if err != nil {
		return fmt.Sprintf("step logs of job %s: not read, %s", jobID, ReportText(err.Error(), secrets...))
	}

	var report strings.Builder

	for p := range got.Phases {
		for i := range got.Phases[p].Steps {
			if rec := &got.Phases[p].Steps[i]; rec.Log != nil {
				ref := StepLogRef{
					Namespace: ns, JobID: jobID, Name: rec.Name, Version: rec.Log.ID, ExitCode: rec.ExitCode,
				}
				report.WriteString(StepLogEntry(ctx, reader, ref, secrets))
			}
		}
	}

	if report.Len() == 0 {
		return fmt.Sprintf("step logs of job %s: the job holds no step log", jobID)
	}

	return report.String()
}

// killServerForReport ends the server process and waits for its end, so that
// the database file is free. The test has failed: the context of the test killed
// the server before the cleanups run, or the kill here does it, and a graceful
// stop would fail the test a second time. It returns the reason why it cannot
// end the server, or "".
func (e *Env) killServerForReport(tb testing.TB) string {
	tb.Helper()

	if e.ha != nil || e.backendCmd == nil || e.backendCmd.Process == nil {
		return "the server is not a single binary process"
	}

	if err := e.backendCmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Sprintf("kill the server: %v", err)
	}

	// The error of a killed process is the expected one. A child of the server
	// that keeps the output open must not hang the wait: ErrWaitDelay is the
	// bounded end of that wait. spawnBinaryBackend sets WaitDelay before Start.
	var exit *exec.ExitError
	if err := e.backendCmd.Wait(); err != nil && !errors.As(err, &exit) && !errors.Is(err, exec.ErrWaitDelay) {
		return fmt.Sprintf("wait for the killed server: %v", err)
	}

	return ""
}

// LogOpener opens the stored log of a step. *job.ArtifactReads is one.
type LogOpener interface {
	OpenLog(ctx context.Context, ns namespace.Name, version string) (io.ReadCloser, error)
}

// StepLogEntry returns the report of the log version of one step, or the reason
// why it cannot read the log. The name, the job ID and each error text go
// through ReportText, so that they hold no secret and no control character.
func StepLogEntry(ctx context.Context, opener LogOpener, ref StepLogRef, secrets []string) string {
	label := ReportText(ref.Name, secrets...)
	jobLabel := ReportText(ref.JobID, secrets...)

	rc, err := opener.OpenLog(ctx, namespace.Name(ref.Namespace), ref.Version)
	if err != nil {
		return fmt.Sprintf("step log %s of job %s: cannot open: %s\n",
			label, jobLabel, ReportText(err.Error(), secrets...))
	}

	// The read goes past the cap by the longest secret and one byte, so that a
	// secret at the cap is whole when StepLogReport replaces it, and so that the
	// report can show that the log is longer than the cap.
	text, readErr := io.ReadAll(io.LimitReader(rc, int64(readLimit(secrets))))

	closeErr := rc.Close()
	if readErr != nil || closeErr != nil {
		return fmt.Sprintf("step log %s of job %s: cannot read: %s\n", label, jobLabel,
			ReportText(errors.Join(readErr, closeErr).Error(), secrets...))
	}

	return fmt.Sprintf("step log %s of job %s (exit code %d):\n%s\n",
		label, jobLabel, ref.ExitCode, StepLogReport(text, secrets...))
}

// StepLogRef names the log of one step for StepLogEntry.
type StepLogRef struct {
	Namespace string
	JobID     string
	Name      string
	// Version is the ID of the log version of the step.
	Version  string
	ExitCode int
}

// readLimit returns the most bytes that StepLogEntry reads from one log.
func readLimit(secrets []string) int {
	return MaxStepLogReport + 1 + longest(secrets)
}

// ReportText returns s for a one-line part of a report (a name or an error
// text): each text of secrets is replaced, each control character is replaced
// by "?", and at most MaxStepLogReport bytes stay.
func ReportText(s string, secrets ...string) string {
	return cleanReport(s, secrets, flatRune)
}

// flatRune keeps a rune of a one-line text: every control character becomes "?".
func flatRune(r rune) rune {
	if r < ' ' || r == 0x7f {
		return '?'
	}

	return r
}

// lineRune keeps a rune of a log: it keeps the newline and the tab.
func lineRune(r rune) rune {
	if r == '\n' || r == '\t' {
		return r
	}

	return flatRune(r)
}

// longest returns the length in bytes of the longest text of texts.
func longest(texts []string) int {
	n := 0

	for _, s := range texts {
		n = max(n, len(s))
	}

	return n
}

// StepLogReport returns text for a report: at most MaxStepLogReport bytes,
// with each non-empty text of secrets replaced by [redacted] and each control
// character other than the newline and the tab replaced by "?". It ends with a
// note when it cut text. A secret that the cut splits is not a whole text any
// more, so the replacement runs on the whole input first.
//
// StepLogEntry reads at most readLimit bytes. When text has that length, the
// log may go on, so the end of text may hold the start of a secret: that start
// is dropped and the note is added.
func StepLogReport(text []byte, secrets ...string) string {
	return cleanReport(string(text), secrets, lineRune)
}

// cleanReport redacts in, drops a partial secret at its end when in may be the
// start of a longer text, cuts the result at MaxStepLogReport bytes and
// maps each rune with mapRune.
func cleanReport(in string, secrets []string, mapRune func(rune) rune) string {
	// The input has the read limit as its length: the text went on past it.
	more := len(in) >= readLimit(secrets)

	out := redactSecrets(in, secrets)

	if more {
		if rest, dropped := dropSecretStart(out, secrets); dropped {
			out = strings.ToValidUTF8(rest, "")
		}
	}

	cut := more || len(out) > MaxStepLogReport
	if len(out) > MaxStepLogReport {
		out = strings.ToValidUTF8(out[:MaxStepLogReport], "")
	}

	out = strings.Map(mapRune, out)

	if cut {
		out += "\n[cut at " + strconv.Itoa(MaxStepLogReport) + " bytes]"
	}

	return out
}

// byteRange is the half-open byte range [start, end) of s.
type byteRange struct{ start, end int }

// redactSecrets replaces each non-empty text of secrets in s by [redacted].
// It finds every occurrence of every secret, including occurrences that
// overlap, merges the ranges that overlap or touch, and writes [redacted]
// once for each merged range. No part of a secret stays in the output.
func redactSecrets(s string, secrets []string) string {
	var ranges []byteRange

	for _, text := range secrets {
		if text == "" {
			continue
		}

		for from := 0; from <= len(s)-len(text); from++ {
			at := strings.Index(s[from:], text)
			if at < 0 {
				break
			}

			start := from + at
			ranges = append(ranges, byteRange{start: start, end: start + len(text)})
			from = start // The loop adds 1: the next search can overlap this match.
		}
	}

	if len(ranges) == 0 {
		return s
	}

	slices.SortFunc(ranges, func(a, b byteRange) int { return cmp.Compare(a.start, b.start) })

	var out strings.Builder

	pos := 0
	cur := ranges[0]

	flush := func() {
		out.WriteString(s[pos:cur.start])
		out.WriteString("[redacted]")

		pos = cur.end
	}

	for _, r := range ranges[1:] {
		if r.start <= cur.end {
			cur.end = max(cur.end, r.end)

			continue
		}

		flush()

		cur = r
	}

	flush()
	out.WriteString(s[pos:])

	return out.String()
}

// dropSecretStart removes from the end of s the longest text that is a proper
// start of a secret. It reports whether it removed text.
func dropSecretStart(s string, secrets []string) (string, bool) {
	drop := 0

	for _, text := range secrets {
		for k := min(len(text)-1, len(s)); k > drop; k-- {
			if strings.HasSuffix(s, text[:k]) {
				drop = k

				break
			}
		}
	}

	return s[:len(s)-drop], drop > 0
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
func (e *Env) newJobLogReader(parts jobParts) (*job.ArtifactReads, error) {
	blobStore, err := fs.Open(e.backendCfg.blobDir)
	if err != nil {
		return nil, fmt.Errorf("open the blob directory: %w", err)
	}

	artifacts, err := job.NewArtifactBlobs(blobStore, noRefusals{})
	if err != nil {
		return nil, fmt.Errorf("artifact blobs: %w", err)
	}

	itemBlobs, err := secret.NewItemKeyBlobs(blobStore, noRefusals{}, noRefusals{})
	if err != nil {
		return nil, fmt.Errorf("item key blobs: %w", err)
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte(testKeyEncryptionKey))
	if err != nil {
		return nil, fmt.Errorf("key-encryption key: %w", err)
	}

	sealer, err := secret.NewItemKeySealer(secret.ItemSealerConfig{
		Index: parts.index, Blobs: itemBlobs, Wrapper: wrapper, Namespaces: parts.names, Now: time.Now,
	})
	if err != nil {
		return nil, fmt.Errorf("item sealer: %w", err)
	}

	reader, err := job.NewArtifactReader(parts.jobs, artifacts, sealer, parts.names)
	if err != nil {
		return nil, fmt.Errorf("job log reader: %w", err)
	}

	return reader, nil
}

// openJobParts opens the database of the stopped binary server.
func (e *Env) openJobParts(ctx context.Context) (jobParts, func() error, error) {
	switch e.store {
	case StoreSQLite:
		st, err := sqlite.Open(ctx, strings.TrimPrefix(e.backendCfg.dbURL, "sqlite:"))
		if err != nil {
			return jobParts{jobs: nil, index: nil, names: nil}, nil, fmt.Errorf("open the stopped database: %w", err)
		}

		return jobParts{jobs: st.Jobs(), index: st, names: st}, st.Close, nil
	case StorePostgres:
		st, err := postgres.Open(ctx, e.backendCfg.dbURL)
		if err != nil {
			return jobParts{jobs: nil, index: nil, names: nil}, nil, fmt.Errorf("open the stopped database: %w", err)
		}

		return jobParts{jobs: st.Jobs(), index: st, names: st}, func() error { st.CloseContext(ctx); return nil }, nil
	default:
		return jobParts{jobs: nil, index: nil, names: nil}, nil, fmt.Errorf("%w: %s", errUnknownStore, e.store)
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
