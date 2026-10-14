package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
)

// writeSigningKey writes a base64 seed of n bytes to a file with mode and
// answers its path.
func writeSigningKey(t *testing.T, n int, mode os.FileMode) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "audit-signing.key")
	raw := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, n)) + "\n"

	if err := os.WriteFile(path, []byte(raw), mode); err != nil {
		t.Fatalf("write signing key: %v", err)
	}

	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod signing key: %v", err)
	}

	return path
}

func TestReadSigningKeyFile(t *testing.T) {
	t.Parallel()

	signer, err := readSigningKeyFile(writeSigningKey(t, 32, 0o600))
	if err != nil {
		t.Fatalf("read a valid key: %v", err)
	}

	if signer.KeyID() == "" {
		t.Fatal("read a valid key: no key ID")
	}

	if _, err := readSigningKeyFile(writeSigningKey(t, 32, 0o644)); !errors.Is(err, errKeyFilePermissions) {
		t.Fatalf("read a key that others can read: got %v, want errKeyFilePermissions", err)
	}

	if _, err := readSigningKeyFile(writeSigningKey(t, 16, 0o600)); !errors.Is(err, audit.ErrInvalidSigningKey) {
		t.Fatalf("read a 16-byte key: got %v, want ErrInvalidSigningKey", err)
	}

	if _, err := readSigningKeyFile(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("read a missing key file: no error")
	}
}

func TestAddVerifyKeys(t *testing.T) {
	t.Parallel()

	signer, err := readSigningKeyFile(writeSigningKey(t, 32, 0o600))
	if err != nil {
		t.Fatalf("read signing key: %v", err)
	}

	old, err := audit.NewSigner(bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatalf("old signer: %v", err)
	}

	dir := t.TempDir()
	good := filepath.Join(dir, "verify-keys")
	content := "# the key before the change\n\n" + base64.StdEncoding.EncodeToString(old.PublicKey()) + "\n"

	if err := os.WriteFile(good, []byte(content), 0o600); err != nil {
		t.Fatalf("write verify keys: %v", err)
	}

	if err := addVerifyKeys(signer, good); err != nil {
		t.Fatalf("add verify keys: %v", err)
	}

	cp := old.Sign(audit.Checkpoint{
		At: time.Unix(1, 0), Scope: "acme", Hash: "abc", KeyID: "", Signature: nil, Sequence: 1,
	})
	if err := signer.Verify(cp); err != nil {
		t.Fatalf("verify a checkpoint of the old key: %v", err)
	}

	bad := filepath.Join(dir, "bad-keys")
	if err := os.WriteFile(bad, []byte("not a key\n"), 0o600); err != nil {
		t.Fatalf("write bad keys: %v", err)
	}

	if err := addVerifyKeys(signer, bad); !errors.Is(err, audit.ErrInvalidVerifyKey) {
		t.Fatalf("add a bad key: got %v, want ErrInvalidVerifyKey", err)
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestServeCheckpointFlags(t *testing.T) {
	key := writeSigningKey(t, 32, 0o600)

	cases := []struct {
		want error
		name string
		args []string
	}{
		{name: "no key", want: errCheckpointsNeedKey, args: []string{"--audit-chain", "on", "--audit-checkpoints"}},
		{
			name: "a key without checkpoints", want: errKeyWithoutCheckpoints,
			args: []string{"--audit-signing-key-file", key},
		},
		{
			name: "the chain on auto", want: errCheckpointsNeedChain,
			args: []string{"--audit-checkpoints", "--audit-signing-key-file", key},
		},
		{
			name: "verify keys without checkpoints", want: errVerifyKeysWithoutCheckpoints,
			args: []string{"--audit-verify-keys-file", key},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { //nolint:paralleltest // The subtest sets process environment.
			setServeEnv(t, testKeyBase64())

			args := append([]string{"--database", "sqlite:" + filepath.Join(t.TempDir(), "dustpan.db")}, tc.args...)
			if err := serve(t.Context(), args, io.Discard); !errors.Is(err, tc.want) {
				t.Fatalf("serve: got %v, want %v", err, tc.want)
			}
		})
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestServeWithAuditCheckpoints(t *testing.T) {
	setServeEnv(t, testKeyBase64())

	dir := t.TempDir()

	err := startServer(t,
		"--database", "sqlite:"+filepath.Join(dir, "dustpan.db"),
		"--listen", "127.0.0.1:0",
		"--audit-chain", "on",
		"--audit-checkpoints",
		"--audit-signing-key-file", writeSigningKey(t, 32, 0o600),
	)
	if err != nil {
		t.Fatalf("serve with audit checkpoints: %v", err)
	}
}
