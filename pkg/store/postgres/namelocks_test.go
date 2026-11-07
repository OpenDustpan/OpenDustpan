package postgres_test

import (
	"bytes"
	"os"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/authgate/authgatetest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// TestNameLockConformance runs the suite of the lockout by name: each handle
// opens one schema, as two servers open one database. The broken handle is a
// store that is closed.
func TestNameLockConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	authgatetest.RunNameLocks(t, func(t *testing.T) authgatetest.NameLockHarness {
		t.Helper()

		schemaURL := isolatedURL(t, base)

		return authgatetest.NameLockHarness{
			Open: func(t *testing.T) authgate.NameLockCounter {
				t.Helper()

				return openURL(t, schemaURL)
			},
			Now: func(t *testing.T) time.Time {
				t.Helper()

				now, err := openURL(t, schemaURL).DatabaseNow(t.Context())
				if err != nil {
					t.Fatalf("read the clock of the store: %v", err)
				}

				return now
			},
			Broken: func(t *testing.T) authgate.NameLockCounter {
				t.Helper()

				closed := openURL(t, schemaURL)
				closed.Close()

				return closed
			},
			Entries: func(t *testing.T) []audit.Entry {
				t.Helper()

				return serverEntries(t, openURL(t, schemaURL))
			},
			Notices: func(t *testing.T) []notify.Notification {
				t.Helper()

				return serverNotices(t, openURL(t, schemaURL).Notifications())
			},
		}
	})
}

// TestNameLockKey proves that two handles on one database read one key of 32
// bytes.
func TestNameLockKey(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	schemaURL := isolatedURL(t, base)

	first, err := openURL(t, schemaURL).NameLockKey(t.Context())
	if err != nil || len(first) != 32 {
		t.Fatalf("key = %d bytes, %v; want 32", len(first), err)
	}

	second, err := openURL(t, schemaURL).NameLockKey(t.Context())
	if err != nil || !bytes.Equal(second, first) {
		t.Fatalf("second handle reads another key or fails: %v", err)
	}
}

// serverEntries returns the audit entries of the installation that s holds.
func serverEntries(t *testing.T, s *postgres.Store) []audit.Entry {
	t.Helper()

	page, err := s.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	return page.Entries
}

// serverNotices returns the notifications of the server scope that n holds,
// in the order of the append.
func serverNotices(t *testing.T, n notify.Outbox) []notify.Notification {
	t.Helper()

	got, err := n.After(t.Context(), audit.ServerScope, 0, notify.MaxRecipients*1000)
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}

	return got
}
