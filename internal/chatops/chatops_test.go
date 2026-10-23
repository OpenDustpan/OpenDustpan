package chatops_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/chatops"
	"opendustpan.org/opendustpan/pkg/job"
)

func now() time.Time { return time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC) }

func binding() job.PlanBinding {
	return job.PlanBinding{Plan: "plan-1", DisplaySHA256: "ab", ApplySHA256: "cd", StateVersion: "7"}
}

func noPlan() job.PlanBinding {
	return job.PlanBinding{Plan: "", DisplaySHA256: "", ApplySHA256: "", StateVersion: ""}
}

// check fails t when err is not want, or not nil for a nil want.
func check(t *testing.T, what string, err, want error) {
	t.Helper()

	if (want == nil && err != nil) || (want != nil && !errors.Is(err, want)) {
		t.Fatalf("%s = %v, want %v", what, err, want)
	}
}

func allow() chatops.Workspaces { return chatops.Workspaces{"chat-a": {"T1", "T3"}} }

func target(kind chatops.TargetKind) chatops.Target {
	if kind != chatops.TargetJob {
		return chatops.Target{Kind: kind, ID: "inc-1", Phase: 0, Gate: "", Plan: noPlan()}
	}

	return chatops.Target{Kind: kind, ID: "job-1", Phase: 1, Gate: job.GateApproval, Plan: binding()}
}

func request(a chatops.Action, t chatops.Target) chatops.Request {
	return chatops.Request{
		Platform: "chat-a", Workspace: "T1", User: "U1", DeliveryID: "d-1", Action: a, Target: t, At: now(),
	}
}

func link() *chatops.Link {
	return &chatops.Link{Platform: "chat-a", Workspace: "T1", User: "U1", Principal: "alice", LinkedAt: now()}
}

func TestDecide(t *testing.T) {
	t.Parallel()

	approve := request(chatops.ActionApprove, target(chatops.TargetJob))

	cases := []struct {
		name    string
		req     func(r chatops.Request) chatops.Request
		link    func(l *chatops.Link) *chatops.Link
		outcome chatops.Outcome
		action  string
		err     error
	}{
		{
			"open needs no link",
			func(r chatops.Request) chatops.Request { r.Action = chatops.ActionOpen; return r },
			func(*chatops.Link) *chatops.Link { return nil },
			chatops.OutcomeOpen, "", nil,
		},
		{
			"acknowledge an incident",
			func(chatops.Request) chatops.Request {
				return request(chatops.ActionAcknowledge, target(chatops.TargetIncident))
			},
			nil, chatops.OutcomeApply, "namespace:respond-incidents", nil,
		},
		{"approve needs a step-up", nil, nil, chatops.OutcomeStepUp, "job:approve", nil},
		{
			"approve with no link", nil,
			func(*chatops.Link) *chatops.Link { return nil },
			"", "", chatops.ErrNotLinked,
		},
		{
			"a link of another allowed workspace", nil,
			func(l *chatops.Link) *chatops.Link { l.Workspace = "T3"; return l },
			"", "", chatops.ErrNotLinked,
		},
		{
			"a link of a user ID in another case", nil,
			func(l *chatops.Link) *chatops.Link { l.User = "u1"; return l },
			"", "", chatops.ErrNotLinked,
		},
		{
			"a workspace that is not on the allowlist",
			func(r chatops.Request) chatops.Request { r.Workspace = "T2"; return r },
			func(l *chatops.Link) *chatops.Link { l.Workspace = "T2"; return l },
			"", "", chatops.ErrWorkspace,
		},
		{
			"open from a workspace that is not on the allowlist",
			func(r chatops.Request) chatops.Request { r.Workspace = "T2"; r.Action = chatops.ActionOpen; return r },
			nil, "", "", chatops.ErrWorkspace,
		},
		{
			"a platform that is not on the allowlist",
			func(r chatops.Request) chatops.Request { r.Platform = "chat-b"; return r },
			nil, "", "", chatops.ErrWorkspace,
		},
		{
			"approve with no plan binding",
			func(r chatops.Request) chatops.Request { r.Target.Plan = noPlan(); return r },
			nil, "", "", chatops.ErrUnsupported,
		},
		{
			"approve a confirmation gate",
			func(r chatops.Request) chatops.Request { r.Target.Gate = job.GateConfirmation; return r },
			nil, "", "", chatops.ErrUnsupported,
		},
		{
			"approve with no gate type",
			func(r chatops.Request) chatops.Request { r.Target.Gate = ""; return r },
			nil, "", "", chatops.ErrUnsupported,
		},
		{
			"approve a review",
			func(r chatops.Request) chatops.Request { r.Target.Kind = chatops.TargetReview; return r },
			nil, "", "", chatops.ErrUnsupported,
		},
		{
			"acknowledge a job",
			func(r chatops.Request) chatops.Request { r.Action = chatops.ActionAcknowledge; return r },
			nil, "", "", chatops.ErrUnsupported,
		},
		{
			"an action chat does not offer",
			func(r chatops.Request) chatops.Request { r.Action = "apply"; return r },
			nil, "", "", chatops.ErrUnsupported,
		},
		{
			"no delivery ID",
			func(r chatops.Request) chatops.Request { r.DeliveryID = ""; return r },
			nil, "", "", chatops.ErrInvalid,
		},
		{
			"a negative phase",
			func(r chatops.Request) chatops.Request { r.Target.Phase = -1; return r },
			nil, "", "", chatops.ErrInvalid,
		},
		{
			"a long user ID",
			func(r chatops.Request) chatops.Request { r.User = strings.Repeat("u", chatops.MaxIDLen+1); return r },
			nil, "", "", chatops.ErrInvalid,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req, l := approve, link()
			if tc.req != nil {
				req = tc.req(req)
			}

			if tc.link != nil {
				l = tc.link(l)
			}

			got, err := chatops.Decide(req, l, allow())
			check(t, "Decide", err, tc.err)

			if got.Outcome != tc.outcome || got.Action != tc.action {
				t.Fatalf("Decide = %+v (%v), want %s with %q", got, err, tc.outcome, tc.action)
			}
		})
	}
}

