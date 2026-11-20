package profile_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/profile"
)

func sessionStartTime() time.Time { return time.UnixMilli(1_700_000_000_000).UTC() }

func validSessionParams() profile.SessionParams {
	return profile.SessionParams{
		ID: "pos-abc123", Namespace: "acme", Root: "prf-abc123", Owner: profile.PrincipalOwner("alice"),
		Consumer: profile.OpenConsumerAPI, Paths: []string{"db.password"}, Sealed: []byte("sealed"),
		CreatedAt: sessionStartTime(), ExpiresAt: sessionStartTime().Add(time.Hour),
	}
}

func TestNewSession(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", profile.MaxSessionPathBytes+1)
	manyPaths := make([]string, profile.MaxSessionPaths+1)

	for i := range manyPaths {
		manyPaths[i] = "p"
	}

	tests := []struct {
		name   string
		change func(p *profile.SessionParams)
		ok     bool
	}{
		{"valid", func(*profile.SessionParams) {}, true},
		{"no paths means all", func(p *profile.SessionParams) { p.Paths = nil }, true},
		{"the longest duration", func(p *profile.SessionParams) {
			p.ExpiresAt = p.CreatedAt.Add(profile.MaxSessionDuration)
		}, true},
		{"an ID without the prefix", func(p *profile.SessionParams) { p.ID = "abc123" }, false},
		{"an ID that is only the prefix", func(p *profile.SessionParams) { p.ID = "pos-" }, false},
		{"an ID with upper case", func(p *profile.SessionParams) { p.ID = "pos-ABC" }, false},
		{"an ID with a slash", func(p *profile.SessionParams) { p.ID = "pos-a/b" }, false},
		{"an ID with a newline", func(p *profile.SessionParams) { p.ID = "pos-a\nb" }, false},
		{"no namespace", func(p *profile.SessionParams) { p.Namespace = "" }, false},
		{"a root that is not a profile ID", func(p *profile.SessionParams) { p.Root = "payments" }, false},
		{
			"an owner of the kind job",
			func(p *profile.SessionParams) { p.Owner = profile.SessionOwner{Kind: "job", ID: "j"} },
			false,
		},
		{"an owner with no subject", func(p *profile.SessionParams) { p.Owner = profile.PrincipalOwner("") }, false},
		{"an owner with a CR", func(p *profile.SessionParams) { p.Owner = profile.PrincipalOwner("al\rice") }, false},
		{"an owner subject that is too long", func(p *profile.SessionParams) {
			p.Owner = profile.PrincipalOwner(strings.Repeat("a", profile.MaxSessionOwnerBytes+1))
		}, false},
		{"the consumer run", func(p *profile.SessionParams) { p.Consumer = "run" }, false},
		{"the consumer opentofu", func(p *profile.SessionParams) { p.Consumer = "opentofu" }, false},
		{"no consumer", func(p *profile.SessionParams) { p.Consumer = "" }, false},
		{"an empty path", func(p *profile.SessionParams) { p.Paths = []string{"a", ""} }, false},
		{"a path with a CR", func(p *profile.SessionParams) { p.Paths = []string{"a\rb"} }, false},
		{"a path with an LF", func(p *profile.SessionParams) { p.Paths = []string{"a\nb"} }, false},
		{"a path with a NUL", func(p *profile.SessionParams) { p.Paths = []string{"a\x00b"} }, false},
		{"a path that is too long", func(p *profile.SessionParams) { p.Paths = []string{long} }, false},
		{"too many paths", func(p *profile.SessionParams) { p.Paths = manyPaths }, false},
		{"no sealed bytes", func(p *profile.SessionParams) { p.Sealed = nil }, false},
		{
			"too many sealed bytes",
			func(p *profile.SessionParams) { p.Sealed = make([]byte, profile.MaxSealedBytes+1) },
			false,
		},
		{"an end equal to the start", func(p *profile.SessionParams) { p.ExpiresAt = p.CreatedAt }, false},
		{
			"an end before the start",
			func(p *profile.SessionParams) { p.ExpiresAt = p.CreatedAt.Add(-time.Second) },
			false,
		},
		{"a duration above the limit", func(p *profile.SessionParams) {
			p.ExpiresAt = p.CreatedAt.Add(profile.MaxSessionDuration + time.Millisecond)
		}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := validSessionParams()
			tc.change(&p)

			s, err := profile.NewSession(p)
			if tc.ok {
				if err != nil || s.ID() != p.ID {
					t.Fatalf("NewSession = %v, %v, want a session", s, err)
				}

				return
			}

			if !errors.Is(err, profile.ErrInvalid) {
				t.Fatalf("NewSession error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestSessionKeepsNoAlias(t *testing.T) {
	t.Parallel()

	p := validSessionParams()

	s, err := profile.NewSession(p)
	if err != nil {
		t.Fatal(err)
	}

	p.Paths[0], p.Sealed[0] = "changed", 'X'
	s.Paths()[0], s.Sealed()[0] = "changed", 'X'

	if s.Paths()[0] != "db.password" || string(s.Sealed()) != "sealed" {
		t.Errorf("session = %v %q, want it to keep its own copy", s.Paths(), s.Sealed())
	}
}

func TestSessionLive(t *testing.T) {
	t.Parallel()

	s, err := profile.NewSession(validSessionParams())
	if err != nil {
		t.Fatal(err)
	}

	end := s.ExpiresAt()

	for _, tc := range []struct {
		now  time.Time
		live bool
	}{
		{sessionStartTime(), true}, {end.Add(-time.Nanosecond), true}, {end, false}, {end.Add(time.Second), false},
	} {
		if got := s.Live(tc.now); got != tc.live {
			t.Errorf("Live(%s) = %t, want %t", tc.now.Format(time.RFC3339Nano), got, tc.live)
		}
	}
}

func TestParseSessionID(t *testing.T) {
	t.Parallel()

	for _, id := range []string{profile.NewSessionID(), "pos-a", "pos-a-b-1"} {
		if _, err := profile.ParseSessionID(id); err != nil {
			t.Errorf("ParseSessionID(%q) = %v, want nil", id, err)
		}
	}

	for _, id := range []string{
		"", "pos-", "pos-A", "pos-a b", "pos-a%2Fb", "pos-a/b", "pos-é", "prf-abc", " pos-a",
		"pos-" + strings.Repeat("a", 65),
	} {
		if _, err := profile.ParseSessionID(id); !errors.Is(err, profile.ErrInvalid) {
			t.Errorf("ParseSessionID(%q) error = %v, want ErrInvalid", id, err)
		}
	}
}

func TestParseOpenConsumer(t *testing.T) {
	t.Parallel()

	for _, c := range []string{"api", "console", "pulumi"} {
		if got, err := profile.ParseOpenConsumer(c); err != nil || string(got) != c {
			t.Errorf("ParseOpenConsumer(%q) = %q, %v", c, got, err)
		}
	}

	for _, c := range []string{"", "run", "opentofu", "launcher", "observe", "API", "api ", "job-proxy"} {
		if _, err := profile.ParseOpenConsumer(c); !errors.Is(err, profile.ErrInvalid) {
			t.Errorf("ParseOpenConsumer(%q) error = %v, want ErrInvalid", c, err)
		}
	}
}

func TestSessionLimitWrapsErrLimit(t *testing.T) {
	t.Parallel()

	if !errors.Is(profile.ErrSessionLimit, profile.ErrLimit) {
		t.Error("ErrSessionLimit does not wrap ErrLimit: the API maps ErrLimit to profile.limit")
	}
}
