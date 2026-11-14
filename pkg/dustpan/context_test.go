package dustpan_test

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// The tokens sign in the fixed test principals.
//
//nolint:gosec // These are test tokens, not credentials.
const (
	ctxAdminToken  = "ctx-admin-token-0123456789abcdef012"
	ctxOwnerToken  = "ctx-owner-token-0123456789abcdef01"
	ctxWriterToken = "ctx-writer-token-0123456789abcdef0"
	ctxViewerToken = "ctx-viewer-token-0123456789abcdef0"
	ctxOtherToken  = "ctx-other-token-0123456789abcdef01"
	ctxNobodyToken = "ctx-nobody-token-0123456789abcdef0"
	ctxNsOnlyToken = "ctx-nsonly-token-0123456789abcdef"
	// ctxServiceToken and ctxWorkloadToken sign in a service principal and a
	// workload principal that hold the read-only role on acme.
	ctxServiceToken  = "ctx-service-token-0123456789abcdef0"
	ctxWorkloadToken = "ctx-workload-token-0123456789abcdef"
)

// ctxNsOnlyRole is a custom role that holds namespace:read and no unit action.
const ctxNsOnlyRole = "rol-ns-only"

// ctxReadOnlyRole is a custom role that holds namespace:read and unit:read
// and no other action.
const ctxReadOnlyRole = "rol-read-only"

// contextWorld is a server with the namespaces acme and other, and the units
// that the context tests read: the OpenTofu workspace net and the Pulumi
// stack web/prod in acme, and the Pulumi stack web/prod in other.
type contextWorld struct {
	srv        *dustpan.Server
	store      *sqlite.Store
	workspace  string
	stack      string
	otherStack string
}

