package github_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/forge/github"
)

// proofTrailer is the trailer of the commits of the tests of the proof.
const proofTrailer = "Dustpan-Job: j-1"

// findWithProof answers FindCommit of proofTrailer on rawBase with one commit of
// the connection (the right author, committer and verification) whose message is
// message as it is. opts has the answers of the server and the Prover.
func findWithProof(t *testing.T, opts rawOptions, message string) (forge.Commit, error) {
	t.Helper()

	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	item := map[string]any{
		"sha": shaN(1),
		"commit": map[string]any{
			"message":      message,
			"committer":    map[string]any{"date": since.Add(time.Minute).UTC().Format(time.RFC3339)},
			"verification": map[string]any{"verified": true},
		},
		"author":    login(rawIdentity),
		"committer": login("web-flow"),
	}

	data, err := json.Marshal([]map[string]any{item})
	if err != nil {
		t.Fatal(err)
	}

	fg := rawForgeWith(
		t,
		"",
		opts,
		func(w http.ResponseWriter, _ *http.Request) { answer(w, http.StatusOK, string(data)) },
	)

	return fg.FindCommit(t.Context(), "acme/infra", "main", rawDelivery(proofTrailer, rawBase), since)
}

// goodOptions are the options of a server that answers as GitHub does, with
// the Prover of the tests.
func goodOptions() rawOptions {
	return rawOptions{
		userStatus: http.StatusOK, userBody: `{"id":777,"login":"` + rawIdentity + `"}`,
		repoStatus: http.StatusOK, repoBody: `{"id":` + rawRepoID + `}`, prover: testProver{},
	}
}

// TestFindCommitNeedsTheProof gives a commit of the connection (the author, the
// committer and the verification are right) with each kind of proof. The checks
// of trustedCommit pass for every row, so only the proof can stop a row.
func TestFindCommitNeedsTheProof(t *testing.T) {
	t.Parallel()

	good := testProofLine(rawRepoID, proofTrailer, rawBase)
	block := "Fix\n\n" + proofTrailer

	// Each of these differs from the delivery of the search in one part only.
	otherBranch := rawDelivery(proofTrailer, rawBase)
	otherBranch.Branch = "dustpan/j-1"
	otherChanges := rawDelivery(proofTrailer, rawBase)
	otherChanges.Changes = "sha256:bb02d69ebd4333ea48437b256c9fd03cf708e30f48eca93c7450b0f0a6b735cf"

	tests := []struct {
		name    string
		message string
		wantErr error
	}{
		{"a good proof", block + "\n" + good, nil},
		{"a good proof before the trailer", "Fix\n\n" + good + "\n" + proofTrailer, nil},
		{"no proof", block, forge.ErrUntrustedCommit},
		{
			"the proof of another repository",
			block + "\n" + testProofLine("700002", proofTrailer, rawBase), forge.ErrUntrustedCommit,
		},
		{
			"the proof of another trailer",
			block + "\n" + testProofLine(rawRepoID, "Dustpan-Job: j-2", rawBase), forge.ErrUntrustedCommit,
		},
		{
			"the proof of another base commit",
			block + "\n" + testProofLine(rawRepoID, proofTrailer, rawCommit), forge.ErrUntrustedCommit,
		},
		{
			"the proof of another branch",
			block + "\n" + testProofLineOf(rawRepoID, otherBranch), forge.ErrUntrustedCommit,
		},
		{
			"the proof of other changes",
			block + "\n" + testProofLineOf(rawRepoID, otherChanges), forge.ErrUntrustedCommit,
		},
		{"a proof of the previous form", block + "\n" + previousProofLine(), forge.ErrUntrustedCommit},
		{"two proofs that are good", block + "\n" + good + "\n" + good, forge.ErrUntrustedCommit},
		{"a proof that is not base64", block + "\nDustpan-Proof: !!!!", forge.ErrUntrustedCommit},
		{"an empty proof", block + "\nDustpan-Proof: ", forge.ErrUntrustedCommit},
		{"a good proof with padding", block + "\n" + good + "=", forge.ErrUntrustedCommit},
		// The space at the end of the message is not part of the message.
		{"a good proof and a space at the end of the message", block + "\n" + good + " ", nil},
		{"a proof that is too long", block + "\nDustpan-Proof: " + strings.Repeat("A", 2000), forge.ErrUntrustedCommit},
		{
			"the key of the proof in other letters",
			block + "\n" + strings.Replace(good, "Dustpan", "dustpan", 1),
			forge.ErrUntrustedCommit,
		},
		{"a good proof in another paragraph", "Fix\n\n" + good + "\n\n" + proofTrailer, forge.ErrUntrustedCommit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := findWithProof(t, goodOptions(), tt.message)

			want := ""
			if tt.wantErr == nil {
				want = shaN(1)
			}

			wantFound(t, got, err, want, tt.wantErr)
		})
	}
}

