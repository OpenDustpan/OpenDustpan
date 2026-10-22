package conformance_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/clientgen/conformance"
)

// fakeModeEnv names the variable that turns this test binary into a fake
// adapter. The tests start the binary again with it set, and TestMain runs the
// fake adapter instead of the tests.
const fakeModeEnv = "CONFORMANCE_FAKE_ADAPTER"

// canaryEnv is a variable that stands for a secret of CI.
const canaryEnv = "CONFORMANCE_CANARY_SECRET"

// runFakeAdapter reads one request on each line, and answers as the mode says.
func runFakeAdapter(mode string) {
	switch mode {
	case "noread", "sleeper":
		// An adapter that never reads its input, and the child of an adapter.
		<-time.After(time.Minute)

		return
	default:
	}

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 1<<16), 1<<22)

	out := bufio.NewWriter(os.Stdout)

	for in.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}

		if json.Unmarshal(in.Bytes(), &req) != nil {
			return
		}

		reply, stop := fakeReply(mode, req.ID, req.Method)
		if reply != "" {
			if _, err := out.WriteString(reply + "\n"); err != nil {
				return
			}

			if err := out.Flush(); err != nil {
				return
			}
		}

		if stop || req.Method == conformance.MethodShutdown {
			return
		}
	}
}

// fixedReplies are the modes that answer every request with the same line.
func fixedReplies() map[string]string {
	return map[string]string{
		"badjson":      "this is not json",
		"wrongid":      `{"jsonrpc":"2.0","id":999,"result":{}}`,
		"wrongversion": `{"jsonrpc":"1.0","id":1,"result":{}}`,
	}
}

// fakeReply returns the line to send, or an empty line to send none. stop says
// that the adapter ends.
func fakeReply(mode string, id json.RawMessage, method string) (string, bool) {
	if line, ok := fixedReplies()[mode]; ok {
		return line, false
	}

	ok := func(result string) string { return fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`, id, result) }

	switch mode {
	case "toolong":
		return ok(`"` + strings.Repeat("x", conformance.MaxMessageBytes) + `"`), false
	case "exitearly":
		return "", true
	case "hang":
		<-time.After(time.Minute)

		return "", true
	case "grandchild":
		startGrandchild()
		<-time.After(time.Minute)

		return "", true
	case "rpcerror":
		// Only paginate is refused: the framing is in order, and the session goes on.
		if method == conformance.MethodPaginate {
			return fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"no"}}`, id), false
		}

		return goodReply(mode, method, ok), false
	default:
		return goodReply(mode, method, ok), false
	}
}

// goodReply answers a request of the modes that follow the protocol.
func goodReply(mode, method string, ok func(string) string) string {
	switch {
	case method == conformance.MethodInitialize && mode == "badprotocol":
		return ok(`{"protocolVersion":99}`)
	case method == conformance.MethodInitialize && mode == "env":
		return ok(fmt.Sprintf(`{"protocolVersion":1,"client":%q}`, os.Getenv(canaryEnv)))
	case method == conformance.MethodInitialize:
		return ok(`{"protocolVersion":1,"client":"fake","clientVersion":"0.0.1"}`)
	case method == conformance.MethodCall:
		return ok(`{"status":200,"body":{"ok":true}}`)
	case method == conformance.MethodPaginate:
		return ok(`{"items":[{"n":1}],"pages":1}`)
	default:
		return ok(`null`)
	}
}

// startFake starts this test binary as a fake adapter in a mode.
func startFake(t *testing.T, mode string, timeout time.Duration) *conformance.ProcessAdapter {
	t.Helper()

	a, err := startFakeWith(t, mode, timeout)
	if err != nil {
		t.Fatalf("start the fake adapter: %v", err)
	}

	return a
}

// startFakeWith starts the fake adapter with more variables in its environment.
func startFakeWith(
	t *testing.T,
	mode string,
	timeout time.Duration,
	extra ...string,
) (*conformance.ProcessAdapter, error) {
	t.Helper()

	a, err := conformance.StartAdapter(
		t.Context(),
		fakeOptions(conformance.Environ(nil, append([]string{fakeModeEnv + "=" + mode}, extra...)...), timeout),
	)
	if err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	return a, nil
}

// fakeOptions are the options to start this test binary as the fake adapter.
func fakeOptions(env []string, timeout time.Duration) conformance.ProcessOptions {
	return conformance.ProcessOptions{
		Path: os.Args[0], Args: nil, Env: env, CallTimeout: timeout,
	}
}