// newContextWorld opens a store with the principals owen (admin of acme),
// wanda (writer on acme), vera (viewer on acme), olga (viewer on other) and
// sam (no grant). The token ctxAdminToken signs in an administrator of the
// installation.
func newContextWorld(t *testing.T, extra ...dustpan.Option) contextWorld {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	blobs, err := fs.Open(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	for _, name := range []namespace.Name{"acme", "other"} {
		ns := namespace.Namespace{
			ID:                  namespace.NewID(),
			CreatedAt:           time.Unix(0, 0),
			Name:                name,
			PrimaryInstallation: installationName,
			FailoverVersion:     1,
			Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
		}
		if nsErr := st.CreateNamespace(t.Context(), ns); nsErr != nil {
			t.Fatalf("create namespace %s: %v", name, nsErr)
		}

		seedCatchAllPlacement(t, st, name)
	}

	at := time.Unix(0, 0)
	nsOnly := access.Role{
		CreatedAt: at, UpdatedAt: at, ID: ctxNsOnlyRole, Name: "namespace reader", Description: "", BuiltIn: false,
		Actions: []identity.Action{identity.ActionNamespaceRead},
	}

	roleEntry := storetest.ServerChange(at, "role:"+ctxNsOnlyRole)
	if roleErr := st.CreateRole(t.Context(), nsOnly, roleEntry); roleErr != nil {
		t.Fatalf("create role: %v", roleErr)
	}

	seedReadOnlyPrincipals(t, st)

	for _, who := range []struct {
		subject, role, ns string
	}{
		{"owen", access.RoleAdmin, "acme"},
		{"wanda", access.RoleWriter, "acme"},
		{"vera", access.RoleViewer, "acme"},
		{"olga", access.RoleViewer, "other"},
		{"sam", "", ""},
		{"nora", ctxNsOnlyRole, "acme"},
	} {
		storePrincipal(t, st, who.subject)

		if who.role != "" {
			storeGrant(t, st, who.subject, who.role, identity.NamespaceObject(namespace.Name(who.ns)))
		}
	}

	person := func(subject string) identity.Principal {
		return identity.Principal{
			Subject: subject, DisplayName: subject, Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
			Kind:       "",
			Credential: "",
			JobID:      "",
		}
	}

	authn := tokenMapAuthenticator{tokens: map[string]identity.Principal{
		ctxAdminToken: {
			Subject: "operator", DisplayName: "operator", Admin: true, TokenID: "", TokenExpiresAt: time.Time{},
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
			Kind:       "",
			Credential: "",
			JobID:      "",
		},
		ctxOwnerToken:  person("owen"),
		ctxWriterToken: person("wanda"),
		ctxViewerToken: person("vera"),
		ctxOtherToken:  person("olga"),
		ctxNobodyToken: person("sam"),
		ctxNsOnlyToken: person("nora"),
		ctxServiceToken: {
			Subject: "svc-reader", DisplayName: "svc-reader", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
			Kind:       identity.KindService,
			Credential: "",
			JobID:      "",
		},
		ctxWorkloadToken: {
			Subject: "wl-reader", DisplayName: "wl-reader", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
			Kind:       identity.KindWorkload,
			Credential: "",
			JobID:      "",
		},
	}}

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	srv, err := dustpan.NewServer(append([]dustpan.Option{
		dustpan.WithInstallation(installationName),
		dustpan.WithNamespaceStore(st),
		dustpan.WithStackStore(st),
		dustpan.WithUpdateStore(st),
		dustpan.WithCheckpointStore(st),
		dustpan.WithEventStore(st),
		dustpan.WithSecretKeyStore(st),
		dustpan.WithSecretWrapper(wrapper),
		dustpan.WithWorkspaceStore(st),
		dustpan.WithStateStore(st),
		dustpan.WithBlobStore(blobs),
		dustpan.WithInventoryReader(st),
		dustpan.WithAuthenticator(authn),
	}, extra...)...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	w := contextWorld{srv: srv, store: st, workspace: "", stack: "", otherStack: ""}
	w.seed(t)

	return w
}

// seed makes the units of the world as the administrator.
func (w *contextWorld) seed(t *testing.T) {
	t.Helper()

	name, err := workspace.ParseName("net")
	if err != nil {
		t.Fatalf("parse workspace name: %v", err)
	}

	ws, err := w.srv.Workspaces().Create(
		t.Context(), workspace.Ref{Namespace: "acme", Name: name}, placeWorkspace(t, w.store, "acme"),
	)
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	w.workspace = string(ws.ID)
	w.stack = w.makeStack(t, "acme")
	w.otherStack = w.makeStack(t, "other")
}

// makeStack makes the stack web/prod in ns and returns its stable ID.
func (w *contextWorld) makeStack(t *testing.T, ns string) string {
	t.Helper()

	code, body := serveDeletedScope(t, w.srv, deletedScopeCall{
		method: http.MethodPost, target: "/api/stacks/" + ns + "/web", token: ctxAdminToken,
		body: `{"stackName":"prod"}`,
	})
	if code != http.StatusOK {
		t.Fatalf("create stack in %s: %d %s", ns, code, body)
	}

	code, body = serveDeletedScope(t, w.srv, deletedScopeCall{
		method: http.MethodGet, target: "/api/stacks/" + ns + "/web/prod", token: ctxAdminToken, body: "",
	})
	if code != http.StatusOK {
		t.Fatalf("read stack in %s: %d %s", ns, code, body)
	}

	var got struct {
		ID string `json:"id"`
	}

	if err := json.Unmarshal([]byte(body), &got); err != nil || got.ID == "" {
		t.Fatalf("decode stack %s: %v", body, err)
	}

	return got.ID
}

// call serves one request and answers its status and body.
func (w *contextWorld) call(t *testing.T, method, target, token, body string) (int, string) {
	t.Helper()

	return serveDeletedScope(t, w.srv, deletedScopeCall{method: method, target: target, token: token, body: body})
}

// ctxFromWire is the provenance of a resolved value.
type ctxFromWire struct {
	Object string `json:"object"`
	Source string `json:"source"`
}

// ctxTextWire is a resolved text with its provenance.
type ctxTextWire struct {
	From  *ctxFromWire `json:"from"`
	Value string       `json:"value"`
}

// ctxOwnersWire is the resolved owners with their provenance.
type ctxOwnersWire struct {
	From  *ctxFromWire `json:"from"`
	Value []string     `json:"value"`
}

// ctxResolvedWire is the resolved context that the tests read.
type ctxResolvedWire struct {
	Owners ctxOwnersWire `json:"owners"`
	OnCall ctxTextWire   `json:"onCall"`
}

// ctxDataWire is the data of the context section.
type ctxDataWire struct {
	Resolved ctxResolvedWire `json:"resolved"`
}

// ctxSectionWire is the context section of a document.
type ctxSectionWire struct {
	Status string      `json:"status"`
	Data   ctxDataWire `json:"data"`
}

// ctxSectionsWire holds the sections that the tests read.
type ctxSectionsWire struct {
	Context ctxSectionWire `json:"context"`
}

// resolvedWire is the part of the context document that the tests read.
type resolvedWire struct {
	Sections ctxSectionsWire `json:"sections"`
	Deleted  bool            `json:"deleted"`
}

// read reads the context route target as token and decodes the answer.
func (w *contextWorld) read(t *testing.T, target, token string) resolvedWire {
	t.Helper()

	code, body := w.call(t, http.MethodGet, target, token, "")
	if code != http.StatusOK {
		t.Fatalf("GET %s = %d %s, want 200", target, code, body)
	}

	var got resolvedWire
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}

	return got
}

// ownersFrom returns the resolved owners and the kind of the object that they
// come from.
func ownersFrom(r resolvedWire) ([]string, string) {
	from := ""
	if f := r.Sections.Context.Data.Resolved.Owners.From; f != nil {
		from = f.Object
	}

	return r.Sections.Context.Data.Resolved.Owners.Value, from
}

// onCallOf returns the resolved on-call text.
func onCallOf(r resolvedWire) string {
	return r.Sections.Context.Data.Resolved.OnCall.Value
}

// onCallObject returns the kind of the object that the on-call text comes
// from.
func onCallObject(r resolvedWire) string {
	if f := r.Sections.Context.Data.Resolved.OnCall.From; f != nil {
		return f.Object
	}

	return ""
}

const (
	ctxNamespacePath = "/api/dustpan/v1/namespaces/acme"
	ctxNamespaceDoc  = ctxNamespacePath + "/context"
	ctxOwnersPatch   = `{"owners":["group:platform"],"onCall":"pager:platform"}`
)

// expectCase fails the test when the call of tc answers another status.
func (w *contextWorld) expectCase(t *testing.T, tc contextCase) {
	t.Helper()

	if code, got := w.call(t, tc.method, tc.target, tc.token, tc.body); code != tc.want {
		t.Fatalf("%s %s = %d %s, want %d", tc.method, tc.target, code, got, tc.want)
	}
}

// expectPatch fails the test when a PATCH answers another status than want.
func (w *contextWorld) expectPatch(t *testing.T, want int, target, token, body string) {
	t.Helper()
	w.expectCase(
		t,
		contextCase{name: "", method: http.MethodPatch, target: target, token: token, body: body, want: want},
	)
}

// expectDelete fails the test when a DELETE answers another status than want.
func (w *contextWorld) expectDelete(t *testing.T, want int, target, token string) {
	t.Helper()
	w.expectCase(
		t,
		contextCase{name: "", method: http.MethodDelete, target: target, token: token, body: "", want: want},
	)
}

// expectOwners fails the test when the unit at target does not resolve its
// owners to owners from the object of the kind from.
func (w *contextWorld) expectOwners(t *testing.T, label, target string, owners []string, from string) {
	t.Helper()

	got, gotFrom := ownersFrom(w.read(t, target, ctxViewerToken))
	if !slices.Equal(got, owners) || (len(owners) > 0 && gotFrom != from) {
		t.Errorf("%s: owners = %v from %q, want %v from %q", label, got, gotFrom, owners, from)
	}
}

// TestContextInheritsFromTheNamespace proves the thin slice through the
// assembled server: a platform engineer sets the owners and the on-call text
// on a namespace, and the OpenTofu workspace and the Pulumi stack of that
// namespace read them back from their own context routes.
func TestContextInheritsFromTheNamespace(t *testing.T) {
	t.Parallel()

	w := newContextWorld(t)
	units := map[string]string{
		"opentofu workspace": ctxNamespacePath + "/units/" + w.workspace + "/context",
		"pulumi stack":       ctxNamespacePath + "/units/" + w.stack + "/context",
	}

	w.expectPatch(t, http.StatusOK, ctxNamespaceDoc, ctxOwnerToken, ctxOwnersPatch)

	for name, path := range units {
		w.expectOwners(t, name, path, []string{"group:platform"}, "namespace")

		got := w.read(t, path, ctxViewerToken)
		if onCallOf(got) != "pager:platform" || onCallObject(got) != "namespace" {
			t.Errorf("%s: on-call = %q from %q, want pager:platform from the namespace",
				name, onCallOf(got), onCallObject(got))
		}
	}
}

// TestContextUnitOverridesAndClears proves that a unit overrides the owners of
// its namespace, keeps the on-call text of the namespace, and gets the
// inherited owners back after a clear.
func TestContextUnitOverridesAndClears(t *testing.T) {
	t.Parallel()

	w := newContextWorld(t)
	path := ctxNamespacePath + "/units/" + w.workspace + "/context"

	w.expectPatch(t, http.StatusOK, ctxNamespaceDoc, ctxOwnerToken, ctxOwnersPatch)
	w.expectPatch(t, http.StatusOK, path, ctxWriterToken, `{"owners":["group:net"]}`)
	w.expectOwners(t, "after an override", path, []string{"group:net"}, "unit")

	if onCall := onCallOf(w.read(t, path, ctxViewerToken)); onCall != "pager:platform" {
		t.Errorf("on-call after an override of the owners = %q, want the one of the namespace", onCall)
	}

	w.expectDelete(t, http.StatusNoContent, path, ctxWriterToken)
	w.expectOwners(t, "after a clear", path, []string{"group:platform"}, "namespace")
	w.expectDelete(t, http.StatusNotFound, path, ctxWriterToken)
	w.expectDelete(t, http.StatusNoContent, ctxNamespaceDoc, ctxOwnerToken)
	w.expectOwners(t, "after the namespace clear", path, nil, "")
}

// ctxIdentityDocWire is the identity section of a document.
type ctxIdentityDocWire struct {
	Sections ctxIdentitySectionsWire `json:"sections"`
}

// ctxIdentitySectionsWire holds the identity section.
type ctxIdentitySectionsWire struct {
	Identity ctxIdentitySectionWire `json:"identity"`
}

// ctxIdentitySectionWire is the identity section.
type ctxIdentitySectionWire struct {
	Data ctxIdentityDataWire `json:"data"`
}

// ctxIdentityDataWire is the data of the identity section.
type ctxIdentityDataWire struct {
	Name string `json:"name"`
}

// TestContextUnitSurvivesRename proves that the context of a stack stays with
// the stable ID of the stack: after a rename through the Pulumi API, the same
// context route answers the same owners and the new name.
func TestContextUnitSurvivesRename(t *testing.T) {
	t.Parallel()

	w := newContextWorld(t)
	path := ctxNamespacePath + "/units/" + w.stack + "/context"

	w.expectPatch(t, http.StatusOK, path, ctxWriterToken, `{"owners":["group:web"]}`)

	code, body := w.call(
		t, http.MethodPost, "/api/stacks/acme/web/prod/rename", ctxAdminToken, `{"newName":"live","newProject":"web"}`,
	)
	if code != http.StatusNoContent && code != http.StatusOK {
		t.Fatalf("rename stack = %d %s", code, body)
	}

	w.expectOwners(t, "after a rename", path, []string{"group:web"}, "unit")

	code, body = w.call(t, http.MethodGet, path, ctxViewerToken, "")
	if code != http.StatusOK {
		t.Fatalf("GET %s = %d %s, want 200", path, code, body)
	}

	var got ctxIdentityDocWire
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}

	if name := got.Sections.Identity.Data.Name; !strings.HasSuffix(name, "live") {
		t.Errorf("identity name after the rename = %q, want the new name live", name)
	}
}

