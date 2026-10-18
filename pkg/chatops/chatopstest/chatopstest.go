// Package chatopstest is the conformance suite for chatops.Links,
// chatops.LinkRequests, chatops.Intents and chatops.Verifier
// implementations. Every store adapter runs Run:
//
//	func TestChatOpsConformance(t *testing.T) {
//		t.Parallel()
//		chatopstest.Run(t, func(t *testing.T) chatopstest.Harness { return newHarness(t) })
//	}
//
// Every adapter of a chat tool runs RunVerifier. A reference adapter in
// memory and a fake adapter of a chat tool, in the tests of this package,
// run the suites, so each case runs before the first real adapter.
package chatopstest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/chatops"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Harness is one fresh, empty database for one case.
type Harness struct {
	Links        chatops.Links
	LinkRequests chatops.LinkRequests
	Intents      chatops.Intents
	Domain       Domain
}

// Domain creates the namespaces of a case and reads the audit entries
// that the stores append.
type Domain interface {
	// CreateNamespace creates the namespace name and returns its fence.
	CreateNamespace(ctx context.Context, name namespace.Name) (namespace.Fence, error)
	// BumpFence increments the failover version of the namespace of
	// fence, as a failover does, and returns the new fence.
	BumpFence(ctx context.Context, fence namespace.Fence) (namespace.Fence, error)
	// AuditEntries returns the audit entries of scope, in append order.
	AuditEntries(ctx context.Context, scope namespace.Name) ([]audit.Entry, error)
}

// Factory returns a fresh harness for one case.
type Factory func(t *testing.T) Harness

// Run executes the conformance suite against harnesses from newHarness.
func Run(t *testing.T, newHarness Factory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, h Harness)
	}{
		{"a link is stored with its audit entry", testLinkCreate},
		{"a link never replaces another link silently", testLinkNoSilentReplace},
		{"a replace of a missing link is a conflict", testLinkReplaceMissing},
		{"the IDs of a chat user match exactly", testLinkExactIDs},
		{"a link with an empty field is invalid", testLinkInvalid},
		{"links of a principal list each chat user", testLinksOf},
		{"delete of a link", testUnlink},
		{"link writes take the zero fence only", testLinkFences},
		{"a link request is used once", testLinkRequestOnce},
		{"an expired link request cannot be used", testLinkRequestExpired},
		{"a second link request with the same hash is a conflict", testLinkRequestConflict},
		{"an invalid link request is refused", testLinkRequestInvalid},
		{"link request writes take the zero fence only", testLinkRequestFences},
		{"an intent is used once", testIntentOnce},
		{"an expired intent cannot be used", testIntentExpired},
		{"a second intent with the same ID is a conflict", testIntentConflict},
		{"an invalid intent is refused", testIntentInvalid},
		{"intent writes take the fence of the namespace of the job", testIntentFences},
		{"an audit entry of another scope stops the write", testEntryScope},
		{"a repeated approve request changes no stored intent", testRepeatedRequest},
		{"the intents of a deleted link are deleted with the fence of their namespace", testDeleteIntents},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newHarness(t))
		})
	}
}

const (
	shop = namespace.Name("shop")
	bank = namespace.Name("bank")
)

func now() time.Time { return time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC) }

func zero() namespace.Fence { return namespace.Fence{Namespace: "", FailoverVersion: 0} }

// entry returns a valid audit entry of the kind change in scope.
func entry(scope namespace.Name, action string) audit.Entry {
	return audit.Entry{
		At: now(), Namespace: scope, Subject: "alice", IDPSubject: "", TokenID: "", Action: action,
		Object: "@caller", Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1",
		UserAgent: "suite", Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess,
		RequestID: "req-suite", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0,
	}
}

func linkEntry() audit.Entry { return entry(audit.ServerScope, "caller:manage-chat-links") }

func chatUser(workspace, user string) chatops.ChatUser {
	return chatops.ChatUser{Platform: "chat-a", Workspace: workspace, User: user}
}

func link(user, principal string) chatops.Link {
	return chatops.Link{Platform: "chat-a", Workspace: "T1", User: user, Principal: principal, LinkedAt: now()}
}

