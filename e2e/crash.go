//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"syscall"
	"testing"
)

// KillServer ends the binary server with SIGKILL and reaps it, so the test
// proves a crash, not a graceful stop. The database and the blob storage stay,
// and StartServer reuses them on the same port, so the next start proves
// recovery. It needs E2E_SERVER=binary.
//
// The kill follows the dispatch of one request with no sleep: callers send a
// large body through StartAsyncCall, wait for its sent signal, then kill. The
// signal proves only that the client sent the request. The server is likely
// inside the write (blob write, SQL commit), but it may be before or after
// it, so the tests check the result of both windows. No hook in production
// code is needed: the hook is this helper with the sent signal.
func (e *Env) KillServer(tb testing.TB) {
	tb.Helper()

	if e.binPath == "" {
		tb.Fatalf("KillServer needs E2E_SERVER=binary")
	}

	if e.backendCmd == nil || e.backendCmd.Process == nil {
		tb.Fatalf("KillServer: the server is not running")
	}

	cmd := e.backendCmd
	logs := e.backendLogs

	if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
		tb.Fatalf("kill server: %v", err)
	}

	// Reap the process, so the kill leaves none behind. The exit error of a
	// killed process is the expected outcome, not a failure.
	_ = cmd.Wait() //nolint:errcheck // The exit of a killed process is recorded in the log below.

	tb.Logf("killed server binary output:\n%s", logs.String())

	e.backendCmd = nil
}

// asyncResult is the outcome of one background request.
type asyncResult struct {
	status int
	body   string
	err    error
}

// AsyncCall is one HTTP request sent in the background while the test kills
// the server. Callers wait for Sent, kill the server, then call Wait. Every
// started call needs its Wait, so no goroutine stays behind.
type AsyncCall struct {
	sent chan struct{}
	done chan asyncResult
}

// AsyncRequest is one HTTP request sent in the background while the test
// kills the server. Auth carries the whole Authorization header value, or ""
// for none (the upload capability travels in the path).
type AsyncRequest struct {
	Method      string
	Target      string
	Auth        string
	ContentType string
	Body        []byte
}

// Sent closes when the client wrote the request, so the server is inside the
// write while the test kills it.
func (c *AsyncCall) Sent() <-chan struct{} {
	return c.sent
}

// Wait returns the status and body of the call. A kill shows up as an error,
// which the caller logs, never checks: either outcome is a valid crash.
func (c *AsyncCall) Wait() (int, string, error) {
	res := <-c.done

	return res.status, res.body, res.err
}

// StartAsyncCall sends req in the background and returns the call. The call
// goes through the recording proxy, so the run keeps the same request log as
// other steps. The caller waits for Sent, kills the server with KillServer,
// then calls Wait.
func (e *Env) StartAsyncCall(ctx context.Context, req AsyncRequest) *AsyncCall {
	call := &AsyncCall{sent: make(chan struct{}), done: make(chan asyncResult, 1)}

	//nolint:exhaustruct_v5 // The trace signals the written request only; the rest stays zero.
	trace := &httptrace.ClientTrace{
		WroteRequest: func(httptrace.WroteRequestInfo) {
			select {
			case <-call.sent:
			default:
				close(call.sent)
			}
		},
	}

	go func() {
		status, respBody, err := e.doAsyncCall(httptrace.WithClientTrace(ctx, trace), req)

		select {
		case <-call.sent:
		default:
			close(call.sent)
		}

		call.done <- asyncResult{status: status, body: respBody, err: err}
	}()

	return call
}

// doAsyncCall sends one request and returns its status and body.
func (e *Env) doAsyncCall(ctx context.Context, req AsyncRequest) (int, string, error) {
	var reader io.Reader
	if len(req.Body) > 0 {
		reader = bytes.NewReader(req.Body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.Target, reader)
	if err != nil {
		return 0, "", fmt.Errorf("build %s %s: %w", req.Method, req.Target, err)
	}

	if req.Auth != "" {
		httpReq.Header.Set("Authorization", req.Auth)
	}

	if req.ContentType != "" {
		httpReq.Header.Set("Content-Type", req.ContentType)
	}

	resp, err := e.client.Do(httpReq)
	if err != nil {
		return 0, "", fmt.Errorf("%s %s: %w", req.Method, req.Target, err)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // The body was read to completion below where it matters.
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, "", fmt.Errorf("read %s %s: %w", req.Method, req.Target, err)
	}

	return resp.StatusCode, string(respBody), nil
}