// TestContextWorkspaceSurvivesRename proves that the context of an OpenTofu
// workspace stays with its stable ID. A workspace gets a new name when a
// restore of the deleted workspace names it: the same context route then
// answers the same owners and the new name.
func TestContextWorkspaceSurvivesRename(t *testing.T) {
	t.Parallel()

	w := newContextWorld(t)
	path := ctxNamespacePath + "/units/" + w.workspace + "/context"

	w.expectPatch(t, http.StatusOK, path, ctxWriterToken, `{"owners":["group:net"]}`)

	oldName, err := workspace.ParseName("net")
	if err != nil {
		t.Fatalf("parse workspace name: %v", err)
	}

	newName, err := workspace.ParseName("core")
	if err != nil {
		t.Fatalf("parse workspace name: %v", err)
	}

	at := time.UnixMilli(1_800_000_000_000).UTC()
	e := audit.Entry{
		At: at, Namespace: "acme", Subject: "operator", IDPSubject: "", TokenID: "", Action: "workspace:delete",
		Object: "acme/net", Version: "", ValuePath: "", ValueCount: nil, SourceIP: "", UserAgent: "",
		Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "",
		PrevHash: "", Hash: "", Sequence: 0, Changes: nil, ObjectID: "", FenceVersion: 0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}

	if err = w.srv.Workspaces().Delete(t.Context(), workspace.Ref{Namespace: "acme", Name: oldName}, e); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}

	e.Action = "workspace:restore"

	_, err = w.srv.Workspaces().RestoreDeleted(t.Context(), "acme", workspace.ID(w.workspace), newName, e)
	if err != nil {
		t.Fatalf("restore workspace under the name core: %v", err)
	}

	w.expectOwners(t, "after a rename", path, []string{"group:net"}, "unit")

	code, body := w.call(t, http.MethodGet, path, ctxViewerToken, "")
	if code != http.StatusOK {
		t.Fatalf("GET %s = %d %s, want 200", path, code, body)
	}

	var got ctxIdentityDocWire
	if err = json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}

	if name := got.Sections.Identity.Data.Name; !strings.HasSuffix(name, "core") {
		t.Errorf("identity name after the rename = %q, want the new name core", name)
	}
}

