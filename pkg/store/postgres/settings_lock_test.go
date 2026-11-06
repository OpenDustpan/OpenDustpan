package postgres_test

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/setting"
	"opendustpan.org/opendustpan/pkg/setting/settingtest"
)

// raceEntry returns a valid audit entry in the scope of the namespace acme.
func raceEntry(action string) audit.Entry {
	return audit.Entry{
		At: time.UnixMilli(1), Namespace: "acme", Subject: "alice", IDPSubject: "", TokenID: "", Action: action,
		Object: "acme", Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1",
		UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess,
		RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, Reason: "",
		PrincipalKind: "", JobID: "",
	}
}

// TestSettingsAndGrantWritesDoNotDeadlock races writes of the settings of a
// namespace against grant writes in the same namespace. Both take the lock of
// the server audit scope before the row of the namespace, so PostgreSQL never
// aborts one of them with a deadlock (SQLSTATE 40P01).
func TestSettingsAndGrantWritesDoNotDeadlock(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL suite", envURL)
	}

	s := openURL(t, isolatedURL(t, base))
	fence := ensureNamespace(t, s, "acme")
	scope := setting.NamespaceScope("acme")
	store := s.SettingStore()

	const (
		workers = 6
		rounds  = 20
	)

	var wg sync.WaitGroup

	errs := make(chan error, 2*workers*rounds)

	for w := range workers {
		wg.Go(func() {
			for round := range rounds {
				errs <- writeSetting(t, store, scope, fence, w*rounds+round)
			}
		})

		wg.Go(func() {
			for round := range rounds {
				id := "grt-lock-" + strconv.Itoa(w) + "-" + strconv.Itoa(round)
				errs <- s.CreateGrant(t.Context(), raceGrant(id, w*rounds+round), fence, raceEntry("namespace:manage-access"), nil)
			}
		})
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("write failed: %v", err)

			if strings.Contains(err.Error(), "40P01") {
				t.Error("PostgreSQL aborted a write with a deadlock")
			}
		}
	}
}

// raceGrant returns a viewer grant in acme to a principal.
func raceGrant(id string, n int) access.Grant {
	return access.Grant{
		CreatedAt: time.UnixMilli(1), Condition: nil, Object: identity.NamespaceObject("acme"), ID: id,
		Role: "viewer", CreatedBy: "admin",
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: "prn-lock-" + strconv.Itoa(n)},
	}
}

// writeSetting changes a setting of the namespace. A stale revision reads the
// revision again and tries once more, as a client does.
func writeSetting(t *testing.T, store setting.Store, scope setting.Scope, fence namespace.Fence, n int) error {
	t.Helper()

	for range 50 {
		stored, err := store.Load(t.Context(), scope)
		if err != nil {
			return err
		}

		in := setting.ChangeInput{
			Registry: settingtest.Registry(), Static: nil,
			Patch: setting.Patch{{Key: settingtest.KeyNSLimit, Value: strconv.Itoa(n%9000 + 1), Delete: false}},
			Entry: raceEntry("namespace:manage-settings"), IfMatch: stored.Revision, Fresh: false, AtMs: 1,
		}

		_, err = store.Update(t.Context(), scope, fence, in)
		if !errors.Is(err, setting.ErrPreconditionFailed) {
			return err
		}
	}

	return setting.ErrPreconditionFailed
}
