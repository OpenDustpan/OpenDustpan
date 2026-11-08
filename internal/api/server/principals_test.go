package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/gate/limitgroups"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

// principalDoc, groupDoc and memberDoc decode the wire forms in tests.
type principalDoc struct {
	ID          string `json:"id"`
	Subject     string `json:"subject"`
	DisplayName string `json:"displayName"`
	Kind        string `json:"kind"`
	Source      string `json:"source"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
	Namespace   string `json:"namespace"`
	GroupCount  int64  `json:"groupCount"`
}

type groupDoc struct {
	ID                       string `json:"id"`
	Name                     string `json:"name"`
	Description              string `json:"description"`
	Source                   string `json:"source"`
	MemberCount              int64  `json:"memberCount"`
	ClosedToIdentityProvider bool   `json:"closedToIdentityProvider"`
	Protected                bool   `json:"protected"`
}

type memberDoc struct {
	AddedAt   string       `json:"addedAt"`
	Principal principalDoc `json:"principal"`
}

// newTestPrincipals returns a principal service over a fresh, empty SQLite
// store that the test closes at cleanup.
func newTestPrincipals(t *testing.T) *principal.Service {
	t.Helper()

	svc, _ := newTestPrincipalStore(t)

	return svc
}

// newTestPrincipalStore returns a principal service and its fresh, empty
// SQLite store, which also keeps the audit entries.
func newTestPrincipalStore(t *testing.T) (*principal.Service, *sqlite.Store) {
	t.Helper()

	st := openServerTemplateStore(t, filepath.Join(t.TempDir(), "dustpan.db"))

	principals, err := principal.NewService(
		principal.Config{
			TempAdmins: nil,
			Store:      st,
			Guard:      allowAllGuard{},
			GroupReach: allowAllReach{},
			Namespaces: nil,
			Tokens:     nil,
			Now:        nil,
			NewID:      nil,
		},
	)
	if err != nil {
		t.Fatalf("principal service: %v", err)
	}

	principalSessions.Store(principals, newStoreSessions(t, st))

	return principals, st
}

// principalSessions maps a principal service of a test to the console
// sessions over its store. A change of group membership needs a fresh
// authentication, which an access token never gives.
//
//nolint:gochecknoglobals // A test-only registry: the service has no field for its sessions.
var principalSessions sync.Map

// newLimitedPrincipalStore returns a principal service whose guard refuses a
// change that can lift an access limit of a scope that names the group, the
// store, and the namespace acme, whose limits a test seeds.
func newLimitedPrincipalStore(t *testing.T) (*principal.Service, *sqlite.Store) {
	t.Helper()

	st := openServerTemplateStore(t, filepath.Join(t.TempDir(), "dustpan.db"))

	err := st.CreateNamespace(t.Context(), namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.Unix(0, 0), Name: "acme", PrimaryInstallation: "acme",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	})
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	authz, err := access.NewAuthorizer(access.AuthorizerConfig{
		Store: st, Callers: st, Collections: stubCollections{}, Units: st, Principals: st,
	})
	if err != nil {
		t.Fatalf("authorizer: %v", err)
	}

	grants, err := access.NewService(access.ServiceConfig{
		Store: st, Directory: st, Namespaces: st, Authorizer: authz, Now: nil, NewID: nil,
		BootstrapAdmins: []string{"admin"}, LimitGroups: limitgroups.New(st, st),
		Reach: access.ReachConfig{
			Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
		},
	})
	if err != nil {
		t.Fatalf("access service: %v", err)
	}

	principals, err := principal.NewService(
		principal.Config{
			TempAdmins: nil,
			Store:      st,
			Guard:      grants,
			Namespaces: nil,
			Tokens:     nil,
			GroupReach: allowAllReach{},
			Now:        nil,
			NewID:      nil,
		},
	)
	if err != nil {
		t.Fatalf("principal service: %v", err)
	}

	principalSessions.Store(principals, newStoreSessions(t, st))

	return principals, st
}

// seedNamingLimit stores a limit of the namespace acme that names the group
// name in an unless clause: an add of a member can lift it.
func seedNamingLimit(t *testing.T, st *sqlite.Store, name string) {
	t.Helper()

	fence, e := storetest.GrantWrite(t, st, identity.NamespaceObject("acme"), time.Unix(0, 0), "access-limit:l1")

	_, err := st.AccessLimits().Put(t.Context(), gate.AccessLimit{
		Scope: gate.NamespaceScope("acme"), ID: "l1", Version: 0,
		Policy: `forbid (principal, action, resource) unless { principal in OpenDustpan::Group::"` + name + `" };`,
	}, 0, fence, e)
	if err != nil {
		t.Fatalf("put access limit: %v", err)
	}
}

// newPrincipalHandler builds a Handler with principals as its principal
// service. Nil principals means a store that keeps none.
func newPrincipalHandler(t *testing.T, principals *principal.Service) testHandler {
	t.Helper()

	return newAuditedPrincipalHandler(t, principals, nil)
}

// newAuditedPrincipalHandler builds a Handler with principals as its
// principal service and entries as its audit store of the scope @server. The
// reach config of the group routes reads the store of the sessions of
// principals, when the test registered them.
func newAuditedPrincipalHandler(
	t *testing.T, principals *principal.Service, entries audit.ServerStore,
) testHandler {
	t.Helper()

	return newReachPrincipalHandler(t, principals, entries, reachConfigOf(principals))
}

// reachConfigOf returns the reach config over the store of the sessions of
// principals, or the zero config when the test registered no sessions for
// it.
func reachConfigOf(principals *principal.Service) access.ReachConfig {
	sessions, hasSessions := principalSessions.Load(principals)
	stored, isSessions := sessions.(storeSessions)

	if !hasSessions || !isSessions {
		return access.ReachConfig{
			Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
		}
	}

	return access.ReachConfig{
		Grants: stored.st, Roles: stored.st, Callers: stored.st, Principals: stored.st,
		Collections: nil, Units: stored.st,
	}
}

// newReachPrincipalHandler builds a Handler like newAuditedPrincipalHandler
// with reach as the reach config of the group routes, so a test can make the
// check fail. The handler discards its log records.
func newReachPrincipalHandler(
	t *testing.T, principals *principal.Service, entries audit.ServerStore, reach access.ReachConfig,
) testHandler {
	t.Helper()

	return newReachPrincipalHandlerWithLogger(t, principals, entries, reach, slog.New(slog.DiscardHandler))
}

// newReachPrincipalHandlerWithLogger builds a Handler like
// newReachPrincipalHandler with logger as its logger, so a test can read the
// records.
func newReachPrincipalHandlerWithLogger(
	t *testing.T, principals *principal.Service, entries audit.ServerStore, reach access.ReachConfig,
	logger *slog.Logger,
) testHandler {
	t.Helper()

	return newReachPrincipalHandlerWith(t, principals, entries, reach, principalHandlerOptions{
		now:            nil,
		authenticator:  tokenAuthenticator{},
		logger:         logger,
		onAuditFailure: nil,
	})
}

// principalHandlerOptions holds the parts of a principal handler that a test
// can set.
type principalHandlerOptions struct {
	now            func() time.Time
	authenticator  identity.Authenticator
	logger         *slog.Logger
	onAuditFailure func()
}

// newReachPrincipalHandlerWith builds a Handler like newReachPrincipalHandler
// with the options of the test.
func newReachPrincipalHandlerWith(
	t *testing.T, principals *principal.Service, entries audit.ServerStore, reach access.ReachConfig,
	opts principalHandlerOptions,
) testHandler {
	t.Helper()

	if entries == nil {
		entries = newTestAudit()
	}

	if opts.authenticator == nil {
		opts.authenticator = tokenAuthenticator{}
	}

	if opts.logger == nil {
		opts.logger = slog.New(slog.DiscardHandler)
	}

	var signIn *apiserver.SignIn

	sessions, hasSessions := principalSessions.Load(principals)
	if stored, isSessions := sessions.(storeSessions); hasSessions && isSessions {
		signIn = stored.signIn()
	}

	handler, err := newCheckedHandler(t, &apiserver.Config{
		LocalUsers:       nil,
		Preferences:      nil,
		GitHostLinks:     nil,
		Settings:         nil,
		GitHost:          nil,
		Idempotency:      nil,
		Workers:          nil,
		InstallationKind: "",
		StateWriteGates:  stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		Now:              opts.now,
		TempAdmins:       nil,
		Authenticator:    opts.authenticator,
		Authorizer:       selectiveAuthorizer{deny: map[identity.Action]bool{}},
		Logger:           opts.logger,
		Namespaces:       newTestNamespaces(t),
		Backups:          nil,
		Restores:         nil,
		Maintenance:      newFakeMaintenance(maintenance.StateNormal),
		Metrics:          stubMetrics{},
		Version:          "",
		Schema:           stubSchema{migrations: nil},
		Journal:          nil,
		Principals:       principals,
		Access:           nil,
		Reach:            reach,
		Tokens:           nil,
		OnAuditFailure:   opts.onAuditFailure,
		DeletedUnits:     nil,
		AuditChain:       nil,
		AuditSinks:       nil,
		IPAllowlists:     nil,
		Gates:            nil,
		SessionPolicy:    nil,
		Audit:            entries,
		SignIn:           signIn,
		CheckpointEpochs: nil,
		DenialLimiter:    nil,
		Instances:        nil,
		StoreChecks:      nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if stored, isSessions := sessions.(storeSessions); hasSessions && isSessions {
		rememberSessions(handler, stored)
	}

	return handler
}

// apiCall is one request of a principal test and the status it must answer.
type apiCall struct {
	method string
	path   string
	body   string
	want   int
}

// call sends one request with the test token and returns its status and
// body.
func call(t *testing.T, handler testHandler, c apiCall) (int, string) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), c.method, c.path, strings.NewReader(c.body))

	if _, hasSessions := handlerSessions.Load(handler.Handler); hasSessions && needsFreshSession(c.method, c.path) {
		// An access token never gives the fresh authentication that a change
		// of group membership needs.
		startSession(t, handler, req, 0)
	} else {
		req.Header.Set("Authorization", "Bearer test-token")
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec.Code, rec.Body.String()
}

// mustCall sends c and fails unless it answers c.want. It decodes a JSON
// answer into out when out is not nil.
func mustCall(t *testing.T, handler testHandler, c apiCall, out any) {
	t.Helper()

	status, got := call(t, handler, c)
	if status != c.want {
		t.Fatalf("%s %s answers %d, want %d (%s)", c.method, c.path, status, c.want, got)
	}

	if out == nil {
		return
	}

	if err := json.Unmarshal([]byte(got), out); err != nil {
		t.Fatalf("decode %s %s answer %q: %v", c.method, c.path, got, err)
	}
}

// memberListDoc, groupListDoc and principalListDoc decode the lists.
type memberListDoc struct {
	Members []memberDoc `json:"members"`
}

type groupListDoc struct {
	Groups []groupDoc `json:"groups"`
}

type principalListDoc struct {
	Principals []principalDoc `json:"principals"`
}

// TestPrincipalLifeCycle creates a person and a service, puts both in a
// group, changes one, takes it out and deletes the group, then
// the principal.
func TestPrincipalLifeCycle(t *testing.T) {
	t.Parallel()

	h := newPrincipalHandler(t, newTestPrincipals(t))

	created := createLifeCyclePrincipals(t, h)
	alice, deploy := created.alice, created.deploy

	var group groupDoc
	mustCall(t, h, apiCall{
		http.MethodPost, "/api/server/groups", `{"name":"Ops","description":"Runs it"}`,
		http.StatusCreated,
	}, &group)

	addLifeCycleMembers(t, h, group, alice, deploy)
	changeLifeCyclePrincipal(t, h, group, alice)

	members := "/api/server/groups/" + group.ID + "/members/" + alice.ID
	mustCall(t, h, apiCall{http.MethodDelete, members, "", http.StatusNoContent}, nil)
	mustCall(t, h, apiCall{http.MethodDelete, members, "", http.StatusNotFound}, nil)

	var renamed groupDoc
	mustCall(t, h, apiCall{
		http.MethodPatch, "/api/server/groups/" + group.ID, `{"name":"operations"}`,
		http.StatusOK,
	}, &renamed)

	if renamed.Name != "operations" || renamed.Description != "Runs it" {
		t.Fatalf("renamed group = %+v", renamed)
	}

	deleteLifeCycleRecords(t, h, group, alice, deploy)
}

// createLifeCyclePrincipals creates a person and a service.
func createLifeCyclePrincipals(t *testing.T, h testHandler) lifeCyclePrincipals {
	t.Helper()

	var alice, deploy principalDoc

	mustCall(t, h, apiCall{
		http.MethodPost, "/api/server/principals",
		`{"subject":"alice@example.com","displayName":"Alice","kind":"person"}`, http.StatusCreated,
	}, &alice)
	mustCall(t, h, apiCall{
		http.MethodPost, "/api/server/principals",
		`{"subject":"ci-deploy","kind":"service"}`, http.StatusCreated,
	}, &deploy)

	if !strings.HasPrefix(alice.ID, "prn-") || alice.Kind != "person" {
		t.Fatalf("created principal = %+v", alice)
	}

	if deploy.DisplayName != "ci-deploy" {
		t.Fatalf("display name = %q, want the subject when the body names none", deploy.DisplayName)
	}

	return lifeCyclePrincipals{alice: alice, deploy: deploy}
}

// lifeCyclePrincipals holds the person and the service of the life cycle.
type lifeCyclePrincipals struct {
	alice  principalDoc
	deploy principalDoc
}

// addLifeCycleMembers puts both principals in group and reads the members.
func addLifeCycleMembers(t *testing.T, h testHandler, group groupDoc, alice, deploy principalDoc) {
	t.Helper()

	path := "/api/server/groups/" + group.ID + "/members"

	for _, id := range []string{alice.ID, deploy.ID} {
		var m memberDoc
		mustCall(t, h, apiCall{http.MethodPost, path, `{"principalID":"` + id + `"}`, http.StatusCreated}, &m)

		if m.Principal.ID != id || m.AddedAt == "" {
			t.Fatalf("added member = %+v, want %s", m, id)
		}
	}

	var members memberListDoc
	mustCall(t, h, apiCall{http.MethodGet, path, "", http.StatusOK}, &members)

	if len(members.Members) != 2 || members.Members[0].Principal.Subject != "alice@example.com" {
		t.Fatalf("members = %+v, want alice and ci-deploy", members.Members)
	}
}

// changeLifeCyclePrincipal renames alice and reads her groups.
func changeLifeCyclePrincipal(t *testing.T, h testHandler, group groupDoc, alice principalDoc) {
	t.Helper()

	var changed principalDoc
	mustCall(t, h, apiCall{
		http.MethodPatch, "/api/server/principals/" + alice.ID,
		`{"displayName":"Alice A."}`, http.StatusOK,
	}, &changed)

	if changed.DisplayName != "Alice A." || changed.Subject != "alice@example.com" {
		t.Fatalf("changed principal = %+v", changed)
	}

	var groups groupListDoc
	mustCall(t, h, apiCall{http.MethodGet, "/api/server/principals/" + alice.ID + "/groups", "", http.StatusOK},
		&groups)

	if len(groups.Groups) != 1 || groups.Groups[0].ID != group.ID {
		t.Fatalf("groups of alice = %+v, want %s", groups.Groups, group.ID)
	}
}

// deleteLifeCycleRecords deletes the group, then alice, and lists the rest.
func deleteLifeCycleRecords(t *testing.T, h testHandler, group groupDoc, alice, deploy principalDoc) {
	t.Helper()

	mustCall(t, h, apiCall{http.MethodDelete, "/api/server/groups/" + group.ID, "", http.StatusNoContent}, nil)
	mustCall(t, h, apiCall{http.MethodGet, "/api/server/groups/" + group.ID, "", http.StatusNotFound}, nil)
	mustCall(t, h, apiCall{http.MethodGet, "/api/server/principals/" + deploy.ID, "", http.StatusOK}, nil)
	mustCall(t, h, apiCall{http.MethodDelete, "/api/server/principals/" + alice.ID, "", http.StatusNoContent}, nil)
	mustCall(t, h, apiCall{http.MethodGet, "/api/server/principals/" + alice.ID, "", http.StatusNotFound}, nil)

	var list principalListDoc
	mustCall(t, h, apiCall{http.MethodGet, "/api/server/principals", "", http.StatusOK}, &list)

	// The person of the console session that the membership changes need is
	// the other principal.
	if len(list.Principals) != 2 || list.Principals[0].ID != deploy.ID || list.Principals[1].ID != "prn-tester" {
		t.Fatalf("principals = %+v, want ci-deploy and the session person", list.Principals)
	}
}

// TestGroupIdentityProviderSwitch proves that a group carries the switch
// that closes it to the identity provider: a new group is open, a change
// sets the switch and a read and a list return it, a change clears it, each
// change writes an entry that names the new permission, and a value of
// another type answers 400 with the body error.
func TestGroupIdentityProviderSwitch(t *testing.T) {
	t.Parallel()

	svc, st := newTestPrincipalStore(t)
	h := newAuditedPrincipalHandler(t, svc, st)

	var group groupDoc
	mustCall(t, h, apiCall{http.MethodPost, "/api/server/groups", `{"name":"ops"}`, http.StatusCreated}, &group)

	if group.ClosedToIdentityProvider {
		t.Fatalf("a new group = %+v, want it open to the identity provider", group)
	}

	path := "/api/server/groups/" + group.ID

	var closed groupDoc
	mustCall(t, h, apiCall{
		http.MethodPatch, path, `{"closedToIdentityProvider":true}`, http.StatusOK,
	}, &closed)

	if !closed.ClosedToIdentityProvider || closed.Name != "ops" {
		t.Fatalf("group after the close = %+v, want ops closed", closed)
	}

	var read groupDoc
	mustCall(t, h, apiCall{http.MethodGet, path, "", http.StatusOK}, &read)

	if !read.ClosedToIdentityProvider {
		t.Fatalf("read of the closed group = %+v, want it closed", read)
	}

	var page groupListDoc
	mustCall(t, h, apiCall{http.MethodGet, "/api/server/groups", "", http.StatusOK}, &page)

	if len(page.Groups) != 1 || !page.Groups[0].ClosedToIdentityProvider {
		t.Fatalf("group page = %+v, want one closed group", page.Groups)
	}

	var open groupDoc
	mustCall(t, h, apiCall{
		http.MethodPatch, path, `{"closedToIdentityProvider":false}`, http.StatusOK,
	}, &open)

	if open.ClosedToIdentityProvider {
		t.Fatalf("group after the clear = %+v, want it open", open)
	}

	checkGroupSwitchEntries(t, st, group.ID)

	status, body := call(t, h, apiCall{
		http.MethodPatch, path, `{"closedToIdentityProvider":"true"}`, http.StatusBadRequest,
	})
	if status != http.StatusBadRequest || !strings.Contains(body, "invalid group change body") {
		t.Fatalf("a switch of another type answers %d (%s), want 400 with the body error", status, body)
	}

	if got, err := st.GetGroup(t.Context(), group.ID); err != nil || got.ClosedToIdentityProvider {
		t.Fatalf("stored group after the refused change = %+v, %v; want it open", got, err)
	}
}

// checkGroupSwitchEntries proves that each change of the switch wrote an
// entry for the group that names the new permission of the identity
// provider. The newest entry is the last change.
func checkGroupSwitchEntries(t *testing.T, st *sqlite.Store, groupID string) {
	t.Helper()

	listing, err := st.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list server entries: %v", err)
	}

	entries := listing.Entries
	if len(entries) != 3 {
		t.Fatalf("server entries = %d, want the create and the two changes: %+v", len(entries), entries)
	}

	want := []string{
		"the identity provider may add members: on",
		"the identity provider may add members: off",
	}

	for i, reason := range want {
		e := entries[i]
		if e.Object != "group:"+groupID || e.Action != "server:manage-groups" ||
			e.Kind != audit.KindChange || e.Outcome != audit.OutcomeSuccess || e.Reason != reason {
			t.Fatalf("entry %d = %+v, want a change of the group with the reason %q", i, e, reason)
		}
	}
}

// TestPrincipalRefusals answers 400 for each bad body, subject, kind and
// name, 409 for each taken subject, name and membership, and 404 for each
// unknown ID.
func TestPrincipalRefusals(t *testing.T) {
	t.Parallel()

	h := newPrincipalHandler(t, newTestPrincipals(t))

	var alice principalDoc
	mustCall(t, h, apiCall{
		http.MethodPost, "/api/server/principals", `{"subject":"alice","kind":"person"}`,
		http.StatusCreated,
	}, &alice)

	var group groupDoc
	mustCall(t, h, apiCall{http.MethodPost, "/api/server/groups", `{"name":"ops"}`, http.StatusCreated}, &group)
	mustCall(t, h, apiCall{
		http.MethodPost, "/api/server/groups/" + group.ID + "/members",
		`{"principalID":"` + alice.ID + `"}`, http.StatusCreated,
	}, nil)

	cases := refusalCases(alice.ID, group.ID)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, body := call(t, h, tc.call)
			if status != tc.call.want {
				t.Fatalf("%s %s answers %d, want %d (%s)", tc.call.method, tc.call.path, status, tc.call.want, body)
			}

			var doc struct {
				Message string `json:"message"`
				Code    int    `json:"code"`
			}

			if err := json.Unmarshal([]byte(body), &doc); err != nil || doc.Code != tc.call.want || doc.Message == "" {
				t.Fatalf("error body = %q, want a JSON error with code %d", body, tc.call.want)
			}
		})
	}
}

// refusalCase is one request that the principal routes must refuse.
type refusalCase struct {
	name string
	call apiCall
}

// refusalCases returns the refused requests for the principal aliceID, who
// is a member of the group groupID.
func refusalCases(aliceID, groupID string) []refusalCase {
	one := "/api/server/principals/" + aliceID
	grp := "/api/server/groups/" + groupID
	members := grp + "/members"
	addAlice := `{"principalID":"` + aliceID + `"}`

	return []refusalCase{
		{
			name: "not JSON",
			call: apiCall{
				method: http.MethodPost,
				path:   "/api/server/principals",
				body:   `{`,
				want:   http.StatusBadRequest,
			},
		},
		{
			name: "unknown field",
			call: apiCall{
				method: http.MethodPost,
				path:   "/api/server/principals",
				body:   `{"subject":"b","kind":"person","x":1}`,
				want:   http.StatusBadRequest,
			},
		},
		{
			name: "no kind",
			call: apiCall{
				method: http.MethodPost,
				path:   "/api/server/principals",
				body:   `{"subject":"b"}`,
				want:   http.StatusBadRequest,
			},
		},
		{
			name: "unknown kind",
			call: apiCall{
				method: http.MethodPost,
				path:   "/api/server/principals",
				body:   `{"subject":"b","kind":"robot"}`,
				want:   http.StatusBadRequest,
			},
		},
		{
			name: "empty subject",
			call: apiCall{
				method: http.MethodPost,
				path:   "/api/server/principals",
				body:   `{"subject":"","kind":"person"}`,
				want:   http.StatusBadRequest,
			},
		},
		{
			name: "padded subject",
			call: apiCall{
				method: http.MethodPost,
				path:   "/api/server/principals",
				body:   `{"subject":" b","kind":"person"}`,
				want:   http.StatusBadRequest,
			},
		},
		{
			name: "reserved admin",
			call: apiCall{
				method: http.MethodPost,
				path:   "/api/server/principals",
				body:   `{"subject":"admin","kind":"person"}`,
				want:   http.StatusBadRequest,
			},
		},
		{
			name: "reserved admin-2",
			call: apiCall{
				method: http.MethodPost,
				path:   "/api/server/principals",
				body:   `{"subject":"admin-2","kind":"service"}`,
				want:   http.StatusBadRequest,
			},
		},
		{
			name: "reserved dev person",
			call: apiCall{
				method: http.MethodPost,
				path:   "/api/server/principals",
				body:   `{"subject":"dev:admin","kind":"person"}`,
				want:   http.StatusBadRequest,
			},
		},
		{
			name: "reserved dev prefix, upper case",
			call: apiCall{
				method: http.MethodPost,
				path:   "/api/server/principals",
				body:   `{"subject":"DEV:someone","kind":"person"}`,
				want:   http.StatusBadRequest,
			},
		},
		{
			name: "two objects",
			call: apiCall{
				method: http.MethodPost,
				path:   "/api/server/principals",
				body:   `{"subject":"b","kind":"person"}{}`,
				want:   http.StatusBadRequest,
			},
		},
		{
			name: "taken subject",
			call: apiCall{
				method: http.MethodPost,
				path:   "/api/server/principals",
				body:   `{"subject":"alice","kind":"service"}`,
				want:   http.StatusConflict,
			},
		},
		{
			name: "empty change",
			call: apiCall{method: http.MethodPatch, path: one, body: `{}`, want: http.StatusBadRequest},
		},
		{
			name: "subject change",
			call: apiCall{
				method: http.MethodPatch,
				path:   one,
				body:   `{"subject":"mallory"}`,
				want:   http.StatusBadRequest,
			},
		},
		{
			name: "change of unknown",
			call: apiCall{
				method: http.MethodPatch,
				path:   "/api/server/principals/prn-missing",
				body:   `{"displayName":"x"}`,
				want:   http.StatusNotFound,
			},
		},
		{
			name: "get unknown",
			call: apiCall{
				method: http.MethodGet,
				path:   "/api/server/principals/prn-missing",
				body:   "",
				want:   http.StatusNotFound,
			},
		},
		{
			name: "delete unknown",
			call: apiCall{
				method: http.MethodDelete,
				path:   "/api/server/principals/prn-missing",
				body:   "",
				want:   http.StatusNotFound,
			},
		},
		{
			name: "groups of unknown",
			call: apiCall{
				method: http.MethodGet,
				path:   "/api/server/principals/prn-missing/groups",
				body:   "",
				want:   http.StatusNotFound,
			},
		},
		{
			name: "bad group name",
			call: apiCall{
				method: http.MethodPost,
				path:   "/api/server/groups",
				body:   `{"name":"a b"}`,
				want:   http.StatusBadRequest,
			},
		},
		{
			name: "no group name",
			call: apiCall{
				method: http.MethodPost,
				path:   "/api/server/groups",
				body:   `{"description":"x"}`,
				want:   http.StatusBadRequest,
			},
		},
		{
			name: "taken group name",
			call: apiCall{
				method: http.MethodPost,
				path:   "/api/server/groups",
				body:   `{"name":"OPS"}`,
				want:   http.StatusConflict,
			},
		},
		{
			name: "empty group change",
			call: apiCall{method: http.MethodPatch, path: grp, body: `{}`, want: http.StatusBadRequest},
		},
		{
			name: "group change switch of another type",
			call: apiCall{
				method: http.MethodPatch,
				path:   grp,
				body:   `{"closedToIdentityProvider":"true"}`,
				want:   http.StatusBadRequest,
			},
		},
		{
			name: "group change of unknown",
			call: apiCall{
				method: http.MethodPatch,
				path:   "/api/server/groups/grp-missing",
				body:   `{"name":"x"}`,
				want:   http.StatusNotFound,
			},
		},
		{
			name: "members of unknown",
			call: apiCall{
				method: http.MethodGet,
				path:   "/api/server/groups/grp-missing/members",
				body:   "",
				want:   http.StatusNotFound,
			},
		},
		{
			name: "add to unknown group",
			call: apiCall{
				method: http.MethodPost,
				path:   "/api/server/groups/grp-missing/members",
				body:   addAlice,
				want:   http.StatusNotFound,
			},
		},
		{
			name: "add unknown principal",
			call: apiCall{
				method: http.MethodPost,
				path:   members,
				body:   `{"principalID":"prn-missing"}`,
				want:   http.StatusNotFound,
			},
		},
		{
			name: "add twice",
			call: apiCall{method: http.MethodPost, path: members, body: addAlice, want: http.StatusConflict},
		},
		{
			name: "add without ID",
			call: apiCall{method: http.MethodPost, path: members, body: `{}`, want: http.StatusBadRequest},
		},
		{
			name: "remove unknown member",
			call: apiCall{
				method: http.MethodDelete,
				path:   members + "/prn-missing",
				body:   "",
				want:   http.StatusNotFound,
			},
		},
	}
}

// TestPrincipalRoutesWithoutStore answers 501 that names the cause when the
// store keeps no principals.
func TestPrincipalRoutesWithoutStore(t *testing.T) {
	t.Parallel()

	h := newPrincipalHandler(t, nil)

	status, body := call(t, h, apiCall{http.MethodGet, "/api/server/principals", "", http.StatusNotImplemented})
	if status != http.StatusNotImplemented || !strings.Contains(body, "keeps no principals") {
		t.Fatalf("list without a store answers %d (%s), want 501", status, body)
	}
}

// TestPrincipalChangesWriteAuditEntries proves that each change of a
// principal or a group writes one entry of the kind change in the scope
// @server, with the outcome success or failure, and that a read writes none
// (ADR 60, decisions 4 and 7).
func TestPrincipalChangesWriteAuditEntries(t *testing.T) {
	t.Parallel()

	svc, st := newLimitedPrincipalStore(t)
	h := newAuditedPrincipalHandler(t, svc, st)

	var alice principalDoc
	mustCall(t, h, apiCall{
		http.MethodPost, "/api/server/principals", `{"subject":"alice","kind":"person"}`,
		http.StatusCreated,
	}, &alice)
	mustCall(t, h, apiCall{
		http.MethodPost, "/api/server/principals", `{"subject":"alice","kind":"person"}`,
		http.StatusConflict,
	}, nil)
	mustCall(t, h, apiCall{http.MethodGet, "/api/server/principals", "", http.StatusOK}, nil)

	var group groupDoc
	mustCall(t, h, apiCall{http.MethodPost, "/api/server/groups", `{"name":"ops"}`, http.StatusCreated}, &group)
	mustCall(t, h, apiCall{
		http.MethodPost, "/api/server/groups/" + group.ID + "/members",
		`{"principalID":"` + alice.ID + `"}`, http.StatusCreated,
	}, nil)

	// A refusal by an access limit writes a failure entry too: the limit
	// names the group in an unless clause, so an add of a member can lift
	// it and the guard refuses the add of the caller.
	seedNamingLimit(t, st, group.Name)
	mustCall(t, h, apiCall{
		http.MethodPost, "/api/server/groups/" + group.ID + "/members",
		`{"principalID":"` + alice.ID + `"}`, http.StatusForbidden,
	}, nil)

	listing, err := st.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list server entries: %v", err)
	}

	want := []struct {
		action, object string
		outcome        audit.Outcome
	}{
		{"server:manage-groups", "group:" + group.ID + "#member:" + alice.ID, audit.OutcomeFailure},
		{"server:manage-groups", "group:" + group.ID + "#member:" + alice.ID, audit.OutcomeSuccess},
		{"server:manage-groups", "group:" + group.ID, audit.OutcomeSuccess},
		{"server:manage-principals", "principals", audit.OutcomeFailure},
		{"server:manage-principals", "principal:" + alice.ID, audit.OutcomeSuccess},
	}

	entries := withoutSessionEntries(listing.Entries)
	if len(entries) != len(want) {
		t.Fatalf("server entries = %d, want %d: %+v", len(entries), len(want), entries)
	}

	for i, w := range want {
		e := entries[i]
		if e.Action != w.action || e.Object != w.object || e.Outcome != w.outcome ||
			e.Kind != audit.KindChange || e.Subject != "tester" || e.Namespace != audit.ServerScope {
			t.Fatalf("entry %d = %+v, want %s on %s with %s", i, e, w.action, w.object, w.outcome)
		}
	}

	check, err := st.Check(t.Context(), audit.ServerScope)
	if err != nil || !check.Valid {
		t.Fatalf("server chain check = %+v, %v; want valid", check, err)
	}
}

// withoutSessionEntries drops the entries of the console session of the test
// person: its creation and the start of its session. A change of group
// membership needs a console session, which makes both.
func withoutSessionEntries(entries []audit.Entry) []audit.Entry {
	return slices.DeleteFunc(slices.Clone(entries), func(e audit.Entry) bool {
		return e.Action == "session:start" || e.Object == "principal:prn-tester"
	})
}

// TestGroupRenameThatCanLiftALimitIsRefused proves that the route of a group
// change passes the caller to the service: a rename to a name that no access
// limit names goes through, and a rename to the name of a group in an unless
// clause of a limit answers 403 and keeps the name.
func TestGroupRenameThatCanLiftALimitIsRefused(t *testing.T) {
	t.Parallel()

	svc, st := newLimitedPrincipalStore(t)
	h := newAuditedPrincipalHandler(t, svc, st)

	var group groupDoc
	mustCall(t, h, apiCall{
		http.MethodPost, "/api/server/groups", `{"name":"ops"}`, http.StatusCreated,
	}, &group)

	// The limit names the group auditors in an unless clause: a rename to
	// that name can lift it for every member.
	seedNamingLimit(t, st, "auditors")

	mustCall(t, h, apiCall{
		http.MethodPatch, "/api/server/groups/" + group.ID, `{"name":"ops2"}`, http.StatusOK,
	}, nil)

	status, body := call(t, h, apiCall{
		http.MethodPatch, "/api/server/groups/" + group.ID, `{"name":"auditors"}`, http.StatusForbidden,
	})
	if status != http.StatusForbidden || !strings.Contains(body, "rename the group") {
		t.Fatalf("the rename to auditors answers %d (%s), want 403 that names the change", status, body)
	}

	stored, err := svc.GetGroup(t.Context(), group.ID)
	if err != nil || stored.Name != "ops2" {
		t.Fatalf("the group after the refused rename = %+v, %v, want the name ops2", stored, err)
	}
}

// failingStore is a principal store whose principal create fails the way a
// lost audit append fails: inside the transaction, so nothing is stored.
type failingStore struct {
	*sqlite.Store
}

var errAppendLost = errors.New("append audit entry: disk I/O error at /var/lib/secret")

func (failingStore) CreatePrincipal(context.Context, principal.Principal, audit.Entry) error {
	return errAppendLost
}

// TestPrincipalChangeFailsClosed proves that a change whose audit entry
// cannot be written answers 500 with no driver text, and writes a failure
// entry after the fact.
func TestPrincipalChangeFailsClosed(t *testing.T) {
	t.Parallel()

	_, st := newTestPrincipalStore(t)

	svc, err := principal.NewService(
		principal.Config{
			TempAdmins: nil,
			Store:      failingStore{st},
			Guard:      allowAllGuard{},
			GroupReach: allowAllReach{},
			Namespaces: nil,
			Tokens:     nil,
			Now:        nil,
			NewID:      nil,
		},
	)
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	h := newAuditedPrincipalHandler(t, svc, st)

	status, body := call(t, h, apiCall{
		http.MethodPost,
		"/api/server/principals",
		`{"subject":"alice","kind":"person"}`,
		http.StatusInternalServerError,
	})
	if status != http.StatusInternalServerError || strings.Contains(body, "secret") {
		t.Fatalf("create with a lost entry = %d %s, want 500 with no driver text", status, body)
	}

	if _, getErr := st.GetPrincipalBySubject(t.Context(), "alice"); !errors.Is(getErr, principal.ErrNotFound) {
		t.Fatalf("principal after a lost entry = %v, want ErrNotFound", getErr)
	}

	listing, err := st.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil || len(listing.Entries) != 1 || listing.Entries[0].Outcome != audit.OutcomeFailure {
		t.Fatalf("entries = %+v, %v; want one failure entry", listing.Entries, err)
	}
}

// TestPrincipalFailureObjectsNeverHoldRequestText proves that a refused
// change of a malformed ID names "invalid" in its entry, never the path.
func TestPrincipalFailureObjectsNeverHoldRequestText(t *testing.T) {
	t.Parallel()

	svc, st := newTestPrincipalStore(t)
	h := newAuditedPrincipalHandler(t, svc, st)

	mustCall(t, h, apiCall{http.MethodDelete, "/api/server/principals/prn-x%0Aforged", "", http.StatusNotFound}, nil)
	mustCall(t, h, apiCall{http.MethodDelete, "/api/server/groups/drop%20table", "", http.StatusNotFound}, nil)

	listing, err := st.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}

	entries := withoutSessionEntries(listing.Entries)
	objects := make([]string, 0, len(entries))

	for i := range entries {
		objects = append(objects, entries[i].Object)
	}

	if want := []string{"group:invalid", "principal:invalid"}; !slices.Equal(objects, want) {
		t.Fatalf("failure objects = %q, want %q", objects, want)
	}
}

func TestPrincipalBodyTooLarge(t *testing.T) {
	t.Parallel()

	h := newPrincipalHandler(t, newTestPrincipals(t))
	big := `{"subject":"` + strings.Repeat("a", 20_000) + `","kind":"person"}`

	mustCall(t, h, apiCall{http.MethodPost, "/api/server/principals", big, http.StatusRequestEntityTooLarge}, nil)
}

func TestPrincipalPaging(t *testing.T) {
	t.Parallel()

	h := newPrincipalHandler(t, newTestPrincipals(t))

	for _, subject := range []string{"carol", "alice", "bob", "dave"} {
		mustCall(t, h, apiCall{
			http.MethodPost,
			"/api/server/principals",
			`{"subject":"` + subject + `","kind":"person"}`,
			http.StatusCreated,
		}, nil)
	}

	var got []string

	path := "/api/server/principals?limit=3"

	for path != "" {
		var page struct {
			NextCursor string         `json:"nextCursor"`
			Principals []principalDoc `json:"principals"`
		}
		mustCall(t, h, apiCall{http.MethodGet, path, "", http.StatusOK}, &page)

		for i := range page.Principals {
			got = append(got, page.Principals[i].Subject)
		}

		path = ""
		if page.NextCursor != "" {
			path = "/api/server/principals?limit=3&cursor=" + page.NextCursor
		}
	}

	if want := []string{"alice", "bob", "carol", "dave"}; !slices.Equal(got, want) {
		t.Fatalf("pages = %q, want %q", got, want)
	}

	var prefixed principalListDoc
	mustCall(t, h, apiCall{http.MethodGet, "/api/server/principals?prefix=ca", "", http.StatusOK}, &prefixed)

	if len(prefixed.Principals) != 1 || prefixed.Principals[0].Subject != "carol" {
		t.Fatalf("prefix ca = %+v, want carol", prefixed.Principals)
	}

	for _, bad := range []string{
		"limit=x", "limit=0x10", "limit=1001", "limit=-1", "limit=0", "cursor=%21%21", "prefix=a%00", "cursor=YQA",
	} {
		mustCall(t, h, apiCall{http.MethodGet, "/api/server/principals?" + bad, "", http.StatusBadRequest}, nil)
	}
}

// TestPrincipalCountsAndSource proves that each list answers its total,
// that each record answers its count and source, and that a group of a
// directory refuses a membership change with 409 naming the directory.
func TestPrincipalCountsAndSource(t *testing.T) {
	t.Parallel()

	svc, st := newTestPrincipalStore(t)
	h := newPrincipalHandler(t, svc)

	var alice principalDoc
	mustCall(t, h, apiCall{
		http.MethodPost, "/api/server/principals", `{"subject":"alice","kind":"person"}`, http.StatusCreated,
	}, &alice)
	mustCall(t, h, apiCall{
		http.MethodPost, "/api/server/principals", `{"subject":"bob","kind":"person"}`, http.StatusCreated,
	}, nil)

	var ops groupDoc
	mustCall(t, h, apiCall{http.MethodPost, "/api/server/groups", `{"name":"ops"}`, http.StatusCreated}, &ops)
	mustCall(t, h, apiCall{
		http.MethodPost, "/api/server/groups/" + ops.ID + "/members", `{"principalID":"` + alice.ID + `"}`,
		http.StatusCreated,
	}, nil)

	var list struct {
		Principals []principalDoc `json:"principals"`
		NextCursor string         `json:"nextCursor"`
		Total      int64          `json:"total"`
	}
	mustCall(t, h, apiCall{http.MethodGet, "/api/server/principals?limit=1", "", http.StatusOK}, &list)

	// The total counts the person of the console session that the membership
	// change needs.
	if list.Total != 3 || len(list.Principals) != 1 || list.Principals[0].GroupCount != 1 ||
		list.Principals[0].Source != "local" || list.NextCursor == "" {
		t.Fatalf("principal page = %+v, want alice in 1 group, local, of 3 with a cursor", list)
	}

	var groups struct {
		Groups []groupDoc `json:"groups"`
		Total  int64      `json:"total"`
	}
	mustCall(t, h, apiCall{http.MethodGet, "/api/server/groups", "", http.StatusOK}, &groups)

	if groups.Total != 1 || groups.Groups[0].MemberCount != 1 || groups.Groups[0].Source != "local" {
		t.Fatalf("group page = %+v, want ops with 1 member, local", groups)
	}

	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)
	directory := principal.Group{
		CreatedAt: at, UpdatedAt: at, ID: "grp-ldap", Name: "engineering", Description: "",
		Source: principal.SourceLDAP, MemberCount: 0, ClosedToIdentityProvider: false,
	}

	if err := st.CreateGroup(t.Context(), directory, storetest.ServerChange(at, "group:grp-ldap")); err != nil {
		t.Fatalf("seed directory group: %v", err)
	}

	status, body := call(t, h, apiCall{
		http.MethodPost, "/api/server/groups/grp-ldap/members", `{"principalID":"` + alice.ID + `"}`,
		http.StatusConflict,
	})
	if status != http.StatusConflict || !strings.Contains(body, "ldap") {
		t.Fatalf("add to a directory group = %d %s, want 409 naming ldap", status, body)
	}

	mustCall(t, h, apiCall{
		http.MethodDelete, "/api/server/groups/grp-ldap/members/" + alice.ID, "", http.StatusConflict,
	}, nil)
}

var errTempAdminList = errors.New("temp admin list: disk I/O error")

// failingTempAdmins is a temporary administrator reader whose list fails.
type failingTempAdmins struct {
	err error
}

func (f failingTempAdmins) GetTempAdminBySubject(
	_ context.Context, _ string,
) (tempadmin.TempAdmin, error) {
	return tempadmin.TempAdmin{}, f.err
}

func (f failingTempAdmins) ListTempAdmins(_ context.Context) ([]tempadmin.TempAdmin, error) {
	return nil, f.err
}

// TestPrincipalListFailsWhenTempAdminReadFails proves that a failed read of
// the temporary administrator records answers 500 with the usual error body
// and no driver text.
func TestPrincipalListFailsWhenTempAdminReadFails(t *testing.T) {
	t.Parallel()

	svc := newTestPrincipals(t)

	handler, err := newCheckedHandler(t, &apiserver.Config{
		Reach:            noReach(),
		LocalUsers:       nil,
		Preferences:      nil,
		GitHostLinks:     nil,
		Settings:         nil,
		GitHost:          nil,
		Idempotency:      nil,
		Workers:          nil,
		InstallationKind: "",
		StateWriteGates:  stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		Backups:          nil,
		Restores:         nil,
		Version:          "",
		Journal:          nil,
		Access:           nil,
		Tokens:           nil,
		Audit:            newTestAudit(),
		IPAllowlists:     nil,
		Gates:            nil,
		SessionPolicy:    nil,
		CheckpointEpochs: nil,
		OnAuditFailure:   nil,
		AuditChain:       nil,
		DeletedUnits:     nil,
		Now:              nil,
		AuditSinks:       nil,
		SignIn:           nil,
		TempAdmins:       failingTempAdmins{err: errTempAdminList},
		Authenticator:    tokenAuthenticator{},
		Authorizer:       selectiveAuthorizer{deny: map[identity.Action]bool{}},
		Logger:           slog.New(slog.DiscardHandler),
		Namespaces:       newTestNamespaces(t),
		Maintenance:      newFakeMaintenance(maintenance.StateNormal),
		Metrics:          stubMetrics{},
		Schema:           stubSchema{migrations: nil},
		Principals:       svc,
		DenialLimiter:    nil,
		Instances:        nil,
		StoreChecks:      nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	status, body := call(
		t,
		handler,
		apiCall{http.MethodGet, "/api/server/principals", "", http.StatusInternalServerError},
	)
	if status != http.StatusInternalServerError || strings.Contains(body, "disk") {
		t.Fatalf("list with a failed read = %d %s, want 500 with no driver text", status, body)
	}

	var doc struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil || doc.Code != http.StatusInternalServerError ||
		doc.Message == "" {
		t.Fatalf("error body = %q, want a JSON error with code 500", body)
	}
}

// fixedTempAdmins is a temporary administrator reader that lists the
// records that its pointer holds at the time of the read.
type fixedTempAdmins struct {
	records *[]tempadmin.TempAdmin
}

func (f fixedTempAdmins) GetTempAdminBySubject(
	_ context.Context, subject string,
) (tempadmin.TempAdmin, error) {
	for i := range *f.records {
		if (*f.records)[i].Subject == subject {
			return (*f.records)[i], nil
		}
	}

	return tempadmin.TempAdmin{}, tempadmin.ErrNotFound
}

func (f fixedTempAdmins) ListTempAdmins(_ context.Context) ([]tempadmin.TempAdmin, error) {
	return *f.records, nil
}

// TestTempAdminFlagFollowsThePrincipalID proves that the banner flag
// follows the principal ID of the record, not its subject: a record of an
// old principal with the subject of a new person does not mark the new
// person.
func TestTempAdminFlagFollowsThePrincipalID(t *testing.T) {
	t.Parallel()

	records := []tempadmin.TempAdmin{}

	handler, err := newCheckedHandler(t, &apiserver.Config{
		Reach:            noReach(),
		LocalUsers:       nil,
		Preferences:      nil,
		GitHostLinks:     nil,
		Settings:         nil,
		GitHost:          nil,
		Idempotency:      nil,
		Workers:          nil,
		InstallationKind: "",
		StateWriteGates:  stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		Backups:          nil,
		Restores:         nil,
		Version:          "",
		Journal:          nil,
		Access:           nil,
		Tokens:           nil,
		Audit:            newTestAudit(),
		IPAllowlists:     nil,
		Gates:            nil,
		SessionPolicy:    nil,
		CheckpointEpochs: nil,
		OnAuditFailure:   nil,
		AuditChain:       nil,
		DeletedUnits:     nil,
		Now:              nil,
		AuditSinks:       nil,
		SignIn:           nil,
		TempAdmins:       fixedTempAdmins{records: &records},
		Authenticator:    tokenAuthenticator{},
		Authorizer:       selectiveAuthorizer{deny: map[identity.Action]bool{}},
		Logger:           slog.New(slog.DiscardHandler),
		Namespaces:       newTestNamespaces(t),
		Maintenance:      newFakeMaintenance(maintenance.StateNormal),
		Metrics:          stubMetrics{},
		Schema:           stubSchema{migrations: nil},
		Principals:       newTestPrincipals(t),
		DenialLimiter:    nil,
		Instances:        nil,
		StoreChecks:      nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	var alice principalDoc

	mustCall(t, handler, apiCall{
		http.MethodPost, "/api/server/principals",
		`{"subject":"alice@example.com","displayName":"Alice","kind":"person"}`, http.StatusCreated,
	}, &alice)

	record := func(principalID string) tempadmin.TempAdmin {
		created := time.Now().UTC().Truncate(time.Millisecond)

		return tempadmin.TempAdmin{
			CreatedAt: created, ExpiresAt: created.Add(time.Hour), EndedAt: time.Time{}, ReviewOpenedAt: time.Time{},
			PrincipalID: principalID, Subject: "alice@example.com", CreatedBy: "operator", Reason: "lost access",
		}
	}

	for _, c := range []struct {
		principalID string
		want        bool
	}{
		{principalID: "prn-old", want: false},
		{principalID: alice.ID, want: true},
	} {
		records = []tempadmin.TempAdmin{record(c.principalID)}

		var got struct {
			TemporaryAdministrator bool `json:"temporaryAdministrator"`
		}

		mustCall(t, handler, apiCall{http.MethodGet, "/api/server/principals/" + alice.ID, "", http.StatusOK}, &got)

		if got.TemporaryAdministrator != c.want {
			t.Fatalf("record of %s: temporaryAdministrator = %v, want %v",
				c.principalID, got.TemporaryAdministrator, c.want)
		}
	}
}

// allowAllGuard is a principal.Guard that allows every change. These tests
// check the routes, not the grant check, which the principal package tests.
type allowAllGuard struct{}

func (allowAllGuard) CheckGroupGrants(
	context.Context, identity.Principal, string, string, principal.GroupChange,
) error {
	return nil
}

func (allowAllGuard) CheckPrincipalGrants(context.Context, identity.Principal, string) error {
	return nil
}

// allowAllReach is a principal.GroupReach that allows every membership add.
// These tests check the routes, not the reach check, which the principal
// package tests.
type allowAllReach struct{}

func (allowAllReach) CheckGroupReach(context.Context, identity.Principal, string) error { return nil }

// TestLastSiteAdministratorIsKept proves that each principal route that could end the
// last site administrator answers 409 with a JSON error, changes nothing, and
// leaves the administrator as it was.
func TestLastSiteAdministratorIsKept(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		method string
		path   func(admin string) string
		body   string
	}{
		{"delete", http.MethodDelete, func(admin string) string { return "/api/server/principals/" + admin }, ""},
		{
			"disable",
			http.MethodPost,
			func(admin string) string { return "/api/server/principals/" + admin + "/disable" },
			"",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			principals, st := newTestPrincipalStore(t)
			h := newPrincipalHandler(t, principals)

			var admin principalDoc
			mustCall(t, h, apiCall{
				http.MethodPost, "/api/server/principals", `{"subject":"root","kind":"person"}`, http.StatusCreated,
			}, &admin)

			grant := access.Grant{
				CreatedAt: time.Unix(0, 0), Condition: nil, Object: identity.ServerObject(), ID: "grt-last",
				Role: access.RoleSiteAdministrator, CreatedBy: "test",
				Subject: access.Subject{Kind: access.SubjectPrincipal, ID: admin.ID},
			}

			entry := storetest.ServerChange(time.Unix(0, 0), "@server")
			fence := namespace.Fence{Namespace: "", FailoverVersion: 0}

			err := st.CreateGrant(t.Context(), grant, fence, entry, nil)
			if err != nil {
				t.Fatalf("create grant: %v", err)
			}

			status, body := call(t, h, apiCall{tc.method, tc.path(admin.ID), tc.body, http.StatusConflict})
			if status != http.StatusConflict || !strings.Contains(body, "last site administrator") {
				t.Fatalf("%s answers %d %q, want 409 and the last site administrator error", tc.name, status, body)
			}

			mustCall(t, h, apiCall{http.MethodGet, "/api/server/principals/" + admin.ID, "", http.StatusOK}, nil)
		})
	}
}

// TestLastSiteAdministratorErrorIsConflict proves that the grant routes
// answer 409 when a revoke would end the last site administrator.
func TestLastSiteAdministratorErrorIsConflict(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("revoke grant: %w", access.ErrLastSiteAdministrator)

	status, message := apiserver.ClassifyForTest(err)
	if status != http.StatusConflict || !strings.Contains(message, "last site administrator") {
		t.Fatalf("classified as %d %q, want 409 and the last site administrator error", status, message)
	}
}