// contextCase is one request of the table of refusals, and the status that it
// must answer.
type contextCase struct {
	name, method, target, token, body string
	want                              int
}

// contextCases returns the table of refusals and bad requests for the units
// of w. A case never changes the context of the workspace, so the cases can
// run side by side.
func contextCases(w *contextWorld) []contextCase {
	unit := ctxNamespacePath + "/units/" + w.workspace + "/context"
	stack := ctxNamespacePath + "/units/" + w.stack + "/context"
	other := "/api/dustpan/v1/namespaces/other/units/"

	return []contextCase{
		{"a signed-out caller", http.MethodGet, unit, "", "", http.StatusUnauthorized},
		{"read a unit without a grant", http.MethodGet, unit, ctxNobodyToken, "", http.StatusNotFound},
		{"read a namespace without a grant", http.MethodGet, ctxNamespaceDoc, ctxNobodyToken, "", http.StatusNotFound},
		{"read through another namespace", http.MethodGet, ctxNamespaceDoc, ctxOtherToken, "", http.StatusNotFound},
		{"read a unit as a viewer", http.MethodGet, unit, ctxViewerToken, "", http.StatusOK},
		{
			"an unknown namespace",
			http.MethodGet,
			"/api/dustpan/v1/namespaces/absent/context",
			ctxAdminToken,
			"",
			http.StatusNotFound,
		},
		{
			"an unknown unit", http.MethodGet, ctxNamespacePath + "/units/ws-absent/context", ctxAdminToken, "",
			http.StatusNotFound,
		},
		{
			"a unit of another namespace", http.MethodGet, ctxNamespacePath + "/units/" + w.otherStack + "/context",
			ctxAdminToken, "", http.StatusNotFound,
		},
		{
			"a unit of this namespace through another namespace",
			http.MethodGet,
			other + w.stack + "/context",
			ctxOtherToken,
			"",
			http.StatusNotFound,
		},
		{"set a unit as a viewer", http.MethodPatch, unit, ctxViewerToken, `{"onCall":"x"}`, http.StatusForbidden},
		{"set a unit without a grant", http.MethodPatch, unit, ctxNobodyToken, `{"onCall":"x"}`, http.StatusNotFound},
		{"clear a unit as a viewer", http.MethodDelete, unit, ctxViewerToken, "", http.StatusForbidden},
		{
			"set a namespace as a writer", http.MethodPatch, ctxNamespaceDoc, ctxWriterToken, `{"onCall":"x"}`,
			http.StatusForbidden,
		},
		{
			"clear a namespace as a writer",
			http.MethodDelete,
			ctxNamespaceDoc,
			ctxWriterToken,
			"",
			http.StatusForbidden,
		},
		{
			"set a namespace as its admin", http.MethodPatch, ctxNamespaceDoc, ctxOwnerToken, `{"summary":"The shop"}`,
			http.StatusOK,
		},
		{
			"set a stack as a writer",
			http.MethodPatch,
			stack,
			ctxWriterToken,
			`{"summary":"The web stack"}`,
			http.StatusOK,
		},
		{"an unknown field", http.MethodPatch, unit, ctxWriterToken, `{"owner":["group:x"]}`, http.StatusBadRequest},
		{"an empty patch", http.MethodPatch, unit, ctxWriterToken, `{}`, http.StatusBadRequest},
		{"a body that is not an object", http.MethodPatch, unit, ctxWriterToken, `[]`, http.StatusBadRequest},
		{
			"two objects in the body", http.MethodPatch, unit, ctxWriterToken, `{"onCall":"x"}{"onCall":"y"}`,
			http.StatusBadRequest,
		},
		{"an empty owner", http.MethodPatch, unit, ctxWriterToken, `{"owners":[""]}`, http.StatusBadRequest},
		{
			"a link with a bad URL",
			http.MethodPatch,
			unit,
			ctxWriterToken,
			`{"links":[{"title":"x","url":"ftp://x"}]}`,
			http.StatusBadRequest,
		},
		{"an unknown section", http.MethodGet, unit + "?include=nothing", ctxAdminToken, "", http.StatusBadRequest},
		{"an empty section name", http.MethodGet, unit + "?include=context,", ctxAdminToken, "", http.StatusBadRequest},
		{"clear a unit with no API context", http.MethodDelete, unit, ctxAdminToken, "", http.StatusNotFound},
	}
}

