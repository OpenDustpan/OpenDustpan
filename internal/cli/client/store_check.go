package client

// Store check subcommand: a client of POST /api/dustpan/v1/store-checks and
// GET /api/dustpan/v1/store-checks/{id}.
//
//	opendustpan store check --server https://dustpan.example --target blobs [--ca-file ca.pem]
//
// The command asks the server to run the deep check of its blob store or of
// its backup target, waits until the check ends, and prints the report. The
// target is a name ("blobs" or "backup"), never an address: the server reads
// its own store. The check adds load to the store and costs requests on a
// paid store. The exit code is 0 when every case passed and 1 when a case
// failed, the check did not run to its end, or an error stopped the command.
//
// This file is client code: it must not import a store adapter, a blob
// adapter, backup-target code or key-encryption key code.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/cli"
)

const (
	// storeCheckPollInterval sets how often the command reads the check while
	// it runs.
	storeCheckPollInterval = time.Second
	// storeChecksPath is the route that starts and lists the checks.
	storeChecksPath = "/api/dustpan/v1/store-checks"
	// storeCheckRerun names the one command that helps after an error.
	storeCheckRerun = "run opendustpan store check --target %s again"
)

// Sentinel errors of the store check subcommand. Each error text names what
// failed and the one command that helps.
var (
	// ErrStoreCheckNeedsServer reports a store check run without --server.
	ErrStoreCheckNeedsServer = errors.New("store check needs --server")
	// ErrStoreCheckTarget reports a target that is not "blobs" or "backup".
	ErrStoreCheckTarget = errors.New("store check target is missing or not on the list")
	// ErrStoreCheckStart reports a check that never started.
	ErrStoreCheckStart = errors.New("start store check failed")
	// ErrStoreCheckRead reports a check row that never arrived.
	ErrStoreCheckRead = errors.New("read store check failed")
	// ErrStoreCheckFailed reports a check that did not run to its end.
	ErrStoreCheckFailed = errors.New("store check failed")
	// ErrStoreCheckCases reports a check in which a case did not pass.
	ErrStoreCheckCases = errors.New("store check found a case that did not pass")
)

// storeCheckFlags holds the settings of the store check subcommand.
type storeCheckFlags struct {
	server string
	caFile string
	target string
}

func (f *storeCheckFlags) register(fs *flag.FlagSet) {
	cli.ByFlag("server").String(fs, &f.server)
	cli.ByFlag("ca-file").String(fs, &f.caFile)
	cli.ByFlag("target").String(fs, &f.target)
}

// StoreCheckLatency is a measured latency in microseconds.
type StoreCheckLatency struct {
	P50Micros int64 `json:"p50Micros"`
	P99Micros int64 `json:"p99Micros"`
}

// StoreCheckCase is the result of one case.
type StoreCheckCase struct {
	Name    string `json:"name"`
	Message string `json:"message"`
	Passed  bool   `json:"passed"`
}

// StoreCheckReport is the report of a check, as the server answers it.
type StoreCheckReport struct {
	Prefix            string            `json:"prefix"`
	ConditionalWrites string            `json:"conditionalWrites"`
	Cases             []StoreCheckCase  `json:"cases"`
	PutLatency        StoreCheckLatency `json:"putLatency"`
	GetLatency        StoreCheckLatency `json:"getLatency"`
	DeleteLatency     StoreCheckLatency `json:"deleteLatency"`
	PutBytesPerSecond int64             `json:"putBytesPerSecond"`
	GetBytesPerSecond int64             `json:"getBytesPerSecond"`
	DeletesPerSecond  int64             `json:"deletesPerSecond"`
	ThrottlingSeen    bool              `json:"throttlingSeen"`
	PrefixDeleted     bool              `json:"prefixDeleted"`
	Passed            bool              `json:"passed"`
}

// StoreCheckAnswer is the answer of GET /api/dustpan/v1/store-checks/{id}.
type StoreCheckAnswer struct {
	ID     string            `json:"id"`
	Target string            `json:"target"`
	State  string            `json:"state"`
	Error  string            `json:"error"`
	Report *StoreCheckReport `json:"report"`
}

// storeCheckTargets is the fixed list of targets. The server checks the name
// again. The command refuses any other value before it sends a request, so a
// typo or an address never leaves the host.
func storeCheckTargets() []string { return []string{"blobs", "backup"} }

// StoreCheck runs `opendustpan store check --server <url> --target <name>`.
func StoreCheck(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	token, err := BackupToken()
	if err != nil {
		return err
	}

	return runStoreCheck(ctx, args, token, stdout, stderr)
}

