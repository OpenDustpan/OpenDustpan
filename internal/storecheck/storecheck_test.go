package storecheck_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/storecheck"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
)

// errAdapter is an adapter error that holds an endpoint and a credential.
var errAdapter = errors.New(
	"request failed: https://s3.internal.example:9000/private-bucket?X-Amz-Signature=abc AKIAEXAMPLE",
)

var errObjectLock = errors.New("access denied: object lock")

// hostilePrefixes are prefixes that a check must never write under or
// delete. Each layer of the containment gets each of them directly, as if
// every other layer had failed. OWN stands for the prefix of the check that
// the layer scopes, and FOREIGN for the prefix of a real check of another
// start: see concrete and withForeign.
func hostilePrefixes() []string {
	return []string{
		"",
		"/",
		"namespaces/",
		"backups/",
		"store-checks/",
		"store-checks/../namespaces/",
		"store-checks/other-check/",
		"store-checks/OWN/../../backups/",
		"store-checks/FOREIGN/",
	}
}

// withForeign starts and runs a check, then starts a second check, so that the
// second is a real check of another start. It returns the rig, the ended
// check and the running check. The hostile prefix "store-checks/FOREIGN/"
// stands for the prefix of the running check.
func withForeign(t *testing.T) foreignChecks {
	t.Helper()

	r := newRig(t, nil)

	ended := r.start(t, "blobs")
	r.runOne(t)

	*r.clock = r.clock.Add(storecheck.Cooldown + time.Minute)

	running := r.start(t, "blobs")

	return foreignChecks{rig: r, ended: ended, running: running}
}

// foreignChecks is a rig with an ended check and a running check.
type foreignChecks struct {
	rig     *rig
	ended   storecheck.ID
	running storecheck.ID
}

// concrete replaces the OWN and FOREIGN placeholders of a hostile prefix
// with real check IDs: own is the check that the layer scopes, foreign is the
// running check of another start.
func concrete(prefix string, own, foreign storecheck.ID) string {
	prefix = strings.ReplaceAll(prefix, "OWN", own.String())

	return strings.ReplaceAll(prefix, "FOREIGN", foreign.String())
}

func TestServiceRefusesAHostilePrefix(t *testing.T) {
	t.Parallel()

	for _, prefix := range hostilePrefixes() {
		t.Run(prefix, func(t *testing.T) {
			t.Parallel()

			f := withForeign(t)
			r, a, b := f.rig, f.ended, f.running
			before := r.blobs.callCount()

			view, err := r.svc.Scope(t.Context(), storecheck.TargetBlobs, a, concrete(prefix, a, b))
			if !errors.Is(err, storecheck.ErrPrefixRefused) || view != nil {
				t.Fatalf("Scope(%q) = %v, %v; want nil and ErrPrefixRefused", prefix, view, err)
			}

			if got := r.blobs.callCount(); got != before {
				t.Fatalf("the store got %d calls for a refused prefix", got-before)
			}
		})
	}
}

func TestServiceAllowsOnlyThePrefixOfTheCheck(t *testing.T) {
	t.Parallel()

	t.Run("the prefix of the check itself", func(t *testing.T) {
		t.Parallel()

		f := withForeign(t)
		r, a, _ := f.rig, f.ended, f.running

		if _, err := r.svc.Scope(t.Context(), storecheck.TargetBlobs, a, a.Prefix()); err != nil {
			t.Fatalf("Scope(own prefix): %v", err)
		}
	})

	t.Run("an ID that the service did not make", func(t *testing.T) {
		t.Parallel()

		r := newRig(t, nil)

		for _, id := range []storecheck.ID{"", "chk-", "chk-../x", "x", "chk-ABC", "chk-a/b"} {
			_, err := r.svc.Scope(t.Context(), storecheck.TargetBlobs, id, "store-checks/"+id.String()+"/")
			if !errors.Is(err, storecheck.ErrPrefixRefused) {
				t.Errorf("Scope(id %q) err = %v, want ErrPrefixRefused", id, err)
			}
		}
	})
}

func TestScopedViewStopsAWriteOutsideThePrefix(t *testing.T) {
	t.Parallel()

	for _, prefix := range hostilePrefixes() {
		t.Run(prefix, func(t *testing.T) {
			t.Parallel()

			f := withForeign(t)
			r, a, b := f.rig, f.ended, f.running

			// The view of the prefix of a, taken by the allowlist. The test
			// sends the hostile key straight to it, as if the allowlist had
			// failed on that key.
			view, err := r.svc.Scope(t.Context(), storecheck.TargetBlobs, a, a.Prefix())
			if err != nil {
				t.Fatalf("Scope: %v", err)
			}

			hostile := concrete(prefix, a, b)
			before := r.blobs.callCount()

			checkViewRefuses(t, view, hostile)

			if got := r.blobs.callCount(); got != before {
				t.Fatalf("the store got %d calls for keys outside the prefix", got-before)
			}
		})
	}
}

