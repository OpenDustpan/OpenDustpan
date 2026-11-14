package conformance

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"
)

// Time limits of the adapter process.
const (
	// DefaultCallTimeout bounds one request to the adapter.
	DefaultCallTimeout = 30 * time.Second
	// closeTimeout bounds the wait for the adapter to exit after shutdown.
	closeTimeout = 5 * time.Second
	// stderrKeepBytes is the part of the standard error of the adapter that the
	// harness keeps for a report.
	stderrKeepBytes = 8 << 10
	// readBufferBytes is the size of the buffer of the reader of the output of
	// the adapter. It holds one message and its line break.
	readBufferBytes = MaxMessageBytes + 1
	// MaxRPCMessageBytes bounds the message of an RPCError. The harness cuts a
	// longer message from the adapter.
	MaxRPCMessageBytes = 512
)

// RPCError is an error object of a JSON-RPC 2.0 answer. It wraps
// ErrRequestRefused: a correct adapter reports a fault of the client in the
// result, so the answer is a fault of this one request. The framing is still
// in step, so the session goes on and only the case that sent the request
// fails. Message is the text from the adapter after rpcMessage: it has no
// control character and at most MaxRPCMessageBytes bytes.
type RPCError struct {
	Code    int
	Message string
}

// Error implements error.
func (e *RPCError) Error() string {
	return fmt.Sprintf("%v: JSON-RPC error %d: %s", ErrRequestRefused, e.Code, e.Message)
}

// Unwrap lets errors.Is find ErrRequestRefused.
func (e *RPCError) Unwrap() error { return ErrRequestRefused }

// ProcessOptions are the settings of StartAdapter.
type ProcessOptions struct {
	// Path is the adapter program, and Args are its arguments. The harness runs
	// it with exec.Command and no shell.
	Path string
	Args []string
	// Env is the whole environment of the adapter. The adapter never gets the
	// environment of the harness, because CI holds tokens in it.
	Env []string
	// CallTimeout bounds one request. Zero means DefaultCallTimeout.
	CallTimeout time.Duration
}

// ProcessAdapter is an Adapter that talks to a child process. It is not safe
// for concurrent use: the protocol has one request in flight.
type ProcessAdapter struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	lines   chan []byte
	done    chan struct{}
	stderr  *tailBuffer
	timeout time.Duration

	nextID int64
	// stop ends a send that waits for a reader. kill closes it.
	stop     chan struct{}
	stopOnce sync.Once
	killed   atomic.Bool
	// groupEnded says that the post-exit step has ended the process group.
	// The wait that follows can then reap the adapter, so its process ID, and
	// the ID of its group, can belong to another process: a later kill sends
	// no signal.
	groupEnded atomic.Bool
	// killProcess stops the process group of the adapter. It is killGroup; a
	// test replaces it to count the calls.
	killProcess func(p *os.Process) error
	// readErr and waitErr are set before done is closed.
	readErr error
	waitErr error
}

// StartAdapter starts the adapter process.
func StartAdapter(ctx context.Context, opts ProcessOptions) (*ProcessAdapter, error) {
	a := new(ProcessAdapter)

	cmd := exec.CommandContext(ctx, opts.Path, opts.Args...) //nolint:gosec // G204: the caller names the program.
	cmd.Env = append([]string{}, opts.Env...)                // Never nil: nil would give the adapter our environment.
	cmd.WaitDelay = closeTimeout

	// The adapter runs in its own process group, and a stop ends the whole
	// group. An adapter that starts through a shell, npx or uv leaves no
	// grandchild behind. A stop after the post-exit step has ended the group
	// sends no signal.
	prepareGroup(cmd, a)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("adapter stdin: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("adapter stdout: %w", err)
	}

	a.cmd, a.stdin, a.stderr, a.timeout = cmd, stdin, new(tailBuffer), opts.CallTimeout
	a.killProcess = killGroup
	a.lines, a.done, a.stop = make(chan []byte), make(chan struct{}), make(chan struct{})

	a.stderr.max = stderrKeepBytes
	if a.timeout <= 0 {
		a.timeout = DefaultCallTimeout
	}

	cmd.Stderr = a.stderr

	if err = cmd.Start(); err != nil {
		return nil, fmt.Errorf("start the adapter: %w", err)
	}

	go a.read(stdout)

	return a, nil
}