// runStoreCheck is StoreCheck with the token given, so a test sets no
// environment variable.
func runStoreCheck(ctx context.Context, args []string, token string, stdout, stderr io.Writer) error {
	var f storeCheckFlags

	fs := flag.NewFlagSet("store check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	f.register(fs)

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	if fs.NArg() != 0 {
		return ErrUsage
	}

	target := strings.TrimSpace(f.target)
	if !slices.Contains(storeCheckTargets(), target) {
		return fmt.Errorf(
			"%w: set --target to blobs or backup and run opendustpan store check again",
			ErrStoreCheckTarget,
		)
	}

	server := strings.TrimSpace(f.server)
	if server == "" {
		return fmt.Errorf(
			"%w: set --server (%s) to the server base URL and run opendustpan store check --target %s again",
			ErrStoreCheckNeedsServer, BackupServerEnvVar, target,
		)
	}

	if err := cli.ByFlag("server").CheckURL(server); err != nil {
		return err
	}

	client, err := newBackupHTTPClient(f.caFile, fmt.Sprintf(storeCheckRerun, target), stderr)
	if err != nil {
		return err
	}

	call := storeCheckCall{client: client, server: server, token: token, target: target}

	id, err := call.start(ctx)
	if err != nil {
		return err
	}

	doc, err := call.wait(ctx, id)
	if err != nil {
		return err
	}

	return finishStoreCheck(stdout, doc, target)
}

// exchange sends req and returns the status and the body, which it reads up to
// MaxClientBodyBytes. A larger body is an error wrapping ErrResponseTooLarge,
// so a hostile or broken server cannot grow the client without bound.
func exchange(client *http.Client, req *http.Request) (int, []byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("send request: %w", err)
	}

	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, MaxClientBodyBytes+1))
	closeErr := resp.Body.Close()

	if readErr != nil || closeErr != nil {
		return 0, nil, fmt.Errorf("read server answer: %w", errors.Join(readErr, closeErr))
	}

	if len(raw) > MaxClientBodyBytes {
		return 0, nil, fmt.Errorf("%w: over %d bytes", ErrResponseTooLarge, MaxClientBodyBytes)
	}

	return resp.StatusCode, raw, nil
}

// answerMessage returns the text of an error answer: the status and the
// message of the server, or the status and the text of the body when the body
// is not the JSON error shape.
func answerMessage(status int, raw []byte) string {
	var doc errorAnswer

	if err := json.Unmarshal(raw, &doc); err == nil && doc.Message != "" {
		return fmt.Sprintf("%d %s: %s", status, http.StatusText(status), doc.Message)
	}

	if text := strings.TrimSpace(string(raw)); text != "" {
		return fmt.Sprintf("%d %s: %s", status, http.StatusText(status), text)
	}

	return fmt.Sprintf("%d %s", status, http.StatusText(status))
}

// storeCheckCall holds what each request of the command needs: the client, the
// server, the token and the target.
type storeCheckCall struct {
	client *http.Client
	server string
	token  string
	target string
}

