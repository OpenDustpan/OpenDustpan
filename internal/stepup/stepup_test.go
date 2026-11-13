package stepup_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/stepup"
)

// TestErrorsAreDistinct checks that no sentinel matches another, because an
// adapter maps each one to its own error code.
func TestErrorsAreDistinct(t *testing.T) {
	t.Parallel()

	all := []error{
		stepup.ErrNotFound, stepup.ErrExpired, stepup.ErrUsed, stepup.ErrMismatch,
		stepup.ErrBadAnswer, stepup.ErrFactorRefused, stepup.ErrCLIStepUpOff, stepup.ErrLocked,
		stepup.ErrDisabled, stepup.ErrNotPerson, stepup.ErrInvalid, stepup.ErrLimit,
		stepup.ErrChallengeLimit, stepup.ErrConflict, stepup.ErrCounterRegressed,
	}

	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Errorf("%v matches %v", a, b)
			}
		}
	}
}

// TestServiceRules names the rules of the step-up service that no store can
// check. Each case skips until the service exists, so that the names are the
// contract that the service meets.
func TestServiceRules(t *testing.T) {
	t.Parallel()

	rules := []string{
		"a TOTP step-up cannot add a step-up key",
		"the service sets Challenge.CreatedBy and the adminID of EnrollFirstKey from the authenticated principal of the request, never from a request field",
		"a refusal of AddKey or EnrollFirstKey is recorded once by the service as a failure entry, and the store appends nothing",
		"a TOTP step-up cannot revoke a step-up key",
		"a console sign-in with a password only or a recovery code cannot add a step-up key",
		"the console path counts at the level key only after a WebAuthn assertion",
		"a replayed TOTP step answers ErrBadAnswer and counts as a failure",
		"a challenge from a personal token that the token route made fails with ErrNotPerson",
		"a challenge from a service, job or trust token fails with ErrNotPerson",
		"a window challenge with an excluded action fails with ErrInvalid",
		"a window credential on an excluded action fails with ErrMismatch",
		"the signed text is built from the record, never from the client",
		"a value with a CR, an LF or a control character fails with ErrInvalid at the challenge create",
		"a counter regression answers ErrBadAnswer and appends a security entry",
		"the TOTP secret is sealed before it reaches the store",
		"a runner protocol, tool-compatible or MCP request with the header gets 400",
		"a job spec with an stp- value gets 422",
		"a TOTP answer at the level key fails with ErrFactorRefused",
		"a CLI answer with cliStepUp off fails with ErrCLIStepUpOff",
		"a disabled principal gets no challenge and its credentials stop working",
		"a fresh authentication at the identity provider counts at the level any only",
		"a fresh authentication at the identity provider alone cannot add a step-up key",
		"an admin enrollment for a person who has a key fails",
		"an admin cannot enroll a key for themselves",
		"an admin enrollment without a fresh authentication at the level key fails",
		"an admin enrollment appends a security entry and sends an email to the person",
		"a key-add proof for another person or another key fails with ErrBadAnswer",
		"a poll by another token of the same person gets ErrNotFound",
		"a window credential on each one-write-only write fails with ErrMismatch",
		"a key add, a key revoke, a TOTP enrollment, a TOTP reset and an admin reset each send an email to the person",
		// The audit of refusals: exactly one layer records each one.
		"a use refused with ErrNotFound, ErrExpired or ErrUsed appends one failure entry",
		"a recorded failure refused with ErrNotFound, ErrExpired or ErrUsed appends one failure entry",
		"a use refused with ErrMismatch or ErrFactorRefused appends no second entry",
		"a refusal whose failure entry cannot be appended still refuses the write",
		// The claims of other credentials. The tests of the server and of the
		// issuer check the paths that exist today: a job token and an issued
		// token get no fresh authentication and no claim from the header.
		"a step-up challenge from a job token fails with ErrNotPerson and gives the job no step-up claim",
		"a step-up credential with a job token gives the job token no step-up and no fresh-authentication claim",
		"an issued token never holds a claim from a step-up credential of the request that opened its profile",
	}

	for _, rule := range rules {
		t.Run(rule, func(t *testing.T) {
			t.Parallel()
			t.Skip("no service implements the step-up yet")
		})
	}
}
