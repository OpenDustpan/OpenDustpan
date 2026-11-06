package session_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/session"
)

func TestSecretIsRandomAndRedacted(t *testing.T) {
	t.Parallel()

	a, err := session.NewSecret()
	if err != nil {
		t.Fatalf("new secret: %v", err)
	}

	b, err := session.NewSecret()
	if err != nil {
		t.Fatalf("new secret: %v", err)
	}

	if a.Reveal() == b.Reveal() {
		t.Fatal("two secrets are equal")
	}

	if !strings.HasPrefix(a.Reveal(), session.SecretPrefix) || len(a.Reveal()) != session.SecretLength {
		t.Fatalf("secret has length %d, want %d with the prefix %q", len(a.Reveal()), session.SecretLength,
			session.SecretPrefix)
	}

	for _, text := range []string{a.String(), fmt.Sprintf("%v %+v %#v %s", a, a, a, a)} {
		if strings.Contains(text, a.Reveal()) {
			t.Fatalf("formatted secret %q holds the secret", text)
		}
	}
}

func TestParseSecret(t *testing.T) {
	t.Parallel()

	s, err := session.NewSecret()
	if err != nil {
		t.Fatalf("new secret: %v", err)
	}

	got, err := session.ParseSecret(s.Reveal())
	if err != nil {
		t.Fatalf("parse a new secret: %v", err)
	}

	if session.DigestOf(got) != session.DigestOf(s) {
		t.Fatal("a parsed secret has another digest")
	}

	for _, raw := range []string{
		"",
		s.Reveal()[len(session.SecretPrefix):],
		"odp_" + s.Reveal()[len(session.SecretPrefix):],
		s.Reveal() + "A",
		s.Reveal()[:session.SecretLength-1] + "*",
	} {
		if _, err := session.ParseSecret(raw); !errors.Is(err, session.ErrInvalid) {
			t.Errorf("ParseSecret(%q): err = %v, want ErrInvalid", raw, err)
		}
	}
}

func TestCSRFValue(t *testing.T) {
	t.Parallel()

	a, err := session.NewSecret()
	if err != nil {
		t.Fatalf("new secret: %v", err)
	}

	b, err := session.NewSecret()
	if err != nil {
		t.Fatalf("new secret: %v", err)
	}

	value := session.CSRFValue(a)

	if value != session.CSRFValue(a) {
		t.Fatal("the CSRF value of one secret changes")
	}

	if strings.Contains(value, a.Reveal()[len(session.SecretPrefix):]) {
		t.Fatal("the CSRF value holds the secret")
	}

	if !session.CheckCSRF(a, value) {
		t.Fatal("the CSRF value of a secret fails its check")
	}

	for _, wrong := range []string{"", session.CSRFValue(b), value[:len(value)-1], value + "A"} {
		if session.CheckCSRF(a, wrong) {
			t.Errorf("CheckCSRF(%q) = true, want false", wrong)
		}
	}
}

func TestValidReturnPath(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"/":                             true,
		"/tokens":                       true,
		"/tokens?new=1#top":             true,
		"":                              false,
		"tokens":                        false,
		"//evil.example/":               false,
		"https://evil.example/":         false,
		"/\\evil.example":               false,
		"/a\nb":                         false,
		"/a\x7fb":                       false,
		"/" + strings.Repeat("a", 2048): false,
	}

	for path, want := range cases {
		if got := session.ValidReturnPath(path); got != want {
			t.Errorf("ValidReturnPath(%q) = %t, want %t", path, got, want)
		}
	}
}

func TestStatusAt(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, time.October, 15, 9, 0, 0, 0, time.UTC)
	s := session.Session{
		CreatedAt: created, LastSeenAt: created.Add(10 * time.Minute), AuthTime: created, EndedAt: time.Time{},
		ID: "ses-1", PrincipalID: "prn-1", Provider: "corp-idp", EndReason: "", SourceIP: "", UserAgent: "",
		Generation: 1,
	}
	limits := session.DefaultLimits()

	cases := []struct {
		now  time.Time
		want session.Status
	}{
		{now: created.Add(24 * time.Minute), want: session.StatusActive},
		{now: created.Add(25 * time.Minute), want: session.StatusIdle},
		{now: created.Add(12 * time.Hour), want: session.StatusExpired},
	}

	for _, tc := range cases {
		if got := s.StatusAt(tc.now, limits); got != tc.want {
			t.Errorf("status at %s = %s, want %s", tc.now, got, tc.want)
		}
	}

	if got := s.StatusAt(created.Add(time.Hour), session.Limits{Idle: 0, MaxLength: 0}); got != session.StatusActive {
		t.Errorf("status with no limits = %s, want active", got)
	}

	s.EndedAt = created.Add(11 * time.Minute)
	if got := s.StatusAt(created.Add(12*time.Minute), limits); got != session.StatusEnded {
		t.Errorf("status of an ended session = %s, want ended", got)
	}
}

func TestRef(t *testing.T) {
	t.Parallel()

	if got := session.Ref("ses-abc"); got != "session:ses-abc" {
		t.Errorf("Ref = %q", got)
	}

	for _, id := range []string{"", "tok-abc", "ses-a b", "ses-a\nb"} {
		if got := session.Ref(id); got != "session:invalid" {
			t.Errorf("Ref(%q) = %q, want session:invalid", id, got)
		}
	}

	if got := session.FullRef("prn-1", "ses-abc"); got != "principal:prn-1#session:ses-abc" {
		t.Errorf("FullRef = %q", got)
	}
}

func TestParseDigest(t *testing.T) {
	t.Parallel()

	s, err := session.NewSecret()
	if err != nil {
		t.Fatalf("new secret: %v", err)
	}

	d := session.DigestOf(s)

	got, err := session.ParseDigest(d.String())
	if err != nil || got != d {
		t.Fatalf("ParseDigest(String()) = %s, %v, want %s", got, err, d)
	}

	for _, raw := range []string{"", "zz", strings.ToUpper(d.String()), d.String()[:62]} {
		if _, err := session.ParseDigest(raw); !errors.Is(err, session.ErrInvalid) {
			t.Errorf("ParseDigest(%q): err = %v, want ErrInvalid", raw, err)
		}
	}
}
