package ldapauth_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/go-ldap/ldap/v3"

	"opendustpan.org/opendustpan/internal/ldapauth"
)

// TestEscapeFilter checks the filter escape of RFC 4515 on hostile values.
func TestEscapeFilter(t *testing.T) {
	t.Parallel()

	cases := []struct{ in, want string }{
		{"alice", "alice"},
		{"*", `\2a`},
		{"*)(uid=*", `\2a\29\28uid=\2a`},
		{`a\b`, `a\5cb`},
		{"a\x00b", `a\00b`},
		{"é", `\c3\a9`},
		{"", ""},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			if got := ldapauth.EscapeFilter(tc.in); got != tc.want {
				t.Fatalf("EscapeFilter(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestEscapeDNValue checks the DN escape of RFC 4514 on hostile values.
func TestEscapeDNValue(t *testing.T) {
	t.Parallel()

	cases := []struct{ in, want string }{
		{"alice", "alice"},
		{"alice,ou=admins", `alice\,ou\=admins`},
		{"a+b", `a\+b`},
		{`"q";<x>`, `\"q\"\;\<x\>`},
		{"#tag", `\#tag`},
		{" lead", `\ lead`},
		{"trail ", `trail\ `},
		{`back\slash`, `back\\slash`},
		{"nul\x00", `nul\00`},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			if got := ldapauth.EscapeDNValue(tc.in); got != tc.want {
				t.Fatalf("EscapeDNValue(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// FuzzEscapeFilter checks that an escaped value is one equality item with
// the value itself, whatever the value holds.
func FuzzEscapeFilter(f *testing.F) {
	for _, seed := range []string{"alice", "*", "*)(uid=*", `\`, "\x00", "(", "é", "\xff"} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, v string) {
		filter := "(uid=" + ldapauth.EscapeFilter(v) + ")"

		packet, err := ldap.CompileFilter(filter)
		if err != nil {
			t.Fatalf("CompileFilter(%q) = %v", filter, err)
		}

		const equalityMatch = 3

		if packet.Tag != equalityMatch || len(packet.Children) != 2 {
			t.Fatalf("filter %q is not one equality item", filter)
		}

		if got := packet.Children[1].Data.String(); got != v {
			t.Fatalf("the value of %q is %q, want %q", filter, got, v)
		}
	})
}

// FuzzEscapeDNValue checks that an escaped value is one attribute value of
// one RDN, with the value itself, whatever the value holds.
func FuzzEscapeDNValue(f *testing.F) {
	for _, seed := range []string{"alice", "a,b", "a+b=c", " x ", "#x", `\`, "\x00", "é", `"`} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, v string) {
		if !utf8.ValidString(v) || strings.TrimSpace(v) != v {
			// A DN holds UTF-8. The parser trims the spaces at the ends of
			// a value even when they are escaped.
			t.Skip()
		}

		dn := "cn=" + ldapauth.EscapeDNValue(v) + ",dc=example"

		parsed, err := ldap.ParseDN(dn)
		if err != nil {
			t.Fatalf("ParseDN(%q) = %v", dn, err)
		}

		if len(parsed.RDNs) != 2 || len(parsed.RDNs[0].Attributes) != 1 {
			t.Fatalf("DN %q has another structure", dn)
		}

		if got := parsed.RDNs[0].Attributes[0].Value; got != v {
			t.Fatalf("the value of %q is %q, want %q", dn, got, v)
		}
	})
}
