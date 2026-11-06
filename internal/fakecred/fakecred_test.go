package fakecred_test

import (
	"net/url"
	"testing"

	"opendustpan.org/opendustpan/internal/fakecred"
)

func TestURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                               string
		scheme, user, password, host, path string
		want                               string
	}{
		{"user and password", "https", "user", "pw", "hooks.example.com", "x", "https://user:pw@hooks.example.com/x"},
		{"empty path", "https", "u", "p", "host", "", "https://u:p@host/"},
		{
			"empty password",
			"https",
			"user",
			"",
			"git.example.com",
			"acme/infra.git",
			"https://user:@git.example.com/acme/infra.git",
		},
		{"port", "https", "user", "pw", "127.0.0.1:1", "", "https://user:pw@127.0.0.1:1/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := fakecred.URL(tt.scheme, tt.user, tt.password, tt.host, tt.path)
			if got != tt.want {
				t.Fatalf("URL = %q, want %q", got, tt.want)
			}
			u, err := url.Parse(got)
			if err != nil {
				t.Fatalf("parse %q: %v", got, err)
			}
			pw, ok := u.User.Password()
			if u.User.Username() != tt.user || !ok || pw != tt.password {
				t.Fatalf("user info of %q = %q, want %q and %q", got, u.User, tt.user, tt.password)
			}
		})
	}
}

func TestURLEscapesSpecialCharacters(t *testing.T) {
	t.Parallel()
	got := fakecred.URL("https", "us@er", "p:w/", "host", "")
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse %q: %v", got, err)
	}
	pw, _ := u.User.Password()
	if u.User.Username() != "us@er" || pw != "p:w/" || u.Host != "host" {
		t.Fatalf("round trip of %q gave %q, %q, %q", got, u.User.Username(), pw, u.Host)
	}
}
