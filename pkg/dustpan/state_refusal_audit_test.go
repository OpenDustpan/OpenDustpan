package dustpan_test

import (
	"crypto/md5" //nolint:gosec // The protocol under test uses MD5 for integrity.
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestStateBlobRefusalReachesTheServerAuditChain proves the wiring of the
// refusal recorder of the state blobs: a read of state content with a key of
// another namespace is refused with blob.ErrOutOfScope, and the audit chain
// of @server holds one entry of kind security for it, with the namespace of the
// workspace as the object and no blob key in the entry.
func TestStateBlobRefusalReachesTheServerAuditChain(t *testing.T) {
	t.Parallel()

	w := newContextWorld(t)
	svc := w.srv.Workspaces()
	sv := w.pushState(t, svc)

	acme, err := w.store.GetNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("get namespace acme: %v", err)
	}

	other, err := w.store.GetNamespace(t.Context(), "other")
	if err != nil {
		t.Fatalf("get namespace other: %v", err)
	}

	if entries := w.securityEntries(t); len(entries) != 0 {
		t.Fatalf("security entries before the refusal = %d, want 0", len(entries))
	}

	foreign := blob.Key(strings.Replace(sv.Content.String(), acme.ID.String(), other.ID.String(), 1))

	if _, openErr := svc.OpenStateBlob(t.Context(), foreign); !errors.Is(openErr, blob.ErrOutOfScope) {
		t.Fatalf("open a key of another namespace: err = %v, want blob.ErrOutOfScope", openErr)
	}

	entries := w.securityEntries(t)
	if len(entries) != 1 {
		t.Fatalf("security entries = %d, want 1: %+v", len(entries), entries)
	}

	if got := entries[0]; got.Kind != "security" || got.Outcome != "denied" ||
		got.Action != secret.ActionBlobOutOfScope || got.Object != acme.ID.String() {
		t.Errorf("entry = %+v, want a denied security entry for %s", got, acme.ID)
	}

	if text := entries[0].Reason; strings.Contains(text, "namespaces/") || strings.Contains(text, other.ID.String()) {
		t.Errorf("reason %q names a key or the other namespace", text)
	}
}

// pushState locks the workspace of the world as alice and writes one state
// version through svc.
func (w *contextWorld) pushState(t *testing.T, svc *workspace.Service) workspace.StateVersion {
	t.Helper()

	id := workspace.ID(w.workspace)

	const content = `{"version":4,"serial":1,"lineage":"lin-1","outputs":{}}`

	digest := md5.Sum([]byte(content)) //nolint:gosec // The protocol under test uses MD5 for integrity.

	if _, err := svc.Lock(t.Context(), id, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	auth := workspace.Authority{Allow: namespace.Overrides{Forced: false, Unlocked: false}, Record: nil}

	pending, err := svc.BeginState(t.Context(), id, "alice", workspace.NewState{
		Lineage: "lin-1", MD5: hex.EncodeToString(digest[:]), Outputs: nil, Serial: 1, Force: false,
	}, auth)
	if err != nil {
		t.Fatalf("begin state: %v", err)
	}

	sv, err := svc.Upload(t.Context(), pending.Version.ID, pending.Upload, strings.NewReader(content))
	if err != nil {
		t.Fatalf("upload state: %v", err)
	}

	return sv
}

// securityEntryWire is the part of an audit entry that the refusal test reads.
type securityEntryWire struct {
	Kind    string `json:"kind"`
	Action  string `json:"action"`
	Object  string `json:"object"`
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
}

// securityEntries returns the entries of kind security in the audit chain of
// @server, read through the audit API with the administrator token.
func (w *contextWorld) securityEntries(t *testing.T) []securityEntryWire {
	t.Helper()

	code, body := w.call(t, http.MethodGet, "/api/audit/entries?organization=@server&page[size]=100",
		ctxAdminToken, "")
	if code != http.StatusOK {
		t.Fatalf("list @server entries: status = %d: %s", code, body)
	}

	var list struct {
		Entries []securityEntryWire `json:"entries"`
	}

	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode entries %s: %v", body, err)
	}

	var found []securityEntryWire

	for _, e := range list.Entries {
		if e.Kind == "security" {
			found = append(found, e)
		}
	}

	return found
}
