package servertest_test

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/servertest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// recordInMessage finds the ID of the decision record that a refusal names.
var recordInMessage = regexp.MustCompile(`decision record (gdr-[A-Za-z0-9_-]+)`)

// rmGateText is the message of the deny gate of the stack rm test.
const rmGateText = "Stack removal is frozen for the test."

// rmGateEnv is a server with the namespace acme and the stack web/dev, and the
// store handle that writes the gate configuration of the stack.
type rmGateEnv struct {
	t     *testing.T
	c     *servertest.Cluster
	store *postgres.Store
	fence namespace.Fence
	scope gate.Scope
}

func newRMGateEnv(t *testing.T) rmGateEnv {
	t.Helper()

	c := servertest.Start(t, 1)
	send(t, c,
		request{
			server: 0, method: http.MethodPost, path: "/api/server/namespaces", body: `{"name":"acme"}`,
			want: http.StatusCreated,
		},
		request{
			server: 0, method: http.MethodPost, path: "/api/stacks/acme/web", body: `{"stackName":"dev"}`,
			want: http.StatusOK,
		},
	)

	st := c.Store(t, 0)

	n, err := st.GetNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("namespace acme: %v", err)
	}

	scope, err := gate.UnitScope("acme", "pulumi", "web/dev")
	if err != nil {
		t.Fatalf("unit scope: %v", err)
	}

	return rmGateEnv{t: t, c: c, store: st, fence: n.Fence(), scope: scope}
}

// configEntry is the audit entry of a change of the gate configuration.
func configEntry() audit.Entry {
	return audit.Entry{
		At: time.Now(), Namespace: "acme", Subject: "root", IDPSubject: "", TokenID: "",
		Action: "namespace:manage-gates", Object: "acme", Version: "", ValuePath: "", ValueCount: nil,
		Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}
}

// putDenyGate saves an enforced gate of the stack that denies its delete.
func (e rmGateEnv) putDenyGate() gate.ObjectRef {
	e.t.Helper()

	g := gate.Gate{
		Scope: e.scope, ID: "no-rm", Version: 0, DecisionPoint: gate.PointStateWrite,
		Condition: `context.operation.kind == "delete"`, Unless: "", Outcome: gate.OutcomeDeny, Message: rmGateText,
		Mode: gate.ModeEnforce, OnError: gate.OnErrorFail, Final: false,
		CheckedWith: gate.CheckedWith{Engine: "", Schema: gate.SchemaVersion{Subject: "", Major: 0, Minor: 0}},
		State:       "", Reads: nil,
	}

	v, err := e.store.Gates().Put(e.t.Context(), g, 0, e.fence, configEntry())
	if err != nil {
		e.t.Fatalf("put gate: %v", err)
	}

	return gate.ObjectRef{Scope: e.scope, ID: g.ID, Version: v}
}

// putLimit saves an access limit of the stack that forbids unit:manage, the
// action of the stack rm.
func (e rmGateEnv) putLimit() gate.ObjectRef {
	e.t.Helper()

	l := gate.AccessLimit{
		Scope: e.scope, ID: "no-manage", Version: 0,
		Policy: `forbid (principal, action == OpenDustpan::Action::"unit:manage", resource);`,
	}

	v, err := e.store.AccessLimits().Put(e.t.Context(), l, 0, e.fence, configEntry())
	if err != nil {
		e.t.Fatalf("put access limit: %v", err)
	}

	return gate.ObjectRef{Scope: e.scope, ID: l.ID, Version: v}
}

// refusedRM sends the stack rm, and checks that it answers 403 with code and
// the ID of a decision record. It returns that ID.
func (e rmGateEnv) refusedRM(code string) gate.RecordID {
	e.t.Helper()

	status, body := e.c.Do(e.t, 0, http.MethodDelete, "/api/stacks/acme/web/dev", "")

	var refused struct {
		Code      int    `json:"code"`
		Message   string `json:"message"`
		ErrorCode string `json:"errorCode"`
	}

	if err := json.Unmarshal([]byte(body), &refused); err != nil || status != http.StatusForbidden ||
		refused.Code != http.StatusForbidden || refused.ErrorCode != code {
		e.t.Fatalf("DELETE stack = %d %s (%v), want 403 with the code %s", status, body, err, code)
	}

	m := recordInMessage.FindStringSubmatch(refused.Message)
	if m == nil {
		e.t.Fatalf("refusal %q names no decision record", refused.Message)
	}

	// The stack is still there: the refusal stopped the delete.
	send(e.t, e.c, request{
		server: 0, method: http.MethodGet, path: "/api/stacks/acme/web/dev", body: "", want: http.StatusOK,
	})

	return gate.RecordID(m[1])
}

// checkRecord checks that the decision record id is stored with the result
// want, and that the audit log holds the denial of the stack rm with code.
func (e rmGateEnv) checkRecord(id gate.RecordID, want gate.Result, code string) {
	e.t.Helper()

	rec, err := e.store.Records().Get(e.t.Context(), "acme", id)
	if err != nil || rec.Result != want {
		e.t.Fatalf("decision record %s = %+v, %v, want a stored record with the result %s", id, rec, err, want)
	}

	listing, err := e.store.List(e.t.Context(), "acme", audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		e.t.Fatalf("audit entries: %v", err)
	}

	for i := range listing.Entries {
		entry := &listing.Entries[i]
		if entry.Outcome == audit.OutcomeDenied && entry.Action == "unit:manage" &&
			entry.Object == "pulumi:acme/web/dev" &&
			strings.HasPrefix(entry.Reason, "state.write refused with "+code+" ") {
			return
		}
	}

	e.t.Fatalf("the audit log holds no denial of the stack rm with the code %s", code)
}

// TestPulumiStackRMMeetsTheGateAndTheAccessLimit proves, on a real server,
// that a deny gate and an access limit each refuse a Pulumi stack rm on their
// own, that each refusal writes its decision record and an audit entry, and
// that the stack rm goes on when the gate or the limit is removed. Each case
// has its own server: the audit log holds back a second denial of the same
// principal and action in a namespace for a time.
func TestPulumiStackRMMeetsTheGateAndTheAccessLimit(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		code  string
		setUp func(e rmGateEnv) func()
	}{
		{"deny gate", "gate.denied", func(e rmGateEnv) func() {
			ref := e.putDenyGate()

			return func() {
				err := e.store.Gates().Delete(e.t.Context(), ref, e.fence, configEntry())
				if err != nil {
					e.t.Fatalf("delete gate: %v", err)
				}
			}
		}},
		{"access limit", "gate.limited", func(e rmGateEnv) func() {
			ref := e.putLimit()

			return func() {
				err := e.store.AccessLimits().Delete(e.t.Context(), ref, e.fence, configEntry())
				if err != nil {
					e.t.Fatalf("delete access limit: %v", err)
				}
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newRMGateEnv(t)
			remove := tc.setUp(e)

			e.checkRecord(e.refusedRM(tc.code), gate.ResultDeny, tc.code)

			remove()
			send(t, e.c, request{
				server: 0, method: http.MethodDelete, path: "/api/stacks/acme/web/dev", body: "",
				want: http.StatusNoContent,
			})
		})
	}
}
