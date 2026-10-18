package runner_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
)

// namespaceName converts s for the calls of the tests.
func namespaceName(s string) namespace.Name {
	return namespace.Name(s)
}

// newKey makes an Ed25519 key pair created at at.
func newKey(t *testing.T, at time.Time) (runner.Key, ed25519.PrivateKey) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	return runner.Key{Public: pub, CreatedAt: at}, priv
}

// member returns an online Kubernetes runner seen at now, with a key
// created at now.
func member(now time.Time) runner.Runner {
	return runner.Runner{
		ID: "rn-1", Pool: "rp-1", Namespace: "acme", Name: "cluster-a", Kind: runner.KindKubernetes,
		Isolation: runner.IsolationKubernetes, Labels: nil, Release: runner.Release{Major: 0, Minor: 14, Patch: 0},
		Key:        runner.Key{Public: make([]byte, ed25519.PublicKeySize), CreatedAt: now},
		JoinedWith: "rjt-1", Draining: false, RetiredAt: time.Time{}, LastSeenAt: now, JoinedAt: now,
	}
}

// joinRequest returns a valid join request with a new key.
func joinRequest(t *testing.T) runner.JoinRequest {
	t.Helper()

	k, _ := newKey(t, time.Time{})

	return runner.JoinRequest{
		Token: runner.JoinTokenPrefix + "secret", Name: "cluster-a", Kind: runner.KindKubernetes,
		Isolation: runner.IsolationKubernetes, Labels: map[string]string{"region": "eu-west-1"},
		Release: runner.Release{Major: 0, Minor: 14, Patch: 0}, Key: k, Proof: nil,
	}
}
