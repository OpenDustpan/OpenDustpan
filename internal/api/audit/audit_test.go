package audit_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	apitaudit "opendustpan.org/opendustpan/internal/api/audit"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// memNamespaceStore is an in-memory namespace.Store.
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

	slices.SortFunc(out, func(a, b namespace.Namespace) int {
		return strings.Compare(a.Name.String(), b.Name.String())
	})

	return out, nil
}

func (m *memNamespaceStore) Ping(_ context.Context) error {
	return nil
}

// memAuditStore is an in-memory audit.Store. It chains appends of one
// namespace exactly as the port requires, so handler tests read realistic
// entries. Failures are injected through err.
type memAuditStore struct {
	entries map[namespace.Name][]audit.Entry
	err     error
}

var _ audit.Store = (*memAuditStore)(nil)

func (m *memAuditStore) Append(_ context.Context, fence namespace.Fence, e audit.Entry) (audit.Entry, error) {
	if m.err != nil {
		return audit.Entry{}, m.err
	}

	if err := e.Validate(); err != nil {
		return audit.Entry{}, err
	}

	if e.Namespace != fence.Namespace {
		return audit.Entry{}, fmt.Errorf("%w: namespace mismatch", audit.ErrInvalidEntry)
	}

	chain := m.entries[e.Namespace]

	var prevHash string

	if len(chain) > 0 {
		prevHash = chain[len(chain)-1].Hash
	}

	e.Sequence = int64(len(chain)) + 1
	e.PrevHash = prevHash
	e.Hash = audit.ComputeHash(e, prevHash)
	m.entries[e.Namespace] = append(chain, e)

	return e, nil
}

func (m *memAuditStore) List(
	_ context.Context, ns namespace.Name, page audit.Page,
) (audit.Listing, error) {
	if m.err != nil {
		return audit.Listing{}, m.err
	}

	p := page.Normalized()
	chain := m.entries[ns]

	out := make([]audit.Entry, 0, min(int64(len(chain)), int64(p.Size)))
	for i := len(chain) - 1; i >= 0 && len(out) < p.Size; i-- {
		if int64(len(chain)-1-i) >= p.Offset() {
			out = append(out, chain[i])
		}
	}

	return audit.Listing{Entries: out, Total: int64(len(chain))}, nil
}

func (m *memAuditStore) Check(_ context.Context, ns namespace.Name) (audit.CheckResult, error) {
	if m.err != nil {
		return audit.CheckResult{}, m.err
	}

	chain := m.entries[ns]

	var prevHash string

	var wantSeq int64 = 1

	for i := range chain {
		e := &chain[i]
		if e.Sequence != wantSeq || e.PrevHash != prevHash || audit.ComputeHash(*e, e.PrevHash) != e.Hash {
			return audit.CheckResult{Checked: int64(len(chain)), BadSequence: e.Sequence, Valid: false}, nil
		}

		prevHash = e.Hash
		wantSeq++
	}

	return audit.CheckResult{Checked: int64(len(chain)), BadSequence: 0, Valid: true}, nil
}

// changeEntrySubject rewrites the subject of one stored entry behind the
// back of the store, so the check test reads a broken chain.
func (m *memAuditStore) changeEntrySubject(ns namespace.Name, seq int64, subject string) {
	for i := range m.entries[ns] {
		if m.entries[ns][i].Sequence == seq {
			m.entries[ns][i].Subject = subject
		}
	}
}

// stubAuthenticator denies every token. Tests that send no token never reach it.
type stubAuthenticator struct{}

func (stubAuthenticator) AuthenticateToken(_ context.Context, _ string) (identity.Principal, error) {
	return identity.Principal{}, identity.ErrUnauthenticated
}

// tokenAuthenticator signs in exactly the test token.
type tokenAuthenticator struct{}

func (tokenAuthenticator) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	if token != "test-token" {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return identity.Principal{
		Subject:        "tester",
		DisplayName:    "Tester",
		Admin:          false,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
	}, nil
}

// stubAuthorizer denies every action.
type stubAuthorizer struct{}

func (stubAuthorizer) Authorize(
	_ context.Context, _ identity.Principal, _ identity.Action, _ identity.Object,
) error {
	return identity.ErrDenied
}

// selectiveAuthorizer denies exactly the actions in deny and allows the rest.
type selectiveAuthorizer struct {
	deny map[identity.Action]bool
}

func (s selectiveAuthorizer) Authorize(
	_ context.Context, _ identity.Principal, action identity.Action, _ identity.Object,
) error {
	if s.deny[action] {
		return fmt.Errorf("%w: test denies %s", identity.ErrDenied, action)
	}

	return nil
}

