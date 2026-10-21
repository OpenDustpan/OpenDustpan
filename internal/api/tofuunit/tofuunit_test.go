package tofuunit_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/tofuunit"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

var (
	errNoNamespace = errors.New("namespace read failed")
	errAuthzDown   = errors.New("authorizer failed")
	errAppend      = errors.New("append failed")
)

// namespaces answers one namespace with the given switches, or err.
type namespaces struct {
	overrides namespace.Overrides
	err       error
}

func (n namespaces) Get(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	var ns namespace.Namespace

	if n.err != nil {
		return ns, n.err
	}

	ns.Name, ns.Overrides, ns.FailoverVersion = name, n.overrides, 1

	return ns, nil
}

// authorizer allows the actions in allow, answers err for every other
// action when err is set, and denies the rest.
type authorizer struct {
	identity.Authorizer

	allow map[identity.Action]bool
	err   error
}

func (a authorizer) Authorize(
	_ context.Context,
	_ identity.Principal,
	action identity.Action,
	_ identity.Object,
) error {
	switch {
	case a.allow[action]:
		return nil
	case a.err != nil:
		return a.err
	default:
		return identity.ErrDenied
	}
}

// store keeps the entries that it appends, or answers err.
type store struct {
	audit.Store

	mu      sync.Mutex
	entries []audit.Entry
	err     error
}

func (s *store) Append(_ context.Context, _ namespace.Fence, e audit.Entry) (audit.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.err != nil {
		var none audit.Entry

		return none, s.err
	}

	s.entries = append(s.entries, e)

	return e, nil
}

func (s *store) all() []audit.Entry {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]audit.Entry(nil), s.entries...)
}

func newNamespaces(ov namespace.Overrides, err error) namespaces {
	var n namespaces

	n.overrides, n.err = ov, err

	return n
}

func newAuthorizer(allow map[identity.Action]bool, err error) authorizer {
	var a authorizer

	a.allow, a.err = allow, err

	return a
}

func newStore(err error) *store {
	var s store

	s.err = err

	return &s
}

func fixture(t *testing.T) (workspace.Workspace, identity.Object, identity.Principal) {
	t.Helper()

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("parse namespace: %v", err)
	}

	name, err := workspace.ParseName("web")
	if err != nil {
		t.Fatalf("parse workspace name: %v", err)
	}

	var w workspace.Workspace

	w.Ref = workspace.Ref{Namespace: ns, Name: name}

	obj, err := tofuunit.Object(w.Ref)
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	var p identity.Principal

	p.Subject, p.TokenID, p.Kind, p.JobID = "ana", "tok-1", identity.KindPerson, "job-1"

	return w, obj, p
}

func rules(ns namespaces, authz authorizer, st *store) tofuunit.Rules {
	var guard *refusal.Recorder
	if st != nil {
		guard = refusal.New(authz, st, ns, slog.New(slog.DiscardHandler), nil)
	}

	var auditStore audit.Store
	if st != nil {
		auditStore = st
	}

	return tofuunit.Rules{Authz: authz, Namespaces: ns, Audit: auditStore, Guard: guard}
}