// previousProofLine returns the proof line of the previous form of the
// binding, which bound only the repository, the trailer and the base commit,
// for the delivery of the search. A proof of that form must not open.
func previousProofLine() string {
	binding := "dustpan.forge.commit-proof.v1\x00" + rawRepoID + "\x00" + proofTrailer + "\x00" + rawBase

	return "Dustpan-Proof: " + base64.RawURLEncoding.EncodeToString(testMAC([]byte(binding)))
}

// errKeyStore is the error of failingProver.
var errKeyStore = errors.New("the key store does not answer")

// failingProver is ready, and cannot decide.
type failingProver struct{}

func (failingProver) Ready() bool { return true }

func (failingProver) Seal(context.Context, []byte) ([]byte, error) { return nil, errKeyStore }

func (failingProver) Open(context.Context, []byte, []byte) (bool, error) { return false, errKeyStore }

// TestFindCommitFailsClosedWhenTheProofCannotBeChecked gives a good proof and a
// connection that cannot check it: no key, a Prover that cannot decide, and a
// repository whose ID cannot be read.
func TestFindCommitFailsClosedWhenTheProofCannotBeChecked(t *testing.T) {
	t.Parallel()

	message := "Fix\n\n" + proofTrailer + "\n" + testProofLine(rawRepoID, proofTrailer, rawBase)

	tests := []struct {
		name    string
		change  func(*rawOptions)
		wantErr error
	}{
		{"no Prover", func(o *rawOptions) { o.prover = nil }, forge.ErrUnsupported},
		{"a Prover that cannot decide", func(o *rawOptions) { o.prover = failingProver{} }, forge.ErrUnavailable},
		{
			"a repository that GitHub does not have",
			func(o *rawOptions) { o.repoStatus, o.repoBody = http.StatusNotFound, `{}` },
			forge.ErrNotFound,
		},
		{
			"a repository answer that does not parse",
			func(o *rawOptions) { o.repoBody = `not json` },
			forge.ErrUnavailable,
		},
		{"a repository with no ID", func(o *rawOptions) { o.repoBody = `{}` }, forge.ErrUnavailable},
		{
			"a repository ID that is not a number",
			func(o *rawOptions) { o.repoBody = `{"id":"abc"}` },
			forge.ErrUnavailable,
		},
		{
			"a repository server error",
			func(o *rawOptions) { o.repoStatus, o.repoBody = http.StatusServiceUnavailable, `{}` },
			forge.ErrUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := goodOptions()
			tt.change(&opts)

			got, err := findWithProof(t, opts, message)
			wantFound(t, got, err, "", tt.wantErr)
		})
	}
}

// unreadyProver has no key. Each Seal and Open fails, as the Prover of a server
// with no key-encryption key does.
type unreadyProver struct{}

func (unreadyProver) Ready() bool { return false }

func (unreadyProver) Seal(context.Context, []byte) ([]byte, error) { return nil, errKeyStore }

func (unreadyProver) Open(context.Context, []byte, []byte) (bool, error) { return false, errKeyStore }

