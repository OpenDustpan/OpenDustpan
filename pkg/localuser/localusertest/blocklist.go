package localusertest

import (
	"context"
	"testing"
	"time"

	"golang.org/x/text/unicode/norm"

	"opendustpan.org/opendustpan/pkg/localuser"
)

// listedPassword is a password that each list of common passwords holds.
const listedPassword = "password"

// formPairs are passwords with letters that have two Unicode forms, in their
// NFC form. A list may hold them or not, but it must answer the same for
// the NFC form and the NFD form of each one.
func formPairs() []string {
	return []string{"café", "CAFÉ", "passwörd", "contraseña", "Ångström1", "mañana"}
}

// loadBound is the time that the first check may take: the load of the list
// from the binary. A check that waits on the network takes longer when no
// network answers.
const loadBound = 10 * time.Second

func blocklistCases() []struct {
	test func(t *testing.T, l localuser.Blocklist)
	name string
} {
	return []struct {
		test func(t *testing.T, l localuser.Blocklist)
		name string
	}{
		{name: "a password of the list is blocked", test: testListed},
		{name: "a password of the list in another case is blocked", test: testListedOtherCase},
		{name: "a password of the list in another Unicode normalization form is blocked", test: testListedOtherForm},
		{name: "a long random password is not blocked", test: testNotListed},
		{name: "the list loads from the binary and opens no network connection", test: testListLoads},
	}
}

// isBlocked returns the answer of l for candidate and fails the test on an
// error.
func isBlocked(t *testing.T, l localuser.Blocklist, candidate string) bool {
	t.Helper()

	got, err := l.Blocked(t.Context(), candidate)
	if err != nil {
		t.Fatalf("blocked %q: %v", candidate, err)
	}

	return got
}

func mustBlock(t *testing.T, l localuser.Blocklist, candidate string) {
	t.Helper()

	if !isBlocked(t, l, candidate) {
		t.Fatalf("blocked %q = false, want true", candidate)
	}
}

func mustAllow(t *testing.T, l localuser.Blocklist, candidate string) {
	t.Helper()

	if isBlocked(t, l, candidate) {
		t.Fatalf("blocked %q = true, want false", candidate)
	}
}

func testListed(t *testing.T, l localuser.Blocklist) {
	t.Helper()

	mustBlock(t, l, listedPassword)
	mustBlock(t, l, "123456")
}

func testListedOtherCase(t *testing.T, l localuser.Blocklist) {
	t.Helper()

	mustBlock(t, l, "PASSWORD")
	mustBlock(t, l, "PassWord")
}

func testListedOtherForm(t *testing.T, l localuser.Blocklist) {
	t.Helper()

	for _, nfc := range formPairs() {
		nfd := norm.NFD.String(nfc)
		if nfd == nfc {
			t.Fatalf("the NFD form of %q is its NFC form: the case tests nothing", nfc)
		}

		if a, b := isBlocked(t, l, nfc), isBlocked(t, l, nfd); a != b {
			t.Errorf("blocked %q = %t, and its NFD form = %t; want the same", nfc, a, b)
		}
	}
}

func testNotListed(t *testing.T, l localuser.Blocklist) {
	t.Helper()

	mustAllow(t, l, "vq3k-8mzt-2hwp-x7rn-c4jd")
}

func testListLoads(t *testing.T, l localuser.Blocklist) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), loadBound)
	defer cancel()

	start := time.Now()

	for range 3 {
		got, err := l.Blocked(ctx, listedPassword)
		if err != nil || !got {
			t.Fatalf("blocked %q = %t, %v; want true", listedPassword, got, err)
		}
	}

	if d := time.Since(start); d > loadBound {
		t.Fatalf("the first checks took %s, want the list from the binary", d)
	}
}
