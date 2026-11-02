package inventory_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
)

const (
	releasedUnitID = "ws-released"
	freshUnitID    = "ws-fresh"
	// releaseMarker is text that only the last release of the released unit
	// holds, so a body that holds it shows the record.
	releaseMarker = "lock-marker-7"
)

// lastReleaseEntries holds three live units in acme: an OpenTofu workspace
// that released a lock, an OpenTofu workspace that never did, and a Pulumi
// stack.
func lastReleaseEntries() map[namespace.Name][]coreinventory.Entry {
	at := changeTime()

	entry := func(tool, name, id string, release *coreinventory.LastRelease) coreinventory.Entry {
		return coreinventory.Entry{
			LastRelease:   release,
			ManualLock:    nil,
			LastChanged:   at,
			Namespace:     "acme",
			Tool:          tool,
			Name:          name,
			UnitID:        id,
			DisplayName:   name,
			Status:        "",
			ResourceCount: nil,
			Lock:          nil,
			Collection:    "",
			Labels:        nil,
		}
	}

	return map[namespace.Name][]coreinventory.Entry{
		"acme": {
			entry("opentofu", "released", releasedUnitID, &coreinventory.LastRelease{
				At: at.Add(time.Minute), LockID: releaseMarker, Holder: "alice", By: "admin", Kind: "forced",
			}),
			entry("opentofu", "fresh", freshUnitID, nil),
			entry("pulumi", "web/prod", acmeUnitID, nil),
		},
	}
}

// listBody returns the status and body of the unit list of acme.
func listBody(t *testing.T, h http.Handler) (int, string) {
	t.Helper()

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/api/dustpan/v1/namespaces/acme/units", http.NoBody,
	)
	req.Header.Set("Authorization", "token test-token")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec.Code, rec.Body.String()
}

// TestLastReleaseNeedsUnitRead proves that a caller who cannot read a unit
// gets no last release of it, in the list and in the read by unit ID. The
// callers: no grant, a grant on another unit, and a grant on the unit.
func TestLastReleaseNeedsUnitRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		allow       map[string]bool
		name        string
		wantRecord  bool
		wantListSee []string
	}{
		{
			name:        "no grant",
			allow:       map[string]bool{},
			wantRecord:  false,
			wantListSee: nil,
		},
		{
			name: "a grant on another unit",
			allow: map[string]bool{
				grant(identity.ActionNamespaceRead, "acme"):           true,
				grant(identity.ActionUnitRead, "opentofu:acme/fresh"): true,
			},
			wantRecord:  false,
			wantListSee: []string{freshUnitID},
		},
		{
			name: "a grant on the unit",
			allow: map[string]bool{
				grant(identity.ActionNamespaceRead, "acme"):              true,
				grant(identity.ActionUnitRead, "opentofu:acme/released"): true,
			},
			wantRecord:  true,
			wantListSee: []string{releasedUnitID},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := newUnitHandlerWithEntries(t, grantAuthorizer{allow: tc.allow}, lastReleaseEntries())

			_, list := listBody(t, handler)
			if got := strings.Contains(list, releaseMarker); got != tc.wantRecord {
				t.Fatalf("list shows the last release = %v, want %v (%s)", got, tc.wantRecord, list)
			}

			for _, id := range tc.wantListSee {
				if !strings.Contains(list, id) {
					t.Fatalf("list does not hold unit %s (%s)", id, list)
				}
			}

			rec := getUnitByID(t, handler, releasedUnitID, "test-token")
			if got := strings.Contains(rec.Body.String(), releaseMarker); got != tc.wantRecord {
				t.Fatalf("unit view shows the last release = %v, want %v (%s)", got, tc.wantRecord, rec.Body.String())
			}

			wantStatus := http.StatusNotFound
			if tc.wantRecord {
				wantStatus = http.StatusOK
			}

			if rec.Code != wantStatus {
				t.Fatalf("unit view status = %d, want %d (%s)", rec.Code, wantStatus, rec.Body.String())
			}
		})
	}
}

// TestGetUnitByIDLastRelease proves that the unit view shows the last release
// of a workspace that released a lock, and omits the field for a workspace
// that never did and for a Pulumi stack.
func TestGetUnitByIDLastRelease(t *testing.T) {
	t.Parallel()

	allow := grantAuthorizer{allow: map[string]bool{
		grant(identity.ActionNamespaceRead, "acme"):              true,
		grant(identity.ActionUnitRead, "opentofu:acme/released"): true,
		grant(identity.ActionUnitRead, "opentofu:acme/fresh"):    true,
		grant(identity.ActionUnitRead, "pulumi:acme/web/prod"):   true,
	}}

	released := map[string]any{
		"id": releaseMarker, "holder": "alice", "releasedBy": "admin",
		"releasedAt": changeTime().Add(time.Minute).UTC().Format(time.RFC3339Nano), "kind": "forced",
	}

	tests := []struct {
		want map[string]any
		name string
		id   string
	}{
		{name: "a workspace that released a lock", id: releasedUnitID, want: released},
		{name: "a workspace that never released a lock", id: freshUnitID, want: nil},
		{name: "a Pulumi stack", id: acmeUnitID, want: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := newUnitHandlerWithEntries(t, allow, lastReleaseEntries())

			rec := getUnitByID(t, handler, tc.id, "test-token")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
			}

			got, present := decodeObject(t, rec)["lastRelease"]
			if tc.want == nil {
				if present {
					t.Fatalf("unit view shows lastRelease %v, want the field omitted", got)
				}

				return
			}

			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("encode lastRelease: %v", err)
			}

			want, err := json.Marshal(tc.want)
			if err != nil {
				t.Fatalf("encode want: %v", err)
			}

			if string(encoded) != string(want) {
				t.Fatalf("lastRelease = %s, want %s", encoded, want)
			}
		})
	}
}

// TestListAndUnitViewAgreeOnLastRelease proves the list and the unit view show
// the same lastRelease object for a repeated read of the same unit.
func TestListAndUnitViewAgreeOnLastRelease(t *testing.T) {
	t.Parallel()

	allow := grantAuthorizer{allow: map[string]bool{
		grant(identity.ActionNamespaceRead, "acme"):              true,
		grant(identity.ActionUnitRead, "opentofu:acme/released"): true,
	}}
	handler := newUnitHandlerWithEntries(t, allow, lastReleaseEntries())

	for range 2 {
		_, list := listBody(t, handler)

		var doc struct {
			Units []map[string]any `json:"units"`
		}
		if err := json.Unmarshal([]byte(list), &doc); err != nil || len(doc.Units) != 1 {
			t.Fatalf("list = %q, err %v: want one unit", list, err)
		}

		view := decodeObject(t, getUnitByID(t, handler, releasedUnitID, "test-token"))

		a, _ := json.Marshal(doc.Units[0]["lastRelease"])
		b, _ := json.Marshal(view["lastRelease"])

		if string(a) != string(b) || string(a) == "null" {
			t.Fatalf("list lastRelease = %s, unit view lastRelease = %s: want equal and set", a, b)
		}
	}
}