// checkViewRefuses sends the hostile prefix, as a key and as a listing, to
// every method of the view.
func checkViewRefuses(t *testing.T, view blob.Store, hostile string) {
	t.Helper()

	key := blob.Key(hostile + "x")

	if err := view.Put(t.Context(), key, strings.NewReader("x")); !errors.Is(err, blob.ErrOutOfScope) {
		t.Errorf("Put err = %v, want ErrOutOfScope", err)
	}

	if _, err := view.Get(t.Context(), key); !errors.Is(err, blob.ErrOutOfScope) {
		t.Errorf("Get err = %v, want ErrOutOfScope", err)
	}

	if err := view.Delete(t.Context(), key); !errors.Is(err, blob.ErrOutOfScope) {
		t.Errorf("Delete err = %v, want ErrOutOfScope", err)
	}

	// A hostile Prefix: the scope check of the view refuses it.
	req := blob.PageRequest{Prefix: hostile, After: "", Limit: 10}

	if page, err := view.ListPage(t.Context(), req); !errors.Is(err, blob.ErrOutOfScope) || len(page.Objects) != 0 {
		t.Errorf("ListPage(prefix %q) = %v, %v; want no objects and ErrOutOfScope", hostile, page, err)
	}
}

// checkViewRefusesAfter sends the hostile key as the After of a page request
// with a valid Prefix, the prefix of the view. The check of the request and the
// scope check of the view each refuse it: ErrInvalidKey from the first,
// ErrOutOfScope from the second.
func checkViewRefusesAfter(t *testing.T, view blob.Store, prefix, hostile string) {
	t.Helper()

	req := blob.PageRequest{Prefix: prefix, After: blob.Key(hostile + "x"), Limit: 10}

	page, err := view.ListPage(t.Context(), req)
	if !errors.Is(err, blob.ErrOutOfScope) && !errors.Is(err, blob.ErrInvalidKey) {
		t.Errorf("ListPage(after %q) err = %v, want ErrOutOfScope or ErrInvalidKey", req.After, err)
	}

	if len(page.Objects) != 0 || page.Next != "" {
		t.Errorf("ListPage(after %q) = %v, want an empty page", req.After, page)
	}
}

func TestScopedViewStopsAHostileAfterKey(t *testing.T) {
	t.Parallel()

	for _, prefix := range hostilePrefixes() {
		t.Run(prefix, func(t *testing.T) {
			t.Parallel()

			f := withForeign(t)
			r, a, b := f.rig, f.ended, f.running

			view, err := r.svc.Scope(t.Context(), storecheck.TargetBlobs, a, a.Prefix())
			if err != nil {
				t.Fatalf("Scope: %v", err)
			}

			r.blobs.seed(a.Prefix()+"left", b.Prefix()+"x")

			before := r.blobs.callCount()

			checkViewRefusesAfter(t, view, a.Prefix(), concrete(prefix, a, b))

			if got := r.blobs.callCount(); got != before {
				t.Fatalf("the store got %d calls for an After key outside the prefix", got-before)
			}
		})
	}
}

func TestPrefixReaperRefusesAHostilePrefix(t *testing.T) {
	t.Parallel()

	for _, prefix := range hostilePrefixes() {
		t.Run(prefix, func(t *testing.T) {
			t.Parallel()

			f := withForeign(t)
			r, a, b := f.rig, f.ended, f.running
			seeded := []string{
				"namespaces/ns-x/data", "backups/bkp-x/manifest.json", "store-checks/other-check/x",
				b.Prefix() + "x", a.Prefix() + "left",
			}
			r.blobs.seed(seeded...)
			before := r.blobs.callCount()

			_, err := r.svc.ReapPrefix(t.Context(), storecheck.TargetBlobs, a, concrete(prefix, a, b))
			if !errors.Is(err, storecheck.ErrPrefixRefused) {
				t.Fatalf("ReapPrefix(%q) err = %v, want ErrPrefixRefused", prefix, err)
			}

			if got := r.blobs.callCount(); got != before {
				t.Fatalf("the store got %d calls for a refused prefix", got-before)
			}

			if got := len(r.blobs.keys()); got != len(seeded) {
				t.Fatalf("store holds %d objects after the refusal, want %d", got, len(seeded))
			}
		})
	}
}