func put(t *testing.T, h Harness, l chatops.Link, previous string) {
	t.Helper()

	if err := h.Links.PutLink(t.Context(), zero(), l, previous, linkEntry()); err != nil {
		t.Fatalf("PutLink: %v", err)
	}
}

func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("%s: err = %v, want %v", what, err, want)
	}
}

func wantEntries(t *testing.T, h Harness, scope namespace.Name, n int) {
	t.Helper()

	entries, err := h.Domain.AuditEntries(t.Context(), scope)
	if err != nil || len(entries) != n {
		t.Fatalf("audit entries of %s = %d (%v), want %d", scope, len(entries), err, n)
	}
}

func wantPrincipal(t *testing.T, h Harness, user, principal string) {
	t.Helper()

	got, err := h.Links.LinkOf(t.Context(), chatUser("T1", user))
	if err != nil || got.Principal != principal {
		t.Fatalf("LinkOf %s = %+v (%v), want %s", user, got, err, principal)
	}
}

// fences holds the current fence of shop and a stale fence of shop from
// before a failover.
type fences struct{ current, stale namespace.Fence }

// namespaceFences creates shop and makes one failover.
func namespaceFences(t *testing.T, h Harness) fences {
	t.Helper()

	stale, err := h.Domain.CreateNamespace(t.Context(), shop)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	current, err := h.Domain.BumpFence(t.Context(), stale)
	if err != nil {
		t.Fatalf("BumpFence: %v", err)
	}

	return fences{current: current, stale: stale}
}

func testLinkCreate(t *testing.T, h Harness) {
	t.Helper()

	put(t, h, link("U1", "alice"), "")
	wantPrincipal(t, h, "U1", "alice")
	wantEntries(t, h, audit.ServerScope, 1)
}

func testLinkNoSilentReplace(t *testing.T, h Harness) {
	t.Helper()

	put(t, h, link("U1", "alice"), "")

	err := h.Links.PutLink(t.Context(), zero(), link("U1", "bob"), "", linkEntry())
	wantErr(t, "PutLink over a link", err, chatops.ErrConflict)

	err = h.Links.PutLink(t.Context(), zero(), link("U1", "bob"), "carol", linkEntry())
	wantErr(t, "PutLink with another previous principal", err, chatops.ErrConflict)

	wantPrincipal(t, h, "U1", "alice")
	wantEntries(t, h, audit.ServerScope, 1)

	put(t, h, link("U1", "bob"), "alice")
	wantPrincipal(t, h, "U1", "bob")
	wantEntries(t, h, audit.ServerScope, 2)
}

func testLinkReplaceMissing(t *testing.T, h Harness) {
	t.Helper()

	err := h.Links.PutLink(t.Context(), zero(), link("U1", "bob"), "alice", linkEntry())
	wantErr(t, "PutLink that replaces no link", err, chatops.ErrConflict)

	_, err = h.Links.LinkOf(t.Context(), chatUser("T1", "U1"))
	wantErr(t, "LinkOf", err, chatops.ErrNotFound)
	wantEntries(t, h, audit.ServerScope, 0)
}

func testLinkExactIDs(t *testing.T, h Harness) {
	t.Helper()

	put(t, h, link("U1", "alice"), "")
	put(t, h, link("u1", "bob"), "")

	wantPrincipal(t, h, "U1", "alice")
	wantPrincipal(t, h, "u1", "bob")

	_, err := h.Links.LinkOf(t.Context(), chatUser("T2", "U1"))
	wantErr(t, "LinkOf in another workspace", err, chatops.ErrNotFound)

	_, err = h.Links.LinkOf(t.Context(), chatUser("t1", "U1"))
	wantErr(t, "LinkOf in a workspace ID of another case", err, chatops.ErrNotFound)
}

func testLinkInvalid(t *testing.T, h Harness) {
	t.Helper()

	for _, l := range []chatops.Link{link("U1", ""), link("", "alice"), {
		Platform: "chat-a", Workspace: "T1", User: "U1", Principal: "alice", LinkedAt: time.Time{},
	}} {
		err := h.Links.PutLink(t.Context(), zero(), l, "", linkEntry())
		wantErr(t, "PutLink of an invalid link", err, chatops.ErrInvalid)
	}

	wantEntries(t, h, audit.ServerScope, 0)
}

