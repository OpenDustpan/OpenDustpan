package chatops_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/chatops"
)

func now() time.Time { return time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC) }

func request(a chatops.Action, kind chatops.TargetKind, plan string) chatops.Request {
	return chatops.Request{
		Platform: "chat-a", Workspace: "T1", User: "U1", Action: a,
		Target: chatops.Target{Kind: kind, ID: "job-1", Plan: plan}, At: now(),
	}
}

func link() *chatops.Link {
	return &chatops.Link{Platform: "chat-a", Workspace: "T1", User: "U1", Principal: "alice", LinkedAt: now()}
}

func TestDecide(t *testing.T) {
	t.Parallel()

	other := link()
	other.Workspace = "T2"

	cases := []struct {
		name    string
		req     chatops.Request
		link    *chatops.Link
		outcome chatops.Outcome
		action  string
		err     error
	}{
		{"open needs no link", request(chatops.ActionOpen, chatops.TargetJob, ""), nil, chatops.OutcomeOpen, "", nil},
		{
			"acknowledge an incident",
			request(chatops.ActionAcknowledge, chatops.TargetIncident, ""),
			link(),
			chatops.OutcomeApply,
			"incident:respond",
			nil,
		},
		{
			"approve needs a step-up",
			request(chatops.ActionApprove, chatops.TargetJob, "sha256:ab"),
			link(),
			chatops.OutcomeStepUp,
			"job:approve",
			nil,
		},
		{
			"approve with no link",
			request(chatops.ActionApprove, chatops.TargetJob, "sha256:ab"),
			nil,
			"",
			"",
			chatops.ErrNotLinked,
		},
		{
			"a link of another workspace",
			request(chatops.ActionApprove, chatops.TargetJob, "sha256:ab"),
			other,
			"",
			"",
			chatops.ErrNotLinked,
		},
		{
			"approve with no plan binding",
			request(chatops.ActionApprove, chatops.TargetJob, ""),
			link(),
			"",
			"",
			chatops.ErrUnsupported,
		},
		{
			"approve a review",
			request(chatops.ActionApprove, chatops.TargetReview, "x"),
			link(),
			"",
			"",
			chatops.ErrUnsupported,
		},
		{
			"acknowledge a job",
			request(chatops.ActionAcknowledge, chatops.TargetJob, ""),
			link(),
			"",
			"",
			chatops.ErrUnsupported,
		},
		{
			"an action chat does not offer",
			request("apply", chatops.TargetJob, ""),
			link(),
			"",
			"",
			chatops.ErrUnsupported,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := chatops.Decide(tc.req, tc.link)
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("Decide: err = %v, want %v", err, tc.err)
				}

				return
			}

			if err != nil || got.Outcome != tc.outcome || got.Action != tc.action {
				t.Fatalf("Decide = %+v (%v), want %s with %q", got, err, tc.outcome, tc.action)
			}
		})
	}
}

func TestCheckIntent(t *testing.T) {
	t.Parallel()

	in := chatops.NewIntent("cin-1", "alice", request(chatops.ActionApprove, chatops.TargetJob, "sha256:ab"), now())
	fresh := chatops.Auth{Principal: "alice", Agent: "", Kind: chatops.AuthSSO, At: now().Add(time.Minute)}
	at := now().Add(2 * time.Minute)

	cases := []struct {
		name string
		in   func(i chatops.Intent) chatops.Intent
		auth func(a chatops.Auth) chatops.Auth
		at   time.Time
		want error
	}{
		{"a fresh authentication after the button", nil, nil, at, nil},
		{
			"an authentication from before the button",
			nil,
			func(a chatops.Auth) chatops.Auth { a.At = now().Add(-time.Minute); return a },
			at,
			chatops.ErrAuthStale,
		},
		{
			"another principal",
			nil,
			func(a chatops.Auth) chatops.Auth { a.Principal = "bob"; return a },
			at,
			chatops.ErrDenied,
		},
		{"an agent", nil, func(a chatops.Auth) chatops.Auth { a.Agent = "dusty"; return a }, at, chatops.ErrDenied},
		{
			"no authentication kind",
			nil,
			func(a chatops.Auth) chatops.Auth { a.Kind = ""; return a },
			at,
			chatops.ErrAuthStale,
		},
		{"after the expiry", nil, nil, now().Add(chatops.IntentLifetime), chatops.ErrIntentExpired},
		{
			"a second use",
			func(i chatops.Intent) chatops.Intent { i.UsedAt = now(); return i },
			nil,
			at,
			chatops.ErrIntentUsed,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			i, a := in, fresh
			if tc.in != nil {
				i = tc.in(i)
			}

			if tc.auth != nil {
				a = tc.auth(a)
			}

			err := chatops.CheckIntent(i, a, tc.at)
			if (tc.want == nil && err != nil) || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("CheckIntent = %v, want %v", err, tc.want)
			}
		})
	}
}
