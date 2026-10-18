// Package chatopstest is the conformance suite for chatops.Links and
// chatops.Intents implementations. Every store adapter runs it:
//
//	func TestChatOpsConformance(t *testing.T) {
//		t.Parallel()
//		chatopstest.Run(t, func(t *testing.T) chatopstest.Harness { return newHarness(t) })
//	}
//
// A reference adapter in memory, in the tests of this package, runs the
// suite, so each case runs before the first SQL adapter. The suite of the
// Verifier port comes with the first adapter of a chat tool.
package chatopstest

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/opsmem"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/chatops"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Harness is one fresh, empty database for one case.
type Harness struct {
	Links   chatops.Links
	Intents chatops.Intents
	Domain  Domain
}

// Domain reads the audit entries that the stores append.
type Domain interface {
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
		{"a link replaces the link of the same chat user", testLinkReplace},
		{"links of a principal list each chat user", testLinksOf},
		{"delete of a link", testUnlink},
		{"an intent is used once", testIntentOnce},
		{"an expired intent cannot be used", testIntentExpired},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newHarness(t))
		})
	}
}

func now() time.Time { return time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC) }

func entry() audit.Entry {
	return opsmem.Entry(audit.ServerScope, "alice", "principal:link-chat", "@caller", now())
}

func link(user, principal string) chatops.Link {
	return chatops.Link{Platform: "chat-a", Workspace: "T1", User: user, Principal: principal, LinkedAt: now()}
}

func put(t *testing.T, h Harness, l chatops.Link) {
	t.Helper()

	if err := h.Links.PutLink(t.Context(), l, entry()); err != nil {
		t.Fatalf("PutLink: %v", err)
	}
}

func intent(id string) chatops.Intent {
	req := chatops.Request{
		Platform: "chat-a", Workspace: "T1", User: "U1", Action: chatops.ActionApprove,
		Target: chatops.Target{Kind: chatops.TargetJob, ID: "job-1", Plan: "sha256:ab"}, At: now(),
	}

	return chatops.NewIntent(id, "alice", req, now())
}

func testLinkReplace(t *testing.T, h Harness) {
	t.Helper()

	put(t, h, link("U1", "alice"))
	put(t, h, link("U1", "bob"))

	got, err := h.Links.LinkOf(t.Context(), "chat-a", "T1", "U1")
	if err != nil || got.Principal != "bob" {
		t.Fatalf("LinkOf = %+v (%v), want bob", got, err)
	}

	entries, err := h.Domain.AuditEntries(t.Context(), audit.ServerScope)
	if err != nil || len(entries) != 2 {
		t.Fatalf("audit entries = %d (%v), want 2", len(entries), err)
	}

	if _, err := h.Links.LinkOf(t.Context(), "chat-a", "T2", "U1"); !errors.Is(err, chatops.ErrNotFound) {
		t.Fatalf("LinkOf in another workspace: err = %v, want ErrNotFound", err)
	}
}

func testLinksOf(t *testing.T, h Harness) {
	t.Helper()

	put(t, h, link("U1", "alice"))
	put(t, h, link("U2", "alice"))
	put(t, h, link("U3", "bob"))

	got, err := h.Links.LinksOf(t.Context(), "alice")
	if err != nil || len(got) != 2 {
		t.Fatalf("LinksOf = %+v (%v), want 2", got, err)
	}
}

func testUnlink(t *testing.T, h Harness) {
	t.Helper()

	put(t, h, link("U1", "alice"))

	if err := h.Links.DeleteLink(t.Context(), "chat-a", "T1", "U1", entry()); err != nil {
		t.Fatalf("DeleteLink: %v", err)
	}

	if err := h.Links.DeleteLink(t.Context(), "chat-a", "T1", "U1", entry()); !errors.Is(err, chatops.ErrNotFound) {
		t.Fatalf("second DeleteLink: err = %v, want ErrNotFound", err)
	}
}

func testIntentOnce(t *testing.T, h Harness) {
	t.Helper()

	if err := h.Intents.CreateIntent(t.Context(), intent("cin-1")); err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}

	if err := h.Intents.UseIntent(t.Context(), "cin-1", now().Add(time.Minute)); err != nil {
		t.Fatalf("UseIntent: %v", err)
	}

	err := h.Intents.UseIntent(t.Context(), "cin-1", now().Add(time.Minute))
	if !errors.Is(err, chatops.ErrIntentUsed) {
		t.Fatalf("second UseIntent: err = %v, want ErrIntentUsed", err)
	}

	got, err := h.Intents.GetIntent(t.Context(), "cin-1")
	if err != nil || got.UsedAt.IsZero() {
		t.Fatalf("GetIntent = %+v (%v), want a use", got, err)
	}

	if err := h.Intents.UseIntent(t.Context(), "cin-none", now()); !errors.Is(err, chatops.ErrNotFound) {
		t.Fatalf("UseIntent unknown: err = %v, want ErrNotFound", err)
	}
}

func testIntentExpired(t *testing.T, h Harness) {
	t.Helper()

	if err := h.Intents.CreateIntent(t.Context(), intent("cin-1")); err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}

	err := h.Intents.UseIntent(t.Context(), "cin-1", now().Add(chatops.IntentLifetime))
	if !errors.Is(err, chatops.ErrIntentExpired) {
		t.Fatalf("UseIntent after the expiry: err = %v, want ErrIntentExpired", err)
	}
}
