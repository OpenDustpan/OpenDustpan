package runner_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/runner"
)

func TestJoinTTL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   time.Duration
		want time.Duration
		err  error
	}{
		{0, time.Hour, nil},
		{30 * time.Minute, 30 * time.Minute, nil},
		{7 * 24 * time.Hour, 7 * 24 * time.Hour, nil},
		{7*24*time.Hour + time.Second, 0, runner.ErrInvalid},
		{-time.Second, 0, runner.ErrInvalid},
	}

	for _, tc := range cases {
		got, err := runner.JoinTTL(tc.in)
		if got != tc.want || !errors.Is(err, tc.err) || (tc.err == nil && err != nil) {
			t.Errorf("JoinTTL(%v) = %v, %v, want %v, %v", tc.in, got, err, tc.want, tc.err)
		}
	}
}

func TestJoinTokenIsSingleUse(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		change func(j *runner.JoinToken)
		want   error
	}{
		{"open", func(*runner.JoinToken) {}, nil},
		{"used", func(j *runner.JoinToken) { j.UsedAt = now.Add(-time.Minute) }, runner.ErrTokenUsed},
		{"revoked", func(j *runner.JoinToken) { j.RevokedAt = now.Add(-time.Minute) }, runner.ErrTokenUsed},
		{"at expiry", func(j *runner.JoinToken) { j.ExpiresAt = now }, runner.ErrTokenExpired},
		{"after expiry", func(j *runner.JoinToken) { j.ExpiresAt = now.Add(-time.Second) }, runner.ErrTokenExpired},
	}

	for _, tc := range cases {
		j := runner.JoinToken{
			ID: "rjt-1", Pool: "rp-1", SHA256: "", CreatedBy: "alice", CreatedAt: now.Add(-time.Minute),
			ExpiresAt: now.Add(time.Hour), UsedAt: time.Time{}, UsedBy: "", RevokedAt: time.Time{},
		}
		tc.change(&j)

		err := j.CheckUse(now)
		if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
			t.Errorf("%s: CheckUse = %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestHandoffTokenIsSingleUse(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	open := runner.HandoffToken{
		SHA256: "", Operation: "op-1", Token: 1, Runner: "rn-1", IssuedAt: now,
		ExpiresAt: now.Add(runner.HandoffTokenTTL), UsedAt: time.Time{},
	}

	if err := open.CheckUse(now); err != nil {
		t.Fatalf("CheckUse of an open token = %v", err)
	}

	used := open
	used.UsedAt = now

	if err := used.CheckUse(now); !errors.Is(err, runner.ErrTokenUsed) {
		t.Fatalf("CheckUse of a used token = %v, want ErrTokenUsed", err)
	}

	if err := open.CheckUse(now.Add(runner.HandoffTokenTTL)); !errors.Is(err, runner.ErrTokenExpired) {
		t.Fatalf("CheckUse at expiry = %v, want ErrTokenExpired", err)
	}
}

func TestKeyRotationAndExpiry(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	k := runner.Key{Public: make([]byte, ed25519.PublicKeySize), CreatedAt: created}

	cases := []struct {
		age     time.Duration
		rotate  bool
		expired bool
	}{
		{0, false, false},
		{24 * 24 * time.Hour, false, false},
		{25*24*time.Hour + time.Second, true, false},
		{30 * 24 * time.Hour, true, false},
		{30*24*time.Hour + time.Second, true, true},
	}

	for _, tc := range cases {
		now := created.Add(tc.age)
		if k.RotationDue(now) != tc.rotate || k.ExpiredAt(now) != tc.expired {
			t.Errorf("age %v: rotate %v expired %v, want %v %v",
				tc.age, k.RotationDue(now), k.ExpiredAt(now), tc.rotate, tc.expired)
		}
	}

	if err := (runner.Key{Public: []byte("short"), CreatedAt: created}).Validate(); !errors.Is(err, runner.ErrInvalid) {
		t.Errorf("Validate of a short key = %v, want ErrInvalid", err)
	}

	if len(k.ID()) != 64 {
		t.Errorf("ID has %d characters, want 64", len(k.ID()))
	}
}

