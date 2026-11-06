// Command demo-seed seeds a local OpenDustpan demo installation with
// namespaces, stacks, finished updates and one backup.
//
//	demo-seed -url <server> [-token <token>]
//
// The token comes from -token or, when -token is empty, from the
// DUSTPAN_ADMIN_TOKEN environment variable, so scripts/demo.sh passes it
// through the environment and it never shows in process listings. The exit
// code is 0 on success and 1 on any refusal.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Settings and wire values of the seed run.
const (
	// requestTimeout bounds one seed API request.
	requestTimeout = 30 * time.Second
	// backupPollInterval spaces backup state polls.
	backupPollInterval = 500 * time.Millisecond
	// backupPollTimeout bounds the wait for a backup to leave running.
	backupPollTimeout = 60 * time.Second
	// errorBodyLimit caps an error body the seed reads: answers are small,
	// and the limit keeps a long body out of the error text.
	errorBodyLimit = 4 << 10
	// successBodyLimit caps a successful body the seed reads: the backup
	// manifest is larger than the error limit.
	successBodyLimit = 1 << 20
	// contentTypeJSON marks request bodies the seed sends.
	contentTypeJSON = "application/json"
	// projectDemo is the project every seed stack lives in.
	projectDemo = "demo"
	// namespacePlatform and namespaceData are the seeded namespaces.
	namespacePlatform = "demo-platform"
	namespaceData     = "demo-data"
	// tokenEnvVar holds the admin token when -token is empty. The value names
	// the setting; it holds no credential.
	tokenEnvVar = "DUSTPAN_ADMIN_TOKEN" //nolint:gosec // The value names the setting; it holds no credential.
	// backupStateRunning and backupStateSucceeded are the backup states the
	// seed waits for.
	backupStateRunning   = "running"
	backupStateSucceeded = "succeeded"
	// statusSucceeded and statusFailed are the update outcomes the seed records.
	statusSucceeded = "succeeded"
	statusFailed    = "failed"
	// hintServerLog names the one thing to do when a seed call fails.
	hintServerLog = "see the server log above, then run make demo again"
)

// Sentinel errors of the seed run. Each error text names what failed.
var (
	// errSeedNeedsURL reports a seed run without -url.
	errSeedNeedsURL = errors.New("demo seed needs a server URL")
	// errSeedNoToken reports a seed run without an admin token.
	errSeedNoToken = errors.New("demo seed has no admin token")
	// errCreateNamespace reports a namespace that never arrived.
	errCreateNamespace = errors.New("create namespace failed")
	// errCreateStack reports a stack that never arrived.
	errCreateStack = errors.New("create stack failed")
	// errCreateUpdate reports an update that never arrived.
	errCreateUpdate = errors.New("create update failed")
	// errStartUpdate reports an update that never started.
	errStartUpdate = errors.New("start update failed")
	// errCompleteUpdate reports an update that never finished.
	errCompleteUpdate = errors.New("complete update failed")
	// errStartBackup reports a backup that never started.
	errStartBackup = errors.New("start backup failed")
	// errReadBackup reports a backup state that never arrived.
	errReadBackup = errors.New("read backup failed")
	// errWaitBackup reports a backup that did not succeed.
	errWaitBackup = errors.New("backup did not succeed")
)

// seeder seeds one server: its base URL, admin token, client and output.
type seeder struct {
	base   string
	token  string
	client *http.Client
	out    io.Writer
}

// apiCall is one seed request: its display name, method, path, full
// Authorization header value, body and wanted status. Fail is the sentinel
// the error wraps, so every failure names its call.
type apiCall struct {
	name   string
	method string
	path   string
	auth   string
	body   any
	want   int
	fail   error
}

// stackSeed is one stack to create with the outcomes of its updates in order.
type stackSeed struct {
	namespace string
	stack     string
	statuses  []string
}

// updateTarget names one update: its stack with the update ID. The ID stays
// empty until createUpdate answers it.
type updateTarget struct {
	namespace string
	stack     string
	id        string
}

// namespaceRequest is the namespace create body: the name to create.
type namespaceRequest struct {
	Name string `json:"name"`
}

// stackRequest is the stack create body: the stack name to create.
type stackRequest struct {
	StackName string `json:"stackName"`
}

// completeRequest ends an update with the status its client reports.
type completeRequest struct {
	Status string `json:"status"`
}

// updateCreateResponse answers an update create: the opaque ID of the update.
type updateCreateResponse struct {
	UpdateID string `json:"updateID"`
}

// updateStartResponse answers an update start: the lease its client presents
// on every later write.
type updateStartResponse struct {
	Token string `json:"token"`
}

// backupStartResponse answers a started backup: its ID.
type backupStartResponse struct {
	ID string `json:"id"`
}