func testLinksOf(t *testing.T, h Harness) {
	t.Helper()

	put(t, h, link("U1", "alice"), "")
	put(t, h, link("U2", "alice"), "")
	put(t, h, link("U3", "bob"), "")

	got, err := h.Links.LinksOf(t.Context(), "alice")
	if err != nil || len(got) != 2 {
		t.Fatalf("LinksOf = %+v (%v), want 2", got, err)
	}
}

func testUnlink(t *testing.T, h Harness) {
	t.Helper()

	put(t, h, link("U1", "alice"), "")

	if err := h.Links.DeleteLink(t.Context(), zero(), chatUser("T1", "U1"), linkEntry()); err != nil {
		t.Fatalf("DeleteLink: %v", err)
	}

	err := h.Links.DeleteLink(t.Context(), zero(), chatUser("T1", "U1"), linkEntry())
	wantErr(t, "second DeleteLink", err, chatops.ErrNotFound)
	wantEntries(t, h, audit.ServerScope, 2)

	put(t, h, link("U1", "bob"), "")
	wantPrincipal(t, h, "U1", "bob")
}

func testLinkFences(t *testing.T, h Harness) {
	t.Helper()

	f := namespaceFences(t, h)
	put(t, h, link("U1", "alice"), "")

	for _, nf := range []namespace.Fence{f.current, f.stale} {
		err := h.Links.PutLink(t.Context(), nf, link("U2", "alice"), "", linkEntry())
		wantErr(t, "PutLink with a namespace fence", err, chatops.ErrInvalid)

		err = h.Links.DeleteLink(t.Context(), nf, chatUser("T1", "U1"), linkEntry())
		wantErr(t, "DeleteLink with a namespace fence", err, chatops.ErrInvalid)
	}

	wantPrincipal(t, h, "U1", "alice")

	_, err := h.Links.LinkOf(t.Context(), chatUser("T1", "U2"))
	wantErr(t, "LinkOf after a refused PutLink", err, chatops.ErrNotFound)
	wantEntries(t, h, audit.ServerScope, 1)
	wantEntries(t, h, shop, 0)
}

func linkToken(n string) string { return n + strings.Repeat("k", chatops.MinLinkTokenLen) }

func linkRequest(t *testing.T, token string) chatops.LinkRequest {
	t.Helper()

	r, err := chatops.NewLinkRequest(token, chatUser("T1", "U1"), chatops.Workspaces{"chat-a": {"T1"}}, now())
	if err != nil {
		t.Fatalf("NewLinkRequest: %v", err)
	}

	return r
}

func createLinkRequest(t *testing.T, h Harness, token string) chatops.LinkRequest {
	t.Helper()

	r := linkRequest(t, token)
	if err := h.LinkRequests.CreateLinkRequest(t.Context(), zero(), r); err != nil {
		t.Fatalf("CreateLinkRequest: %v", err)
	}

	return r
}

func testLinkRequestOnce(t *testing.T, h Harness) {
	t.Helper()

	r := createLinkRequest(t, h, linkToken("a"))

	got, err := h.LinkRequests.GetLinkRequest(t.Context(), r.TokenSHA256)
	if err != nil || got.User != "U1" || !got.UsedAt.IsZero() {
		t.Fatalf("GetLinkRequest = %+v (%v), want the request of U1 with no use", got, err)
	}

	used, winner := useConcurrently(t, func(principal string) (chatops.LinkRequest, error) {
		return h.LinkRequests.UseLinkRequest(t.Context(), zero(), r.TokenSHA256, principal, now().Add(time.Minute))
	})
	if used.UsedBy != winner || !used.UsedAt.Equal(now().Add(time.Minute)) || used.User != "U1" {
		t.Fatalf("UseLinkRequest = %+v, want a use by %s", used, winner)
	}

	got, err = h.LinkRequests.GetLinkRequest(t.Context(), r.TokenSHA256)
	if err != nil || got.UsedBy != winner {
		t.Fatalf("GetLinkRequest = %+v (%v), want the use by %s", got, err, winner)
	}

	_, err = h.LinkRequests.UseLinkRequest(t.Context(), zero(), chatops.HashLinkToken("none"), "alice", now())
	wantErr(t, "UseLinkRequest of an unknown hash", err, chatops.ErrNotFound)

	_, err = h.LinkRequests.GetLinkRequest(t.Context(), chatops.HashLinkToken("none"))
	wantErr(t, "GetLinkRequest of an unknown hash", err, chatops.ErrNotFound)
}

