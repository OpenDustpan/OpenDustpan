package outputsharetest

import (
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// The tools of the cases. Each case runs its unit of a tool in the one
// harness that the case gets.
const (
	toolOpenTofu = "opentofu"
	toolPulumi   = "pulumi"
)

// reader is the subject of the principal that reads in the cases.
const reader = "reader"

// readerPrincipal returns the principal of a person who reads with an access
// token.
func readerPrincipal() identity.Principal {
	return identity.Principal{
		Subject: reader, DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: identity.KindPerson,
		Credential: identity.CredentialAccessToken, JobID: "",
	}
}

// eachTool runs fn once for each tool, with a unit of that tool in the
// namespace acme. The unit has no state.
func eachTool(t *testing.T, h Harness, fn func(t *testing.T, tool string, unit outputshare.Placement)) {
	t.Helper()

	h.Namespace(t, acme.String())

	for _, tool := range []string{toolOpenTofu, toolPulumi} {
		unit := h.Unit(t, tool, acme, "prod", "")

		t.Run(tool, func(t *testing.T) {
			t.Helper()

			fn(t, tool, unit)
		})
	}
}

// grantReader gives the reader each action on the unit.
func grantReader(t *testing.T, h Harness, unit outputshare.Placement, actions ...identity.Action) {
	t.Helper()

	for _, action := range actions {
		h.Grant(t, reader, action, unit.Unit)
	}
}

// readOutputs makes the read of the latest state of unit by the reader.
func readOutputs(
	t *testing.T, h Harness, unit outputshare.Placement, latest string, secrets bool,
) (outputshare.Decision, error) {
	t.Helper()

	return h.Checker.CheckRead(t.Context(), outputshare.ReadRequest{
		Caller: readerPrincipal(), Producer: unit, Attachment: outputshare.AttachmentRef{
			Attachment: "", Profile: "", Block: "", Revision: 0,
		}, Latest: latest, Secrets: secrets,
	})
}

// wantAllowed fails the test unless the read is allowed as a read by a
// principal. It returns the decision.
func wantAllowed(t *testing.T, dec outputshare.Decision, err error) outputshare.Decision {
	t.Helper()

	if err != nil {
		t.Fatalf("CheckRead = %v, want an allowed read", err)
	}

	if dec.Kind != outputshare.ReadByPrincipal || dec.Rule != "" {
		t.Fatalf("Decision = %+v, want a read by a principal with no rule", dec)
	}

	return dec
}

// wantDenied fails the test unless err is a denial by the check, that
// unwraps to the sentinel only and never to the other sentinels.
func wantDenied(t *testing.T, err error, check string, sentinel error) {
	t.Helper()

	var denied *outputshare.DeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("CheckRead = %v, want a *DeniedError of the check %q", err, check)
	}

	if denied.Check != check {
		t.Errorf("check = %q, want %q", denied.Check, check)
	}

	for _, other := range []error{identity.ErrDenied, outputshare.ErrUnknownProducer, outputshare.ErrClientEncrypted} {
		if got, want := errors.Is(err, other), errors.Is(other, sentinel); got != want {
			t.Errorf("errors.Is(err, %v) = %v, want %v", other, got, want)
		}
	}
}

// principalCases are the cases of the checker for a read by a principal.
func principalCases() []suiteCase {
	return []suiteCase{
		{testPrincipalReads, "checker: a principal with unit:read-outputs reads the plain outputs"},
		{testPrincipalNoGrant, "checker: a principal without unit:read-outputs is denied by the grant check"},
		{testPrincipalReadIsNotEnough, "checker: a principal with unit:read but no unit:read-outputs is denied"},
		{testPrincipalSecrets, "checker: a secret output needs unit:read-secrets"},
		{testPrincipalOrder, "checker: of several failed checks the denial names the first in the order of the checks"},
		{testPrincipalInvalidPlacement, "checker: an invalid producer placement is refused"},
		{testPrincipalNotAUnit, "checker: a producer reference that is not a unit is refused with ErrInvalid"},
		{
			testPrincipalUnknownProducer,
			"checker: an unknown producer for a principal answers ErrUnknownProducer and not identity.ErrDenied",
		},
		{
			testPrincipalAudit,
			"checker: a denial of a principal writes one audit entry in the namespace of the producer",
		},
		{testPrincipalJobToken, "checker: a read by a job token is denied until the job path is served"},
	}
}