func TestProcessAdapterSession(t *testing.T) {
	t.Parallel()

	a := startFake(t, "good", 0)

	info, err := a.Initialize(t.Context(), initParams())
	if err != nil || info.Client != "fake" || info.ClientVersion != "0.0.1" {
		t.Fatalf("initialize: %+v %v", info, err)
	}

	res, err := a.Call(t.Context(), callParams())
	if err != nil || res.Status != 200 || string(res.Body) != `{"ok":true}` {
		t.Fatalf("call: %+v %v", res, err)
	}

	page, err := a.Paginate(t.Context(), paginateParams("x", 2))
	if err != nil || page.Pages != 1 || len(page.Items) != 1 {
		t.Fatalf("paginate: %+v %v", page, err)
	}

	if err = a.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// TestProcessAdapterBreaks checks that each break of the protocol gives its
// own error, and that Close still ends the process.
func TestProcessAdapterBreaks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mode    string
		timeout time.Duration
		call    func(context.Context, *conformance.ProcessAdapter) error
		want    error
	}{
		{"badjson", 0, callOnce, conformance.ErrProtocol},
		{"wrongid", 0, callOnce, conformance.ErrProtocol},
		{"wrongversion", 0, callOnce, conformance.ErrProtocol},
		{"toolong", 0, callOnce, conformance.ErrProtocol},
		{"badprotocol", 0, initOnce, conformance.ErrProtocolVersion},
		{"exitearly", 0, callOnce, conformance.ErrAdapterExited},
		{"hang", 300 * time.Millisecond, callOnce, context.DeadlineExceeded},
	}

	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			t.Parallel()

			a := startFake(t, tt.mode, tt.timeout)

			err := tt.call(t.Context(), a)
			if !errors.Is(err, tt.want) {
				t.Fatalf("want %v, got %v", tt.want, err)
			}

			// The session is lost. Close must not hang, and goleak checks that no
			// goroutine stays. What Close reports depends on the break.
			_ = a.Close() //nolint:errcheck // The test checks the error of the call.
		})
	}
}

// TestProcessAdapterRefusesALongRequest checks the limit on the side of the
// harness: a request longer than one message is not sent.
func TestProcessAdapterRefusesALongRequest(t *testing.T) {
	t.Parallel()

	a := startFake(t, "good", 0)

	t.Cleanup(func() { _ = a.Close() }) //nolint:errcheck // The test checks the error of the call.

	p := callParams()
	p.Body = json.RawMessage(`"` + strings.Repeat("x", conformance.MaxMessageBytes) + `"`)

	if _, err := a.Call(t.Context(), p); !errors.Is(err, conformance.ErrProtocol) {
		t.Fatalf("want ErrProtocol, got %v", err)
	}

	// The session is still usable: the long request was never sent.
	if _, err := a.Call(t.Context(), callParams()); err != nil {
		t.Fatalf("the next call: %v", err)
	}
}

func callOnce(ctx context.Context, a *conformance.ProcessAdapter) error {
	_, err := a.Call(ctx, callParams())

	return err
}

func initOnce(ctx context.Context, a *conformance.ProcessAdapter) error {
	_, err := a.Initialize(ctx, initParams())

	return err
}

// TestProcessAdapterRPCErrorFailsOneRequest checks that a JSON-RPC error is a
// fault of one request: it is not a break of the protocol, and the next request
// gets its answer.
func TestProcessAdapterRPCErrorFailsOneRequest(t *testing.T) {
	t.Parallel()

	a := startFake(t, "rpcerror", 0)

	t.Cleanup(func() { _ = a.Close() }) //nolint:errcheck // The test checks the error of the call.

	_, err := a.Paginate(t.Context(), paginateParams("x", 2))

	var rpc *conformance.RPCError
	if !errors.As(err, &rpc) || rpc.Code != -32601 {
		t.Fatalf("want an RPCError with code -32601, got %v", err)
	}

	if !errors.Is(err, conformance.ErrRequestRefused) || errors.Is(err, conformance.ErrProtocol) {
		t.Errorf("an RPC error is ErrRequestRefused and not ErrProtocol: %v", err)
	}

	if res, callErr := a.Call(t.Context(), callParams()); callErr != nil || res.Status != 200 {
		t.Fatalf("the next call: %+v %v", res, callErr)
	}
}

// TestProcessAdapterTimeoutCoversTheWrite checks the limit of a request that
// the adapter does not read. The request is longer than a pipe holds, so the
// write blocks, and the limit of the call must end it.
func TestProcessAdapterTimeoutCoversTheWrite(t *testing.T) {
	t.Parallel()

	a := startFake(t, "noread", 500*time.Millisecond)

	t.Cleanup(func() { _ = a.Close() }) //nolint:errcheck // The test checks the error of the call.

	p := callParams()
	p.Body = json.RawMessage(`"` + strings.Repeat("x", conformance.MaxMessageBytes-4096) + `"`)

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	start := time.Now()
	_, err := a.Call(ctx, p)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want DeadlineExceeded, got %v", err)
	}

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("the call took %s: the limit of 500ms did not cover the write", elapsed)
	}
}

// TestProcessAdapterGetsNoSecret is the canary: a secret in the environment of
// the harness does not reach the adapter, unless the caller lists it by name.
func TestProcessAdapterGetsNoSecret(t *testing.T) {
	t.Setenv(canaryEnv, "canary-value")

	a := startFake(t, "env", 0)

	info, err := a.Initialize(t.Context(), initParams())
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}

	if info.Client != "" {
		t.Errorf("the adapter saw the secret: the value has %d bytes", len(info.Client))
	}

	if err = a.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// The positive control: the helper passes a variable that it is told to pass.
	listed := conformance.Environ([]string{canaryEnv}, fakeModeEnv+"=env")

	b, err := conformance.StartAdapter(t.Context(), fakeOptions(listed, 0))
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	info, err = b.Initialize(t.Context(), initParams())
	if err != nil || info.Client != "canary-value" {
		t.Errorf(
			"the listed variable should reach the adapter: the value has %d bytes, error %v", len(info.Client), err,
		)
	}

	if err = b.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestEnvironNeverReturnsNil(t *testing.T) {
	t.Parallel()

	if env := conformance.Environ(nil); env == nil {
		t.Fatal("a nil environment would give the child the environment of the harness")
	}
}