// racers is the number of concurrent requests of a single-use case.
const racers = 16

// useConcurrently calls use from racers goroutines, each with another
// principal. Exactly one call must succeed and every other call must
// return ErrUsed. It returns the result and the principal of that call.
func useConcurrently[T any](t *testing.T, use func(principal string) (T, error)) (T, string) {
	t.Helper()

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		won     T
		winners []string
		others  []error
	)

	start := make(chan struct{})

	for i := range racers {
		principal := fmt.Sprintf("p%d", i)

		wg.Go(func() {
			<-start

			got, err := use(principal)

			mu.Lock()
			defer mu.Unlock()

			if err == nil {
				won, winners = got, append(winners, principal)

				return
			}

			others = append(others, err)
		})
	}

	close(start)
	wg.Wait()

	if len(winners) != 1 {
		t.Fatalf("%d of %d concurrent uses succeeded (%v), want exactly 1", len(winners), racers, winners)
	}

	for _, err := range others {
		wantErr(t, "concurrent use", err, chatops.ErrUsed)
	}

	return won, winners[0]
}

func testLinkRequestExpired(t *testing.T, h Harness) {
	t.Helper()

	r := createLinkRequest(t, h, linkToken("a"))

	_, err := h.LinkRequests.UseLinkRequest(t.Context(), zero(), r.TokenSHA256, "alice",
		now().Add(chatops.LinkRequestLifetime))
	wantErr(t, "UseLinkRequest after the expiry", err, chatops.ErrExpired)
}

func testLinkRequestConflict(t *testing.T, h Harness) {
	t.Helper()

	r := createLinkRequest(t, h, linkToken("a"))

	if _, err := h.LinkRequests.UseLinkRequest(t.Context(), zero(), r.TokenSHA256, "alice", now()); err != nil {
		t.Fatalf("UseLinkRequest: %v", err)
	}

	err := h.LinkRequests.CreateLinkRequest(t.Context(), zero(), linkRequest(t, linkToken("a")))
	wantErr(t, "second CreateLinkRequest", err, chatops.ErrConflict)

	got, err := h.LinkRequests.GetLinkRequest(t.Context(), r.TokenSHA256)
	if err != nil || got.UsedAt.IsZero() {
		t.Fatalf("GetLinkRequest = %+v (%v), want the use to stay", got, err)
	}
}

func testLinkRequestInvalid(t *testing.T, h Harness) {
	t.Helper()

	bad := linkRequest(t, linkToken("a"))
	bad.TokenSHA256 = linkToken("a")
	wantErr(t, "CreateLinkRequest with a token in place of its hash",
		h.LinkRequests.CreateLinkRequest(t.Context(), zero(), bad), chatops.ErrInvalid)

	used := linkRequest(t, linkToken("b"))
	used.UsedAt, used.UsedBy = now(), "alice"
	wantErr(t, "CreateLinkRequest with a use", h.LinkRequests.CreateLinkRequest(t.Context(), zero(), used),
		chatops.ErrInvalid)

	r := createLinkRequest(t, h, linkToken("c"))
	_, err := h.LinkRequests.UseLinkRequest(t.Context(), zero(), r.TokenSHA256, "", now())
	wantErr(t, "UseLinkRequest with no principal", err, chatops.ErrInvalid)
}