// Initialize implements Adapter.
func (a *ProcessAdapter) Initialize(ctx context.Context, p InitializeParams) (InitializeResult, error) {
	var res InitializeResult

	if err := a.roundTrip(ctx, MethodInitialize, p, &res); err != nil {
		return res, err
	}

	if res.ProtocolVersion != ProtocolVersion {
		return res, fmt.Errorf("%w: the adapter has version %d and the harness has %d",
			ErrProtocolVersion, res.ProtocolVersion, ProtocolVersion)
	}

	return res, nil
}

// Call implements Adapter.
func (a *ProcessAdapter) Call(ctx context.Context, p CallParams) (CallResult, error) {
	var res CallResult

	err := a.roundTrip(ctx, MethodCall, p, &res)

	return res, err
}

// Paginate implements Adapter.
func (a *ProcessAdapter) Paginate(ctx context.Context, p PaginateParams) (PaginateResult, error) {
	var res PaginateResult

	err := a.roundTrip(ctx, MethodPaginate, p, &res)

	return res, err
}

// Close implements Adapter. It sends shutdown, closes the input, and waits for
// the exit. It stops the process when the exit takes more than five seconds.
func (a *ProcessAdapter) Close() error {
	select {
	case <-a.done:
	default:
		a.shutdown()
	}

	if a.killed.Load() {
		return fmt.Errorf("%w: the harness stopped the process", ErrAdapterExited)
	}

	if a.waitErr != nil {
		return fmt.Errorf("%w: %w; standard error: %q", ErrAdapterExited, a.waitErr, a.stderr.String())
	}

	return nil
}

// shutdown asks the adapter to end, and stops it when it does not.
func (a *ProcessAdapter) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()

	// A process that is gone gives an error here. It is closed anyway.
	if err := a.roundTrip(ctx, MethodShutdown, struct{}{}, nil); err != nil {
		a.stderr.note(err)
	}

	if err := a.stdin.Close(); err != nil {
		a.stderr.note(err)
	}

	select {
	case <-a.done:
	case <-time.After(closeTimeout):
		a.kill()
		<-a.done
	}
}

// read sends each line of the standard output to the channel until the output
// ends, and then waits for the process.
func (a *ProcessAdapter) read(stdout io.Reader) {
	defer close(a.done)
	defer close(a.lines)

	r := bufio.NewReaderSize(stdout, readBufferBytes)

	for a.readLine(r) {
	}

	// The process may still write; drain the pipe so that it can exit.
	if _, err := io.Copy(io.Discard, stdout); err != nil {
		a.stderr.note(err)
	}

	// Stop the processes that the adapter left behind: an adapter that exits
	// on its own does not stop them. Nothing reaps the adapter before the
	// wait below, so its process ID stays reserved for the group kill. Where
	// the platform supports it, the step waits for the exit, so that the kill
	// cannot end a clean adapter while it exits.
	if a.cmd.Process != nil {
		endGroupAfterExit(a)
	}

	a.waitErr = a.cmd.Wait()
}

// readLine sends one line. It returns false at the end of the output, on an
// error, or when the harness stops the process.
func (a *ProcessAdapter) readLine(r *bufio.Reader) bool {
	line, err := r.ReadSlice('\n')
	if err != nil {
		if errors.Is(err, bufio.ErrBufferFull) {
			a.readErr = fmt.Errorf("%w: a message is longer than %d bytes", ErrProtocol, MaxMessageBytes)
			a.kill()
		} else if !errors.Is(err, io.EOF) {
			a.readErr = fmt.Errorf("read the adapter output: %w", err)
		}

		return false
	}

	select {
	case a.lines <- bytes.Clone(bytes.TrimRight(line, "\r\n")):
		return true
	case <-a.stop:
		return false
	}
}

// kill stops the process. The read goroutine then ends.
func (a *ProcessAdapter) kill() {
	a.killed.Store(true)
	a.stopOnce.Do(func() { close(a.stop) })

	select {
	case <-a.done:
		// Wait reaped the adapter. Its process ID, and so the ID of its
		// group, can now belong to another process: send no signal.
	default:
		// The post-exit step may have ended the group while Wait still runs.
		// A signal then could reach a group ID that another process owns.
		if a.cmd.Process != nil && !a.groupEnded.Load() {
			if err := a.killProcess(a.cmd.Process); err != nil {
				a.stderr.note(err)
			}
		}
	}

	// A write that waits for an adapter that does not read ends with this
	// close. The error of a second close is of no use.
	if err := a.stdin.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		a.stderr.note(err)
	}
}

// response is a JSON-RPC 2.0 answer.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

