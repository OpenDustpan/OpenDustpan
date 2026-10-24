package tofuhttp_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestLockRefusedByAManualLock proves a LOCK that a manual lock refuses answers
// 423 with lock info that names the holder in Who and the reason in Info, and
// that it does so for a unit with no tool lock of its own. A unit that the
// service cannot lock for another reason keeps its answer.
func TestLockRefusedByAManualLock(t *testing.T) {
	t.Parallel()

	held := manuallock.Lock{
		Since:  time.Date(2026, time.October, 19, 10, 0, 0, 0, time.UTC),
		Target: manuallock.CollectionTarget(collection.Ref{Namespace: "acme", Name: "payments"}),
		Holder: "alice",
		Reason: "change window closed",
	}

	refusal := &manuallock.LockedError{Lock: held, Noun: "workspace", Name: "acme/web"}
	manual := fmt.Errorf("%w: %w", workspace.ErrLocked, refusal)

	heldUnit := held
	heldUnit.Target = manuallock.UnitTarget(collection.UnitRef{Namespace: "acme", Tool: "opentofu", Name: "web"})
	manualUnit := fmt.Errorf("%w: %w", workspace.ErrLocked, &manuallock.LockedError{
		Lock: heldUnit, Noun: "workspace", Name: "acme/web",
	})

	heldNamespace := held
	heldNamespace.Target = manuallock.NamespaceTarget("acme")
	manualNamespace := fmt.Errorf("%w: %w", workspace.ErrLocked, &manuallock.LockedError{
		Lock: heldNamespace, Noun: "workspace", Name: "acme/web",
	})

	cases := []struct {
		name    string
		lockErr error
		want    int
		info    manualInfoWant
	}{
		{
			name: "manual lock", lockErr: manual, want: http.StatusLocked,
			info: manualInfoWant{
				who:    "alice",
				remove: "/api/dustpan/v1/namespaces/acme/collections/payments/lock",
				force:  "/api/dustpan/v1/namespaces/acme/collections/payments/lock/force-unlock",
				text: "workspace acme/web is in collection acme/payments, which has a manual lock " +
					"by alice since 2026-10-19T10:00:00Z: change window closed",
			},
		},
		{
			name: "manual lock on the unit", lockErr: manualUnit, want: http.StatusLocked,
			info: manualInfoWant{
				who:    "alice",
				remove: "/api/inventory/unit-lock?unit=opentofu:acme/web",
				force:  "/api/inventory/unit-lock/force-unlock?unit=opentofu:acme/web",
				text:   "workspace acme/web has a manual lock by alice since 2026-10-19T10:00:00Z: change window closed",
			},
		},
		{
			name: "manual lock on the namespace", lockErr: manualNamespace, want: http.StatusLocked,
			info: manualInfoWant{
				who:    "alice",
				remove: "/api/dustpan/v1/namespaces/acme/lock",
				force:  "/api/dustpan/v1/namespaces/acme/lock/force-unlock",
				text: "workspace acme/web is in namespace acme, which has a manual lock " +
					"by alice since 2026-10-19T10:00:00Z: change window closed",
			},
		},
		{
			name: "lock of a tool without a lock to show", lockErr: workspace.ErrLocked, want: http.StatusLocked,
			info: manualInfoWant{who: "", text: "", remove: "", force: ""},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			units := newFakeUnits(true)
			units.lockErr = tc.lockErr
			h := newTestHandler(t, units)

			rec := do(h, "LOCK", unitPath, lockBody(lockOne, "ci"), bearer(adminToken))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}

			if tc.info.who == "" {
				return
			}

			checkManualInfo(t, rec.Body.Bytes(), tc.info)
		})
	}
}

// manualInfoWant is the lock info that a manual lock must give: the holder, the
// start of the text, and the routes that remove the lock.
type manualInfoWant struct {
	who    string
	text   string
	remove string
	force  string
}

// checkManualInfo checks the lock info of a manual lock: the holder, the text of
// the lock, plain words first and then the routes in monospace. The ID is no
// tool lock ID that `tofu force-unlock` could use.
func checkManualInfo(t *testing.T, body []byte, w manualInfoWant) {
	t.Helper()

	var info struct {
		ID   string `json:"ID"`   //nolint:tagliatelle // The OpenTofu http backend reads PascalCase keys.
		Who  string `json:"Who"`  //nolint:tagliatelle // The OpenTofu http backend reads PascalCase keys.
		Info string `json:"Info"` //nolint:tagliatelle // The OpenTofu http backend reads PascalCase keys.
	}

	if err := json.Unmarshal(body, &info); err != nil {
		t.Fatalf("lock body %q: %v", body, err)
	}

	if info.Who != w.who || !strings.HasPrefix(info.Info, w.text) {
		t.Errorf("lock info = Who %q Info %q, want Who %q and Info that starts with %q",
			info.Who, info.Info, w.who, w.text)
	}

	route := "`DELETE " + w.remove + "`"
	for field, got := range map[string]string{"ID": info.ID, "Info": info.Info} {
		if !strings.Contains(got, route) {
			t.Errorf("%s = %q, want the route %s", field, got, route)
		}
	}

	if !strings.HasPrefix(info.ID, "A manual lock blocks this run.") {
		t.Errorf("ID = %q, want plain words that say a manual lock blocks the run", info.ID)
	}

	if !strings.Contains(info.Info, "`POST "+w.force+"`") {
		t.Errorf("Info = %q, want the force-unlock route", info.Info)
	}
}