func TestPrefixReaperDeletesOnlyThePrefixOfAnEndedCheck(t *testing.T) {
	t.Parallel()

	t.Run("the own prefix of an ended check", func(t *testing.T) {
		t.Parallel()

		f := withForeign(t)
		r, a, b := f.rig, f.ended, f.running
		r.blobs.seed("namespaces/ns-x/data", b.Prefix()+"x", a.Prefix()+"left", a.Prefix()+"deep/er")

		n, err := r.svc.ReapPrefix(t.Context(), storecheck.TargetBlobs, a, a.Prefix())
		if err != nil || n != 0 {
			t.Fatalf("ReapPrefix = %d, %v", n, err)
		}

		want := []string{"namespaces/ns-x/data", b.Prefix() + "x"}
		if got := r.blobs.keys(); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("store holds %v, want %v", got, want)
		}
	})

	t.Run("a running check keeps its objects", func(t *testing.T) {
		t.Parallel()

		f := withForeign(t)
		r, _, b := f.rig, f.ended, f.running
		r.blobs.seed(b.Prefix() + "x")

		_, err := r.svc.ReapPrefix(t.Context(), storecheck.TargetBlobs, b, b.Prefix())
		if !errors.Is(err, storecheck.ErrRunning) {
			t.Fatalf("ReapPrefix of a running check err = %v, want ErrRunning", err)
		}

		if len(r.blobs.keys()) != 1 {
			t.Fatal("the reaper deleted an object of a running check")
		}
	})

	t.Run("an ID with no row is not ours to delete", func(t *testing.T) {
		t.Parallel()

		r := newRig(t, nil)
		stranger := storecheck.ID("chk-strangerstranger")
		r.blobs.seed(stranger.Prefix() + "x")

		_, err := r.svc.ReapPrefix(t.Context(), storecheck.TargetBlobs, stranger, stranger.Prefix())
		if !errors.Is(err, storecheck.ErrNotFound) {
			t.Fatalf("ReapPrefix err = %v, want ErrNotFound", err)
		}

		if len(r.blobs.keys()) != 1 {
			t.Fatal("the reaper deleted the objects of a check that is not in the table")
		}
	})
}

func TestRefusalWritesAnAuditEntry(t *testing.T) {
	t.Parallel()

	f := withForeign(t)
	r, a, b := f.rig, f.ended, f.running
	before := len(r.audit.all())

	_, err := r.svc.Scope(t.Context(), storecheck.TargetBlobs, a, b.Prefix())
	if !errors.Is(err, storecheck.ErrPrefixRefused) {
		t.Fatalf("Scope err = %v", err)
	}

	entries := r.audit.all()
	if len(entries) != before+1 {
		t.Fatalf("audit has %d entries, want %d", len(entries), before+1)
	}

	got := entries[len(entries)-1]
	if got.Outcome != audit.OutcomeDenied || got.Kind != audit.KindSecurity ||
		got.Action != "installation:store-check" || got.Object != "store-check "+a.String() {
		t.Fatalf("entry = %+v", got)
	}

	if strings.Contains(got.Object+got.Reason, b.String()) {
		t.Fatal("the entry holds the refused prefix")
	}
}

func TestReportHoldsNoEndpointFromAnAdapterError(t *testing.T) {
	t.Parallel()

	r := newRig(t, nil)
	r.blobs.getErr = errAdapter

	id := r.start(t, "blobs")
	r.runOne(t)

	rec, report := r.report(t, id)
	if report == nil || report.Passed {
		t.Fatalf("report = %+v; a store that fails every get must not pass", report)
	}

	var text strings.Builder

	text.Write(rec.Report)
	text.WriteString(rec.Error)

	for _, e := range r.audit.all() {
		text.WriteString(e.Object + e.Reason + e.Subject + audit.EncodeChanges(e.Changes))
	}

	for _, leak := range []string{"s3.internal.example", "private-bucket", "AKIAEXAMPLE", "X-Amz"} {
		if strings.Contains(text.String(), leak) {
			t.Fatalf("the report, the row or an audit entry holds %q", leak)
		}
	}
}

func TestReportSaysWhenTheStoreRefusesThePrefixDelete(t *testing.T) {
	t.Parallel()

	r := newRig(t, nil)
	r.blobs.deleteErr = errObjectLock

	id := r.start(t, "blobs")
	r.runOne(t)

	_, report := r.report(t, id)
	if report == nil {
		t.Fatal("the check has no report")
	}

	if report.PrefixDeleted || report.Passed {
		t.Fatalf("PrefixDeleted = %v, Passed = %v; want false for both", report.PrefixDeleted, report.Passed)
	}

	last := report.Cases[len(report.Cases)-1]
	if last.Passed || !strings.Contains(last.Message, "did not delete") {
		t.Fatalf("delete case = %+v", last)
	}
}
