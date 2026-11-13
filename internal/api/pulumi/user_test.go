package pulumi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/pulumi"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// memNamespaceStore is an in-memory namespace.Store ordered by name on list.
type memNamespaceStore struct {
	namespaces map[namespace.Name]namespace.Namespace
}

func (m *memNamespaceStore) CreateNamespace(_ context.Context, ns namespace.Namespace) error {
	if _, ok := m.namespaces[ns.Name]; ok {
		return namespace.ErrAlreadyExists
	}

	m.namespaces[ns.Name] = ns

	return nil
}

func (m *memNamespaceStore) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	ns, ok := m.namespaces[name]
	if !ok {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return ns, nil
}

func (m *memNamespaceStore) ListNamespaces(_ context.Context) ([]namespace.Namespace, error) {
	out := make([]namespace.Namespace, 0, len(m.namespaces))
	for _, ns := range m.namespaces {
		out = append(out, ns)
	}

	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Name > out[j].Name; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}

	return out, nil
}

func (m *memNamespaceStore) Ping(_ context.Context) error {
	return nil
}

// errNotUsed reports a call to a stub method that these tests never make.
var errNotUsed = errors.New("not used by this test")

// memStackStore is an in-memory stack.Store.
type memStackStore struct {
	stacks map[stack.Ref]stack.Stack
}

func (m *memStackStore) CreateStack(_ context.Context, st stack.Stack, _ namespace.Fence) error {
	if _, ok := m.stacks[st.Ref]; ok {
		return stack.ErrAlreadyExists
	}

	if _, err := namespace.ParseName(st.Ref.Namespace.String()); err != nil {
		return err
	}

	m.stacks[st.Ref] = st

	return nil
}

// RenameStack is not used by the user and organization tests.
func (m *memStackStore) RenameStack(
	_ context.Context, _ stack.Ref, _ stack.Name, _ namespace.Fence, _ audit.Entry,
) error {
	return errNotUsed
}

func (m *memStackStore) DeleteStack(
	_ context.Context,
	_ stack.Ref,
	_ namespace.Fence,
	_ stack.DeleteGuard,
	_ inventory.Deletion,
) error {
	return errNotUsed
}

func (m *memStackStore) ForceDeleteStack(
	_ context.Context,
	_ stack.Ref,
	_ namespace.Fence,
	_ inventory.Deletion,
) error {
	return errNotUsed
}

// RestoreStack implements stack.Store; this fake holds no deleted stack.
func (*memStackStore) RestoreStack(
	_ context.Context, _ stack.ID, _ stack.Name, _ namespace.Fence, _ audit.Entry,
) (stack.Ref, error) {
	return stack.Ref{}, stack.ErrNotFound
}

// PurgeStack implements stack.Store; this fake holds no deleted stack.
func (*memStackStore) PurgeStack(_ context.Context, _ stack.ID, _ namespace.Fence, _ time.Time, _ audit.Entry) error {
	return stack.ErrNotFound
}

func (m *memStackStore) UpdateStackTags(
	_ context.Context, _ stack.Ref, _ map[string]string, _ time.Time, _ namespace.Fence,
) error {
	return errNotUsed
}

func (m *memStackStore) GetStack(_ context.Context, ref stack.Ref) (stack.Stack, error) {
	st, ok := m.stacks[ref]
	if !ok {
		return stack.Stack{}, stack.ErrNotFound
	}

	return st, nil
}

func (m *memStackStore) ListStacks(_ context.Context, filter stack.Filter) ([]stack.Stack, error) {
	var out []stack.Stack

	for ref := range m.stacks {
		st := m.stacks[ref]
		if st.Ref.Namespace != filter.Namespace {
			continue
		}

		if filter.Project != "" && st.Ref.Project != filter.Project {
			continue
		}

		out = append(out, st)
	}

	return out, nil
}

// QueryStacks implements stack.Store over the lists of ListStacks.
func (m *memStackStore) QueryStacks(ctx context.Context, q stack.PageQuery) ([]stack.Stack, error) {
	return stack.ListPage(ctx, m.ListStacks, q)
}

// denyBeta denies namespace:read on beta and allows everything else.
type denyBeta struct{}

func (denyBeta) Authorize(
	_ context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	if action == identity.ActionNamespaceRead && obj.Namespace().String() == "beta" {
		return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
	}

	return nil
}

// ListObjects gives an administrator every object and any other principal none.
func (denyBeta) ListObjects(_ context.Context, p identity.Principal, _ identity.Action) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