func TestCheckContentHash(t *testing.T) {
	t.Parallel()

	sum := sha256.Sum256([]byte("chunk"))
	other := sha256.Sum256([]byte("other"))

	if err := runner.CheckContentHash(sum[:], sum[:]); err != nil {
		t.Errorf("CheckContentHash of the signed hash = %v", err)
	}

	if err := runner.CheckContentHash(sum[:], other[:]); !errors.Is(err, runner.ErrBadProof) {
		t.Errorf("CheckContentHash of another body = %v, want ErrBadProof", err)
	}

	if err := runner.CheckContentHash(nil, nil); !errors.Is(err, runner.ErrBadProof) {
		t.Errorf("CheckContentHash with no signed hash = %v, want ErrBadProof", err)
	}

	p := runner.Proof{Subject: "rn-1", At: time.Unix(1_700_000_000, 0), Nonce: "", Signature: nil}
	if got := runner.NonceExpiry(p); !got.Equal(p.At.Add(runner.ProofWindow + runner.MaxClockSkew)) {
		t.Errorf("NonceExpiry = %v, want the time of the proof plus the proof window and the clock skew", got)
	}
}

func TestCheckRotation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	old, _ := newKey(t, now.Add(-runner.RotateKeyAfter))
	cur, _ := newKey(t, now)
	next, _ := newKey(t, now)
	r := runner.Runner{
		ID:          "rn-1",
		Pool:        "rp-1",
		Namespace:   "acme",
		Name:        "cluster-a",
		Kind:        runner.KindKubernetes,
		Isolation:   runner.IsolationKubernetes,
		Labels:      nil,
		Release:     runner.Release{Major: 0, Minor: 14, Patch: 0, Pre: ""},
		Key:         cur,
		PreviousKey: old,
		RotatedAt:   now,
		JoinedWith:  "rjt-1",
		Draining:    false,
		RetiredAt:   time.Time{},
		LastSeenAt:  now,
		JoinedAt:    now,
	}

	cases := []struct {
		name   string
		signer runner.Key
		next   runner.Key
		at     time.Time
		rotate bool
		ok     bool
	}{
		{"current key to a new key", cur, next, now, true, true},
		{"current key to itself", cur, cur, now, false, true},
		{"previous key repeats the rotation", old, cur, now.Add(runner.RotateGrace), false, true},
		{"previous key to a new key", old, next, now.Add(time.Second), false, false},
		{"previous key after the grace window", old, cur, now.Add(runner.RotateGrace + time.Millisecond), false, false},
		{"unknown key", next, next, now, false, false},
	}

	for _, tc := range cases {
		rotate, err := r.CheckRotation(tc.signer.ID(), tc.next, tc.at)
		if rotate != tc.rotate || tc.ok != (err == nil) || (err != nil && !errors.Is(err, runner.ErrBadProof)) {
			t.Errorf("%s: CheckRotation = %v, %v, want %v ok %v", tc.name, rotate, err, tc.rotate, tc.ok)
		}
	}
}

