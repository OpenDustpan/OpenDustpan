package github_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
)

// proofTestKey is the key of testProver. The key is public: it stands for the
// key of the server, in tests only.
const proofTestKey = "key of the proofs of the tests"

// testProver is a github.Prover for the tests. It keeps no state. The proof is
// an HMAC-SHA256 of the binding, so that two calls with one binding give one
// proof, and a proof opens for its binding only.
type testProver struct{}

func (testProver) Seal(_ context.Context, binding []byte) ([]byte, error) {
	return testMAC(binding), nil
}

func (testProver) Open(_ context.Context, binding, proof []byte) (bool, error) {
	return hmac.Equal(testMAC(binding), proof), nil
}

func testMAC(binding []byte) []byte {
	mac := hmac.New(sha256.New, []byte(proofTestKey))
	_, _ = mac.Write(binding) // The write of a hash does not fail.

	return mac.Sum(nil)
}

// rawRepoID is the ID of the repository that rawForge answers for.
const rawRepoID = "700001"

// testProofLine returns the trailer line that a commit of the repository with
// the ID repoID, the trailer and the base commit carries. The binding repeats
// the form of the adapter, so a change of that form fails the tests.
func testProofLine(repoID, trailer, base string) string {
	binding := "dustpan.forge.commit-proof.v1\x00" + repoID + "\x00" + trailer + "\x00" + base

	return "Dustpan-Proof: " + base64.RawURLEncoding.EncodeToString(testMAC([]byte(binding)))
}