// backupGetResponse answers one backup: its state and, when it failed, the
// error text.
type backupGetResponse struct {
	State string `json:"state"`
	Error string `json:"error"`
}

// apiErrorResponse is the error body the server answers. Only the message is
// read; the code repeats the status.
type apiErrorResponse struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

func main() {
	ctx := context.Background()

	if err := runMain(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "demo-seed:", err)
		os.Exit(1)
	}
}

// runMain parses args and seeds the server at -url. The token comes from
// -token or from DUSTPAN_ADMIN_TOKEN when -token is empty.
func runMain(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("demo-seed", flag.ContinueOnError)
	fs.SetOutput(stderr)

	urlFlag := fs.String(
		"url",
		"",
		"server base URL, for example http://127.0.0.1:8080",
	)
	tokenFlag := fs.String(
		"token",
		"",
		"admin token (default the DUSTPAN_ADMIN_TOKEN environment variable)",
	)
	fs.Usage = func() {
		_, err := fmt.Fprintln(
			stderr,
			"Usage: demo-seed -url <server> [-token <token>]\n"+
				"Seed a local demo installation with namespaces, stacks, updates and one backup.\n"+
				"The token comes from -token or from the DUSTPAN_ADMIN_TOKEN environment variable.",
		)
		if err != nil {
			return
		}
	}

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	base := strings.TrimSpace(*urlFlag)
	if base == "" {
		return fmt.Errorf("%w: set -url to the server base URL and run make demo again", errSeedNeedsURL)
	}

	token := strings.TrimSpace(*tokenFlag)
	if token == "" {
		token = strings.TrimSpace(os.Getenv(tokenEnvVar))
	}

	if token == "" {
		return fmt.Errorf("%w: set %s and run make demo again", errSeedNoToken, tokenEnvVar)
	}

	return runSeed(ctx, stdout, base, token)
}

// stackSeeds answers the stacks to create with the outcomes of their updates
// in order.
func stackSeeds() []stackSeed {
	return []stackSeed{
		{
			namespace: namespacePlatform,
			stack:     "dev",
			statuses:  []string{statusSucceeded, statusFailed, statusSucceeded},
		},
		{
			namespace: namespacePlatform,
			stack:     "prod",
			statuses:  []string{statusSucceeded, statusSucceeded},
		},
		{
			namespace: namespaceData,
			stack:     "staging",
			statuses:  []string{statusFailed, statusSucceeded},
		},
	}
}

// runSeed creates the namespaces, stacks, updates and one backup on the
// server at base, and prints one line per step.
func runSeed(ctx context.Context, stdout io.Writer, base, token string) error {
	s := &seeder{
		base:   strings.TrimSuffix(base, "/"),
		token:  token,
		client: &http.Client{Timeout: requestTimeout},
		out:    stdout,
	}

	for _, name := range []string{namespacePlatform, namespaceData} {
		if err := s.createNamespace(ctx, name); err != nil {
			return err
		}
	}

	for _, item := range stackSeeds() {
		if err := s.createStack(ctx, item.namespace, item.stack); err != nil {
			return err
		}

		for _, status := range item.statuses {
			if err := s.finishUpdate(ctx, item.namespace, item.stack, status); err != nil {
				return err
			}
		}
	}

	if err := s.seedBackup(ctx); err != nil {
		return err
	}

	return s.announce("demo data is ready")
}

// bearerAuth answers the Authorization header value of the installation API.
func bearerAuth(token string) string {
	return "Bearer " + token
}

// tokenAuth answers the Authorization header value of the stack API.
func tokenAuth(token string) string {
	return "token " + token
}

// updateTokenAuth answers the Authorization header value of an update lease.
func updateTokenAuth(lease string) string {
	return "update-token " + lease
}

// announce prints one seed step.
func (s *seeder) announce(line string) error {
	if _, err := fmt.Fprintln(s.out, line); err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return nil
}

// createNamespace creates name through POST /api/server/namespaces.
func (s *seeder) createNamespace(ctx context.Context, name string) error {
	call := apiCall{
		name:   "create namespace",
		method: http.MethodPost,
		path:   "/api/server/namespaces",
		auth:   bearerAuth(s.token),
		body:   namespaceRequest{Name: name},
		want:   http.StatusCreated,
		fail:   errCreateNamespace,
	}

	if err := s.call(ctx, call, nil); err != nil {
		return err
	}

	if err := s.placeEveryStack(ctx, name); err != nil {
		return err
	}

	return s.announce("created namespace " + name)
}

// demoCollection is the collection that holds the demo stacks.
const demoCollection = "demo"

// collectionRequest is the collection create body.
type collectionRequest struct {
	Name string `json:"name"`
}