// TestContextRoutes is the table of the refusals of the context routes.
func TestContextRoutes(t *testing.T) {
	t.Parallel()

	w := newContextWorld(t)

	for _, tc := range contextCases(&w) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w.expectCase(t, tc)
		})
	}
}

// TestContextOneNotFound proves that an unknown unit, a unit of another
// namespace and a unit that the caller cannot read answer with the same body.
func TestContextOneNotFound(t *testing.T) {
	t.Parallel()

	w := newContextWorld(t)
	bodies := map[string]string{}

	for name, c := range map[string]struct{ target, token string }{
		"unknown unit":    {ctxNamespacePath + "/units/ws-absent/context", ctxAdminToken},
		"other namespace": {ctxNamespacePath + "/units/" + w.otherStack + "/context", ctxAdminToken},
		"cannot read":     {ctxNamespacePath + "/units/" + w.workspace + "/context", ctxNobodyToken},
		"namespace read only": {
			ctxNamespacePath + "/units/" + w.workspace + "/context", ctxNsOnlyToken,
		},
		"unknown namespace": {"/api/dustpan/v1/namespaces/absent/units/" + w.workspace + "/context", ctxAdminToken},
	} {
		code, body := w.call(t, http.MethodGet, c.target, c.token, "")
		if code != http.StatusNotFound {
			t.Fatalf("%s = %d %s, want 404", name, code, body)
		}

		bodies[name] = body
	}

	for name, body := range bodies {
		if body != bodies["unknown unit"] {
			t.Errorf("%s answers %q, want the body of an unknown unit %q", name, body, bodies["unknown unit"])
		}
	}
}