func testLinkRequestFences(t *testing.T, h Harness) {
	t.Helper()

	f := namespaceFences(t, h)
	r := createLinkRequest(t, h, linkToken("a"))

	for _, nf := range []namespace.Fence{f.current, f.stale} {
		err := h.LinkRequests.CreateLinkRequest(t.Context(), nf, linkRequest(t, linkToken("b")))
		wantErr(t, "CreateLinkRequest with a namespace fence", err, chatops.ErrInvalid)

		_, err = h.LinkRequests.UseLinkRequest(t.Context(), nf, r.TokenSHA256, "alice", now())
		wantErr(t, "UseLinkRequest with a namespace fence", err, chatops.ErrInvalid)
	}

	_, err := h.LinkRequests.GetLinkRequest(t.Context(), chatops.HashLinkToken(linkToken("b")))
	wantErr(t, "GetLinkRequest after a refused create", err, chatops.ErrNotFound)

	got, err := h.LinkRequests.GetLinkRequest(t.Context(), r.TokenSHA256)
	if err != nil || !got.UsedAt.IsZero() {
		t.Fatalf("GetLinkRequest = %+v (%v), want no use", got, err)
	}
}

func intentEntry(ns namespace.Name) audit.Entry { return entry(ns, "job:approve") }

func intent(id string, ns namespace.Name) chatops.Intent {
	req := chatops.Request{
		Platform: "chat-a", Workspace: "T1", User: "U1", DeliveryID: "d-1", Action: chatops.ActionApprove,
		Target: chatops.Target{
			Kind: chatops.TargetJob, ID: "job-1", Phase: 1, Gate: job.GateApproval,
			Plan: job.PlanBinding{Plan: "plan-1", DisplaySHA256: "ab", ApplySHA256: "cd", StateVersion: "7"},
		},
		At: now(),
	}

	return chatops.NewIntent(id, ns, "alice", req, now())
}

// createIntent stores the intent cin-1 in the namespace of f.
func createIntent(t *testing.T, h Harness, f namespace.Fence) {
	t.Helper()

	err := h.Intents.CreateIntent(t.Context(), f, intent("cin-1", f.Namespace), intentEntry(f.Namespace))
	if err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}
}

func shopFence(t *testing.T, h Harness) namespace.Fence {
	t.Helper()

	f, err := h.Domain.CreateNamespace(t.Context(), shop)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	return f
}

func testIntentOnce(t *testing.T, h Harness) {
	t.Helper()

	f := shopFence(t, h)
	createIntent(t, h, f)
	wantEntries(t, h, shop, 1)

	useConcurrently(t, func(string) (struct{}, error) {
		return struct{}{}, h.Intents.UseIntent(t.Context(), f, "cin-1", now().Add(time.Minute))
	})

	got, err := h.Intents.GetIntent(t.Context(), "cin-1")
	if err != nil || got.UsedAt.IsZero() || got.Request.Target.Phase != 1 ||
		got.Request.Target.Plan.ApplySHA256 != "cd" {
		t.Fatalf("GetIntent = %+v (%v), want a use, the phase and the plan binding", got, err)
	}

	wantErr(t, "UseIntent unknown", h.Intents.UseIntent(t.Context(), f, "cin-none", now()), chatops.ErrNotFound)

	_, err = h.Intents.GetIntent(t.Context(), "cin-none")
	wantErr(t, "GetIntent unknown", err, chatops.ErrNotFound)
}

func testIntentExpired(t *testing.T, h Harness) {
	t.Helper()

	f := shopFence(t, h)
	createIntent(t, h, f)

	err := h.Intents.UseIntent(t.Context(), f, "cin-1", now().Add(chatops.IntentLifetime))
	wantErr(t, "UseIntent after the expiry", err, chatops.ErrExpired)
}

func testIntentConflict(t *testing.T, h Harness) {
	t.Helper()

	f := shopFence(t, h)
	createIntent(t, h, f)

	if err := h.Intents.UseIntent(t.Context(), f, "cin-1", now().Add(time.Minute)); err != nil {
		t.Fatalf("UseIntent: %v", err)
	}

	err := h.Intents.CreateIntent(t.Context(), f, intent("cin-1", shop), intentEntry(shop))
	wantErr(t, "second CreateIntent", err, chatops.ErrConflict)

	got, err := h.Intents.GetIntent(t.Context(), "cin-1")
	if err != nil || got.UsedAt.IsZero() {
		t.Fatalf("GetIntent = %+v (%v), want the use to stay", got, err)
	}

	wantErr(t, "UseIntent after a refused create", h.Intents.UseIntent(t.Context(), f, "cin-1", now()),
		chatops.ErrUsed)
	wantEntries(t, h, shop, 1)
}

