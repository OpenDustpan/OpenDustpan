package dustpan

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// plainNamespaceStore is a namespace store that holds no passkeys: the
// embedded interface gives it the methods of namespace.Store and no more.
type plainNamespaceStore struct {
	namespace.Store
}

// passkeyWiringCase is one row of TestNewPasskeyService. logged is the text
// that the log must hold, or empty for no check of the log.
type passkeyWiringCase struct {
	name   string
	plain  bool
	url    string
	want   bool
	logged string
}

// off is the log line of a server with no public URL.
const off = "passkeys are off: the server has no public URL"

// noRelyingParty is the log line of a public URL that gives no relying party.
const noRelyingParty = "the public URL gives no relying party"

// run builds the passkey service for the case over st and checks it.
func (tc passkeyWiringCase) run(t *testing.T, st *sqlite.Store) {
	t.Helper()

	var store namespace.Store = st
	if tc.plain {
		store = plainNamespaceStore{Store: st}
	}

	var out bytes.Buffer

	log := slog.New(slog.NewTextHandler(&out, nil))

	got, err := newPasskeyService(store, tc.url, st, time.Now, log)
	if err != nil {
		t.Fatalf("newPasskeyService: %v", err)
	}

	if (got.service != nil) != tc.want {
		t.Fatalf("service built = %v, want %v", got.service != nil, tc.want)
	}

	if !strings.Contains(out.String(), tc.logged) {
		t.Fatalf("log = %q, want %q", out.String(), tc.logged)
	}

	if tc.want && out.Len() != 0 {
		t.Fatalf("log = %q, want none for a service that runs", out.String())
	}
}

// TestNewPasskeyService proves when the area builds the passkey service: the
// store must keep passkeys, and the public URL must give a relying party. In
// each other case the area builds none, so the routes are not mounted, and
// the log line names the reason.
func TestNewPasskeyService(t *testing.T) {
	t.Parallel()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	tests := []passkeyWiringCase{
		{"https URL", false, "https://dustpan.example.com", true, ""},
		{"https URL with a port and a path", false, "https://dustpan.example.com:8443/base", true, ""},
		{"http on localhost", false, "http://localhost:8080", true, ""},
		{"URL with spaces around it", false, "  https://dustpan.example.com  ", true, ""},
		{"no public URL", false, "", false, off},
		{"only spaces", false, "   ", false, off},
		{"http on another host", false, "http://dustpan.example.com", false, noRelyingParty},
		{"IP address", false, "https://192.0.2.10", false, noRelyingParty},
		{"user information", false, "https://user@dustpan.example.com", false, noRelyingParty},
		{"query", false, "https://dustpan.example.com/?a=b", false, noRelyingParty},
		{"not a URL", false, "dustpan", false, noRelyingParty},
		{"store without passkeys", true, "https://dustpan.example.com", false, "the store keeps no passkeys"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.run(t, st)
		})
	}
}