func TestCheckProof(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	key, priv := newKey(t, now)
	other, _ := newKey(t, now)
	body := sha256.Sum256([]byte(`{"free":1}`))
	req := runner.Signed{Method: "POST", PathQuery: "/runner/v1/heartbeat", BodySHA256: body[:]}

	sign := func(p runner.Proof, r runner.Signed) runner.Proof {
		p.Signature = ed25519.Sign(priv, runner.ProofInput(p, r))

		return p
	}
	good := sign(runner.Proof{
		Subject: "rn-1", At: now, Nonce: "00112233445566778899aabbccddeeff", Signature: nil,
	}, req)

	otherBody := sha256.Sum256([]byte(`{"free":2}`))

	cases := []struct {
		name string
		key  runner.Key
		p    runner.Proof
		r    runner.Signed
		now  time.Time
		ok   bool
	}{
		{"valid", key, good, req, now, true},
		{"clock ahead within window", key, good, req, now.Add(runner.ProofWindow), true},
		{"too old", key, good, req, now.Add(runner.ProofWindow + time.Millisecond), false},
		{"from the future", key, good, req, now.Add(-runner.ProofWindow - time.Millisecond), false},
		{"other key", other, good, req, now, false},
		{
			"other body",
			key,
			good,
			runner.Signed{Method: "POST", PathQuery: req.PathQuery, BodySHA256: otherBody[:]},
			now,
			false,
		},
		{
			"other path",
			key,
			good,
			runner.Signed{Method: "POST", PathQuery: "/runner/v1/poll", BodySHA256: body[:]},
			now,
			false,
		},
		{
			"other method",
			key,
			good,
			runner.Signed{Method: "PUT", PathQuery: req.PathQuery, BodySHA256: body[:]},
			now,
			false,
		},
		{
			"other subject",
			key,
			runner.Proof{Subject: "rn-2", At: good.At, Nonce: good.Nonce, Signature: good.Signature},
			req,
			now,
			false,
		},
		{
			"short nonce",
			key,
			sign(runner.Proof{Subject: "rn-1", At: now, Nonce: "0011", Signature: nil}, req),
			req,
			now,
			false,
		},
		{"bad key", runner.Key{Public: []byte("x"), CreatedAt: now}, good, req, now, false},
	}

	for _, tc := range cases {
		err := runner.CheckProof(tc.key, tc.p, tc.r, tc.now)
		if tc.ok != (err == nil) || (err != nil && !errors.Is(err, runner.ErrBadProof)) {
			t.Errorf("%s: CheckProof = %v, want ok %v", tc.name, err, tc.ok)
		}
	}
}

func TestChallengesBindTheirContext(t *testing.T) {
	t.Parallel()

	a := runner.JoinChallenge("https://dustpan.example.com", "rp-1", "cluster-a", "dpj_x")

	for _, b := range [][]byte{
		runner.JoinChallenge("https://other.example.com", "rp-1", "cluster-a", "dpj_x"),
		runner.JoinChallenge("https://dustpan.example.com", "rp-2", "cluster-a", "dpj_x"),
		runner.JoinChallenge("https://dustpan.example.com", "rp-1", "cluster-b", "dpj_x"),
		runner.JoinChallenge("https://dustpan.example.com", "rp-1", "cluster-a", "dpj_y"),
	} {
		if bytes.Equal(a, b) {
			t.Fatalf("two join challenges are equal: %q", a)
		}
	}

	if strings.Contains(string(a), "dpj_x") {
		t.Fatal("a join challenge holds the join token")
	}

	k1, _ := newKey(t, time.Time{})
	k2, _ := newKey(t, time.Time{})

	if bytes.Equal(runner.RotateChallenge("rn-1", k1), runner.RotateChallenge("rn-1", k2)) {
		t.Fatal("rotate challenges of two keys are equal")
	}

	s := runner.SessionChallenge("https://dustpan.example.com", "dph_x", k1)
	for _, other := range [][]byte{
		runner.SessionChallenge("https://other.example.com", "dph_x", k1),
		runner.SessionChallenge("https://dustpan.example.com", "dph_y", k1),
		runner.SessionChallenge("https://dustpan.example.com", "dph_x", k2),
	} {
		if bytes.Equal(s, other) {
			t.Fatalf("two session challenges are equal: %q", s)
		}
	}

	if strings.Contains(string(s), "dph_x") {
		t.Fatal("a session challenge holds the handoff token")
	}
}

func TestRejoinArgsReadTheTokenFromStdin(t *testing.T) {
	t.Parallel()

	got := runner.RejoinArgs("dustpan-runner", "https://dustpan.example.com", "rp-1", "cluster-a")
	want := []string{
		"dustpan-runner", "join", "--server", "https://dustpan.example.com", "--pool", "rp-1", "--name", "cluster-a",
		"--token-stdin",
	}

	if !slices.Equal(got, want) {
		t.Fatalf("RejoinArgs = %q, want %q", got, want)
	}
}