// TestCommitNeedsAProverThatIsReady checks that a connection with no Prover, or
// with a Prover that has no key, does not list the capability to commit, and
// refuses Commit and FindCommit with no call. A Prover with a key lists it.
func TestCommitNeedsAProverThatIsReady(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		prover github.Prover
		want   bool
	}{
		{"no Prover", nil, false},
		{"a Prover that has no key", unreadyProver{}, false},
		{"a Prover that has its key", testProver{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := goodOptions()
			opts.prover = tt.prover

			calls := 0
			fg := rawForgeWith(t, "", opts, func(w http.ResponseWriter, _ *http.Request) {
				calls++

				answer(w, http.StatusInternalServerError, `{}`)
			})

			if got := fg.Capabilities().Has(forge.CapCommit); got != tt.want {
				t.Fatalf("CapCommit listed = %v, want %v", got, tt.want)
			}

			if !tt.want {
				refuseWithNoCall(t, fg)

				if calls != 0 {
					t.Fatalf("%d calls, want none", calls)
				}
			}
		})
	}
}

// refuseWithNoCall checks that Commit and FindCommit return ErrUnsupported.
func refuseWithNoCall(t *testing.T, fg rawClient) {
	t.Helper()

	_, commitErr := fg.Commit(t.Context(), "acme/infra", oneCommit(rawBase))
	if !errors.Is(commitErr, forge.ErrUnsupported) {
		t.Errorf("Commit = %v, want ErrUnsupported", commitErr)
	}

	d := rawDelivery(proofTrailer, rawBase)

	_, findErr := fg.FindCommit(t.Context(), "acme/infra", "main", d, time.Unix(0, 0))
	if !errors.Is(findErr, forge.ErrUnsupported) {
		t.Errorf("FindCommit = %v, want ErrUnsupported", findErr)
	}
}

// TestCommitFailsClosedWhenTheProofCannotBeSealed checks that a Prover that is
// ready but cannot seal gives ErrUnavailable, and that the adapter makes no
// mutation: a commit never leaves with no proof.
func TestCommitFailsClosedWhenTheProofCannotBeSealed(t *testing.T) {
	t.Parallel()

	opts := goodOptions()
	opts.prover = failingProver{}

	mutations := 0
	fg := rawForgeWith(t, "", opts, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/graphql" {
			mutations++
		}

		answer(w, http.StatusOK, goodAnswer())
	})

	if _, err := fg.Commit(t.Context(), "acme/infra", oneCommit(rawBase)); !errors.Is(err, forge.ErrUnavailable) {
		t.Fatalf("Commit = %v, want ErrUnavailable", err)
	}

	if mutations != 0 {
		t.Fatalf("%d mutations, want none", mutations)
	}
}

// TestCommitSealsTheProofForTheRequest checks the proof that Commit puts on the
// wire: it opens for the repository, the trailer and the base of the request,
// and for nothing else.
func TestCommitSealsTheProofForTheRequest(t *testing.T) {
	t.Parallel()

	rec := new(recorder)
	c := rawForge(t, "", rec.serve)

	if _, err := c.Commit(t.Context(), "acme/infra", oneCommit(rawBase)); err != nil {
		t.Fatal(err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()

	var proof string

	for line := range strings.SplitSeq(rec.in.Variables.Input.Message.Body, "\n") {
		if rest, ok := strings.CutPrefix(line, "Dustpan-Proof: "); ok {
			proof = rest
		}
	}

	want := strings.TrimPrefix(testProofLine(rawRepoID, proofTrailer, rawBase), "Dustpan-Proof: ")
	if proof != want {
		t.Fatalf("proof %q, want %q (the proof of the repository, the trailer and the base commit)", proof, want)
	}

	other := strings.TrimPrefix(testProofLine(rawRepoID, proofTrailer, rawCommit), "Dustpan-Proof: ")
	if proof == other {
		t.Fatal("the proof does not depend on the base commit")
	}

	otherBranch := rawDelivery(proofTrailer, rawBase)
	otherBranch.Branch = "dustpan/j-1"

	if proof == strings.TrimPrefix(testProofLineOf(rawRepoID, otherBranch), "Dustpan-Proof: ") {
		t.Fatal("the proof does not depend on the branch")
	}
}
