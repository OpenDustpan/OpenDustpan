package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"testing"
)

// currentStateResponse is the part of a current state version answer the
// topology test reads: the URL of the state content.
type currentStateResponse struct {
	Data currentStateData `json:"data"`
}

// currentStateData is the data member of a current state version answer.
type currentStateData struct {
	Attributes currentStateAttributes `json:"attributes"`
}

// currentStateAttributes holds the download URL of the state content.
type currentStateAttributes struct {
	DownloadURL string `json:"hosted-state-download-url"` //nolint:tagliatelle // The TFE wire name; it must not change.
}

// TestInstancesShareStorage proves the two PostgreSQL instances share one
// blob store and one key-encryption key: a state that the first instance
// stores reads back unchanged through the second instance.
func TestInstancesShareStorage(t *testing.T) {
	t.Parallel()

	if os.Getenv(postgresURLEnv) == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run two instances")
	}

	ctx := t.Context()
	ns := "load-topology"

	urls, cleanup, err := openServers(ctx, slog.New(slog.DiscardHandler), t.TempDir(), ns)
	if err != nil {
		t.Fatalf("open servers: %v", err)
	}

	t.Cleanup(func() {
		cleanup(context.WithoutCancel(ctx))
	})

	if len(urls) != 2 {
		t.Fatalf("instances = %d, want 2", len(urls))
	}

	fx := newTarget(urls, harnessToken(), ns)

	id, err := fx.createWorkspace(ctx, "shared-ws")
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	if _, err := fx.lockWorkspace(ctx, id); err != nil {
		t.Fatalf("lock workspace: %v", err)
	}

	state := buildState(smokeCheckpointBytes, 1, "load-"+id)

	if err := fx.uploadState(ctx, id, state, 1); err != nil {
		t.Fatalf("upload state through the first instance: %v", err)
	}

	got := readStateThrough(ctx, t, &fx, 1, id)
	if !bytes.Equal(got, state) {
		t.Fatalf("state read through the second instance differs: %d bytes, want %d", len(got), len(state))
	}
}

// readStateThrough reads the current state of workspace id through
// instance i.
func readStateThrough(ctx context.Context, t *testing.T, fx *target, i int, id string) []byte {
	t.Helper()

	url := fx.urlFor(i, tfeByID+id+"/current-state-version")

	resp, err := fx.do(ctx, request{
		method: http.MethodGet, url: url, auth: tfeScheme + fx.token, mediaType: "", body: nil,
	})
	if err != nil {
		t.Fatalf("read current state version: %v", err)
	}

	if checkErr := check(http.MethodGet, url, resp.status, http.StatusOK, resp.body); checkErr != nil {
		t.Fatalf("read current state version: %v", checkErr)
	}

	var current currentStateResponse
	if decodeErr := json.Unmarshal(resp.body, &current); decodeErr != nil {
		t.Fatalf("decode current state version: %v", decodeErr)
	}

	download := current.Data.Attributes.DownloadURL

	content, err := fx.do(ctx, request{
		method: http.MethodGet, url: download, auth: tfeScheme + fx.token, mediaType: "", body: nil,
	})
	if err != nil {
		t.Fatalf("download state: %v", err)
	}

	if checkErr := check(http.MethodGet, download, content.status, http.StatusOK, content.body); checkErr != nil {
		t.Fatalf("download state through the second instance: %v", checkErr)
	}

	return content.body
}