// placementRuleRequest is the placement rule create body.
type placementRuleRequest struct {
	Tool       string `json:"tool"`
	Project    string `json:"project"`
	Stack      string `json:"stack"`
	Collection string `json:"collection"`
}

// placeEveryStack makes the collection demoCollection and a placement rule
// that puts every Pulumi stack in it. The server refuses a new stack that no
// rule places.
func (s *seeder) placeEveryStack(ctx context.Context, name string) error {
	base := "/api/dustpan/v1/namespaces/" + name

	for _, call := range []apiCall{
		{
			name: "create collection", method: http.MethodPost, path: base + "/collections",
			auth: bearerAuth(s.token), body: collectionRequest{Name: demoCollection},
			want: http.StatusCreated, fail: errCreateNamespace,
		},
		{
			name: "create placement rule", method: http.MethodPost, path: base + "/placement-rules",
			auth: bearerAuth(s.token),
			body: placementRuleRequest{Tool: "pulumi", Project: "*", Stack: "*", Collection: demoCollection},
			want: http.StatusCreated, fail: errCreateNamespace,
		},
	} {
		if err := s.call(ctx, call, nil); err != nil {
			return err
		}
	}

	return nil
}

// createStack creates stack in projectDemo through the stack API.
func (s *seeder) createStack(ctx context.Context, namespace, stack string) error {
	call := apiCall{
		name:   "create stack",
		method: http.MethodPost,
		path:   "/api/stacks/" + namespace + "/" + projectDemo,
		auth:   tokenAuth(s.token),
		body:   stackRequest{StackName: stack},
		want:   http.StatusOK,
		fail:   errCreateStack,
	}

	if err := s.call(ctx, call, nil); err != nil {
		return err
	}

	return s.announce("created stack " + namespace + "/" + stack)
}

// finishUpdate records one finished update with status on the stack: it
// creates the update, starts it for its lease, and completes it.
func (s *seeder) finishUpdate(ctx context.Context, namespace, stack, status string) error {
	target, err := s.createUpdate(ctx, namespace, stack)
	if err != nil {
		return err
	}

	lease, err := s.startUpdate(ctx, target)
	if err != nil {
		return err
	}

	if err := s.completeUpdate(ctx, target, lease, status); err != nil {
		return err
	}

	return s.announce("finished update " + target.id + " with status " + status)
}

// updatePath answers the update path of target: the create path while the ID
// is empty, else the path of the update.
func updatePath(target updateTarget) string {
	path := "/api/stacks/" + target.namespace + "/" + projectDemo + "/" + target.stack + "/update"

	if target.id != "" {
		path += "/" + target.id
	}

	return path
}

// createUpdate records a new update and answers its target with the ID the
// server answered.
func (s *seeder) createUpdate(ctx context.Context, namespace, stack string) (updateTarget, error) {
	var created updateCreateResponse

	target := updateTarget{namespace: namespace, stack: stack, id: ""}

	call := apiCall{
		name:   "create update",
		method: http.MethodPost,
		path:   updatePath(target),
		auth:   tokenAuth(s.token),
		body:   struct{}{},
		want:   http.StatusOK,
		fail:   errCreateUpdate,
	}

	if err := s.call(ctx, call, &created); err != nil {
		return updateTarget{}, err
	}

	if strings.TrimSpace(created.UpdateID) == "" {
		return updateTarget{}, fmt.Errorf("%w: the server answered no update ID: %s", errCreateUpdate, hintServerLog)
	}

	target.id = created.UpdateID

	return target, nil
}

// startUpdate starts the update and answers its lease.
func (s *seeder) startUpdate(ctx context.Context, target updateTarget) (string, error) {
	var started updateStartResponse

	call := apiCall{
		name:   "start update",
		method: http.MethodPost,
		path:   updatePath(target),
		auth:   tokenAuth(s.token),
		body:   nil,
		want:   http.StatusOK,
		fail:   errStartUpdate,
	}

	if err := s.call(ctx, call, &started); err != nil {
		return "", err
	}

	if strings.TrimSpace(started.Token) == "" {
		return "", fmt.Errorf("%w: the server answered no lease: %s", errStartUpdate, hintServerLog)
	}

	return started.Token, nil
}

// completeUpdate ends the update with status, authorized by its lease.
func (s *seeder) completeUpdate(ctx context.Context, target updateTarget, lease, status string) error {
	call := apiCall{
		name:   "complete update",
		method: http.MethodPost,
		path:   updatePath(target) + "/complete",
		auth:   updateTokenAuth(lease),
		body:   completeRequest{Status: status},
		want:   http.StatusOK,
		fail:   errCompleteUpdate,
	}

	return s.call(ctx, call, nil)
}

