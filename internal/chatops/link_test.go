package chatops_test

import (
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/chatops"
)

func user() chatops.ChatUser {
	return chatops.ChatUser{Platform: "chat-a", Workspace: "T1", User: "U1"}
}

func token() string { return strings.Repeat("k", chatops.MinLinkTokenLen) }

func TestNewLinkRequest(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		token     string
		workspace string
		user      string
		want      error
	}{
		{"a valid request", token(), "T1", "U1", nil},
		{"a short token", token()[1:], "T1", "U1", chatops.ErrInvalid},
		{"a long token", strings.Repeat("k", 513), "T1", "U1", chatops.ErrInvalid},
		{"a workspace that is not on the allowlist", token(), "T2", "U1", chatops.ErrWorkspace},
		{"no user", token(), "T1", "", chatops.ErrInvalid},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r, err := chatops.NewLinkRequest(
				tc.token,
				chatops.ChatUser{Platform: "chat-a", Workspace: tc.workspace, User: tc.user},
				allow(),
				now(),
			)
			check(t, "NewLinkRequest", err, tc.want)

			if tc.want != nil {
				return
			}

			if r.TokenSHA256 != chatops.HashLinkToken(tc.token) || strings.Contains(r.TokenSHA256, tc.token) {
				t.Fatalf("TokenSHA256 = %q, want the hash of the token only", r.TokenSHA256)
			}

			if !r.ExpiresAt.Equal(now().Add(chatops.LinkRequestLifetime)) {
				t.Fatalf("ExpiresAt = %v, want %s after the creation", r.ExpiresAt, chatops.LinkRequestLifetime)
			}

			l := r.Link("alice", now())
			if l.Platform != "chat-a" || l.Workspace != "T1" || l.User != "U1" || l.Principal != "alice" {
				t.Fatalf("Link = %+v, want the chat user of the request", l)
			}
		})
	}
}

func TestLinkRequestCheck(t *testing.T) {
	t.Parallel()

	valid, err := chatops.NewLinkRequest(token(), user(), allow(), now())
	if err != nil {
		t.Fatalf("NewLinkRequest: %v", err)
	}

	cases := []struct {
		name  string
		edit  func(r chatops.LinkRequest) chatops.LinkRequest
		allow chatops.Workspaces
		at    time.Time
		want  error
	}{
		{"before the expiry", nil, allow(), now().Add(time.Minute), nil},
		{"at the expiry", nil, allow(), now().Add(chatops.LinkRequestLifetime), chatops.ErrExpired},
		{
			"a second use",
			func(r chatops.LinkRequest) chatops.LinkRequest { r.UsedAt = now(); r.UsedBy = "bob"; return r },
			allow(), now().Add(time.Minute), chatops.ErrUsed,
		},
		{
			"a workspace removed from the allowlist", nil,
			chatops.Workspaces{"chat-a": {"T3"}},
			now().Add(time.Minute), chatops.ErrWorkspace,
		},
		{"an empty allowlist", nil, nil, now().Add(time.Minute), chatops.ErrWorkspace},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := valid
			if tc.edit != nil {
				r = tc.edit(r)
			}

			check(t, "Check", r.Check(tc.allow, tc.at), tc.want)
		})
	}
}

func TestLinkRequestValidate(t *testing.T) {
	t.Parallel()

	valid, err := chatops.NewLinkRequest(token(), user(), allow(), now())
	if err != nil {
		t.Fatalf("NewLinkRequest: %v", err)
	}

	cases := []struct {
		name string
		edit func(r chatops.LinkRequest) chatops.LinkRequest
		want error
	}{
		{"a valid request", nil, nil},
		{
			"the token in place of its hash",
			func(r chatops.LinkRequest) chatops.LinkRequest { r.TokenSHA256 = token(); return r }, chatops.ErrInvalid,
		},
		{
			"a hash in uppercase",
			func(r chatops.LinkRequest) chatops.LinkRequest {
				r.TokenSHA256 = strings.ToUpper(r.TokenSHA256)
				return r
			},
			chatops.ErrInvalid,
		},
		{
			"a long lifetime",
			func(r chatops.LinkRequest) chatops.LinkRequest { r.ExpiresAt = r.ExpiresAt.Add(time.Hour); return r },
			chatops.ErrInvalid,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := valid
			if tc.edit != nil {
				r = tc.edit(r)
			}

			check(t, "Validate", r.Validate(), tc.want)
		})
	}
}
