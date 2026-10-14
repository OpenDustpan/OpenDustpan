package pulumi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/pulumi"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// actionLogEntry decodes one structured action log line in tests.
type actionLogEntry struct {
	Level     string `json:"level"`
	Message   string `json:"msg"`
	Action    string `json:"action"`
	Object    string `json:"object"`
	Principal string `json:"principal"`
	Released  string `json:"released"`
}

// loggedUpdateFixture builds an update fixture whose info log goes to buf.
func loggedUpdateFixture(t *testing.T, authz identity.Authorizer, buf *bytes.Buffer) updateFixture {
	t.Helper()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: homedNamespaceStore{}, Now: nil, Region: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	stores := &recordingStackStore{created: nil, renamed: nil, tags: nil, deleted: nil, forced: nil, deleteErr: nil}

	stacks, err := stack.NewService(
		stack.Config{Store: stores, Namespaces: namespaces, Now: nil, State: nil},
	)
	if err != nil {
		t.Fatalf("stack service: %v", err)
	}

	mem := newMemUpdateStore()

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
		Updates:            mem,
		Checkpoints:        mem,
		Events:             mem,
		Namespaces:         namespaces,
		Stacks:             stacks,
		Blobs:              newMemBlob(),
		Now:                nil,
		Logger:             nil,
		MaxCheckpointBytes: 0,
		Journal:            nil,
	})
	if err != nil {
		t.Fatalf("updates service: %v", err)
	}

	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	handler, err := pulumi.NewHandler(pulumi.Config{
		DisablePulumiJournaling: false,
		Stacks:                  stacks,
		Updates:                 updates,
		Secrets:                 newTestSecrets(t, namespaces),
		Namespaces:              namespaces,
		Authenticator:           tokenAuthenticator{},
		Authorizer:              authz,
		Logger:                  logger,
		BaseURL:                 baseurl.Resolver{},
		MaxBodyBytesInFlight:    0,
		Gate:                    nil,
		Audit:                   nil,
		OnAuditFailure:          nil,
		CheckpointDeltaCutoff:   0,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	return updateFixture{handler: handler, updates: updates, ref: ref}
}

// decodeActionLog proves buf holds exactly one log line and returns it.
func decodeActionLog(t *testing.T, buf *bytes.Buffer) actionLogEntry {
	t.Helper()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 || lines[0] == "" {
		t.Fatalf("log holds %d lines, want 1: %q", len(lines), buf.String())
	}

	var entry actionLogEntry

	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("decode log line: %v (%q)", err, lines[0])
	}

	return entry
}

// wantActionLog holds the wanted fields of one action log line.
type wantActionLog struct {
	action    string
	object    string
	principal string
	released  string
}

// checkActionLog proves the entry holds the wanted fields at info level.
func checkActionLog(t *testing.T, entry actionLogEntry, want wantActionLog) {
	t.Helper()

	if entry.Level != slog.LevelInfo.String() {
		t.Fatalf("log level = %q, want %q", entry.Level, slog.LevelInfo.String())
	}

	if entry.Action != want.action || entry.Object != want.object ||
		entry.Principal != want.principal || entry.Released != want.released {
		t.Fatalf(
			"log entry = %+v, want action %q object %q principal %q released %q",
			entry,
			want.action,
			want.object,
			want.principal,
			want.released,
		)
	}
}

// TestCancelLogsAction proves a cancel writes one info line with the action,
// the unit, the principal and the freed update ID.
func TestCancelLogsAction(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	f := loggedUpdateFixture(t, allowAll(), &buf)

	id, _ := f.mintStartedMutating(t)
	paths := mintedPaths(id)

	status, body := f.doUpdate(t, accessRequest(http.MethodPost, paths.Cancel, ""))
	if status != http.StatusOK {
		t.Fatalf("cancel answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	checkActionLog(t, decodeActionLog(t, &buf), wantActionLog{
		action:    "cancel",
		object:    "pulumi:acme/web/prod",
		principal: "tester",
		released:  id.String(),
	})
}

// TestPulumiRestoreLogsAction proves a restore writes one info line with the
// action, the unit and the principal and an empty released value.
func TestPulumiRestoreLogsAction(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	f := loggedUpdateFixture(t, allowAll(), &buf)

	seedAccessCheckpoint(t, f)

	status, body := f.doUpdate(t, accessRequest(http.MethodPost, "/api/stacks/acme/web/prod/restore/1", ""))
	if status != http.StatusOK {
		t.Fatalf("restore answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	checkActionLog(t, decodeActionLog(t, &buf), wantActionLog{
		action:    "restore",
		object:    "pulumi:acme/web/prod",
		principal: "tester",
		released:  "",
	})
}

// TestPulumiActionLogHidesQuery proves the action log never holds the query:
// a canary in the query never appears in the output.
func TestPulumiActionLogHidesQuery(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	f := loggedUpdateFixture(t, allowAll(), &buf)

	id, _ := f.mintStartedMutating(t)
	paths := mintedPaths(id)

	const queryCanary = "canary-query-9c1e4a7b2d"

	status, body := f.doUpdate(t, accessRequest(http.MethodPost, paths.Cancel+"?probe="+queryCanary, ""))
	if status != http.StatusOK {
		t.Fatalf("cancel answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	entry := decodeActionLog(t, &buf)
	checkActionLog(t, entry, wantActionLog{
		action:    "cancel",
		object:    "pulumi:acme/web/prod",
		principal: "tester",
		released:  id.String(),
	})

	if strings.Contains(buf.String(), queryCanary) {
		t.Fatalf("log holds canary value %q", queryCanary)
	}
}

// SetOverrides is not used by this fake.
func (homedNamespaceStore) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}
