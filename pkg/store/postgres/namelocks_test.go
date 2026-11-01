package postgres_test

import (
	"bytes"
	"os"
	"testing"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/authgate/authgatetest"
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
			Broken: func(t *testing.T) authgate.NameLockCounter {
				t.Helper()

				closed := openURL(t, schemaURL)
				closed.Close()

				return closed
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