func TestCheckIntent(t *testing.T) {
	t.Parallel()

	in := chatops.NewIntent("cin-1", "shop", "alice", request(chatops.ActionApprove, target(chatops.TargetJob)), now())
	fresh := chatops.Auth{
		Principal: "alice", Agent: "", Session: "ses-1", TokenID: "", BreakGlass: false, Kind: job.ConfirmAuthSSO,
		At: now().Add(time.Minute),
	}
	at := now().Add(2 * time.Minute)

	cases := []struct {
		name    string
		in      func(i chatops.Intent) chatops.Intent
		auth    func(a chatops.Auth) chatops.Auth
		current func(c chatops.Target) chatops.Target
		at      time.Time
		want    error
	}{
		{"a fresh authentication after the button", nil, nil, nil, at, nil},
		{
			"an MFA step", nil,
			func(a chatops.Auth) chatops.Auth { a.Kind = job.ConfirmAuthMFA; return a }, nil, at, nil,
		},
		{
			"an authentication from before the button", nil,
			func(a chatops.Auth) chatops.Auth { a.At = now().Add(-time.Minute); return a }, nil, at,
			chatops.ErrAuthStale,
		},
		{
			"an authentication in the future", nil,
			func(a chatops.Auth) chatops.Auth { a.At = at.Add(time.Second); return a }, nil, at,
			chatops.ErrAuthStale,
		},
		{
			"an authentication of an unknown kind", nil,
			func(a chatops.Auth) chatops.Auth { a.Kind = "password"; return a }, nil, at, chatops.ErrAuthStale,
		},
		{
			"no authentication kind", nil,
			func(a chatops.Auth) chatops.Auth { a.Kind = ""; return a }, nil, at, chatops.ErrAuthStale,
		},
		{
			"an access token", nil,
			func(a chatops.Auth) chatops.Auth { a.TokenID = "tok-1"; return a }, nil, at, chatops.ErrNeedsSession,
		},
		{
			"an access token with a session", nil,
			func(a chatops.Auth) chatops.Auth { a.TokenID = "tok-1"; a.Session = "ses-1"; return a }, nil, at,
			chatops.ErrNeedsSession,
		},
		{
			"no console session", nil,
			func(a chatops.Auth) chatops.Auth { a.Session = ""; return a }, nil, at, chatops.ErrNeedsSession,
		},
		{
			"another principal", nil,
			func(a chatops.Auth) chatops.Auth { a.Principal = "bob"; return a }, nil, at, chatops.ErrNotFound,
		},
		{
			"another principal on a used intent",
			func(i chatops.Intent) chatops.Intent { i.UsedAt = now(); return i },
			func(a chatops.Auth) chatops.Auth { a.Principal = "bob"; return a }, nil, at, chatops.ErrNotFound,
		},
		{
			"another principal after the expiry", nil,
			func(a chatops.Auth) chatops.Auth { a.Principal = "bob"; return a }, nil,
			now().Add(chatops.IntentLifetime), chatops.ErrNotFound,
		},
		{
			"another principal with an access token", nil,
			func(a chatops.Auth) chatops.Auth { a.Principal, a.TokenID = "bob", "tok-1"; return a }, nil, at,
			chatops.ErrNotFound,
		},
		{
			"no principal", nil,
			func(a chatops.Auth) chatops.Auth { a.Principal = ""; return a }, nil, at, chatops.ErrNotFound,
		},
		{
			"an agent", nil,
			func(a chatops.Auth) chatops.Auth { a.Agent = "dusty"; return a }, nil, at, chatops.ErrDenied,
		},
		{
			"a break-glass session", nil,
			func(a chatops.Auth) chatops.Auth { a.BreakGlass = true; return a }, nil, at, chatops.ErrDenied,
		},
		{
			"a later phase of the same job", nil, nil,
			func(c chatops.Target) chatops.Target { c.Phase = 2; return c }, at, chatops.ErrGateChanged,
		},
		{
			"a confirmation gate now", nil, nil,
			func(c chatops.Target) chatops.Target { c.Gate = job.GateConfirmation; return c }, at,
			chatops.ErrGateChanged,
		},
		{
			"another job", nil, nil,
			func(c chatops.Target) chatops.Target { c.ID = "job-2"; return c }, at, chatops.ErrGateChanged,
		},
		{
			"plans that changed", nil, nil,
			func(c chatops.Target) chatops.Target { c.Plan.ApplySHA256 = "ef"; return c }, at, job.ErrPlanChanged,
		},
		{"after the expiry", nil, nil, nil, now().Add(chatops.IntentLifetime), chatops.ErrExpired},
		{
			"a second use",
			func(i chatops.Intent) chatops.Intent { i.UsedAt = now(); return i },
			nil, nil, at, chatops.ErrUsed,
		},
		{
			"the intent of a repeated request, with the authentication of the first",
			func(i chatops.Intent) chatops.Intent {
				return chatops.NewIntent("cin-2", i.Namespace, i.Principal, i.Request, now().Add(90*time.Second))
			},
			nil, nil, at, chatops.ErrAuthStale,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			i, a, c := in, fresh, target(chatops.TargetJob)
			if tc.in != nil {
				i = tc.in(i)
			}

			if tc.auth != nil {
				a = tc.auth(a)
			}

			if tc.current != nil {
				c = tc.current(c)
			}

			l := chatops.Link{Platform: "chat-a", Workspace: "T1", User: "U1", Principal: "alice", LinkedAt: now()}
			check(t, "CheckIntent", chatops.CheckIntent(i, &l, a, c, tc.at), tc.want)
		})
	}
}

