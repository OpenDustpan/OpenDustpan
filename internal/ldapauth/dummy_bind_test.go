package ldapauth_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ldapauth"
	"opendustpan.org/opendustpan/internal/ldaptest"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/session/sessiontest"
)

// TestUnknownUserCostsTheBindsOfAWrongPassword proves that a user name that
// the search does not find makes the same binds as a wrong password: the
// service bind, then one bind that fails. The second bind of an unknown user
// names an entry below the base DN that is neither the service account nor a
// user, and both attempts get the same error.
func TestUnknownUserCostsTheBindsOfAWrongPassword(t *testing.T) {
	t.Parallel()

	s := newSetup(t)
	m := newMethod(t, s.baseConfig(sessiontest.DirectorySettings{
		Transport: sessiontest.TransportTLS, Subject: sessiontest.SubjectEntryUUID,
		Groups: sessiontest.DirectoryGroupsOff, AllowPlain: false, EgressRefused: false, OperationTimeout: 0,
	}))

	s.addUser(t, sessiontest.TestPerson{
		AuthTime: time.Time{}, Subject: "", UserName: "alice", Name: "", Email: "", Groups: nil,
		GroupsPresent: false, Disabled: false,
	}, "right", nil)

	bindsOf := func(name string) ([]string, error) {
		t.Helper()

		before := len(s.dir.Binds())
		_, err := m.Verify(t.Context(), session.Credentials{
			ProviderID: providerID, UserName: name, Password: session.NewPassword("wrong"),
		}, openGate{})

		return s.dir.Binds()[before:], err
	}

	wrong, wrongErr := bindsOf("alice")
	unknown, unknownErr := bindsOf("nobody")

	if len(wrong) != 2 || len(unknown) != len(wrong) {
		t.Fatalf("binds: wrong password %q, unknown user %q; want 2 each", wrong, unknown)
	}

	checkSameRejection(t, wrongErr, unknownErr)

	dummy := unknown[1]

	switch {
	case unknown[0] != ldaptest.ServiceDN:
		t.Fatalf("first bind of an unknown user = %q, want the service account", unknown[0])
	case strings.EqualFold(dummy, ldaptest.ServiceDN) || strings.EqualFold(dummy, ldaptest.UserDN("alice")) ||
		strings.EqualFold(dummy, ldaptest.UserDN("nobody")):
		t.Fatalf("dummy bind = %q, want an entry that is neither the service account nor a user", dummy)
	case !strings.HasSuffix(strings.ToLower(dummy), ","+strings.ToLower(ldaptest.BaseDN)):
		t.Fatalf("dummy bind = %q, want an entry below the base DN", dummy)
	default:
		// The dummy bind names an entry below the base DN that is no account.
	}
}

// checkSameRejection fails unless wrong and unknown are both
// ErrMethodRejected with the same text.
func checkSameRejection(t *testing.T, wrong, unknown error) {
	t.Helper()

	if wrong == nil || unknown == nil || !errors.Is(wrong, session.ErrMethodRejected) ||
		!errors.Is(unknown, session.ErrMethodRejected) || wrong.Error() != unknown.Error() {
		t.Fatalf("errors: wrong password %v, unknown user %v; want the same ErrMethodRejected", wrong, unknown)
	}
}

var errGateRefused = errors.New("the person is locked")

