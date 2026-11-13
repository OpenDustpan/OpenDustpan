package triggers_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
)

// addressRefused refuses every check as the IP allowlist gate does for a
// client address outside the allowlist of the namespace.
type addressRefused struct{}

func (addressRefused) Authorize(context.Context, identity.Principal, identity.Action, identity.Object) error {
	return errors.Join(identity.ErrDenied, ipallow.ErrAddressNotAllowed)
}

func (addressRefused) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{}, errNoList
}

// TestAddressOutsideTheAllowlistAnswers403 proves that a read of a unit
// trigger answers 403 with access.ip-not-allowed for a client address outside
// the IP allowlist, as the other routes of the namespace do, and not the 404
// of a trigger that the caller cannot read. The refusal does not tell whether
// the namespace, the unit or the trigger exists.
func TestAddressOutsideTheAllowlistAnswers403(t *testing.T) {
	t.Parallel()

	r := newRig(t, addressRefused{})
	seed(r)

	for _, path := range []string{
		basePath + "/trg-seed",
		basePath + "/trg-nope",
		"/api/dustpan/v1/namespaces/absent/units/" + netID + "/triggers/trg-seed",
		basePath + "/TRG-SEED",
	} {
		rec := serve(t, r.h, request{method: http.MethodGet, path: path, token: testToken, body: "", header: nil})
		errcodetest.Check(t, rec.Code, rec.Body.Bytes(), errcodetest.Cause{
			Name: "read " + path, Err: nil, Code: errcode.AccessIPNotAllowed, Message: "forbidden", Fixed: true,
		})

		for _, named := range []string{"acme", "absent", "trg-seed", "trg-nope", netID} {
			if strings.Contains(rec.Body.String(), named) {
				t.Errorf("read %s: the refusal names %q: %s", path, named, rec.Body.String())
			}
		}
	}
}

// writingLimiter returns a limiter whose window ends between two refusals, so
// that each refusal writes one entry and the test can count them.
func writingLimiter() *denylimit.Limiter {
	var mu sync.Mutex

	now := time.Unix(1_800_000_000, 0)

	return denylimit.New(denylimit.Config{
		Window: time.Second, MaxKeys: 0, MaxFlush: 0, MaxKeysPerPrincipal: 0,
		Now: func() time.Time {
			mu.Lock()
			defer mu.Unlock()

			now = now.Add(2 * time.Second)

			return now
		},
	})
}

// TestRefusedReadWritesOneDenialEntry proves that one refused read writes one
// denial entry. An address outside the allowlist writes its entry in
// checkAddress. A policy refusal of the namespace read writes its entry in
// AuthorizeObject, and checkAddress writes none. A policy refusal of a
// namespace that does not exist writes nothing, because the lookup answers 404
// and never checks the policy on that name.
func TestRefusedReadWritesOneDenialEntry(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		authz   identity.Authorizer
		path    string
		status  int
		entries int
	}{
		{
			name: "address outside the allowlist", authz: addressRefused{},
			path: basePath + "/trg-seed", status: http.StatusForbidden, entries: 1,
		},
		{
			name: "policy refuses the namespace read", authz: newAuthorizer(),
			path: basePath + "/trg-seed", status: http.StatusNotFound, entries: 1,
		},
		{
			name: "unknown namespace under a policy that refuses everything", authz: newAuthorizer(),
			path:    "/api/dustpan/v1/namespaces/absent/units/" + netID + "/triggers/trg-seed",
			status:  http.StatusNotFound,
			entries: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := &recordingAudit{Store: nil, mu: sync.Mutex{}, entries: nil}
			r := newRigGuarded(
				t, tc.authz, slog.New(slog.DiscardHandler), nil,
				guardOptions{records: store, limiter: writingLimiter()},
			)
			seed(r)

			rec := serve(t, r.h, request{
				method: http.MethodGet, path: tc.path, token: testToken, body: "", header: nil,
			})
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}

			got := store.denied()
			if len(got) != tc.entries {
				t.Fatalf("wrote %d denial entries, want %d: %+v", len(got), tc.entries, got)
			}

			for i := range got {
				if got[i].Outcome != audit.OutcomeDenied || got[i].Action != string(identity.ActionNamespaceRead) {
					t.Errorf("entry %d = %+v, want a denial of %s", i, got[i], identity.ActionNamespaceRead)
				}
			}
		})
	}
}