// TestContextDeletedUnit proves that the read of a soft-deleted unit answers
// with the mark deleted, that a write answers 409, and that a clear works.
func TestContextDeletedUnit(t *testing.T) {
	t.Parallel()

	w := newContextWorld(t)
	path := ctxNamespacePath + "/units/" + w.stack + "/context"

	w.expectPatch(t, http.StatusOK, path, ctxWriterToken, `{"onCall":"pager:web"}`)

	code, body := w.call(t, http.MethodDelete, "/api/stacks/acme/web/prod", ctxAdminToken, "")
	if code != http.StatusNoContent && code != http.StatusOK {
		t.Fatalf("delete stack = %d %s", code, body)
	}

	got := w.read(t, path, ctxAdminToken)
	if !got.Deleted || onCallOf(got) != "pager:web" {
		t.Errorf("read of a deleted unit = %+v, want the mark deleted and its on-call text", got)
	}

	w.expectPatch(t, http.StatusConflict, path, ctxWriterToken, `{"onCall":"x"}`)
	w.expectDelete(t, http.StatusNoContent, path, ctxWriterToken)
}

// ctxChangeWire is one change of an audit entry.
type ctxChangeWire struct {
	Key string `json:"key"`
}

// ctxAuditEntryWire is the part of an audit entry that the test reads.
type ctxAuditEntryWire struct {
	Action   string          `json:"action"`
	ObjectID string          `json:"objectID"`
	Subject  string          `json:"subject"`
	Changes  []ctxChangeWire `json:"changes"`
}