func testIntentInvalid(t *testing.T, h Harness) {
	t.Helper()

	f := shopFence(t, h)

	noPrefix := intent("x-1", shop)
	used := intent("cin-2", shop)
	used.UsedAt = now()
	noPrincipal := intent("cin-3", shop)
	noPrincipal.Principal = ""

	bad := []chatops.Intent{noPrefix, used, noPrincipal}
	for i := range bad {
		wantErr(
			t,
			"CreateIntent of an invalid intent",
			h.Intents.CreateIntent(t.Context(), f, bad[i], intentEntry(shop)),
			chatops.ErrInvalid,
		)
	}

	wantEntries(t, h, shop, 0)
}

func testIntentFences(t *testing.T, h Harness) {
	t.Helper()

	f := namespaceFences(t, h)

	other, err := h.Domain.CreateNamespace(t.Context(), bank)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	cases := []struct {
		name   string
		fence  namespace.Fence
		create error
		use    error
	}{
		{"the zero fence", zero(), chatops.ErrInvalid, chatops.ErrNotFound},
		{"the fence of another namespace", other, chatops.ErrInvalid, chatops.ErrNotFound},
		{"a stale fence", f.stale, namespace.ErrFenced, namespace.ErrFenced},
	}

	createIntent(t, h, f.current)

	for _, tc := range cases {
		err = h.Intents.CreateIntent(t.Context(), tc.fence, intent("cin-2", shop), intentEntry(shop))
		wantErr(t, "CreateIntent with "+tc.name, err, tc.create)

		wantErr(t, "UseIntent with "+tc.name, h.Intents.UseIntent(t.Context(), tc.fence, "cin-1", now()), tc.use)
	}

	_, err = h.Intents.GetIntent(t.Context(), "cin-2")
	wantErr(t, "GetIntent after refused creates", err, chatops.ErrNotFound)

	got, err := h.Intents.GetIntent(t.Context(), "cin-1")
	if err != nil || !got.UsedAt.IsZero() {
		t.Fatalf("GetIntent = %+v (%v), want no use", got, err)
	}

	wantEntries(t, h, shop, 1)
	wantEntries(t, h, bank, 0)
	wantEntries(t, h, audit.ServerScope, 0)
}

func testEntryScope(t *testing.T, h Harness) {
	t.Helper()

	f := shopFence(t, h)

	if _, err := h.Domain.CreateNamespace(t.Context(), bank); err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	err := h.Links.PutLink(t.Context(), zero(), link("U1", "alice"), "", entry(bank, "caller:manage-chat-links"))
	wantErr(t, "PutLink with an entry of a namespace", err, chatops.ErrInvalid)

	_, err = h.Links.LinkOf(t.Context(), chatUser("T1", "U1"))
	wantErr(t, "LinkOf after a refused PutLink", err, chatops.ErrNotFound)

	put(t, h, link("U1", "alice"), "")

	err = h.Links.DeleteLink(t.Context(), zero(), chatUser("T1", "U1"), entry(bank, "caller:manage-chat-links"))
	wantErr(t, "DeleteLink with an entry of a namespace", err, chatops.ErrInvalid)
	wantPrincipal(t, h, "U1", "alice")

	err = h.Intents.CreateIntent(t.Context(), f, intent("cin-1", shop), intentEntry(bank))
	wantErr(t, "CreateIntent with an entry of another namespace", err, chatops.ErrInvalid)

	_, err = h.Intents.GetIntent(t.Context(), "cin-1")
	wantErr(t, "GetIntent after a refused create", err, chatops.ErrNotFound)

	wantEntries(t, h, audit.ServerScope, 1)
	wantEntries(t, h, shop, 0)
	wantEntries(t, h, bank, 0)
}