func TestCheckIntentLink(t *testing.T) {
	t.Parallel()

	in := chatops.NewIntent("cin-1", "shop", "alice", request(chatops.ActionApprove, target(chatops.TargetJob)), now())
	fresh := chatops.Auth{
		Principal: "alice", Agent: "", Session: "ses-1", TokenID: "", BreakGlass: false, Kind: job.ConfirmAuthSSO,
		At: now().Add(time.Minute),
	}
	linked := chatops.Link{Platform: "chat-a", Workspace: "T1", User: "U1", Principal: "alice", LinkedAt: now()}

	cases := []struct {
		name string
		link func(l chatops.Link) *chatops.Link
		want error
	}{
		{"the link of the intent", func(l chatops.Link) *chatops.Link { return &l }, nil},
		{"a deleted link", func(chatops.Link) *chatops.Link { return nil }, chatops.ErrNotLinked},
		{
			"a link replaced by another principal",
			func(l chatops.Link) *chatops.Link { l.Principal = "bob"; return &l }, chatops.ErrNotLinked,
		},
		{
			"the link of another chat user",
			func(l chatops.Link) *chatops.Link { l.User = "u1"; return &l }, chatops.ErrNotLinked,
		},
		{
			"the link of a user of another chat tool",
			func(l chatops.Link) *chatops.Link { l.Platform = "chat-b"; return &l }, chatops.ErrNotLinked,
		},
		{
			"a link made again after the intent",
			func(l chatops.Link) *chatops.Link { l.LinkedAt = now().Add(30 * time.Second); return &l },
			chatops.ErrNotLinked,
		},
		{
			"a link made before the intent",
			func(l chatops.Link) *chatops.Link { l.LinkedAt = now().Add(-time.Hour); return &l }, nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := chatops.CheckIntent(in, tc.link(linked), fresh, target(chatops.TargetJob), now().Add(2*time.Minute))
			check(t, "CheckIntent", err, tc.want)
		})
	}
}

