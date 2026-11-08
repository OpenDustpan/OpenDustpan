package ldapauth_test

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/ldapauth"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/setting"
)

// plainSettings answers one effective item or one error, as the settings
// service of the installation does.
type plainSettings struct {
	item setting.Item
	err  error
}

// Effective implements egress.PlainRead.
func (s plainSettings) Effective(_ context.Context, _ setting.Scope, _ string) (setting.Item, error) {
	return s.item, s.err
}

// plainSettingsOf returns a reader that answers the effective item.
func plainSettingsOf(item setting.Item) plainSettings {
	return plainSettings{item: item, err: nil}
}

// plainSettingsError returns a reader that fails with err.
func plainSettingsError(err error) plainSettings {
	return plainSettings{item: plainSettingItem("false"), err: err}
}

// plainSettingItem returns the effective item of the plain-connection
// setting with the value.
func plainSettingItem(value string) setting.Item {
	for _, d := range egress.SettingDeclarations() {
		if d.Key == egress.SettingAllowPlainConnections {
			return setting.Effective(d, &value, nil)
		}
	}

	panic("ldapauth: no declaration for " + egress.SettingAllowPlainConnections)
}

// checkPlainRefusal fails the test unless err is the refusal of a plain
// connection that names address, the setting key and the call that changes
// it.
func checkPlainRefusal(t *testing.T, err error, address string) {
	t.Helper()

	if !errors.Is(err, ldapauth.ErrPlainRefused) || !errors.Is(err, session.ErrMethodUnavailable) {
		t.Fatalf("error = %v, want ErrPlainRefused and ErrMethodUnavailable", err)
	}

	text := err.Error()
	for _, want := range []string{
		address, egress.SettingAllowPlainConnections, "PATCH /api/dustpan/v1/settings",
		`{"egress.allowPlainConnections": true}`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("error = %q, want the text %q", text, want)
		}
	}
}

// TestAPlainConnectionNeedsTheInstallationSetting is layer 2 for the
// connection: a reader built on egress.PlainAllowed drives Method.open and
// the dial guard, each called directly as if the other layer had failed. A
// plain connection to a host name needs the setting; a plain connection to
// a loopback IP address does not.
func TestAPlainConnectionNeedsTheInstallationSetting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		reader egress.PlainRead
		want   error
		name   string
		rule   ldapauth.PlainRuleForTest
	}{
		{
			name: "the setting is off", reader: plainSettingsOf(plainSettingItem("false")),
			rule: ldapauth.PlainLoopbackOnlyForTest, want: ldapauth.ErrPlainRefused,
		},
		{
			name: "the setting is on", reader: plainSettingsOf(plainSettingItem("true")),
			rule: ldapauth.PlainAllowedForTest, want: nil,
		},
		{
			name: "the setting cannot be read", reader: plainSettingsError(errState),
			rule: ldapauth.PlainLoopbackOnlyForTest, want: ldapauth.ErrPlainRefused,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newSetup(t)

			checkDialGuard(t, tc.rule, tc.want)
			checkPlainOpen(t, s, tc.reader, tc.want)
		})
	}
}

// checkDialGuard calls the dial guard directly with the address that the
// name lookup gives, as if Method.open had not refused. want is the error
// that the guard answers, nil when it allows the connection.
func checkDialGuard(t *testing.T, rule ldapauth.PlainRuleForTest, want error) {
	t.Helper()

	private := egress.Rules{Allow: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, LoopbackForTests: true}

	err := ldapauth.DialControlForTest(private, rule)("tcp", "10.0.0.5:389", nil)
	if want == nil {
		if err != nil {
			t.Fatalf("dialControl(10.0.0.5:389) = %v, want the connection allowed", err)
		}

		return
	}

	if !errors.Is(err, want) {
		t.Fatalf("dialControl(10.0.0.5:389) = %v, want %v", err, want)
	}

	checkPlainRefusal(t, err, "10.0.0.5:389")
}

// checkPlainOpen calls Method.open directly with a plain URL of the test
// directory, as if the dial guard had passed. want is the error that open
// answers, nil when it opens the connection.
func checkPlainOpen(t *testing.T, s setup, read egress.PlainRead, want error) {
	t.Helper()

	url := s.dir.PlainNameURL()

	cfg := s.configWith(func(c *ldapauth.Config) {
		c.URL = url
		c.AllowPlain = func(ctx context.Context) (bool, error) {
			return egress.PlainAllowed(ctx, read), nil
		}
	})

	_, done, err := ldapauth.OpenForTest(t.Context(), newMethod(t, cfg))
	if want == nil {
		if err != nil {
			t.Fatalf("open() = %v, want a connection", err)
		}

		done()

		return
	}

	if !errors.Is(err, want) {
		t.Fatalf("open() = %v, want %v", err, want)
	}

	checkPlainRefusal(t, err, strings.TrimPrefix(url, "ldap://"))

	if got := s.dir.Connections(); got != 0 {
		t.Fatalf("connections = %d after a refused plain connection, want 0", got)
	}
}
