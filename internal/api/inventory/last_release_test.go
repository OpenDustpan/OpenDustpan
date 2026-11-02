package inventory_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestListUnitsLastRelease proves the unit list shows the last released lock
// of a unit when the unit has a record, and omits the field when it has none,
// as it does for a Pulumi stack and for a workspace that never released a lock.
func TestListUnitsLastRelease(t *testing.T) {
	t.Parallel()

	at := changeTime()

	tests := []struct {
		release *coreinventory.LastRelease
		want    map[string]any
		name    string
		tool    string
	}{
		{
			name: "a normal release",
			tool: "opentofu",
			release: &coreinventory.LastRelease{
				At: at, LockID: "lock-1", Holder: "alice", By: "alice", Kind: "normal",
			},
			want: map[string]any{
				"lockId": "lock-1", "holder": "alice", "releasedBy": "alice",
				"releasedAt": at.UTC().Format(time.RFC3339Nano), "kind": "normal",
			},
		},
		{
			name: "a forced release",
			tool: "opentofu",
			release: &coreinventory.LastRelease{
				At: at, LockID: "lock-2", Holder: "alice", By: "admin", Kind: "forced",
			},
			want: map[string]any{
				"lockId": "lock-2", "holder": "alice", "releasedBy": "admin",
				"releasedAt": at.UTC().Format(time.RFC3339Nano), "kind": "forced",
			},
		},
		{name: "a workspace that never released a lock", tool: "opentofu", release: nil, want: nil},
		{name: "a Pulumi stack", tool: "pulumi", release: nil, want: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
			seedNamespace(t, nsStore)

			ns := namespace.Name("acme")
			reader := &memReader{
				entries: map[namespace.Name][]coreinventory.Entry{
					ns: {
						{
							ManualLock:    nil,
							Collection:    "",
							LastChanged:   at,
							Namespace:     ns,
							Tool:          tc.tool,
							Name:          "network",
							UnitID:        "u-1",
							DisplayName:   "network",
							Status:        "",
							ResourceCount: nil,
							Lock:          nil,
							LastRelease:   tc.release,
							Labels:        nil,
						},
					},
				},
				err: nil,
			}

			handler := newBehaviorHandler(t, nsStore, reader, selectiveAuthorizer{deny: map[identity.Action]bool{}})

			rec := authorizedGet(t, handler, "/api/dustpan/v1/namespaces/acme/units", "test-token")
			if rec.Code != http.StatusOK {
				t.Fatalf("list answers %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
			}

			var doc struct {
				Units []map[string]any `json:"units"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
				t.Fatalf("decode units: %v", err)
			}

			if len(doc.Units) != 1 {
				t.Fatalf("units = %s, want one", rec.Body.String())
			}

			got, present := doc.Units[0]["lastRelease"]
			if tc.want == nil {
				if present {
					t.Fatalf("unit shows lastRelease %v, want the field omitted", got)
				}

				return
			}

			object, isObject := got.(map[string]any)
			if !isObject || len(object) != len(tc.want) {
				t.Fatalf("lastRelease = %v, want %v", got, tc.want)
			}

			for key, want := range tc.want {
				if object[key] != want {
					t.Fatalf("lastRelease.%s = %v, want %v", key, object[key], want)
				}
			}
		})
	}
}