// TestLockPassesTheJobOfTheToken proves the job ID reaches the lock service
// only for a request that a job token authenticated. An access token whose
// principal names a job gets no pass.
func TestLockPassesTheJobOfTheToken(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		token string
		want  string
	}{
		{name: "job token", token: jobToken, want: "job-1"},
		{name: "access token that names a job", token: jobIDOnAccessToken, want: ""},
		{name: "access token", token: adminToken, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			units := newFakeUnits(true)
			h := newTestHandler(t, units)

			rec := do(h, "LOCK", unitPath, lockBody(lockOne, "ci"), bearer(tc.token))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
			}

			if len(units.lockJobs) != 1 || units.lockJobs[0] != tc.want {
				t.Errorf("job IDs of the lock requests = %q, want [%q]", units.lockJobs, tc.want)
			}
		})
	}
}

// TestStateWriteRefusedByAManualLock proves a POST of state that a manual lock
// refuses answers 423 with lock info that names the holder, with or without the
// lock ID of the client. The write without a lock ID is the one that `tofu
// -lock=false` sends.
func TestStateWriteRefusedByAManualLock(t *testing.T) {
	t.Parallel()

	held := manuallock.Lock{
		Since:  time.Date(2026, time.October, 19, 10, 0, 0, 0, time.UTC),
		Target: manuallock.CollectionTarget(collection.Ref{Namespace: "acme", Name: "payments"}),
		Holder: "alice",
		Reason: "change window closed",
	}
	refusal := fmt.Errorf("%w: %w", workspace.ErrLocked, &manuallock.LockedError{
		Lock: held, Noun: "workspace", Name: "acme/web",
	})

	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "without a lock ID", path: unitPath},
		{name: "with a lock ID", path: unitPath + "?ID=lock-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			units := newFakeUnits(true)
			units.writeErr = refusal
			h := newTestHandler(t, units)

			rec := do(h, http.MethodPost, tc.path, `{"serial":1,"lineage":"l"}`, bearer(adminToken))
			if rec.Code != http.StatusLocked {
				t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusLocked, rec.Body.String())
			}

			checkManualInfo(t, rec.Body.Bytes(), manualInfoWant{
				who:    "alice",
				remove: "/api/dustpan/v1/namespaces/acme/collections/payments/lock",
				force:  "/api/dustpan/v1/namespaces/acme/collections/payments/lock/force-unlock",
				text: "workspace acme/web is in collection acme/payments, which has a manual lock " +
					"by alice since 2026-10-19T10:00:00Z: change window closed",
			})
		})
	}
}

// TestFirstRequestRefusedByANamespaceLock proves a first LOCK or POST for a
// workspace that does not exist yet answers 423 with the lock info of the
// manual lock of the namespace. The service refuses it in the admit check or in
// the create, and both answer the same lock info as for a workspace that
// exists. No workspace is made.
func TestFirstRequestRefusedByANamespaceLock(t *testing.T) {
	t.Parallel()

	held := manuallock.Lock{
		Since:  time.Date(2026, time.October, 19, 10, 0, 0, 0, time.UTC),
		Target: manuallock.NamespaceTarget("acme"),
		Holder: "alice",
		Reason: "change window closed",
	}
	refusal := fmt.Errorf("%w: %w", workspace.ErrLocked, &manuallock.LockedError{
		Lock: held, Noun: "workspace", Name: "acme/web",
	})

	cases := []struct {
		name      string
		method    string
		body      string
		admitErr  error
		createErr error
	}{
		{
			name: "LOCK refused by the admit check", method: "LOCK", body: lockBody(lockOne, "ci"),
			admitErr: refusal, createErr: nil,
		},
		{
			name: "LOCK refused by the create", method: "LOCK", body: lockBody(lockOne, "ci"),
			admitErr: nil, createErr: refusal,
		},
		{
			name: "POST refused by the admit check", method: http.MethodPost, body: `{"serial":1,"lineage":"l"}`,
			admitErr: refusal, createErr: nil,
		},
		{
			name: "POST refused by the create", method: http.MethodPost, body: `{"serial":1,"lineage":"l"}`,
			admitErr: nil, createErr: refusal,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			units := newFakeUnits(false)
			units.admitErr = tc.admitErr
			units.createErr = tc.createErr
			h := newTestHandler(t, units)

			rec := do(h, tc.method, unitPath, tc.body, bearer(adminToken))
			if rec.Code != http.StatusLocked {
				t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusLocked, rec.Body.String())
			}

			if got := rec.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}

			checkManualInfo(t, rec.Body.Bytes(), manualInfoWant{
				who:    "alice",
				remove: "/api/dustpan/v1/namespaces/acme/lock",
				force:  "/api/dustpan/v1/namespaces/acme/lock/force-unlock",
				text: "workspace acme/web is in namespace acme, which has a manual lock " +
					"by alice since 2026-10-19T10:00:00Z: change window closed",
			})

			if units.exists {
				t.Errorf("a refused first %s made the workspace", tc.method)
			}
		})
	}
}
