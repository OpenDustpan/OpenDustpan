package github_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/pkg/forge"
)

// proofTestKey is the key of testProver. The key is public: it stands for the
// key of the server, in tests only.
const proofTestKey = "key of the proofs of the tests"

// testProver is a github.Prover for the tests. It keeps no state. The proof is
// an HMAC-SHA256 of the binding, so that two calls with one binding give one
// proof, and a proof opens for its binding only.
type testProver struct{}

func (testProver) Ready() bool { return true }

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

// rawBranch is the branch of oneCommit, and rawChanges is the digest of its
// changes, from an independent Python run of the form of forge.ChangesDigest.
const (
	rawBranch  = "main"
	rawChanges = "sha256:fba8bd48f831c77a61fa10a332619d49c19e1541d2f36fe3fb2e96c4546e4905"
)

// rawDelivery is the delivery of a commit with the trailer and the base
// commit, on rawBranch, with the changes of oneCommit.
func rawDelivery(trailer, base string) forge.Delivery {
	return forge.Delivery{Trailer: trailer, Base: base, Branch: rawBranch, Changes: rawChanges}
}

// testProofLine returns the trailer line that a commit of the repository with
// the ID repoID, the trailer and the base commit carries, on rawBranch with
// the changes of oneCommit.
func testProofLine(repoID, trailer, base string) string {
	return testProofLineOf(repoID, rawDelivery(trailer, base))
}

// testProofLineOf returns the trailer line of the proof of d in the repository
// with the ID repoID. The binding repeats the form of the adapter, so a change
// of that form fails the tests.
func testProofLineOf(repoID string, d forge.Delivery) string {
	var binding strings.Builder

	for _, p := range []string{"dustpan.forge.commit-proof.v2", repoID, d.Trailer, d.Base, d.Branch, d.Changes} {
		binding.WriteString(strconv.Itoa(len(p)) + ":" + p)
	}

	return "Dustpan-Proof: " + base64.RawURLEncoding.EncodeToString(testMAC([]byte(binding.String())))
}