func TestIntentValidate(t *testing.T) {
	t.Parallel()

	valid := chatops.NewIntent("cin-1", "shop", "alice", request(chatops.ActionApprove, target(chatops.TargetJob)),
		now())

	cases := []struct {
		name string
		edit func(i chatops.Intent) chatops.Intent
		want error
	}{
		{"a valid intent", nil, nil},
		{"no prefix", func(i chatops.Intent) chatops.Intent { i.ID = "x-1"; return i }, chatops.ErrInvalid},
		{"only the prefix", func(i chatops.Intent) chatops.Intent { i.ID = "cin-"; return i }, chatops.ErrInvalid},
		{"no principal", func(i chatops.Intent) chatops.Intent { i.Principal = ""; return i }, chatops.ErrInvalid},
		{"no namespace", func(i chatops.Intent) chatops.Intent { i.Namespace = ""; return i }, chatops.ErrInvalid},
		{
			"the server scope",
			func(i chatops.Intent) chatops.Intent { i.Namespace = "@server"; return i }, chatops.ErrInvalid,
		},
		{
			"not an approval",
			func(i chatops.Intent) chatops.Intent { i.Request.Action = chatops.ActionOpen; return i },
			chatops.ErrInvalid,
		},
		{
			"a long lifetime",
			func(i chatops.Intent) chatops.Intent { i.ExpiresAt = i.ExpiresAt.Add(time.Hour); return i },
			chatops.ErrInvalid,
		},
		{
			"a request with no user",
			func(i chatops.Intent) chatops.Intent { i.Request.User = ""; return i }, chatops.ErrInvalid,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			in := valid
			if tc.edit != nil {
				in = tc.edit(in)
			}

			check(t, "Validate", in.Validate(), tc.want)
		})
	}
}

func TestCheckTimestamp(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		at   time.Time
		want error
	}{
		{"now", now(), nil},
		{"at the maximum age", now().Add(-chatops.SignatureMaxAge), nil},
		{"older than the maximum age", now().Add(-chatops.SignatureMaxAge - time.Second), chatops.ErrSignature},
		{"in the skew", now().Add(chatops.SignatureMaxSkew), nil},
		{"later than the skew", now().Add(chatops.SignatureMaxSkew + time.Second), chatops.ErrSignature},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			check(t, "CheckTimestamp", chatops.CheckTimestamp(tc.at, now()), tc.want)
		})
	}
}

func TestLinkValidate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		edit func(l *chatops.Link)
		want error
	}{
		{"a valid link", nil, nil},
		{"no principal", func(l *chatops.Link) { l.Principal = "" }, chatops.ErrInvalid},
		{"no workspace", func(l *chatops.Link) { l.Workspace = "" }, chatops.ErrInvalid},
		{"no time", func(l *chatops.Link) { l.LinkedAt = time.Time{} }, chatops.ErrInvalid},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			l := link()
			if tc.edit != nil {
				tc.edit(l)
			}

			check(t, "Validate", l.Validate(), tc.want)
		})
	}
}
