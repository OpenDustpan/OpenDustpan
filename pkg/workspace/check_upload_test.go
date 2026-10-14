package workspace_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// capabilityCheck is one of the two read-only capability checks.
type capabilityCheck func(svc *workspace.Service, id workspace.StateVersionID, tok token.Token) error

// checker binds a check to its test and fixture.
type checker struct {
	t     *testing.T
	f     fixture
	check capabilityCheck
}

// expect runs check and proves two things: it returns want (nil, or a
// sentinel that errors.Is matches), and the version row reads back identical
// before and after, so the check wrote nothing.
func (c checker) expect(label string, id workspace.StateVersionID, tok token.Token, want error) {
	t, f, check := c.t, c.f, c.check

	t.Helper()

	before, err := f.store.GetStateVersion(t.Context(), id)
	if err != nil && !errors.Is(err, workspace.ErrStateVersionNotFound) {
		t.Fatalf("%s: read version before the check: %v", label, err)
	}

	got := check(f.svc, id, tok)

	after, err := f.store.GetStateVersion(t.Context(), id)
	if err != nil && !errors.Is(err, workspace.ErrStateVersionNotFound) {
		t.Fatalf("%s: read version after the check: %v", label, err)
	}

	if !reflect.DeepEqual(before, after) {
		t.Fatalf("%s: version row changed by the check: before %+v, after %+v", label, before, after)
	}

	if want == nil && got != nil || want != nil && !errors.Is(got, want) {
		t.Errorf("%s: err = %v, want %v", label, got, want)
	}
}

// beginVersion locks nothing new: it begins a pending state version.
func beginVersion(t *testing.T, f fixture, serial int64) workspace.PendingState {
	t.Helper()

	pending, err := f.svc.BeginState(
		t.Context(),
		f.ws.ID,
		"alice",
		newState("l1", serial, "{\"v\":1}"),
		authority(forcedOnly(), nil),
	)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	return pending
}

// runCapabilityChecks drives the table of ADR 45 for one check.
// finalizedExpired is what the check answers for a finalized version whose
// capability window has passed.
func runCapabilityChecks(t *testing.T, check capabilityCheck, finalizedExpired error) {
	t.Helper()

	f := newFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	c := checker{t: t, f: f, check: check}
	pending := beginVersion(t, f, 1)
	id := pending.Version.ID

	other, err := token.New()
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	c.expect("valid pending capability", id, pending.Upload, nil)
	c.expect("wrong capability", id, other, workspace.ErrUploadDenied)
	c.expect("unknown version", "sv-missing", pending.Upload, workspace.ErrStateVersionNotFound)

	body := strings.NewReader("{\"v\":1}")
	if _, uploadErr := f.svc.Upload(ctx, id, pending.Upload, body); uploadErr != nil {
		t.Fatalf("upload: %v", uploadErr)
	}

	c.expect("finalizing capability again", id, pending.Upload, nil)
	c.expect("other capability after finalize", id, other, workspace.ErrUploadDenied)

	late := beginVersion(t, f, 2)

	*f.now = f.now.Add(workspace.UploadWindow)

	c.expect("expired capability", late.Version.ID, late.Upload, workspace.ErrUploadDenied)

	if finalizedExpired == nil {
		c.expect("expired capability on a finalized version", id, pending.Upload, nil)

		return
	}

	c.expect("expired capability on a finalized version", id, pending.Upload, finalizedExpired)
}

// TestCheckUpload proves the capability check reports what Upload enforces
// without reading any body and without writing anything.
func TestCheckUpload(t *testing.T) {
	t.Parallel()

	runCapabilityChecks(t, func(svc *workspace.Service, id workspace.StateVersionID, tok token.Token) error {
		return svc.CheckUpload(t.Context(), id, tok)
	}, nil)
}

// TestCheckJSONRenderingUpload does the same for the JSON rendering upload,
// where an expired capability fails even on a finalized version.
func TestCheckJSONRenderingUpload(t *testing.T) {
	t.Parallel()

	runCapabilityChecks(t, func(svc *workspace.Service, id workspace.StateVersionID, tok token.Token) error {
		return svc.CheckJSONRenderingUpload(t.Context(), id, tok)
	}, workspace.ErrUploadDenied)
}
