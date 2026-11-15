package forgelink_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/forgelink"
)

func gh(id string) forgelink.Account {
	return forgelink.Account{Kind: forge.KindGitHub, Host: "github.com", ID: id}
}

func TestAccountValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a    forgelink.Account
		ok   bool
	}{
		{"github", forgelink.Account{Kind: forge.KindGitHub, Host: "github.com", ID: "583231"}, true},
		{"gitlab with a port", forgelink.Account{Kind: forge.KindGitLab, Host: "git.example.com:8443", ID: "7"}, true},
		{"unknown kind", forgelink.Account{Kind: "svn", Host: "github.com", ID: "1"}, false},
		{"host in capitals", forgelink.Account{Kind: forge.KindGitHub, Host: "GitHub.com", ID: "1"}, false},
		{"host with a path", forgelink.Account{Kind: forge.KindGitHub, Host: "github.com/x", ID: "1"}, false},
		{"host with a scheme", forgelink.Account{Kind: forge.KindGitHub, Host: "https://github.com", ID: "1"}, false},
		{"host with a zero port", forgelink.Account{Kind: forge.KindGitHub, Host: "github.com:0", ID: "1"}, false},
		{"empty host", forgelink.Account{Kind: forge.KindGitHub, Host: "", ID: "1"}, false},
		{"empty ID", forgelink.Account{Kind: forge.KindGitHub, Host: "github.com", ID: ""}, false},
		{"ID with a space", forgelink.Account{Kind: forge.KindGitHub, Host: "github.com", ID: "1 2"}, false},
		{"ID with a newline", forgelink.Account{Kind: forge.KindGitHub, Host: "github.com", ID: "1\n2"}, false},
		{
			"ID with a zero-width space",
			forgelink.Account{Kind: forge.KindGitHub, Host: "github.com", ID: "1\u200b2"},
			false,
		},
		{"ID that is not UTF-8", forgelink.Account{Kind: forge.KindGitHub, Host: "github.com", ID: "\xff"}, false},
		{
			"long ID",
			forgelink.Account{
				Kind: forge.KindGitHub,
				Host: "github.com",
				ID:   strings.Repeat("1", forgelink.MaxIDLen+1),
			},
			false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.a.Validate()
			if (err == nil) != tc.ok || (err != nil && !errors.Is(err, forgelink.ErrInvalid)) {
				t.Errorf("Validate = %v, want ok %v", err, tc.ok)
			}
		})
	}
}

func TestNewRequest(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	token := strings.Repeat("a", forgelink.MinTokenLen)

	r, err := forgelink.NewRequest(token, "prn-ana", now)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	if r.TokenSHA256 != forgelink.HashToken(token) || strings.Contains(r.TokenSHA256, token) ||
		!r.ExpiresAt.Equal(now.Add(forgelink.RequestLifetime)) {
		t.Errorf("request = %+v", r)
	}

	if r.IsProved() {
		t.Error("a new request is proved")
	}

	for name, tok := range map[string]string{"short": token[:forgelink.MinTokenLen-1], "long": strings.Repeat("a", 513), "empty": ""} {
		if _, err := forgelink.NewRequest(tok, "prn-ana", now); !errors.Is(err, forgelink.ErrInvalid) {
			t.Errorf("%s token = %v, want ErrInvalid", name, err)
		}
	}

	if _, err := forgelink.NewRequest(token, "", now); !errors.Is(err, forgelink.ErrInvalid) {
		t.Errorf("no principal = %v, want ErrInvalid", err)
	}
}

func TestRequestCheckAccept(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)

	fresh, err := forgelink.NewRequest(strings.Repeat("a", forgelink.MinTokenLen), "prn-ana", now)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	proved := fresh
	proved.Proved = forgelink.Proof{Account: gh("42"), Login: "ana", DeliveryID: "d"}
	proved.ProvedAt = now.Add(time.Second)

	used := proved
	used.UsedAt = now.Add(2 * time.Second)

	tests := []struct {
		name      string
		r         forgelink.Request
		principal string
		at        time.Time
		want      error
	}{
		{"proved and good", proved, "prn-ana", now.Add(time.Minute), nil},
		{"not proved", fresh, "prn-ana", now.Add(time.Minute), forgelink.ErrNotProved},
		{"other principal", proved, "prn-bob", now.Add(time.Minute), forgelink.ErrDenied},
		{"no principal", proved, "", now.Add(time.Minute), forgelink.ErrDenied},
		{"used", used, "prn-ana", now.Add(time.Minute), forgelink.ErrUsed},
		{"at the expiry", proved, "prn-ana", now.Add(forgelink.RequestLifetime), forgelink.ErrExpired},
		{"just before the expiry", proved, "prn-ana", now.Add(forgelink.RequestLifetime - time.Millisecond), nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.r.CheckAccept(tc.principal, tc.at)
			if !errors.Is(err, tc.want) && (err != nil || tc.want != nil) {
				t.Errorf("CheckAccept = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestRequestValidateProof(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)

	r, err := forgelink.NewRequest(strings.Repeat("a", forgelink.MinTokenLen), "prn-ana", now)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	half := r
	half.ProvedAt = now

	account := r
	account.Proved.Account = gh("42")

	for name, bad := range map[string]forgelink.Request{"a time without an account": half, "an account without a time": account} {
		if err := bad.Validate(); !errors.Is(err, forgelink.ErrInvalid) {
			t.Errorf("%s = %v, want ErrInvalid", name, err)
		}
	}
}