// start starts a check and answers its ID.
func (c storeCheckCall) start(ctx context.Context) (string, error) {
	rerun := fmt.Sprintf(storeCheckRerun, c.target)

	body, err := json.Marshal(map[string]string{"target": c.target})
	if err != nil {
		return "", fmt.Errorf("encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, backupEndpoint(c.server, storeChecksPath), bytes.NewReader(body),
	)
	if err != nil {
		return "", fmt.Errorf("%w: %w: check --server and %s", ErrStoreCheckStart, err, rerun)
	}

	req.Header.Set("Content-Type", "application/json")
	backupAuth(req, c.token)

	status, raw, err := exchange(c.client, req)
	if err != nil {
		return "", fmt.Errorf("%w: %w: check --server and %s", ErrStoreCheckStart, err, rerun)
	}

	if status != http.StatusAccepted {
		return "", fmt.Errorf("%w: %s", ErrStoreCheckStart,
			answerText(status, answerMessage(status, raw), "check the server log and "+rerun))
	}

	var doc StoreCheckAnswer

	if err := json.Unmarshal(raw, &doc); err != nil || strings.TrimSpace(doc.ID) == "" {
		return "", fmt.Errorf(
			"%w: the server answered no check ID: check the server log and %s",
			ErrStoreCheckStart,
			rerun,
		)
	}

	return doc.ID, nil
}

// read reads one check.
func (c storeCheckCall) read(ctx context.Context, id string) (StoreCheckAnswer, error) {
	rerun := fmt.Sprintf(storeCheckRerun, c.target)

	req, err := http.NewRequestWithContext(
		ctx, http.MethodGet, backupEndpoint(c.server, storeChecksPath+"/"+url.PathEscape(id)), http.NoBody,
	)
	if err != nil {
		return StoreCheckAnswer{}, fmt.Errorf("%w %s: %w: %s", ErrStoreCheckRead, id, err, rerun)
	}

	backupAuth(req, c.token)

	status, raw, err := exchange(c.client, req)
	if err != nil {
		return StoreCheckAnswer{}, fmt.Errorf("%w %s: %w: %s", ErrStoreCheckRead, id, err, rerun)
	}

	if status != http.StatusOK {
		return StoreCheckAnswer{}, fmt.Errorf("%w %s: %s", ErrStoreCheckRead, id,
			answerText(status, answerMessage(status, raw), rerun))
	}

	var doc StoreCheckAnswer

	if err := json.Unmarshal(raw, &doc); err != nil {
		return StoreCheckAnswer{}, fmt.Errorf("%w %s: %w: %s", ErrStoreCheckRead, id, err, rerun)
	}

	return doc, nil
}

// wait polls one check until it leaves running. A canceled context stops the
// wait with the context error.
func (c storeCheckCall) wait(ctx context.Context, id string) (StoreCheckAnswer, error) {
	timer := time.NewTimer(storeCheckPollInterval)
	defer timer.Stop()

	for {
		doc, err := c.read(ctx, id)
		if err != nil {
			return StoreCheckAnswer{}, err
		}

		if doc.State != "running" {
			return doc, nil
		}

		select {
		case <-ctx.Done():
			return StoreCheckAnswer{}, fmt.Errorf(
				"wait for store check %s: %w: "+storeCheckRerun,
				id,
				ctx.Err(),
				c.target,
			)
		case <-timer.C:
			timer.Reset(storeCheckPollInterval)
		}
	}
}

// finishStoreCheck prints the report and returns the error that sets the exit
// code: 1 for a check that did not run to its end, and 1 for a case that did
// not pass.
func finishStoreCheck(stdout io.Writer, doc StoreCheckAnswer, target string) error {
	if err := writeStoreCheckReport(stdout, doc); err != nil {
		return err
	}

	if doc.State != "succeeded" {
		return fmt.Errorf(
			"%w %s: %s: "+storeCheckRerun,
			ErrStoreCheckFailed,
			doc.ID,
			strings.TrimSpace(doc.Error),
			target,
		)
	}

	if doc.Report == nil || !doc.Report.Passed {
		return fmt.Errorf("%w in check %s: read the cases above", ErrStoreCheckCases, doc.ID)
	}

	return nil
}

// writeStoreCheckReport prints what the check tested and what it measured. A
// figure is a value from this run on this store, and says nothing about
// another time.
func writeStoreCheckReport(w io.Writer, doc StoreCheckAnswer) error {
	var out strings.Builder

	fmt.Fprintf(&out, "store check %s on target %s: %s\n", doc.ID, doc.Target, doc.State)

	if doc.Error != "" {
		fmt.Fprintf(&out, "stopped: %s\n", doc.Error)
	}

	if r := doc.Report; r != nil {
		fmt.Fprintf(&out, "throwaway prefix: %s/\n", r.Prefix)

		for _, c := range r.Cases {
			verdict := "pass"
			if !c.Passed {
				verdict = "FAIL"
			}

			fmt.Fprintf(&out, "  %s  %s\n", verdict, c.Name)

			if c.Message != "" {
				fmt.Fprintf(&out, "        %s\n", c.Message)
			}
		}

		fmt.Fprintf(&out, "conditional writes: %s\n", r.ConditionalWrites)
		fmt.Fprintf(&out, "throttling seen: %t\n", r.ThrottlingSeen)
		fmt.Fprintf(&out, "prefix deleted: %t\n", r.PrefixDeleted)
		fmt.Fprintf(&out, "put latency: p50 %d us, p99 %d us\n", r.PutLatency.P50Micros, r.PutLatency.P99Micros)
		fmt.Fprintf(&out, "get latency: p50 %d us, p99 %d us\n", r.GetLatency.P50Micros, r.GetLatency.P99Micros)
		fmt.Fprintf(
			&out,
			"delete latency: p50 %d us, p99 %d us\n",
			r.DeleteLatency.P50Micros,
			r.DeleteLatency.P99Micros,
		)
		fmt.Fprintf(&out, "put throughput: %d bytes per second\n", r.PutBytesPerSecond)
		fmt.Fprintf(&out, "get throughput: %d bytes per second\n", r.GetBytesPerSecond)
		fmt.Fprintf(&out, "delete rate: %d objects per second\n", r.DeletesPerSecond)
	}

	if _, err := io.WriteString(w, out.String()); err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return nil
}
