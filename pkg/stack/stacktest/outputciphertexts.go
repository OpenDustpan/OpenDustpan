package stacktest

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
)

// PendingOutputCiphertexts is the skip reason of a case of
// RunOutputCiphertexts that no adapter can run yet.
const PendingOutputCiphertexts = "no adapter implements stack.OutputCiphertexts yet; " +
	"the slice that adds the first adapter writes this case"

// OutputCiphertextsHarness is one fresh stack store for one subtest of
// RunOutputCiphertexts.
type OutputCiphertextsHarness struct {
	// Port is the port under test.
	Port stack.OutputCiphertexts
	// Save saves a checkpoint of ref with the deployment, as a completed
	// update, and returns nothing. The deployment is the JSON of the
	// checkpoint as the Pulumi CLI sends it.
	Save func(t *testing.T, ref stack.Ref, deployment []byte)
}

// OutputCiphertextsFactory returns a fresh harness for one subtest.
type OutputCiphertextsFactory func(t *testing.T) OutputCiphertextsHarness

// outputCiphertextsCase is one behavior of the port. A nil test waits for the
// first adapter.
type outputCiphertextsCase struct {
	test func(t *testing.T, h OutputCiphertextsHarness)
	name string
}

// RunOutputCiphertexts runs the conformance cases of stack.OutputCiphertexts.
func RunOutputCiphertexts(t *testing.T, newHarness OutputCiphertextsFactory) {
	t.Helper()

	for _, tc := range outputCiphertextsCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.test == nil {
				t.Skip(PendingOutputCiphertexts)
			}

			tc.test(t, newHarness(t))
		})
	}
}

// OutputCiphertextsCaseNames returns the names of the cases of
// RunOutputCiphertexts, in their order. Each call returns a new slice.
func OutputCiphertextsCaseNames() []string {
	cases := outputCiphertextsCases()
	names := make([]string, 0, len(cases))

	for _, tc := range cases {
		names = append(names, tc.name)
	}

	return names
}

// outputCiphertextsCases are the behaviors of stack.OutputCiphertexts.
func outputCiphertextsCases() []outputCiphertextsCase {
	return []outputCiphertextsCase{
		{nil, "output ciphertexts: each secret root output of the latest checkpoint"},
		{nil, "output ciphertexts: a secret inside an object or a list of an output"},
		{nil, "output ciphertexts: a plain output gives no ciphertext"},
		{nil, "output ciphertexts: a secret of another resource is not an output"},
		{nil, "output ciphertexts: a config secret is not an output"},
		{nil, "output ciphertexts: an output of an older checkpoint is not an output after an update"},
		{nil, "output ciphertexts: a checkpoint that the journal built"},
		{nil, "output ciphertexts: a stack with no state gives ErrNoState"},
		{nil, "output ciphertexts: an unknown stack gives ErrNotFound"},
		{nil, "output ciphertexts: a checkpoint that does not match its record gives an error"},
		{nil, "output ciphertexts: a stack of another namespace gives ErrNotFound"},
	}
}