func testRepeatedRequest(t *testing.T, h Harness) {
	t.Helper()

	f := shopFence(t, h)
	createIntent(t, h, f)

	usedAt := now().Add(time.Minute)
	if err := h.Intents.UseIntent(t.Context(), f, "cin-1", usedAt); err != nil {
		t.Fatalf("UseIntent: %v", err)
	}

	// The chat tool sends the same request again, with the same delivery
	// ID. The server makes a new intent; the used one stays as it is.
	repeat := intent("cin-2", shop)
	if err := h.Intents.CreateIntent(t.Context(), f, repeat, intentEntry(shop)); err != nil {
		t.Fatalf("CreateIntent of the repeat: %v", err)
	}

	first, err := h.Intents.GetIntent(t.Context(), "cin-1")
	if err != nil || !first.UsedAt.Equal(usedAt) || first.Request != repeat.Request {
		t.Fatalf("GetIntent cin-1 = %+v (%v), want the use at %v to stay", first, err, usedAt)
	}

	second, err := h.Intents.GetIntent(t.Context(), "cin-2")
	if err != nil || !second.UsedAt.IsZero() {
		t.Fatalf("GetIntent cin-2 = %+v (%v), want no use", second, err)
	}

	wantErr(t, "UseIntent of the used intent", h.Intents.UseIntent(t.Context(), f, "cin-1", usedAt),
		chatops.ErrUsed)

	again, err := h.Intents.GetIntent(t.Context(), "cin-1")
	if err != nil || again != first {
		t.Fatalf("GetIntent cin-1 = %+v (%v), want %+v", again, err, first)
	}

	wantEntries(t, h, shop, 2)
}

func testDeleteIntents(t *testing.T, h Harness) {
	t.Helper()

	f := namespaceFences(t, h)

	other, err := h.Domain.CreateNamespace(t.Context(), bank)
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	createIntent(t, h, f.current)

	err = h.Intents.CreateIntent(t.Context(), f.current, intent("cin-2", shop), intentEntry(shop))
	if err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}

	err = h.Intents.UseIntent(t.Context(), f.current, "cin-2", now())
	if err != nil {
		t.Fatalf("UseIntent: %v", err)
	}

	of := intent("cin-3", shop)
	of.Request.User = "u1"

	err = h.Intents.CreateIntent(t.Context(), f.current, of, intentEntry(shop))
	if err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}

	got, err := h.Intents.IntentsOf(t.Context(), chatUser("T1", "U1"))
	if err != nil || len(got) != 2 {
		t.Fatalf("IntentsOf U1 = %+v (%v), want cin-1 and cin-2", got, err)
	}

	for _, tc := range []struct {
		name  string
		fence namespace.Fence
		want  error
	}{
		{"the zero fence", zero(), chatops.ErrNotFound},
		{"the fence of another namespace", other, chatops.ErrNotFound},
		{"a stale fence", f.stale, namespace.ErrFenced},
	} {
		wantErr(t, "DeleteIntent with "+tc.name, h.Intents.DeleteIntent(t.Context(), tc.fence, "cin-1"), tc.want)
	}

	wantErr(t, "DeleteIntent of a used intent", h.Intents.DeleteIntent(t.Context(), f.current, "cin-2"),
		chatops.ErrUsed)

	err = h.Intents.DeleteIntent(t.Context(), f.current, "cin-1")
	if err != nil {
		t.Fatalf("DeleteIntent: %v", err)
	}

	_, err = h.Intents.GetIntent(t.Context(), "cin-1")
	wantErr(t, "GetIntent after DeleteIntent", err, chatops.ErrNotFound)
	wantErr(t, "second DeleteIntent", h.Intents.DeleteIntent(t.Context(), f.current, "cin-1"), chatops.ErrNotFound)

	for _, id := range []string{"cin-2", "cin-3"} {
		_, err = h.Intents.GetIntent(t.Context(), id)
		if err != nil {
			t.Fatalf("GetIntent %s: %v, want it to stay", id, err)
		}
	}

	wantEntries(t, h, shop, 3)
}
