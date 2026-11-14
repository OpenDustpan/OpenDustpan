package conformance_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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
	case "noread":
		// An adapter that never reads its input.
		<-time.After(time.Minute)

		return
	case "sleeper":
		// The child of an adapter. It blocks until a kill ends it.
		runSleeper()

		return
	case "grandchildclean":
		// An adapter that starts a child and then answers as the default mode.
		startGrandchild()
	case "waitstdin":
		// An adapter that answers shutdown, closes its output, and then waits
		// for the end of its input.
		runShutdownWait()

		return
	default:
	}

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 1<<16), 1<<22)

	out := bufio.NewWriter(os.Stdout)

	for {
		if answer(mode, in, out) {
			return
		}
	}
}

// runShutdownWait answers shutdown, closes the output, and then blocks until
// the input ends. Close closes the input after the answer, and the adapter
// then exits 0.
func runShutdownWait() {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 1<<16), 1<<22)

	out := bufio.NewWriter(os.Stdout)

	for {
		if answer("waitstdin", in, out) {
			// Close the output, wait for the end of the input, and exit 0. The
			// harness must wait for this exit before it stops the group.
			if err := os.Stdout.Close(); err != nil {
				return
			}

			if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
				return
			}

			return
		}
	}
}

// answer reads one request line and writes its answer. end reports that the
// adapter must end: the input ended, the request was not JSON, a write failed,
// the mode stops, or the request was shutdown.
func answer(mode string, in *bufio.Scanner, out *bufio.Writer) bool {
	var req struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}

	if !in.Scan() {
		return true
	}

	if json.Unmarshal(in.Bytes(), &req) != nil {
		return true
	}

	reply, stop := fakeReply(mode, req.ID, req.Method)
	if reply != "" {
		if _, err := out.WriteString(reply + "\n"); err != nil {
			return true
		}

		if err := out.Flush(); err != nil {
			return true
		}
	}

	return stop || req.Method == conformance.MethodShutdown
}

// rightToLeftOverride is the format character U+202E in UTF-8. A report
// that prints it shows the text after it in the other direction.
const rightToLeftOverride = "\xe2\x80\xae"

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
	case "rpcerrorhostile":
		// A message with a false report line, a format character and a long tail
		// of 2-byte runes. The odd length of the head puts the limit inside a rune.
		msg := `"no\r\n--- PASS: TestForged` + rightToLeftOverride + `\nx` +
			strings.Repeat("é", conformance.MaxRPCMessageBytes) + `"`

		return fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":-32000,"message":%s}}`, id, msg), false
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

// TestProcessAdapterWaitsForTheExit checks that the harness waits for the exit
// of an adapter that ends on its own before it stops the process group. The
// fake adapter answers shutdown, closes its output, and then waits for the end
// of its input. Close closes the input after the answer, and the adapter exits
// 0. The test needs no clock. Without the wait for the exit, the group kill
// lands before the exit and Close reports a killed process.
func TestProcessAdapterWaitsForTheExit(t *testing.T) {
	t.Parallel()

	a := startFake(t, "waitstdin", 0)

	if err := a.Close(); err != nil {
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

// closeLogged closes the adapter. What Close reports depends on how the
// adapter ended, so the test only logs it.
func closeLogged(t *testing.T, a *conformance.ProcessAdapter) {
	t.Helper()

	if err := a.Close(); err != nil {
		t.Logf("close: %v", err)
	}
}

// TestProcessAdapterKillSkipsAReapedAdapter checks that kill sends no signal
// to a process group after Wait has reaped the adapter. A later signal could
// reach a new process that has the same process ID.
func TestProcessAdapterKillSkipsAReapedAdapter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		mode      string
		timeout   time.Duration
		wantErr   error
		afterCall func(t *testing.T, a *conformance.ProcessAdapter)
		wantKills int64
	}{
		{
			// The timeout in roundTrip calls kill while the process runs, so one
			// signal goes out. Close sees done closed and sends no second signal.
			name:    "adapter that runs",
			mode:    "hang",
			timeout: 300 * time.Millisecond,
			wantErr: context.DeadlineExceeded,
			afterCall: func(t *testing.T, a *conformance.ProcessAdapter) {
				t.Helper()
				closeLogged(t, a)
			},
			wantKills: 1,
		},
		{
			// The adapter exits by itself. Close returns only after done is
			// closed, so the later kill must send no signal.
			name:    "adapter that was reaped",
			mode:    "exitearly",
			timeout: 0,
			wantErr: conformance.ErrAdapterExited,
			afterCall: func(t *testing.T, a *conformance.ProcessAdapter) {
				t.Helper()
				closeLogged(t, a)
				conformance.Kill(a)
			},
			wantKills: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := startFake(t, tt.mode, tt.timeout)
			kills := conformance.CountKills(a)

			err := callOnce(t.Context(), a)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("want %v, got %v", tt.wantErr, err)
			}

			tt.afterCall(t, a)

			if got := kills.Load(); got != tt.wantKills {
				t.Errorf("kill sent %d signals to the process group, want %d", got, tt.wantKills)
			}
		})
	}
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

// TestProcessAdapterRPCErrorMessageIsSafe checks that the message of a
// JSON-RPC error from the adapter has no CR, LF or format character, is valid
// UTF-8, and has at most MaxRPCMessageBytes bytes, so that it cannot add a
// false line to a test report.
func TestProcessAdapterRPCErrorMessageIsSafe(t *testing.T) {
	t.Parallel()

	a := startFake(t, "rpcerrorhostile", 0)

	_, err := a.Paginate(t.Context(), paginateParams("x", 2))
	if closeErr := a.Close(); closeErr != nil {
		t.Errorf("Close = %v, want nil: an RPC error does not stop the session", closeErr)
	}

	var rpc *conformance.RPCError
	if !errors.As(err, &rpc) || rpc.Code != -32000 {
		t.Fatalf("want an RPCError with code -32000, got %v", err)
	}

	if strings.ContainsAny(rpc.Error(), "\r\n"+rightToLeftOverride) {
		t.Errorf("the error text holds a CR, an LF or a format character: %q", rpc.Error())
	}

	if !utf8.ValidString(rpc.Message) || len(rpc.Message) > conformance.MaxRPCMessageBytes {
		t.Errorf("the message has %d bytes (valid UTF-8: %t), want at most %d of valid UTF-8",
			len(rpc.Message), utf8.ValidString(rpc.Message), conformance.MaxRPCMessageBytes)
	}

	if !strings.HasPrefix(rpc.Message, "no  --- PASS: TestForged  xé") {
		t.Errorf("message = %q, want the text with each CR, LF and format character as a space", rpc.Message)
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
