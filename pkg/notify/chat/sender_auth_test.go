package chat_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/chat"
	"opendustpan.org/opendustpan/pkg/notify/chat/slack"
)

// authRenderer is a renderer for a platform that needs an access token. It
// takes everything else from Slack.
type authRenderer struct{ slack.Renderer }

func (authRenderer) AuthSecret() bool { return true }

// noAuthRenderer implements AuthRenderer and says that its platform needs no
// token.
type noAuthRenderer struct{ slack.Renderer }

func (noAuthRenderer) AuthSecret() bool { return false }

const authCanary = "syt_AUTH-CANARY-0c4b"

// authMessage returns message() with the access token as its one secret.
func authMessage(token string) notify.Message {
	m := message()
	m.Secrets = [][]byte{[]byte(token)}

	return m
}

func newAuthSender(t *testing.T, recv *receiver, r chat.Renderer) *chat.Sender {
	t.Helper()

	s, err := chat.NewForTest(recv.srv, now, r)
	if err != nil {
		t.Fatalf("NewForTest: %v", err)
	}

	t.Cleanup(s.Close)

	return s
}

func TestSenderSendsTheAccessTokenAsABearerHeader(t *testing.T) {
	t.Parallel()

	recv := newReceiver(t)
	s := newAuthSender(t, recv, authRenderer{Renderer: slack.New()})

	if err := s.Send(t.Context(), authMessage(authCanary)); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got := recv.requests()
	if len(got) != 1 || got[0].header.Get("Authorization") != "Bearer "+authCanary {
		t.Fatalf("requests = %+v, want one with the header Authorization: Bearer <token>", got)
	}

	if strings.Contains(string(got[0].body), authCanary) {
		t.Error("the body holds the access token")
	}
}

func TestSenderSendsNoTokenToAPlatformThatTakesNone(t *testing.T) {
	t.Parallel()

	for name, r := range map[string]chat.Renderer{
		"a renderer that is no AuthRenderer":  slack.New(),
		"an AuthRenderer that needs no token": noAuthRenderer{Renderer: slack.New()},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			recv := newReceiver(t)
			s := newAuthSender(t, recv, r)

			if err := s.Send(t.Context(), authMessage(authCanary)); err != nil {
				t.Fatalf("Send: %v", err)
			}

			got := recv.requests()
			if len(got) != 1 || got[0].header.Get("Authorization") != "" {
				t.Fatalf("requests = %+v, want one with no Authorization header", got)
			}
		})
	}
}

// TestSenderRefusesAnUnusableAccessToken sends a hostile token straight to the
// sender, as if the sink service had not checked it. The sender must refuse it
// with ErrPermanent, open no connection, and keep the token out of the error.
func TestSenderRefusesAnUnusableAccessToken(t *testing.T) {
	t.Parallel()

	for name, token := range map[string]string{
		"no token":            "",
		"a space":             "syt_a b" + authCanary,
		"a line feed":         "syt_a\nX-Evil: 1" + authCanary,
		"a carriage return":   "syt_a\r\nX-Evil: 1" + authCanary,
		"a tab":               "syt_a\tb" + authCanary,
		"a NUL byte":          "syt_a\x00b" + authCanary,
		"a DEL byte":          "syt_a\x7fb" + authCanary,
		"a non-ASCII byte":    "syt_é" + authCanary,
		"a token above limit": strings.Repeat("t", notify.MaxSecretLen+1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			recv := newReceiver(t)
			s := newAuthSender(t, recv, authRenderer{Renderer: slack.New()})

			err := s.Send(t.Context(), authMessage(token))
			if !errors.Is(err, notify.ErrPermanent) {
				t.Fatalf("Send = %v, want ErrPermanent", err)
			}

			if strings.Contains(err.Error(), authCanary) || (token != "" && strings.Contains(err.Error(), token)) {
				t.Errorf("the error text holds the token: %v", err)
			}

			if got := recv.requests(); len(got) != 0 {
				t.Errorf("the server got %d requests, want none", len(got))
			}
		})
	}
}

func TestSenderWithNoSecretRefusesAPlatformThatNeedsAToken(t *testing.T) {
	t.Parallel()

	recv := newReceiver(t)
	s := newAuthSender(t, recv, authRenderer{Renderer: slack.New()})

	m := message()
	m.Secrets = nil

	if err := s.Send(t.Context(), m); !errors.Is(err, notify.ErrPermanent) {
		t.Fatalf("Send = %v, want ErrPermanent", err)
	}

	if got := recv.requests(); len(got) != 0 {
		t.Errorf("the server got %d requests, want none", len(got))
	}
}

func TestTakesAuthSecret(t *testing.T) {
	t.Parallel()

	s := chat.NewSender([]chat.Renderer{
		authRenderer{Renderer: slack.New()}, noAuthRenderer{Renderer: slack.New()},
	})
	t.Cleanup(s.Close)

	// Both renderers serve Slack, so the later one wins: it needs no token.
	if s.TakesAuthSecret("slack") || s.TakesAuthSecret("teams") || s.TakesAuthSecret("") {
		t.Error("TakesAuthSecret is true for a platform that needs no token or has no renderer")
	}

	s2 := chat.NewSender([]chat.Renderer{authRenderer{Renderer: slack.New()}})
	t.Cleanup(s2.Close)

	if !s2.TakesAuthSecret("slack") {
		t.Error("TakesAuthSecret(slack) is false for a renderer that needs a token")
	}
}