// ctxAuditWire is the part of an audit list that the test reads.
type ctxAuditWire struct {
	Entries []ctxAuditEntryWire `json:"entries"`
}

// TestContextWritesAreAudited proves that a context write appends its audit
// entry with the action and the changes.
func TestContextWritesAreAudited(t *testing.T) {
	t.Parallel()

	w := newContextWorld(t)

	w.expectPatch(t, http.StatusOK, ctxNamespaceDoc, ctxOwnerToken, ctxOwnersPatch)
	w.expectPatch(t, http.StatusOK, ctxNamespacePath+"/units/"+w.stack+"/context", ctxWriterToken, `{"onCall":"x"}`)

	code, body := w.call(t, http.MethodGet, "/api/audit/entries?organization=acme", ctxAdminToken, "")
	if code != http.StatusOK {
		t.Fatalf("audit list = %d %s", code, body)
	}

	var got ctxAuditWire
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}

	checkUnitAuditObjectID(t, got, w.stack)

	for i := range got.Entries {
		e := &got.Entries[i]
		if e.Action != "namespace:set-context" {
			continue
		}

		keys := make([]string, 0, len(e.Changes))
		for _, c := range e.Changes {
			keys = append(keys, c.Key)
		}

		slices.Sort(keys)

		if e.ObjectID != "" {
			t.Errorf("audit entry of a namespace has objectID %q, want none", e.ObjectID)
		}

		if e.Subject != "owen" || !slices.Equal(keys, []string{"onCall", "owners"}) {
			t.Errorf("audit entry = %+v, want owen and the changes onCall and owners", *e)
		}

		return
	}

	t.Errorf("audit log lacks the namespace:set-context entry: %s", body)
}

// checkUnitAuditObjectID checks that the audit list holds the unit:set-context
// entry, and that the entry names the unit by its stable ID.
func checkUnitAuditObjectID(t *testing.T, list ctxAuditWire, unitID string) {
	t.Helper()

	for i := range list.Entries {
		e := &list.Entries[i]
		if e.Action != "unit:set-context" {
			continue
		}

		if e.ObjectID != unitID {
			t.Errorf("audit entry of a unit has objectID %q, want the stable ID %q", e.ObjectID, unitID)
		}

		return
	}

	t.Errorf("audit log lacks the unit:set-context entry: %+v", list)
}
