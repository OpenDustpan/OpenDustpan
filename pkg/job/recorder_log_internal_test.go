package job

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/blob/blobfault"
	"opendustpan.org/opendustpan/internal/scopeaudit"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

var errInjectedPartDelete = errors.New("injected plan part delete failure")

// noRefusalsInternal drops every refusal of the artifact blobs.
type noRefusalsInternal struct{}

func (noRefusalsInternal) RecordOutOfScope(context.Context, namespace.ID, string, scopeaudit.Where) error {
	return nil
}

// TestDeletePartsLogsAFailedDelete proves that the recorder logs a warning for
// each plan part that it did not store and could not delete, and logs nothing
// for a delete that succeeds. The logger is injected through the recorder.
func TestDeletePartsLogsAFailedDelete(t *testing.T) {
	t.Parallel()

	failing := []blobfault.Fault{{
		Method: "Delete", Call: 0, Kind: blobfault.KindFail, Err: errInjectedPartDelete, Delay: 0,
	}}

	tests := []struct {
		name     string
		faults   []blobfault.Fault
		wantLogs bool
	}{
		{name: "a failed delete logs a warning", faults: failing, wantLogs: true},
		{name: "a successful delete logs nothing", faults: nil, wantLogs: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out := runDeletePartsWithLog(t, tc.faults)

			if got := strings.Contains(out, "level=WARN"); got != tc.wantLogs {
				t.Fatalf("warning logged = %t, want %t: %q", got, tc.wantLogs, out)
			}

			if tc.wantLogs && !strings.Contains(out, "kind=plan-apply") {
				t.Errorf("warning does not name the part: %q", out)
			}
		})
	}
}

// runDeletePartsWithLog deletes one plan part that the recorder did not store,
// through a blob store that applies faults. It returns the text that the
// injected logger wrote.
func runDeletePartsWithLog(t *testing.T, faults []blobfault.Fault) string {
	t.Helper()

	inner, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() { _ = inner.Close() }) //nolint:errcheck // Test cleanup.

	store, err := blobfault.New(inner, faults...)
	if err != nil {
		t.Fatalf("blobfault: %v", err)
	}

	artifacts, err := NewArtifactBlobs(store, noRefusalsInternal{})
	if err != nil {
		t.Fatalf("NewArtifactBlobs: %v", err)
	}

	var buf bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelWarn, AddSource: false, ReplaceAttr: nil,
	}))

	r := &DurableRecorder{
		store: nil, jobs: nil, blobs: artifacts, sealer: nil, namespaces: nil,
		retention: history.RetentionSettings{Plan: 0, JobLog: 0},
		now:       nil, log: logger,
	}

	ns := namespace.NewID()
	at := &planAt{
		ns:   ns,
		job:  "job-1",
		plan: "pln-1",
		item: secret.ItemRef{Scope: secret.Scope{Namespace: "", ID: ""}, Kind: "", Item: ""},
	}
	part := PlanPart{
		Kind: OutputPlanApply, Size: 0, SHA256: "", Secret: false, StoredSize: 0, StoredMD5: "",
	}

	r.deleteParts(t.Context(), at, []PlanPart{part})

	return buf.String()
}
