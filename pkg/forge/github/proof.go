package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/pkg/forge"
)

// Prover seals and opens the proof of a commit that the adapter makes. The
// proof binds the trailer, the base commit, the branch, the digest of the
// changes and the repository to a key that
// only the server holds, so that a person with push access, who knows the job
// but not the key, cannot make a commit that FindCommit counts. The adapter
// holds no key: the Prover keeps it.
type Prover interface {
	// Ready reports whether the Prover has the key that it needs. It makes no
	// call, because Capabilities uses it. A Prover that is not ready fails each
	// Seal and Open with an error: it never says that a proof is bad because it
	// has no key.
	Ready() bool
	// Seal returns the proof for binding. Two calls with the same binding may
	// return different proofs; both open.
	Seal(ctx context.Context, binding []byte) ([]byte, error)
	// Open reports whether proof is a proof that Seal made for binding. A
	// proof that does not open, for any reason, is false and a nil error. An
	// error means that the Prover cannot decide, for example when it has no
	// key or when its key store does not answer.
	Open(ctx context.Context, binding, proof []byte) (bool, error)
}

// proofKey is the key of the trailer line that holds the proof.
const proofKey = "Dustpan-Proof"

// proofPrefix starts the line that holds the proof.
const proofPrefix = proofKey + ": "

// maxProofBytes is the longest proof, as text, that the adapter reads.
const maxProofBytes = 1024

// proofPurpose is the first part of each binding. It keeps a proof of a commit
// from opening for any other use of the same key. Version 2 binds the branch
// and the digest of the changes too, so a proof of version 1 does not open.
const proofPurpose = "dustpan.forge.commit-proof.v2"

// proofLengthBase is the base of the length before each part of a binding.
const proofLengthBase = 10

// proofBinding returns the value that a proof binds: the repository, the
// trailer line, the base commit, the branch and the digest of the changes.
// Each part has its length before it, in decimal digits and then a colon, so
// the encoding alone keeps the parts apart.
//
// Defense in depth: checkCommit and checkFind refuse a part that is not valid
// before it reaches the binding. The length prefix stays so that the encoding
// does not rely on them.
func proofBinding(repoID string, d forge.Delivery) []byte {
	var b []byte

	for _, p := range []string{proofPurpose, repoID, d.Trailer, d.Base, d.Branch, d.Changes} {
		b = strconv.AppendInt(b, int64(len(p)), proofLengthBase)
		b = append(b, ':')
		b = append(b, p...)
	}

	return b
}

// proofLine returns the trailer line that holds proof.
func proofLine(proof []byte) string {
	return proofPrefix + base64.RawURLEncoding.EncodeToString(proof)
}

// proofOf returns the proof in the last paragraph of message. It returns false
// when the paragraph has no line with the key of the proof, has more than one,
// or has one that is not valid base64 text of a length that the adapter reads.
// The message is host data that is not trusted.
func proofOf(message string) ([]byte, bool) {
	message = strings.TrimRight(strings.ReplaceAll(message, "\r\n", "\n"), " \t\n")

	_, last, found := strings.CutLast(message, "\n\n")
	if !found {
		return nil, false
	}

	var text string

	count := 0

	for line := range strings.SplitSeq(last, "\n") {
		if rest, ok := strings.CutPrefix(line, proofPrefix); ok {
			text, count = rest, count+1
		}
	}

	if count != 1 || text == "" || len(text) > maxProofBytes {
		return nil, false
	}

	proof, err := base64.RawURLEncoding.Strict().DecodeString(text)
	if err != nil {
		return nil, false
	}

	return proof, true
}

// hasProofKey reports whether any line of text starts with the key of the proof,
// in any letter case. The adapter writes the proof, so a request or a trailer
// that carries such a line is not valid.
func hasProofKey(text string) bool {
	for line := range strings.SplitSeq(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.ToLower(line), strings.ToLower(proofKey)+":") {
			return true
		}
	}

	return false
}

// repositoryID returns the ID of the repository. A rename keeps the ID, so a
// proof binds the ID and not the name.
func (f *gitHub) repositoryID(ctx context.Context, r repoName, bearer string) (string, error) {
	resp, err := f.send(ctx, call{
		method: http.MethodGet, path: r.path(), bearer: bearer, body: nil, status: f.commitStatus, oversize: nil,
		target: "",
	})
	if err != nil {
		return "", err
	}

	var repo struct {
		ID json.Number `json:"id"`
	}

	if decodeErr := decode(resp.body, &repo); decodeErr != nil {
		return "", decodeErr
	}

	if !validID(repo.ID.String()) {
		return "", forge.Unavailable(0, "GitHub gave a repository with no ID")
	}

	return repo.ID.String(), nil
}

// delivery is what FindCommit looks for, and what a proof binds.
type delivery struct {
	forge.Delivery

	repo   repoName
	bearer string
}

// checkProof requires that the commit c has a proof that opens for the
// trailer, the base commit, the branch, the digest of the changes and the
// repository of d. It is its own layer: it
// stops a commit that passes the checks of trustedCommit, and it does not rely
// on them. A proof that does not open is forge.ErrUntrustedCommit. A Prover
// that cannot decide gives forge.ErrUnavailable and no commit counts.
func (f *gitHub) checkProof(ctx context.Context, d delivery, c listedCommit) error {
	// Defense in depth: commitAllowed refuses a connection with no Prover. This
	// check stays so that a new caller cannot skip the proof.
	if !canProve(f.o.prover) {
		return fmt.Errorf("%w: the connection has no key to open the proof", forge.ErrUnsupported)
	}

	proof, ok := proofOf(c.Commit.Message)
	if !ok {
		return untrusted(c, "has no valid proof")
	}

	repoID, err := f.repositoryID(ctx, d.repo, d.bearer)
	if err != nil {
		return err
	}

	opens, err := f.o.prover.Open(ctx, proofBinding(repoID, d.Delivery), proof)
	if err != nil {
		return forge.Unavailable(0, "the proof of a commit cannot be checked")
	}

	if !opens {
		return untrusted(c,
			"has a proof that does not open for this trailer, base commit, branch, change set and repository")
	}

	return nil
}