// seedBackup starts one backup and waits until it succeeds.
func (s *seeder) seedBackup(ctx context.Context) error {
	var started backupStartResponse

	call := apiCall{
		name:   "start backup",
		method: http.MethodPost,
		path:   "/api/server/backups",
		auth:   bearerAuth(s.token),
		body:   nil,
		want:   http.StatusAccepted,
		fail:   errStartBackup,
	}

	if err := s.call(ctx, call, &started); err != nil {
		return err
	}

	if strings.TrimSpace(started.ID) == "" {
		return fmt.Errorf("%w: the server answered no backup ID: %s", errStartBackup, hintServerLog)
	}

	if err := s.announce("started backup " + started.ID); err != nil {
		return err
	}

	return s.pollBackup(ctx, started.ID)
}

// pollBackup waits until the backup leaves running, and fails unless it
// succeeded.
func (s *seeder) pollBackup(ctx context.Context, id string) error {
	pollCtx, cancel := context.WithTimeout(ctx, backupPollTimeout)
	defer cancel()

	for {
		var doc backupGetResponse

		call := apiCall{
			name:   "read backup",
			method: http.MethodGet,
			path:   "/api/server/backups/" + id,
			auth:   bearerAuth(s.token),
			body:   nil,
			want:   http.StatusOK,
			fail:   errReadBackup,
		}

		if err := s.call(pollCtx, call, &doc); err != nil {
			return err
		}

		if doc.State != backupStateRunning {
			if doc.State != backupStateSucceeded {
				return fmt.Errorf(
					"%w %s: the server answered state %q: %s: %s",
					errWaitBackup,
					id,
					doc.State,
					doc.Error,
					hintServerLog,
				)
			}

			return s.announce("backup " + id + " succeeded")
		}

		timer := time.NewTimer(backupPollInterval)

		select {
		case <-pollCtx.Done():
			timer.Stop()

			return fmt.Errorf("%w %s: %w: %s", errWaitBackup, id, pollCtx.Err(), hintServerLog)
		case <-timer.C:
		}
	}
}

// call sends one seed request and decodes its answer into result, which stays
// nil when the answer holds nothing to read. An answer with another status
// names the call, the status and the error text from the body, never the
// token.
func (s *seeder) call(ctx context.Context, call apiCall, result any) error {
	body, err := requestReader(call.body)
	if err != nil {
		return fmt.Errorf("%w: %s: encode request: %w: %s", call.fail, call.name, err, hintServerLog)
	}

	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := s.newRequest(reqCtx, call, body)
	if err != nil {
		return err
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf(
			"%w: %s: send request: %w: check the server is running and run make demo again",
			call.fail,
			call.name,
			err,
		)
	}

	defer func() {
		_ = resp.Body.Close() //nolint:errcheck // A close error after the answer was read changes nothing the seed reports.
	}()

	limit := int64(successBodyLimit)
	if resp.StatusCode != call.want {
		limit = errorBodyLimit
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return fmt.Errorf("%w: %s: read answer: %w: %s", call.fail, call.name, err, hintServerLog)
	}

	if resp.StatusCode != call.want {
		return fmt.Errorf(
			"%w: %s: the server answered %d: %s: %s",
			call.fail,
			call.name,
			resp.StatusCode,
			errorDetail(raw),
			hintServerLog,
		)
	}

	if result == nil {
		return nil
	}

	if err := json.Unmarshal(raw, result); err != nil {
		return fmt.Errorf("%w: %s: decode answer: %w: %s", call.fail, call.name, err, hintServerLog)
	}

	return nil
}

// requestReader answers the request body: no body when body is nil, else the
// JSON form.
func requestReader(body any) (io.Reader, error) {
	if body == nil {
		return http.NoBody, nil
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	return bytes.NewReader(raw), nil
}

// newRequest builds the HTTP request of one seed call.
func (s *seeder) newRequest(ctx context.Context, call apiCall, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, call.method, s.base+call.path, body)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: build request: %w: %s", call.fail, call.name, err, hintServerLog)
	}

	req.Header.Set("Authorization", call.auth)

	if call.body != nil {
		req.Header.Set("Content-Type", contentTypeJSON)
	}

	return req, nil
}

// errorDetail answers the error text of a failed answer: the message of its
// JSON error, else the trimmed body, else a note that it held nothing.
func errorDetail(raw []byte) string {
	var doc apiErrorResponse

	if err := json.Unmarshal(raw, &doc); err == nil && strings.TrimSpace(doc.Message) != "" {
		return strings.TrimSpace(doc.Message)
	}

	if text := strings.TrimSpace(string(raw)); text != "" {
		return text
	}

	return "no answer body"
}