// TestGateRefusalCostsTheBindsOfAnUnknownUser proves that a known user name
// that the bind gate refuses makes the same binds as an unknown user name:
// the service bind, then one dummy bind below the base DN. The bind of the
// user itself never happens. Without the dummy bind the refused name is one
// round trip faster than an unknown name, and the time of the answer tells
// that the name exists.
func TestGateRefusalCostsTheBindsOfAnUnknownUser(t *testing.T) {
	t.Parallel()

	s := newSetup(t)
	m := newMethod(t, s.baseConfig(sessiontest.DirectorySettings{
		Transport: sessiontest.TransportTLS, Subject: sessiontest.SubjectEntryUUID,
		Groups: sessiontest.DirectoryGroupsOff, AllowPlain: false, EgressRefused: false, OperationTimeout: 0,
	}))

	s.addUser(t, testPerson("alice"), "right", nil)

	bindsOf := func(name string, gate session.BindGate) ([]string, error) {
		t.Helper()

		before := len(s.dir.Binds())
		_, err := m.Verify(t.Context(), credentials(name, "right"), gate)

		return s.dir.Binds()[before:], err
	}

	refused, refusedErr := bindsOf("alice", refusingGate{err: errGateRefused})
	unknown, unknownErr := bindsOf("nobody", openGate{})

	if !errors.Is(unknownErr, session.ErrMethodRejected) {
		t.Fatalf("error of an unknown name = %v, want ErrMethodRejected", unknownErr)
	}

	if !errors.Is(refusedErr, errGateRefused) {
		t.Fatalf("error of a refused name = %v, want the error of the gate", refusedErr)
	}

	if len(refused) != 2 || len(unknown) != len(refused) {
		t.Fatalf("binds: refused known name %q, unknown name %q; want 2 each", refused, unknown)
	}

	dummy := refused[1]

	switch {
	case refused[0] != ldaptest.ServiceDN:
		t.Fatalf("first bind of a refused name = %q, want the service account", refused[0])
	case strings.EqualFold(dummy, ldaptest.UserDN("alice")) || strings.EqualFold(dummy, ldaptest.ServiceDN):
		t.Fatalf("second bind = %q, want an entry that is neither the user nor the service account", dummy)
	case !strings.HasSuffix(strings.ToLower(dummy), ","+strings.ToLower(ldaptest.BaseDN)):
		t.Fatalf("dummy bind = %q, want an entry below the base DN", dummy)
	default:
		// The refused name made one dummy bind and no bind of the user.
	}
}

// TestDisabledPersonCostsTheBindsOfAnUnknownUser proves that a person that the
// directory reports as disabled, and a person whose disabled value fails
// closed, make the same binds as an unknown user name: the service bind, then
// one dummy bind below the base DN. The bind of the person never happens.
// Without the dummy bind such a name is one round trip faster than an unknown
// name, and the time of the answer tells that the name exists.
func TestDisabledPersonCostsTheBindsOfAnUnknownUser(t *testing.T) {
	t.Parallel()

	rule := ldapauth.Disabled{Attribute: "userAccountControl", Values: nil, Mask: 2}

	cases := []struct {
		want  error
		name  string
		value string
	}{
		{name: "the directory reports the person as disabled", value: "514", want: session.ErrSubjectDisabled},
		{name: "the disabled value is not a number", value: "x514", want: session.ErrMethodAnswer},
		{name: "the entry has no disabled value", value: "", want: session.ErrMethodAnswer},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newSetup(t)
			attrs := map[string][]string{"entryUUID": {"6ba7b810-9dad-11d1-80b4-00c04fd430c8"}}
			if tc.value != "" {
				attrs[rule.Attribute] = []string{tc.value}
			}

			s.dir.AddUser(ldaptest.User{Attributes: attrs, UserName: "alice", Password: goodPassword})

			m := newMethod(t, s.configWith(func(c *ldapauth.Config) { c.Disabled = rule }))

			bindsOf := func(name string) ([]string, error) {
				t.Helper()

				before := len(s.dir.Binds())
				_, err := m.Verify(t.Context(), credentials(name, goodPassword), openGate{})

				return s.dir.Binds()[before:], err
			}

			disabled, disabledErr := bindsOf("alice")
			unknown, unknownErr := bindsOf("nobody")

			if !errors.Is(unknownErr, session.ErrMethodRejected) {
				t.Fatalf("error of an unknown name = %v, want ErrMethodRejected", unknownErr)
			}

			if !errors.Is(disabledErr, tc.want) {
				t.Fatalf("error of the person = %v, want %v", disabledErr, tc.want)
			}

			if len(disabled) != 2 || len(unknown) != len(disabled) {
				t.Fatalf("binds: person %q, unknown name %q; want 2 each", disabled, unknown)
			}

			checkDummyBind(t, disabled)
		})
	}
}

// checkDummyBind fails unless binds are the bind of the service account and
// one dummy bind below the base DN, with no bind of the user alice.
func checkDummyBind(t *testing.T, binds []string) {
	t.Helper()

	dummy := binds[1]

	switch {
	case binds[0] != ldaptest.ServiceDN:
		t.Fatalf("first bind = %q, want the service account", binds[0])
	case strings.EqualFold(dummy, ldaptest.UserDN("alice")) || strings.EqualFold(dummy, ldaptest.ServiceDN):
		t.Fatalf("second bind = %q, want an entry that is neither the user nor the service account", dummy)
	case !strings.HasSuffix(strings.ToLower(dummy), ","+strings.ToLower(ldaptest.BaseDN)):
		t.Fatalf("dummy bind = %q, want an entry below the base DN", dummy)
	default:
		// The person made one dummy bind and no bind of its own.
	}
}