// testPrincipalReads proves that the grant of unit:read-outputs allows the
// read of the latest state of a unit of each tool.
func testPrincipalReads(t *testing.T, h Harness) {
	t.Helper()

	eachTool(t, h, func(t *testing.T, _ string, unit outputshare.Placement) {
		t.Helper()

		id := h.SaveState(t, unit, false)
		grantReader(t, h, unit, identity.ActionUnitReadOutputs)

		dec, err := readOutputs(t, h, unit, id, false)
		if got := wantAllowed(t, dec, err); got.Secrets {
			t.Error("Decision.Secrets = true, want false without unit:read-secrets")
		}
	})
}

// testPrincipalNoGrant proves that a principal with no grant is denied by the
// grant check, with identity.ErrDenied only.
func testPrincipalNoGrant(t *testing.T, h Harness) {
	t.Helper()

	eachTool(t, h, func(t *testing.T, _ string, unit outputshare.Placement) {
		t.Helper()

		id := h.SaveState(t, unit, false)

		_, err := readOutputs(t, h, unit, id, false)
		wantDenied(t, err, outputshare.CheckGrant, identity.ErrDenied)
	})
}

// testPrincipalReadIsNotEnough proves that unit:read does not imply
// unit:read-outputs: the check reads grants of unit:read-outputs only.
func testPrincipalReadIsNotEnough(t *testing.T, h Harness) {
	t.Helper()

	eachTool(t, h, func(t *testing.T, _ string, unit outputshare.Placement) {
		t.Helper()

		id := h.SaveState(t, unit, false)
		grantReader(t, h, unit, identity.ActionUnitRead, identity.ActionUnitReadSecrets)

		_, err := readOutputs(t, h, unit, id, false)
		wantDenied(t, err, outputshare.CheckGrant, identity.ErrDenied)
	})
}

// testPrincipalSecrets proves that a request that needs a secret output is
// denied without unit:read-secrets, and that a request that does not need one
// gets a decision that leaves out the secret outputs.
func testPrincipalSecrets(t *testing.T, h Harness) {
	t.Helper()

	eachTool(t, h, func(t *testing.T, _ string, unit outputshare.Placement) {
		t.Helper()

		id := h.SaveState(t, unit, false)
		grantReader(t, h, unit, identity.ActionUnitReadOutputs)

		_, err := readOutputs(t, h, unit, id, true)
		wantDenied(t, err, outputshare.CheckSecrets, identity.ErrDenied)

		dec, err := readOutputs(t, h, unit, id, false)
		if got := wantAllowed(t, dec, err); got.Secrets {
			t.Error("Decision.Secrets = true, want false without unit:read-secrets")
		}

		grantReader(t, h, unit, identity.ActionUnitReadSecrets)

		dec, err = readOutputs(t, h, unit, id, true)
		if got := wantAllowed(t, dec, err); !got.Secrets {
			t.Error("Decision.Secrets = false, want true with unit:read-secrets")
		}
	})
}

// testPrincipalOrder proves that a read that fails the grant check and the
// latest-state check is denied by the grant check, the first of the two.
func testPrincipalOrder(t *testing.T, h Harness) {
	t.Helper()

	eachTool(t, h, func(t *testing.T, _ string, unit outputshare.Placement) {
		t.Helper()

		h.SaveState(t, unit, false)

		_, err := readOutputs(t, h, unit, "not-the-latest-state", false)
		wantDenied(t, err, outputshare.CheckGrant, identity.ErrDenied)

		grantReader(t, h, unit, identity.ActionUnitReadOutputs)

		_, err = readOutputs(t, h, unit, "not-the-latest-state", false)
		wantDenied(t, err, outputshare.CheckLatest, identity.ErrDenied)
	})
}