func newUserHandler(
	t *testing.T, nsStore *memNamespaceStore, stStore *memStackStore, authz identity.Authorizer,
) *pulumi.Handler {
	t.Helper()

	return newUserHandlerWithSessions(t, nsStore, stStore, authz, nil)
}

// newUserHandlerWithSessions is newUserHandler with the session cookie
// check sessions; nil takes access tokens only.
func newUserHandlerWithSessions(
	t *testing.T, nsStore *memNamespaceStore, stStore *memStackStore, authz identity.Authorizer,
	sessions *sessionauth.Checker,
) *pulumi.Handler {
	t.Helper()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: nsStore, Now: nil, Installation: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	stacks, err := stack.NewService(
		stack.Config{Store: stStore, Namespaces: namespaces, Now: nil, State: nil, Retention: 0, ManualLocks: nil},
	)
	if err != nil {
		t.Fatalf("stack service: %v", err)
	}

	handler, err := pulumi.NewHandler(pulumi.Config{
		Placer:                  nil,
		StateGuard:              stategate.Off("the test keeps no gates"),
		SessionPolicy:           nil,
		DisablePulumiJournaling: false,
		Stacks:                  stacks,
		Updates:                 newUnusedUpdates(t, namespaces),
		Secrets:                 newTestSecrets(t, namespaces),
		Namespaces:              namespaces,
		Authenticator:           tokenAuthenticator{},
		Authorizer:              authz,
		Sessions:                sessions,
		AddressGate:             nil,
		Logger:                  slog.New(slog.DiscardHandler),
		BaseURL:                 baseurl.Resolver{},
		MaxBodyBytesInFlight:    0,
		Gate:                    nil,
		Audit:                   newTestAudit(),
		OnAuditFailure:          nil,
		CheckpointDeltaCutoff:   0,
		DenialLimiter:           nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

func seedNamespaces(t *testing.T, nsStore *memNamespaceStore, names ...string) {
	t.Helper()

	for _, name := range names {
		seedNamespace(t, nsStore, name)
	}
}

// seedNamespace creates one namespace whose primary installation is the test installation.
func seedNamespace(t *testing.T, nsStore *memNamespaceStore, name string) {
	t.Helper()

	n, err := namespace.ParseName(name)
	if err != nil {
		t.Fatalf("parse namespace: %v", err)
	}

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.Now(),
		Name:                n,
		PrimaryInstallation: "test-region",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := nsStore.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
}

func seedStack(t *testing.T, stStore *memStackStore, ns, project, name string) {
	t.Helper()

	ref, err := stack.ParseRef(ns, project, name)
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	stStore.stacks[ref] = stack.Stack{
		NamespaceID: "",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Tags:        map[string]string{},
		Ref:         ref,
		ID:          "stk-test",
		Version:     0,
	}
}

func get(t *testing.T, handler *pulumi.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	req.Header.Set("Authorization", "token test-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	return rec
}

// userBody is the decoded GET /api/user response in these tests.
type userBody struct {
	GithubLogin   string             `json:"githubLogin"`
	Organizations []organizationBody `json:"organizations"`
}

// organizationBody is one decoded organization entry.
type organizationBody struct {
	GithubLogin string `json:"githubLogin"`
	Name        string `json:"name"`
}

func TestCurrentUserListsOrganizations(t *testing.T) {
	t.Parallel()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	stStore := &memStackStore{stacks: map[stack.Ref]stack.Stack{}}
	seedNamespaces(t, nsStore, "beta", "acme")

	handler := newUserHandler(t, nsStore, stStore, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	rec := get(t, handler, "/api/user")
	if rec.Code != http.StatusOK {
		t.Fatalf("user answers %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got userBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode user: %v", err)
	}

	if len(got.Organizations) != 2 ||
		got.Organizations[0].GithubLogin != "acme" || got.Organizations[0].Name != "acme" ||
		got.Organizations[1].GithubLogin != "beta" || got.Organizations[1].Name != "beta" {
		t.Fatalf("organizations = %+v, want acme then beta", got.Organizations)
	}
}

func TestCurrentUserFiltersDeniedNamespaces(t *testing.T) {
	t.Parallel()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	stStore := &memStackStore{stacks: map[stack.Ref]stack.Stack{}}
	seedNamespaces(t, nsStore, "beta", "acme")

	handler := newUserHandler(t, nsStore, stStore, denyBeta{})

	rec := get(t, handler, "/api/user")
	if rec.Code != http.StatusOK {
		t.Fatalf("user answers %d, want %d", rec.Code, http.StatusOK)
	}

	if body := rec.Body.String(); !strings.Contains(body, "acme") || strings.Contains(body, "beta") {
		t.Fatalf("user body = %s, want acme without beta", body)
	}
}

func TestDefaultOrganization(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		namespaces []string
		wantStatus int
		wantLogin  string
	}{
		{"one namespace", []string{"acme"}, http.StatusOK, "acme"},
		{"no namespace", nil, http.StatusNotFound, ""},
		{"two namespaces", []string{"acme", "beta"}, http.StatusNotFound, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
			stStore := &memStackStore{stacks: map[stack.Ref]stack.Stack{}}
			seedNamespaces(t, nsStore, tc.namespaces...)

			handler := newUserHandler(t, nsStore, stStore, selectiveAuthorizer{deny: map[identity.Action]bool{}})

			rec := get(t, handler, "/api/user/organizations/default")
			if rec.Code != tc.wantStatus {
				t.Fatalf("default answers %d, want %d (%s)", rec.Code, tc.wantStatus, rec.Body.String())
			}

			if tc.wantStatus != http.StatusOK {
				return
			}

			var got defaultBody
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode default: %v", err)
			}

			if got.GithubLogin != tc.wantLogin {
				t.Fatalf("default = %q, want %q", got.GithubLogin, tc.wantLogin)
			}
		})
	}
}

// defaultBody is the decoded default-organization response.
type defaultBody struct {
	GithubLogin string `json:"GitHubLogin"` //nolint:tagliatelle // The Pulumi CLI's wire name; it must not change.
}

// stackEntry is one decoded stack summary.
type stackEntry struct {
	OrgName   string `json:"orgName"`
	StackName string `json:"stackName"`
}

// stackListBody is the decoded GET /api/user/stacks response.
type stackListBody struct {
	Stacks []stackEntry `json:"stacks"`
}

func TestListStacksAcrossNamespaces(t *testing.T) {
	t.Parallel()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	stStore := &memStackStore{stacks: map[stack.Ref]stack.Stack{}}
	seedNamespaces(t, nsStore, "acme", "beta")
	seedStack(t, stStore, "acme", "web", "prod")
	seedStack(t, stStore, "beta", "web", "dev")

	handler := newUserHandler(t, nsStore, stStore, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	rec := get(t, handler, "/api/user/stacks")
	if rec.Code != http.StatusOK {
		t.Fatalf("list answers %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got stackListBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode stacks: %v", err)
	}

	if len(got.Stacks) != 2 {
		t.Fatalf("stacks = %+v, want 2", got.Stacks)
	}

	rec = get(t, handler, "/api/user/stacks?organization=beta")
	if rec.Code != http.StatusOK {
		t.Fatalf("filtered list answers %d, want %d", rec.Code, http.StatusOK)
	}

	if body := rec.Body.String(); !strings.Contains(body, `"orgName":"beta"`) ||
		strings.Contains(body, `"orgName":"acme"`) {
		t.Fatalf("filtered body = %s, want only beta", body)
	}
}

func TestMissingSubjectNamespaceNamesTheFix(t *testing.T) {
	t.Parallel()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	stStore := &memStackStore{stacks: map[stack.Ref]stack.Stack{}}
	seedNamespaces(t, nsStore, "acme")

	handler := newUserHandler(t, nsStore, stStore, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	// The test principal is "tester": a missing "tester" namespace names the fix.
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/api/stacks/tester/web", strings.NewReader(`{"stackName":"prod"}`),
	)
	req.Header.Set("Authorization", "token test-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("create answers %d, want %d (%s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}

	if body := rec.Body.String(); !strings.Contains(body, "pulumi org set-default") {
		t.Fatalf("create body = %s, want the set-default fix", body)
	}

	// A missing namespace that is not the subject answers 404 without the fix.
	req = httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/api/stacks/ghost/web", strings.NewReader(`{"stackName":"prod"}`),
	)
	req.Header.Set("Authorization", "token test-token")
	rec = httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("create answers %d, want %d", rec.Code, http.StatusNotFound)
	}

	if body := rec.Body.String(); strings.Contains(body, "pulumi org set-default") {
		t.Fatalf("create body = %s, want no set-default fix", body)
	}
}

// SetOverrides is not used by this fake.
func (*memNamespaceStore) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}