// changeTime is a fixed instant for seeded entries.
func changeTime() time.Time {
	return time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC)
}

// seedEntry appends one reveal entry with subject to ns.
func seedEntry(t *testing.T, store *memAuditStore, ns namespace.Name, subject string) audit.Entry {
	t.Helper()

	stored, err := store.Append(t.Context(), namespace.Fence{Namespace: ns, FailoverVersion: 1}, audit.Entry{
		At:         changeTime(),
		Namespace:  ns,
		Subject:    subject,
		IDPSubject: "",
		TokenID:    "",
		Action:     "unit:decrypt",
		Object:     "pulumi:" + ns.String() + "/web/prod",
		Version:    "",
		ValuePath:  "",
		ValueCount: nil,
		SourceIP:   "192.0.2.1",
		UserAgent:  "pulumi-cli/3.200.0",
		Channel:    audit.ChannelAPI,
		Kind:       audit.KindReveal,
		Outcome:    audit.OutcomeSuccess,
		RequestID:  "req-" + subject,
		PrevHash:   "",
		Hash:       "",
		Sequence:   0,
	})
	if err != nil {
		t.Fatalf("seed audit entry: %v", err)
	}

	return stored
}

// newTestHandler builds a Handler over empty stores that denies every
// action, so behavior tests replace what they need.
func newTestHandler(t *testing.T) *apitaudit.Handler {
	t.Helper()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	entries := &memAuditStore{entries: map[namespace.Name][]audit.Entry{}, err: nil}

	namespaces, err := namespace.NewService(
		namespace.Config{Store: nsStore, Now: nil, Region: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := apitaudit.NewHandler(apitaudit.Config{
		Namespaces:    namespaces,
		Entries:       entries,
		Authenticator: stubAuthenticator{},
		Authorizer:    stubAuthorizer{},
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// newEnforcingHandler builds a Handler whose namespace reads succeed, so
// the enforcement tests can tell authorization apart from backend failures.
func newEnforcingHandler(
	t *testing.T, authz identity.Authorizer,
) (*apitaudit.Handler, *memAuditStore) {
	t.Helper()

	entries := &memAuditStore{entries: map[namespace.Name][]audit.Entry{}, err: nil}

	namespaces, err := namespace.NewService(
		namespace.Config{Store: &homedNamespaceStore{}, Now: nil, Region: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := apitaudit.NewHandler(apitaudit.Config{
		Namespaces:    namespaces,
		Entries:       entries,
		Authenticator: tokenAuthenticator{},
		Authorizer:    authz,
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler, entries
}

// homedNamespaceStore answers every namespace homed in the test region, so
// the enforcement tests reach the store instead of failing on the region.
type homedNamespaceStore struct{}

func (homedNamespaceStore) CreateNamespace(_ context.Context, _ namespace.Namespace) error {
	return nil
}

func (homedNamespaceStore) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	var out namespace.Namespace

	out.Name = name
	out.HomeRegion = "test-region"
	out.FailoverVersion = 1

	return out, nil
}

func (homedNamespaceStore) ListNamespaces(_ context.Context) ([]namespace.Namespace, error) {
	ns := namespace.Namespace{
		CreatedAt:       changeTime(),
		Name:            "acme",
		HomeRegion:      "test-region",
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}

	return []namespace.Namespace{ns}, nil
}

func (homedNamespaceStore) Ping(_ context.Context) error {
	return nil
}

// authedRequest builds a signed-in GET request for target.
func authedRequest(t *testing.T, target string) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	req.Header.Set("Authorization", "token test-token")

	return req
}

// listEntriesItem is one decoded entry of the entry list test.
type listEntriesItem struct {
	Subject   string `json:"subject"`
	Sequence  int64  `json:"sequence"`
	Kind      string `json:"kind"`
	AtMs      int64  `json:"atMs"`
	SourceIP  string `json:"sourceIP"`
	RequestID string `json:"requestID"`
	Hash      string `json:"hash"`
}

// listEntriesDoc is the decoded entry list of the entry list test.
type listEntriesDoc struct {
	Entries []listEntriesItem `json:"entries"`
	Total   int64             `json:"total"`
}

// listPageItem is one decoded entry of the paging test.
type listPageItem struct {
	Subject string `json:"subject"`
}

// listPageDoc is the decoded entry list of the paging test.
type listPageDoc struct {
	Entries []listPageItem `json:"entries"`
	Total   int64          `json:"total"`
}

// TestListEntriesAnswersNewestFirstWithTotal proves the list answers the
// entries of the namespace newest first with the total count and the entry
// fields in camelCase.
func TestListEntriesAnswersNewestFirstWithTotal(t *testing.T) {
	t.Parallel()

	handler, entries := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("parse namespace: %v", err)
	}

	seedEntry(t, entries, ns, "first")
	seedEntry(t, entries, ns, "second")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(t, "/api/audit/entries?organization=acme"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got listEntriesDoc

	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	checkEntriesNewestFirst(t, got)
	checkListedFirstEntry(t, got.Entries[1])
}

// checkEntriesNewestFirst fails the test unless got holds two entries with
// the total, newest first.
func checkEntriesNewestFirst(t *testing.T, got listEntriesDoc) {
	t.Helper()

	if got.Total != 2 || len(got.Entries) != 2 {
		t.Fatalf("entries/total = %d/%d, want 2/2", len(got.Entries), got.Total)
	}

	if got.Entries[0].Subject != "second" || got.Entries[1].Subject != "first" {
		t.Fatalf("order = %q, %q, want second then first", got.Entries[0].Subject, got.Entries[1].Subject)
	}
}

// checkListedFirstEntry fails the test unless first carries the sequence,
// kind, time, source, request and hash of a stored entry.
func checkListedFirstEntry(t *testing.T, first listEntriesItem) {
	t.Helper()

	if first.Sequence != 1 || first.Kind != "reveal" || first.AtMs == 0 ||
		first.SourceIP == "" || first.RequestID == "" || first.Hash == "" {
		t.Fatalf("entry = %+v, want sequence, kind, time, source, request and hash", first)
	}
}

// TestListEntriesPages proves page[size]=1 answers one entry per page.
func TestListEntriesPages(t *testing.T) {
	t.Parallel()

	handler, entries := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("parse namespace: %v", err)
	}

	seedEntry(t, entries, ns, "first")
	seedEntry(t, entries, ns, "second")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(
		rec,
		authedRequest(t, "/api/audit/entries?organization=acme&page[number]=2&page[size]=1"),
	)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got listPageDoc

	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	if got.Total != 2 || len(got.Entries) != 1 || got.Entries[0].Subject != "first" {
		t.Fatalf("page 2 = %+v total %d, want first with total 2", got.Entries, got.Total)
	}
}

// TestListEntriesRejectsAnInvalidPage proves a non-numeric page answers 400.
func TestListEntriesRejectsAnInvalidPage(t *testing.T) {
	t.Parallel()

	handler, _ := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(
		rec,
		authedRequest(t, "/api/audit/entries?organization=acme&page[size]=nope"),
	)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// TestListEntriesOfAnUnknownNamespaceAnswers404 proves the list of a
// namespace that does not exist answers 404.
func TestListEntriesOfAnUnknownNamespaceAnswers404(t *testing.T) {
	t.Parallel()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	entries := &memAuditStore{entries: map[namespace.Name][]audit.Entry{}, err: nil}

	namespaces, err := namespace.NewService(
		namespace.Config{Store: nsStore, Now: nil, Region: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := apitaudit.NewHandler(apitaudit.Config{
		Namespaces:    namespaces,
		Entries:       entries,
		Authenticator: tokenAuthenticator{},
		Authorizer:    selectiveAuthorizer{deny: map[identity.Action]bool{}},
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(t, "/api/audit/entries?organization=ghost"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// TestCheckChainAnswersValid proves the check answers a valid chain.
func TestCheckChainAnswersValid(t *testing.T) {
	t.Parallel()

	handler, entries := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("parse namespace: %v", err)
	}

	seedEntry(t, entries, ns, "first")
	seedEntry(t, entries, ns, "second")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(t, "/api/audit/check?organization=acme"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got struct {
		Valid          bool  `json:"valid"`
		EntriesChecked int64 `json:"entriesChecked"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	if !got.Valid || got.EntriesChecked != 2 {
		t.Fatalf("check = %+v, want valid with 2 entries", got)
	}
}

// TestCheckChainFindsAChangedEntry proves the check answers the sequence of
// the changed entry.
func TestCheckChainFindsAChangedEntry(t *testing.T) {
	t.Parallel()

	handler, entries := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("parse namespace: %v", err)
	}

	seedEntry(t, entries, ns, "first")
	seedEntry(t, entries, ns, "second")
	entries.changeEntrySubject(ns, 1, "mallory")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(t, "/api/audit/check?organization=acme"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (%s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got struct {
		Valid       bool  `json:"valid"`
		BadSequence int64 `json:"badSequence"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	if got.Valid || got.BadSequence != 1 {
		t.Fatalf("check = %+v, want invalid with bad sequence 1", got)
	}
}

// SetOverrides is not used by this fake.
func (*memNamespaceStore) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}

// SetOverrides is not used by this fake.
func (homedNamespaceStore) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}