// rpcError is the error object of a JSON-RPC 2.0 answer.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// roundTrip sends one request and decodes the result into out.
func (a *ProcessAdapter) roundTrip(ctx context.Context, method string, params, out any) error {
	a.nextID++

	id := a.nextID

	req, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return fmt.Errorf("encode %s: %w", method, err)
	}

	if len(req) > MaxMessageBytes {
		return fmt.Errorf("%w: the %s request is longer than %d bytes", ErrProtocol, method, MaxMessageBytes)
	}

	// The limit covers the write too. A pipe holds 64 KiB, so a long request
	// blocks the write when the adapter does not read, and a write cannot be
	// canceled: it ends when kill closes the pipe.
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	written := make(chan error, 1)

	go func() {
		_, werr := a.stdin.Write(append(req, '\n'))
		written <- werr
	}()

	select {
	case werr := <-written:
		if werr != nil {
			return fmt.Errorf("%w: write %s: %w", ErrAdapterExited, method, werr)
		}
	case <-ctx.Done():
		a.kill()
		writeErr := <-written
		_ = writeErr // The kill closes the pipe, so the write reports it.

		return fmt.Errorf("write %s: %w", method, ctx.Err())
	}

	select {
	case line, ok := <-a.lines:
		if !ok {
			return a.exited(method)
		}

		return a.decode(line, id, method, out)
	case <-ctx.Done():
		a.kill()

		return fmt.Errorf("%s: %w", method, ctx.Err())
	}
}

// exited builds the error for an adapter that ended before it answered.
func (a *ProcessAdapter) exited(method string) error {
	<-a.done

	if a.readErr != nil {
		return a.readErr
	}

	return fmt.Errorf("%w: during %s: %w; standard error: %q", ErrAdapterExited, method, a.waitErr, a.stderr.String())
}

// decode checks one answer line and decodes its result.
func (a *ProcessAdapter) decode(line []byte, id int64, method string, out any) error {
	var r response
	if err := json.Unmarshal(line, &r); err != nil {
		a.kill()

		return fmt.Errorf("%w: the answer to %s is not JSON: %w", ErrProtocol, method, err)
	}

	if r.JSONRPC != "2.0" || string(r.ID) != strconv.FormatInt(id, 10) {
		a.kill()

		return fmt.Errorf("%w: the answer to %s has jsonrpc %q and ID %s, and the request ID is %d",
			ErrProtocol, method, r.JSONRPC, r.ID, id)
	}

	if r.Error != nil {
		return &RPCError{Code: r.Error.Code, Message: rpcMessage(r.Error.Message)}
	}

	if out == nil {
		return nil
	}

	if err := json.Unmarshal(r.Result, out); err != nil {
		return fmt.Errorf("%w: the result of %s: %w", ErrProtocol, method, err)
	}

	return nil
}

// rpcMessage makes the message of a JSON-RPC error safe for a test report.
// The adapter is not trusted, so a CR or an LF could add a false line to the
// report. It changes each control or format character, CR and LF included,
// to a space, and it changes bytes that are not valid UTF-8 to U+FFFD. It
// cuts a message that is longer than MaxRPCMessageBytes at a rune boundary.
func rpcMessage(m string) string {
	m = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			return ' '
		}

		return r
	}, strings.ToValidUTF8(m, string(utf8.RuneError)))

	if len(m) <= MaxRPCMessageBytes {
		return m
	}

	cut := MaxRPCMessageBytes
	for cut > 0 && !utf8.RuneStart(m[cut]) {
		cut--
	}

	return m[:cut]
}

// tailBuffer keeps the last bytes that are written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

// Write implements io.Writer.
func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}

	return len(p), nil
}

// String returns the kept bytes.
func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	return string(t.buf)
}

// note keeps the text of an error that has no other place to go, so that a
// report can show it.
func (t *tailBuffer) note(err error) {
	if _, werr := t.Write([]byte("harness: " + err.Error() + "\n")); werr != nil {
		return
	}
}

// keep adds a line of text.
func (t *tailBuffer) keep(line string) {
	if _, err := t.Write([]byte(line + "\n")); err != nil {
		return
	}
}

// Environ returns the variables of this process that have the given names, as
// NAME=value strings, and then extra. The result is never nil, so that a child
// process does not inherit the rest of the environment. The harness reads each
// variable by name and never lists the environment.
func Environ(names []string, extra ...string) []string {
	env := make([]string, 0, len(names)+len(extra))

	for _, name := range names {
		if v, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+v)
		}
	}

	return append(env, extra...)
}