// TestAllowanceNeedsBothSwitchesAndTheAction checks each layer of the
// allowance alone: the namespace switch, the unit switch and the action. An
// error of the authorizer denies.
func TestAllowanceNeedsBothSwitchesAndTheAction(t *testing.T) {
	t.Parallel()

	on := namespace.Overrides{Forced: true, Unlocked: true}
	off := namespace.Overrides{Forced: false, Unlocked: false}
	both := map[identity.Action]bool{identity.ActionUnitWriteForced: true, identity.ActionUnitWriteUnlocked: true}

	cases := []struct {
		name   string
		nsOn   namespace.Overrides
		unitOn namespace.Overrides
		authz  authorizer
		want   namespace.Overrides
	}{
		{"all on", on, on, newAuthorizer(both, nil), on},
		{"namespace switch off", off, on, newAuthorizer(both, nil), off},
		{"unit switch off", on, off, newAuthorizer(both, nil), off},
		{"no action", on, on, newAuthorizer(nil, nil), off},
		{"authorizer fails", on, on, newAuthorizer(nil, errAuthzDown), off},
		{
			"forced only", on, on,
			newAuthorizer(map[identity.Action]bool{identity.ActionUnitWriteForced: true}, nil),
			namespace.Overrides{Forced: true, Unlocked: false},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w, obj, p := fixture(t)
			w.Overrides = tc.unitOn

			got, err := rules(newNamespaces(tc.nsOn, nil), tc.authz, nil).Allowance(t.Context(), p, w, obj)
			if err != nil {
				t.Fatalf("Allowance: %v", err)
			}

			if got != tc.want {
				t.Fatalf("Allowance = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestAllowanceNamespaceErrorRefuses checks that a failed namespace read
// returns its error and allows no override.
func TestAllowanceNamespaceErrorRefuses(t *testing.T) {
	t.Parallel()

	w, obj, p := fixture(t)

	got, err := rules(
		newNamespaces(namespace.Overrides{Forced: false, Unlocked: false}, errNoNamespace),
		newAuthorizer(nil, nil),
		nil,
	).Allowance(t.Context(), p, w, obj)
	if !errors.Is(err, errNoNamespace) {
		t.Fatalf("Allowance error = %v, want the namespace error", err)
	}

	if got.Forced || got.Unlocked {
		t.Fatalf("Allowance = %+v, want no override", got)
	}
}

// TestOverrideAction checks that only the unlocked override maps to the
// unlocked action, so an unknown value reads as the stronger forced write.
func TestOverrideAction(t *testing.T) {
	t.Parallel()

	cases := map[workspace.Override]identity.Action{
		workspace.OverrideUnlocked: identity.ActionUnitWriteUnlocked,
		workspace.OverrideForced:   identity.ActionUnitWriteForced,
		workspace.Override(0):      identity.ActionUnitWriteForced,
		workspace.Override(99):     identity.ActionUnitWriteForced,
	}

	for override, want := range cases {
		if got := tofuunit.OverrideAction(override); got != want {
			t.Errorf("OverrideAction(%d) = %q, want %q", override, got, want)
		}
	}
}

// TestRecordOverrideRefusal checks that only an override refusal writes a
// record, with the action of the typed refusal, and forced for a refusal
// without a type.
func TestRecordOverrideRefusal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want identity.Action
	}{
		{"other error", errAppend, ""},
		{"untyped refusal", fmt.Errorf("write: %w", workspace.ErrOverrideRefused), identity.ActionUnitWriteForced},
		{
			"unlocked refusal",
			workspace.NewOverrideRefusedError(workspace.OverrideUnlocked, "refused"),
			identity.ActionUnitWriteUnlocked,
		},
		{
			"forced refusal",
			workspace.NewOverrideRefusedError(workspace.OverrideForced, "refused"),
			identity.ActionUnitWriteForced,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, obj, p := fixture(t)
			st := newStore(nil)
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/x", http.NoBody)

			rules(
				newNamespaces(namespace.Overrides{Forced: false, Unlocked: false}, nil),
				newAuthorizer(nil, nil),
				st,
			).RecordOverrideRefusal(t.Context(), r, p, obj, tc.err)

			entries := st.all()

			if tc.want == "" {
				if len(entries) != 0 {
					t.Fatalf("entries = %d, want none", len(entries))
				}

				return
			}

			if len(entries) != 1 || entries[0].Action != string(tc.want) {
				t.Fatalf("entries = %+v, want one with the action %q", entries, tc.want)
			}
		})
	}
}

// TestEntryFillsRequestFacts checks the facts that Entry takes from the
// request: an unknown channel reads as api, and a long user agent is cut on a
// character boundary.
func TestEntryFillsRequestFacts(t *testing.T) {
	t.Parallel()

	_, obj, p := fixture(t)

	ctx := requestid.WithContext(t.Context(), "req-1")
	r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/x", http.NoBody)
	r.RemoteAddr = "192.0.2.7:4000"
	r.Header.Set("User-Agent", strings.Repeat("é", audit.MaxUserAgentLen))
	r.Header.Set(tofuunit.ChannelHeader, "not-a-channel\r\nX: y")

	at := time.Unix(1700000000, 0).UTC()

	e := tofuunit.Entry(r, p, obj, tofuunit.Detail{
		At: at, Action: identity.ActionUnitReadSecrets, Version: "7", ValuePath: "a.b",
		Kind: audit.KindReveal, Outcome: audit.OutcomeSuccess, Reason: "why",
	})

	if e.Channel != audit.ChannelAPI {
		t.Errorf("channel = %q, want api", e.Channel)
	}

	if len(e.UserAgent) > audit.MaxUserAgentLen || !utf8.ValidString(e.UserAgent) {
		t.Errorf("user agent is %d bytes, valid UTF-8 %t; want at most %d bytes of valid UTF-8",
			len(e.UserAgent), utf8.ValidString(e.UserAgent), audit.MaxUserAgentLen)
	}

	checks := []struct {
		name      string
		got, want string
	}{
		{"namespace", e.Namespace.String(), "acme"},
		{"subject", e.Subject, "ana"},
		{"token", e.TokenID, "tok-1"},
		{"object", e.Object, obj.String()},
		{"source ip", e.SourceIP, "192.0.2.7"},
		{"request id", e.RequestID, "req-1"},
		{"principal kind", e.PrincipalKind, string(identity.KindPerson)},
		{"job", e.JobID, "job-1"},
		{"action", e.Action, string(identity.ActionUnitReadSecrets)},
		{"version", e.Version, "7"},
		{"value path", e.ValuePath, "a.b"},
		{"reason", e.Reason, "why"},
		{"kind", string(e.Kind), string(audit.KindReveal)},
	}

	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}

	if !e.At.Equal(at) {
		t.Errorf("at = %v, want %v", e.At, at)
	}
}

// TestReadKind checks that only a person reads with the kind reveal.
func TestReadKind(t *testing.T) {
	t.Parallel()

	var person, service identity.Principal

	person.Kind, service.Kind = identity.KindPerson, identity.KindService

	if got := tofuunit.ReadKind(person); got != audit.KindReveal {
		t.Errorf("ReadKind(person) = %q, want reveal", got)
	}

	if got := tofuunit.ReadKind(service); got != audit.KindToolOperation {
		t.Errorf("ReadKind(service) = %q, want tool-operation", got)
	}
}

// TestAppend checks that Append writes the entry, writes nothing without a
// store, and returns the error of the namespace read and of the append.
func TestAppend(t *testing.T) {
	t.Parallel()

	_, obj, p := fixture(t)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", http.NoBody)
	entry := tofuunit.Entry(r, p, obj, tofuunit.Detail{
		At: time.Now(), Action: identity.ActionUnitReadSecrets, Version: "", ValuePath: "",
		Kind: audit.KindToolOperation, Outcome: audit.OutcomeSuccess, Reason: "",
	})

	none := namespace.Overrides{Forced: false, Unlocked: false}
	allow := newAuthorizer(nil, nil)

	st := newStore(nil)

	err := rules(newNamespaces(none, nil), allow, st).Append(t.Context(), entry)
	if err != nil || len(st.all()) != 1 {
		t.Fatalf("Append: err %v, %d entries; want nil and one entry", err, len(st.all()))
	}

	err = rules(newNamespaces(none, nil), allow, nil).Append(t.Context(), entry)
	if err != nil {
		t.Fatalf("Append without a store: %v, want nil", err)
	}

	err = rules(newNamespaces(none, errNoNamespace), allow, newStore(nil)).Append(t.Context(), entry)
	if !errors.Is(err, errNoNamespace) {
		t.Fatalf("Append with a failed namespace read: %v, want the namespace error", err)
	}

	err = rules(newNamespaces(none, nil), allow, newStore(errAppend)).Append(t.Context(), entry)
	if !errors.Is(err, errAppend) {
		t.Fatalf("Append with a failed store: %v, want the store error", err)
	}
}