// testPrincipalInvalidPlacement proves that a producer that has a stable ID
// and fails Placement.Validate is refused with ErrInvalid.
func testPrincipalInvalidPlacement(t *testing.T, h Harness) {
	t.Helper()

	eachTool(t, h, func(t *testing.T, _ string, unit outputshare.Placement) {
		t.Helper()

		bad := outputshare.Placement{
			Unit:       unit.Unit,
			Collection: identity.CollectionObject("globex", "other").WithCollectionID("collection-1"),
		}

		_, err := readOutputs(t, h, bad, "1", false)
		if !errors.Is(err, outputshare.ErrInvalid) {
			t.Fatalf("CheckRead = %v, want ErrInvalid for a collection of another namespace", err)
		}
	})
}

// testPrincipalNotAUnit proves that a producer that is not a unit is refused
// with ErrInvalid.
func testPrincipalNotAUnit(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, acme.String())

	objects := []identity.Object{
		identity.NamespaceObject(acme), identity.CollectionObject(acme, "apps"), identity.ServerObject(), {},
	}

	for i := range objects {
		object := objects[i]

		_, err := readOutputs(t, h, outputshare.Placement{Unit: object, Collection: identity.Object{}}, "1", false)
		if !errors.Is(err, outputshare.ErrInvalid) {
			t.Errorf("CheckRead(%q) = %v, want ErrInvalid", object, err)
		}
	}
}

// testPrincipalUnknownProducer proves that a producer that does not exist
// answers ErrUnknownProducer only, also for a principal that holds the
// grants, and not identity.ErrDenied.
func testPrincipalUnknownProducer(t *testing.T, h Harness) {
	t.Helper()

	eachTool(t, h, func(t *testing.T, tool string, _ outputshare.Placement) {
		t.Helper()

		name := "ghost"
		if tool == toolPulumi {
			name = "web/ghost"
		}

		unit, err := identity.UnitObject(tool, acme, name)
		if err != nil {
			t.Fatalf("unit object: %v", err)
		}

		missing := outputshare.Placement{Unit: unit, Collection: identity.Object{}}
		grantReader(t, h, missing, identity.ActionUnitReadOutputs, identity.ActionUnitRead)

		_, err = readOutputs(t, h, missing, "1", false)
		wantDenied(t, err, outputshare.CheckProducer, outputshare.ErrUnknownProducer)
	})
}

// testPrincipalAudit proves that a denial writes one audit entry in the
// namespace of the producer, and that an allowed read writes none.
func testPrincipalAudit(t *testing.T, h Harness) {
	t.Helper()

	if h.Audited == nil {
		t.Skip("the adapter gives no view of the audit log")
	}

	eachTool(t, h, func(t *testing.T, _ string, unit outputshare.Placement) {
		t.Helper()

		id := h.SaveState(t, unit, false)
		before := len(h.Audited(t, acme.String()))

		if _, err := readOutputs(t, h, unit, id, false); err == nil {
			t.Fatal("CheckRead without a grant = nil, want a denial")
		}

		got := h.Audited(t, acme.String())
		if len(got) != before+1 || got[len(got)-1] != string(identity.ActionUnitReadOutputs) {
			t.Fatalf("audit actions = %v, want one more entry of %s", got, identity.ActionUnitReadOutputs)
		}

		grantReader(t, h, unit, identity.ActionUnitReadOutputs)

		dec, err := readOutputs(t, h, unit, id, false)
		wantAllowed(t, dec, err)

		if after := h.Audited(t, acme.String()); !slices.Equal(after, got) {
			t.Errorf("an allowed read changed the audit log: %v, want %v", after, got)
		}
	})
}

// testPrincipalJobToken proves that the checker denies a read by a job token
// with identity.ErrDenied while the job path has no adapter: it fails closed.
func testPrincipalJobToken(t *testing.T, h Harness) {
	t.Helper()

	eachTool(t, h, func(t *testing.T, _ string, unit outputshare.Placement) {
		t.Helper()

		id := h.SaveState(t, unit, false)
		grantReader(t, h, unit, identity.ActionUnitReadOutputs)

		caller := readerPrincipal()
		caller.Credential = identity.CredentialJobToken
		caller.JobID = "job-1"

		_, err := h.Checker.CheckRead(t.Context(), outputshare.ReadRequest{
			Caller: caller, Producer: unit, Attachment: outputshare.AttachmentRef{
				Attachment: "", Profile: "", Block: "", Revision: 0,
			}, Latest: id, Secrets: false,
		})
		wantDenied(t, err, outputshare.CheckCaller, identity.ErrDenied)
	})
}
